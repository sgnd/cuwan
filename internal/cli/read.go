package cli

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/sgnd/cuwan/internal/exchangex"
	"github.com/sgnd/cuwan/internal/out"
	"github.com/sgnd/cuwan/internal/store"
)

// readVenue runs one read call against an exchange.
func readVenue(cmd string, args []string, call func(ctx context.Context, d exchangex.Exchange, symbol string) (any, string, error)) int {
	f, err := parse(args, []string{"symbol"}, nil)
	if err == nil {
		_, err = needOne(f, "cuwan "+cmd+" EXCHANGE [--symbol SYM]")
	}
	if err != nil {
		return out.Fail(cmd, err, 2)
	}
	return withEnv(cmd, func(e *env) int {
		_, d, err := e.driver(f.pos[0])
		if err != nil {
			return out.Fail(cmd, err, 1)
		}
		data, text, err := call(context.Background(), d, f.vals["symbol"])
		if err != nil {
			return out.Fail(cmd, err, 1)
		}
		out.Print(os.Stdout, cmd, data, text)
		return 0
	})
}

func cmdOrders(args []string) int {
	return readVenue("orders", args, func(ctx context.Context, d exchangex.Exchange, sym string) (any, string, error) {
		list, err := d.OpenOrders(ctx, sym)
		var lines []string
		for _, o := range list {
			lines = append(lines, fmt.Sprintf("%s %s %g @ %g filled %g", o.Symbol, o.Side, o.Qty, o.Price, o.Filled))
		}
		return nonNil(list), strings.Join(lines, "\n"), err
	})
}

func cmdPositions(args []string) int {
	return readVenue("positions", args, func(ctx context.Context, d exchangex.Exchange, _ string) (any, string, error) {
		ps, err := d.Positions(ctx)
		var lines []string
		for _, p := range ps {
			lines = append(lines, fmt.Sprintf("%s %s %g entry %g liq %g", p.Symbol, p.Side, p.Size, p.EntryPrice, p.LiquidationPrice))
		}
		return nonNil(ps), strings.Join(lines, "\n"), err
	})
}

func cmdBalance(args []string) int {
	return readVenue("balance", args, func(ctx context.Context, d exchangex.Exchange, _ string) (any, string, error) {
		bs, err := d.Balances(ctx)
		var lines []string
		for _, b := range bs {
			lines = append(lines, fmt.Sprintf("%s free %g locked %g", b.Asset, b.Free, b.Locked))
		}
		return nonNil(bs), strings.Join(lines, "\n"), err
	})
}

// nonNil makes an empty list print as [] in JSON.
func nonNil[T any](xs []T) []T {
	if xs == nil {
		return []T{}
	}
	return xs
}

// cmdAudit prints the audit log, checked as it is read.
func cmdAudit(args []string) int {
	f, err := parse(args, []string{"limit"}, nil)
	if err != nil || len(f.pos) > 0 {
		return out.Fail("audit", fmt.Errorf("usage: cuwan audit [--limit N]"), 2)
	}
	limit := 50
	if f.set["limit"] {
		if limit, err = strconv.Atoi(f.vals["limit"]); err != nil || limit < 0 {
			return out.Fail("audit", fmt.Errorf("--limit must be 0 or more (0 is all)"), 2)
		}
	}
	return withEnv("audit", func(e *env) int {
		var entries []store.Entry
		if err := e.st.View(func(s *store.State) error { entries = s.Audit; return nil }); err != nil {
			return out.Fail("audit", err, 1)
		}
		if limit > 0 && len(entries) > limit {
			entries = entries[len(entries)-limit:]
		}
		entries = nonNil(entries)
		var lines []string
		for _, en := range entries {
			lines = append(lines, fmt.Sprintf("%d %s %s", en.Seq, en.Time.Format("2006-01-02 15:04:05"), en.Data))
		}
		out.Print(os.Stdout, "audit", entries, strings.Join(lines, "\n"))
		return 0
	})
}
