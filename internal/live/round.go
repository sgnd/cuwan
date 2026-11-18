package live

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/sgnd/cuwan/internal/exchangex"
)

// toGrid rounds v to a multiple of unit, down or up. A tiny tolerance keeps
// values that are already on the grid (but carry float noise) where they are.
func toGrid(v, unit float64, up bool) float64 {
	if unit <= 0 {
		return v
	}
	n := v / unit
	if up {
		n = math.Ceil(n - 1e-9)
	} else {
		n = math.Floor(n + 1e-9)
	}
	dec := 0
	if u := strconv.FormatFloat(unit, 'f', -1, 64); strings.Contains(u, ".") {
		dec = len(u) - strings.Index(u, ".") - 1
	}
	r, _ := strconv.ParseFloat(strconv.FormatFloat(n*unit, 'f', dec, 64), 64)
	return r
}

// roundReq fits an order to the symbol's filters. Grid orders round to the
// safe side (buys down, sells up). Crossing orders (closes) round toward a
// fill (sells down, buys up). Sizes round down and must reach MinQty.
func roundReq(req exchangex.PlaceReq, r exchangex.SymbolRules, crossing bool) (exchangex.PlaceReq, error) {
	up := req.Side == "sell"
	if crossing {
		up = !up
	}
	req.Price = toGrid(req.Price, r.Tick, up)
	req.Qty = toGrid(req.Qty, r.Step, false)
	if req.Qty <= 0 || req.Qty+1e-12 < r.MinQty {
		return req, fmt.Errorf("live: size %g is below the venue minimum %g for %s", req.Qty, r.MinQty, req.Symbol)
	}
	return req, nil
}
