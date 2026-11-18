package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sgnd/cuwan/internal/exchangex"
	"github.com/sgnd/cuwan/internal/live"
	"github.com/sgnd/cuwan/internal/out"
	"github.com/sgnd/cuwan/internal/store"
)

// fakeEx records every call so tests can prove nothing touched the account.
type fakeEx struct {
	perms     exchangex.Permissions
	permsErr  error
	tickerErr error
	calls     []string
	placed    map[string]exchangex.Order
}

func (f *fakeEx) OrderStatus(_ context.Context, _, id, cid string) (exchangex.Order, error) {
	for _, o := range f.placed {
		if o.ClientOID == cid || (id != "" && o.ID == id) {
			return o, nil
		}
	}
	return exchangex.Order{}, exchangex.ErrOrderNotFound
}

// bare hides OrderStatus.
type bare struct{ exchangex.Exchange }

func (f *fakeEx) Name() string   { return "fake" }
func (f *fakeEx) Market() string { return "usdm" }
func (f *fakeEx) Ticker(context.Context, string) (exchangex.Ticker, error) {
	return exchangex.Ticker{Price: 100}, f.tickerErr
}
func (f *fakeEx) Balances(context.Context) ([]exchangex.Balance, error)   { return nil, nil }
func (f *fakeEx) Positions(context.Context) ([]exchangex.Position, error) { return nil, nil }
func (f *fakeEx) OpenOrders(context.Context, string) ([]exchangex.Order, error) {
	return nil, nil
}
func (f *fakeEx) PlaceOrder(_ context.Context, r exchangex.PlaceReq) (exchangex.Order, error) {
	f.calls = append(f.calls, "place")
	if f.placed == nil {
		f.placed = map[string]exchangex.Order{}
	}
	o := exchangex.Order{ID: r.ClientOID, ClientOID: r.ClientOID, Side: r.Side, Price: r.Price, Qty: r.Qty, Status: "open"}
	f.placed[r.ClientOID] = o
	return o, nil
}
func (f *fakeEx) CancelOrder(_ context.Context, _, _, cid string) error {
	f.calls = append(f.calls, "cancel")
	if o, ok := f.placed[cid]; ok {
		o.Status = "canceled"
		f.placed[cid] = o
	}
	return nil
}
func (f *fakeEx) SetLeverage(context.Context, string, float64) error {
	f.calls = append(f.calls, "leverage")
	return nil
}
func (f *fakeEx) Permissions(context.Context) (exchangex.Permissions, error) {
	return f.perms, f.permsErr
}

func setup(t *testing.T, fx *fakeEx) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CUWAN_HOME", filepath.Join(dir, "home"))
	t.Setenv("CUWAN_KEYCHAIN", "off")
	t.Setenv("CUWAN_AGENT", "1")
	store.ResetForTest()
	prevKS := store.KeySource
	store.KeySource = func(map[string]string, bool) ([]byte, map[string]string, error) {
		k := make([]byte, 32)
		for i := range k {
			k[i] = byte(i + 1)
		}
		return k, map[string]string{"key_mode": "test"}, nil
	}
	prevLoop := startLoop
	startLoop = func(*store.Bot, exchangex.Exchange, *store.RuntimeState) int { return 0 }
	prev := openExchange
	openExchange = func(name string, _ bool) (*store.Exchange, exchangex.Exchange, error) {
		e, err := store.GetExchange(name)
		return e, fx, err
	}
	t.Cleanup(func() {
		openExchange = prev
		startLoop = prevLoop
		store.ResetForTest()
		store.KeySource = prevKS
	})
	if err := store.PutExchange(&store.Exchange{Name: "fx", Venue: "binance", Market: "usdm", SecretRef: "secret:x"}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutBot(&store.Bot{Name: "g", Exchange: "fx", Symbol: "BTCUSDT", Lower: 90, Upper: 110, Levels: 4, OrderSize: 0.01, Leverage: 50}); err != nil {
		t.Fatal(err)
	}
}

// callIn is call with stdin set to in.
func callIn(t *testing.T, in string, args ...string) (int, out.Envelope) {
	t.Helper()
	r, w, _ := os.Pipe()
	old := os.Stdin
	os.Stdin = r
	go func() { w.WriteString(in); w.Close() }()
	defer func() { os.Stdin = old }()
	return call(t, args...)
}

// call runs cuwan with args and decodes the JSON envelope from stdout.
func call(t *testing.T, args ...string) (int, out.Envelope) {
	t.Helper()
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	code := run(args)
	w.Close()
	os.Stdout = old
	b, _ := io.ReadAll(r)
	var env out.Envelope
	if err := json.Unmarshal(b, &env); err != nil {
		t.Fatalf("not a JSON envelope: %q", b)
	}
	return code, env
}

