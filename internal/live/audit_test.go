package live

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/sgnd/cuwan/internal/exchangex"
)

// Audit HIGH 1: futures is decided by the market, not by the leverage field.
func TestFuturesBotWithoutLeverageIsGuarded(t *testing.T) {
	bot := futBot()
	bot.Leverage = 0 // a record from before validation, or edited by hand
	b, rt, save := longFrom95(t, bot)
	b.mark = 47 // past any trigger for a long from 95
	res, err := Step(context.Background(), bot, b, rt, save)
	if err != nil || !res.Stopped || !strings.Contains(res.Reason, "preventive") {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if c := b.placed[len(b.placed)-1]; !c.ReduceOnly {
		t.Fatalf("the close is not reduce-only: %+v", c)
	}
}

func TestFuturesShortGuardFiresOnRise(t *testing.T) {
	bot := futBot()
	bot.Leverage = 10
	b := newBook(100)
	rt, save, _ := start(t, b, bot)
	b.fill(t, "sell", 105)
	b.pos = []exchangex.Position{{Symbol: "S", Side: "short", Size: 1, EntryPrice: 105}}
	if _, err := Step(context.Background(), bot, b, rt, save); err != nil {
		t.Fatal(err)
	}
	t0 := Trigger(105, 0, 10, false)
	b.price = 106
	b.mark = t0 + 0.01
	res, err := Step(context.Background(), bot, b, rt, save)
	if err != nil || !res.Stopped || !strings.Contains(res.Reason, "preventive") {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if c := b.placed[len(b.placed)-1]; c.Side != "buy" || !c.ReduceOnly {
		t.Fatalf("close %+v", c)
	}
}

// Audit HIGH 2: a value that is not finite stops the bot instead of
// switching the guard off.
func TestGuardRefusesNonFinite(t *testing.T) {
	for name, set := range map[string]func(*book){
		"mark NaN": func(b *book) { b.mark = math.NaN() },
		"mark Inf": func(b *book) { b.mark = math.Inf(1) },
	} {
		bot := futBot()
		b, rt, save := longFrom95(t, bot)
		set(b)
		if _, err := Step(context.Background(), bot, b, rt, save); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	bot := futBot()
	bot.Leverage = math.NaN()
	b, rt, save := longFrom95(t, bot)
	_ = b
	if _, err := Step(context.Background(), bot, b, rt, save); err == nil {
		t.Error("NaN leverage: no error")
	}
}

func TestRoundRefusesNonFinite(t *testing.T) {
	r := exchangex.SymbolRules{Tick: 0.1, Step: 0.01, MinQty: 0.01}
	for _, q := range []float64{math.NaN(), math.Inf(1)} {
		if _, err := roundReq(exchangex.PlaceReq{Side: "buy", Price: 10, Qty: q}, r, false); err == nil {
			t.Errorf("size %v accepted", q)
		}
	}
	if _, err := roundReq(exchangex.PlaceReq{Side: "buy", Price: math.NaN(), Qty: 1}, r, false); err == nil {
		t.Error("NaN price accepted")
	}
}

// From the spec branch: leverage above the cap counts as 20x in the guard.
func TestFuturesGuardCapsLeverage(t *testing.T) {
	bot := futBot()
	bot.Leverage = 50
	b, rt, save := longFrom95(t, bot)
	b.mark = Trigger(95, 0, 20, true) - 0.01
	res, err := Step(context.Background(), bot, b, rt, save)
	if err != nil || !res.Stopped {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

// Low: trail on spot moves the range down at most one range width; past
// that the bot stops and sells back.
func TestSpotTrailIsCapped(t *testing.T) {
	bot := *gridBot // 90..110
	bot.OnExit = OnExitTrail
	b := newBook(100)
	b.free = 100
	rt, save, _ := start(t, b, &bot)
	// each price stays above the spot guard (5% below the moved range)
	for _, px := range []float64{86, 81, 76.5, 71.5} { // down to 70..90: one width
		b.price = px
		if res, err := Step(context.Background(), &bot, b, rt, save); err != nil || res.Stopped {
			t.Fatalf("trail to %v: %+v %v", px, res, err)
		}
	}
	b.price = 66.6 // the next move would put the range more than one width below
	res, err := Step(context.Background(), &bot, b, rt, save)
	if err != nil || !res.Stopped || !strings.Contains(res.Reason, "trail") {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}
