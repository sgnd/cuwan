package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sgnd/cuwan/internal/exchangex"
	"github.com/sgnd/cuwan/internal/grid"
	"github.com/sgnd/cuwan/internal/live"
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
		return out.Fail("run.start", fmt.Errorf("usage: cuwan run start NAME [--yes] [--dry-run] [--foreground]"), 2)
	}
	dry := flagBool(args, "--dry-run")
	bot, err := store.GetBot(pos[0])
	if err != nil {
		return out.Fail("run.start", err, 1)
	}
	exRec, drv, err := openExchange(bot.Exchange, dry)
	if err != nil {
		return out.Fail("run.start", err, 1)
	}
	ctx := context.Background()
	if !dry {
		if err := live.CanRun(drv); err != nil {
			return out.Fail("run.start", err, 1)
		}
		// Fail closed: a live run needs a key proven unable to withdraw.
		perms, err := drv.Permissions(ctx)
		if err != nil {
			return out.Fail("run.start", fmt.Errorf("refusing live run: cannot check API key permissions: %w", err), 1)
		}
		if perms.CanWithdraw {
			return out.Fail("run.start", fmt.Errorf("refusing live run: API key appears withdrawal-capable"), 1)
		}
	}
	if bot.Leverage > 0 {
		bot.Leverage, _ = risk.CapLeverage(bot.Leverage)
	}
	plan, err := grid.Build(bot.Lower, bot.Upper, bot.Levels)
	if err != nil {
		return out.Fail("run.start", err, 1)
	}
	ticker, err := drv.Ticker(ctx, bot.Symbol)
	if err != nil {
		return out.Fail("run.start", err, 1)
	}
	if bot.Leverage > 0 {
		d := risk.CheckFutures((bot.Lower+bot.Upper)/2, ticker.Price, bot.Leverage, "buy")
		if d.Preventive {
			return out.Fail("run.start", fmt.Errorf("%s", d.BlockReason), 1)
		}
	}
	if dry {
		rt := &store.RuntimeState{BotName: bot.Name, Mode: "dry-run", Status: "dry-run", StartedAt: time.Now().UTC(),
			Meta: map[string]string{"last_price": fmt.Sprintf("%g", ticker.Price)}}
		for _, px := range plan.Prices {
			side := grid.LevelSide(px, ticker.Price)
			rt.Orders = append(rt.Orders, store.RuntimeOrder{Side: side, Price: px, Qty: bot.OrderSize, Status: "dry-run"})
		}
		out.Print(os.Stdout, "run.start", rt, fmt.Sprintf("dry run %s orders=%d (nothing sent)", bot.Name, len(rt.Orders)))
		return 0
	}
	// The guards run only inside the loop, so a live bot must have one.
	if !flagBool(args, "--foreground") {
		return out.Fail("run.start", fmt.Errorf("live runs need --foreground (the preventive close and loss guard run only while the loop runs; a detached worker is planned)"), 2)
	}
	if out.Agent() && !flagBool(args, "--yes") {
		return out.Fail("run.start", fmt.Errorf("live trading requires a person confirm; pass --yes only when a human approved"), 1)
	}
	if !flagBool(args, "--yes") {
		msg := fmt.Sprintf("place %d live grid orders for %s on %s at mid≈%g?", len(plan.Prices), bot.Name, exRec.Venue, ticker.Price)
		if !confirm(msg) {
			return out.Fail("run.start", fmt.Errorf("aborted"), 1)
		}
	}
	lock, err := live.TryLock(bot.Name)
	if err != nil {
		return out.Fail("run.start", err, 1)
	}
	defer lock.Unlock()
	live.ClearStop(bot.Name)
	// Never drop a runtime that may still have orders on the book.
	if prev, err := store.GetRuntime(bot.Name); err == nil && prev.Mode == "live" && prev.Status != "stopped" {
		return out.Fail("run.start", fmt.Errorf("bot %s has a live runtime in state %q; run `cuwan run stop %s` first", bot.Name, prev.Status, bot.Name), 1)
	}
	// Nothing touches the account before the person confirmed.
	if bot.Leverage > 0 {
		if err := drv.SetLeverage(ctx, bot.Symbol, bot.Leverage); err != nil {
			return out.Fail("run.start", err, 1)
		}
	}
	rt := &store.RuntimeState{BotName: bot.Name, Mode: "live", Status: "running", StartedAt: time.Now().UTC(),
		Meta: map[string]string{"last_price": fmt.Sprintf("%g", ticker.Price)}}
	save := func() error { return store.PutRuntime(rt) }
	if err := live.PlaceGrid(ctx, bot, drv, rt, save, ticker.Price); err != nil {
		return halt(ctx, bot, drv, rt, save, "initial grid failed: "+err.Error())
	}
	_ = store.AppendAudit("run.start", map[string]any{"bot": bot.Name, "orders": len(rt.Orders)})
	out.Print(os.Stdout, "run.start", rt, fmt.Sprintf("live start %s orders=%d", bot.Name, len(rt.Orders)))
	return startLoop(bot, drv, rt)
}

// startLoop is a seam so CLI tests can skip the blocking loop.
var startLoop = runLoop

// runEvery is the live loop interval; tests shorten it.
var runEvery = 5 * time.Second

// maxStepErrors is how many failed steps in a row halt the bot.
const maxStepErrors = 6

