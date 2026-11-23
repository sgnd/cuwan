package cli

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/sgnd/cuwan/internal/exchangex"
	"github.com/sgnd/cuwan/internal/out"
	"github.com/sgnd/cuwan/internal/store"
)

// markets each venue supports; the first is the default.
var markets = map[string][]string{
	"binance":     {exchangex.MarketSpot, exchangex.MarketUSDM},
	"hyperliquid": {exchangex.MarketPerp},
}

func exchangeAdd(args []string) int {
	f, err := parse(args, []string{"venue", "market", "secret"}, []string{"testnet"})
	if err == nil {
		_, err = needOne(f, "cuwan exchange add NAME --venue binance|hyperliquid --secret SECRET [--market spot|usdm|perp] [--testnet]")
	}
	if err != nil {
		return out.Fail("exchange.add", err, 2)
	}
	ms, ok := markets[f.vals["venue"]]
	if !ok {
		return out.Fail("exchange.add", fmt.Errorf("--venue must be binance or hyperliquid"), 2)
	}
	market := f.vals["market"]
	if market == "" {
		market = ms[0]
	}
	if !contains(ms, market) {
		return out.Fail("exchange.add", fmt.Errorf("%s has markets %s", f.vals["venue"], strings.Join(ms, ", ")), 2)
	}
	x := store.Exchange{Name: f.pos[0], Venue: f.vals["venue"], Market: market, Secret: f.vals["secret"], Testnet: f.set["testnet"], CreatedAt: time.Now().UTC()}
	return withEnv("exchange.add", func(e *env) int {
		err := e.st.Update(func(s *store.State) error {
			if _, ok := s.Secrets[x.Secret]; !ok {
				return fmt.Errorf("no secret %q; add it with `cuwan secret set %s`", x.Secret, x.Secret)
			}
			if _, ok := s.Exchanges[x.Name]; ok {
				return fmt.Errorf("exchange %s exists", x.Name)
			}
			s.Exchanges[x.Name] = x
			return s.Append(map[string]any{"command": "exchange.add", "name": x.Name, "venue": x.Venue, "agent": out.Agent()})
		})
		if err != nil {
			return out.Fail("exchange.add", err, 1)
		}
		out.Print(os.Stdout, "exchange.add", x, fmt.Sprintf("added %s (%s %s testnet=%v)", x.Name, x.Venue, x.Market, x.Testnet))
		return 0
	})
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func exchangeList(args []string) int {
	return withEnv("exchange.list", func(e *env) int {
		list := []store.Exchange{}
		_ = e.st.View(func(s *store.State) error {
			for _, x := range s.Exchanges {
				list = append(list, x)
			}
			return nil
		})
		sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
		var lines []string
		for _, x := range list {
			lines = append(lines, fmt.Sprintf("%s  %s %s testnet=%v secret=%s", x.Name, x.Venue, x.Market, x.Testnet, out.SecretRef(x.Secret)))
		}
		out.Print(os.Stdout, "exchange.list", list, strings.Join(lines, "\n"))
		return 0
	})
}

// driver opens the venue driver of an exchange with its key. The secret is
// KEY:SECRET (Binance) or ADDRESS:PRIVATE_KEY (Hyperliquid).
func (e *env) driver(name string) (store.Exchange, exchangex.Exchange, error) {
	var x store.Exchange
	var secret string
	err := e.st.View(func(s *store.State) error {
		var ok bool
		if x, ok = s.Exchanges[name]; !ok {
			return fmt.Errorf("no exchange %q", name)
		}
		secret = s.Secrets[x.Secret]
		return nil
	})
	if err != nil {
		return x, nil, err
	}
	key, sec, ok := strings.Cut(secret, ":")
	if !ok {
		return x, nil, fmt.Errorf("secret %s must be KEY:SECRET", x.Secret)
	}
	d, err := openDriver(exchangex.Config{Venue: x.Venue, Market: x.Market, APIKey: key, APISecret: sec, Testnet: x.Testnet})
	return x, d, err
}

// exchangeTest checks the key: it must read and must not withdraw.
func exchangeTest(args []string) int {
	f, err := parse(args, nil, nil)
	if err == nil {
		_, err = needOne(f, "cuwan exchange test NAME")
	}
	if err != nil {
		return out.Fail("exchange.test", err, 2)
	}
	return withEnv("exchange.test", func(e *env) int {
		_, d, err := e.driver(f.pos[0])
		if err != nil {
			return out.Fail("exchange.test", err, 1)
		}
		p, err := d.Permissions(context.Background())
		if err != nil {
			return out.Fail("exchange.test", fmt.Errorf("cannot check the key's permissions: %w", err), 1)
		}
		if p.CanWithdraw {
			return out.Fail("exchange.test", fmt.Errorf("this key can withdraw or transfer funds; make a key without that right"), 1)
		}
		out.Print(os.Stdout, "exchange.test", p, fmt.Sprintf("ok: read=%v trade=%v withdraw=false", p.CanRead, p.CanTrade))
		return 0
	})
}
