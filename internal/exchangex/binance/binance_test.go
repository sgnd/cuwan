package binance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sgnd/cuwan/internal/exchangex"
)

func TestPaperTicker(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"symbol": "BTCUSDT", "price": "42000.5"})
	}))
	t.Cleanup(srv.Close)
	d := &Driver{
		cfg:    exchangex.Config{Paper: true},
		market: exchangex.MarketSpot,
		base:   srv.URL,
		client: srv.Client(),
	}
	tk, err := d.Ticker(context.Background(), "BTCUSDT")
	if err != nil {
		t.Fatal(err)
	}
	if tk.Price != 42000.5 {
		t.Fatalf("price %v", tk.Price)
	}
	if _, err := d.PlaceOrder(context.Background(), exchangex.PlaceReq{}); err == nil {
		t.Fatal("paper PlaceOrder should fail")
	}
}
