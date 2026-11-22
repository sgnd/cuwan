// Package exchangex defines the venue driver interface. cuwan has two
// drivers: Binance (spot and USD-M, live) and Hyperliquid (perps, testnet
// only until its testnet run is done).
package exchangex

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Markets.
const (
	MarketSpot = "spot"
	MarketUSDM = "usdm"
	MarketPerp = "perp"
)

// Sides.
const (
	SideBuy  = "buy"
	SideSell = "sell"
)

// Order states the engine uses.
const (
	StatusOpen     = "open"
	StatusFilled   = "filled"
	StatusCanceled = "canceled"
	StatusUnknown  = "unknown"
)

// ErrOrderNotFound means the venue has no order with that ID. Drivers wrap it.
var ErrOrderNotFound = errors.New("order not found")

// Exchange is a venue driver. Every method that sends a key goes through the
// driver's error cleaning (CleanErr), so no error holds a key or signature.
type Exchange interface {
	Name() string
	Market() string
	// Ticker is the last traded price.
	Ticker(ctx context.Context, symbol string) (Ticker, error)
	// MarkPrice is the price the venue liquidates on (the last price on spot).
	MarkPrice(ctx context.Context, symbol string) (float64, error)
	Balances(ctx context.Context) ([]Balance, error)
	// Positions lists open futures positions; spot returns none.
	Positions(ctx context.Context) ([]Position, error)
	OpenOrders(ctx context.Context, symbol string) ([]Order, error)
	PlaceOrder(ctx context.Context, req PlaceReq) (Order, error)
	CancelOrder(ctx context.Context, symbol, orderID, clientOID string) error
	// OrderStatus reports one order by exchange ID or client order ID, or an
	// error wrapping ErrOrderNotFound.
	OrderStatus(ctx context.Context, symbol, orderID, clientOID string) (Order, error)
	// Rules are the symbol's price tick, size step and minimums.
	Rules(ctx context.Context, symbol string) (SymbolRules, error)
	SetLeverage(ctx context.Context, symbol string, lev float64) error
	// Permissions says whether the key can withdraw.
	Permissions(ctx context.Context) (Permissions, error)
}

// Ticker is a last-price snapshot.
type Ticker struct {
	Symbol string
	Price  float64
	Time   time.Time
}

// Balance is an asset balance.
type Balance struct {
	Asset  string
	Free   float64
	Locked float64
}

// Position is a futures position. LiquidationPrice is the venue's own
// figure, 0 when it gave none.
type Position struct {
	Symbol           string
	Side             string
	Size             float64
	EntryPrice       float64
	Leverage         float64
	LiquidationPrice float64
	UnrealizedPNL    float64
}

// Order is a venue order.
type Order struct {
	ID         string
	ClientOID  string
	Symbol     string
	Side       string
	Price      float64
	Qty        float64
	Filled     float64
	Status     string
	ReduceOnly bool
}

// PlaceReq is a new limit order.
type PlaceReq struct {
	Symbol     string
	Side       string
	Price      float64
	Qty        float64
	ClientOID  string
	ReduceOnly bool
	// IOC fills what it can at once and cancels the rest.
	IOC bool
}

// SymbolRules are a symbol's order filters.
type SymbolRules struct {
	Tick        float64
	Step        float64
	MinQty      float64
	MinNotional float64
	// SigFigs, when set, limits prices to that many significant figures
	// (Hyperliquid).
	SigFigs int
}

// Permissions describes what an API key may do.
type Permissions struct {
	CanTrade    bool
	CanWithdraw bool
	CanRead     bool
	Raw         string
}

// Config opens a driver.
type Config struct {
	Venue     string
	Market    string
	APIKey    string
	APISecret string // the wallet private key on Hyperliquid
	Testnet   bool
}

// Factory builds a driver.
type Factory func(cfg Config) (Exchange, error)

var (
	mu        sync.RWMutex
	factories = map[string]Factory{}
)

// Register adds a venue's driver.
func Register(venue string, f Factory) {
	mu.Lock()
	defer mu.Unlock()
	factories[venue] = f
}

// Open builds the driver for cfg.Venue.
func Open(cfg Config) (Exchange, error) {
	mu.RLock()
	f, ok := factories[cfg.Venue]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("exchangex: unknown venue %q (binance or hyperliquid)", cfg.Venue)
	}
	return f(cfg)
}

// TestnetOnly reports whether a venue may only trade on its testnet.
// Hyperliquid stays testnet-only until its testnet run is recorded.
func TestnetOnly(venue string) bool { return venue == "hyperliquid" }
