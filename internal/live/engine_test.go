package live

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/sgnd/cuwan/internal/exchangex"
	"github.com/sgnd/cuwan/internal/store"
)

// book is a fake venue keyed by client order ID.
type book struct {
	price     float64
	orders    map[string]*exchangex.Order // every order the venue got
	pos       []exchangex.Position
	placed    []exchangex.PlaceReq
	cancels   int
	placeErr  func(exchangex.PlaceReq) (reached bool, err error)
	noClose   bool    // closes fill nothing
	closeFill float64 // fraction of each close that fills (default all)
	posErr    error
	rules     exchangex.SymbolRules
	n         int
}

func newBook(p float64) *book {
	return &book{price: p, orders: map[string]*exchangex.Order{}, rules: exchangex.SymbolRules{Tick: 0.01, Step: 0.001, MinQty: 0.001}}
}

func (b *book) Name() string   { return "fake" }
func (b *book) Market() string { return "usdm" }
func (b *book) Ticker(context.Context, string) (exchangex.Ticker, error) {
	return exchangex.Ticker{Price: b.price}, nil
}
func (b *book) Balances(context.Context) ([]exchangex.Balance, error) { return nil, nil }
func (b *book) Positions(context.Context) ([]exchangex.Position, error) {
	return b.pos, b.posErr
}
func (b *book) OpenOrders(context.Context, string) ([]exchangex.Order, error) {
	var out []exchangex.Order
	for _, o := range b.orders {
		if o.Status == StatusOpen {
			out = append(out, *o)
		}
	}
	return out, nil
}
func (b *book) PlaceOrder(_ context.Context, r exchangex.PlaceReq) (exchangex.Order, error) {
	b.placed = append(b.placed, r)
	reached, err := true, error(nil)
	if b.placeErr != nil {
		reached, err = b.placeErr(r)
	}
	if !reached {
		return exchangex.Order{}, err
	}
	b.n++
	o := &exchangex.Order{ID: fmt.Sprint(b.n), ClientOID: r.ClientOID, Side: r.Side, Price: r.Price, Qty: r.Qty, Status: StatusOpen, ReduceOnly: r.ReduceOnly}
	if r.ReduceOnly {
		frac := 1.0
		if b.noClose {
			frac = 0
		} else if b.closeFill > 0 {
			frac = b.closeFill
		}
		o.Filled = r.Qty * frac
		o.Status = StatusFilled
		if frac < 1 {
			o.Status = StatusCanceled // IOC: the rest is cancelled
		}
		b.shrink(o.Filled)
	}
	b.orders[r.ClientOID] = o
	return *o, err
}
func (b *book) CancelOrder(_ context.Context, _, _, cid string) error {
	b.cancels++
	if o, ok := b.orders[cid]; ok && o.Status == StatusOpen {
		o.Status = StatusCanceled
	}
	return nil
}
func (b *book) SetLeverage(context.Context, string, float64) error { return nil }
func (b *book) Permissions(context.Context) (exchangex.Permissions, error) {
	return exchangex.Permissions{CanTrade: true}, nil
}
func (b *book) OrderStatus(_ context.Context, _, id, cid string) (exchangex.Order, error) {
	for _, o := range b.orders {
		if (cid != "" && o.ClientOID == cid) || (id != "" && o.ID == id) {
			return *o, nil
		}
	}
	return exchangex.Order{}, fmt.Errorf("fake: %w", exchangex.ErrOrderNotFound)
}

// shrink reduces the first position by q, as a filled close would.
func (b *book) shrink(q float64) {
	if len(b.pos) == 0 {
		return
	}
	if b.pos[0].Size > 0 {
		b.pos[0].Size -= q
	} else {
		b.pos[0].Size += q
	}
}

// fill fills the venue order at price px.
func (b *book) fill(t *testing.T, side string, px float64) {
	t.Helper()
	for _, o := range b.orders {
		if o.Side == side && o.Price == px && o.Status == StatusOpen {
			o.Status, o.Filled = StatusFilled, o.Qty
			return
		}
	}
	t.Fatalf("no open %s at %g", side, px)
}

