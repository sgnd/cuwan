// Package grid computes arithmetic grid levels and client order IDs.
package grid

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
)

// Plan is a set of buy/sell levels across a price range.
type Plan struct {
	Lower  float64
	Upper  float64
	Levels int
	Prices []float64
}

// Build returns arithmetic grid prices from lower to upper inclusive.
func Build(lower, upper float64, levels int) (Plan, error) {
	if lower <= 0 || upper <= 0 || upper <= lower {
		return Plan{}, fmt.Errorf("grid: need 0 < lower < upper")
	}
	if levels < 2 {
		return Plan{}, fmt.Errorf("grid: levels must be >= 2")
	}
	step := (upper - lower) / float64(levels-1)
	prices := make([]float64, levels)
	for i := 0; i < levels; i++ {
		prices[i] = lower + step*float64(i)
	}
	prices[levels-1] = upper // avoid float drift on last
	return Plan{Lower: lower, Upper: upper, Levels: levels, Prices: prices}, nil
}

// LevelSide picks buy below mid, sell at/above mid for initial placement.
func LevelSide(price, mid float64) string {
	if price < mid {
		return "buy"
	}
	return "sell"
}

// ClientOID builds an idempotent client order id for a bot level.
func ClientOID(botName string, price float64, side string, generation int) string {
	raw := fmt.Sprintf("cuwan|%s|%.8f|%s|%d", botName, price, side, generation)
	sum := sha256.Sum256([]byte(raw))
	return "cw" + hex.EncodeToString(sum[:10])
}

// QtyFromQuote sizes an order as quote/price.
func QtyFromQuote(quote, price float64) float64 {
	if price <= 0 {
		return 0
	}
	return quote / price
}

// RoundPrice rounds to decimals (simple).
func RoundPrice(px float64, decimals int) float64 {
	p := math.Pow10(decimals)
	return math.Round(px*p) / p
}