// runLoop steps the live engine while this process holds the bot's lock.
// It ends on a signal, a stop request from `run stop`, a guard, or too many
// errors in a row.
func runLoop(bot *store.Bot, drv exchangex.Exchange, rt *store.RuntimeState) int {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sig)
	tick := time.NewTicker(runEvery)
	defer tick.Stop()
	ctx := context.Background()
	save := func() error { return store.PutRuntime(rt) }
	fails := 0
	for {
		select {
		case <-sig:
			// Orders stay on the book with no guard watching them.
			rt.Status = "unguarded"
			_ = save()
			_ = store.AppendAudit("run.signal", map[string]any{"bot": bot.Name})
			alert(bot.Name, "loop ended by signal; orders are still on the book and unguarded; run `cuwan run stop "+bot.Name+"`")
			return 0
		case <-tick.C:
		}
		if live.StopRequested(bot.Name) {
			return 0 // release the lock; `run stop` takes over
		}
		res, err := live.Step(ctx, bot, drv, rt, save)
		var ce *live.CloseError
		if errors.As(err, &ce) {
			return halt(ctx, bot, drv, rt, save, ce.Error())
		}
		if err != nil {
			fails++
			_ = store.AppendAudit("run.step_error", map[string]any{"bot": bot.Name, "error": err.Error(), "in_a_row": fails})
			if fails >= maxStepErrors {
				return halt(ctx, bot, drv, rt, save, fmt.Sprintf("%d failed steps in a row, last: %v", fails, err))
			}
			continue
		}
		fails = 0
		if res.Stopped {
			_ = store.AppendAudit("run.guard", map[string]any{"bot": bot.Name, "reason": res.Reason})
			alert(bot.Name, res.Reason)
			return 0
		}
	}
}

// halt cancels what it can, marks the bot halted, and alerts a person.
func halt(ctx context.Context, bot *store.Bot, drv exchangex.Exchange, rt *store.RuntimeState, save live.Saver, reason string) int {
	if err := live.CancelAll(ctx, bot, drv, rt, save); err != nil {
		reason += "; cancelling also failed: " + err.Error()
	}
	rt.Status = "halted"
	if rt.Meta == nil {
		rt.Meta = map[string]string{}
	}
	rt.Meta["halt_reason"] = reason
	_ = save()
	_ = store.AppendAudit("run.halt", map[string]any{"bot": bot.Name, "reason": reason})
	alert(bot.Name, "HALTED: "+reason+"; check open orders and positions on the venue")
	return out.Fail("run.loop", fmt.Errorf("bot %s halted: %s", bot.Name, reason), 1)
}

// alert tells a person. Local notifications are planned (M6).
func alert(bot, msg string) {
	fmt.Fprintf(os.Stderr, "cuwan ALERT [%s]: %s\n", bot, msg)
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

// stopWait is how long `run stop` waits for a running loop to let go.
var stopWait = 60 * time.Second

func runStop(args []string) int {
	pos := positional(args)
	if len(pos) < 1 {
		return out.Fail("run.stop", fmt.Errorf("usage: cuwan run stop NAME [--yes] [--flatten]"), 2)
	}
	if !flagBool(args, "--yes") {
		if out.Agent() {
			return out.Fail("run.stop", fmt.Errorf("destructive stop needs --yes from a person"), 1)
		}
		if !confirm("stop live bot " + pos[0] + "?") {
			return out.Fail("run.stop", fmt.Errorf("aborted"), 1)
		}
	}
	bot, err := store.GetBot(pos[0])
	if err != nil {
		return out.Fail("run.stop", err, 1)
	}
	lock, err := takeOver(bot.Name)
	if err != nil {
		return out.Fail("run.stop", err, 1)
	}
	defer lock.Unlock()
	defer live.ClearStop(bot.Name)
	// Read the runtime only now: the loop saved every order before it let go.
	rt, err := store.GetRuntime(bot.Name)
	if err != nil {
		return out.Fail("run.stop", err, 1)
	}
	if rt.Mode == "live" && rt.Status != "stopped" {
		_, drv, err := openExchange(bot.Exchange, false)
		if err != nil {
			return out.Fail("run.stop", err, 1)
		}
		ctx := context.Background()
		save := func() error { return store.PutRuntime(rt) }
		if flagBool(args, "--flatten") {
			if err := live.Flatten(ctx, bot, drv, rt, save); err != nil {
				alert(bot.Name, "flatten failed: "+err.Error())
				return out.Fail("run.stop", fmt.Errorf("flatten: %w (bot left as is; retry or close by hand)", err), 1)
			}
			_ = store.AppendAudit("run.flatten", map[string]any{"bot": bot.Name})
		} else {
			if err := live.Reconcile(ctx, bot, drv, rt, save); err != nil {
				return out.Fail("run.stop", err, 1)
			}
			if err := live.CancelAll(ctx, bot, drv, rt, save); err != nil {
				return out.Fail("run.stop", fmt.Errorf("%w (bot left as is; retry)", err), 1)
			}
		}
	}
	rt.Status = "stopped"
	if err := store.PutRuntime(rt); err != nil {
		return out.Fail("run.stop", err, 1)
	}
	_ = store.AppendAudit("run.stop", map[string]any{"bot": bot.Name})
	out.Print(os.Stdout, "run.stop", map[string]string{"name": bot.Name}, "stopped "+bot.Name)
	return 0
}

// takeOver gets the bot's lock, asking a running loop to stop first.
func takeOver(bot string) (*live.Lock, error) {
	lock, err := live.TryLock(bot)
	if !errors.Is(err, live.ErrLocked) {
		return lock, err
	}
	if err := live.RequestStop(bot); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(stopWait)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		lock, err = live.TryLock(bot)
		if !errors.Is(err, live.ErrLocked) {
			return lock, err
		}
	}
	live.ClearStop(bot) // do not leave a request that would stop a later loop
	return nil, fmt.Errorf("the running loop did not stop within %s; nothing was cancelled", stopWait)
}