// noStatus hides OrderStatus.
type noStatus struct{ exchangex.Exchange }

var gridBot = &store.Bot{Name: "g", Symbol: "S", Lower: 90, Upper: 110, Levels: 5, OrderSize: 1}

// start places the grid (90 95 | 105 110 around 100; 100 is a sell).
func start(t *testing.T, b *book, bot *store.Bot) (*store.RuntimeState, Saver, *[]string) {
	t.Helper()
	rt := &store.RuntimeState{BotName: bot.Name, Mode: "live", Status: "running"}
	var log []string
	save := func() error {
		last := rt.Orders[len(rt.Orders)-1]
		log = append(log, fmt.Sprintf("%s:%s", last.ClientOID, last.Status))
		return nil
	}
	if err := PlaceGrid(context.Background(), bot, b, rt, save, b.price); err != nil {
		t.Fatal(err)
	}
	b.placed = nil
	return rt, save, &log
}

func countStatus(rt *store.RuntimeState, st string) int {
	n := 0
	for _, o := range rt.Orders {
		if o.Status == st {
			n++
		}
	}
	return n
}

// Finding 1: every order is saved before it is sent.
func TestSavedBeforeSend(t *testing.T) {
	b := newBook(100)
	_, _, log := start(t, b, gridBot)
	if len(*log) != 10 {
		t.Fatalf("want 2 saves per order, got %v", *log)
	}
	for i := 0; i < len(*log); i += 2 {
		if (*log)[i][len((*log)[i])-7:] != "pending" {
			t.Fatalf("save %d before send is not pending: %v", i, *log)
		}
	}
}

// Finding 2: a send that timed out but reached the venue is adopted, not lost.
func TestTimedOutSendIsReconciled(t *testing.T) {
	b := newBook(100)
	rt, save, _ := start(t, b, gridBot)
	b.placeErr = func(exchangex.PlaceReq) (bool, error) { return true, errors.New("timeout") }
	b.fill(t, "buy", 95)
	if _, err := Step(context.Background(), gridBot, b, rt, save); err == nil {
		t.Fatal("timeout not reported")
	}
	last := rt.Orders[len(rt.Orders)-1]
	if last.Status != StatusPending || last.Side != "sell" || last.Price != 100 {
		t.Fatalf("refill not kept as pending: %+v", last)
	}
	b.placeErr = nil
	if _, err := Step(context.Background(), gridBot, b, rt, save); err != nil {
		t.Fatal(err)
	}
	last = rt.Orders[len(rt.Orders)-1]
	if last.Status != StatusOpen || last.ExchangeID == "" || len(b.placed) != 1 {
		t.Fatalf("not reconciled or sent twice: %+v placed=%d", last, len(b.placed))
	}
}

// Review N3: a missing order stays pending through the grace period, then fails.
func TestSendThatNeverArrivedIsFailedAfterGrace(t *testing.T) {
	clock := time.Now()
	now = func() time.Time { return clock }
	t.Cleanup(func() { now = time.Now })
	b := newBook(100)
	rt, save, _ := start(t, b, gridBot)
	b.placeErr = func(exchangex.PlaceReq) (bool, error) { return false, errors.New("timeout") }
	b.fill(t, "buy", 95)
	_, _ = Step(context.Background(), gridBot, b, rt, save)
	b.placeErr = nil
	for i := 0; i < 3; i++ {
		if _, err := Step(context.Background(), gridBot, b, rt, save); err != nil {
			t.Fatal(err)
		}
	}
	if countStatus(rt, StatusPending) != 1 {
		t.Fatalf("failed before the grace period: %+v", rt.Orders[len(rt.Orders)-1])
	}
	clock = clock.Add(3 * time.Minute)
	if _, err := Step(context.Background(), gridBot, b, rt, save); err != nil {
		t.Fatal(err)
	}
	if countStatus(rt, StatusFailed) != 1 || countStatus(rt, StatusPending) != 0 {
		t.Fatalf("%+v", rt.Orders[len(rt.Orders)-1])
	}
}

