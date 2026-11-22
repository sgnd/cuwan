// Package live runs a grid bot against a venue driver.
//
// Rules the engine keeps:
//   - Every order is saved as "pending" before it is sent, and saved again
//     once the venue answers. A pending order is found again by its client
//     order ID, so a timed-out send is never lost.
//   - An order that left the book counts as filled only when the venue says
//     so (OrderStatus).
//   - The guard runs first on every step, before any exit setting, and has
//     no switch. A failed protective close is a CloseError: the caller halts
//     the bot and tells a person.
package live

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/sgnd/cuwan/internal/exchangex"
	"github.com/sgnd/cuwan/internal/store"
)

// Order states kept in the runtime, beside the venue's open, filled,
// canceled and unknown.
const (
	StatusPending  = "pending" // saved, send not confirmed
	StatusOpen     = exchangex.StatusOpen
	StatusFilled   = exchangex.StatusFilled
	StatusCanceled = exchangex.StatusCanceled
	StatusFailed   = "failed" // the venue never got it
	StatusUnknown  = exchangex.StatusUnknown
)

const (
	// MaxLeverage is the futures leverage cap. It has no switch.
	MaxLeverage = 20.0
	// closeSlip makes a close's limit price cross the book.
	closeSlip = 0.005
	// closeAttempts is how many IOC closes are tried, each with a wider price.
	closeAttempts = 3
	// A pending order counts as never sent only after pendingMisses lookups
	// over at least pendingGrace.
	pendingMisses = 3
	pendingGrace  = 2 * time.Minute
	// eps is the size below which a position counts as flat.
	eps = 1e-12
)

// now is the clock; tests replace it.
var now = time.Now

// Saver persists the runtime. The engine calls it before and after every
// order it sends.
type Saver func() error

// CloseError means a protective close failed. Halt and alert.
type CloseError struct{ Err error }

func (e *CloseError) Error() string { return "live: protective close failed: " + e.Err.Error() }
func (e *CloseError) Unwrap() error { return e.Err }

// Result says whether the bot stopped during a step, and why.
type Result struct {
	Stopped bool
	Reason  string
}

// ErrNoSize means the bot has neither an order size nor a quote amount.
var ErrNoSize = errors.New("live: the bot has no order size; set --order-size (base asset) or --quote-amount (quote asset)")

// OrderQty is the size of one grid order at price px: the bot's order size,
// or its quote amount divided by px. There is no default size.
func OrderQty(bot *store.Bot, px float64) (float64, error) {
	switch {
	case bot.OrderSize > 0:
		return bot.OrderSize, nil
	case bot.QuoteAmount > 0 && px > 0:
		return bot.QuoteAmount / px, nil
	}
	return 0, ErrNoSize
}

// CapLeverage clamps leverage to MaxLeverage.
func CapLeverage(lev float64) float64 { return math.Min(lev, MaxLeverage) }

// clientOID is a client order ID unique to this run, price, side and count.
func clientOID(bot *store.Bot, rt *store.Runtime, px float64, side string) string {
	raw := fmt.Sprintf("cuwan|%s|%s|%.8f|%s|%d", bot.Name, RunID(rt), px, side, len(rt.Orders)+1)
	sum := sha256.Sum256([]byte(raw))
	return "cw" + hex.EncodeToString(sum[:10])
}

// RunID returns the run's random ID, making one on first use.
func RunID(rt *store.Runtime) string {
	if rt.Meta == nil {
		rt.Meta = map[string]string{}
	}
	if rt.Meta["run_id"] == "" {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		rt.Meta["run_id"] = hex.EncodeToString(b)
	}
	return rt.Meta["run_id"]
}

// gridPrices are the bot's levels from lower to upper inclusive.
func gridPrices(lo, hi float64, levels int) ([]float64, error) {
	if lo <= 0 || hi <= lo {
		return nil, fmt.Errorf("live: need 0 < lower < upper")
	}
	if levels < 2 {
		return nil, fmt.Errorf("live: levels must be at least 2")
	}
	step := (hi - lo) / float64(levels-1)
	out := make([]float64, levels)
	for i := range out {
		out[i] = lo + step*float64(i)
	}
	out[levels-1] = hi
	return out, nil
}

