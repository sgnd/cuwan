package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/sgnd/cuwan/internal/exchangex"
	"github.com/sgnd/cuwan/internal/out"
	"github.com/sgnd/cuwan/internal/store"
)

// fakeEx is a venue that records calls and never touches a network.
type fakeEx struct {
	perms    exchangex.Permissions
	permsErr error
	calls    []string
	price    float64
	fillBuys bool // grid buys fill at once; the position follows
	orders   map[string]exchangex.PlaceReq
	held     float64
	closes   int
	leverage float64
}

func (f *fakeEx) note(c string)  { f.calls = append(f.calls, c) }
func (f *fakeEx) Name() string   { return "fake" }
func (f *fakeEx) Market() string { return "usdm" }
func (f *fakeEx) px() float64 {
	if f.price == 0 {
		return 100
	}
	return f.price
}
func (f *fakeEx) Ticker(context.Context, string) (exchangex.Ticker, error) {
	return exchangex.Ticker{Price: f.px()}, nil
}
func (f *fakeEx) MarkPrice(context.Context, string) (float64, error) { return f.px(), nil }
func (f *fakeEx) Balances(context.Context) ([]exchangex.Balance, error) {
	return []exchangex.Balance{{Asset: "USDT", Free: 1000}}, nil
}
func (f *fakeEx) Positions(context.Context) ([]exchangex.Position, error) {
	if f.held == 0 {
		return nil, nil
	}
	return []exchangex.Position{{Symbol: "BTCUSDT", Side: "long", Size: f.held, EntryPrice: 95}}, nil
}
func (f *fakeEx) OpenOrders(context.Context, string) ([]exchangex.Order, error) {
	return nil, nil
}
func (f *fakeEx) PlaceOrder(_ context.Context, r exchangex.PlaceReq) (exchangex.Order, error) {
	f.note("place")
	if f.orders == nil {
		f.orders = map[string]exchangex.PlaceReq{}
	}
	f.orders[r.ClientOID] = r
	if r.IOC && r.ReduceOnly {
		f.closes++
		f.held -= r.Qty
		return exchangex.Order{ID: r.ClientOID, ClientOID: r.ClientOID, Status: "filled", Filled: r.Qty}, nil
	}
	if f.fillBuys && r.Side == "buy" {
		f.held += r.Qty
		return exchangex.Order{ID: r.ClientOID, ClientOID: r.ClientOID, Status: "filled", Filled: r.Qty}, nil
	}
	return exchangex.Order{ID: r.ClientOID, ClientOID: r.ClientOID, Status: "open"}, nil
}
func (f *fakeEx) CancelOrder(context.Context, string, string, string) error {
	f.note("cancel")
	return nil
}
func (f *fakeEx) OrderStatus(_ context.Context, _, id, cid string) (exchangex.Order, error) {
	r := f.orders[cid]
	if (f.fillBuys && r.Side == "buy") || (r.IOC && r.ReduceOnly) {
		return exchangex.Order{ID: id, ClientOID: cid, Status: "filled", Filled: r.Qty}, nil
	}
	return exchangex.Order{ID: id, ClientOID: cid, Status: "canceled"}, nil
}
func (f *fakeEx) Rules(context.Context, string) (exchangex.SymbolRules, error) {
	return exchangex.SymbolRules{Tick: 0.01, Step: 0.001, MinQty: 0.001, Base: "BTC"}, nil
}
func (f *fakeEx) SetLeverage(_ context.Context, _ string, lev float64) error {
	f.note("leverage")
	f.leverage = lev
	return nil
}
func (f *fakeEx) Permissions(context.Context) (exchangex.Permissions, error) {
	return f.perms, f.permsErr
}

