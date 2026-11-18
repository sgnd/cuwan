// Package report computes profit for one bot from its stored runtime.
// It reads only local state: no venue call, no order.
package report

import (
	"math"
	"strconv"

	"github.com/sgnd/cuwan/internal/store"
)

// DefaultFeeRate is 0.1%, a common taker fee, used when --fee is not given.
const DefaultFeeRate = 0.001

// Report is the profit summary of one runtime.
type Report struct {
	Bot        string  `json:"bot"`
	Mode       string  `json:"mode"`
	Status     string  `json:"status"`
	Fills      int     `json:"fills"`
	RoundTrips int     `json:"round_trips"`
	NetSize    float64 `json:"net_size"`
	LastPrice  float64 `json:"last_price"`
	Realised   float64 `json:"realised"`
	Unrealised float64 `json:"unrealised"`
	// Fees are an estimate at FeeRate on filled notional: the store keeps no venue fees.
	Fees     float64 `json:"fees_estimate"`
	FeeRate  float64 `json:"fee_rate"`
	Net      float64 `json:"net"`
	Estimate bool    `json:"fees_are_estimate"`
}

type lot struct{ qty, price float64 } // qty > 0 long, < 0 short

const eps = 1e-12

// Compute matches fills FIFO in stored order. A fill on the opposite side of
// the open lots closes them first; any rest opens a new lot. Reduce-only
// closes are plain fills here, so a flatten realises like any other close.
func Compute(rt *store.RuntimeState, feeRate float64) Report {
	r := Report{Bot: rt.BotName, Mode: rt.Mode, Status: rt.Status, FeeRate: feeRate, Estimate: true}
	if v, err := strconv.ParseFloat(rt.Meta["last_price"], 64); err == nil {
		r.LastPrice = v
	}
	var lots []lot
	for _, o := range rt.Orders {
		if o.Filled <= 0 {
			continue
		}
		r.Fills++
		r.Fees += o.Filled * o.Price * feeRate
		q := o.Filled
		if o.Side != "buy" {
			q = -q
		}
		closed := false
		for len(lots) > 0 && q != 0 && (lots[0].qty > 0) != (q > 0) {
			m := math.Min(math.Abs(lots[0].qty), math.Abs(q))
			if lots[0].qty > 0 {
				r.Realised += m * (o.Price - lots[0].price)
				lots[0].qty -= m
				q += m
			} else {
				r.Realised += m * (lots[0].price - o.Price)
				lots[0].qty += m
				q -= m
			}
			closed = true
			if math.Abs(lots[0].qty) < eps {
				lots = lots[1:]
			}
			if math.Abs(q) < eps {
				q = 0
			}
		}
		if closed {
			r.RoundTrips++
		}
		if q != 0 {
			lots = append(lots, lot{q, o.Price})
		}
	}
	for _, l := range lots {
		r.NetSize += l.qty
		if r.LastPrice > 0 {
			r.Unrealised += l.qty * (r.LastPrice - l.price)
		}
	}
	r.Net = r.Realised + r.Unrealised - r.Fees
	return r
}
