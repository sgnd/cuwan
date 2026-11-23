package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/sgnd/cuwan/internal/exchangex"
	"github.com/sgnd/cuwan/internal/live"
	"github.com/sgnd/cuwan/internal/out"
	"github.com/sgnd/cuwan/internal/store"
)

// startLoop is a seam so tests skip the blocking loop.
var startLoop = runLoop

// runEvery is the loop interval.
var runEvery = 5 * time.Second

// maxStepErrors failed steps in a row halt the bot.
const maxStepErrors = 6

// stopWait is how long `run stop` waits for a running loop to let go.
var stopWait = 60 * time.Second

func (e *env) bot(name string) (store.Bot, error) {
	var b store.Bot
	err := e.st.View(func(s *store.State) error {
		var ok bool
		if b, ok = s.Bots[name]; !ok {
			return fmt.Errorf("no bot %q", name)
		}
		return nil
	})
	return b, err
}

// saver writes the runtime back into the store.
func (e *env) saver(rt *store.Runtime) live.Saver {
	return func() error {
		return e.st.Update(func(s *store.State) error { s.Runtimes[rt.Bot] = *rt; return nil })
	}
}

func runStart(args []string) int {
	f, err := parse(args, nil, []string{"yes", "dry-run"})
	if err == nil {
		_, err = needOne(f, "cuwan run start NAME [--dry-run] [--yes]")
	}
	if err != nil {
		return out.Fail("run.start", err, 2)
	}
	return withEnv("run.start", func(e *env) int { return start(e, f) })
}

func start(e *env, f *flags) int {
	ctx := context.Background()
	bot, err := e.bot(f.pos[0])
	if err != nil {
		return out.Fail("run.start", err, 1)
	}
	x, drv, err := e.driver(bot.Exchange)
	if err != nil {
		return out.Fail("run.start", err, 1)
	}
	t, err := drv.Ticker(ctx, bot.Symbol)
	if err != nil {
		return out.Fail("run.start", err, 1)
	}
	if f.set["dry-run"] {
		return dryRun(&bot, t.Price)
	}
	if exchangex.TestnetOnly(x.Venue) && !x.Testnet {
		return out.Fail("run.start", fmt.Errorf("%s runs on its testnet only until its testnet run is recorded; add the exchange with --testnet", x.Venue), 1)
	}
	size := fmt.Sprintf("%g per order", bot.OrderSize)
	if bot.OrderSize == 0 {
		size = fmt.Sprintf("%g quote per order", bot.QuoteAmount)
	}
	lev := "spot"
	if bot.Futures {
		lev = fmt.Sprintf("%gx futures", live.CapLeverage(bot.Leverage))
	}
	q := fmt.Sprintf("start live bot %s on %s (%s, %s, %s, %d levels %g-%g) at about %g?",
		bot.Name, x.Name, x.Venue, lev, size, bot.Levels, bot.Lower, bot.Upper, t.Price)
	if err := confirm(f, "cuwan run start "+bot.Name, q); err != nil {
		return out.Fail("run.start", err, 1)
	}
	// Fail closed: a live run needs a key proven unable to withdraw.
	p, err := drv.Permissions(ctx)
	if err != nil {
		return out.Fail("run.start", fmt.Errorf("refusing: cannot check the key's permissions: %w", err), 1)
	}
	if p.CanWithdraw {
		return out.Fail("run.start", errors.New("refusing: the key can withdraw or transfer funds"), 1)
	}
	lock, err := live.TryLock(e.dir, bot.Name)
	if err != nil {
		return out.Fail("run.start", err, 1)
	}
	defer lock.Unlock()
	live.ClearStop(e.dir, bot.Name)

	var prev store.Runtime
	had := false
	_ = e.st.View(func(s *store.State) error { prev, had = s.Runtimes[bot.Name]; return nil })
	if had && prev.Status != "stopped" {
		return resume(e, &bot, drv, &prev)
	}
	if bot.Futures {
		if err := drv.SetLeverage(ctx, bot.Symbol, live.CapLeverage(bot.Leverage)); err != nil {
			return out.Fail("run.start", err, 1)
		}
	}
	rt := &store.Runtime{Bot: bot.Name, Status: "running", StartedAt: time.Now().UTC(), Meta: map[string]string{}}
	save := e.saver(rt)
	if err := live.PlaceGrid(ctx, &bot, drv, rt, save, t.Price); err != nil {
		return halt(e, &bot, drv, rt, "the grid could not be placed: "+err.Error())
	}
	if err := e.audit("run.start", map[string]any{"bot": bot.Name, "orders": len(rt.Orders)}); err != nil {
		return out.Fail("run.start", err, 1)
	}
	out.Print(os.Stdout, "run.start", map[string]any{"name": bot.Name, "orders": len(rt.Orders), "resumed": false},
		fmt.Sprintf("started %s with %d orders; the loop runs until you stop it", bot.Name, len(rt.Orders)))
	return startLoop(e, &bot, drv, rt)
}

