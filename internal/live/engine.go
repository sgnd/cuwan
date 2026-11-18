// Package live runs a live grid bot against a venue driver.
//
// Rules the engine keeps:
//   - Every order is saved as "pending" before it is sent, and saved again
//     once the venue answers. A pending order is reconciled by its client
//     order ID on the next step, so a timed-out send is never lost.
//   - An order that left the book counts as filled only when the driver says
//     so (OrderStatuser). Drivers without it cannot run live.
//   - A failed preventive close or flatten is a CloseError: the caller must
//     halt the bot and tell a person.
package live

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/sgnd/cuwan/internal/exchangex"
	"github.com/sgnd/cuwan/internal/grid"
	"github.com/sgnd/cuwan/internal/risk"
	"github.com/sgnd/cuwan/internal/store"
)

// OrderStatuser reports one order by exchange ID or client order ID. It
// returns an error wrapping exchangex.ErrOrderNotFound when there is none.
type OrderStatuser interface {
	OrderStatus(ctx context.Context, symbol, orderID, clientOID string) (exchangex.Order, error)
}

// Order status values kept in the runtime.
const (
	StatusPending  = "pending"  // saved, send not confirmed
	StatusOpen     = "open"     // on the book
	StatusFilled   = "filled"   // fully filled
	StatusCanceled = "canceled" // off the book, maybe partly filled
	StatusFailed   = "failed"   // the venue never got it
)

// closeSlip makes the reduce-only close limit price cross the book.
const closeSlip = 0.005

// eps is the size below which a net position counts as flat.
const eps = 1e-12

// Saver persists the runtime. The engine calls it before and after every
// order it sends.
type Saver func() error

// ErrNoOrderStatus means the driver cannot confirm fills.
var ErrNoOrderStatus = errors.New("live: this venue driver cannot confirm fills (no OrderStatus); live runs are refused")

// CloseError means a protective close failed. Halt and alert.
type CloseError struct{ Err error }

func (e *CloseError) Error() string { return "live: protective close failed: " + e.Err.Error() }
func (e *CloseError) Unwrap() error { return e.Err }

// Result says whether the bot stopped during a step, and why.
type Result struct {
	Stopped bool
	Reason  string
}

// ErrNoRules means the driver cannot report price tick and size step.
var ErrNoRules = errors.New("live: this venue driver cannot report tick and step sizes (no Rules); live runs are refused")

// statuser returns the driver's OrderStatuser or ErrNoOrderStatus.
func statuser(ex exchangex.Exchange) (OrderStatuser, error) {
	st, ok := ex.(OrderStatuser)
	if !ok {
		return nil, ErrNoOrderStatus
	}
	return st, nil
}

// CanRun reports whether the driver can run live.
func CanRun(ex exchangex.Exchange) error {
	if _, err := statuser(ex); err != nil {
		return err
	}
	if _, ok := ex.(exchangex.RulesProvider); !ok {
		return ErrNoRules
	}
	return nil
}

// place fits the order to the venue filters, saves it as pending, sends it,
// then saves the answer. crossing marks an order meant to fill at once.
func place(ctx context.Context, ex exchangex.Exchange, rt *store.RuntimeState, save Saver, req exchangex.PlaceReq, crossing bool) (int, error) {
	rp, ok := ex.(exchangex.RulesProvider)
	if !ok {
		return -1, ErrNoRules
	}
	rules, err := rp.Rules(ctx, req.Symbol)
	if err != nil {
		return -1, err
	}
	if req, err = roundReq(req, rules, crossing); err != nil {
		return -1, err
	}
	rt.Orders = append(rt.Orders, store.RuntimeOrder{
		ClientOID: req.ClientOID, Side: req.Side, Price: req.Price, Qty: req.Qty,
		ReduceOnly: req.ReduceOnly, Status: StatusPending,
	})
	i := len(rt.Orders) - 1
	if err := save(); err != nil {
		return i, err // nothing sent
	}
	o, err := ex.PlaceOrder(ctx, req)
	if err != nil {
		return i, fmt.Errorf("place %s: %w (kept as pending; reconciled by client order ID)", req.ClientOID, err)
	}
	rt.Orders[i].ExchangeID = o.ID
	rt.Orders[i].Status = StatusOpen
	if o.Status == StatusFilled {
		rt.Orders[i].Status = StatusFilled
		rt.Orders[i].Filled = req.Qty
	}
	return i, save()
}

