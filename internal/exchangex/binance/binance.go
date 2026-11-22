// Package binance implements Binance spot and USDT-M futures drivers.
package binance

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sgnd/cuwan/internal/exchangex"
)

func init() {
	exchangex.Register("binance", Open)
}

// Open builds a Binance driver.
func Open(cfg exchangex.Config) (exchangex.Exchange, error) {
	market := cfg.Market
	if market == "" {
		market = exchangex.MarketSpot
	}
	base := spotBase(cfg.Testnet)
	if market == exchangex.MarketUSDM {
		base = futuresBase(cfg.Testnet)
	}
	return &Driver{
		cfg:    cfg,
		market: market,
		base:   base,
		client: &http.Client{Timeout: 15 * time.Second},
	}, nil
}

func spotBase(testnet bool) string {
	if testnet {
		return "https://testnet.binance.vision"
	}
	return "https://api.binance.com"
}

func futuresBase(testnet bool) string {
	if testnet {
		return "https://testnet.binancefuture.com"
	}
	return "https://fapi.binance.com"
}

// codeNoSuchOrder is Binance's "Order does not exist." error code.
const codeNoSuchOrder = -2013

// codeUnknownOrder is Binance's "Unknown order sent." (cancel of a missing order).
const codeUnknownOrder = -2011

// Driver talks to Binance REST.
type Driver struct {
	cfg    exchangex.Config
	market string
	base   string
	client *http.Client
	// sapiBase overrides the /sapi host in tests.
	sapiBase string

	rulesMu sync.Mutex
	rules   map[string]exchangex.SymbolRules
}

func (d *Driver) Name() string   { return "binance" }
func (d *Driver) Market() string { return d.market }

func (d *Driver) Ticker(ctx context.Context, symbol string) (exchangex.Ticker, error) {
	path := "/api/v3/ticker/price"
	if d.market == exchangex.MarketUSDM {
		path = "/fapi/v1/ticker/price"
	}
	var raw struct {
		Symbol string `json:"symbol"`
		Price  string `json:"price"`
	}
	if err := d.getPublic(ctx, path, url.Values{"symbol": {symbol}}, &raw); err != nil {
		return exchangex.Ticker{}, err
	}
	px, err := strconv.ParseFloat(raw.Price, 64)
	if err != nil {
		return exchangex.Ticker{}, err
	}
	return exchangex.Ticker{Symbol: symbol, Price: px, Time: time.Now().UTC()}, nil
}

// MarkPrice is the USD-M mark price (premiumIndex); spot has none, so it is
// the last price there.
func (d *Driver) MarkPrice(ctx context.Context, symbol string) (float64, error) {
	if d.market != exchangex.MarketUSDM {
		t, err := d.Ticker(ctx, symbol)
		return t.Price, err
	}
	var raw struct {
		MarkPrice string `json:"markPrice"`
	}
	if err := d.getPublic(ctx, "/fapi/v1/premiumIndex", url.Values{"symbol": {symbol}}, &raw); err != nil {
		return 0, err
	}
	var p parser
	px := p.f(raw.MarkPrice)
	if p.err == nil && px <= 0 {
		p.err = fmt.Errorf("binance: mark price %v for %s", px, symbol)
	}
	return px, p.err
}