// resume takes over a running or unguarded runtime by client order ID.
func resume(e *env, bot *store.Bot, drv exchangex.Exchange, rt *store.Runtime) int {
	if err := live.Resume(context.Background(), bot, drv, rt, e.saver(rt)); err != nil {
		return out.Fail("run.start", fmt.Errorf("%w; run `cuwan run stop %s` first", err, bot.Name), 1)
	}
	if err := e.audit("run.resume", map[string]any{"bot": bot.Name, "orders": len(rt.Orders)}); err != nil {
		return out.Fail("run.start", err, 1)
	}
	out.Print(os.Stdout, "run.start", map[string]any{"name": bot.Name, "orders": len(rt.Orders), "resumed": true},
		fmt.Sprintf("resumed %s with %d orders, found again by client order ID", bot.Name, len(rt.Orders)))
	return startLoop(e, bot, drv, rt)
}

func dryRun(bot *store.Bot, mid float64) int {
	type planned struct {
		Side  string  `json:"side"`
		Price float64 `json:"price"`
		Qty   float64 `json:"qty"`
	}
	var orders []planned
	step := (bot.Upper - bot.Lower) / float64(bot.Levels-1)
	for i := 0; i < bot.Levels; i++ {
		px := bot.Lower + step*float64(i)
		side := exchangex.SideSell
		if px < mid {
			side = exchangex.SideBuy
		}
		q, err := live.OrderQty(bot, px)
		if err != nil {
			return out.Fail("run.start", err, 1)
		}
		orders = append(orders, planned{side, px, q})
	}
	out.Print(os.Stdout, "run.start", map[string]any{"name": bot.Name, "dry_run": true, "orders": orders},
		fmt.Sprintf("dry run %s: %d orders around %g, nothing sent", bot.Name, len(orders), mid))
	return 0
}

// runLoop steps the engine while this process holds the bot's lock. It
// ends on a signal, a stop request, a stop by the guard or an exit, or too
// many failed steps in a row.
func runLoop(e *env, bot *store.Bot, drv exchangex.Exchange, rt *store.Runtime) int {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sig)
	tick := time.NewTicker(runEvery)
	defer tick.Stop()
	ctx := context.Background()
	save := e.saver(rt)
	fails := 0
	for {
		select {
		case <-sig:
			rt.Status = "unguarded" // orders stay on the book with no guard watching
			_ = save()
			_ = e.audit("run.signal", map[string]any{"bot": bot.Name})
			alert(bot.Name, "the loop ended by a signal; orders are on the book and unguarded; run `cuwan run start "+bot.Name+"` to resume or `cuwan run stop "+bot.Name+"`")
			return 0
		case <-tick.C:
		}
		if live.StopRequested(e.dir, bot.Name) {
			return 0 // let go of the lock; `run stop` takes over
		}
		res, err := live.Step(ctx, bot, drv, rt, save)
		var ce *live.CloseError
		if errors.As(err, &ce) {
			return halt(e, bot, drv, rt, ce.Error())
		}
		if err != nil {
			fails++
			_ = e.audit("run.step_error", map[string]any{"bot": bot.Name, "error": err.Error(), "in_a_row": fails})
			if fails >= maxStepErrors {
				return halt(e, bot, drv, rt, fmt.Sprintf("%d failed steps in a row, last: %v", fails, err))
			}
			continue
		}
		fails = 0
		if res.Stopped {
			_ = e.audit("run.stopped", map[string]any{"bot": bot.Name, "reason": res.Reason})
			alert(bot.Name, res.Reason)
			return 0
		}
	}
}

// halt cancels what it can, marks the bot halted, and tells a person.
func halt(e *env, bot *store.Bot, drv exchangex.Exchange, rt *store.Runtime, reason string) int {
	if err := live.CancelAll(context.Background(), bot, drv, rt, e.saver(rt)); err != nil {
		reason += "; cancelling also failed: " + err.Error()
	}
	rt.Status = "halted"
	if rt.Meta == nil {
		rt.Meta = map[string]string{}
	}
	rt.Meta["halt_reason"] = reason
	_ = e.saver(rt)()
	_ = e.audit("run.halt", map[string]any{"bot": bot.Name, "reason": reason})
	alert(bot.Name, "HALTED: "+reason+"; check open orders and positions on the venue")
	return out.Fail("run.loop", fmt.Errorf("bot %s halted: %s", bot.Name, reason), 1)
}