func TestUnknownCommand(t *testing.T) {
	t.Setenv("CUWAN_AGENT", "1")
	code, env := call(t, "nope")
	if code != 2 || env.OK || len(env.Hints) == 0 {
		t.Fatalf("code=%d env=%+v", code, env)
	}
}

func TestRunStartAgentNeedsYes(t *testing.T) {
	fx := &fakeEx{}
	setup(t, fx)
	code, env := call(t, "run", "start", "g", "--foreground")
	if code == 0 || !strings.Contains(env.Error, "person confirm") {
		t.Fatalf("code=%d env=%+v", code, env)
	}
	if len(fx.calls) != 0 {
		t.Fatalf("account touched before confirm: %v", fx.calls)
	}
}

func TestRunStartPermissionsErrorFailsClosed(t *testing.T) {
	fx := &fakeEx{permsErr: errors.New("probe failed")}
	setup(t, fx)
	code, env := call(t, "run", "start", "g", "--yes", "--foreground")
	if code == 0 || !strings.Contains(env.Error, "cannot check") || len(fx.calls) != 0 {
		t.Fatalf("code=%d env=%+v calls=%v", code, env, fx.calls)
	}
}

func TestRunStartWithdrawKeyRefused(t *testing.T) {
	fx := &fakeEx{perms: exchangex.Permissions{CanWithdraw: true}}
	setup(t, fx)
	code, _ := call(t, "run", "start", "g", "--yes", "--foreground")
	if code == 0 || len(fx.calls) != 0 {
		t.Fatalf("code=%d calls=%v", code, fx.calls)
	}
}

func TestRunStartDryRunPlacesNothing(t *testing.T) {
	fx := &fakeEx{}
	setup(t, fx)
	code, env := call(t, "run", "start", "g", "--dry-run")
	if code != 0 || !env.OK || len(fx.calls) != 0 {
		t.Fatalf("code=%d env=%+v calls=%v", code, env, fx.calls)
	}
}

func TestRunStartLiveCapsLeverageAfterConfirm(t *testing.T) {
	fx := &fakeEx{perms: exchangex.Permissions{CanTrade: true}}
	setup(t, fx)
	code, env := call(t, "run", "start", "g", "--yes", "--foreground")
	if code != 0 || !env.OK {
		t.Fatalf("code=%d env=%+v", code, env)
	}
	if fx.calls[0] != "leverage" || fx.calls[len(fx.calls)-1] != "place" {
		t.Fatalf("calls=%v", fx.calls)
	}
	code, _ = call(t, "run", "stop", "g")
	if code == 0 {
		t.Fatal("agent stop without --yes must fail")
	}
	code, _ = call(t, "run", "stop", "g", "--yes")
	if code != 0 || fx.calls[len(fx.calls)-1] != "cancel" {
		t.Fatalf("code=%d calls=%v", code, fx.calls)
	}
}

func TestPositional(t *testing.T) {
	got := positional([]string{"a", "--x", "1", "--y=2", "b", "--yes"})
	if strings.Join(got, ",") != "a,b" {
		t.Fatalf("%v", got)
	}
}

func TestRunStopFlattenClosesPosition(t *testing.T) {
	fx := &fakeEx{perms: exchangex.Permissions{CanTrade: true}}
	setup(t, fx)
	if code, env := call(t, "run", "start", "g", "--yes", "--foreground"); code != 0 {
		t.Fatalf("%+v", env)
	}
	fx.calls = nil
	if code, env := call(t, "run", "stop", "g", "--yes", "--flatten"); code != 0 {
		t.Fatalf("%+v", env)
	}
	cancels := 0
	for _, c := range fx.calls {
		if c == "cancel" {
			cancels++
		}
	}
	if cancels != 4 {
		t.Fatalf("each order cancelled once, got %v", fx.calls)
	}
	rt, _ := store.GetRuntime("g")
	if rt.Status != "stopped" {
		t.Fatalf("status %s", rt.Status)
	}
}

func TestRunLoopEndsOnStopRequest(t *testing.T) {
	fx := &fakeEx{perms: exchangex.Permissions{CanTrade: true}}
	setup(t, fx)
	runEvery = time.Millisecond
	t.Cleanup(func() { runEvery = 5 * time.Second })
	bot, _ := store.GetBot("g")
	if err := live.RequestStop("g"); err != nil {
		t.Fatal(err)
	}
	rt := &store.RuntimeState{BotName: "g", Mode: "live", Status: "running"}
	if code := runLoop(bot, fx, rt); code != 0 {
		t.Fatalf("code %d", code)
	}
}

