// Package hyperliquid implements Hyperliquid perps (info + signed trading stub).
package hyperliquid

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sgnd/cuwan/internal/exchangex"
)

func init() {
	exchangex.Register("hyperliquid", Open)
	exchangex.RegisterMeta(exchangex.VenueInfo{
		ID: "hyperliquid", Kind: exchangex.KindDEX,
		Markets: []string{exchangex.MarketPerp},
		Backend: exchangex.BackendNative, PublicTicker: true, Live: "paper-only",
	}, Open)
}

const mainnetInfo = "https://api.hyperliquid.xyz/info"
const mainnetExchange = "https://api.hyperliquid.xyz/exchange"
const testnetInfo = "https://api.hyperliquid-testnet.xyz/info"
const testnetExchange = "https://api.hyperliquid-testnet.xyz/exchange"

// Open builds a Hyperliquid driver.
func Open(cfg exchangex.Config) (exchangex.Exchange, error) {
	info, exch := mainnetInfo, mainnetExchange
	if cfg.Testnet {
		info, exch = testnetInfo, testnetExchange
	}
	return &Driver{
		cfg:     cfg,
		infoURL: info,
		exchURL: exch,
		client:  &http.Client{Timeout: 15 * time.Second},
	}, nil
}

// Driver talks to Hyperliquid.
type Driver struct {
	cfg     exchangex.Config
	infoURL string
	exchURL string
	client  *http.Client

	mu        sync.Mutex
	assets    map[string]int // coin -> index in meta.universe
	lastNonce uint64
	sg        *signer
}

// wallet returns the signer for the secret key.
func (d *Driver) wallet() (*signer, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.sg == nil {
		if d.cfg.APISecret == "" {
			return nil, fmt.Errorf("hyperliquid: missing API wallet private key (secret)")
		}
		sg, err := newSigner(d.cfg.APISecret)
		if err != nil {
			return nil, err
		}
		d.sg = sg
	}
	return d.sg, nil
}

// asset returns the coin's index in meta.universe, which actions use.
func (d *Driver) asset(ctx context.Context, coin string) (int, error) {
	d.mu.Lock()
	i, ok := d.assets[coin]
	d.mu.Unlock()
	if ok {
		return i, nil
	}
	var meta struct {
		Universe []struct {
			Name string `json:"name"`
		} `json:"universe"`
	}
	if err := d.postJSON(ctx, d.infoURL, map[string]any{"type": "meta"}, &meta); err != nil {
		return 0, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.assets = map[string]int{}
	for i, u := range meta.Universe {
		d.assets[u.Name] = i
	}
	if i, ok := d.assets[coin]; ok {
		return i, nil
	}
	return 0, fmt.Errorf("hyperliquid: unknown coin %s", coin)
}

// nonce is the time in ms, strictly increasing within this driver.
func (d *Driver) nonce() uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := uint64(time.Now().UnixMilli())
	if n <= d.lastNonce {
		n = d.lastNonce + 1
	}
	d.lastNonce = n
	return n
}

// exchange signs and posts an L1 action, then decodes response.data.
func (d *Driver) exchange(ctx context.Context, action omap, data any) error {
	sg, err := d.wallet()
	if err != nil {
		return err
	}
	n := d.nonce()
	sig, err := sg.signL1(action, n, !d.cfg.Testnet)
	if err != nil {
		return err
	}
	var resp struct {
		Status   string          `json:"status"`
		Response json.RawMessage `json:"response"`
	}
	body := map[string]any{"action": action, "nonce": n, "signature": sig, "vaultAddress": nil}
	if err := d.postJSON(ctx, d.exchURL, body, &resp); err != nil {
		return err
	}
	if resp.Status != "ok" {
		return fmt.Errorf("hyperliquid: %s", truncate(resp.Response))
	}
	if data == nil {
		return nil
	}
	var inner struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(resp.Response, &inner); err != nil {
		return err
	}
	return json.Unmarshal(inner.Data, data)
}