func (d *Driver) Balances(ctx context.Context) ([]exchangex.Balance, error) {
	var p parser
	if d.market == exchangex.MarketUSDM {
		var raw []struct {
			Asset            string `json:"asset"`
			Balance          string `json:"balance"`
			AvailableBalance string `json:"availableBalance"`
		}
		if err := d.signed(ctx, http.MethodGet, "/fapi/v2/balance", nil, &raw); err != nil {
			return nil, err
		}
		var out []exchangex.Balance
		for _, r := range raw {
			free := p.f(r.AvailableBalance)
			total := p.f(r.Balance)
			if total == 0 {
				continue
			}
			out = append(out, exchangex.Balance{Asset: r.Asset, Free: free, Locked: total - free})
		}
		return out, p.err
	}
	var raw struct {
		Balances []struct {
			Asset  string `json:"asset"`
			Free   string `json:"free"`
			Locked string `json:"locked"`
		} `json:"balances"`
	}
	if err := d.signed(ctx, http.MethodGet, "/api/v3/account", nil, &raw); err != nil {
		return nil, err
	}
	var out []exchangex.Balance
	for _, b := range raw.Balances {
		free := p.f(b.Free)
		locked := p.f(b.Locked)
		if free == 0 && locked == 0 {
			continue
		}
		out = append(out, exchangex.Balance{Asset: b.Asset, Free: free, Locked: locked})
	}
	return out, p.err
}

func (d *Driver) Positions(ctx context.Context) ([]exchangex.Position, error) {
	var p parser
	if d.market != exchangex.MarketUSDM {
		return nil, nil
	}
	var raw []struct {
		Symbol           string `json:"symbol"`
		PositionAmt      string `json:"positionAmt"`
		EntryPrice       string `json:"entryPrice"`
		Leverage         string `json:"leverage"`
		LiquidationPrice string `json:"liquidationPrice"`
		UnrealizedProfit string `json:"unRealizedProfit"`
		PositionSide     string `json:"positionSide"`
	}
	if err := d.signed(ctx, http.MethodGet, "/fapi/v2/positionRisk", nil, &raw); err != nil {
		return nil, err
	}
	var out []exchangex.Position
	for _, r := range raw {
		// Hedge mode splits a symbol into LONG and SHORT legs; cuwan's
		// net-size checks need one-way mode.
		if r.PositionSide != "" && r.PositionSide != "BOTH" {
			return nil, fmt.Errorf("binance: hedge mode (positionSide %s) is not supported; switch the account to one-way mode", r.PositionSide)
		}
		size := p.f(r.PositionAmt)
		if size == 0 {
			continue
		}
		side := exchangex.SideBuy
		if size < 0 {
			side = exchangex.SideSell
			size = -size
		}
		entry := p.f(r.EntryPrice)
		lev := p.f(r.Leverage)
		liq := p.f(r.LiquidationPrice)
		upnl := p.f(r.UnrealizedProfit)
		out = append(out, exchangex.Position{
			Symbol: r.Symbol, Side: side, Size: size, EntryPrice: entry,
			Leverage: lev, LiquidationPrice: liq, UnrealizedPNL: upnl,
		})
	}
	return out, p.err
}

func (d *Driver) OpenOrders(ctx context.Context, symbol string) ([]exchangex.Order, error) {
	var p parser
	q := url.Values{}
	if symbol != "" {
		q.Set("symbol", symbol)
	}
	path := "/api/v3/openOrders"
	if d.market == exchangex.MarketUSDM {
		path = "/fapi/v1/openOrders"
	}
	var raw []struct {
		OrderID       int64  `json:"orderId"`
		ClientOrderID string `json:"clientOrderId"`
		Symbol        string `json:"symbol"`
		Side          string `json:"side"`
		Price         string `json:"price"`
		OrigQty       string `json:"origQty"`
		ExecutedQty   string `json:"executedQty"`
		Status        string `json:"status"`
		ReduceOnly    bool   `json:"reduceOnly"`
	}
	if err := d.signed(ctx, http.MethodGet, path, q, &raw); err != nil {
		return nil, err
	}
	out := make([]exchangex.Order, 0, len(raw))
	for _, r := range raw {
		px := p.f(r.Price)
		qty := p.f(r.OrigQty)
		filled := p.f(r.ExecutedQty)
		out = append(out, exchangex.Order{
			ID: strconv.FormatInt(r.OrderID, 10), ClientOID: r.ClientOrderID,
			Symbol: r.Symbol, Side: strings.ToLower(r.Side), Price: px, Qty: qty,
			Filled: filled, Status: normStatus(r.Status), ReduceOnly: r.ReduceOnly,
		})
	}
	return out, p.err
}

