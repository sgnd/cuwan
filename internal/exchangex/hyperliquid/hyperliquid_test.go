package hyperliquid

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTickerAndMarkPrice(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch body["type"] {
		case "allMids":
			_ = json.NewEncoder(w).Encode(map[string]string{"BTC": "65000.25"})
		case "metaAndAssetCtxs":
			_ = json.NewEncoder(w).Encode([]any{
				map[string]any{"universe": []map[string]any{{"name": "ETH"}, {"name": "BTC"}}},
				[]map[string]any{{"markPx": "3000.5"}, {"markPx": "64990.1"}},
			})
		}
	}))
	t.Cleanup(srv.Close)
	d := &Driver{infoURL: srv.URL, exchURL: srv.URL, client: srv.Client()}
	tk, err := d.Ticker(context.Background(), "BTC")
	if err != nil || tk.Price != 65000.25 {
		t.Fatalf("ticker %v %v", tk.Price, err)
	}
	m, err := d.MarkPrice(context.Background(), "BTC")
	if err != nil || m != 64990.1 {
		t.Fatalf("mark %v %v", m, err)
	}
	if _, err := d.MarkPrice(context.Background(), "NOPE"); err == nil {
		t.Fatal("unknown coin has a mark price")
	}
}