// Review finding 3: failed steps in a row halt the bot and cancel orders.
func TestRunLoopHaltsAfterErrorsInARow(t *testing.T) {
	fx := &fakeEx{perms: exchangex.Permissions{CanTrade: true}}
	setup(t, fx)
	runEvery = time.Millisecond
	t.Cleanup(func() { runEvery = 5 * time.Second })
	if code, env := call(t, "run", "start", "g", "--yes", "--foreground"); code != 0 {
		t.Fatalf("%+v", env)
	}
	bot, _ := store.GetBot("g")
	rt, _ := store.GetRuntime("g")
	fx.tickerErr = errors.New("venue down")
	fx.calls = nil
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	code := runLoop(bot, fx, rt)
	w.Close()
	os.Stdout = old
	_, _ = io.ReadAll(r)
	if code == 0 {
		t.Fatal("loop kept going")
	}
	got, _ := store.GetRuntime("g")
	if got.Status != "halted" || len(fx.calls) != 4 {
		t.Fatalf("status %s calls %v", got.Status, fx.calls)
	}
}

// Review finding 4: live start refuses a driver that cannot confirm fills.
func TestRunStartRefusesDriverWithoutOrderStatus(t *testing.T) {
	fx := &fakeEx{perms: exchangex.Permissions{CanTrade: true}}
	setup(t, fx)
	openExchange = func(name string, _ bool) (*store.Exchange, exchangex.Exchange, error) {
		e, err := store.GetExchange(name)
		return e, bare{fx}, err
	}
	code, env := call(t, "run", "start", "g", "--yes", "--foreground")
	if code == 0 || !strings.Contains(env.Error, "OrderStatus") || len(fx.calls) != 0 {
		t.Fatalf("%d %+v %v", code, env, fx.calls)
	}
}

// Review finding 1: run stop waits for a running loop to let go.
func TestRunStopTakesOverFromLoop(t *testing.T) {
	fx := &fakeEx{perms: exchangex.Permissions{CanTrade: true}}
	setup(t, fx)
	if code, env := call(t, "run", "start", "g", "--yes", "--foreground"); code != 0 {
		t.Fatalf("%+v", env)
	}
	held, err := live.TryLock("g")
	if err != nil {
		t.Fatal(err)
	}
	stopWait = 300 * time.Millisecond
	t.Cleanup(func() { stopWait = 60 * time.Second })
	code, env := call(t, "run", "stop", "g", "--yes")
	if code == 0 || !strings.Contains(env.Error, "did not stop") || !live.StopRequested("g") {
		t.Fatalf("stop ran under a held lock: %d %+v", code, env)
	}
	held.Unlock()
	if code, env := call(t, "run", "stop", "g", "--yes"); code != 0 {
		t.Fatalf("%+v", env)
	}
	if live.StopRequested("g") {
		t.Fatal("stop request left behind")
	}
}

// Review N1: live start without the loop is refused.
func TestRunStartNeedsForeground(t *testing.T) {
	fx := &fakeEx{perms: exchangex.Permissions{CanTrade: true}}
	setup(t, fx)
	code, env := call(t, "run", "start", "g", "--yes")
	if code == 0 || !strings.Contains(env.Error, "--foreground") || len(fx.calls) != 0 {
		t.Fatalf("%d %+v %v", code, env, fx.calls)
	}
}

// Review N4: start never drops a halted or unguarded runtime.
func TestRunStartRefusesOverHaltedRuntime(t *testing.T) {
	for _, st := range []string{"halted", "unguarded", "running"} {
		fx := &fakeEx{perms: exchangex.Permissions{CanTrade: true}}
		setup(t, fx)
		if err := store.PutRuntime(&store.RuntimeState{BotName: "g", Mode: "live", Status: st,
			Orders: []store.RuntimeOrder{{ClientOID: "old", Status: "open"}}}); err != nil {
			t.Fatal(err)
		}
		code, env := call(t, "run", "start", "g", "--yes", "--foreground")
		if code == 0 || !strings.Contains(env.Error, "run stop") || len(fx.calls) != 0 {
			t.Errorf("%s: %d %+v", st, code, env)
		}
		rt, _ := store.GetRuntime("g")
		if len(rt.Orders) != 1 || rt.Orders[0].ClientOID != "old" {
			t.Errorf("%s: old runtime dropped", st)
		}
	}
}
