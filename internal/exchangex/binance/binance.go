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

// Driver talks to Binance REST.
type Driver struct {
	cfg    exchangex.Config
	market string
	base   string
	client *http.Client
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

func (d *Driver) Balances(ctx context.Context) ([]exchangex.Balance, error) {
	if d.cfg.Paper {
		return nil, fmt.Errorf("binance: balances require credentials (not available in paper public mode)")
	}
	if d.market == exchangex.MarketUSDM {
		var raw []struct {
			Asset string `json:"asset"`
			Balance string `json:"balance"`
			AvailableBalance string `json:"availableBalance"`
		}
		if err := d.signed(ctx, http.MethodGet, "/fapi/v2/balance", nil, &raw); err != nil {
			return nil, err
		}
		var out []exchangex.Balance
		for _, r := range raw {
			free, _ := strconv.ParseFloat(r.AvailableBalance, 64)
			total, _ := strconv.ParseFloat(r.Balance, 64)
			if total == 0 {
				continue
			}
			out = append(out, exchangex.Balance{Asset: r.Asset, Free: free, Locked: total - free})
		}
		return out, nil
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
		free, _ := strconv.ParseFloat(b.Free, 64)
		locked, _ := strconv.ParseFloat(b.Locked, 64)
		if free == 0 && locked == 0 {
			continue
		}
		out = append(out, exchangex.Balance{Asset: b.Asset, Free: free, Locked: locked})
	}
	return out, nil
}

func (d *Driver) Positions(ctx context.Context) ([]exchangex.Position, error) {
	if d.market != exchangex.MarketUSDM {
		return nil, nil
	}
	if d.cfg.Paper {
		return nil, nil
	}
	var raw []struct {
		Symbol           string `json:"symbol"`
		PositionAmt      string `json:"positionAmt"`
		EntryPrice       string `json:"entryPrice"`
		Leverage         string `json:"leverage"`
		LiquidationPrice string `json:"liquidationPrice"`
		UnrealizedProfit string `json:"unRealizedProfit"`
	}
	if err := d.signed(ctx, http.MethodGet, "/fapi/v2/positionRisk", nil, &raw); err != nil {
		return nil, err
	}
	var out []exchangex.Position
	for _, r := range raw {
		size, _ := strconv.ParseFloat(r.PositionAmt, 64)
		if size == 0 {
			continue
		}
		side := exchangex.SideBuy
		if size < 0 {
			side = exchangex.SideSell
			size = -size
		}
		entry, _ := strconv.ParseFloat(r.EntryPrice, 64)
		lev, _ := strconv.ParseFloat(r.Leverage, 64)
		liq, _ := strconv.ParseFloat(r.LiquidationPrice, 64)
		upnl, _ := strconv.ParseFloat(r.UnrealizedProfit, 64)
		out = append(out, exchangex.Position{
			Symbol: r.Symbol, Side: side, Size: size, EntryPrice: entry,
			Leverage: lev, LiquidationPrice: liq, UnrealizedPNL: upnl,
		})
	}
	return out, nil
}

func (d *Driver) OpenOrders(ctx context.Context, symbol string) ([]exchangex.Order, error) {
	if d.cfg.Paper {
		return nil, nil
	}
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
		px, _ := strconv.ParseFloat(r.Price, 64)
		qty, _ := strconv.ParseFloat(r.OrigQty, 64)
		filled, _ := strconv.ParseFloat(r.ExecutedQty, 64)
		out = append(out, exchangex.Order{
			ID: strconv.FormatInt(r.OrderID, 10), ClientOID: r.ClientOrderID,
			Symbol: r.Symbol, Side: strings.ToLower(r.Side), Price: px, Qty: qty,
			Filled: filled, Status: strings.ToLower(r.Status), ReduceOnly: r.ReduceOnly,
		})
	}
	return out, nil
}

func (d *Driver) PlaceOrder(ctx context.Context, req exchangex.PlaceReq) (exchangex.Order, error) {
	if d.cfg.Paper {
		return exchangex.Order{}, fmt.Errorf("binance: paper mode refuses signed PlaceOrder")
	}
	q := url.Values{
		"symbol":           {req.Symbol},
		"side":             {strings.ToUpper(req.Side)},
		"type":             {"LIMIT"},
		"timeInForce":      {"GTC"},
		"quantity":         {formatFloat(req.Qty)},
		"price":            {formatFloat(req.Price)},
		"newClientOrderId": {req.ClientOID},
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
		Symbol        string `json:"symbol"`
		Side          string `json:"side"`
		Price         string `json:"price"`
		OrigQty       string `json:"origQty"`
		Status        string `json:"status"`
	}
	if err := d.signed(ctx, http.MethodPost, path, q, &raw); err != nil {
		return exchangex.Order{}, err
	}
	px, _ := strconv.ParseFloat(raw.Price, 64)
	qty, _ := strconv.ParseFloat(raw.OrigQty, 64)
	return exchangex.Order{
		ID: strconv.FormatInt(raw.OrderID, 10), ClientOID: raw.ClientOrderID,
		Symbol: raw.Symbol, Side: strings.ToLower(raw.Side), Price: px, Qty: qty,
		Status: strings.ToLower(raw.Status), ReduceOnly: req.ReduceOnly,
	}, nil
}

func (d *Driver) CancelOrder(ctx context.Context, symbol, orderID, clientOID string) error {
	if d.cfg.Paper {
		return fmt.Errorf("binance: paper mode refuses signed CancelOrder")
	}
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
	if d.market != exchangex.MarketUSDM || d.cfg.Paper {
		return nil
	}
	q := url.Values{
		"symbol":   {symbol},
		"leverage": {strconv.Itoa(int(lev))},
	}
	return d.signed(ctx, http.MethodPost, "/fapi/v1/leverage", q, &struct{}{})
}

func (d *Driver) Permissions(ctx context.Context) (exchangex.Permissions, error) {
	if d.cfg.Paper || d.cfg.APIKey == "" {
		return exchangex.Permissions{CanRead: true}, nil
	}
	// Spot account endpoint implies read+trade when it succeeds; withdrawal
	// restriction is not always exposed — document least-privilege in SECURITY.md.
	p := exchangex.Permissions{CanRead: true, CanTrade: true, CanWithdraw: false}
	if d.market == exchangex.MarketSpot {
		var raw struct {
			Permissions []string `json:"permissions"`
		}
		if err := d.signed(ctx, http.MethodGet, "/api/v3/account", nil, &raw); err != nil {
			return p, err
		}
		for _, perm := range raw.Permissions {
			if strings.EqualFold(perm, "Withdraw") {
				p.CanWithdraw = true
			}
		}
		p.Raw = strings.Join(raw.Permissions, ",")
	}
	return p, nil
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
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("binance: %s: %s", resp.Status, truncate(body))
	}
	return json.Unmarshal(body, dest)
}

func (d *Driver) signed(ctx context.Context, method, path string, q url.Values, dest any) error {
	if d.cfg.APIKey == "" || d.cfg.APISecret == "" {
		return fmt.Errorf("binance: missing API credentials")
	}
	if q == nil {
		q = url.Values{}
	}
	q.Set("timestamp", strconv.FormatInt(time.Now().UnixMilli(), 10))
	payload := q.Encode()
	mac := hmac.New(sha256.New, []byte(d.cfg.APISecret))
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))
	u := d.base + path + "?" + payload + "&signature=" + sig
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-MBX-APIKEY", d.cfg.APIKey)
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("binance: %s: %s", resp.Status, truncate(body))
	}
	if dest == nil {
		return nil
	}
	return json.Unmarshal(body, dest)
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
