package live

import (
	"context"
	"fmt"
	"math"

	"github.com/sgnd/cuwan/internal/exchangex"
	"github.com/sgnd/cuwan/internal/store"
)

// The guard's constants. They have no switch: no flag, environment variable
// or bot field changes them.
const (
	// closeAt: the close fires once the mark price has gone this share of
	// the way from entry to the liquidation price.
	closeAt = 0.95
	// minGap: the close also fires within this share of the entry price of
	// the liquidation price, so a small distance still leaves room.
	minGap = 0.002
	// spotDrop: a spot bot stops this far below its range.
	spotDrop = 0.05
)

// LiqEstimate is a liquidation price that cannot be beyond the real one.
// The venue liquidates when the loss reaches the initial margin (1/lev)
// minus the maintenance margin. Venues keep the maintenance rate at or
// below half the initial rate for the leverage they allow, so assuming
// half gives a price at or before the real liquidation:
//
//	long:  entry * (1 - 1/(2*lev))
//	short: entry * (1 + 1/(2*lev))
func LiqEstimate(entry, lev float64, long bool) float64 {
	lev = CapLeverage(math.Max(lev, 1))
	if long {
		return entry * (1 - 1/(2*lev))
	}
	return entry * (1 + 1/(2*lev))
}

// Trigger is the mark price at which the guard closes a position. venueLiq
// is the venue's own liquidation price (0 when it gave none); when it is on
// the losing side of entry it is used, else the estimate is. The trigger is
// closeAt of the way from entry to liquidation, and never closer to the
// liquidation price than minGap of entry.
func Trigger(entry, venueLiq, lev float64, long bool) float64 {
	liq := LiqEstimate(entry, lev, long)
	if venueLiq > 0 && ((long && venueLiq < entry) || (!long && venueLiq > entry)) {
		liq = venueLiq
	}
	gap := math.Max((1-closeAt)*math.Abs(entry-liq), minGap*entry)
	if long {
		return liq + gap
	}
	return liq - gap
}

// guard returns a reason to stop, or "".
//   - futures: the mark price reached the trigger for the bot's own position
//   - spot: the price is more than spotDrop below the range
func guard(ctx context.Context, bot *store.Bot, ex exchangex.Exchange, rt *store.Runtime, last float64) (string, error) {
	if !bot.Futures {
		lo, _ := Bounds(bot, rt)
		if math.IsNaN(last) || math.IsInf(last, 0) || math.IsNaN(lo) || math.IsInf(lo, 0) {
			return "", fmt.Errorf("live: spot guard input is not a finite number (price %v, range %v); the bot stops", last, lo)
		}
		if last < lo*(1-spotDrop) {
			return fmt.Sprintf("spot loss guard: price %g is more than %g%% below the range", last, spotDrop*100), nil
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
	mark, err := ex.MarkPrice(ctx, bot.Symbol)
	if err != nil {
		return "", err
	}
	entry := p.EntryPrice
	if entry <= 0 {
		lo, hi := Bounds(bot, rt)
		entry = (lo + hi) / 2
	}
	long := net > 0
	t := Trigger(entry, p.LiquidationPrice, bot.Leverage, long)
	// A value that is not a finite number would make every comparison
	// false and switch the guard off: stop the bot instead.
	for _, v := range []float64{entry, t, mark, bot.Leverage} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return "", fmt.Errorf("live: guard input is not a finite number (entry %v, trigger %v, mark %v, leverage %v); the bot stops", entry, t, mark, bot.Leverage)
		}
	}
	if (long && mark <= t) || (!long && mark >= t) {
		return fmt.Sprintf("preventive close: mark %g reached %g, before liquidation", mark, t), nil
	}
	return "", nil
}