func truncate(b []byte) string {
	s := string(b)
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

func (d *Driver) Name() string   { return "hyperliquid" }
func (d *Driver) Market() string { return exchangex.MarketPerp }

func (d *Driver) Ticker(ctx context.Context, symbol string) (exchangex.Ticker, error) {
	body := map[string]any{"type": "allMids"}
	var mids map[string]string
	if err := d.postJSON(ctx, d.infoURL, body, &mids); err != nil {
		return exchangex.Ticker{}, err
	}
	pxs, ok := mids[symbol]
	if !ok {
		// try without -PERP suffix conventions
		return exchangex.Ticker{}, fmt.Errorf("hyperliquid: no mid for %s", symbol)
	}
	px, err := strconv.ParseFloat(pxs, 64)
	if err != nil {
		return exchangex.Ticker{}, err
	}
	return exchangex.Ticker{Symbol: symbol, Price: px, Time: time.Now().UTC()}, nil
}

func (d *Driver) Balances(ctx context.Context) ([]exchangex.Balance, error) {
	if d.cfg.Paper || d.cfg.APIKey == "" {
		return nil, fmt.Errorf("hyperliquid: balances need wallet address (APIKey)")
	}
	body := map[string]any{
		"type": "clearinghouseState",
		"user": d.cfg.APIKey,
	}
	var raw struct {
		MarginSummary struct {
			AccountValue string `json:"accountValue"`
		} `json:"marginSummary"`
		Withdrawable string `json:"withdrawable"`
	}
	if err := d.postJSON(ctx, d.infoURL, body, &raw); err != nil {
		return nil, err
	}
	free, _ := strconv.ParseFloat(raw.Withdrawable, 64)
	total, _ := strconv.ParseFloat(raw.MarginSummary.AccountValue, 64)
	return []exchangex.Balance{{Asset: "USDC", Free: free, Locked: total - free}}, nil
}

func (d *Driver) Positions(ctx context.Context) ([]exchangex.Position, error) {
	if d.cfg.Paper || d.cfg.APIKey == "" {
		return nil, nil
	}
	body := map[string]any{
		"type": "clearinghouseState",
		"user": d.cfg.APIKey,
	}
	var raw struct {
		AssetPositions []struct {
			Position struct {
				Coin     string `json:"coin"`
				Szi      string `json:"szi"`
				EntryPx  string `json:"entryPx"`
				Leverage struct {
					Value float64 `json:"value"`
				} `json:"leverage"`
				LiquidationPx *string `json:"liquidationPx"`
				UnrealizedPnl string  `json:"unrealizedPnl"`
			} `json:"position"`
		} `json:"assetPositions"`
	}
	if err := d.postJSON(ctx, d.infoURL, body, &raw); err != nil {
		return nil, err
	}
	var out []exchangex.Position
	for _, ap := range raw.AssetPositions {
		p := ap.Position
		size, _ := strconv.ParseFloat(p.Szi, 64)
		if size == 0 {
			continue
		}
		side := exchangex.SideBuy
		if size < 0 {
			side = exchangex.SideSell
			size = -size
		}
		entry, _ := strconv.ParseFloat(p.EntryPx, 64)
		upnl, _ := strconv.ParseFloat(p.UnrealizedPnl, 64)
		liq := 0.0
		if p.LiquidationPx != nil {
			liq, _ = strconv.ParseFloat(*p.LiquidationPx, 64)
		}
		out = append(out, exchangex.Position{
			Symbol: p.Coin, Side: side, Size: size, EntryPrice: entry,
			Leverage: p.Leverage.Value, LiquidationPrice: liq, UnrealizedPNL: upnl,
		})
	}
	return out, nil
}

func (d *Driver) OpenOrders(ctx context.Context, symbol string) ([]exchangex.Order, error) {
	if d.cfg.Paper || d.cfg.APIKey == "" {
		return nil, nil
	}
	body := map[string]any{"type": "openOrders", "user": d.cfg.APIKey}
	var raw []struct {
		Coin    string `json:"coin"`
		Side    string `json:"side"`
		LimitPx string `json:"limitPx"`
		Sz      string `json:"sz"`
		Oid     int64  `json:"oid"`
		Cloid   string `json:"cloid"`
	}
	if err := d.postJSON(ctx, d.infoURL, body, &raw); err != nil {
		return nil, err
	}
	var out []exchangex.Order
	for _, r := range raw {
		if symbol != "" && r.Coin != symbol {
			continue
		}
		px, _ := strconv.ParseFloat(r.LimitPx, 64)
		qty, _ := strconv.ParseFloat(r.Sz, 64)
		side := exchangex.SideBuy
		if r.Side == "A" {
			side = exchangex.SideSell
		}
		out = append(out, exchangex.Order{
			ID: strconv.FormatInt(r.Oid, 10), ClientOID: r.Cloid,
			Symbol: r.Coin, Side: side, Price: px, Qty: qty, Status: "open",
		})
	}
	return out, nil
}

func (d *Driver) PlaceOrder(ctx context.Context, req exchangex.PlaceReq) (exchangex.Order, error) {
	if d.cfg.Paper {
		return exchangex.Order{}, fmt.Errorf("hyperliquid: paper mode refuses signed PlaceOrder")
	}
	asset, err := d.asset(ctx, req.Symbol)
	if err != nil {
		return exchangex.Order{}, err
	}
	tif := "Gtc"
	switch {
	case req.IOC:
		tif = "Ioc"
	case req.PostOnly:
		tif = "Alo"
	}
	cloid := ""
	if req.ClientOID != "" {
		cloid = Cloid(req.ClientOID)
	}
	w, err := orderWire(asset, req.Side == exchangex.SideBuy, req.Price, req.Qty, req.ReduceOnly, tif, cloid)
	if err != nil {
		return exchangex.Order{}, err
	}
	var st struct {
		Statuses []struct {
			Resting *struct {
				Oid int64 `json:"oid"`
			} `json:"resting"`
			Filled *struct {
				Oid     int64  `json:"oid"`
				TotalSz string `json:"totalSz"`
			} `json:"filled"`
			Error string `json:"error"`
		} `json:"statuses"`
	}
	if err := d.exchange(ctx, omap{{"type", "order"}, {"orders", []any{w}}, {"grouping", "na"}}, &st); err != nil {
		return exchangex.Order{}, err
	}
	if len(st.Statuses) != 1 {
		return exchangex.Order{}, fmt.Errorf("hyperliquid: expected one order status, got %d", len(st.Statuses))
	}
	o := exchangex.Order{ClientOID: req.ClientOID, Symbol: req.Symbol, Side: req.Side, Price: req.Price, Qty: req.Qty, ReduceOnly: req.ReduceOnly}
	switch r := st.Statuses[0]; {
	case r.Error != "":
		return exchangex.Order{}, fmt.Errorf("hyperliquid: order rejected: %s", r.Error)
	case r.Resting != nil:
		o.ID, o.Status = strconv.FormatInt(r.Resting.Oid, 10), "open"
	case r.Filled != nil:
		o.ID, o.Status = strconv.FormatInt(r.Filled.Oid, 10), "filled"
		o.Filled, _ = strconv.ParseFloat(r.Filled.TotalSz, 64)
	default:
		return exchangex.Order{}, fmt.Errorf("hyperliquid: unexpected order status")
	}
	return o, nil
}

// CancelOrder cancels by oid, or by the cloid made from the client order ID.
func (d *Driver) CancelOrder(ctx context.Context, symbol, orderID, clientOID string) error {
	if d.cfg.Paper {
		return fmt.Errorf("hyperliquid: paper mode refuses signed CancelOrder")
	}
	asset, err := d.asset(ctx, symbol)
	if err != nil {
		return err
	}
	var action omap
	switch {
	case orderID != "":
		oid, err := strconv.ParseInt(orderID, 10, 64)
		if err != nil {
			return fmt.Errorf("hyperliquid: bad oid %q", orderID)
		}
		action = omap{{"type", "cancel"}, {"cancels", []any{omap{{"a", asset}, {"o", oid}}}}}
	case clientOID != "":
		action = omap{{"type", "cancelByCloid"}, {"cancels", []any{omap{{"asset", asset}, {"cloid", Cloid(clientOID)}}}}}
	default:
		return fmt.Errorf("hyperliquid: CancelOrder needs an order ID")
	}
	var st struct {
		Statuses []json.RawMessage `json:"statuses"`
	}
	if err := d.exchange(ctx, action, &st); err != nil {
		return err
	}
	for _, raw := range st.Statuses {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			if strings.Contains(e.Error, "never placed") || strings.Contains(e.Error, "already canceled") || strings.Contains(e.Error, "filled") {
				return fmt.Errorf("hyperliquid: %s: %w", e.Error, exchangex.ErrOrderNotFound)
			}
			return fmt.Errorf("hyperliquid: cancel rejected: %s", e.Error)
		}
	}
	return nil
}