// Review N3: a pending order that arrives late is cancelled by client ID on stop.
func TestCancelAllCancelsPendingByClientID(t *testing.T) {
	b := newBook(100)
	rt, save, _ := start(t, b, gridBot)
	b.placeErr = func(exchangex.PlaceReq) (bool, error) { return true, errors.New("timeout") }
	b.fill(t, "buy", 95)
	_, _ = Step(context.Background(), gridBot, b, rt, save) // refill reached the venue, kept pending
	if err := CancelAll(context.Background(), gridBot, b, rt, save); err != nil {
		t.Fatal(err)
	}
	if countStatus(rt, StatusPending) != 0 || countStatus(rt, StatusOpen) != 0 {
		t.Fatalf("%+v", rt.Orders)
	}
	for _, o := range b.orders {
		if o.Status == StatusOpen {
			t.Fatalf("left on the book: %+v", o)
		}
	}
}

func TestConfirmedFillRefillsOnce(t *testing.T) {
	b := newBook(100)
	rt, save, _ := start(t, b, gridBot)
	b.fill(t, "buy", 95)
	for i := 0; i < 2; i++ {
		if _, err := Step(context.Background(), gridBot, b, rt, save); err != nil {
			t.Fatal(err)
		}
	}
	if len(b.placed) != 1 || b.placed[0].Side != "sell" || b.placed[0].Price != 100 {
		t.Fatalf("placed %+v", b.placed)
	}
}

func TestCanceledOutsideIsNotAFill(t *testing.T) {
	b := newBook(100)
	rt, save, _ := start(t, b, gridBot)
	b.orders[rt.Orders[4].ClientOID].Status = StatusCanceled // by hand on the venue
	if _, err := Step(context.Background(), gridBot, b, rt, save); err != nil {
		t.Fatal(err)
	}
	if len(b.placed) != 0 || rt.Orders[4].Status != StatusCanceled {
		t.Fatalf("placed=%v status=%s", b.placed, rt.Orders[4].Status)
	}
}

// Finding 4: drivers without OrderStatus cannot run live.
func TestNoOrderStatusRefused(t *testing.T) {
	b := newBook(100)
	ex := noStatus{b}
	if !errors.Is(CanRun(ex), ErrNoOrderStatus) {
		t.Fatal("CanRun accepted a driver without OrderStatus")
	}
	rt := &store.RuntimeState{}
	if _, err := Step(context.Background(), gridBot, ex, rt, func() error { return nil }); !errors.Is(err, ErrNoOrderStatus) {
		t.Fatalf("err %v", err)
	}
}

// Finding 4: spot has its own loss guard.
func TestSpotLossGuard(t *testing.T) {
	b := newBook(100)
	rt, save, _ := start(t, b, gridBot)
	b.price = 86 // 90 * 0.95 = 85.5: still inside
	if res, _ := Step(context.Background(), gridBot, b, rt, save); res.Stopped {
		t.Fatal("fired too early")
	}
	b.price = 85
	res, err := Step(context.Background(), gridBot, b, rt, save)
	if err != nil || !res.Stopped || rt.Status != "stopped" || countStatus(rt, StatusOpen) != 0 {
		t.Fatalf("res=%+v err=%v rt=%+v", res, err, rt.Orders)
	}
	if len(b.placed) != 0 {
		t.Fatalf("spot guard must not sell: %v", b.placed)
	}
}

func futBot() *store.Bot {
	bot := *gridBot
	bot.Leverage = 20
	return &bot
}

