// Package exchangex defines the venue interface and driver registry.
package exchangex

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Market kinds.
const (
	MarketSpot  = "spot"
	MarketUSDM  = "usdm"
	MarketPerp  = "perp"
)

// Side of an order.
const (
	SideBuy  = "buy"
	SideSell = "sell"
)

// Exchange is a trading venue driver.
type Exchange interface {
	Name() string
	Market() string
	// Ticker returns the last price for symbol.
	Ticker(ctx context.Context, symbol string) (Ticker, error)
	// Balances returns non-zero balances.
	Balances(ctx context.Context) ([]Balance, error)
	// Positions returns open positions (futures/perps); spot may return nil.
	Positions(ctx context.Context) ([]Position, error)
	// OpenOrders lists open orders for symbol (empty = all).
	OpenOrders(ctx context.Context, symbol string) ([]Order, error)
	// PlaceOrder submits an order. Paper drivers must not hit signed endpoints.
	PlaceOrder(ctx context.Context, req PlaceReq) (Order, error)
	// CancelOrder cancels by exchange id or client oid.
	CancelOrder(ctx context.Context, symbol, orderID, clientOID string) error
	// SetLeverage sets leverage for symbol (futures). Spot may no-op.
	SetLeverage(ctx context.Context, symbol string, lev float64) error
	// Permissions probes key capabilities when the venue supports it.
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

// Position is a futures/perp position.
type Position struct {
	Symbol            string
	Side              string
	Size              float64
	EntryPrice        float64
	Leverage          float64
	LiquidationPrice  float64
	UnrealizedPNL     float64
}

// Order is an exchange order.
type Order struct {
	ID        string
	ClientOID string
	Symbol    string
	Side      string
	Price     float64
	Qty       float64
	Filled    float64
	Status    string
	ReduceOnly bool
}

// PlaceReq is a new order request.
type PlaceReq struct {
	Symbol     string
	Side       string
	Price      float64
	Qty        float64
	ClientOID  string
	ReduceOnly bool
	PostOnly   bool
}

// Permissions describes API key capabilities.
type Permissions struct {
	CanTrade     bool
	CanWithdraw  bool
	CanRead      bool
	Raw          string
}

var (
	regMu sync.RWMutex
	ctors = map[string]Factory{}
)

// Factory builds an Exchange from config.
type Factory func(cfg Config) (Exchange, error)

// Config for opening a driver.
type Config struct {
	Venue     string
	Market    string
	APIKey    string
	APISecret string // or wallet private key for DEX
	Testnet   bool
	Paper     bool // public data only; never signed orders
}

// Register adds a venue factory.
func Register(venue string, f Factory) {
	regMu.Lock()
	defer regMu.Unlock()
	ctors[venue] = f
}

// Open constructs a driver by venue name.
func Open(cfg Config) (Exchange, error) {
	regMu.RLock()
	f, ok := ctors[cfg.Venue]
	regMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("exchangex: unknown venue %q", cfg.Venue)
	}
	return f(cfg)
}
