package cli

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/sgnd/cuwan/internal/backtest"
)

type fakeCandles struct {
	candles  []backtest.Candle
	market   string
	from, to time.Time
}

func (f *fakeCandles) Candles(_ context.Context, _, _ string, from, to time.Time) ([]backtest.Candle, error) {
	f.from, f.to = from, to
	return f.candles, nil
}

func useCandles(t *testing.T, f *fakeCandles) {
	t.Helper()
	prev := candleSource
	candleSource = func(m string) backtest.CandleSource { f.market = m; return f }
	t.Cleanup(func() { candleSource = prev })
}

func TestBacktestJSON(t *testing.T) {
	setup(t, &fakeEx{})
	f := &fakeCandles{candles: []backtest.Candle{{Open: 101, High: 101, Low: 96, Close: 100}}}
	useCandles(t, f)
	code, env := call(t, "backtest", "g", "--from", "2026-01-01", "--to", "2026-01-02", "--fee", "0.1%")
	if code != 0 || !env.OK {
		t.Fatalf("code %d env %+v", code, env)
	}
	var rep backtest.Report
	b, _ := json.Marshal(env.Data)
	_ = json.Unmarshal(b, &rep)
	// Bot g: 90..110 in 4 levels, 50x on usdm. The cap brings it to 20x.
	if f.market != "usdm" || rep.Leverage != 20 || rep.Fills != 1 || rep.Fees <= 0 {
		t.Fatalf("market %s report %+v", f.market, rep)
	}
	if !f.from.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) || !f.to.Equal(time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("range %v to %v", f.from, f.to)
	}
}

func TestBacktestEmptyRange(t *testing.T) {
	setup(t, &fakeEx{})
	useCandles(t, &fakeCandles{})
	code, env := call(t, "backtest", "g")
	if code != 1 || env.OK || len(env.Hints) == 0 {
		t.Fatalf("code %d env %+v", code, env)
	}
}

func TestBacktestBadFlags(t *testing.T) {
	setup(t, &fakeEx{})
	useCandles(t, &fakeCandles{})
	for _, args := range [][]string{
		{"backtest", "g", "--interval", "7m"},
		{"backtest", "g", "--fee", "abc"},
		{"backtest", "g", "--from", "2026-02-01", "--to", "2026-01-01"},
	} {
		if code, env := call(t, args...); code != 2 || env.OK {
			t.Fatalf("%v: code %d", args, code)
		}
	}
}

func TestParseFee(t *testing.T) {
	for in, want := range map[string]float64{"0.1%": 0.001, "0.001": 0.001, "0": 0} {
		if got, err := parseFee(in); err != nil || got != want {
			t.Fatalf("%s: %v %v", in, got, err)
		}
	}
}