// Finding 5: flatten closes only the bot's own net size.
func TestFlattenClosesOwnNetOnly(t *testing.T) {
	b := newBook(100)
	bot := futBot()
	rt, save, _ := start(t, b, bot)
	b.fill(t, "buy", 95)
	b.fill(t, "buy", 90)
	if _, err := Step(context.Background(), bot, b, rt, save); err != nil {
		t.Fatal(err)
	}
	b.pos = []exchangex.Position{{Symbol: "S", Side: "long", Size: 5, EntryPrice: 92}} // 3 more from a manual trade
	b.placed = nil
	if err := Flatten(context.Background(), bot, b, rt, save); err != nil {
		t.Fatal(err)
	}
	c := b.placed[len(b.placed)-1]
	if !c.ReduceOnly || c.Side != "sell" || c.Qty != 2 {
		t.Fatalf("close %+v", c)
	}
	if NetSize(rt) != 0 || rt.Status != "stopped" {
		t.Fatalf("net %g status %s", NetSize(rt), rt.Status)
	}
}

func TestFlattenRefusesWhenVenueDoesNotCover(t *testing.T) {
	for name, pos := range map[string][]exchangex.Position{
		"smaller":  {{Symbol: "S", Side: "long", Size: 0.5}},
		"opposite": {{Symbol: "S", Side: "short", Size: 3}},
		"none":     nil,
	} {
		b := newBook(100)
		bot := futBot()
		rt, save, _ := start(t, b, bot)
		b.fill(t, "buy", 95)
		b.pos = pos
		if err := Flatten(context.Background(), bot, b, rt, save); err == nil {
			t.Errorf("%s: flatten closed anyway", name)
		}
		for _, p := range b.placed {
			if p.ReduceOnly {
				t.Errorf("%s: close order sent", name)
			}
		}
	}
}

// Finding 3 and low 8: a close that does not fill is a CloseError.
func TestPreventiveCloseNotFilledIsCloseError(t *testing.T) {
	b := newBook(100)
	bot := futBot()
	rt, save, _ := start(t, b, bot)
	b.fill(t, "buy", 95)
	b.pos = []exchangex.Position{{Symbol: "S", Side: "long", Size: 1, EntryPrice: 95}}
	if _, err := Step(context.Background(), bot, b, rt, save); err != nil { // records the fill
		t.Fatal(err)
	}
	b.noClose = true
	b.price = 90.4 // 20x long from 95: liq 90.25
	_, err := Step(context.Background(), bot, b, rt, save)
	var ce *CloseError
	if !errors.As(err, &ce) {
		t.Fatalf("want CloseError, got %v", err)
	}
}

func TestPreventiveCloseFlattens(t *testing.T) {
	b := newBook(100)
	bot := futBot()
	rt, save, _ := start(t, b, bot)
	b.fill(t, "buy", 95)
	b.pos = []exchangex.Position{{Symbol: "S", Side: "long", Size: 1, EntryPrice: 95}}
	if _, err := Step(context.Background(), bot, b, rt, save); err != nil { // records the fill
		t.Fatal(err)
	}
	b.price = 90.4
	res, err := Step(context.Background(), bot, b, rt, save)
	if err != nil || !res.Stopped || rt.Status != "stopped" || NetSize(rt) != 0 {
		t.Fatalf("res=%+v err=%v net=%g", res, err, NetSize(rt))
	}
	if c := b.placed[len(b.placed)-1]; !c.ReduceOnly || c.Price >= 90.4 {
		t.Fatalf("close %+v", c)
	}
}

// Low 6: partial fills count toward the net size.
func TestPartialFillCounts(t *testing.T) {
	b := newBook(100)
	rt, save, _ := start(t, b, gridBot)
	b.orders[rt.Orders[1].ClientOID].Filled = 0.4 // the 95 buy, still open
	if _, err := Step(context.Background(), gridBot, b, rt, save); err != nil {
		t.Fatal(err)
	}
	if NetSize(rt) != 0.4 {
		t.Fatalf("net %g", NetSize(rt))
	}
}

