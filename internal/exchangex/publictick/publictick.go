// Package publictick is a thin public-REST ticker driver for paper mode.
// It never signs requests and refuses PlaceOrder/CancelOrder when Paper is set
// (and for registry backends, live trading is not implemented here).
package publictick

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sgnd/cuwan/internal/exchangex"
)

// Spec describes how to fetch a public last price.
type Spec struct {
	// URLTemplate uses {symbol} placeholder (already normalized).
	URLTemplate string
	// SymbolFmt: "concat" (BTCUSDT), "slash" (BTC/USDT), "upper_concat", "lower_concat", "raw"
	SymbolFmt string
	// PricePath is a dotted JSON path to a numeric/string price, e.g. "price" or "ticker.last".
	PricePath string
	// ArrayPrice: if true, response is an array; take first element's PricePath.
	ArrayPrice bool
}

// Driver implements exchangex.Exchange for public tickers / paper.
type Driver struct {
	cfg    exchangex.Config
	info   exchangex.VenueInfo
	spec   *Spec // nil => no public ticker
	client *http.Client
}

// Open builds a public/registry driver.
func Open(cfg exchangex.Config, info exchangex.VenueInfo, spec *Spec) (exchangex.Exchange, error) {
	market := cfg.Market
	if market == "" && len(info.Markets) > 0 {
		market = info.Markets[0]
	}
	if market == "" {
		market = exchangex.MarketSpot
	}
	cfg.Market = market
	return &Driver{
		cfg:    cfg,
		info:   info,
		spec:   spec,
		client: &http.Client{Timeout: 15 * time.Second},
	}, nil
}

func (d *Driver) Name() string   { return d.info.ID }
func (d *Driver) Market() string { return d.cfg.Market }

func (d *Driver) Ticker(ctx context.Context, symbol string) (exchangex.Ticker, error) {
	if d.spec == nil || d.spec.URLTemplate == "" {
		return exchangex.Ticker{}, fmt.Errorf("%s: no public ticker endpoint wired yet (paper where available)", d.info.ID)
	}
	sym := formatSymbol(symbol, d.spec.SymbolFmt)
	u := strings.ReplaceAll(d.spec.URLTemplate, "{symbol}", sym)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return exchangex.Ticker{}, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := d.client.Do(req)
	if err != nil {
		return exchangex.Ticker{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return exchangex.Ticker{}, err
	}
	if resp.StatusCode >= 300 {
		return exchangex.Ticker{}, fmt.Errorf("%s: ticker %s: %s", d.info.ID, resp.Status, truncate(body))
	}
	px, err := extractPrice(body, d.spec.PricePath, d.spec.ArrayPrice)
	if err != nil {
		return exchangex.Ticker{}, fmt.Errorf("%s: %w", d.info.ID, err)
	}
	return exchangex.Ticker{Symbol: symbol, Price: px, Time: time.Now().UTC()}, nil
}

func (d *Driver) Balances(ctx context.Context) ([]exchangex.Balance, error) {
	return nil, fmt.Errorf("%s: balances require live credentials (not available via public ticker)", d.info.ID)
}

func (d *Driver) Positions(ctx context.Context) ([]exchangex.Position, error) {
	return nil, nil
}

func (d *Driver) OpenOrders(ctx context.Context, symbol string) ([]exchangex.Order, error) {
	return nil, nil
}

func (d *Driver) PlaceOrder(ctx context.Context, req exchangex.PlaceReq) (exchangex.Order, error) {
	if d.cfg.Paper {
		return exchangex.Order{}, fmt.Errorf("%s: paper mode refuses signed PlaceOrder", d.info.ID)
	}
	return exchangex.Order{}, fmt.Errorf("%s: live trading not enabled for this venue backend yet (use paper or a native driver)", d.info.ID)
}

func (d *Driver) CancelOrder(ctx context.Context, symbol, orderID, clientOID string) error {
	if d.cfg.Paper {
		return fmt.Errorf("%s: paper mode refuses signed CancelOrder", d.info.ID)
	}
	return fmt.Errorf("%s: live CancelOrder not enabled for this venue backend yet", d.info.ID)
}

func (d *Driver) SetLeverage(ctx context.Context, symbol string, lev float64) error {
	if d.cfg.Paper {
		return nil
	}
	return fmt.Errorf("%s: live SetLeverage not enabled for this venue backend yet", d.info.ID)
}

func (d *Driver) Permissions(ctx context.Context) (exchangex.Permissions, error) {
	return exchangex.Permissions{CanRead: true, CanTrade: false, CanWithdraw: false, Raw: d.info.Backend}, nil
}

func formatSymbol(symbol, fmtName string) string {
	s := strings.TrimSpace(symbol)
	switch fmtName {
	case "slash":
		if strings.Contains(s, "/") {
			return s
		}
		return toSlash(s)
	case "dash":
		if strings.Contains(s, "-") {
			return strings.ToUpper(s)
		}
		return strings.ReplaceAll(toSlash(s), "/", "-")
	case "underscore":
		if strings.Contains(s, "_") {
			return strings.ToUpper(s)
		}
		return strings.ReplaceAll(toSlash(s), "/", "_")
	case "lower_concat":
		return strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(s, "/", ""), "-", ""))
	case "upper_concat", "concat", "":
		return strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(s, "/", ""), "-", ""))
	case "raw":
		return s
	default:
		return strings.ToUpper(strings.ReplaceAll(s, "/", ""))
	}
}

func toSlash(s string) string {
	s = strings.ToUpper(strings.ReplaceAll(s, "/", ""))
	quotes := []string{"USDT", "USDC", "USD", "BTC", "ETH", "EUR", "GBP", "FDUSD", "BUSD"}
	for _, q := range quotes {
		if strings.HasSuffix(s, q) && len(s) > len(q) {
			return s[:len(s)-len(q)] + "/" + q
		}
	}
	if len(s) > 3 {
		return s[:len(s)-3] + "/" + s[len(s)-3:]
	}
	return s
}

func extractPrice(body []byte, path string, array bool) (float64, error) {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return 0, err
	}
	if array {
		arr, ok := v.([]any)
		if !ok || len(arr) == 0 {
			return 0, fmt.Errorf("expected non-empty JSON array for ticker")
		}
		v = arr[0]
	}
	cur := v
	if path != "" {
		var err error
		cur, err = walkPath(v, path)
		if err != nil {
			return 0, err
		}
	}
	return asFloat(cur)
}

func walkPath(v any, path string) (any, error) {
	parts := strings.Split(path, ".")
	cur := v
	for _, p := range parts {
		if p == "" {
			continue
		}
		if idx, err := strconv.Atoi(p); err == nil {
			arr, ok := cur.([]any)
			if !ok || idx < 0 || idx >= len(arr) {
				return nil, fmt.Errorf("ticker path %q not found", path)
			}
			cur = arr[idx]
			continue
		}
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("ticker path %q not found", path)
		}
		next, ok := m[p]
		if !ok {
			return nil, fmt.Errorf("ticker path %q not found", path)
		}
		cur = next
	}
	return cur, nil
}

func asFloat(cur any) (float64, error) {
	switch t := cur.(type) {
	case float64:
		return t, nil
	case string:
		return strconv.ParseFloat(t, 64)
	case json.Number:
		return t.Float64()
	default:
		return 0, fmt.Errorf("ticker price has unexpected type %T", cur)
	}
}

func truncate(b []byte) string {
	s := string(b)
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}
