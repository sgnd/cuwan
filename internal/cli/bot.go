package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/sgnd/cuwan/internal/out"
	"github.com/sgnd/cuwan/internal/risk"
	"github.com/sgnd/cuwan/internal/store"
)

func cmdBot(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(os.Stdout, "usage: cuwan bot add|list|show|edit|rm …")
		return 0
	}
	switch args[0] {
	case "add":
		return botAdd(args[1:])
	case "list":
		return botList(args[1:])
	case "show":
		return botShow(args[1:])
	case "edit":
		return botEdit(args[1:])
	case "rm", "delete":
		return botRm(args[1:])
	case "draft-range":
		return botDraftRange(args[1:])
	default:
		return out.Fail("bot", fmt.Errorf("unknown verb %q", args[0]), 2)
	}
}

// botDraftRange is a stub for a later AI range-draft command (15% buffer).
func botDraftRange(args []string) int {
	return out.Fail("bot.draft-range", fmt.Errorf("not implemented yet (planned: AI range draft with 15%% buffer); set --lower/--upper on bot add for now"), 1)
}

func botAdd(args []string) int {
	pos := positional(args)
	if len(pos) < 1 {
		return out.Fail("bot.add", fmt.Errorf("usage: cuwan bot add NAME --exchange EX --symbol SYM --lower N --upper N --levels N [--order-size|--size N] [--quote-amount N] [--leverage N]"), 2)
	}
	b := &store.Bot{Name: pos[0]}
	if err := applyBotFlags(b, args); err != nil {
		return out.Fail("bot.add", err, 2)
	}
	if b.Exchange == "" || b.Symbol == "" || b.Lower == 0 || b.Upper == 0 || b.Levels == 0 {
		return out.Fail("bot.add", fmt.Errorf("need --exchange --symbol --lower --upper --levels"), 2)
	}
	if b.Leverage > 0 {
		lev, warn := risk.CapLeverage(b.Leverage)
		b.Leverage = lev
		if warn != nil && !out.Agent() {
			fmt.Fprintf(os.Stderr, "warning: %v\n", warn)
		}
	}
	if err := store.PutBot(b); err != nil {
		return out.Fail("bot.add", err, 1)
	}
	_ = store.AppendAudit("bot.add", map[string]any{"name": b.Name})
	out.Print(os.Stdout, "bot.add", b, fmt.Sprintf("added bot %s (%s %s %.0f–%.0f x%d)", b.Name, b.Exchange, b.Symbol, b.Lower, b.Upper, b.Levels))
	return 0
}

func applyBotFlags(b *store.Bot, args []string) error {
	if v, ok := flagValue(args, "--exchange"); ok {
		b.Exchange = v
	}
	if v, ok := flagValue(args, "--symbol"); ok {
		b.Symbol = v
	}
	if v, ok := flagValue(args, "--lower"); ok {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return err
		}
		b.Lower = f
	}
	if v, ok := flagValue(args, "--upper"); ok {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return err
		}
		b.Upper = f
	}
	if v, ok := flagValue(args, "--levels"); ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			return err
		}
		b.Levels = n
	}
	if v, ok := flagValue(args, "--order-size"); ok {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return err
		}
		b.OrderSize = f
	}
	if v, ok := flagValue(args, "--size"); ok {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return err
		}
		b.OrderSize = f
	}
	if v, ok := flagValue(args, "--quote-amount"); ok {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return err
		}
		b.QuoteAmount = f
	}
	if v, ok := flagValue(args, "--leverage"); ok {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return err
		}
		b.Leverage = f
	}
	return nil
}

func botList(args []string) int {
	list, err := store.ListBots()
	if err != nil {
		if err == store.ErrNoStore {
			out.Print(os.Stdout, "bot.list", []any{}, "(none)")
			return 0
		}
		return out.Fail("bot.list", err, 1)
	}
	var lines []string
	for _, b := range list {
		lines = append(lines, fmt.Sprintf("%s  %s %s  %.4g–%.4g x%d", b.Name, b.Exchange, b.Symbol, b.Lower, b.Upper, b.Levels))
	}
	text := "(none)"
	if len(lines) > 0 {
		text = strings.Join(lines, "\n")
	}
	out.Print(os.Stdout, "bot.list", list, text)
	return 0
}

func botShow(args []string) int {
	pos := positional(args)
	if len(pos) < 1 {
		return out.Fail("bot.show", fmt.Errorf("usage: cuwan bot show NAME"), 2)
	}
	b, err := store.GetBot(pos[0])
	if err != nil {
		return out.Fail("bot.show", err, 1)
	}
	out.Print(os.Stdout, "bot.show", b, fmt.Sprintf("%s\n  exchange: %s\n  symbol: %s\n  range: %.8g – %.8g\n  levels: %d\n  order_size: %g\n  leverage: %g (cap %g)",
		b.Name, b.Exchange, b.Symbol, b.Lower, b.Upper, b.Levels, b.OrderSize, b.Leverage, risk.MaxLeverage))
	return 0
}

func botEdit(args []string) int {
	pos := positional(args)
	if len(pos) < 1 {
		return out.Fail("bot.edit", fmt.Errorf("usage: cuwan bot edit NAME [flags]"), 2)
	}
	b, err := store.GetBot(pos[0])
	if err != nil {
		return out.Fail("bot.edit", err, 1)
	}
	if err := applyBotFlags(b, args); err != nil {
		return out.Fail("bot.edit", err, 2)
	}
	if b.Leverage > 0 {
		b.Leverage, _ = risk.CapLeverage(b.Leverage)
	}
	if err := store.PutBot(b); err != nil {
		return out.Fail("bot.edit", err, 1)
	}
	out.Print(os.Stdout, "bot.edit", b, "updated "+b.Name)
	return 0
}

func botRm(args []string) int {
	pos := positional(args)
	if len(pos) < 1 {
		return out.Fail("bot.rm", fmt.Errorf("usage: cuwan bot rm NAME [--yes]"), 2)
	}
	if !flagBool(args, "--yes") {
		if out.Agent() {
			return out.Fail("bot.rm", fmt.Errorf("refusing without --yes"), 1)
		}
		if !confirm("delete bot "+pos[0]+"?") {
			return out.Fail("bot.rm", fmt.Errorf("aborted"), 1)
		}
	}
	if err := store.DeleteBot(pos[0]); err != nil {
		return out.Fail("bot.rm", err, 1)
	}
	out.Print(os.Stdout, "bot.rm", map[string]string{"name": pos[0]}, "removed "+pos[0])
	return 0
}
