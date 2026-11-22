package binance

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sgnd/cuwan/internal/exchangex"
)

// Fake credentials, not a real account.
const (
	fakeKey    = "FAKEKEYabcdef0123456789"
	fakeSecret = "FAKESECRETfedcba9876543210"
)

func leakDriver(url string, c *http.Client) *Driver {
	return &Driver{cfg: exchangex.Config{APIKey: fakeKey, APISecret: fakeSecret, Testnet: true},
		market: exchangex.MarketUSDM, base: url, sapiBase: url, client: c}
}

func checkNoLeak(t *testing.T, what string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: want an error", what)
	}
	for _, s := range []string{fakeKey, fakeSecret, "signature=", "timestamp="} {
		if strings.Contains(err.Error(), s) {
			t.Errorf("%s: error holds %q: %v", what, s, err)
		}
	}
}

// A closed server: Go's error names the whole request URL.
func TestErrorsHoldNoSecretClosedServer(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url, c := srv.URL, srv.Client()
	srv.Close()
	d := leakDriver(url, c)
	ctx := context.Background()
	_, err := d.Balances(ctx)
	checkNoLeak(t, "balances", err)
	_, err = d.PlaceOrder(ctx, exchangex.PlaceReq{Symbol: "BTCUSDT", Side: "buy", Price: 1, Qty: 1, ClientOID: "c"})
	checkNoLeak(t, "place", err)
	_, err = d.Ticker(ctx, "BTCUSDT")
	checkNoLeak(t, "ticker", err)
}

// A server that echoes the request back in its error body.
func TestErrorsHoldNoSecretEchoServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.WriteHeader(400)
		_, _ = io.WriteString(w, `{"code":-2015,"msg":"bad key `+r.Header.Get("X-MBX-APIKEY")+` `+fakeSecret+` `+r.URL.RawQuery+string(b)+`"}`)
	}))
	defer srv.Close()
	d := leakDriver(srv.URL, srv.Client())
	_, err := d.Balances(context.Background())
	checkNoLeak(t, "balances", err)
}
