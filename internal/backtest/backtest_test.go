package backtest

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

type fakeSource struct{ candles []Candle }

func (f fakeSource) Candles(context.Context, string, string, time.Time, time.Time) ([]Candle, error) {
	return f.candles, nil
}

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func bars(ohlc ...[4]float64) []Candle {
	out := make([]Candle, len(ohlc))
	for i, v := range ohlc {
		out[i] = Candle{Time: t0.Add(time.Duration(i) * time.Hour), Open: v[0], High: v[1], Low: v[2], Close: v[3]}
	}
	return out
}

func near(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

// Grid 90/100/110 opened at 101: buys at 90 and 100, a sell at 110.
var spot = Config{Symbol: "BTCUSDT", Lower: 90, Upper: 110, Levels: 3, OrderSize: 1, FeeRate: 0.001}

func TestFillsRoundTripsAndFees(t *testing.T) {
	rep, err := Run(context.Background(), fakeSource{bars(
		[4]float64{101, 102, 99, 100},  // buy 100 fills, sell 110 placed
		[4]float64{100, 111, 100, 110}, // both sells at 110 fill; one closes the round trip
	)}, spot, "1h", t0, t0.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Fills != 3 || rep.RoundTrips != 1 {
		t.Fatalf("fills %d round trips %d", rep.Fills, rep.RoundTrips)
	}
	near(t, "realised", rep.Realised, 10)
	near(t, "fees", rep.Fees, 100*0.001+2*110*0.001)
	near(t, "unrealised", rep.Unrealised, 0)
	near(t, "net", rep.Net, 10-0.32)
	near(t, "position", rep.Position, -1)
	if rep.Guard.Fired {
		t.Fatalf("guard fired: %+v", rep.Guard)
	}
}

func TestUnrealisedAndDrawdown(t *testing.T) {
	rep, err := Replay(spot, bars(
		[4]float64{101, 101, 99, 99}, // buy 100
		[4]float64{99, 99, 89, 92},   // buy 90
	))
	if err != nil {
		t.Fatal(err)
	}
	// Long 2 from 100 and 90, marked at 92.
	near(t, "unrealised", rep.Unrealised, (92-100)+(92-90))
	// Peak is 0 before any fill; worst point is the low of 89 with fees paid.
	near(t, "drawdown", rep.MaxDrawdown, 190-178+0.19)
}

func TestSpotGuard(t *testing.T) {
	rep, err := Replay(spot, bars(
		[4]float64{101, 101, 85, 86}, // falls through both buys, then below 85.5
		[4]float64{86, 120, 80, 100}, // the bot is stopped: no more fills
	))
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Guard.Fired || !rep.Guard.Time.Equal(t0) || !strings.Contains(rep.Guard.Reason, "spot loss guard") {
		t.Fatalf("guard %+v", rep.Guard)
	}
	near(t, "guard price", rep.Guard.Price, 85.5)
	if rep.Fills != 2 {
		t.Fatalf("fills %d after the guard", rep.Fills)
	}
	near(t, "position", rep.Position, 2) // spot keeps the coins
}

func TestFuturesGuardCapsLeverage(t *testing.T) {
	cfg := spot
	cfg.Leverage = 50
	rep, err := Replay(cfg, bars(
		[4]float64{101, 101, 95, 96}, // buy 100, then within 5% of liquidation at 95
	))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Leverage != 20 || rep.Warn == "" {
		t.Fatalf("leverage %v warn %q", rep.Leverage, rep.Warn)
	}
	if !rep.Guard.Fired || !strings.Contains(rep.Guard.Reason, "preventive close") {
		t.Fatalf("guard %+v", rep.Guard)
	}
	// Liquidation estimate at 20x is 95; the close fires 95% of the way there.
	near(t, "guard price", rep.Guard.Price, 95.25)
	near(t, "position", rep.Position, 0)
	near(t, "realised", rep.Realised, -4.75)
	near(t, "fees", rep.Fees, 0.1+0.09525)
}

func TestEmptyRange(t *testing.T) {
	_, err := Run(context.Background(), fakeSource{}, spot, "1h", t0, t0)
	if !errors.Is(err, ErrNoCandles) {
		t.Fatalf("err %v", err)
	}
}

// The Binance source pages until a short page, on a local test server.
func TestBinancePaging(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/fapi/v1/klines" || r.Header.Get("X-MBX-APIKEY") != "" {
			t.Errorf("path %s", r.URL.Path)
		}
		start, _ := strconv.ParseInt(r.URL.Query().Get("startTime"), 10, 64)
		n := binanceLimit
		if calls > 1 {
			n = 2
		}
		var rows []string
		for i := 0; i < n; i++ {
			rows = append(rows, fmt.Sprintf(`[%d,"1","2","0.5","1.5","9",0]`, start+int64(i)*60000))
		}
		fmt.Fprint(w, "["+strings.Join(rows, ",")+"]")
	}))
	defer srv.Close()
	got, err := Binance{Market: "usdm", Base: srv.URL}.Candles(context.Background(), "BTCUSDT", "1m", t0, t0.Add(2000*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(got) != binanceLimit+2 || got[0].High != 2 || !got[0].Time.Equal(t0) {
		t.Fatalf("calls %d candles %d first %+v", calls, len(got), got[0])
	}
}
