package cli

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/sgnd/cuwan/internal/live"
	"github.com/sgnd/cuwan/internal/out"
	"github.com/sgnd/cuwan/internal/store"
)

var botFlags = []string{"exchange", "symbol", "lower", "upper", "levels", "order-size", "quote-amount", "leverage", "on-exit", "take-profit", "stop-loss"}

const botUsage = "cuwan bot add NAME --exchange EX --symbol SYM --lower N --upper N --levels N (--order-size N | --quote-amount N) [--leverage N] [--on-exit stop|pause|trail] [--take-profit PRICE] [--stop-loss PRICE]"

// apply copies the given flags onto b.
func applyBot(b *store.Bot, f *flags) error {
	if f.set["exchange"] {
		b.Exchange = f.vals["exchange"]
	}
	if f.set["symbol"] {
		b.Symbol = f.vals["symbol"]
	}
	if f.set["on-exit"] {
		b.OnExit = f.vals["on-exit"]
	}
	if f.set["levels"] {
		n, err := strconv.Atoi(f.vals["levels"])
		if err != nil {
			return errors.New("--levels must be a whole number")
		}
		b.Levels = n
	}
	for name, dst := range map[string]*float64{
		"lower": &b.Lower, "upper": &b.Upper, "order-size": &b.OrderSize, "quote-amount": &b.QuoteAmount,
		"leverage": &b.Leverage, "take-profit": &b.TakeProfit, "stop-loss": &b.StopLoss,
	} {
		if f.set[name] {
			v, err := f.num(name)
			if err != nil {
				return err
			}
			*dst = v
		}
	}
	return nil
}

// validBot checks a bot against the store it will live in.
func validBot(b *store.Bot, s *store.State) error {
	x, ok := s.Exchanges[b.Exchange]
	switch {
	case !ok:
		return fmt.Errorf("no exchange %q", b.Exchange)
	case b.Symbol == "":
		return errors.New("--symbol is needed")
	case b.Lower <= 0 || b.Upper <= b.Lower:
		return errors.New("need 0 < --lower < --upper")
	case b.Levels < 2:
		return errors.New("--levels must be at least 2")
	case b.OrderSize < 0 || b.QuoteAmount < 0 || b.Leverage < 0 || b.TakeProfit < 0 || b.StopLoss < 0:
		return errors.New("sizes, leverage and prices must not be negative")
	case b.OrderSize > 0 && b.QuoteAmount > 0:
		return errors.New("set --order-size or --quote-amount, not both")
	case b.OrderSize == 0 && b.QuoteAmount == 0:
		return live.ErrNoSize
	case !live.ValidOnExit(b.OnExit):
		return errors.New("--on-exit must be stop, pause or trail")
	case b.TakeProfit > 0 && b.StopLoss > 0 && b.StopLoss >= b.TakeProfit:
		return errors.New("--stop-loss must be below --take-profit")
	case b.Leverage > 0 && x.Market == "spot":
		return errors.New("--leverage is for futures; this exchange is spot")
	}
	b.Leverage = live.CapLeverage(b.Leverage)
	return nil
}

func botAdd(args []string) int { return botSave("bot.add", args, true) }

func botEdit(args []string) int { return botSave("bot.edit", args, false) }

