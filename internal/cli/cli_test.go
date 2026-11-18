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

	"github.com/sgnd/cuwan/internal/exchangex"
	"github.com/sgnd/cuwan/internal/out"
	"github.com/sgnd/cuwan/internal/store"
)

// fakeEx records every call so tests can prove nothing touched the account.
type fakeEx struct {
	perms    exchangex.Permissions
	permsErr error
	calls    []string
}

func (f *fakeEx) Name() string   { return "fake" }
func (f *fakeEx) Market() string { return "usdm" }
func (f *fakeEx) Ticker(context.Context, string) (exchangex.Ticker, error) {
	return exchangex.Ticker{Price: 100}, nil
}
func (f *fakeEx) Balances(context.Context) ([]exchangex.Balance, error)   { return nil, nil }
func (f *fakeEx) Positions(context.Context) ([]exchangex.Position, error) { return nil, nil }
func (f *fakeEx) OpenOrders(context.Context, string) ([]exchangex.Order, error) {
	return nil, nil
}
func (f *fakeEx) PlaceOrder(_ context.Context, r exchangex.PlaceReq) (exchangex.Order, error) {
	f.calls = append(f.calls, "place")
	return exchangex.Order{ID: r.ClientOID, Status: "open"}, nil
}
func (f *fakeEx) CancelOrder(context.Context, string, string, string) error {
	f.calls = append(f.calls, "cancel")
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
	store.KeySource = func(map[string]string, bool) ([]byte, map[string]string, error) {
		k := make([]byte, 32)
		for i := range k {
			k[i] = byte(i + 1)
		}
		return k, map[string]string{"key_mode": "test"}, nil
	}
	prev := openExchange
	openExchange = func(name string, _ bool) (*store.Exchange, exchangex.Exchange, error) {
		e, err := store.GetExchange(name)
		return e, fx, err
	}
	t.Cleanup(func() {
		openExchange = prev
		store.ResetForTest()
		store.KeySource = nil
	})
	if err := store.PutExchange(&store.Exchange{Name: "fx", Venue: "binance", Market: "usdm", SecretRef: "secret:x"}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutBot(&store.Bot{Name: "g", Exchange: "fx", Symbol: "BTCUSDT", Lower: 90, Upper: 110, Levels: 4, OrderSize: 0.01, Leverage: 50}); err != nil {
		t.Fatal(err)
	}
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
	code, env := call(t, "run", "start", "g")
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
	code, env := call(t, "run", "start", "g", "--yes")
	if code == 0 || !strings.Contains(env.Error, "cannot check") || len(fx.calls) != 0 {
		t.Fatalf("code=%d env=%+v calls=%v", code, env, fx.calls)
	}
}

func TestRunStartWithdrawKeyRefused(t *testing.T) {
	fx := &fakeEx{perms: exchangex.Permissions{CanWithdraw: true}}
	setup(t, fx)
	code, _ := call(t, "run", "start", "g", "--yes")
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
	code, env := call(t, "run", "start", "g", "--yes")
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
