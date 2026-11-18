// Package paper simulates grid fills on live public market data without signed orders.
package paper

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/sgnd/cuwan/internal/exchangex"
	"github.com/sgnd/cuwan/internal/grid"
	"github.com/sgnd/cuwan/internal/home"
	"github.com/sgnd/cuwan/internal/risk"
	"github.com/sgnd/cuwan/internal/store"
)

// Session is an in-process paper bot.
type Session struct {
	Bot      *store.Bot
	Exchange exchangex.Exchange
	Runtime  *store.RuntimeState
	mu       sync.Mutex
	stop     chan struct{}
	stopOnce sync.Once
	done     chan struct{}
}

func (s *Session) requestStop() {
	s.stopOnce.Do(func() { close(s.stop) })
}

var (
	sessionsMu sync.Mutex
	sessions   = map[string]*Session{}
)

// Start begins a paper session for bot, using public tickers only.
// If a sealed runtime is already marked running, it resumes that order book.
func Start(ctx context.Context, bot *store.Bot, ex exchangex.Exchange) (*Session, error) {
	sessionsMu.Lock()
	defer sessionsMu.Unlock()
	if s, ok := sessions[bot.Name]; ok && s.Runtime.Status == "running" {
		return nil, fmt.Errorf("paper: bot %q already running", bot.Name)
	}
	if alive, _ := WorkerAlive(bot.Name); alive {
		return nil, fmt.Errorf("paper: bot %q worker already running", bot.Name)
	}

	var rt *store.RuntimeState
	if existing, err := store.GetRuntime(bot.Name); err == nil && existing.Mode == "paper" && existing.Status == "running" && len(existing.Orders) > 0 {
		rt = existing
		if rt.Meta == nil {
			rt.Meta = map[string]string{}
		}
		rt.Meta["resumed"] = time.Now().UTC().Format(time.RFC3339)
	} else {
		plan, err := grid.Build(bot.Lower, bot.Upper, bot.Levels)
		if err != nil {
			return nil, err
		}
		ticker, err := ex.Ticker(ctx, bot.Symbol)
		if err != nil {
			return nil, err
		}
		var orders []store.RuntimeOrder
		for _, px := range plan.Prices {
			side := grid.LevelSide(px, ticker.Price)
			qty := bot.OrderSize
			if qty <= 0 && bot.QuoteAmount > 0 {
				qty = grid.QtyFromQuote(bot.QuoteAmount/float64(bot.Levels), px)
			}
			if qty <= 0 {
				qty = 0.001
			}
			orders = append(orders, store.RuntimeOrder{
				ClientOID: grid.ClientOID(bot.Name, px, side, 1),
				Side:      side,
				Price:     px,
				Qty:       qty,
				Status:    "open",
			})
		}
		rt = &store.RuntimeState{
			BotName:   bot.Name,
			Mode:      "paper",
			Status:    "running",
			Orders:    orders,
			StartedAt: time.Now().UTC(),
			Meta: map[string]string{
				"last_price": fmt.Sprintf("%g", ticker.Price),
			},
		}
	}
	if err := store.PutRuntime(rt); err != nil {
		return nil, err
	}
	s := &Session{Bot: bot, Exchange: ex, Runtime: rt, stop: make(chan struct{}), done: make(chan struct{})}
	sessions[bot.Name] = s
	if err := writePID(bot.Name); err != nil {
		return nil, err
	}
	go s.loop()
	_ = store.AppendAudit("paper.start", map[string]any{"bot": bot.Name, "levels": len(rt.Orders), "resumed": rt.Meta["resumed"] != ""})
	return s, nil
}

// Wait blocks until the session stops.
func (s *Session) Wait() {
	<-s.done
}

func (s *Session) loop() {
	defer close(s.done)
	defer clearPID(s.Bot.Name)
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-tick.C:
			if externalStop(s.Bot.Name) {
				s.mu.Lock()
				s.Runtime.Status = "stopped"
				_ = store.PutRuntime(s.Runtime)
				s.mu.Unlock()
				return
			}
			s.step(context.Background())
		}
	}
}

func externalStop(name string) bool {
	rt, err := store.GetRuntime(name)
	if err != nil {
		return false
	}
	return rt.Status == "stopped"
}