// Low 7: an order with empty IDs never matches the book.
func TestEmptyIDsDoNotMatch(t *testing.T) {
	b := newBook(100)
	rt, save, _ := start(t, b, gridBot)
	b.orders["ghost"] = &exchangex.Order{Status: StatusOpen} // no IDs
	rt.Orders = append(rt.Orders, store.RuntimeOrder{Side: "buy", Price: 50, Qty: 1, Status: StatusOpen})
	// It must be looked up (and not found), never matched to the ghost.
	if _, err := Step(context.Background(), gridBot, b, rt, save); !errors.Is(err, exchangex.ErrOrderNotFound) {
		t.Fatalf("err %v", err)
	}
}

func TestLock(t *testing.T) {
	t.Setenv("CUWAN_HOME", t.TempDir())
	l, err := TryLock("g")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TryLock("g"); !errors.Is(err, ErrLocked) {
		t.Fatalf("second lock: %v", err)
	}
	if StopRequested("g") {
		t.Fatal("stop before request")
	}
	_ = RequestStop("g")
	if !StopRequested("g") {
		t.Fatal("stop not seen")
	}
	ClearStop("g")
	l.Unlock()
	l2, err := TryLock("g")
	if err != nil {
		t.Fatal(err)
	}
	l2.Unlock()
}

func (b *book) Rules(context.Context, string) (exchangex.SymbolRules, error) {
	return b.rules, nil
}

// Review N2: orders fit the tick and step; closes round toward a fill.
func TestRoundToVenueFilters(t *testing.T) {
	r := exchangex.SymbolRules{Tick: 0.1, Step: 0.01, MinQty: 0.01}
	cases := []struct {
		side     string
		crossing bool
		px, qty  float64
		wantPx   float64
		wantQty  float64
	}{
		{"buy", false, 95.27, 1.239, 95.2, 1.23},
		{"sell", false, 95.21, 1.0, 95.3, 1.0},
		{"sell", true, 95.29, 2.0, 95.2, 2.0},
		{"buy", true, 95.21, 2.0, 95.3, 2.0},
		{"buy", false, 0.3, 0.3, 0.3, 0.3}, // already on the grid despite float noise
	}
	for _, c := range cases {
		got, err := roundReq(exchangex.PlaceReq{Side: c.side, Price: c.px, Qty: c.qty}, r, c.crossing)
		if err != nil || got.Price != c.wantPx || got.Qty != c.wantQty {
			t.Errorf("%+v -> %+v %v", c, got, err)
		}
	}
	if _, err := roundReq(exchangex.PlaceReq{Side: "buy", Price: 1, Qty: 0.004}, r, false); err == nil {
		t.Error("size below minimum accepted")
	}
	r.MinNotional = 100
	if _, err := roundReq(exchangex.PlaceReq{Side: "buy", Price: 95, Qty: 1}, r, false); err == nil {
		t.Error("order value below minimum accepted")
	}
}

func TestNoRulesRefused(t *testing.T) {
	if !errors.Is(CanRun(noRules{b: newBook(1)}), ErrNoRules) {
		t.Fatal("driver without Rules accepted")
	}
}

// noRules keeps OrderStatus but hides Rules.
type noRules struct{ b *book }

func (n noRules) Name() string   { return "x" }
func (n noRules) Market() string { return "x" }
func (n noRules) Ticker(ctx context.Context, s string) (exchangex.Ticker, error) {
	return n.b.Ticker(ctx, s)
}
func (n noRules) Balances(ctx context.Context) ([]exchangex.Balance, error) {
	return n.b.Balances(ctx)
}
func (n noRules) Positions(ctx context.Context) ([]exchangex.Position, error) {
	return n.b.Positions(ctx)
}
func (n noRules) OpenOrders(ctx context.Context, s string) ([]exchangex.Order, error) {
	return n.b.OpenOrders(ctx, s)
}
func (n noRules) PlaceOrder(ctx context.Context, r exchangex.PlaceReq) (exchangex.Order, error) {
	return n.b.PlaceOrder(ctx, r)
}
func (n noRules) CancelOrder(ctx context.Context, s, id, cid string) error {
	return n.b.CancelOrder(ctx, s, id, cid)
}
func (n noRules) SetLeverage(ctx context.Context, s string, l float64) error {
	return n.b.SetLeverage(ctx, s, l)
}
func (n noRules) Permissions(ctx context.Context) (exchangex.Permissions, error) {
	return n.b.Permissions(ctx)
}
func (n noRules) OrderStatus(ctx context.Context, s, id, cid string) (exchangex.Order, error) {
	return n.b.OrderStatus(ctx, s, id, cid)
}

