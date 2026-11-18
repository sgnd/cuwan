// Package exchangex defines the venue interface and driver registry.
package exchangex

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Market kinds.
const (
	MarketSpot = "spot"
	MarketUSDM = "usdm"
	MarketPerp = "perp"
)

// Side of an order.
const (
	SideBuy  = "buy"
	SideSell = "sell"
)

// Venue kinds.
const (
	KindCEX = "cex"
	KindDEX = "dex"
)

// Backend labels for registry metadata.
const (
	BackendNative   = "native"
	BackendPublic   = "public"
	BackendRegistry = "registry"
)

// ErrOrderNotFound is returned by OrderStatus when the venue has no order
// with that ID. Drivers wrap it so callers can use errors.Is.
var ErrOrderNotFound = errors.New("order not found")

// SymbolRules are a symbol's order filters: price tick, size step and the
// smallest size the venue accepts.
type SymbolRules struct {
	Tick   float64
	Step   float64
	MinQty float64
	// MinNotional is the smallest price * size the venue accepts.
	MinNotional float64
	// SigFigs, when set, limits prices to that many significant figures,
	// so the tick depends on the price (Hyperliquid). Tick is then the
	// smallest tick allowed by the decimals limit.
	SigFigs int
}

// RulesProvider is an optional driver interface that reports SymbolRules.
type RulesProvider interface {
	Rules(ctx context.Context, symbol string) (SymbolRules, error)
}

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
	Symbol           string
	Side             string
	Size             float64
	EntryPrice       float64
	Leverage         float64
	LiquidationPrice float64
	UnrealizedPNL    float64
}

// Order is an exchange order.
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

// PlaceReq is a new order request.
type PlaceReq struct {
	Symbol     string
	Side       string
	Price      float64
	Qty        float64
	ClientOID  string
	ReduceOnly bool
	PostOnly   bool
	// IOC fills what it can at once and cancels the rest.
	IOC bool
}

// Permissions describes API key capabilities.
type Permissions struct {
	CanTrade    bool
	CanWithdraw bool
	CanRead     bool
	Raw         string
}

// VenueInfo describes a registered trading venue.
type VenueInfo struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"`
	Markets      []string `json:"markets"`
	Backend      string   `json:"backend"`
	PublicTicker bool     `json:"public_ticker"`
	Live         string   `json:"live"` // full | paper-only | stub
}

var (
	regMu sync.RWMutex
	ctors = map[string]Factory{}
	metas = map[string]VenueInfo{}
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

// Register adds a venue factory (overwrites existing factory for the same id).
func Register(venue string, f Factory) {
	regMu.Lock()
	defer regMu.Unlock()
	ctors[venue] = f
	m, ok := metas[venue]
	if !ok {
		m = VenueInfo{ID: venue, Kind: KindCEX, Markets: []string{MarketSpot}, Backend: BackendNative, Live: "stub"}
	}
	metas[venue] = m
}

// RegisterMeta stores/overrides venue metadata and optionally its factory.
func RegisterMeta(info VenueInfo, f Factory) {
	regMu.Lock()
	defer regMu.Unlock()
	if info.ID == "" {
		return
	}
	metas[info.ID] = info
	if f != nil {
		ctors[info.ID] = f
	}
}

// SetBackend updates the backend label after a native driver overrides a catalog entry.
func SetBackend(venue, backend, live string) {
	regMu.Lock()
	defer regMu.Unlock()
	m, ok := metas[venue]
	if !ok {
		return
	}
	m.Backend = backend
	if live != "" {
		m.Live = live
	}
	metas[venue] = m
}

// Open constructs a driver by venue name.
func Open(cfg Config) (Exchange, error) {
	regMu.RLock()
	f, ok := ctors[cfg.Venue]
	regMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("exchangex: unknown venue %q (run: cuwan exchange venues)", cfg.Venue)
	}
	return f(cfg)
}

// Has reports whether venue is registered.
func Has(venue string) bool {
	regMu.RLock()
	defer regMu.RUnlock()
	_, ok := ctors[venue]
	return ok
}

// Lookup returns venue metadata.
func Lookup(venue string) (VenueInfo, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	m, ok := metas[venue]
	return m, ok
}

// Venues returns all registered venues sorted by id.
func Venues() []VenueInfo {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]VenueInfo, 0, len(metas))
	for _, m := range metas {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// VenueCount returns the number of registered venue ids.
func VenueCount() int {
	regMu.RLock()
	defer regMu.RUnlock()
	return len(metas)
}
