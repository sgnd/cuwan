// Package risk enforces always-on futures guardrails: 20x leverage cap and
// ~5% preventive reduce-only close vs estimated liquidation.
package risk

import (
	"fmt"
	"math"
)

// MaxLeverage is the hard futures leverage cap. No disable flag.
const MaxLeverage = 20.0

// PreventiveClosePct fires a reduce-only close when mark is within this
// fraction of the distance from entry toward liquidation.
const PreventiveClosePct = 0.05

// CapLeverage clamps requested leverage to MaxLeverage.
func CapLeverage(requested float64) (float64, error) {
	if requested <= 0 {
		return 1, nil
	}
	if requested > MaxLeverage {
		return MaxLeverage, fmt.Errorf("risk: leverage %.2fx exceeds hard cap %.0fx; using %.0fx", requested, MaxLeverage, MaxLeverage)
	}
	return requested, nil
}

// EstimateLiq is a simple isolated-margin liquidation estimate.
// Long: entry * (1 - 1/lev); Short: entry * (1 + 1/lev). Ignores fees/MMR for MVP.
func EstimateLiq(entry, leverage float64, side string) float64 {
	if leverage <= 0 {
		leverage = 1
	}
	lev, _ := CapLeverage(leverage)
	switch side {
	case "sell", "short":
		return entry * (1 + 1/lev)
	default:
		return entry * (1 - 1/lev)
	}
}

// ApproachFraction is how close mark is to liq relative to entry→liq distance.
// 0 = at entry, 1 = at liquidation. Returns 0 when distance is tiny.
func ApproachFraction(entry, mark, liq float64) float64 {
	dist := math.Abs(liq - entry)
	if dist < 1e-12 {
		return 0
	}
	return math.Abs(mark-entry) / dist
}

// ShouldPreventiveClose reports whether mark has entered the last PreventiveClosePct
// of the path from entry toward liquidation (i.e. approach >= 1-5% = 95%).
func ShouldPreventiveClose(entry, mark, liq float64) bool {
	if liq <= 0 || entry <= 0 || mark <= 0 {
		return false
	}
	// Only when mark is between entry and liq (moving toward liq).
	toward := (liq > entry && mark > entry) || (liq < entry && mark < entry)
	if !toward {
		return false
	}
	frac := ApproachFraction(entry, mark, liq)
	return frac >= (1 - PreventiveClosePct)
}

// Decision is the result of a risk check before placing or holding.
type Decision struct {
	Leverage     float64 `json:"leverage"`
	LiqEstimate  float64 `json:"liq_estimate,omitempty"`
	Preventive   bool    `json:"preventive_close"`
	BlockReason  string  `json:"block_reason,omitempty"`
	Warn         string  `json:"warn,omitempty"`
}

// CheckFutures applies leverage cap and preventive close logic.
func CheckFutures(entry, mark, requestedLev float64, side string) Decision {
	lev, warnErr := CapLeverage(requestedLev)
	d := Decision{Leverage: lev}
	if warnErr != nil {
		d.Warn = warnErr.Error()
	}
	d.LiqEstimate = EstimateLiq(entry, lev, side)
	d.Preventive = ShouldPreventiveClose(entry, mark, d.LiqEstimate)
	if d.Preventive {
		d.BlockReason = "preventive reduce-only close: mark within 5% of path to estimated liquidation"
	}
	return d
}