func alert(bot, msg string) { fmt.Fprintf(os.Stderr, "cuwan ALERT [%s]: %s\n", bot, msg) }

func runStatus(args []string) int {
	f, err := parse(args, nil, nil)
	if err == nil {
		_, err = needOne(f, "cuwan run status NAME")
	}
	if err != nil {
		return out.Fail("run.status", err, 2)
	}
	return withEnv("run.status", func(e *env) int {
		var rt store.Runtime
		ok := false
		_ = e.st.View(func(s *store.State) error { rt, ok = s.Runtimes[f.pos[0]]; return nil })
		if !ok {
			return out.Fail("run.status", fmt.Errorf("bot %s has not run", f.pos[0]), 1)
		}
		out.Print(os.Stdout, "run.status", rt, fmt.Sprintf("%s status=%s orders=%d net=%g", rt.Bot, rt.Status, len(rt.Orders), live.NetSize(&rt)))
		return 0
	})
}

func runStop(args []string) int {
	f, err := parse(args, nil, []string{"yes", "flatten"})
	if err == nil {
		_, err = needOne(f, "cuwan run stop NAME [--flatten] [--yes]")
	}
	if err != nil {
		return out.Fail("run.stop", err, 2)
	}
	if err := confirm(f, "cuwan run stop "+strings.Join(args, " "), "stop live bot "+f.pos[0]+"?"); err != nil {
		return out.Fail("run.stop", err, 1)
	}
	return withEnv("run.stop", func(e *env) int { return stop(e, f.pos[0], f.set["flatten"]) })
}

func stop(e *env, name string, flatten bool) int {
	ctx := context.Background()
	bot, err := e.bot(name)
	if err != nil {
		return out.Fail("run.stop", err, 1)
	}
	lock, err := takeOver(e.dir, name)
	if err != nil {
		return out.Fail("run.stop", err, 1)
	}
	defer lock.Unlock()
	defer live.ClearStop(e.dir, name)
	var rt store.Runtime
	ok := false
	_ = e.st.View(func(s *store.State) error { rt, ok = s.Runtimes[name]; return nil })
	if !ok || rt.Status == "stopped" {
		out.Print(os.Stdout, "run.stop", map[string]string{"name": name}, name+" is not running")
		return 0
	}
	_, drv, err := e.driver(bot.Exchange)
	if err != nil {
		return out.Fail("run.stop", err, 1)
	}
	save := e.saver(&rt)
	if flatten {
		if err := live.Flatten(ctx, &bot, drv, &rt, save); err != nil {
			alert(name, "flatten failed: "+err.Error())
			return out.Fail("run.stop", fmt.Errorf("flatten: %w (bot left as is; retry or close by hand)", err), 1)
		}
	} else {
		if err := live.Reconcile(ctx, &bot, drv, &rt, save); err != nil {
			return out.Fail("run.stop", err, 1)
		}
		if err := live.CancelAll(ctx, &bot, drv, &rt, save); err != nil {
			return out.Fail("run.stop", fmt.Errorf("%w (bot left as is; retry)", err), 1)
		}
	}
	rt.Status = "stopped"
	if err := save(); err != nil {
		return out.Fail("run.stop", err, 1)
	}
	if err := e.audit("run.stop", map[string]any{"bot": name, "flatten": flatten}); err != nil {
		return out.Fail("run.stop", err, 1)
	}
	out.Print(os.Stdout, "run.stop", map[string]any{"name": name, "flatten": flatten, "net": live.NetSize(&rt)}, "stopped "+name)
	return 0
}

// takeOver gets the bot's lock, asking a running loop to let go first.
func takeOver(dir, bot string) (*live.Lock, error) {
	lock, err := live.TryLock(dir, bot)
	if !errors.Is(err, live.ErrLocked) {
		return lock, err
	}
	if err := live.RequestStop(dir, bot); err != nil {
		return nil, err
	}
	for deadline := time.Now().Add(stopWait); time.Now().Before(deadline); {
		time.Sleep(100 * time.Millisecond)
		if lock, err = live.TryLock(dir, bot); !errors.Is(err, live.ErrLocked) {
			return lock, err
		}
	}
	live.ClearStop(dir, bot) // do not leave a request that would stop a later loop
	return nil, fmt.Errorf("the running loop did not stop within %s; nothing was cancelled", stopWait)
}
