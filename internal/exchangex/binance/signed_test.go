package binance

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sgnd/cuwan/internal/exchangex"
)

// mock serves signed endpoints and checks the HMAC on every call.
func mock(t *testing.T, routes map[string]any) (*httptest.Server, *[]string) {
	t.Helper()
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		if r.Header.Get("X-MBX-APIKEY") != "k" {
			t.Errorf("missing api key header")
		}
		raw := r.URL.RawQuery
		i := strings.LastIndex(raw, "&signature=")
		mac := hmac.New(sha256.New, []byte("s"))
		mac.Write([]byte(raw[:i]))
		if hex.EncodeToString(mac.Sum(nil)) != raw[i+len("&signature="):] {
			t.Errorf("bad signature on %s", r.URL.Path)
		}
		body, ok := routes[r.Method+" "+r.URL.Path]
		if !ok {
			http.Error(w, `{"code":-1,"msg":"no route"}`, 404)
			return
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func drv(srv *httptest.Server, market string, testnet bool) *Driver {
	return &Driver{
		cfg:    exchangex.Config{APIKey: "k", APISecret: "s", Testnet: testnet},
		market: market, base: srv.URL, sapiBase: srv.URL, client: srv.Client(),
	}
}

func TestPermissionsFromAPIRestrictions(t *testing.T) {
	cases := []struct {
		name     string
		body     map[string]bool
		market   string
		withdraw bool
		trade    bool
	}{
		{"trade only spot", map[string]bool{"enableReading": true, "enableSpotAndMarginTrading": true}, exchangex.MarketSpot, false, true},
		{"withdraw on", map[string]bool{"enableReading": true, "enableFutures": true, "enableWithdrawals": true}, exchangex.MarketUSDM, true, true},
		{"internal transfer counts", map[string]bool{"enableFutures": true, "enableInternalTransfer": true}, exchangex.MarketUSDM, true, true},
		{"futures key without futures", map[string]bool{"enableSpotAndMarginTrading": true}, exchangex.MarketUSDM, false, false},
	}
	for _, c := range cases {
		srv, _ := mock(t, map[string]any{"GET /sapi/v1/account/apiRestrictions": c.body})
		p, err := drv(srv, c.market, false).Permissions(context.Background())
		if err != nil || p.CanWithdraw != c.withdraw || p.CanTrade != c.trade {
			t.Errorf("%s: %+v err=%v", c.name, p, err)
		}
	}
}

func TestPermissionsErrorIsReturned(t *testing.T) {
	srv, _ := mock(t, map[string]any{})
	if _, err := drv(srv, exchangex.MarketUSDM, false).Permissions(context.Background()); err == nil {
		t.Fatal("a failed probe must be an error (run start then refuses)")
	}
}

func TestTestnetSkipsSapi(t *testing.T) {
	srv, hits := mock(t, map[string]any{})
	p, err := drv(srv, exchangex.MarketUSDM, true).Permissions(context.Background())
	if err != nil || p.CanWithdraw || len(*hits) != 0 {
		t.Fatalf("%+v %v %v", p, err, *hits)
	}
}

func TestOrderStatusAndNormalisedStates(t *testing.T) {
	srv, hits := mock(t, map[string]any{
		"GET /fapi/v1/order":  map[string]any{"orderId": 7, "clientOrderId": "c", "side": "BUY", "price": "95", "origQty": "1", "executedQty": "1", "status": "FILLED"},
		"POST /fapi/v1/order": map[string]any{"orderId": 8, "clientOrderId": "d", "side": "SELL", "price": "100", "origQty": "1", "status": "NEW"},
	})
	d := drv(srv, exchangex.MarketUSDM, true)
	o, err := d.OrderStatus(context.Background(), "BTCUSDT", "7", "c")
	if err != nil || o.Status != "filled" || o.Filled != 1 {
		t.Fatalf("%+v %v", o, err)
	}
	if !strings.Contains((*hits)[0], "orderId=7") {
		t.Fatalf("query %s", (*hits)[0])
	}
	p, err := d.PlaceOrder(context.Background(), exchangex.PlaceReq{Symbol: "BTCUSDT", Side: "sell", Price: 100, Qty: 1, ClientOID: "d", ReduceOnly: true})
	if err != nil || p.Status != "open" {
		t.Fatalf("place: %+v %v", p, err)
	}
	if !strings.Contains((*hits)[1], "reduceOnly=true") {
		t.Fatalf("reduce-only lost: %s", (*hits)[1])
	}
	for in, want := range map[string]string{"NEW": "open", "PARTIALLY_FILLED": "open", "PENDING_CANCEL": "open", "FILLED": "filled", "CANCELED": "canceled", "EXPIRED": "canceled", "REJECTED": "canceled", "WEIRD": "unknown"} {
		if got := normStatus(in); got != want {
			t.Errorf("%s -> %s want %s", in, got, want)
		}
	}
}

func TestOrderStatusNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"code":-2013,"msg":"Order does not exist."}`))
	}))
	t.Cleanup(srv.Close)
	_, err := drv(srv, exchangex.MarketUSDM, true).OrderStatus(context.Background(), "BTCUSDT", "", "c")
	if !errors.Is(err, exchangex.ErrOrderNotFound) {
		t.Fatalf("err %v", err)
	}
}

func TestRulesFromExchangeInfo(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/fapi/v1/exchangeInfo" {
			t.Errorf("path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"symbols":[{"symbol":"ETHUSDT","filters":[]},{"symbol":"BTCUSDT","filters":[
			{"filterType":"PRICE_FILTER","tickSize":"0.10"},
			{"filterType":"LOT_SIZE","stepSize":"0.001","minQty":"0.001"}]}]}`))
	}))
	t.Cleanup(srv.Close)
	d := drv(srv, exchangex.MarketUSDM, true)
	for i := 0; i < 2; i++ {
		r, err := d.Rules(context.Background(), "BTCUSDT")
		if err != nil || r.Tick != 0.1 || r.Step != 0.001 || r.MinQty != 0.001 {
			t.Fatalf("%+v %v", r, err)
		}
	}
	if calls != 1 {
		t.Fatalf("not cached: %d calls", calls)
	}
	if _, err := d.Rules(context.Background(), "ETHUSDT"); err == nil {
		t.Fatal("symbol without filters accepted")
	}
}

