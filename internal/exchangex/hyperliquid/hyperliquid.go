// Package hyperliquid implements Hyperliquid perps (info + signed trading stub).
package hyperliquid

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
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
		cfg:      cfg,
		infoURL:  info,
		exchURL:  exch,
		client:   &http.Client{Timeout: 15 * time.Second},
	}, nil
}

// Driver talks to Hyperliquid.
type Driver struct {
	cfg     exchangex.Config
	infoURL string
	exchURL string
	client  *http.Client
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
		Coin  string  `json:"coin"`
		Side  string  `json:"side"`
		LimitPx string `json:"limitPx"`
		Sz    string  `json:"sz"`
		Oid   int64   `json:"oid"`
		Cloid string  `json:"cloid"`
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
	if d.cfg.APISecret == "" {
		return exchangex.Order{}, fmt.Errorf("hyperliquid: missing wallet private key (secret)")
	}
	// Full L1 signing is venue-specific; MVP returns a clear error directing
	// operators to paper mode until the signer lands for their network.
	return exchangex.Order{}, fmt.Errorf("hyperliquid: live PlaceOrder signing not enabled in this build; use paper mode or contribute the EIP-712 signer")
}

func (d *Driver) CancelOrder(ctx context.Context, symbol, orderID, clientOID string) error {
	if d.cfg.Paper {
		return fmt.Errorf("hyperliquid: paper mode refuses signed CancelOrder")
	}
	return fmt.Errorf("hyperliquid: live CancelOrder signing not enabled in this build")
}

func (d *Driver) SetLeverage(ctx context.Context, symbol string, lev float64) error {
	if d.cfg.Paper {
		return nil
	}
	return fmt.Errorf("hyperliquid: live SetLeverage signing not enabled in this build")
}

func (d *Driver) Permissions(ctx context.Context) (exchangex.Permissions, error) {
	return exchangex.Permissions{
		CanRead: true, CanTrade: d.cfg.APISecret != "" && !d.cfg.Paper,
		CanWithdraw: false,
		Raw:         "wallet",
	}, nil
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
