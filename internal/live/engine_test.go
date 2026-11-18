package live

import (
	"context"
	"testing"

	"github.com/sgnd/cuwan/internal/exchangex"
	"github.com/sgnd/cuwan/internal/store"
)

// book is a fake venue: orders stay open until the test fills them.
type book struct {
	price   float64
	open    map[string]exchangex.Order
	filled  map[string]bool
	pos     []exchangex.Position
	placed  []exchangex.PlaceReq
	cancels int
}

func newBook(p float64) *book {
	return &book{price: p, open: map[string]exchangex.Order{}, filled: map[string]bool{}}
}

func (b *book) Name() string   { return "fake" }
func (b *book) Market() string { return "usdm" }
func (b *book) Ticker(context.Context, string) (exchangex.Ticker, error) {
	return exchangex.Ticker{Price: b.price}, nil
}
func (b *book) Balances(context.Context) ([]exchangex.Balance, error)   { return nil, nil }
func (b *book) Positions(context.Context) ([]exchangex.Position, error) { return b.pos, nil }
func (b *book) OpenOrders(context.Context, string) ([]exchangex.Order, error) {
	var out []exchangex.Order
	for _, o := range b.open {
		out = append(out, o)
	}
	return out, nil
}
func (b *book) PlaceOrder(_ context.Context, r exchangex.PlaceReq) (exchangex.Order, error) {
	b.placed = append(b.placed, r)
	o := exchangex.Order{ID: "x" + r.ClientOID, ClientOID: r.ClientOID, Side: r.Side, Price: r.Price, Qty: r.Qty, Status: StatusOpen, ReduceOnly: r.ReduceOnly}
	b.open[o.ID] = o
	return o, nil
}
func (b *book) CancelOrder(_ context.Context, _, id, _ string) error {
	b.cancels++
	delete(b.open, id)
	return nil
}
func (b *book) SetLeverage(context.Context, string, float64) error { return nil }
func (b *book) Permissions(context.Context) (exchangex.Permissions, error) {
	return exchangex.Permissions{CanTrade: true}, nil
}

// fill removes an order from the book as filled.
func (b *book) fill(id string) { delete(b.open, id); b.filled[id] = true }

// statusBook adds OrderStatus, so the engine may trust fills.
type statusBook struct{ *book }

func (s statusBook) OrderStatus(_ context.Context, _, id, _ string) (exchangex.Order, error) {
	if s.filled[id] {
		return exchangex.Order{ID: id, Status: StatusFilled}, nil
	}
	return exchangex.Order{ID: id, Status: StatusCanceled}, nil
}

func seed(b *book, bot *store.Bot) *store.RuntimeState {
	rt := &store.RuntimeState{BotName: bot.Name, Mode: "live", Status: "running"}
	for _, p := range []struct {
		side string
		px   float64
	}{{"buy", 95}, {"sell", 105}} {
		o, _ := b.PlaceOrder(context.Background(), exchangex.PlaceReq{Side: p.side, Price: p.px, Qty: 1, ClientOID: p.side})
		rt.Orders = append(rt.Orders, store.RuntimeOrder{ClientOID: o.ClientOID, ExchangeID: o.ID, Side: p.side, Price: p.px, Qty: 1, Status: StatusOpen})
	}
	b.placed = nil
	return rt
}

var gridBot = &store.Bot{Name: "g", Symbol: "S", Lower: 90, Upper: 110, Levels: 5}

func TestConfirmedFillPlacesOppositeOneLevelAway(t *testing.T) {
	b := newBook(100)
	rt := seed(b, gridBot)
	ex := statusBook{b}
	if stop, err := Step(context.Background(), gridBot, ex, rt); stop || err != nil || len(b.placed) != 0 {
		t.Fatalf("no fill yet: stop=%v err=%v placed=%v", stop, err, b.placed)
	}
	b.fill("xbuy")
	if _, err := Step(context.Background(), gridBot, ex, rt); err != nil {
		t.Fatal(err)
	}
	if len(b.placed) != 1 || b.placed[0].Side != "sell" || b.placed[0].Price != 100 {
		t.Fatalf("placed %+v", b.placed)
	}
	if rt.Orders[0].Status != StatusFilled {
		t.Fatalf("runtime %+v", rt.Orders[0])
	}
	// A second step must not place again for the same fill.
	if _, err := Step(context.Background(), gridBot, ex, rt); err != nil || len(b.placed) != 1 {
		t.Fatalf("duplicate placement: %v", b.placed)
	}
}

func TestCanceledOutsideIsNotTreatedAsFill(t *testing.T) {
	b := newBook(100)
	rt := seed(b, gridBot)
	delete(b.open, "xsell") // canceled by hand on the venue
	if _, err := Step(context.Background(), gridBot, statusBook{b}, rt); err != nil {
		t.Fatal(err)
	}
	if len(b.placed) != 0 || rt.Orders[1].Status != StatusCanceled {
		t.Fatalf("placed=%v status=%s", b.placed, rt.Orders[1].Status)
	}
}

func TestWithoutStatusDriverNothingIsPlaced(t *testing.T) {
	b := newBook(100)
	rt := seed(b, gridBot)
	b.fill("xbuy")
	if _, err := Step(context.Background(), gridBot, b, rt); err != nil {
		t.Fatal(err)
	}
	if len(b.placed) != 0 || rt.Orders[0].Status != StatusUnknown {
		t.Fatalf("placed=%v status=%s", b.placed, rt.Orders[0].Status)
	}
}

func TestPreventiveCloseFlattens(t *testing.T) {
	b := newBook(100)
	bot := *gridBot
	bot.Leverage = 20
	rt := seed(b, &bot)
	b.pos = []exchangex.Position{{Symbol: "S", Side: "long", Size: 2, EntryPrice: 100}}
	if stop, _ := Step(context.Background(), &bot, statusBook{b}, rt); stop {
		t.Fatal("fired at entry")
	}
	b.price = 95.1 // 20x long from 100: liq 95; past 95% of the path
	stop, err := Step(context.Background(), &bot, statusBook{b}, rt)
	if err != nil || !stop || rt.Status != "stopped" || rt.Meta["preventive"] != "fired" {
		t.Fatalf("stop=%v err=%v rt=%+v", stop, err, rt)
	}
	if b.cancels != 2 || len(b.placed) != 1 {
		t.Fatalf("cancels=%d placed=%v", b.cancels, b.placed)
	}
	c := b.placed[0]
	if !c.ReduceOnly || c.Side != "sell" || c.Qty != 2 || c.Price >= 95.1 {
		t.Fatalf("close order %+v", c)
	}
}

func TestFlattenShort(t *testing.T) {
	b := newBook(100)
	rt := seed(b, gridBot)
	b.pos = []exchangex.Position{{Symbol: "S", Side: "short", Size: 3, EntryPrice: 100}, {Symbol: "OTHER", Size: 9}}
	if err := Flatten(context.Background(), gridBot, b, rt, 100); err != nil {
		t.Fatal(err)
	}
	if len(b.placed) != 1 || b.placed[0].Side != "buy" || !b.placed[0].ReduceOnly || b.placed[0].Price <= 100 {
		t.Fatalf("%+v", b.placed)
	}
}
