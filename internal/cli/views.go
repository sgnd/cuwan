package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/sgnd/cuwan/internal/out"
	"github.com/sgnd/cuwan/internal/store"
)

func cmdOrders(args []string) int {
	pos := positional(args)
	if len(pos) < 1 {
		return out.Fail("orders", fmt.Errorf("usage: cuwan orders EXCHANGE [--symbol SYM]"), 2)
	}
	_, drv, err := openExchange(pos[0], false)
	if err != nil {
		// Open orders need credentials; without them, show the stored runtime.
		if rt, rerr := store.GetRuntime(pos[0]); rerr == nil {
			out.Print(os.Stdout, "orders", rt.Orders, fmt.Sprintf("runtime orders for %s: %d", pos[0], len(rt.Orders)))
			return 0
		}
		return out.Fail("orders", err, 1)
	}
	symbol, _ := flagValue(args, "--symbol")
	ords, err := drv.OpenOrders(context.Background(), symbol)
	if err != nil {
		return out.Fail("orders", err, 1)
	}
	var lines []string
	for _, o := range ords {
		lines = append(lines, fmt.Sprintf("%s %s %s %g @ %g [%s]", o.ID, o.Side, o.Symbol, o.Qty, o.Price, o.Status))
	}
	text := "(none)"
	if len(lines) > 0 {
		text = strings.Join(lines, "\n")
	}
	out.Print(os.Stdout, "orders", ords, text)
	return 0
}

func cmdPositions(args []string) int {
	pos := positional(args)
	if len(pos) < 1 {
		return out.Fail("positions", fmt.Errorf("usage: cuwan positions EXCHANGE"), 2)
	}
	_, drv, err := openExchange(pos[0], false)
	if err != nil {
		return out.Fail("positions", err, 1)
	}
	ps, err := drv.Positions(context.Background())
	if err != nil {
		return out.Fail("positions", err, 1)
	}
	var lines []string
	for _, p := range ps {
		lines = append(lines, fmt.Sprintf("%s %s size=%g entry=%g lev=%gx liq=%g", p.Symbol, p.Side, p.Size, p.EntryPrice, p.Leverage, p.LiquidationPrice))
	}
	text := "(none)"
	if len(lines) > 0 {
		text = strings.Join(lines, "\n")
	}
	out.Print(os.Stdout, "positions", ps, text)
	return 0
}

func cmdBalance(args []string) int {
	pos := positional(args)
	if len(pos) < 1 {
		return out.Fail("balance", fmt.Errorf("usage: cuwan balance EXCHANGE"), 2)
	}
	_, drv, err := openExchange(pos[0], false)
	if err != nil {
		return out.Fail("balance", err, 1)
	}
	bs, err := drv.Balances(context.Background())
	if err != nil {
		return out.Fail("balance", err, 1)
	}
	var lines []string
	for _, b := range bs {
		lines = append(lines, fmt.Sprintf("%s free=%g locked=%g", b.Asset, b.Free, b.Locked))
	}
	text := "(none)"
	if len(lines) > 0 {
		text = strings.Join(lines, "\n")
	}
	out.Print(os.Stdout, "balance", bs, text)
	return 0
}

func cmdAudit(args []string) int {
	limit := 50
	rows, err := store.ListAudit(limit)
	if err != nil {
		if err == store.ErrNoStore {
			out.Print(os.Stdout, "audit", []any{}, "(none)")
			return 0
		}
		return out.Fail("audit", err, 1)
	}
	var lines []string
	for _, r := range rows {
		lines = append(lines, string(r))
	}
	text := "(none)"
	if len(lines) > 0 {
		text = strings.Join(lines, "\n")
	}
	out.Print(os.Stdout, "audit", rows, text)
	return 0
}
