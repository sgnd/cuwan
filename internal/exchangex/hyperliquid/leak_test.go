package hyperliquid

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sgnd/cuwan/internal/exchangex"
)

func leakDriver(url string, c *http.Client) *Driver {
	return &Driver{cfg: exchangex.Config{APIKey: account, APISecret: testKey, Testnet: true},
		infoURL: url + "/info", exchURL: url + "/exchange", client: c}
}

func checkNoLeak(t *testing.T, what string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: want an error", what)
	}
	for _, s := range []string{testKey, strings.TrimPrefix(testKey, "0x")} {
		if strings.Contains(err.Error(), s) {
			t.Errorf("%s: error holds the secret: %v", what, err)
		}
	}
}

func TestErrorsHoldNoSecretClosedServer(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url, c := srv.URL, srv.Client()
	srv.Close()
	d := leakDriver(url, c)
	ctx := context.Background()
	_, err := d.PlaceOrder(ctx, exchangex.PlaceReq{Symbol: "BTC", Side: "buy", Price: 1, Qty: 1, ClientOID: "c"})
	checkNoLeak(t, "place", err)
	checkNoLeak(t, "cancel", d.CancelOrder(ctx, "BTC", "1", ""))
}

func TestErrorsHoldNoSecretEchoServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.WriteHeader(400)
		_, _ = io.WriteString(w, "bad "+testKey+" "+string(b))
	}))
	defer srv.Close()
	d := leakDriver(srv.URL, srv.Client())
	_, err := d.Balances(context.Background())
	checkNoLeak(t, "balances", err)
}