// Review N5: closes are IOC, retried with fresh prices until the bot is flat.
func TestCloseRetriesPartialIOC(t *testing.T) {
	b := newBook(100)
	bot := futBot()
	rt, save, _ := start(t, b, bot)
	b.fill(t, "buy", 95)
	b.fill(t, "buy", 90)
	if _, err := Step(context.Background(), bot, b, rt, save); err != nil {
		t.Fatal(err)
	}
	b.pos = []exchangex.Position{{Symbol: "S", Side: "long", Size: 2, EntryPrice: 92}}
	b.closeFill = 0.5
	b.placed = nil
	if err := Flatten(context.Background(), bot, b, rt, save); err == nil {
		t.Fatal("0.5 + 0.5*... after 3 tries is not flat; want an error")
	}
	closes := 0
	for _, p := range b.placed {
		if p.ReduceOnly {
			closes++
			if !p.IOC {
				t.Fatalf("close is not IOC: %+v", p)
			}
		}
	}
	if closes != closeAttempts {
		t.Fatalf("closes %d", closes)
	}
}

func TestCloseVerifiesVenuePosition(t *testing.T) {
	b := newBook(100)
	bot := futBot()
	rt, save, _ := start(t, b, bot)
	b.fill(t, "buy", 95)
	if _, err := Step(context.Background(), bot, b, rt, save); err != nil {
		t.Fatal(err)
	}
	b.pos = []exchangex.Position{{Symbol: "S", Side: "long", Size: 1, EntryPrice: 95}}
	// The venue reports a fill but the position does not move.
	b.closeFill = 1
	orig := b.pos[0].Size
	if err := Flatten(context.Background(), bot, noShrink{b}, rt, save); err == nil {
		t.Fatal("position did not shrink; want an error")
	}
	if b.pos[0].Size != orig {
		t.Fatal("noShrink should keep the position")
	}
}

// noShrink reports fills but never moves the position.
type noShrink struct{ *book }

func (n noShrink) PlaceOrder(ctx context.Context, r exchangex.PlaceReq) (exchangex.Order, error) {
	keep := append([]exchangex.Position(nil), n.pos...)
	o, err := n.book.PlaceOrder(ctx, r)
	n.pos = keep
	return o, err
}

// Low: an order in an unknown state is checked again on the next step.
func TestUnknownIsCheckedAgain(t *testing.T) {
	b := newBook(100)
	rt, save, _ := start(t, b, gridBot)
	rt.Orders[1].Status = StatusUnknown
	b.fill(t, "buy", 95)
	if _, err := Step(context.Background(), gridBot, b, rt, save); err != nil {
		t.Fatal(err)
	}
	if rt.Orders[1].Status != StatusFilled || len(b.placed) != 1 {
		t.Fatalf("%+v placed=%v", rt.Orders[1], b.placed)
	}
}

