package binance

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/sgnd/cuwan/internal/exchangex"
)

// TestTestnetRoundTrip runs against the Binance testnet only. It is skipped
// unless CUWAN_BINANCE_TESTNET_KEY and CUWAN_BINANCE_TESTNET_SECRET are set
// (testnet keys only; the driver is forced to testnet, so mainnet cannot be
// reached). Market: CUWAN_BINANCE_TESTNET_MARKET (spot or usdm, default spot).
//
// It places one limit buy at half the market price, so it cannot fill, then
// checks its status and cancels it.
func TestTestnetRoundTrip(t *testing.T) {
	key, secret := os.Getenv("CUWAN_BINANCE_TESTNET_KEY"), os.Getenv("CUWAN_BINANCE_TESTNET_SECRET")
	if key == "" || secret == "" {
		t.Skip("set CUWAN_BINANCE_TESTNET_KEY and CUWAN_BINANCE_TESTNET_SECRET to run against the testnet")
	}
	market := os.Getenv("CUWAN_BINANCE_TESTNET_MARKET")
	if market == "" {
		market = exchangex.MarketSpot
	}
	ex, err := Open(exchangex.Config{Venue: "binance", Market: market, APIKey: key, APISecret: secret, Testnet: true})
	if err != nil {
		t.Fatal(err)
	}
	d := ex.(*Driver)
	if d.base != spotBase(true) && d.base != futuresBase(true) {
		t.Fatalf("refusing: base %s is not a testnet host", d.base)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	const sym = "BTCUSDT"

	rules, err := d.Rules(ctx, sym)
	if err != nil {
		t.Fatal(err)
	}
	tk, err := d.Ticker(ctx, sym)
	if err != nil {
		t.Fatal(err)
	}
	px := tk.Price / 2
	px = float64(int64(px/rules.Tick)) * rules.Tick
	qty := rules.MinQty
	if rules.MinNotional > 0 && px*qty < rules.MinNotional {
		qty = float64(int64(rules.MinNotional*1.1/px/rules.Step)+1) * rules.Step
	}
	cid := "cwtest" + time.Now().UTC().Format("150405")
	o, err := d.PlaceOrder(ctx, exchangex.PlaceReq{Symbol: sym, Side: "buy", Price: px, Qty: qty, ClientOID: cid})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.CancelOrder(context.Background(), sym, o.ID, cid) })
	if o.Status != "open" {
		t.Fatalf("placed status %q", o.Status)
	}
	got, err := d.OrderStatus(ctx, sym, "", cid)
	if err != nil || got.Status != "open" || got.ID != o.ID {
		t.Fatalf("status by client ID: %+v %v", got, err)
	}
	if err := d.CancelOrder(ctx, sym, o.ID, cid); err != nil {
		t.Fatal(err)
	}
	got, err = d.OrderStatus(ctx, sym, o.ID, "")
	if err != nil || got.Status != "canceled" {
		t.Fatalf("after cancel: %+v %v", got, err)
	}
	if _, err := d.OrderStatus(ctx, sym, "", "cwtest-never-sent"); !errors.Is(err, exchangex.ErrOrderNotFound) {
		t.Fatalf("missing order: %v", err)
	}
}