// SetLeverage sets cross leverage for the coin.
func (d *Driver) SetLeverage(ctx context.Context, symbol string, lev float64) error {
	if d.cfg.Paper {
		return nil
	}
	asset, err := d.asset(ctx, symbol)
	if err != nil {
		return err
	}
	return d.exchange(ctx, omap{{"type", "updateLeverage"}, {"asset", asset}, {"isCross", true}, {"leverage", int(lev)}}, nil)
}

// Permissions proves the secret is an API wallet of the configured account.
// An API wallet can trade but cannot withdraw; the account's own key can.
// Anything else fails closed.
func (d *Driver) Permissions(ctx context.Context) (exchangex.Permissions, error) {
	if d.cfg.Paper || d.cfg.APISecret == "" {
		return exchangex.Permissions{CanRead: true}, nil
	}
	sg, err := d.wallet()
	if err != nil {
		return exchangex.Permissions{}, err
	}
	if strings.EqualFold(sg.addr, d.cfg.APIKey) {
		return exchangex.Permissions{CanRead: true, CanTrade: true, CanWithdraw: true, Raw: "account key"}, nil
	}
	var role struct {
		Role string `json:"role"`
		Data struct {
			User string `json:"user"`
		} `json:"data"`
	}
	if err := d.postJSON(ctx, d.infoURL, map[string]any{"type": "userRole", "user": sg.addr}, &role); err != nil {
		return exchangex.Permissions{}, err
	}
	if role.Role != "agent" || !strings.EqualFold(role.Data.User, d.cfg.APIKey) {
		return exchangex.Permissions{}, fmt.Errorf("hyperliquid: key %s is not an API wallet of %s (role %q)", sg.addr, d.cfg.APIKey, role.Role)
	}
	return exchangex.Permissions{CanRead: true, CanTrade: true, Raw: "api wallet " + sg.addr}, nil
}