func (s *Session) step(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := s.Exchange.Ticker(ctx, s.Bot.Symbol)
	if err != nil {
		return
	}
	if s.Runtime.Meta == nil {
		s.Runtime.Meta = map[string]string{}
	}
	s.Runtime.Meta["last_price"] = fmt.Sprintf("%g", t.Price)

	// Futures preventive close simulation.
	if s.Bot.Leverage > 0 {
		entry := (s.Bot.Lower + s.Bot.Upper) / 2
		side := "buy"
		d := risk.CheckFutures(entry, t.Price, s.Bot.Leverage, side)
		if d.Preventive {
			s.Runtime.Meta["preventive"] = "fired"
			for i := range s.Runtime.Orders {
				if s.Runtime.Orders[i].Status == "open" {
					s.Runtime.Orders[i].Status = "canceled"
				}
			}
			s.Runtime.Orders = append(s.Runtime.Orders, store.RuntimeOrder{
				ClientOID: grid.ClientOID(s.Bot.Name, t.Price, "sell", 99),
				Side:      "sell",
				Price:     t.Price,
				Qty:       s.Bot.OrderSize,
				Status:    "filled",
			})
			s.Runtime.Status = "stopped"
			_ = store.PutRuntime(s.Runtime)
			s.requestStop()
			return
		}
	}

	step := 0.0
	if s.Bot.Levels > 1 {
		step = (s.Bot.Upper - s.Bot.Lower) / float64(s.Bot.Levels-1)
	}
	for i, n := 0, len(s.Runtime.Orders); i < n; i++ {
		o := &s.Runtime.Orders[i]
		if o.Status != "open" {
			continue
		}
		filled := false
		if o.Side == "buy" && t.Price <= o.Price {
			filled = true
		}
		if o.Side == "sell" && t.Price >= o.Price {
			filled = true
		}
		if filled {
			o.Status = "filled"
			// Classic grid: the opposite order goes one level away, so it
			// takes a real move to fill (not the same price on the next tick).
			opp, px := "sell", o.Price+step
			if o.Side == "sell" {
				opp, px = "buy", o.Price-step
			}
			if px <= 0 {
				continue
			}
			s.Runtime.Orders = append(s.Runtime.Orders, store.RuntimeOrder{
				ClientOID: grid.ClientOID(s.Bot.Name, px, opp, len(s.Runtime.Orders)+1),
				Side:      opp,
				Price:     px,
				Qty:       o.Qty,
				Status:    "open",
			})
		}
	}
	_ = store.PutRuntime(s.Runtime)
}

// Stop ends a paper session (in-process or via sealed runtime signal).
func Stop(name string) error {
	sessionsMu.Lock()
	s, ok := sessions[name]
	sessionsMu.Unlock()
	if ok {
		s.requestStop()
		<-s.done
		s.mu.Lock()
		s.Runtime.Status = "stopped"
		_ = store.PutRuntime(s.Runtime)
		s.mu.Unlock()
		sessionsMu.Lock()
		delete(sessions, name)
		sessionsMu.Unlock()
		_ = store.AppendAudit("paper.stop", map[string]any{"bot": name})
		return nil
	}
	rt, err := store.GetRuntime(name)
	if err != nil {
		return fmt.Errorf("paper: no session for %q", name)
	}
	rt.Status = "stopped"
	if err := store.PutRuntime(rt); err != nil {
		return err
	}
	// Give detached worker a moment to notice; clear stale pid if dead.
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		alive, _ := WorkerAlive(name)
		if !alive {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	clearPID(name)
	_ = store.AppendAudit("paper.stop", map[string]any{"bot": name})
	return nil
}

// Status returns runtime for a bot.
func Status(name string) (*store.RuntimeState, error) {
	sessionsMu.Lock()
	s, ok := sessions[name]
	sessionsMu.Unlock()
	if ok {
		s.mu.Lock()
		defer s.mu.Unlock()
		cp := *s.Runtime
		return &cp, nil
	}
	return store.GetRuntime(name)
}

// Recover reloads sealed paper runtimes into this process when no worker pid is alive.
func Recover(ctx context.Context, openEx func(bot *store.Bot) (exchangex.Exchange, error)) error {
	rts, err := store.ListRuntimes()
	if err != nil {
		return err
	}
	for i := range rts {
		rt := rts[i]
		if rt.Status != "running" || rt.Mode != "paper" {
			continue
		}
		if alive, _ := WorkerAlive(rt.BotName); alive {
			continue
		}
		bot, err := store.GetBot(rt.BotName)
		if err != nil {
			continue
		}
		ex, err := openEx(bot)
		if err != nil {
			continue
		}
		if _, err := Start(ctx, bot, ex); err != nil {
			continue
		}
	}
	return nil
}

func pidPath(name string) (string, error) {
	dir, err := home.Ensure()
	if err != nil {
		return "", err
	}
	d := filepath.Join(dir, "paper")
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(d, name+".pid"), nil
}

func writePID(name string) error {
	p, err := pidPath(name)
	if err != nil {
		return err
	}
	return os.WriteFile(p, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600)
}

func clearPID(name string) {
	p, err := pidPath(name)
	if err != nil {
		return
	}
	_ = os.Remove(p)
}

// WorkerAlive reports whether a paper worker pid file points at a live process.
func WorkerAlive(name string) (bool, int) {
	p, err := pidPath(name)
	if err != nil {
		return false, 0
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return false, 0
	}
	pid, err := strconv.Atoi(string(bytesTrim(b)))
	if err != nil || pid <= 0 {
		return false, 0
	}
	if !processExists(pid) {
		clearPID(name)
		return false, pid
	}
	return true, pid
}

func bytesTrim(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r' || b[len(b)-1] == ' ') {
		b = b[:len(b)-1]
	}
	return b
}