// Review 3, medium: a new run never reuses the last run's client order IDs.
func TestClientIDsDifferPerRun(t *testing.T) {
	b := newBook(100)
	rt1, _, _ := start(t, b, gridBot)
	rt2, _, _ := start(t, newBook(100), gridBot)
	if RunID(rt1) == RunID(rt2) {
		t.Fatal("same run ID twice")
	}
	for i := range rt1.Orders {
		if rt1.Orders[i].ClientOID == rt2.Orders[i].ClientOID {
			t.Fatalf("order %d reuses ID %s", i, rt1.Orders[i].ClientOID)
		}
		if len(rt1.Orders[i].ClientOID) > 36 {
			t.Fatalf("ID longer than Binance's 36: %s", rt1.Orders[i].ClientOID)
		}
	}
}

// Review 3, low: a pending order later found filled gets its counter order.
func TestPendingFoundFilledIsCountered(t *testing.T) {
	b := newBook(100)
	rt, save, _ := start(t, b, gridBot)
	b.placeErr = func(exchangex.PlaceReq) (bool, error) { return true, errors.New("timeout") }
	b.fill(t, "buy", 95)
	_, _ = Step(context.Background(), gridBot, b, rt, save) // 100 sell sent, timed out, pending
	b.placeErr = nil
	b.fill(t, "sell", 100) // the pending sell filled on the venue
	b.placed = nil
	if _, err := Step(context.Background(), gridBot, b, rt, save); err != nil {
		t.Fatal(err)
	}
	if len(b.placed) != 1 || b.placed[0].Side != "buy" || b.placed[0].Price != 95 {
		t.Fatalf("counter order %+v", b.placed)
	}
}

// Review 3, low: a counter order that never reached the venue is sent again.
func TestFailedCounterIsRetried(t *testing.T) {
	clock := time.Now()
	now = func() time.Time { return clock }
	t.Cleanup(func() { now = time.Now })
	b := newBook(100)
	rt, save, _ := start(t, b, gridBot)
	b.placeErr = func(exchangex.PlaceReq) (bool, error) { return false, errors.New("rejected") }
	b.fill(t, "buy", 95)
	_, _ = Step(context.Background(), gridBot, b, rt, save)
	b.placeErr = nil
	for i := 0; i < 3; i++ {
		_, _ = Step(context.Background(), gridBot, b, rt, save)
	}
	clock = clock.Add(3 * time.Minute)
	b.placed = nil
	if _, err := Step(context.Background(), gridBot, b, rt, save); err != nil {
		t.Fatal(err)
	}
	if len(b.placed) != 1 || b.placed[0].Side != "sell" || b.placed[0].Price != 100 {
		t.Fatalf("not retried: %+v", b.placed)
	}
}

// Review 3, low: CancelAll also cancels orders in an unknown state.
func TestCancelAllCoversUnknown(t *testing.T) {
	b := newBook(100)
	rt, save, _ := start(t, b, gridBot)
	rt.Orders[0].Status = StatusUnknown
	if err := CancelAll(context.Background(), gridBot, b, rt, save); err != nil {
		t.Fatal(err)
	}
	if rt.Orders[0].Status != StatusCanceled || b.orders[rt.Orders[0].ClientOID].Status != StatusCanceled {
		t.Fatalf("%+v", rt.Orders[0])
	}
}

// Review #12, 2: with a significant-figure limit the tick follows the
// order's own price, also across a power of ten.
func TestTickFollowsOrderPrice(t *testing.T) {
	r := exchangex.SymbolRules{Tick: 1e-6, SigFigs: 5, Step: 1, MinQty: 1}
	cases := []struct {
		side string
		px   float64
		want float64
	}{
		{"buy", 100.123, 100.12}, // 5 significant figures at 100
		{"buy", 99.5123, 99.512}, // and at 99
		{"sell", 99.99991, 100},  // rounding up crosses to 100
		{"buy", 65000.7, 65000},
		{"sell", 0.123456, 0.12346},
	}
	for _, c := range cases {
		got, err := roundReq(exchangex.PlaceReq{Side: c.side, Price: c.px, Qty: 1}, r, false)
		if err != nil || got.Price != c.want {
			t.Errorf("%s %v -> %v (%v), want %v", c.side, c.px, got.Price, err, c.want)
		}
	}
}