// Cloid maps a cuwan client order ID to Hyperliquid's 16-byte hex cloid.
func Cloid(clientOID string) string {
	sum := sha256.Sum256([]byte(clientOID))
	return "0x" + hex.EncodeToString(sum[:16])
}

// OrderStatus reports one order by oid or by client order ID.
func (d *Driver) OrderStatus(ctx context.Context, symbol, orderID, clientOID string) (exchangex.Order, error) {
	if d.cfg.APIKey == "" {
		return exchangex.Order{}, fmt.Errorf("hyperliquid: OrderStatus needs the account address")
	}
	var oid any
	switch {
	case orderID != "":
		n, err := strconv.ParseInt(orderID, 10, 64)
		if err != nil {
			return exchangex.Order{}, fmt.Errorf("hyperliquid: bad oid %q", orderID)
		}
		oid = n
	case clientOID != "":
		oid = Cloid(clientOID)
	default:
		return exchangex.Order{}, fmt.Errorf("hyperliquid: OrderStatus needs an order ID")
	}
	var raw struct {
		Status string `json:"status"`
		Order  struct {
			Order struct {
				Coin    string `json:"coin"`
				Side    string `json:"side"`
				LimitPx string `json:"limitPx"`
				Sz      string `json:"sz"`
				OrigSz  string `json:"origSz"`
				Oid     int64  `json:"oid"`
			} `json:"order"`
			Status string `json:"status"`
		} `json:"order"`
	}
	if err := d.postJSON(ctx, d.infoURL, map[string]any{"type": "orderStatus", "user": d.cfg.APIKey, "oid": oid}, &raw); err != nil {
		return exchangex.Order{}, err
	}
	if raw.Status != "order" {
		return exchangex.Order{}, fmt.Errorf("hyperliquid: %w", exchangex.ErrOrderNotFound)
	}
	o := raw.Order.Order
	px, _ := strconv.ParseFloat(o.LimitPx, 64)
	left, _ := strconv.ParseFloat(o.Sz, 64)
	orig, _ := strconv.ParseFloat(o.OrigSz, 64)
	side := exchangex.SideBuy
	if o.Side == "A" {
		side = exchangex.SideSell
	}
	return exchangex.Order{
		ID: strconv.FormatInt(o.Oid, 10), ClientOID: clientOID, Symbol: o.Coin, Side: side,
		Price: px, Qty: orig, Filled: orig - left, Status: normStatus(raw.Order.Status),
	}, nil
}