func (d *Driver) PlaceOrder(ctx context.Context, req exchangex.PlaceReq) (exchangex.Order, error) {
	q := url.Values{
		"symbol":           {req.Symbol},
		"side":             {strings.ToUpper(req.Side)},
		"type":             {"LIMIT"},
		"timeInForce":      {"GTC"},
		"quantity":         {formatFloat(req.Qty)},
		"price":            {formatFloat(req.Price)},
		"newClientOrderId": {req.ClientOID},
	}
	if req.IOC {
		q.Set("timeInForce", "IOC")
	}
	path := "/api/v3/order"
	if d.market == exchangex.MarketUSDM {
		path = "/fapi/v1/order"
		if req.ReduceOnly {
			q.Set("reduceOnly", "true")
		}
	}
	var raw struct {
		OrderID       int64  `json:"orderId"`
		ClientOrderID string `json:"clientOrderId"`
		Status        string `json:"status"`
	}
	if err := d.signed(ctx, http.MethodPost, path, q, &raw); err != nil {
		return exchangex.Order{}, err
	}
	// The engine keeps its own price and size; only the ID and state count.
	return exchangex.Order{
		ID: strconv.FormatInt(raw.OrderID, 10), ClientOID: raw.ClientOrderID,
		Symbol: req.Symbol, Side: req.Side, Price: req.Price, Qty: req.Qty,
		Status: normStatus(raw.Status), ReduceOnly: req.ReduceOnly,
	}, nil
}

func (d *Driver) CancelOrder(ctx context.Context, symbol, orderID, clientOID string) error {
	q := url.Values{"symbol": {symbol}}
	if orderID != "" {
		q.Set("orderId", orderID)
	}
	if clientOID != "" {
		q.Set("origClientOrderId", clientOID)
	}
	path := "/api/v3/order"
	if d.market == exchangex.MarketUSDM {
		path = "/fapi/v1/order"
	}
	return d.signed(ctx, http.MethodDelete, path, q, &struct{}{})
}

func (d *Driver) SetLeverage(ctx context.Context, symbol string, lev float64) error {
	if d.market != exchangex.MarketUSDM {
		return nil
	}
	q := url.Values{
		"symbol":   {symbol},
		"leverage": {strconv.Itoa(int(lev))},
	}
	return d.signed(ctx, http.MethodPost, "/fapi/v1/leverage", q, &struct{}{})
}

// Permissions reports whether the key can withdraw. Mainnet keys are checked
// with /sapi/v1/account/apiRestrictions (the same endpoint for spot and USD-M
// keys). Testnet keys hold no real funds, so they are reported as trade-only.
func (d *Driver) Permissions(ctx context.Context) (exchangex.Permissions, error) {
	if d.cfg.APIKey == "" {
		return exchangex.Permissions{CanRead: true}, nil
	}
	if d.cfg.Testnet {
		return exchangex.Permissions{CanRead: true, CanTrade: true, Raw: "testnet"}, nil
	}
	var raw struct {
		EnableReading        bool `json:"enableReading"`
		EnableSpot           bool `json:"enableSpotAndMarginTrading"`
		EnableFutures        bool `json:"enableFutures"`
		EnableWithdrawals    bool `json:"enableWithdrawals"`
		EnableInternalTransf bool `json:"enableInternalTransfer"`
		PermitsUniversalTran bool `json:"permitsUniversalTransfer"`
	}
	if err := d.signedAt(ctx, d.restrictionsBase(), http.MethodGet, "/sapi/v1/account/apiRestrictions", nil, &raw); err != nil {
		return exchangex.Permissions{}, err
	}
	trade := raw.EnableSpot
	if d.market == exchangex.MarketUSDM {
		trade = raw.EnableFutures
	}
	return exchangex.Permissions{
		CanRead:  raw.EnableReading,
		CanTrade: trade,
		// Internal and universal transfers also move funds off the bot's wallet.
		CanWithdraw: raw.EnableWithdrawals || raw.EnableInternalTransf || raw.PermitsUniversalTran,
		Raw:         fmt.Sprintf("withdraw=%v internal_transfer=%v universal_transfer=%v", raw.EnableWithdrawals, raw.EnableInternalTransf, raw.PermitsUniversalTran),
	}, nil
}

