package paper

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/sgnd/cuwan/internal/exchangex"
	"github.com/sgnd/cuwan/internal/store"
)

// tickEx is a public-data fake: it fails the test on any signed call.
type tickEx struct {
	t     *testing.T
	price float64
}

func (f *tickEx) Name() string   { return "fake" }
func (f *tickEx) Market() string { return "spot" }
func (f *tickEx) Ticker(context.Context, string) (exchangex.Ticker, error) {
	return exchangex.Ticker{Price: f.price}, nil
}
func (f *tickEx) Balances(context.Context) ([]exchangex.Balance, error) {
	f.t.Fatal("paper read balances")
	return nil, nil
}
func (f *tickEx) Positions(context.Context) ([]exchangex.Position, error) {
	f.t.Fatal("paper read positions")
	return nil, nil
}
func (f *tickEx) OpenOrders(context.Context, string) ([]exchangex.Order, error) {
	f.t.Fatal("paper read orders")
	return nil, nil
}
func (f *tickEx) PlaceOrder(context.Context, exchangex.PlaceReq) (exchangex.Order, error) {
	f.t.Fatal("paper sent a signed order")
	return exchangex.Order{}, nil
}
func (f *tickEx) CancelOrder(context.Context, string, string, string) error {
	f.t.Fatal("paper sent a signed cancel")
	return nil
}
func (f *tickEx) SetLeverage(context.Context, string, float64) error {
	f.t.Fatal("paper set leverage")
	return nil
}
func (f *tickEx) Permissions(context.Context) (exchangex.Permissions, error) {
	return exchangex.Permissions{}, nil
}

func testHome(t *testing.T) {
	t.Helper()
	t.Setenv("CUWAN_HOME", filepath.Join(t.TempDir(), "home"))
	t.Setenv("CUWAN_KEYCHAIN", "off")
	store.ResetForTest()
	prev := store.KeySource
	store.KeySource = func(map[string]string, bool) ([]byte, map[string]string, error) {
		k := make([]byte, 32)
		for i := range k {
			k[i] = byte(i + 7)
		}
		return k, map[string]string{"key_mode": "test"}, nil
	}
	t.Cleanup(func() { store.ResetForTest(); store.KeySource = prev })
}

func count(rt *store.RuntimeState, side, status string) int {
	n := 0
	for _, o := range rt.Orders {
		if o.Side == side && o.Status == status {
			n++
		}
	}
	return n
}

func TestGridFillPlacesOppositeOneLevelAway(t *testing.T) {
	testHome(t)
	ex := &tickEx{t: t, price: 100}
	bot := &store.Bot{Name: "p", Symbol: "S", Lower: 90, Upper: 110, Levels: 5, OrderSize: 1}
	s, err := Start(context.Background(), bot, ex)
	if err != nil {
		t.Fatal(err)
	}
	defer Stop("p")
	// levels 90 95 100 105 110 at mid 100
	start := len(s.Runtime.Orders)
	ex.price = 94
	s.step(context.Background())
	rt, _ := Status("p")
	if count(rt, "buy", "filled") != 1 {
		t.Fatalf("want the 95 buy filled: %+v", rt.Orders)
	}
	last := rt.Orders[len(rt.Orders)-1]
	if len(rt.Orders) != start+1 || last.Side != "sell" || last.Price != 100 || last.Status != "open" {
		t.Fatalf("opposite order: %+v", last)
	}
	// Same price again must not churn.
	s.step(context.Background())
	if rt2, _ := Status("p"); len(rt2.Orders) != len(rt.Orders) {
		t.Fatalf("refilled at the same price: %d -> %d", len(rt.Orders), len(rt2.Orders))
	}
	ex.price = 100
	s.step(context.Background())
	rt, _ = Status("p")
	if last := rt.Orders[len(rt.Orders)-1]; last.Side != "buy" || last.Price != 95 {
		t.Fatalf("round trip: %+v", last)
	}
	seen := map[string]bool{}
	for _, o := range rt.Orders {
		if seen[o.ClientOID] {
			t.Fatalf("duplicate client order id %s", o.ClientOID)
		}
		seen[o.ClientOID] = true
	}
}

func TestPreventiveCloseStopsSession(t *testing.T) {
	testHome(t)
	ex := &tickEx{t: t, price: 100}
	bot := &store.Bot{Name: "f", Symbol: "S", Lower: 90, Upper: 110, Levels: 4, OrderSize: 1, Leverage: 20}
	s, err := Start(context.Background(), bot, ex)
	if err != nil {
		t.Fatal(err)
	}
	ex.price = 95.1 // liq at 20x long from 100 is 95; fires past 95% of the path
	s.step(context.Background())
	s.Wait()
	rt, _ := store.GetRuntime("f")
	if rt.Status != "stopped" || rt.Meta["preventive"] != "fired" || count(rt, "buy", "open")+count(rt, "sell", "open") != 0 {
		t.Fatalf("preventive close: status=%s meta=%v", rt.Status, rt.Meta)
	}
	_ = Stop("f")
}

func TestStopAndResume(t *testing.T) {
	testHome(t)
	ex := &tickEx{t: t, price: 100}
	bot := &store.Bot{Name: "r", Symbol: "S", Lower: 90, Upper: 110, Levels: 3, OrderSize: 1}
	if _, err := Start(context.Background(), bot, ex); err != nil {
		t.Fatal(err)
	}
	if _, err := Start(context.Background(), bot, ex); err == nil {
		t.Fatal("second start of a running bot accepted")
	}
	if err := Stop("r"); err != nil {
		t.Fatal(err)
	}
	if rt, _ := Status("r"); rt.Status != "stopped" {
		t.Fatalf("status %s", rt.Status)
	}
	if err := Stop("nobody"); err == nil {
		t.Fatal("stop of unknown bot accepted")
	}
}
