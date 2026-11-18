// Package live runs one step of a live grid bot against a venue driver.
//
// The engine only acts on positive evidence. An order that is no longer open
// counts as filled only when the driver confirms it (OrderStatuser); without
// that, it is marked "unknown" and nothing is placed for it.
package live

import (
	"context"
	"fmt"

	"github.com/sgnd/cuwan/internal/exchangex"
	"github.com/sgnd/cuwan/internal/grid"
	"github.com/sgnd/cuwan/internal/risk"
	"github.com/sgnd/cuwan/internal/store"
)

// OrderStatuser is an optional driver interface that reports one order.
type OrderStatuser interface {
	OrderStatus(ctx context.Context, symbol, orderID, clientOID string) (exchangex.Order, error)
}

// Order status values kept in the runtime.
const (
	StatusOpen     = "open"
	StatusFilled   = "filled"
	StatusCanceled = "canceled"
	StatusUnknown  = "unknown"
)

// closeSlip makes the reduce-only close limit price cross the book.
const closeSlip = 0.005

// Step reconciles fills, places the opposite grid orders, and enforces the
// preventive close. It returns stopped=true when the bot must stop.
func Step(ctx context.Context, bot *store.Bot, ex exchangex.Exchange, rt *store.RuntimeState) (stopped bool, err error) {
	if rt.Meta == nil {
		rt.Meta = map[string]string{}
	}
	t, err := ex.Ticker(ctx, bot.Symbol)
	if err != nil {
		return false, err
	}
	rt.Meta["last_price"] = fmt.Sprintf("%g", t.Price)

	if bot.Leverage > 0 {
		fired, err := preventive(ctx, bot, ex, t.Price)
		if err != nil {
			return false, err
		}
		if fired {
			rt.Meta["preventive"] = "fired"
			if err := Flatten(ctx, bot, ex, rt, t.Price); err != nil {
				return false, err
			}
			return true, nil
		}
	}

	open, err := ex.OpenOrders(ctx, bot.Symbol)
	if err != nil {
		return false, err
	}
	stillOpen := map[string]bool{}
	for _, o := range open {
		stillOpen[o.ID] = true
		stillOpen[o.ClientOID] = true
	}
	step := 0.0
	if bot.Levels > 1 {
		step = (bot.Upper - bot.Lower) / float64(bot.Levels-1)
	}
	st, canCheck := ex.(OrderStatuser)
	for i, n := 0, len(rt.Orders); i < n; i++ {
		o := rt.Orders[i]
		if o.Status != StatusOpen || stillOpen[o.ExchangeID] || stillOpen[o.ClientOID] {
			continue
		}
		status := StatusUnknown
		if canCheck {
			got, err := st.OrderStatus(ctx, bot.Symbol, o.ExchangeID, o.ClientOID)
			if err != nil {
				return false, err
			}
			status = got.Status
		}
		rt.Orders[i].Status = status
		if status != StatusFilled {
			continue
		}
		opp, px := "sell", o.Price+step
		if o.Side == "sell" {
			opp, px = "buy", o.Price-step
		}
		if px <= 0 {
			continue
		}
		oid := grid.ClientOID(bot.Name, px, opp, len(rt.Orders)+1)
		placed, err := ex.PlaceOrder(ctx, exchangex.PlaceReq{
			Symbol: bot.Symbol, Side: opp, Price: px, Qty: o.Qty, ClientOID: oid,
		})
		if err != nil {
			return false, err
		}
		rt.Orders = append(rt.Orders, store.RuntimeOrder{
			ClientOID: oid, ExchangeID: placed.ID, Side: opp, Price: px, Qty: o.Qty, Status: StatusOpen,
		})
	}
	return false, nil
}

// preventive reports whether any position on the symbol is past the ~5%
// preventive line. The check cannot be turned off.
func preventive(ctx context.Context, bot *store.Bot, ex exchangex.Exchange, mark float64) (bool, error) {
	ps, err := ex.Positions(ctx)
	if err != nil {
		return false, err
	}
	for _, p := range ps {
		if p.Symbol != bot.Symbol || p.Size == 0 || p.EntryPrice <= 0 {
			continue
		}
		side := "buy"
		if p.Side == "short" || p.Side == "sell" || p.Size < 0 {
			side = "sell"
		}
		if risk.CheckFutures(p.EntryPrice, mark, bot.Leverage, side).Preventive {
			return true, nil
		}
	}
	return false, nil
}

// Flatten cancels the bot's open orders and closes its position on the
// symbol with a reduce-only order priced to cross the book.
func Flatten(ctx context.Context, bot *store.Bot, ex exchangex.Exchange, rt *store.RuntimeState, mark float64) error {
	for i := range rt.Orders {
		o := &rt.Orders[i]
		if o.Status != StatusOpen {
			continue
		}
		if err := ex.CancelOrder(ctx, bot.Symbol, o.ExchangeID, o.ClientOID); err != nil {
			return err
		}
		o.Status = StatusCanceled
	}
	ps, err := ex.Positions(ctx)
	if err != nil {
		return err
	}
	for _, p := range ps {
		if p.Symbol != bot.Symbol || p.Size == 0 {
			continue
		}
		size, side, px := p.Size, "sell", mark*(1-closeSlip)
		if size < 0 || p.Side == "short" || p.Side == "sell" {
			side, px = "buy", mark*(1+closeSlip)
		}
		if size < 0 {
			size = -size
		}
		oid := grid.ClientOID(bot.Name, px, side, len(rt.Orders)+1)
		placed, err := ex.PlaceOrder(ctx, exchangex.PlaceReq{
			Symbol: bot.Symbol, Side: side, Price: px, Qty: size, ClientOID: oid, ReduceOnly: true,
		})
		if err != nil {
			return err
		}
		rt.Orders = append(rt.Orders, store.RuntimeOrder{
			ClientOID: oid, ExchangeID: placed.ID, Side: side, Price: px, Qty: size, Status: StatusOpen,
		})
	}
	rt.Status = "stopped"
	return nil
}