// place fits the order to the venue filters, saves it as pending, sends it,
// then saves the answer. crossing marks an order meant to fill at once.
func place(ctx context.Context, ex exchangex.Exchange, rt *store.Runtime, save Saver, req exchangex.PlaceReq, crossing, isClose bool, parent string) (int, error) {
	rules, err := ex.Rules(ctx, req.Symbol)
	if err != nil {
		return -1, err
	}
	if req, err = roundReq(req, rules, crossing); err != nil {
		return -1, err
	}
	rt.Orders = append(rt.Orders, store.Order{
		ClientOID: req.ClientOID, Side: req.Side, Price: req.Price, Qty: req.Qty,
		ReduceOnly: req.ReduceOnly, Close: isClose, Status: StatusPending, SentAt: now(), Parent: parent,
	})
	i := len(rt.Orders) - 1
	if err := save(); err != nil {
		return i, err // nothing sent
	}
	o, err := ex.PlaceOrder(ctx, req)
	if err != nil {
		return i, fmt.Errorf("place %s: %w (kept as pending; found again by client order ID)", req.ClientOID, err)
	}
	rt.Orders[i].ExchangeID = o.ID
	rt.Orders[i].Status = StatusOpen
	if o.Status == StatusFilled {
		rt.Orders[i].Status = StatusFilled
		rt.Orders[i].Filled = req.Qty
	}
	return i, save()
}

// PlaceGrid places the grid around mid, one order at a time.
func PlaceGrid(ctx context.Context, bot *store.Bot, ex exchangex.Exchange, rt *store.Runtime, save Saver, mid float64) error {
	lo, hi := Bounds(bot, rt)
	prices, err := gridPrices(lo, hi, bot.Levels)
	if err != nil {
		return err
	}
	for _, px := range prices {
		side := exchangex.SideSell
		if px < mid {
			side = exchangex.SideBuy
		}
		qty, err := OrderQty(bot, px)
		if err != nil {
			return err
		}
		req := exchangex.PlaceReq{Symbol: bot.Symbol, Side: side, Price: px, Qty: qty, ClientOID: clientOID(bot, rt, px, side)}
		if _, err := place(ctx, ex, rt, save, req, false, false, ""); err != nil {
			return err
		}
	}
	return nil
}

// Reconcile resolves pending orders by client order ID.
func Reconcile(ctx context.Context, bot *store.Bot, ex exchangex.Exchange, rt *store.Runtime, save Saver) error {
	changed := false
	for i := range rt.Orders {
		o := &rt.Orders[i]
		if o.Status != StatusPending {
			continue
		}
		got, err := ex.OrderStatus(ctx, bot.Symbol, "", o.ClientOID)
		if errors.Is(err, exchangex.ErrOrderNotFound) {
			// Right after a timeout the venue may not know the order yet.
			o.Misses++
			if o.Misses >= pendingMisses && now().Sub(o.SentAt) >= pendingGrace {
				o.Status = StatusFailed
				reopenParent(rt, o.Parent) // its grid level gets a new counter order
			}
			changed = true
			continue
		}
		if err != nil {
			return err
		}
		apply(o, got)
		changed = true
	}
	if changed {
		return save()
	}
	return nil
}

// apply copies a venue answer onto a runtime order.
func apply(o *store.Order, got exchangex.Order) {
	if got.ID != "" {
		o.ExchangeID = got.ID
	}
	o.Status = got.Status
	o.Filled = got.Filled
	if got.Status == StatusFilled && o.Filled == 0 {
		o.Filled = o.Qty
	}
}

// Step reconciles, runs the guard, applies the exits, and refills filled
// grid orders.
func Step(ctx context.Context, bot *store.Bot, ex exchangex.Exchange, rt *store.Runtime, save Saver) (Result, error) {
	if rt.Meta == nil {
		rt.Meta = map[string]string{}
	}
	if err := Reconcile(ctx, bot, ex, rt, save); err != nil {
		return Result{}, err
	}
	t, err := ex.Ticker(ctx, bot.Symbol)
	if err != nil {
		return Result{}, err
	}
	rt.Meta["last_price"] = fmt.Sprintf("%g", t.Price)

	reason, err := guard(ctx, bot, ex, rt, t.Price)
	if err != nil {
		return Result{}, err
	}
	if reason != "" {
		rt.Meta["guard"] = reason
		if err := Flatten(ctx, bot, ex, rt, save); err != nil {
			return Result{}, &CloseError{Err: err}
		}
		return Result{Stopped: true, Reason: reason}, nil
	}
	// Range exits run only after the guard, so no exit setting can skip it.
	if res, done, err := exits(ctx, bot, ex, rt, save, t.Price); done || err != nil {
		return res, err
	}
	if err := syncBook(ctx, bot, ex, rt, save); err != nil {
		return Result{}, err
	}
	return Result{}, refill(ctx, bot, ex, rt, save)
}

