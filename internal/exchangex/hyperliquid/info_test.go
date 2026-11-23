package hyperliquid

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sgnd/cuwan/internal/exchangex"
)

// info serves /info by request type and records each request body.
func info(t *testing.T, replies map[string]any) (*Driver, *[]map[string]any) {
	t.Helper()
	var seen []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		seen = append(seen, body)
		_ = json.NewEncoder(w).Encode(replies[body["type"].(string)])
	}))
	t.Cleanup(srv.Close)
	return &Driver{cfg: exchangex.Config{APIKey: "0xabc", APISecret: "k", Testnet: true}, infoURL: srv.URL, exchURL: srv.URL, client: srv.Client()}, &seen
}

func TestOrderStatus(t *testing.T) {
	d, seen := info(t, map[string]any{"orderStatus": map[string]any{
		"status": "order",
		"order": map[string]any{
			"order":  map[string]any{"coin": "BTC", "side": "A", "limitPx": "65000", "sz": "0.4", "origSz": "1", "oid": 7},
			"status": "canceled",
		},
	}})
	o, err := d.OrderStatus(context.Background(), "BTC", "", "cw123")
	if err != nil || o.Status != "canceled" || o.Side != "sell" || o.Filled != 0.6 || o.ID != "7" {
		t.Fatalf("%+v %v", o, err)
	}
	if got := (*seen)[0]["oid"]; got != Cloid("cw123") || len(Cloid("cw123")) != 34 {
		t.Fatalf("lookup by cloid sent %v", got)
	}
	if _, err := d.OrderStatus(context.Background(), "BTC", "7", ""); err != nil || (*seen)[1]["oid"] != float64(7) {
		t.Fatalf("lookup by oid sent %v (%v)", (*seen)[1]["oid"], err)
	}
}

func TestOrderStatusUnknown(t *testing.T) {
	d, _ := info(t, map[string]any{"orderStatus": map[string]any{"status": "unknownOid"}})
	if _, err := d.OrderStatus(context.Background(), "BTC", "", "cw1"); !errors.Is(err, exchangex.ErrOrderNotFound) {
		t.Fatalf("err %v", err)
	}
}

func TestNormStatus(t *testing.T) {
	for in, want := range map[string]string{"open": "open", "triggered": "open", "filled": "filled", "canceled": "canceled",
		"marginCanceled": "canceled", "rejected": "canceled", "minTradeNtlRejected": "canceled", "scheduledCancel": "unknown"} {
		if got := normStatus(in); got != want {
			t.Errorf("%s -> %s want %s", in, got, want)
		}
	}
}

func TestRulesFromMeta(t *testing.T) {
	d, _ := info(t, map[string]any{
		"meta": map[string]any{"universe": []map[string]any{{"name": "BTC", "szDecimals": 5}, {"name": "DOGE", "szDecimals": 0}}},
	})
	r, err := d.Rules(context.Background(), "BTC")
	if err != nil || r.Step != 1e-5 || r.Tick != 0.1 || r.SigFigs != 5 || r.MinNotional != 10 {
		t.Fatalf("BTC %+v %v", r, err)
	}
	r, err = d.Rules(context.Background(), "DOGE")
	if err != nil || r.Step != 1 || r.Tick != 1e-6 {
		t.Fatalf("DOGE %+v %v", r, err)
	}
	if _, err := d.Rules(context.Background(), "NOPE"); err == nil {
		t.Fatal("unknown coin accepted")
	}
}

// A secret that is not a valid key, or no key at all, fails closed.
func TestPermissionsFailClosed(t *testing.T) {
	d, _ := info(t, nil)
	if _, err := d.Permissions(context.Background()); err == nil {
		t.Fatal("wallet key reported as safe")
	}
	d.cfg.APISecret = ""
	if _, err := d.Permissions(context.Background()); err == nil {
		t.Fatal("no key reported as safe")
	}
}
