package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/sgnd/cuwan/internal/exchangex"
	"github.com/sgnd/cuwan/internal/out"
	"github.com/sgnd/cuwan/internal/store"
	"github.com/sgnd/cuwan/internal/vault"
)

func cmdExchange(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(os.Stdout, "usage: cuwan exchange add|list|test …")
		return 0
	}
	switch args[0] {
	case "add":
		return exchangeAdd(args[1:])
	case "list":
		return exchangeList(args[1:])
	case "test":
		return exchangeTest(args[1:])
	default:
		return out.Fail("exchange", fmt.Errorf("unknown verb %q", args[0]), 2)
	}
}

func exchangeAdd(args []string) int {
	pos := positional(args)
	if len(pos) < 1 {
		return out.Fail("exchange.add", fmt.Errorf("usage: cuwan exchange add NAME --venue binance|hyperliquid --secret secret:NAME [--market spot|usdm|perp]"), 2)
	}
	venue, _ := flagValue(args, "--venue")
	if venue == "" {
		venue = pos[0] // allow: exchange add binance --secret …
		if len(pos) > 1 {
			// NAME was first
		}
	}
	name := pos[0]
	if v, ok := flagValue(args, "--venue"); ok {
		venue = v
	} else if name == "binance" || name == "hyperliquid" {
		venue = name
	}
	if venue == "" {
		return out.Fail("exchange.add", fmt.Errorf("missing --venue"), 2)
	}
	secret, _ := flagValue(args, "--secret")
	if secret == "" {
		return out.Fail("exchange.add", fmt.Errorf("missing --secret secret:NAME"), 2)
	}
	if _, ok := vault.ParseRef(secret); !ok {
		return out.Fail("exchange.add", fmt.Errorf("--secret must be secret:NAME"), 2)
	}
	market, _ := flagValue(args, "--market")
	if market == "" {
		if venue == "hyperliquid" {
			market = exchangex.MarketPerp
		} else {
			market = exchangex.MarketSpot
		}
	}
	e := &store.Exchange{
		Name: name, Venue: venue, Market: market, SecretRef: secret,
		Testnet: flagBool(args, "--testnet"),
	}
	if err := store.PutExchange(e); err != nil {
		return out.Fail("exchange.add", err, 1)
	}
	_ = store.AppendAudit("exchange.add", map[string]any{"name": name, "venue": venue})
	out.Print(os.Stdout, "exchange.add", map[string]any{
		"name": name, "venue": venue, "market": market, "secret": out.SecretRef(strings.TrimPrefix(secret, "secret:")),
	}, fmt.Sprintf("added exchange %s (%s/%s) %s", name, venue, market, out.SecretRef(strings.TrimPrefix(secret, "secret:"))))
	return 0
}

func exchangeList(args []string) int {
	list, err := store.ListExchanges()
	if err != nil {
		if err == store.ErrNoStore {
			out.Print(os.Stdout, "exchange.list", []any{}, "(none)")
			return 0
		}
		return out.Fail("exchange.list", err, 1)
	}
	type row struct {
		Name   string `json:"name"`
		Venue  string `json:"venue"`
		Market string `json:"market"`
		Secret string `json:"secret"`
	}
	rows := make([]row, 0, len(list))
	var lines []string
	for _, e := range list {
		ref := e.SecretRef
		if n, ok := vault.ParseRef(ref); ok {
			ref = out.SecretRef(n)
		}
		rows = append(rows, row{Name: e.Name, Venue: e.Venue, Market: e.Market, Secret: ref})
		lines = append(lines, fmt.Sprintf("%s  %s/%s  %s", e.Name, e.Venue, e.Market, ref))
	}
	text := "(none)"
	if len(lines) > 0 {
		text = strings.Join(lines, "\n")
	}
	out.Print(os.Stdout, "exchange.list", rows, text)
	return 0
}

func exchangeTest(args []string) int {
	pos := positional(args)
	if len(pos) < 1 {
		return out.Fail("exchange.test", fmt.Errorf("usage: cuwan exchange test NAME"), 2)
	}
	ex, drv, err := openNamedExchange(pos[0], true)
	if err != nil {
		return out.Fail("exchange.test", err, 1)
	}
	_ = ex
	ctx := context.Background()
	symbol := "BTCUSDT"
	if drv.Name() == "hyperliquid" {
		symbol = "BTC"
	}
	if s, ok := flagValue(args, "--symbol"); ok {
		symbol = s
	}
	t, err := drv.Ticker(ctx, symbol)
	if err != nil {
		return out.Fail("exchange.test", err, 1)
	}
	perms, _ := drv.Permissions(ctx)
	if perms.CanWithdraw {
		return out.Fail("exchange.test", fmt.Errorf("API key appears withdrawal-capable; refuse live use — create a trade-only key"), 1)
	}
	out.Print(os.Stdout, "exchange.test", map[string]any{
		"name": pos[0], "symbol": symbol, "price": t.Price, "permissions": perms,
	}, fmt.Sprintf("ok %s %s = %g", pos[0], symbol, t.Price))
	return 0
}

func openNamedExchange(name string, paperOK bool) (*store.Exchange, exchangex.Exchange, error) {
	e, err := store.GetExchange(name)
	if err != nil {
		return nil, nil, err
	}
	cfg := exchangex.Config{
		Venue: e.Venue, Market: e.Market, Testnet: e.Testnet, Paper: paperOK,
	}
	if !paperOK {
		secret, err := vault.ResolveRef(e.SecretRef)
		if err != nil {
			return nil, nil, err
		}
		// Binance secrets stored as "KEY:SECRET" or JSON; Hyperliquid as "address:key" or key only.
		cfg.APIKey, cfg.APISecret = splitCred(secret, e.Venue)
	}
	drv, err := exchangex.Open(cfg)
	return e, drv, err
}

func splitCred(secret, venue string) (key, sec string) {
	if i := strings.IndexByte(secret, ':'); i > 0 {
		return secret[:i], secret[i+1:]
	}
	if venue == "hyperliquid" {
		return "", secret
	}
	return secret, secret
}