// syncBook updates open grid orders from the book, asking the venue about each
// order that left it.
func syncBook(ctx context.Context, bot *store.Bot, ex exchangex.Exchange, rt *store.Runtime, save Saver) error {
	open, err := ex.OpenOrders(ctx, bot.Symbol)
	if err != nil {
		return err
	}
	onBook := map[string]exchangex.Order{}
	for _, o := range open {
		if o.ID != "" {
			onBook["id:"+o.ID] = o
		}
		if o.ClientOID != "" {
			onBook["cid:"+o.ClientOID] = o
		}
	}
	for i := range rt.Orders {
		o := rt.Orders[i]
		if (o.Status != StatusOpen && o.Status != StatusUnknown) || o.Close {
			continue
		}
		b, ok := onBook["id:"+o.ExchangeID]
		if !ok || o.ExchangeID == "" {
			b, ok = onBook["cid:"+o.ClientOID]
			ok = ok && o.ClientOID != ""
		}
		if ok {
			rt.Orders[i].Filled = b.Filled // partial fills count toward the net size
			continue
		}
		got, err := ex.OrderStatus(ctx, bot.Symbol, o.ExchangeID, o.ClientOID)
		if err != nil {
			return err
		}
		apply(&rt.Orders[i], got)
		if err := save(); err != nil {
			return err
		}
	}
	return nil
}

// refill gives every filled grid order one counter order one level away,
// however it got filled.
func refill(ctx context.Context, bot *store.Bot, ex exchangex.Exchange, rt *store.Runtime, save Saver) error {
	step := gridStep(bot)
	for i, n := 0, len(rt.Orders); i < n; i++ {
		o := rt.Orders[i]
		if o.Status != StatusFilled || o.Close || o.Countered {
			continue
		}
		opp, px := exchangex.SideSell, o.Price+step
		if o.Side == exchangex.SideSell {
			opp, px = exchangex.SideBuy, o.Price-step
		}
		rt.Orders[i].Countered = true
		if px <= 0 {
			continue
		}
		req := exchangex.PlaceReq{Symbol: bot.Symbol, Side: opp, Price: px, Qty: o.Qty, ClientOID: clientOID(bot, rt, px, opp)}
		if _, err := place(ctx, ex, rt, save, req, false, false, o.ClientOID); err != nil {
			return err
		}
	}
	return save()
}

// reopenParent lets a grid order be countered again after its counter
// order failed.
func reopenParent(rt *store.Runtime, parent string) {
	if parent == "" {
		return
	}
	for i := range rt.Orders {
		if rt.Orders[i].ClientOID == parent {
			rt.Orders[i].Countered = false
		}
	}
}

// NetSize is the bot's own position from its fills: buys minus sells.
func NetSize(rt *store.Runtime) float64 {
	n := 0.0
	for _, o := range rt.Orders {
		if o.Side == exchangex.SideBuy {
			n += o.Filled
		} else {
			n -= o.Filled
		}
	}
	return n
}

// position returns the venue's signed position on the bot's symbol.
func position(ctx context.Context, bot *store.Bot, ex exchangex.Exchange) (exchangex.Position, error) {
	ps, err := ex.Positions(ctx)
	if err != nil {
		return exchangex.Position{}, err
	}
	for _, p := range ps {
		if p.Symbol != bot.Symbol || p.Size == 0 {
			continue
		}
		if (p.Side == "short" || p.Side == exchangex.SideSell) && p.Size > 0 {
			p.Size = -p.Size
		}
		return p, nil
	}
	return exchangex.Position{}, nil
}

// Flatten cancels the bot's open orders and closes the bot's own net size:
// reduce-only on futures, a sell of the bought base asset on spot. It
// refuses when the venue position or balance does not cover the bot's own
// size, and checks that the close filled.
func Flatten(ctx context.Context, bot *store.Bot, ex exchangex.Exchange, rt *store.Runtime, save Saver) error {
	if err := Reconcile(ctx, bot, ex, rt, save); err != nil {
		return err
	}
	if err := CancelAll(ctx, bot, ex, rt, save); err != nil {
		return err
	}
	if err := closeNet(ctx, bot, ex, rt, save); err != nil {
		return err
	}
	rt.Status = "stopped"
	return save()
}

