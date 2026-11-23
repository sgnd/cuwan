package live

import (
	"context"
	"fmt"
	"math"
	"strconv"

	"github.com/sgnd/cuwan/internal/exchangex"
	"github.com/sgnd/cuwan/internal/store"
)

// What a bot does when the price leaves its range.
const (
	OnExitStop  = "stop"  // flatten and stop
	OnExitPause = "pause" // cancel the grid, keep the position, place it again when the price is back (default)
	OnExitTrail = "trail" // move the range by whole grid steps to the price and place a new grid
)

// ValidOnExit reports whether s is an on-exit mode ("" means pause).
func ValidOnExit(s string) bool {
	return s == "" || s == OnExitStop || s == OnExitPause || s == OnExitTrail
}

// Bounds returns the bot's range for this run: the saved range, moved by
// any trail shift kept in the runtime.
func Bounds(bot *store.Bot, rt *store.Runtime) (float64, float64) {
	shift := 0.0
	if rt != nil && rt.Meta != nil {
		shift, _ = strconv.ParseFloat(rt.Meta["trail_shift"], 64)
	}
	return bot.Lower + shift, bot.Upper + shift
}

func gridStep(bot *store.Bot) float64 {
	if bot.Levels < 2 {
		return 0
	}
	return (bot.Upper - bot.Lower) / float64(bot.Levels-1)
}

// exits applies take-profit, stop-loss and the on-exit mode. done means
// Step must return res now.
func exits(ctx context.Context, bot *store.Bot, ex exchangex.Exchange, rt *store.Runtime, save Saver, px float64) (Result, bool, error) {
	stop := func(reason string) (Result, bool, error) {
		rt.Meta["exit"] = reason
		if err := Flatten(ctx, bot, ex, rt, save); err != nil {
			return Result{}, true, &CloseError{Err: err}
		}
		return Result{Stopped: true, Reason: reason}, true, nil
	}
	if bot.TakeProfit > 0 && px >= bot.TakeProfit {
		return stop(fmt.Sprintf("take-profit: price %g reached %g", px, bot.TakeProfit))
	}
	if bot.StopLoss > 0 && px <= bot.StopLoss {
		return stop(fmt.Sprintf("stop-loss: price %g reached %g", px, bot.StopLoss))
	}
	lo, hi := Bounds(bot, rt)
	inside := px >= lo && px <= hi
	switch bot.OnExit {
	case OnExitStop:
		if !inside {
			return stop(fmt.Sprintf("price %g left the range %g..%g", px, lo, hi))
		}
	case OnExitTrail:
		if !inside {
			return trail(ctx, bot, ex, rt, save, px)
		}
	default: // pause
		if !inside && rt.Meta["paused"] == "" {
			if err := CancelAll(ctx, bot, ex, rt, save); err != nil {
				return Result{}, true, err
			}
			rt.Meta["paused"] = fmt.Sprintf("price %g left the range %g..%g", px, lo, hi)
			return Result{}, true, save()
		}
		if !inside {
			return Result{}, true, nil
		}
		if rt.Meta["paused"] != "" {
			delete(rt.Meta, "paused")
			if err := PlaceGrid(ctx, bot, ex, rt, save, px); err != nil {
				return Result{}, true, err
			}
			return Result{}, true, save()
		}
	}
	return Result{}, false, nil
}

// trail cancels the grid, moves the range by whole steps until px is
// inside, and places a new grid. The position stays; the guard keeps
// watching it.
func trail(ctx context.Context, bot *store.Bot, ex exchangex.Exchange, rt *store.Runtime, save Saver, px float64) (Result, bool, error) {
	step := gridStep(bot)
	if step <= 0 {
		return Result{}, true, fmt.Errorf("trail needs at least two levels")
	}
	lo, hi := Bounds(bot, rt)
	var n float64
	if px > hi {
		n = math.Ceil((px - hi) / step)
	} else {
		n = -math.Ceil((lo - px) / step)
	}
	// On spot a falling price would pull the range down without end while the
	// bot keeps buying: one range width is the most it may move down.
	if !bot.Futures && lo+n*step-bot.Lower < -(bot.Upper-bot.Lower)-1e-9 {
		rt.Meta["exit"] = "trail limit"
		if err := Flatten(ctx, bot, ex, rt, save); err != nil {
			return Result{}, true, &CloseError{Err: err}
		}
		return Result{Stopped: true, Reason: fmt.Sprintf("trail limit: the range would move more than one width below %g-%g", bot.Lower, bot.Upper)}, true, nil
	}
	if lo+n*step <= 0 {
		return Result{}, true, fmt.Errorf("trail would move the range to %g or below; stop the bot", lo+n*step)
	}
	if err := CancelAll(ctx, bot, ex, rt, save); err != nil {
		return Result{}, true, err
	}
	shift := lo + n*step - bot.Lower
	rt.Meta["trail_shift"] = strconv.FormatFloat(shift, 'g', -1, 64)
	if err := PlaceGrid(ctx, bot, ex, rt, save, px); err != nil {
		return Result{}, true, err
	}
	return Result{}, true, save()
}

// Resume takes over a saved live runtime after a restart: it keeps the run
// ID, so every order is found again by its client order ID, and resolves
// pending sends. It never places a new grid. A halted runtime needs a
// person (`cuwan run stop`).
func Resume(ctx context.Context, bot *store.Bot, ex exchangex.Exchange, rt *store.Runtime, save Saver) error {
	if rt.Status != "running" && rt.Status != "unguarded" {
		return fmt.Errorf("runtime of %s is %s; only a running or unguarded runtime can resume", bot.Name, rt.Status)
	}
	_ = RunID(rt)
	if err := Reconcile(ctx, bot, ex, rt, save); err != nil {
		return err
	}
	rt.Status = "running"
	return save()
}
