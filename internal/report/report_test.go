package report

import (
	"math"
	"testing"

	"github.com/sgnd/cuwan/internal/store"
)

func rt(last string, orders ...store.RuntimeOrder) *store.RuntimeState {
	return &store.RuntimeState{BotName: "b", Mode: "paper", Status: "running",
		Meta: map[string]string{"last_price": last}, Orders: orders}
}

func fill(side string, price, qty, filled float64) store.RuntimeOrder {
	return store.RuntimeOrder{Side: side, Price: price, Qty: qty, Filled: filled, Status: "filled"}
}

func near(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}

func TestFIFORealised(t *testing.T) {
	r := Compute(rt("0", fill("buy", 100, 1, 1), fill("buy", 110, 1, 1), fill("sell", 120, 1, 1)), 0)
	near(t, "realised", r.Realised, 20) // closes the 100 lot first
	near(t, "net size", r.NetSize, 1)
	if r.RoundTrips != 1 || r.Fills != 3 {
		t.Fatalf("round trips %d fills %d", r.RoundTrips, r.Fills)
	}
}

func TestPartialFills(t *testing.T) {
	open := store.RuntimeOrder{Side: "sell", Price: 130, Qty: 1, Status: "open"}
	r := Compute(rt("105", fill("buy", 100, 1, 0.4), fill("sell", 110, 1, 0.1), open), 0)
	near(t, "realised", r.Realised, 1)
	near(t, "net size", r.NetSize, 0.3)
	near(t, "unrealised", r.Unrealised, 1.5)
	if r.Fills != 2 {
		t.Fatalf("fills %d", r.Fills)
	}
}

func TestFlatteningClose(t *testing.T) {
	flat := fill("sell", 90, 2, 2)
	flat.ReduceOnly = true
	r := Compute(rt("95", fill("buy", 100, 1, 1), fill("buy", 96, 1, 1), flat), 0)
	near(t, "realised", r.Realised, -16)
	near(t, "net size", r.NetSize, 0)
	near(t, "unrealised", r.Unrealised, 0)
}

func TestShortThenCover(t *testing.T) {
	r := Compute(rt("0", fill("sell", 100, 1, 1), fill("buy", 90, 1, 1)), 0)
	near(t, "realised", r.Realised, 10)
}

func TestFees(t *testing.T) {
	r := Compute(rt("100", fill("buy", 100, 1, 1), fill("sell", 110, 1, 1)), 0.001)
	near(t, "fees", r.Fees, 0.21)
	near(t, "net", r.Net, 10-0.21)
	if !r.Estimate {
		t.Fatal("fees must be marked as an estimate")
	}
}

func TestEmptyRuntime(t *testing.T) {
	r := Compute(&store.RuntimeState{BotName: "b"}, 0.001)
	if r.Fills != 0 || r.Realised != 0 || r.Unrealised != 0 || r.Fees != 0 || r.NetSize != 0 {
		t.Fatalf("empty runtime gave %+v", r)
	}
}