// normStatus maps Hyperliquid order states to open, filled, canceled.
func normStatus(s string) string {
	switch {
	case s == "open" || s == "triggered":
		return "open"
	case s == "filled":
		return "filled"
	case s == "canceled" || s == "rejected" || strings.HasSuffix(s, "Canceled") || strings.HasSuffix(s, "Rejected"):
		return "canceled"
	}
	return "unknown"
}

// minOrderValue is Hyperliquid's minimum order value in USD.
const minOrderValue = 10

// Rules derives filters from meta. Size step is 10^-szDecimals. Prices allow
// at most 5 significant figures and 6-szDecimals decimals, so the tick
// depends on the price and is computed from the current mid on every call.
func (d *Driver) Rules(ctx context.Context, symbol string) (exchangex.SymbolRules, error) {
	var meta struct {
		Universe []struct {
			Name       string `json:"name"`
			SzDecimals int    `json:"szDecimals"`
		} `json:"universe"`
	}
	if err := d.postJSON(ctx, d.infoURL, map[string]any{"type": "meta"}, &meta); err != nil {
		return exchangex.SymbolRules{}, err
	}
	for _, u := range meta.Universe {
		if u.Name != symbol {
			continue
		}
		t, err := d.Ticker(ctx, symbol)
		if err != nil {
			return exchangex.SymbolRules{}, err
		}
		step := math.Pow(10, -float64(u.SzDecimals))
		return exchangex.SymbolRules{
			Tick: tickFor(t.Price, u.SzDecimals), Step: step, MinQty: step, MinNotional: minOrderValue,
		}, nil
	}
	return exchangex.SymbolRules{}, fmt.Errorf("hyperliquid: unknown coin %s", symbol)
}

// tickFor is the coarser of the decimals limit and the 5-significant-figure limit.
func tickFor(price float64, szDecimals int) float64 {
	tick := math.Pow(10, -float64(6-szDecimals))
	if price > 0 {
		if sig := math.Pow(10, math.Floor(math.Log10(price))-4); sig > tick {
			tick = sig
		}
	}
	return tick
}

func (d *Driver) postJSON(ctx context.Context, endpoint string, body any, dest any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		s := string(raw)
		if len(s) > 200 {
			s = s[:200] + "…"
		}
		return fmt.Errorf("hyperliquid: %s: %s", resp.Status, s)
	}
	if dest == nil {
		return nil
	}
	return json.Unmarshal(raw, dest)
}