// restrictionsBase is the spot API host, where /sapi lives for every key.
func (d *Driver) restrictionsBase() string {
	if d.sapiBase != "" {
		return d.sapiBase
	}
	return spotBase(false)
}

// Rules reads the symbol's PRICE_FILTER and LOT_SIZE from exchangeInfo and
// caches them for the life of the driver.
func (d *Driver) Rules(ctx context.Context, symbol string) (exchangex.SymbolRules, error) {
	var p parser
	d.rulesMu.Lock()
	defer d.rulesMu.Unlock()
	if r, ok := d.rules[symbol]; ok {
		return r, nil
	}
	path, q := "/api/v3/exchangeInfo", url.Values{"symbol": {symbol}}
	if d.market == exchangex.MarketUSDM {
		path, q = "/fapi/v1/exchangeInfo", nil // no per-symbol filter here
	}
	var raw struct {
		Symbols []struct {
			Symbol    string `json:"symbol"`
			BaseAsset string `json:"baseAsset"`
			Filters   []struct {
				Type     string `json:"filterType"`
				TickSize string `json:"tickSize"`
				StepSize string `json:"stepSize"`
				MinQty   string `json:"minQty"`
				// spot NOTIONAL and MIN_NOTIONAL use minNotional; USD-M MIN_NOTIONAL uses notional
				MinNotional string `json:"minNotional"`
				Notional    string `json:"notional"`
			} `json:"filters"`
		} `json:"symbols"`
	}
	if err := d.getPublic(ctx, path, q, &raw); err != nil {
		return exchangex.SymbolRules{}, err
	}
	for _, sym := range raw.Symbols {
		if sym.Symbol != symbol {
			continue
		}
		r := exchangex.SymbolRules{Base: sym.BaseAsset}
		for _, f := range sym.Filters {
			switch f.Type {
			case "PRICE_FILTER":
				r.Tick = p.f(f.TickSize)
			case "LOT_SIZE":
				r.Step = p.f(f.StepSize)
				r.MinQty = p.f(f.MinQty)
			case "NOTIONAL", "MIN_NOTIONAL":
				v := f.MinNotional
				if v == "" {
					v = f.Notional
				}
				if v != "" {
					r.MinNotional = p.f(v)
				}
			}
		}
		if r.Tick <= 0 || r.Step <= 0 {
			return exchangex.SymbolRules{}, fmt.Errorf("binance: %s has no PRICE_FILTER or LOT_SIZE", symbol)
		}
		if d.rules == nil {
			d.rules = map[string]exchangex.SymbolRules{}
		}
		if p.err != nil {
			return exchangex.SymbolRules{}, p.err
		}
		d.rules[symbol] = r
		return r, nil
	}
	return exchangex.SymbolRules{}, fmt.Errorf("binance: unknown symbol %s", symbol)
}

