package hyperliquid

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"

	"github.com/sgnd/cuwan/internal/exchangex"
)

// testKey is a throwaway key for tests, not a real wallet.
var testKey = "0x" + strings.Repeat("11", 32)

const account = "0x000000000000000000000000000000000000abcd"

// venue is a fake Hyperliquid that records every /exchange request.
type venue struct {
	role   any
	reply  func(action map[string]any) any
	bodies []map[string]any
	raw    []string
}

func newVenue(t *testing.T) (*venue, *Driver) {
	t.Helper()
	v := &venue{reply: func(map[string]any) any { return nil }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		dec := json.NewDecoder(r.Body)
		dec.UseNumber()
		_ = dec.Decode(&body)
		if r.URL.Path == "/info" {
			switch body["type"] {
			case "meta":
				_ = json.NewEncoder(w).Encode(map[string]any{"universe": []map[string]any{{"name": "BTC", "szDecimals": 5}, {"name": "ETH", "szDecimals": 4}}})
			case "userRole":
				_ = json.NewEncoder(w).Encode(v.role)
			}
			return
		}
		v.bodies = append(v.bodies, body)
		action := body["action"].(map[string]any)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "response": map[string]any{"type": action["type"], "data": v.reply(action)}})
	}))
	t.Cleanup(srv.Close)
	d := &Driver{
		cfg:     exchangex.Config{APIKey: account, APISecret: testKey, Testnet: true},
		infoURL: srv.URL + "/info", exchURL: srv.URL + "/exchange", client: srv.Client(),
	}
	return v, d
}

func statuses(s ...any) func(map[string]any) any {
	return func(map[string]any) any { return map[string]any{"statuses": s} }
}

// The signature recovers to the signer's address, as the server checks.
func TestSignatureRecoversSigner(t *testing.T) {
	sg, _ := newSigner(testKey)
	action := omap{{"type", "cancel"}, {"cancels", []any{omap{{"a", 1}, {"o", int64(42)}}}}}
	sig, err := sg.signL1(action, 7, false)
	if err != nil {
		t.Fatal(err)
	}
	h, _ := actionHash(action, 7)
	compact := make([]byte, 65)
	compact[0] = byte(sig.V)
	r, _ := new(big.Int).SetString(sig.R[2:], 16)
	s, _ := new(big.Int).SetString(sig.S[2:], 16)
	r.FillBytes(compact[1:33])
	s.FillBytes(compact[33:])
	pub, _, err := ecdsa.RecoverCompact(compact, agentDigest(h, false))
	if err != nil {
		t.Fatal(err)
	}
	if got := "0x" + hex.EncodeToString(keccak(pub.SerializeUncompressed()[1:])[12:]); got != sg.addr {
		t.Fatalf("recovered %s, signer %s", got, sg.addr)
	}
	// A testnet signature must not verify as mainnet.
	if pub2, _, _ := ecdsa.RecoverCompact(compact, agentDigest(h, true)); pub2 != nil && pub2.IsEqual(pub) {
		t.Fatal("testnet signature also valid on mainnet")
	}
}

func TestPlaceOrderWire(t *testing.T) {
	v, d := newVenue(t)
	v.reply = statuses(map[string]any{"resting": map[string]any{"oid": 77}})
	o, err := d.PlaceOrder(context.Background(), exchangex.PlaceReq{Symbol: "ETH", Side: "sell", Price: 1670.1, Qty: 0.0147, ClientOID: "cwX", ReduceOnly: true, IOC: true})
	if err != nil || o.ID != "77" || o.Status != "open" {
		t.Fatalf("%+v %v", o, err)
	}
	b := v.bodies[0]
	w := b["action"].(map[string]any)["orders"].([]any)[0].(map[string]any)
	if w["a"] != json.Number("1") || w["b"] != false || w["p"] != "1670.1" || w["s"] != "0.0147" || w["r"] != true || w["c"] != Cloid("cwX") {
		t.Fatalf("wire %v", w)
	}
	if tif := w["t"].(map[string]any)["limit"].(map[string]any)["tif"]; tif != "Ioc" {
		t.Fatalf("tif %v", tif)
	}
	if b["vaultAddress"] != nil || b["signature"] == nil {
		t.Fatalf("body %v", b)
	}
}

func TestPlaceOrderFilledAndRejected(t *testing.T) {
	v, d := newVenue(t)
	v.reply = statuses(map[string]any{"filled": map[string]any{"oid": 5, "totalSz": "0.5"}})
	o, err := d.PlaceOrder(context.Background(), exchangex.PlaceReq{Symbol: "BTC", Side: "buy", Price: 1, Qty: 0.5})
	if err != nil || o.Status != "filled" || o.Filled != 0.5 {
		t.Fatalf("%+v %v", o, err)
	}
	v.reply = statuses(map[string]any{"error": "Order must have minimum value of $10."})
	if _, err := d.PlaceOrder(context.Background(), exchangex.PlaceReq{Symbol: "BTC", Side: "buy", Price: 1, Qty: 0.5}); err == nil || !strings.Contains(err.Error(), "minimum value") {
		t.Fatalf("err %v", err)
	}
}

