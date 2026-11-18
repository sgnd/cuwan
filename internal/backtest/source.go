package backtest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Candle is one OHLC bar. Time is the open time.
type Candle struct {
	Time  time.Time `json:"time"`
	Open  float64   `json:"open"`
	High  float64   `json:"high"`
	Low   float64   `json:"low"`
	Close float64   `json:"close"`
}

// CandleSource returns candles for symbol whose open time is in [from, to).
type CandleSource interface {
	Candles(ctx context.Context, symbol, interval string, from, to time.Time) ([]Candle, error)
}

// Intervals are the kline intervals Binance accepts.
var Intervals = map[string]time.Duration{
	"1m": time.Minute, "3m": 3 * time.Minute, "5m": 5 * time.Minute, "15m": 15 * time.Minute,
	"30m": 30 * time.Minute, "1h": time.Hour, "2h": 2 * time.Hour, "4h": 4 * time.Hour,
	"6h": 6 * time.Hour, "8h": 8 * time.Hour, "12h": 12 * time.Hour, "1d": 24 * time.Hour,
}

// Binance reads public klines. It sends no keys and signs nothing.
type Binance struct {
	Market string // spot | usdm
	Base   string // empty means the public mainnet host
	Client *http.Client
}

const binanceLimit = 1000

// Candles pages through /api/v3/klines or /fapi/v1/klines.
func (b Binance) Candles(ctx context.Context, symbol, interval string, from, to time.Time) ([]Candle, error) {
	if _, ok := Intervals[interval]; !ok {
		return nil, fmt.Errorf("backtest: unknown interval %q", interval)
	}
	base, path := "https://api.binance.com", "/api/v3/klines"
	if b.Market == "usdm" {
		base, path = "https://fapi.binance.com", "/fapi/v1/klines"
	}
	if b.Base != "" {
		base = b.Base
	}
	client := b.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	var all []Candle
	start, end := from.UnixMilli(), to.UnixMilli()-1
	for start <= end {
		q := url.Values{}
		q.Set("symbol", symbol)
		q.Set("interval", interval)
		q.Set("startTime", strconv.FormatInt(start, 10))
		q.Set("endTime", strconv.FormatInt(end, 10))
		q.Set("limit", strconv.Itoa(binanceLimit))
		page, err := fetchKlines(ctx, client, base+path+"?"+q.Encode())
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) < binanceLimit {
			break
		}
		start = page[len(page)-1].Time.UnixMilli() + 1
	}
	return all, nil
}

func fetchKlines(ctx context.Context, client *http.Client, u string) ([]Candle, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("backtest: klines: HTTP %d", resp.StatusCode)
	}
	var raw [][]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("backtest: klines: %w", err)
	}
	out := make([]Candle, 0, len(raw))
	for _, r := range raw {
		if len(r) < 5 {
			return nil, fmt.Errorf("backtest: klines: short row")
		}
		var ms int64
		if err := json.Unmarshal(r[0], &ms); err != nil {
			return nil, fmt.Errorf("backtest: klines: %w", err)
		}
		var f [4]float64
		for i := range f {
			var s string
			if err := json.Unmarshal(r[i+1], &s); err != nil {
				return nil, fmt.Errorf("backtest: klines: %w", err)
			}
			if f[i], err = strconv.ParseFloat(s, 64); err != nil {
				return nil, fmt.Errorf("backtest: klines: %w", err)
			}
		}
		out = append(out, Candle{Time: time.UnixMilli(ms).UTC(), Open: f[0], High: f[1], Low: f[2], Close: f[3]})
	}
	return out, nil
}