// PlaceGrid places the initial grid around mid, one order at a time.
func PlaceGrid(ctx context.Context, bot *store.Bot, ex exchangex.Exchange, rt *store.RuntimeState, save Saver, mid float64) error {
	plan, err := grid.Build(bot.Lower, bot.Upper, bot.Levels)
	if err != nil {
		return err
	}
	for _, px := range plan.Prices {
		side := grid.LevelSide(px, mid)
		qty := bot.OrderSize
		if qty <= 0 {
			qty = 0.001
		}
		req := exchangex.PlaceReq{Symbol: bot.Symbol, Side: side, Price: px, Qty: qty, ClientOID: grid.ClientOID(bot.Name, px, side, len(rt.Orders)+1)}
		if _, err := place(ctx, ex, rt, save, req, false); err != nil {
			return err
		}
	}
	return nil
}

// Reconcile resolves pending orders by client order ID.
func Reconcile(ctx context.Context, bot *store.Bot, ex exchangex.Exchange, rt *store.RuntimeState, save Saver) error {
	st, err := statuser(ex)
	if err != nil {
		return err
	}
	changed := false
	for i := range rt.Orders {
		o := &rt.Orders[i]
		if o.Status != StatusPending {
			continue
		}
		got, err := st.OrderStatus(ctx, bot.Symbol, "", o.ClientOID)
		if errors.Is(err, exchangex.ErrOrderNotFound) {
			o.Status = StatusFailed
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
func apply(o *store.RuntimeOrder, got exchangex.Order) {
	if got.ID != "" {
		o.ExchangeID = got.ID
	}
	o.Status = got.Status
	o.Filled = got.Filled
	if got.Status == StatusFilled && o.Filled == 0 {
		o.Filled = o.Qty
	}
}

// Step reconciles, runs the loss guard, and refills filled grid orders.
func Step(ctx context.Context, bot *store.Bot, ex exchangex.Exchange, rt *store.RuntimeState, save Saver) (Result, error) {
	st, err := statuser(ex)
	if err != nil {
		return Result{}, err
	}
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

	open, err := ex.OpenOrders(ctx, bot.Symbol)
	if err != nil {
		return Result{}, err
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
	find := func(o store.RuntimeOrder) (exchangex.Order, bool) {
		if o.ExchangeID != "" {
			if b, ok := onBook["id:"+o.ExchangeID]; ok {
				return b, true
			}
		}
		if o.ClientOID != "" {
			if b, ok := onBook["cid:"+o.ClientOID]; ok {
				return b, true
			}
		}
		return exchangex.Order{}, false
	}
	step := 0.0
	if bot.Levels > 1 {
		step = (bot.Upper - bot.Lower) / float64(bot.Levels-1)
	}
	for i, n := 0, len(rt.Orders); i < n; i++ {
		o := rt.Orders[i]
		if o.Status != StatusOpen || o.ReduceOnly {
			continue
		}
		if b, ok := find(o); ok {
			rt.Orders[i].Filled = b.Filled // partial fills count toward the net size
			continue
		}
		got, err := st.OrderStatus(ctx, bot.Symbol, o.ExchangeID, o.ClientOID)
		if err != nil {
			return Result{}, err
		}
		apply(&rt.Orders[i], got)
		if rt.Orders[i].Status != StatusFilled {
			if err := save(); err != nil {
				return Result{}, err
			}
			continue
		}
		opp, px := "sell", o.Price+step
		if o.Side == "sell" {
			opp, px = "buy", o.Price-step
		}
		if px <= 0 {
			continue
		}
		req := exchangex.PlaceReq{Symbol: bot.Symbol, Side: opp, Price: px, Qty: o.Qty, ClientOID: grid.ClientOID(bot.Name, px, opp, len(rt.Orders)+1)}
		if _, err := place(ctx, ex, rt, save, req, false); err != nil {
			return Result{}, err
		}
	}
	return Result{}, save()
}

// guard returns a reason to stop, or "".
//   - futures: the ~5% preventive close from the bot's own position
//   - spot: price more than 5% below the grid's lower bound
func guard(ctx context.Context, bot *store.Bot, ex exchangex.Exchange, rt *store.RuntimeState, mark float64) (string, error) {
	if bot.Leverage <= 0 {
		if mark < bot.Lower*(1-risk.PreventiveClosePct) {
			return fmt.Sprintf("spot loss guard: price %g is more than %g%% below the grid", mark, risk.PreventiveClosePct*100), nil
		}
		return "", nil
	}
	net := NetSize(rt)
	if math.Abs(net) < eps {
		return "", nil
	}
	p, err := position(ctx, bot, ex)
	if err != nil {
		return "", err
	}
	entry := p.EntryPrice
	if entry <= 0 {
		entry = (bot.Lower + bot.Upper) / 2
	}
	side := "buy"
	if net < 0 {
		side = "sell"
	}
	if risk.CheckFutures(entry, mark, bot.Leverage, side).Preventive {
		return "preventive close: mark within 5% of the path to liquidation", nil
	}
	return "", nil
}

// NetSize is the bot's own position from its fills: buys minus sells.
func NetSize(rt *store.RuntimeState) float64 {
	n := 0.0
	for _, o := range rt.Orders {
		if o.Side == "buy" {
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
		if (p.Side == "short" || p.Side == "sell") && p.Size > 0 {
			p.Size = -p.Size
		}
		return p, nil
	}
	return exchangex.Position{}, nil
}

// Flatten cancels the bot's open orders and, on futures, closes the bot's
// own net size with a reduce-only order. It refuses when the venue position
// is smaller than or opposite to the bot's net size (other bots or manual
// trades share the symbol), and checks that the close filled.
func Flatten(ctx context.Context, bot *store.Bot, ex exchangex.Exchange, rt *store.RuntimeState, save Saver) error {
	st, err := statuser(ex)
	if err != nil {
		return err
	}
	if err := Reconcile(ctx, bot, ex, rt, save); err != nil {
		return err
	}
	for i := range rt.Orders {
		o := &rt.Orders[i]
		if o.Status != StatusOpen {
			continue
		}
		if err := ex.CancelOrder(ctx, bot.Symbol, o.ExchangeID, o.ClientOID); err != nil {
			return fmt.Errorf("cancel %s: %w", o.ClientOID, err)
		}
		// A fill can land between the last step and the cancel.
		got, err := st.OrderStatus(ctx, bot.Symbol, o.ExchangeID, o.ClientOID)
		if err != nil {
			return err
		}
		apply(o, got)
		if o.Status == StatusOpen {
			o.Status = StatusCanceled
		}
		if err := save(); err != nil {
			return err
		}
	}
	if bot.Leverage > 0 {
		if err := closeNet(ctx, bot, ex, st, rt, save); err != nil {
			return err
		}
	}
	rt.Status = "stopped"
	return save()
}

func closeNet(ctx context.Context, bot *store.Bot, ex exchangex.Exchange, st OrderStatuser, rt *store.RuntimeState, save Saver) error {
	net := NetSize(rt)
	if math.Abs(net) < eps {
		return nil
	}
	p, err := position(ctx, bot, ex)
	if err != nil {
		return err
	}
	if p.Size*net <= 0 || math.Abs(p.Size)+eps < math.Abs(net) {
		return fmt.Errorf("venue position %g on %s does not cover the bot's own size %g; close it by hand", p.Size, bot.Symbol, net)
	}
	t, err := ex.Ticker(ctx, bot.Symbol) // fresh price for the close
	if err != nil {
		return err
	}
	side, px := "sell", t.Price*(1-closeSlip)
	if net < 0 {
		side, px = "buy", t.Price*(1+closeSlip)
	}
	req := exchangex.PlaceReq{
		Symbol: bot.Symbol, Side: side, Price: px, Qty: math.Abs(net), ReduceOnly: true,
		ClientOID: grid.ClientOID(bot.Name, px, side, len(rt.Orders)+1),
	}
	i, err := place(ctx, ex, rt, save, req, true)
	if err != nil {
		return err
	}
	got, err := st.OrderStatus(ctx, bot.Symbol, rt.Orders[i].ExchangeID, req.ClientOID)
	if err != nil {
		return err
	}
	apply(&rt.Orders[i], got)
	if err := save(); err != nil {
		return err
	}
	if rt.Orders[i].Status != StatusFilled {
		return fmt.Errorf("close order %s is %s, not filled; check the venue", req.ClientOID, rt.Orders[i].Status)
	}
	return nil
}