func TestCancelPathsAndNotFound(t *testing.T) {
	v, d := newVenue(t)
	v.reply = statuses("success")
	if err := d.CancelOrder(context.Background(), "BTC", "9", ""); err != nil {
		t.Fatal(err)
	}
	if err := d.CancelOrder(context.Background(), "BTC", "", "cwY"); err != nil {
		t.Fatal(err)
	}
	if v.bodies[0]["action"].(map[string]any)["type"] != "cancel" || v.bodies[1]["action"].(map[string]any)["type"] != "cancelByCloid" {
		t.Fatalf("types %v %v", v.bodies[0]["action"], v.bodies[1]["action"])
	}
	v.reply = statuses(map[string]any{"error": "Order was never placed, already canceled, or filled."})
	if err := d.CancelOrder(context.Background(), "BTC", "9", ""); !errors.Is(err, exchangex.ErrOrderNotFound) {
		t.Fatalf("err %v", err)
	}
}

func TestNoncesIncrease(t *testing.T) {
	v, d := newVenue(t)
	v.reply = statuses("success")
	for i := 0; i < 3; i++ {
		_ = d.CancelOrder(context.Background(), "BTC", "1", "")
	}
	var last int64
	for _, b := range v.bodies {
		n, _ := b["nonce"].(json.Number).Int64()
		if n <= last {
			t.Fatalf("nonce %d after %d", n, last)
		}
		last = n
	}
}

func TestSetLeverage(t *testing.T) {
	v, d := newVenue(t)
	if err := d.SetLeverage(context.Background(), "ETH", 20); err != nil {
		t.Fatal(err)
	}
	a := v.bodies[0]["action"].(map[string]any)
	if a["type"] != "updateLeverage" || a["asset"] != json.Number("1") || a["leverage"] != json.Number("20") || a["isCross"] != true {
		t.Fatalf("%v", a)
	}
}

// Live use needs proof the key is an API wallet of the configured account.
func TestPermissionsProveAPIWallet(t *testing.T) {
	sg, _ := newSigner(testKey)
	v, d := newVenue(t)

	v.role = map[string]any{"role": "agent", "data": map[string]any{"user": strings.ToUpper(account[:2]) + account[2:]}}
	p, err := d.Permissions(context.Background())
	if err != nil || p.CanWithdraw || !p.CanTrade {
		t.Fatalf("api wallet: %+v %v", p, err)
	}
	v.role = map[string]any{"role": "agent", "data": map[string]any{"user": "0x0000000000000000000000000000000000000001"}}
	if _, err := d.Permissions(context.Background()); err == nil {
		t.Fatal("api wallet of another account accepted")
	}
	v.role = map[string]any{"role": "user"}
	if _, err := d.Permissions(context.Background()); err == nil {
		t.Fatal("main wallet key accepted as api wallet")
	}
	v.role = map[string]any{"role": "missing"}
	if _, err := d.Permissions(context.Background()); err == nil {
		t.Fatal("unknown key accepted")
	}
	d.cfg.APIKey = sg.addr // the secret is the account's own key
	if p, err := d.Permissions(context.Background()); err != nil || !p.CanWithdraw {
		t.Fatalf("account key: %+v %v", p, err)
	}
}

func TestBadSecretRefused(t *testing.T) {
	_, d := newVenue(t)
	d.cfg.APISecret = "not-a-key"
	if _, err := d.Permissions(context.Background()); err == nil {
		t.Fatal("bad secret accepted")
	}
}

// Review #12, 3: a partial fill is not "filled".
func TestPartialFillStaysOpen(t *testing.T) {
	v, d := newVenue(t)
	v.reply = statuses(map[string]any{"filled": map[string]any{"oid": 5, "totalSz": "0.2"}})
	o, err := d.PlaceOrder(context.Background(), exchangex.PlaceReq{Symbol: "BTC", Side: "buy", Price: 1, Qty: 0.5, IOC: true})
	if err != nil || o.Status != "open" || o.Filled != 0.2 {
		t.Fatalf("%+v %v", o, err)
	}
}

func TestOddResponsesFail(t *testing.T) {
	v, d := newVenue(t)
	v.reply = statuses("waitingForFill")
	if _, err := d.PlaceOrder(context.Background(), exchangex.PlaceReq{Symbol: "BTC", Side: "buy", Price: 1, Qty: 0.5}); err == nil {
		t.Fatal("bare string status accepted as an order")
	}
	v.reply = func(map[string]any) any { return nil }
	if _, err := d.PlaceOrder(context.Background(), exchangex.PlaceReq{Symbol: "BTC", Side: "buy", Price: 1, Qty: 0.5}); err == nil {
		t.Fatal("missing data accepted")
	}
}

func TestBadInputsRefused(t *testing.T) {
	for _, x := range []float64{math.NaN(), math.Inf(1)} {
		if _, err := floatToWire(x); err == nil {
			t.Errorf("%v accepted", x)
		}
	}
	for _, c := range [][2]float64{{0, 1}, {1, 0}, {-1, 1}, {1, -1}} {
		if _, err := orderWire(0, true, c[0], c[1], false, "Gtc", ""); err == nil {
			t.Errorf("price %v size %v accepted", c[0], c[1])
		}
	}
	_, d := newVenue(t)
	if err := d.SetLeverage(context.Background(), "BTC", 2.5); err == nil {
		t.Error("fractional leverage accepted")
	}
	// Zero, and the group order n itself, are not valid keys.
	for _, k := range []string{"0x" + strings.Repeat("00", 32), "0xfffffffffffffffffffffffffffffffebaaedce6af48a03bbfd25e8cd0364141"} {
		if _, err := newSigner(k); err == nil {
			t.Errorf("key %s accepted", k)
		}
	}
}
