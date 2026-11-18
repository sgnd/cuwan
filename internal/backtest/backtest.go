// Package backtest replays a grid bot over historical candles with the
// same rules as paper mode and the same guards as the live loop.
package backtest

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/sgnd/cuwan/internal/grid"
	"github.com/sgnd/cuwan/internal/risk"
)

// ErrNoCandles means the source returned nothing for the range.
var ErrNoCandles = errors.New("backtest: no candles in that range")

// Config is the part of a saved bot the replay needs.
type Config struct {
	Symbol      string
	Lower       float64
	Upper       float64
	Levels      int
	OrderSize   float64
	QuoteAmount float64
	Leverage    float64 // 0 means spot
	FeeRate     float64 // per fill, of the notional (0.001 = 0.1%)
}

// Guard says whether a guard stopped the bot, and when.
type Guard struct {
	Fired  bool      `json:"fired"`
	Time   time.Time `json:"time,omitempty"`
	Price  float64   `json:"price,omitempty"`
	Reason string    `json:"reason,omitempty"`
}

// Report is the result of one replay. Money is in the quote currency.
type Report struct {
	Symbol      string    `json:"symbol"`
	Interval    string    `json:"interval"`
	From        time.Time `json:"from"`
	To          time.Time `json:"to"`
	Candles     int       `json:"candles"`
	FirstOpen   float64   `json:"first_open"`
	LastClose   float64   `json:"last_close"`
	Leverage    float64   `json:"leverage,omitempty"`
	Warn        string    `json:"warn,omitempty"`
	Fills       int       `json:"fills"`
	RoundTrips  int       `json:"round_trips"`
	Realised    float64   `json:"realised"`
	Unrealised  float64   `json:"unrealised"`
	Fees        float64   `json:"fees"`
	Net         float64   `json:"net"`
	Position    float64   `json:"position"`
	MaxDrawdown float64   `json:"max_drawdown"`
	Guard       Guard     `json:"guard"`
}

type order struct {
	side   string
	price  float64
	qty    float64
	parent float64 // price of the fill that placed it; 0 for the first levels
	open   bool
}

type sim struct {
	cfg      Config
	step     float64
	leverage float64
	orders   []order
	pos      float64 // base units, buys minus sells
	entry    float64 // average entry of pos
	cash     float64 // quote, before fees
	peak     float64
	stopped  bool
	rep      Report
}

// Run fetches candles from src and replays the grid over them.
func Run(ctx context.Context, src CandleSource, cfg Config, interval string, from, to time.Time) (Report, error) {
	candles, err := src.Candles(ctx, cfg.Symbol, interval, from, to)
	if err != nil {
		return Report{}, err
	}
	rep, err := Replay(cfg, candles)
	rep.Interval, rep.From, rep.To = interval, from, to
	return rep, err
}

// Replay runs the grid over candles in order.
func Replay(cfg Config, candles []Candle) (Report, error) {
	if len(candles) == 0 {
		return Report{Symbol: cfg.Symbol}, ErrNoCandles
	}
	plan, err := grid.Build(cfg.Lower, cfg.Upper, cfg.Levels)
	if err != nil {
		return Report{Symbol: cfg.Symbol}, err
	}
	s := &sim{cfg: cfg, step: (cfg.Upper - cfg.Lower) / float64(cfg.Levels-1), rep: Report{Symbol: cfg.Symbol}}
	if cfg.Leverage > 0 {
		lev, warn := risk.CapLeverage(cfg.Leverage)
		s.leverage, s.rep.Leverage = lev, lev
		if warn != nil {
			s.rep.Warn = warn.Error()
		}
	}
	first := candles[0].Open
	for _, px := range plan.Prices {
		s.orders = append(s.orders, order{side: grid.LevelSide(px, first), price: px, qty: s.qty(px), open: true})
	}
	for _, c := range candles {
		if !s.stopped {
			s.candle(c)
		}
		s.drawdown(c)
	}
	last := candles[len(candles)-1].Close
	s.rep.Candles = len(candles)
	s.rep.FirstOpen = first
	s.rep.LastClose = last
	s.rep.Position = s.pos
	s.rep.Unrealised = s.cash + s.pos*last - s.rep.Realised
	s.rep.Net = s.rep.Realised + s.rep.Unrealised - s.rep.Fees
	return s.rep, nil
}