// CancelAll cancels the bot's open and pending orders (pending ones by
// client order ID) and records any fill that landed before the cancel.
func CancelAll(ctx context.Context, bot *store.Bot, ex exchangex.Exchange, rt *store.Runtime, save Saver) error {
	for i := range rt.Orders {
		o := &rt.Orders[i]
		if o.Status != StatusOpen && o.Status != StatusPending && o.Status != StatusUnknown {
			continue
		}
		err := ex.CancelOrder(ctx, bot.Symbol, o.ExchangeID, o.ClientOID)
		if err != nil && !errors.Is(err, exchangex.ErrOrderNotFound) {
			return fmt.Errorf("cancel %s: %w", o.ClientOID, err)
		}
		got, err := ex.OrderStatus(ctx, bot.Symbol, o.ExchangeID, o.ClientOID)
		switch {
		case errors.Is(err, exchangex.ErrOrderNotFound):
			o.Status = StatusFailed // cancelled by client ID before it ever existed
		case err != nil:
			return err
		default:
			apply(o, got)
			if o.Status == StatusOpen || o.Status == StatusPending || o.Status == StatusUnknown {
				o.Status = StatusCanceled
			}
		}
		if err := save(); err != nil {
			return err
		}
	}
	return nil
}

// covered returns the venue's own measure of the bot's size: the signed
// futures position, or the free base asset on spot (only a long can be
// closed there).
func covered(ctx context.Context, bot *store.Bot, ex exchangex.Exchange) (float64, error) {
	if bot.Leverage > 0 {
		p, err := position(ctx, bot, ex)
		return p.Size, err
	}
	rules, err := ex.Rules(ctx, bot.Symbol)
	if err != nil {
		return 0, err
	}
	if rules.Base == "" {
		return 0, fmt.Errorf("live: the venue did not name the base asset of %s", bot.Symbol)
	}
	bals, err := ex.Balances(ctx)
	if err != nil {
		return 0, err
	}
	for _, b := range bals {
		if strings.EqualFold(b.Asset, rules.Base) {
			return b.Free, nil
		}
	}
	return 0, nil
}

func closeNet(ctx context.Context, bot *store.Bot, ex exchangex.Exchange, rt *store.Runtime, save Saver) error {
	start := NetSize(rt)
	if math.Abs(start) < eps {
		return nil
	}
	futures := bot.Leverage > 0
	if !futures && start < 0 {
		return fmt.Errorf("the bot sold %g more than it bought on spot; nothing to sell back, check the venue", -start)
	}
	before, err := covered(ctx, bot, ex)
	if err != nil {
		return err
	}
	if before*start <= 0 || math.Abs(before)+eps < math.Abs(start) {
		return fmt.Errorf("venue holds %g on %s, which does not cover the bot's own size %g; close it by hand", before, bot.Symbol, start)
	}
	for attempt := 1; attempt <= closeAttempts; attempt++ {
		net := NetSize(rt)
		if math.Abs(net) < eps {
			break
		}
		t, err := ex.Ticker(ctx, bot.Symbol) // fresh price for every try
		if err != nil {
			return err
		}
		slip := closeSlip * float64(attempt)
		side, px := exchangex.SideSell, t.Price*(1-slip)
		if net < 0 {
			side, px = exchangex.SideBuy, t.Price*(1+slip)
		}
		req := exchangex.PlaceReq{
			Symbol: bot.Symbol, Side: side, Price: px, Qty: math.Abs(net), ReduceOnly: futures, IOC: true,
			ClientOID: clientOID(bot, rt, px, side),
		}
		i, err := place(ctx, ex, rt, save, req, true, true, "")
		if err != nil {
			return err
		}
		got, err := ex.OrderStatus(ctx, bot.Symbol, rt.Orders[i].ExchangeID, req.ClientOID)
		if err != nil {
			return err
		}
		apply(&rt.Orders[i], got) // an IOC ends filled or canceled, maybe partly filled
		if err := save(); err != nil {
			return err
		}
	}
	if left := NetSize(rt); math.Abs(left) >= eps {
		return fmt.Errorf("%g of %g still open after %d close tries; close it by hand", left, start, closeAttempts)
	}
	after, err := covered(ctx, bot, ex)
	if err != nil {
		return err
	}
	if math.Abs(before)-math.Abs(after)+eps < math.Abs(start) {
		return fmt.Errorf("venue went from %g to %g, not down by the bot's %g; check the venue", before, after, start)
	}
	return nil
}
