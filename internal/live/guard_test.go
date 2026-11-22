package live

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sgnd/cuwan/internal/exchangex"
	"github.com/sgnd/cuwan/internal/store"
)

// The formula against a table. "Real" is a venue liquidation price that
// includes a maintenance margin; the trigger must sit before it.
func TestTriggerTable(t *testing.T) {
	cases := []struct {
		name              string
		entry, venue, lev float64
		long              bool
		want              float64
	}{
		{"long, venue price given", 100, 96, 20, true, 96.2},     // gap 0.05*4 = 0.2
		{"long, no venue price", 100, 0, 20, true, 97.7},         // liq 97.5, gap max(0.125, 0.2)
		{"short, venue price given", 100, 104, 20, false, 103.8}, // gap 0.2
		{"short, no venue price", 100, 0, 10, false, 104.75},     // liq 105, gap 0.25
		{"long, minimum gap", 100, 99.9, 20, true, 100.1},        // gap max(0.005, 0.2)
		{"venue price on the wrong side is ignored", 100, 101, 20, true, 97.7},
		{"leverage above the cap counts as 20x", 100, 0, 50, true, 97.7},
	}
	for _, c := range cases {
		got := Trigger(c.entry, c.venue, c.lev, c.long)
		if d := got - c.want; d > 1e-9 || d < -1e-9 {
			t.Errorf("%s: trigger %v, want %v", c.name, got, c.want)
		}
	}
}

// Old defect: the estimate entry*(1-1/lev) ignored the maintenance margin,
// so the close could fire after the real liquidation. For every leverage
// and every maintenance rate up to half the initial rate, the trigger is
// before the real liquidation price.
func TestTriggerIsBeforeRealLiquidation(t *testing.T) {
	for lev := 1.0; lev <= 20; lev++ {
		for _, share := range []float64{0, 0.1, 0.25, 0.5} {
			mmr := share / lev
			realLong := 100 * (1 - 1/lev + mmr)
			realShort := 100 * (1 + 1/lev - mmr)
			if got := Trigger(100, 0, lev, true); got <= realLong {
				t.Errorf("long %gx mmr %.4f: trigger %v not before liquidation %v", lev, mmr, got, realLong)
			}
			if got := Trigger(100, 0, lev, false); got >= realShort {
				t.Errorf("short %gx mmr %.4f: trigger %v not before liquidation %v", lev, mmr, got, realShort)
			}
			if got := Trigger(100, realLong, lev, true); got <= realLong {
				t.Errorf("long %gx with venue price: trigger %v not before %v", lev, got, realLong)
			}
		}
	}
	old := 95.0 + 0.05*5 // the old trigger for 20x from 100: 95.25
	// for a long, a lower price is later: the old trigger came after liquidation
	if real := 100 * (1 - 1.0/20 + 0.5/20); old >= real {
		t.Fatalf("the table no longer shows the old defect: %v vs %v", old, real)
	}
}

// The guard reads the mark price, not the last price.
func TestGuardUsesMarkPrice(t *testing.T) {
	bot := futBot()
	b, rt, save := longFrom95(t, bot)
	b.price = 99  // the last price looks safe
	b.mark = 92.7 // the mark is past the trigger 92.815
	res, err := Step(context.Background(), bot, b, rt, save)
	if err != nil || !res.Stopped || !strings.Contains(res.Reason, "preventive") {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

// Old defect: a bot with neither size used 0.001 per order.
func TestNoSizeRefused(t *testing.T) {
	b := newBook(100)
	bot := *gridBot
	bot.OrderSize = 0
	rt := &store.Runtime{Bot: bot.Name, Status: "running"}
	err := PlaceGrid(context.Background(), &bot, b, rt, func() error { return nil }, 100)
	if !errors.Is(err, ErrNoSize) || len(b.placed) != 0 {
		t.Fatalf("err %v placed %d", err, len(b.placed))
	}
}

func TestQuoteAmountSizesOrders(t *testing.T) {
	b := newBook(100)
	bot := *gridBot
	bot.OrderSize, bot.QuoteAmount = 0, 950
	rt := &store.Runtime{Bot: bot.Name, Status: "running"}
	if err := PlaceGrid(context.Background(), &bot, b, rt, func() error { return nil }, 100); err != nil {
		t.Fatal(err)
	}
	for _, p := range b.placed {
		if want := 950 / p.Price; p.Qty > want || p.Qty < want-0.001 {
			t.Errorf("order at %v has size %v, want about %v", p.Price, p.Qty, want)
		}
	}
}

func spotFilled(t *testing.T, free float64) (*book, *store.Runtime, Saver) {
	t.Helper()
	b := newBook(100)
	b.free = free
	rt, save, _ := start(t, b, gridBot)
	b.fill(t, "buy", 95)
	b.fill(t, "buy", 90)
	if _, err := Step(context.Background(), gridBot, b, rt, save); err != nil {
		t.Fatal(err)
	}
	b.placed = nil
	return b, rt, save
}

// CW25: spot flatten sells the base asset the bot bought.
func TestSpotFlattenSellsBack(t *testing.T) {
	b, rt, save := spotFilled(t, 5)
	if err := Flatten(context.Background(), gridBot, b, rt, save); err != nil {
		t.Fatal(err)
	}
	c := b.placed[len(b.placed)-1]
	if c.Side != exchangex.SideSell || c.Qty != 2 || !c.IOC || c.ReduceOnly {
		t.Fatalf("close %+v", c)
	}
	if NetSize(rt) != 0 || rt.Status != "stopped" {
		t.Fatalf("net %v status %s", NetSize(rt), rt.Status)
	}
}

func TestSpotFlattenRefusesWithoutBalance(t *testing.T) {
	b, rt, save := spotFilled(t, 1)
	if err := Flatten(context.Background(), gridBot, b, rt, save); err == nil {
		t.Fatal("sold more than the free balance")
	}
	for _, p := range b.placed {
		if p.IOC {
			t.Fatalf("a close was sent: %+v", p)
		}
	}
}