// qty sizes an order the way paper does.
func (s *sim) qty(px float64) float64 {
	if s.cfg.OrderSize > 0 {
		return s.cfg.OrderSize
	}
	if s.cfg.QuoteAmount > 0 {
		return grid.QtyFromQuote(s.cfg.QuoteAmount/float64(s.cfg.Levels), px)
	}
	return 0.001
}

func (s *sim) candle(c Candle) {
	var hit []int
	for i, o := range s.orders {
		if o.open && ((o.side == "buy" && c.Low <= o.price) || (o.side == "sell" && c.High >= o.price)) {
			hit = append(hit, i)
		}
	}
	// A candle has no path, only OHLC; price starts at the open, so the level nearest it was most likely reached first.
	sort.SliceStable(hit, func(a, b int) bool {
		return math.Abs(s.orders[hit[a]].price-c.Open) < math.Abs(s.orders[hit[b]].price-c.Open)
	})
	for _, i := range hit {
		s.fill(i)
	}
	s.guard(c)
}

// fill books order i and places the opposite order one level away.
// The new order waits for the next candle: the path after the fill is unknown.
func (s *sim) fill(i int) {
	o := s.orders[i]
	s.orders[i].open = false
	s.trade(o.side, o.price, o.qty)
	if o.parent > 0 {
		s.rep.RoundTrips++
		if o.side == "sell" {
			s.rep.Realised += (o.price - o.parent) * o.qty
		} else {
			s.rep.Realised += (o.parent - o.price) * o.qty
		}
	}
	opp, px := "sell", o.price+s.step
	if o.side == "sell" {
		opp, px = "buy", o.price-s.step
	}
	if px > 0 {
		s.orders = append(s.orders, order{side: opp, price: px, qty: o.qty, parent: o.price, open: true})
	}
}

// trade moves cash and position, charges the fee, and keeps the average entry.
func (s *sim) trade(side string, px, qty float64) {
	s.rep.Fills++
	s.rep.Fees += px * qty * s.cfg.FeeRate
	d := qty
	if side == "sell" {
		d = -qty
	}
	switch {
	case s.pos == 0 || (s.pos > 0) == (d > 0):
		s.entry = (s.entry*math.Abs(s.pos) + px*qty) / (math.Abs(s.pos) + qty)
	case math.Abs(d) > math.Abs(s.pos):
		s.entry = px
	}
	s.pos += d
	s.cash -= d * px
	if math.Abs(s.pos) < 1e-12 {
		s.pos, s.entry = 0, 0
	}
}

// guard applies the live loop's rules after the candle's fills.
func (s *sim) guard(c Candle) {
	if s.cfg.Leverage <= 0 {
		floor := s.cfg.Lower * (1 - risk.PreventiveClosePct)
		if c.Low < floor {
			// Live stops a spot bot and keeps the coins, so nothing is sold.
			s.stop(c.Time, floor, fmt.Sprintf("spot loss guard: price fell more than %g%% below the grid", risk.PreventiveClosePct*100))
		}
		return
	}
	if s.pos == 0 {
		return
	}
	side, worst, closeSide := "buy", c.Low, "sell"
	if s.pos < 0 {
		side, worst, closeSide = "sell", c.High, "buy"
	}
	d := risk.CheckFutures(s.entry, worst, s.leverage, side)
	if !d.Preventive {
		return
	}
	// Close at the trigger point, not the candle extreme: live acts on the first mark past it.
	px := s.entry + (1-risk.PreventiveClosePct)*(d.LiqEstimate-s.entry)
	pos, entry := s.pos, s.entry
	s.trade(closeSide, px, math.Abs(pos))
	s.rep.Realised += (px - entry) * pos
	s.stop(c.Time, px, "preventive close: mark within 5% of the path to liquidation")
}

func (s *sim) stop(t time.Time, px float64, reason string) {
	s.stopped = true
	s.rep.Guard = Guard{Fired: true, Time: t, Price: px, Reason: reason}
	for i := range s.orders {
		s.orders[i].open = false
	}
}

// drawdown measures equity at the candle's worst price for the position.
func (s *sim) drawdown(c Candle) {
	worst := c.Low
	if s.pos < 0 {
		worst = c.High
	}
	low := s.cash + s.pos*worst - s.rep.Fees
	if s.peak-low > s.rep.MaxDrawdown {
		s.rep.MaxDrawdown = s.peak - low
	}
	if atClose := s.cash + s.pos*c.Close - s.rep.Fees; atClose > s.peak {
		s.peak = atClose
	}
}
