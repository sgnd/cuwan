package publictick

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sgnd/cuwan/internal/exchangex"
)

func TestPaperTickerAndRefusePlace(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"price": "42100.25"})
	}))
	t.Cleanup(srv.Close)

	info := exchangex.VenueInfo{ID: "demo", Kind: exchangex.KindCEX, Markets: []string{exchangex.MarketSpot}, Backend: exchangex.BackendPublic, PublicTicker: true, Live: "paper-only"}
	ex, err := Open(exchangex.Config{Venue: "demo", Paper: true}, info, &Spec{
		URLTemplate: srv.URL + "?symbol={symbol}",
		SymbolFmt:   "upper_concat",
		PricePath:   "price",
	})
	if err != nil {
		t.Fatal(err)
	}
	tk, err := ex.Ticker(context.Background(), "btc/usdt")
	if err != nil {
		t.Fatal(err)
	}
	if tk.Price != 42100.25 {
		t.Fatalf("price %v", tk.Price)
	}
	if _, err := ex.PlaceOrder(context.Background(), exchangex.PlaceReq{}); err == nil {
		t.Fatal("expected paper PlaceOrder error")
	}
}

func TestFormatSymbol(t *testing.T) {
	if got := formatSymbol("BTCUSDT", "dash"); got != "BTC-USDT" {
		t.Fatalf("dash %q", got)
	}
	if got := formatSymbol("BTC/USDT", "underscore"); got != "BTC_USDT" {
		t.Fatalf("underscore %q", got)
	}
}

func TestWalkPathArrayIndex(t *testing.T) {
	raw := []byte(`{"data":[{"last":"99.5"}]}`)
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	got, err := walkPath(v, "data.0.last")
	if err != nil {
		t.Fatal(err)
	}
	f, err := asFloat(got)
	if err != nil || f != 99.5 {
		t.Fatalf("got %v err %v", got, err)
	}
}
