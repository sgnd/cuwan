package binance

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sgnd/cuwan/internal/exchangex"
)

func publicDriver(t *testing.T, market string, h http.HandlerFunc) *Driver {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &Driver{market: market, base: srv.URL, client: srv.Client()}
}

func TestMarkPriceUSDM(t *testing.T) {
	d := publicDriver(t, exchangex.MarketUSDM, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/fapi/v1/premiumIndex" || r.URL.Query().Get("symbol") != "BTCUSDT" {
			http.Error(w, "no", 404)
			return
		}
		_, _ = w.Write([]byte(`{"symbol":"BTCUSDT","markPrice":"64990.10","indexPrice":"65000"}`))
	})
	if px, err := d.MarkPrice(context.Background(), "BTCUSDT"); err != nil || px != 64990.10 {
		t.Fatalf("%v %v", px, err)
	}
}

func TestMarkPriceSpotIsLastPrice(t *testing.T) {
	d := publicDriver(t, exchangex.MarketSpot, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"symbol":"BTCUSDT","price":"65000.25"}`))
	})
	if px, err := d.MarkPrice(context.Background(), "BTCUSDT"); err != nil || px != 65000.25 {
		t.Fatalf("%v %v", px, err)
	}
}

// Old defect: a fill quantity that did not parse became 0, which hid a fill.
func TestBadNumberIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"orderId":1,"clientOrderId":"c","symbol":"BTCUSDT","side":"BUY","price":"1","origQty":"1","executedQty":"oops","status":"FILLED"}`))
	}))
	t.Cleanup(srv.Close)
	d := &Driver{cfg: exchangex.Config{APIKey: "k", APISecret: "s"}, market: exchangex.MarketSpot, base: srv.URL, client: srv.Client()}
	if _, err := d.OrderStatus(context.Background(), "BTCUSDT", "1", ""); err == nil {
		t.Fatal("a bad fill quantity was taken as 0")
	}
}