// OrderStatus reports one order by exchange ID or client order ID.
func (d *Driver) OrderStatus(ctx context.Context, symbol, orderID, clientOID string) (exchangex.Order, error) {
	var p parser
	q := url.Values{"symbol": {symbol}}
	if orderID != "" {
		q.Set("orderId", orderID)
	} else if clientOID != "" {
		q.Set("origClientOrderId", clientOID)
	} else {
		return exchangex.Order{}, fmt.Errorf("binance: OrderStatus needs an order ID")
	}
	path := "/api/v3/order"
	if d.market == exchangex.MarketUSDM {
		path = "/fapi/v1/order"
	}
	var r struct {
		OrderID       int64  `json:"orderId"`
		ClientOrderID string `json:"clientOrderId"`
		Symbol        string `json:"symbol"`
		Side          string `json:"side"`
		Price         string `json:"price"`
		OrigQty       string `json:"origQty"`
		ExecutedQty   string `json:"executedQty"`
		Status        string `json:"status"`
	}
	if err := d.signed(ctx, http.MethodGet, path, q, &r); err != nil {
		return exchangex.Order{}, err
	}
	px := p.f(r.Price)
	qty := p.f(r.OrigQty)
	filled := p.f(r.ExecutedQty)
	return exchangex.Order{
		ID: strconv.FormatInt(r.OrderID, 10), ClientOID: r.ClientOrderID,
		Symbol: r.Symbol, Side: strings.ToLower(r.Side), Price: px, Qty: qty,
		Filled: filled, Status: normStatus(r.Status),
	}, p.err
}

// normStatus maps Binance order states to the runtime states cuwan uses:
// open, filled, canceled. A partly filled order is still open.
func normStatus(s string) string {
	switch strings.ToUpper(s) {
	case "NEW", "PARTIALLY_FILLED", "PENDING_NEW", "PENDING_CANCEL":
		return "open"
	case "FILLED":
		return "filled"
	case "CANCELED", "EXPIRED", "EXPIRED_IN_MATCH", "REJECTED":
		return "canceled"
	}
	return "unknown"
}

func (d *Driver) getPublic(ctx context.Context, path string, q url.Values, dest any) error {
	u := d.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return d.clean(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return d.clean(err)
	}
	if resp.StatusCode >= 300 {
		return d.clean(fmt.Errorf("binance: %s: %s", resp.Status, truncate(body)))
	}
	return json.Unmarshal(body, dest)
}

func (d *Driver) signed(ctx context.Context, method, path string, q url.Values, dest any) error {
	return d.signedAt(ctx, d.base, method, path, q, dest)
}

func (d *Driver) signedAt(ctx context.Context, base, method, path string, q url.Values, dest any) error {
	if d.cfg.APIKey == "" || d.cfg.APISecret == "" {
		return fmt.Errorf("binance: missing API credentials")
	}
	if q == nil {
		q = url.Values{}
	}
	q.Set("timestamp", strconv.FormatInt(time.Now().UnixMilli(), 10))
	q.Set("recvWindow", "5000")
	payload := q.Encode()
	mac := hmac.New(sha256.New, []byte(d.cfg.APISecret))
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))
	u := base + path + "?" + payload + "&signature=" + sig
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-MBX-APIKEY", d.cfg.APIKey)
	resp, err := d.client.Do(req)
	if err != nil {
		return d.clean(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return d.clean(err)
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Code int `json:"code"`
		}
		if json.Unmarshal(body, &e) == nil && (e.Code == codeNoSuchOrder || e.Code == codeUnknownOrder) {
			return fmt.Errorf("binance: %w", exchangex.ErrOrderNotFound)
		}
		return d.clean(fmt.Errorf("binance: %s: %s", resp.Status, truncate(body)))
	}
	if dest == nil {
		return nil
	}
	return json.Unmarshal(body, dest)
}

// clean removes the key, the secret and the signed query from err.
func (d *Driver) clean(err error) error {
	return exchangex.CleanErr(err, d.cfg.APIKey, d.cfg.APISecret)
}

// parser reads venue numbers and keeps the first one that does not parse,
// so a bad answer is an error, never a silent 0.
type parser struct{ err error }

func (p *parser) f(v string) float64 {
	x, err := strconv.ParseFloat(v, 64)
	if err != nil && p.err == nil {
		p.err = fmt.Errorf("binance: bad number %q in the answer", truncate([]byte(v)))
	}
	return x
}

func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func truncate(b []byte) string {
	s := string(b)
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}
