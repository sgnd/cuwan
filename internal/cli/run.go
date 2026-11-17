package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/sgnd/cuwan/internal/exchangex"
	"github.com/sgnd/cuwan/internal/grid"
	"github.com/sgnd/cuwan/internal/out"
	"github.com/sgnd/cuwan/internal/risk"
	"github.com/sgnd/cuwan/internal/store"
)

func cmdRun(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(os.Stdout, "usage: cuwan run start|status|stop NAME")
		return 0
	}
	switch args[0] {
	case "start":
		return runStart(args[1:])
	case "status":
		return runStatus(args[1:])
	case "stop":
		return runStop(args[1:])
	default:
		return out.Fail("run", fmt.Errorf("unknown verb %q", args[0]), 2)
	}
}

func runStart(args []string) int {
	pos := positional(args)
	if len(pos) < 1 {
		return out.Fail("run.start", fmt.Errorf("usage: cuwan run start NAME [--yes] [--dry-run]"), 2)
	}
	dry := flagBool(args, "--dry-run")
	bot, err := store.GetBot(pos[0])
	if err != nil {
		return out.Fail("run.start", err, 1)
	}
	exRec, drv, err := openNamedExchange(bot.Exchange, dry)
	if err != nil {
		return out.Fail("run.start", err, 1)
	}
	ctx := context.Background()
	perms, err := drv.Permissions(ctx)
	if err == nil && perms.CanWithdraw && !dry {
		return out.Fail("run.start", fmt.Errorf("refusing live run: API key appears withdrawal-capable"), 1)
	}
	if bot.Leverage > 0 {
		lev, _ := risk.CapLeverage(bot.Leverage)
		bot.Leverage = lev
		if !dry {
			_ = drv.SetLeverage(ctx, bot.Symbol, lev)
		}
	}
	plan, err := grid.Build(bot.Lower, bot.Upper, bot.Levels)
	if err != nil {
		return out.Fail("run.start", err, 1)
	}
	ticker, err := drv.Ticker(ctx, bot.Symbol)
	if err != nil {
		return out.Fail("run.start", err, 1)
	}
	if !dry {
		if out.Agent() && !flagBool(args, "--yes") {
			return out.Fail("run.start", fmt.Errorf("live trading requires a person confirm; pass --yes only when a human approved"), 1)
		}
		if !flagBool(args, "--yes") {
			msg := fmt.Sprintf("place %d live grid orders for %s on %s at mid≈%g?", len(plan.Prices), bot.Name, exRec.Venue, ticker.Price)
			if !confirm(msg) {
				return out.Fail("run.start", fmt.Errorf("aborted"), 1)
			}
		}
	}

	var orders []store.RuntimeOrder
	for _, px := range plan.Prices {
		side := grid.LevelSide(px, ticker.Price)
		qty := bot.OrderSize
		if qty <= 0 {
			qty = 0.001
		}
		oid := grid.ClientOID(bot.Name, px, side, 1)
		ro := store.RuntimeOrder{ClientOID: oid, Side: side, Price: px, Qty: qty, Status: "planned"}
		if dry {
			ro.Status = "dry-run"
		} else {
			ord, err := drv.PlaceOrder(ctx, exchangex.PlaceReq{
				Symbol: bot.Symbol, Side: side, Price: px, Qty: qty, ClientOID: oid,
			})
			if err != nil {
				return out.Fail("run.start", err, 1)
			}
			ro.ExchangeID = ord.ID
			ro.Status = ord.Status
			if ro.Status == "" {
				ro.Status = "open"
			}
		}
		orders = append(orders, ro)
	}
	rt := &store.RuntimeState{
		BotName: bot.Name, Mode: "live", Status: "running",
		Orders: orders, StartedAt: time.Now().UTC(),
		Meta: map[string]string{"last_price": fmt.Sprintf("%g", ticker.Price), "dry_run": fmt.Sprintf("%v", dry)},
	}
	if dry {
		rt.Status = "dry-run"
		rt.Mode = "dry-run"
	}
	if err := store.PutRuntime(rt); err != nil {
		return out.Fail("run.start", err, 1)
	}
	_ = store.AppendAudit("run.start", map[string]any{"bot": bot.Name, "dry_run": dry, "orders": len(orders)})
	out.Print(os.Stdout, "run.start", rt, fmt.Sprintf("live start %s orders=%d dry_run=%v", bot.Name, len(orders), dry))
	return 0
}

func runStatus(args []string) int {
	pos := positional(args)
	if len(pos) < 1 {
		return out.Fail("run.status", fmt.Errorf("usage: cuwan run status NAME"), 2)
	}
	rt, err := store.GetRuntime(pos[0])
	if err != nil {
		return out.Fail("run.status", err, 1)
	}
	out.Print(os.Stdout, "run.status", rt, fmt.Sprintf("%s mode=%s status=%s orders=%d", rt.BotName, rt.Mode, rt.Status, len(rt.Orders)))
	return 0
}

func runStop(args []string) int {
	pos := positional(args)
	if len(pos) < 1 {
		return out.Fail("run.stop", fmt.Errorf("usage: cuwan run stop NAME [--yes] [--flatten]"), 2)
	}
	if !flagBool(args, "--yes") {
		if out.Agent() {
			return out.Fail("run.stop", fmt.Errorf("destructive stop needs --yes from a person"), 1)
		}
		if !confirm("stop live bot "+pos[0]+"?") {
			return out.Fail("run.stop", fmt.Errorf("aborted"), 1)
		}
	}
	rt, err := store.GetRuntime(pos[0])
	if err != nil {
		return out.Fail("run.stop", err, 1)
	}
	bot, err := store.GetBot(pos[0])
	if err != nil {
		return out.Fail("run.stop", err, 1)
	}
	if rt.Mode == "live" && rt.Status == "running" {
		_, drv, err := openNamedExchange(bot.Exchange, false)
		if err == nil {
			ctx := context.Background()
			for _, o := range rt.Orders {
				if o.Status == "open" || o.Status == "new" {
					_ = drv.CancelOrder(ctx, bot.Symbol, o.ExchangeID, o.ClientOID)
				}
			}
			if flagBool(args, "--flatten") {
				// reduce-only market-like close via aggressive limit at mark — MVP cancels only
				_ = store.AppendAudit("run.flatten", map[string]any{"bot": pos[0]})
			}
		}
	}
	rt.Status = "stopped"
	if err := store.PutRuntime(rt); err != nil {
		return out.Fail("run.stop", err, 1)
	}
	_ = store.AppendAudit("run.stop", map[string]any{"bot": pos[0]})
	out.Print(os.Stdout, "run.stop", map[string]string{"name": pos[0]}, "stopped "+pos[0])
	return 0
}
