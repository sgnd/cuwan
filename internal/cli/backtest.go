package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/sgnd/cuwan/internal/backtest"
	"github.com/sgnd/cuwan/internal/out"
	"github.com/sgnd/cuwan/internal/store"
)

// candleSource is swapped in tests so they make no network calls.
var candleSource = func(market string) backtest.CandleSource {
	return backtest.Binance{Market: market}
}

const backtestUsage = "usage: cuwan backtest NAME [--from YYYY-MM-DD] [--to YYYY-MM-DD] [--interval 1h] [--fee 0.1%]"

func cmdBacktest(args []string) int {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(os.Stdout, backtestUsage)
		fmt.Fprintln(os.Stdout, "Replays a saved bot over public Binance candles. Defaults: the last 30 days, 1h, 0.1% fee.")
		return 0
	}
	pos := positional(args)
	if len(pos) < 1 {
		return out.Fail("backtest", errors.New(backtestUsage), 2)
	}
	bot, err := store.GetBot(pos[0])
	if err != nil {
		return out.Fail("backtest", err, 1)
	}
	exRec, err := store.GetExchange(bot.Exchange)
	if err != nil {
		return out.Fail("backtest", err, 1)
	}
	if exRec.Venue != "binance" {
		return out.Fail("backtest", fmt.Errorf("backtest reads Binance candles; exchange %q is on %s", exRec.Name, exRec.Venue), 2)
	}
	market := "spot"
	if exRec.Market == "usdm" {
		market = "usdm"
	}

	to := time.Now().UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
	if v, ok := flagValue(args, "--to"); ok {
		d, err := time.Parse("2006-01-02", v)
		if err != nil {
			return out.Fail("backtest", fmt.Errorf("--to: want YYYY-MM-DD, got %q", v), 2)
		}
		to = d.Add(24 * time.Hour) // the --to day is included
	}
	from := to.AddDate(0, 0, -30)
	if v, ok := flagValue(args, "--from"); ok {
		d, err := time.Parse("2006-01-02", v)
		if err != nil {
			return out.Fail("backtest", fmt.Errorf("--from: want YYYY-MM-DD, got %q", v), 2)
		}
		from = d
	}
	if !from.Before(to) {
		return out.Fail("backtest", errors.New("--from must be on or before --to"), 2)
	}
	interval := "1h"
	if v, ok := flagValue(args, "--interval"); ok {
		interval = v
	}
	if _, ok := backtest.Intervals[interval]; !ok {
		return out.Fail("backtest", fmt.Errorf("unknown interval %q", interval), 2,
			"use 1m, 3m, 5m, 15m, 30m, 1h, 2h, 4h, 6h, 8h, 12h or 1d")
	}
	fee := 0.001
	if v, ok := flagValue(args, "--fee"); ok {
		if fee, err = parseFee(v); err != nil {
			return out.Fail("backtest", err, 2)
		}
	}

	cfg := backtest.Config{
		Symbol: bot.Symbol, Lower: bot.Lower, Upper: bot.Upper, Levels: bot.Levels,
		OrderSize: bot.OrderSize, QuoteAmount: bot.QuoteAmount, Leverage: bot.Leverage, FeeRate: fee,
	}
	rep, err := backtest.Run(context.Background(), candleSource(market), cfg, interval, from, to)
	if errors.Is(err, backtest.ErrNoCandles) {
		return out.Fail("backtest", err, 1, "check the symbol and pick a range with trading")
	}
	if err != nil {
		return out.Fail("backtest", err, 1)
	}
	out.Print(os.Stdout, "backtest", rep, backtestText(bot.Name, rep))
	return 0
}

// parseFee reads "0.1%" as 0.001 and a bare "0.001" as is.
func parseFee(v string) (float64, error) {
	s := strings.TrimSpace(v)
	pct := strings.HasSuffix(s, "%")
	f, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
	if err != nil || f < 0 {
		return 0, fmt.Errorf("--fee: want a rate like 0.1%% or 0.001, got %q", v)
	}
	if pct {
		f /= 100
	}
	if f >= 1 {
		return 0, fmt.Errorf("--fee: %q is 100%% or more", v)
	}
	return f, nil
}

func backtestText(name string, r backtest.Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "backtest %s: %s %s, %s to %s, %d candles\n", name, r.Symbol, r.Interval,
		r.From.Format("2006-01-02"), r.To.Add(-time.Nanosecond).Format("2006-01-02"), r.Candles)
	fmt.Fprintf(&b, "price        %g to %g\n", r.FirstOpen, r.LastClose)
	if r.Leverage > 0 {
		fmt.Fprintf(&b, "leverage     %gx\n", r.Leverage)
	}
	if r.Warn != "" {
		fmt.Fprintf(&b, "warn         %s\n", r.Warn)
	}
	fmt.Fprintf(&b, "fills        %d\n", r.Fills)
	fmt.Fprintf(&b, "round trips  %d\n", r.RoundTrips)
	fmt.Fprintf(&b, "realised     %.4f\n", r.Realised)
	fmt.Fprintf(&b, "unrealised   %.4f (position %g at the last close)\n", r.Unrealised, r.Position)
	fmt.Fprintf(&b, "fees         %.4f\n", r.Fees)
	fmt.Fprintf(&b, "net          %.4f\n", r.Net)
	fmt.Fprintf(&b, "max drawdown %.4f\n", r.MaxDrawdown)
	if r.Guard.Fired {
		fmt.Fprintf(&b, "guard        fired %s at %g: %s", r.Guard.Time.Format("2006-01-02 15:04"), r.Guard.Price, r.Guard.Reason)
	} else {
		b.WriteString("guard        did not fire")
	}
	return b.String()
}