func botSave(cmd string, args []string, create bool) int {
	f, err := parse(args, botFlags, nil)
	if err == nil {
		_, err = needOne(f, botUsage)
	}
	if err != nil {
		return out.Fail(cmd, err, 2)
	}
	return withEnv(cmd, func(e *env) int {
		var b store.Bot
		usage := false
		err := e.st.Update(func(s *store.State) error {
			old, exists := s.Bots[f.pos[0]]
			if create && exists {
				return fmt.Errorf("bot %s exists; use `cuwan bot edit`", f.pos[0])
			}
			if !create && !exists {
				return fmt.Errorf("no bot %q", f.pos[0])
			}
			if rt, ok := s.Runtimes[f.pos[0]]; ok && rt.Status != "stopped" {
				return fmt.Errorf("bot %s is %s; stop it before changing it", f.pos[0], rt.Status)
			}
			b = old
			b.Name = f.pos[0]
			if err := applyBot(&b, f); err != nil {
				usage = true
				return err
			}
			if err := validBot(&b, s); err != nil {
				usage = true
				return err
			}
			s.Bots[b.Name] = b
			return s.Append(map[string]any{"command": cmd, "name": b.Name, "agent": out.Agent()})
		})
		if err != nil {
			code := 1
			if usage {
				code = 2
			}
			return out.Fail(cmd, err, code)
		}
		out.Print(os.Stdout, cmd, b, fmt.Sprintf("%s %s %s %g-%g x%d", b.Name, b.Exchange, b.Symbol, b.Lower, b.Upper, b.Levels))
		return 0
	})
}

func botList(args []string) int {
	return withEnv("bot.list", func(e *env) int {
		list := []store.Bot{}
		_ = e.st.View(func(s *store.State) error {
			for _, b := range s.Bots {
				list = append(list, b)
			}
			return nil
		})
		sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
		var lines []string
		for _, b := range list {
			lines = append(lines, fmt.Sprintf("%s  %s %s %g-%g x%d", b.Name, b.Exchange, b.Symbol, b.Lower, b.Upper, b.Levels))
		}
		out.Print(os.Stdout, "bot.list", list, strings.Join(lines, "\n"))
		return 0
	})
}

func botShow(args []string) int {
	f, err := parse(args, nil, nil)
	if err == nil {
		_, err = needOne(f, "cuwan bot show NAME")
	}
	if err != nil {
		return out.Fail("bot.show", err, 2)
	}
	return withEnv("bot.show", func(e *env) int {
		var b store.Bot
		ok := false
		_ = e.st.View(func(s *store.State) error { b, ok = s.Bots[f.pos[0]]; return nil })
		if !ok {
			return out.Fail("bot.show", fmt.Errorf("no bot %q", f.pos[0]), 1)
		}
		onExit := b.OnExit
		if onExit == "" {
			onExit = live.OnExitPause
		}
		out.Print(os.Stdout, "bot.show", b, fmt.Sprintf("%s\n  exchange: %s\n  symbol: %s\n  range: %g - %g\n  levels: %d\n  order size: %g\n  quote amount: %g\n  leverage: %g (cap %g)\n  on exit: %s\n  take profit: %g\n  stop loss: %g",
			b.Name, b.Exchange, b.Symbol, b.Lower, b.Upper, b.Levels, b.OrderSize, b.QuoteAmount, b.Leverage, live.MaxLeverage, onExit, b.TakeProfit, b.StopLoss))
		return 0
	})
}

func botRm(args []string) int {
	f, err := parse(args, nil, []string{"yes"})
	if err == nil {
		_, err = needOne(f, "cuwan bot rm NAME [--yes]")
	}
	if err != nil {
		return out.Fail("bot.rm", err, 2)
	}
	if !confirmed(f, "remove bot "+f.pos[0]+"?") {
		return out.Fail("bot.rm", errNeedsPerson, 1)
	}
	return withEnv("bot.rm", func(e *env) int {
		err := e.st.Update(func(s *store.State) error {
			if _, ok := s.Bots[f.pos[0]]; !ok {
				return fmt.Errorf("no bot %q", f.pos[0])
			}
			if rt, ok := s.Runtimes[f.pos[0]]; ok && rt.Status != "stopped" {
				return fmt.Errorf("bot %s is %s; stop it first", f.pos[0], rt.Status)
			}
			delete(s.Bots, f.pos[0])
			delete(s.Runtimes, f.pos[0])
			return s.Append(map[string]any{"command": "bot.rm", "name": f.pos[0], "agent": out.Agent()})
		})
		if err != nil {
			return out.Fail("bot.rm", err, 1)
		}
		out.Print(os.Stdout, "bot.rm", map[string]string{"name": f.pos[0]}, "removed "+f.pos[0])
		return 0
	})
}