// setup gives each test its own home, a fixed key, a fake venue and a bot.
func setup(t *testing.T, fx *fakeEx) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CUWAN_HOME", home)
	t.Setenv("CUWAN_AGENT", "1")
	prevKey, prevOpen, prevLoop := masterKey, openDriver, startLoop
	masterKey = func(bool) ([]byte, error) { return make([]byte, 32), nil }
	openDriver = func(exchangex.Config) (exchangex.Exchange, error) { return fx, nil }
	startLoop = func(*env, *store.Bot, exchangex.Exchange, *store.Runtime) int { return 0 }
	t.Cleanup(func() { masterKey, openDriver, startLoop = prevKey, prevOpen, prevLoop })
	e, err := newEnv()
	if err != nil {
		t.Fatal(err)
	}
	if err := e.st.Update(func(s *store.State) error {
		s.Secrets["k"] = "FAKEKEY:FAKESECRET"
		s.Exchanges["fx"] = store.Exchange{Name: "fx", Venue: "binance", Market: "usdm", Secret: "k"}
		s.Bots["g"] = store.Bot{Name: "g", Exchange: "fx", Symbol: "BTCUSDT", Lower: 90, Upper: 110, Levels: 5, OrderSize: 0.01, Leverage: 5, Futures: true}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return home
}

// asPerson makes the yes come from a person at a terminal who types answer.
func asPerson(t *testing.T, answer string) {
	t.Helper()
	prevAgent, prevTTY := isAgent, openTTY
	isAgent = func() bool { return false }
	openTTY = func() (io.ReadWriteCloser, error) { return &fakeTTY{in: strings.NewReader(answer + "\n")}, nil }
	t.Cleanup(func() { isAgent, openTTY = prevAgent, prevTTY })
}

type fakeTTY struct {
	in  io.Reader
	out strings.Builder
}

func (f *fakeTTY) Read(p []byte) (int, error)  { return f.in.Read(p) }
func (f *fakeTTY) Write(p []byte) (int, error) { return f.out.Write(p) }
func (f *fakeTTY) Close() error                { return nil }

// call runs cuwan and decodes the JSON envelope.
func call(t *testing.T, args ...string) (int, out.Envelope) {
	t.Helper()
	return callIn(t, "", args...)
}

func callIn(t *testing.T, in string, args ...string) (int, out.Envelope) {
	t.Helper()
	r, w, _ := os.Pipe()
	ir, iw, _ := os.Pipe()
	oldOut, oldIn := os.Stdout, os.Stdin
	os.Stdout, os.Stdin = w, ir
	go func() { _, _ = iw.WriteString(in); _ = iw.Close() }()
	code := run(args)
	_ = w.Close()
	os.Stdout, os.Stdin = oldOut, oldIn
	b, _ := io.ReadAll(r)
	var e out.Envelope
	if err := json.Unmarshal(b, &e); err != nil {
		t.Fatalf("%v: not a JSON envelope: %q", args, b)
	}
	return code, e
}

func data(e out.Envelope) map[string]any { m, _ := e.Data.(map[string]any); return m }

func TestUnknownCommand(t *testing.T) {
	setup(t, &fakeEx{})
	if code, e := call(t, "nope"); code != 2 || e.OK {
		t.Fatalf("%d %+v", code, e)
	}
	if code, e := call(t, "bot", "nope"); code != 2 || !strings.Contains(e.Error, "unknown") {
		t.Fatalf("%d %+v", code, e)
	}
}

func TestSecretOnlyFromStdinAndNeverShown(t *testing.T) {
	setup(t, &fakeEx{})
	if code, _ := callIn(t, "from-stdin\n", "secret", "set", "x", "the-value"); code == 0 {
		t.Fatal("a secret on the command line was accepted")
	}
	if code, _ := callIn(t, "", "secret", "set", "x"); code == 0 {
		t.Fatal("an empty secret was accepted")
	}
	if code, e := callIn(t, "s3cr3t-value\n", "secret", "set", "x"); code != 0 {
		t.Fatalf("set: %+v", e)
	}
	for _, a := range [][]string{{"secret", "list"}, {"secret", "show", "x"}} {
		code, e := call(t, a...)
		b, _ := json.Marshal(e)
		if code != 0 || strings.Contains(string(b), "s3cr3t") {
			t.Fatalf("%v leaked or failed: %s", a, b)
		}
	}
	if code, _ := call(t, "secret", "rm", "x"); code == 0 {
		t.Fatal("an agent removed a secret without a person's --yes")
	}
}

func TestExchangeAdd(t *testing.T) {
	setup(t, &fakeEx{})
	bad := [][]string{
		{"exchange", "add", "a"},
		{"exchange", "add", "a", "--venue", "okx", "--secret", "k"},
		{"exchange", "add", "a", "--venue", "binance", "--secret", "missing", "--live"},
		{"exchange", "add", "a", "--venue", "binance", "--market", "perp", "--secret", "k", "--live"},
	}
	for _, a := range bad {
		if code, e := call(t, a...); code == 0 {
			t.Errorf("%v accepted: %+v", a, e)
		}
	}
	for _, a := range [][]string{
		{"exchange", "add", "c", "--venue", "binance", "--market", "spot", "--secret", "k"},
		{"exchange", "add", "c", "--venue", "binance", "--secret", "k", "--live", "--testnet"},
	} {
		if code, e := call(t, a...); code == 0 {
			t.Errorf("%v accepted: %+v", a, e)
		}
	}
	if code, e := call(t, "exchange", "add", "b", "--venue", "binance", "--market", "spot", "--secret", "k", "--live"); code != 0 {
		t.Fatalf("%+v", e)
	}
	if code, e := call(t, "exchange", "list"); code != 0 || len(e.Data.([]any)) != 2 {
		t.Fatalf("%+v", e)
	}
}

func TestBotFlags(t *testing.T) {
	setup(t, &fakeEx{})
	base := []string{"bot", "add", "b", "--exchange", "fx", "--symbol", "S", "--lower", "1", "--upper", "5", "--levels", "4", "--order-size", "1", "--leverage", "3"}
	for _, extra := range [][]string{
		{"--on-exit", "hold"}, {"--take-profit", "-1"}, {"--stop-loss", "x"},
		{"--take-profit", "2", "--stop-loss", "3"}, {"--levels", "1"}, {"--upper", "0.5"},
		{"--exchange", "nope"}, {"--order-size", "1", "--quote-amount", "5"},
	} {
		if code, e := call(t, append(append([]string{}, base...), extra...)...); code != 2 {
			t.Errorf("%v accepted: %+v", extra, e)
		}
	}
	code, e := call(t, append(append([]string{}, base...), "--on-exit", "trail", "--take-profit", "6", "--stop-loss", "0.5", "--leverage", "20")...)
	m := data(e)
	if code != 0 || m["on_exit"] != "trail" || m["take_profit"].(float64) != 6 || m["leverage"].(float64) != 20 {
		t.Fatalf("%d %+v", code, e)
	}
	if code, e := call(t, "bot", "edit", "b", "--on-exit", "stop"); code != 0 || data(e)["on_exit"] != "stop" {
		t.Fatalf("%+v", e)
	}
	if code, _ := call(t, "bot", "rm", "b"); code == 0 {
		t.Fatal("an agent removed a bot without --yes")
	}
}

// Old defect: a bot without a size traded 0.001 per order.
func TestBotWithoutSizeIsRefused(t *testing.T) {
	setup(t, &fakeEx{})
	if code, e := call(t, "bot", "add", "b", "--exchange", "fx", "--symbol", "S", "--lower", "1", "--upper", "5", "--levels", "4"); code != 2 || !strings.Contains(e.Error, "size") {
		t.Fatalf("%d %+v", code, e)
	}
}

func TestRunStartAgentNeedsYes(t *testing.T) {
	fx := &fakeEx{perms: exchangex.Permissions{CanTrade: true}}
	setup(t, fx)
	if code, e := call(t, "run", "start", "g"); code == 0 || !strings.Contains(e.Error, "person") || len(fx.calls) != 0 {
		t.Fatalf("%d %+v %v", code, e, fx.calls)
	}
}

func TestRunStartFailsClosedOnPermissions(t *testing.T) {
	for name, fx := range map[string]*fakeEx{
		"check failed": {permsErr: errors.New("probe failed")},
		"can withdraw": {perms: exchangex.Permissions{CanWithdraw: true}},
	} {
		setup(t, fx)
		asPerson(t, "y")
		if code, _ := call(t, "run", "start", "g", "--yes"); code == 0 || len(fx.calls) != 0 {
			t.Errorf("%s: code %d calls %v", name, code, fx.calls)
		}
	}
}

func TestRunStartHyperliquidOnlyOnTestnet(t *testing.T) {
	fx := &fakeEx{perms: exchangex.Permissions{CanTrade: true}}
	setup(t, fx)
	asPerson(t, "y")
	e, _ := newEnv()
	_ = e.st.Update(func(s *store.State) error {
		s.Exchanges["hl"] = store.Exchange{Name: "hl", Venue: "hyperliquid", Market: "perp", Secret: "k"}
		b := s.Bots["g"]
		b.Exchange = "hl"
		s.Bots["g"] = b
		return nil
	})
	if code, e := call(t, "run", "start", "g", "--yes"); code == 0 || !strings.Contains(e.Error, "testnet") || len(fx.calls) != 0 {
		t.Fatalf("%d %+v %v", code, e, fx.calls)
	}
}

func TestRunStartDryRunPlacesNothing(t *testing.T) {
	fx := &fakeEx{}
	setup(t, fx)
	if code, e := call(t, "run", "start", "g", "--dry-run"); code != 0 || len(fx.calls) != 0 || len(data(e)["orders"].([]any)) != 5 {
		t.Fatalf("%d %+v %v", code, e, fx.calls)
	}
}

func TestRunStartLive(t *testing.T) {
	fx := &fakeEx{perms: exchangex.Permissions{CanTrade: true}}
	setup(t, fx)
	asPerson(t, "y")
	if code, e := call(t, "run", "start", "g", "--yes"); code != 0 {
		t.Fatalf("%+v", e)
	}
	if fx.calls[0] != "leverage" || fx.calls[len(fx.calls)-1] != "place" {
		t.Fatalf("calls %v", fx.calls)
	}
	if code, e := call(t, "run", "status", "g"); code != 0 || data(e)["status"] != "running" {
		t.Fatalf("%+v", e)
	}
}

func TestRunStartResumes(t *testing.T) {
	for _, st := range []string{"running", "unguarded"} {
		fx := &fakeEx{perms: exchangex.Permissions{CanTrade: true}}
		setup(t, fx)
		asPerson(t, "y")
		e, _ := newEnv()
		_ = e.st.Update(func(s *store.State) error {
			s.Runtimes["g"] = store.Runtime{Bot: "g", Status: st, Meta: map[string]string{"run_id": "abc"},
				Orders: []store.Order{{ClientOID: "old", Status: "open"}}}
			return nil
		})
		code, env := call(t, "run", "start", "g", "--yes")
		if code != 0 || data(env)["resumed"] != true {
			t.Fatalf("%s: %d %+v", st, code, env)
		}
		for _, c := range fx.calls {
			if c == "place" || c == "leverage" {
				t.Fatalf("%s: resume touched the account: %v", st, fx.calls)
			}
		}
		_ = e.st.View(func(s *store.State) error {
			rt := s.Runtimes["g"]
			if rt.Meta["run_id"] != "abc" || len(rt.Orders) != 1 || rt.Status != "running" {
				t.Fatalf("%s: runtime %+v", st, rt)
			}
			return nil
		})
	}
}

func TestRunStartRefusesHalted(t *testing.T) {
	fx := &fakeEx{perms: exchangex.Permissions{CanTrade: true}}
	setup(t, fx)
	asPerson(t, "y")
	e, _ := newEnv()
	_ = e.st.Update(func(s *store.State) error {
		s.Runtimes["g"] = store.Runtime{Bot: "g", Status: "halted", Orders: []store.Order{{ClientOID: "old", Status: "open"}}}
		return nil
	})
	if code, env := call(t, "run", "start", "g", "--yes"); code == 0 || !strings.Contains(env.Error, "run stop") || len(fx.calls) != 0 {
		t.Fatalf("%d %+v", code, env)
	}
}

func TestRunStopCancels(t *testing.T) {
	fx := &fakeEx{perms: exchangex.Permissions{CanTrade: true}}
	setup(t, fx)
	asPerson(t, "y")
	if code, _ := call(t, "run", "start", "g", "--yes"); code != 0 {
		t.Fatal("start")
	}
	fx.calls = nil
	if code, e := call(t, "run", "stop", "g", "--yes"); code != 0 {
		t.Fatalf("%+v", e)
	}
	if len(fx.calls) != 5 {
		t.Fatalf("cancels %v", fx.calls)
	}
	if _, e := call(t, "run", "status", "g"); data(e)["status"] != "stopped" {
		t.Fatalf("%+v", e)
	}
}

func TestAuditIsWritten(t *testing.T) {
	setup(t, &fakeEx{})
	_, _ = call(t, "bot", "add", "b", "--exchange", "fx", "--symbol", "S", "--lower", "1", "--upper", "5", "--levels", "4", "--order-size", "1", "--leverage", "3")
	code, e := call(t, "audit")
	if code != 0 || !strings.Contains(func() string { b, _ := json.Marshal(e); return string(b) }(), "bot.add") {
		t.Fatalf("%+v", e)
	}
}

func TestReadCommands(t *testing.T) {
	setup(t, &fakeEx{})
	for _, a := range [][]string{{"orders", "fx"}, {"positions", "fx"}, {"balance", "fx"}, {"version"}, {"doctor"}, {"guide"}, {"schema"}} {
		if code, e := call(t, a...); code != 0 || !e.OK {
			t.Errorf("%v: %d %+v", a, code, e)
		}
	}
}

// Audit HIGH 1: a futures exchange requires whole leverage from 1 to 20.
func TestFuturesBotNeedsLeverage(t *testing.T) {
	setup(t, &fakeEx{})
	base := []string{"bot", "add", "b", "--exchange", "fx", "--symbol", "S", "--lower", "1", "--upper", "5", "--levels", "4", "--order-size", "1"}
	for _, extra := range [][]string{{}, {"--leverage", "0"}, {"--leverage", "21"}, {"--leverage", "2.5"}} {
		if code, e := call(t, append(append([]string{}, base...), extra...)...); code != 2 {
			t.Errorf("%v accepted: %+v", extra, e)
		}
	}
	code, e := call(t, append(append([]string{}, base...), "--leverage", "3")...)
	if code != 0 || data(e)["futures"] != true {
		t.Fatalf("%d %+v", code, e)
	}
}

// Audit HIGH 2: NaN and Inf are not numbers here.
func TestNonFiniteFlagsRefused(t *testing.T) {
	setup(t, &fakeEx{})
	base := []string{"bot", "add", "b", "--exchange", "fx", "--symbol", "S", "--lower", "1", "--upper", "5", "--levels", "4"}
	for _, extra := range [][]string{
		{"--order-size", "1", "--leverage", "NaN"},
		{"--order-size", "Inf", "--leverage", "3"},
		{"--order-size", "1", "--leverage", "3", "--stop-loss", "-Inf"},
		{"--order-size", "1", "--leverage", "3", "--take-profit", "nan"},
	} {
		if code, e := call(t, append(append([]string{}, base...), extra...)...); code != 2 {
			t.Errorf("%v accepted: %+v", extra, e)
		}
	}
}

// Audit HIGH 3: an agent never gives the yes, with or without --yes.
func TestAgentYesNeedsPerson(t *testing.T) {
	fx := &fakeEx{perms: exchangex.Permissions{CanTrade: true}}
	setup(t, fx)
	for _, a := range [][]string{
		{"run", "start", "g", "--yes"}, {"run", "stop", "g", "--yes"},
		{"bot", "rm", "g", "--yes"}, {"secret", "rm", "k", "--yes"},
	} {
		code, e := call(t, a...)
		if code == 0 || !strings.Contains(e.Error, "NEEDS_PERSON") || !strings.Contains(e.Error, "cuwan "+a[0]) {
			t.Errorf("%v: %d %+v", a, code, e)
		}
	}
	if len(fx.calls) != 0 {
		t.Fatalf("an agent reached the venue: %v", fx.calls)
	}
}

// Audit HIGH 3: without a terminal there is no yes, even with --yes, and
// stdin is never read as the answer.
func TestNoTerminalNoYes(t *testing.T) {
	fx := &fakeEx{perms: exchangex.Permissions{CanTrade: true}}
	setup(t, fx)
	prevAgent, prevTTY := isAgent, openTTY
	isAgent = func() bool { return false }
	openTTY = func() (io.ReadWriteCloser, error) { return nil, errors.New("no tty") }
	t.Cleanup(func() { isAgent, openTTY = prevAgent, prevTTY })
	if code, _ := callIn(t, "y\n", "run", "start", "g"); code == 0 {
		t.Fatal("a yes on stdin started a live bot")
	}
	if code, _ := call(t, "run", "start", "g", "--yes"); code == 0 || len(fx.calls) != 0 {
		t.Fatalf("--yes without a terminal started a live bot: %v", fx.calls)
	}
}

func TestPersonCanSayNo(t *testing.T) {
	fx := &fakeEx{perms: exchangex.Permissions{CanTrade: true}}
	setup(t, fx)
	asPerson(t, "n")
	if code, _ := call(t, "run", "start", "g"); code == 0 || len(fx.calls) != 0 {
		t.Fatalf("a no started the bot: %v", fx.calls)
	}
}

func TestStartQuestionNamesLeverageAndSize(t *testing.T) {
	fx := &fakeEx{perms: exchangex.Permissions{CanTrade: true}}
	setup(t, fx)
	tty := &fakeTTY{in: strings.NewReader("n\n")}
	prevAgent, prevTTY := isAgent, openTTY
	isAgent = func() bool { return false }
	openTTY = func() (io.ReadWriteCloser, error) { return tty, nil }
	t.Cleanup(func() { isAgent, openTTY = prevAgent, prevTTY })
	_, _ = call(t, "run", "start", "g")
	q := tty.out.String()
	if !strings.Contains(q, "5x") || !strings.Contains(q, "0.01") {
		t.Fatalf("question %q", q)
	}
}

// From the spec branch: run stop --flatten closes the bot's own position.
func TestRunStopFlattenClosesPosition(t *testing.T) {
	fx := &fakeEx{perms: exchangex.Permissions{CanTrade: true}, fillBuys: true}
	setup(t, fx)
	asPerson(t, "y")
	if code, e := call(t, "run", "start", "g"); code != 0 {
		t.Fatalf("%+v", e)
	}
	fx.calls = nil
	if code, e := call(t, "run", "stop", "g", "--flatten"); code != 0 {
		t.Fatalf("%+v", e)
	}
	if fx.closes != 1 {
		t.Fatalf("closes %d, calls %v", fx.closes, fx.calls)
	}
	if _, e := call(t, "run", "status", "g"); data(e)["status"] != "stopped" {
		t.Fatalf("%+v", e)
	}
}

func TestRunStartSetsCappedLeverage(t *testing.T) {
	fx := &fakeEx{perms: exchangex.Permissions{CanTrade: true}}
	setup(t, fx)
	asPerson(t, "y")
	e, _ := newEnv()
	_ = e.st.Update(func(s *store.State) error { b := s.Bots["g"]; b.Leverage = 50; s.Bots["g"] = b; return nil })
	if code, env := call(t, "run", "start", "g"); code != 0 || fx.leverage != 20 {
		t.Fatalf("%d %+v leverage %v", code, env, fx.leverage)
	}
}
