package hyperliquid

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sgnd/cuwan/internal/exchangex"
)

func TestPaperTickerAndRefusesSigned(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"BTC": "65000.25"})
	}))
	t.Cleanup(srv.Close)
	d := &Driver{
		cfg:     exchangex.Config{Paper: true},
		infoURL: srv.URL,
		exchURL: srv.URL,
		client:  srv.Client(),
	}
	tk, err := d.Ticker(context.Background(), "BTC")
	if err != nil {
		t.Fatal(err)
	}
	if tk.Price != 65000.25 {
		t.Fatalf("price %v", tk.Price)
	}
	if _, err := d.PlaceOrder(context.Background(), exchangex.PlaceReq{}); err == nil {
		t.Fatal("paper PlaceOrder should fail")
	}
	if err := d.CancelOrder(context.Background(), "BTC", "1", ""); err == nil {
		t.Fatal("paper CancelOrder should fail")
	}
}