func TestIOCAndCancelNotFound(t *testing.T) {
	srv, hits := mock(t, map[string]any{
		"POST /fapi/v1/order": map[string]any{"orderId": 1, "status": "EXPIRED"},
	})
	d := drv(srv, exchangex.MarketUSDM, true)
	if _, err := d.PlaceOrder(context.Background(), exchangex.PlaceReq{Symbol: "BTCUSDT", Side: "sell", Price: 1, Qty: 1, ClientOID: "c", IOC: true, ReduceOnly: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains((*hits)[0], "timeInForce=IOC") {
		t.Fatalf("not IOC: %s", (*hits)[0])
	}
	nf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"code":-2011,"msg":"Unknown order sent."}`))
	}))
	t.Cleanup(nf.Close)
	if err := drv(nf, exchangex.MarketUSDM, true).CancelOrder(context.Background(), "BTCUSDT", "", "c"); !errors.Is(err, exchangex.ErrOrderNotFound) {
		t.Fatalf("err %v", err)
	}
}

func TestHedgeModeRefused(t *testing.T) {
	srv, _ := mock(t, map[string]any{
		"GET /fapi/v2/positionRisk": []map[string]string{{"symbol": "BTCUSDT", "positionAmt": "1", "positionSide": "LONG"}},
	})
	if _, err := drv(srv, exchangex.MarketUSDM, true).Positions(context.Background()); err == nil || !strings.Contains(err.Error(), "hedge mode") {
		t.Fatalf("err %v", err)
	}
}
