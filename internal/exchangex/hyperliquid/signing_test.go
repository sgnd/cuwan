package hyperliquid

import (
	"encoding/hex"
	"math/big"
	"testing"
)

// Vectors from the official Python SDK, tests/signing_test.py
// (hyperliquid-dex/hyperliquid-python-sdk). The key is the SDK's public
// test key, not a real wallet.
const sdkTestKey = "0x0123456789012345678901234567890123456789012345678901234567890123"

func sdkSigner(t *testing.T) *signer {
	t.Helper()
	s, err := newSigner(sdkTestKey)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// sameHex compares hex numbers by value: the SDK drops leading zeros.
func sameHex(a, b string) bool {
	x, ok1 := new(big.Int).SetString(a[2:], 16)
	y, ok2 := new(big.Int).SetString(b[2:], 16)
	return ok1 && ok2 && x.Cmp(y) == 0
}

func check(t *testing.T, name string, got Signature, r, s string, v int) {
	t.Helper()
	if !sameHex(got.R, r) || !sameHex(got.S, s) || got.V != v {
		t.Errorf("%s: got %+v\nwant r=%s s=%s v=%d", name, got, r, s, v)
	}
}

func orderAction(t *testing.T, asset int, buy bool, px, sz float64, tif string, cloid string) omap {
	t.Helper()
	w, err := orderWire(asset, buy, px, sz, false, tif, cloid)
	if err != nil {
		t.Fatal(err)
	}
	return omap{{"type", "order"}, {"orders", []any{w}}, {"grouping", "na"}}
}

func TestPhantomAgentMatchesProduction(t *testing.T) {
	h, err := actionHash(orderAction(t, 4, true, 1670.1, 0.0147, "Ioc", ""), 1677777606040)
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(h); got != "0fcbeda5ae3c4950a548021552a4fea2226858c4453571bf3f24ba017eac2908" {
		t.Fatalf("connectionId %s", got)
	}
}

func TestL1ActionSigningMatches(t *testing.T) {
	s := sdkSigner(t)
	action := omap{{"type", "dummy"}, {"num", int64(100000000000)}} // float_to_int_for_hashing(1000)
	main, _ := s.signL1(action, 0, true)
	check(t, "dummy mainnet", main, "0x53749d5b30552aeb2fca34b530185976545bb22d0b3ce6f62e31be961a59298", "0x755c40ba9bf05223521753995abb2f73ab3229be8ec921f350cb447e384d8ed8", 27)
	test, _ := s.signL1(action, 0, false)
	check(t, "dummy testnet", test, "0x542af61ef1f429707e3c76c5293c80d01f74ef853e34b76efffcb57e574f9510", "0x17b8b32f086e8cdede991f1e2c529f5dd5297cbe8128500e00cbaf766204a613", 28)
}

func TestL1OrderSigningMatches(t *testing.T) {
	s := sdkSigner(t)
	a := orderAction(t, 1, true, 100, 100, "Gtc", "")
	main, _ := s.signL1(a, 0, true)
	check(t, "order mainnet", main, "0xd65369825a9df5d80099e513cce430311d7d26ddf477f5b3a33d2806b100d78e", "0x2b54116ff64054968aa237c20ca9ff68000f977c93289157748a3162b6ea940e", 28)
	test, _ := s.signL1(a, 0, false)
	check(t, "order testnet", test, "0x82b2ba28e76b3d761093aaded1b1cdad4960b3af30212b343fb2e6cdfa4e3d54", "0x6b53878fc99d26047f4d7e8c90eb98955a109f44209163f52d8dc4278cbbd9f5", 27)
}

func TestL1OrderWithCloidSigningMatches(t *testing.T) {
	s := sdkSigner(t)
	a := orderAction(t, 1, true, 100, 100, "Gtc", "0x00000000000000000000000000000001")
	main, _ := s.signL1(a, 0, true)
	check(t, "cloid mainnet", main, "0x41ae18e8239a56cacbc5dad94d45d0b747e5da11ad564077fcac71277a946e3", "0x3c61f667e747404fe7eea8f90ab0e76cc12ce60270438b2058324681a00116da", 27)
	test, _ := s.signL1(a, 0, false)
	check(t, "cloid testnet", test, "0xeba0664bed2676fc4e5a743bf89e5c7501aa6d870bdb9446e122c9466c5cd16d", "0x7f3e74825c9114bc59086f1eebea2928c190fdfbfde144827cb02b85bbe90988", 28)
}

func TestFloatToWire(t *testing.T) {
	for in, want := range map[float64]string{100: "100", 1670.1: "1670.1", 0.0147: "0.0147", 0: "0", -0.5: "-0.5"} {
		if got, err := floatToWire(in); err != nil || got != want {
			t.Errorf("%v -> %q %v", in, got, err)
		}
	}
	if _, err := floatToWire(0.123456789); err == nil {
		t.Error("9 decimals accepted")
	}
}
