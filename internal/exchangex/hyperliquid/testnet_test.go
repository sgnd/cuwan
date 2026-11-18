package hyperliquid

import (
	"context"
	"errors"
	"math"
	"os"
	"testing"
	"time"

	"github.com/sgnd/cuwan/internal/exchangex"
)

// TestTestnetRoundTrip runs against the Hyperliquid testnet only. It is
// skipped unless CUWAN_HL_TESTNET_ACCOUNT (the account address) and
// CUWAN_HL_TESTNET_KEY (an API wallet key of that account) are set. The
// driver is forced to testnet hosts, so mainnet cannot be reached.
//
// It checks the key is an API wallet, places one limit buy at half the mid
// price (so it cannot fill), checks it by client ID, cancels it, and checks
// a missing order.
func TestTestnetRoundTrip(t *testing.T) {
	account, key := os.Getenv("CUWAN_HL_TESTNET_ACCOUNT"), os.Getenv("CUWAN_HL_TESTNET_KEY")
	if account == "" || key == "" {
		t.Skip("set CUWAN_HL_TESTNET_ACCOUNT and CUWAN_HL_TESTNET_KEY to run against the testnet")
	}
	ex, err := Open(exchangex.Config{Venue: "hyperliquid", Market: exchangex.MarketPerp, APIKey: account, APISecret: key, Testnet: true})
	if err != nil {
		t.Fatal(err)
	}
	d := ex.(*Driver)
	if d.infoURL != testnetInfo || d.exchURL != testnetExchange {
		t.Fatalf("refusing: %s / %s are not testnet hosts", d.infoURL, d.exchURL)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	const coin = "BTC"

	p, err := d.Permissions(ctx)
	if err != nil || p.CanWithdraw {
		t.Fatalf("the key must be an API wallet of the account: %+v %v", p, err)
	}
	rules, err := d.Rules(ctx, coin)
	if err != nil {
		t.Fatal(err)
	}
	tk, err := d.Ticker(ctx, coin)
	if err != nil {
		t.Fatal(err)
	}
	px := tk.Price / 2
	tick := math.Pow(10, math.Floor(math.Log10(px))-float64(rules.SigFigs-1))
	px = math.Floor(px/tick) * tick
	qty := math.Ceil(rules.MinNotional*1.2/px/rules.Step) * rules.Step
	cid := "cwtest" + time.Now().UTC().Format("150405.000")
	o, err := d.PlaceOrder(ctx, exchangex.PlaceReq{Symbol: coin, Side: "buy", Price: px, Qty: qty, ClientOID: cid})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.CancelOrder(context.Background(), coin, "", cid) })
	if o.Status != "open" {
		t.Fatalf("placed status %q", o.Status)
	}
	got, err := d.OrderStatus(ctx, coin, "", cid)
	if err != nil || got.Status != "open" || got.ID != o.ID {
		t.Fatalf("status by client ID: %+v %v", got, err)
	}
	if err := d.CancelOrder(ctx, coin, "", cid); err != nil {
		t.Fatal(err)
	}
	got, err = d.OrderStatus(ctx, coin, o.ID, "")
	if err != nil || got.Status != "canceled" {
		t.Fatalf("after cancel: %+v %v", got, err)
	}
	if _, err := d.OrderStatus(ctx, coin, "", "cwtest-never-sent"); !errors.Is(err, exchangex.ErrOrderNotFound) {
		t.Fatalf("missing order: %v", err)
	}
}
