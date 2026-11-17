package grid

import (
	"testing"
)

func TestBuild(t *testing.T) {
	p, err := Build(100, 110, 6)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Prices) != 6 || p.Prices[0] != 100 || p.Prices[5] != 110 {
		t.Fatalf("%v", p.Prices)
	}
}

func TestClientOIDStable(t *testing.T) {
	a := ClientOID("btc", 100.5, "buy", 1)
	b := ClientOID("btc", 100.5, "buy", 1)
	c := ClientOID("btc", 100.5, "sell", 1)
	if a != b || a == c || len(a) < 10 {
		t.Fatalf("%s %s %s", a, b, c)
	}
}

func TestBadRange(t *testing.T) {
	if _, err := Build(110, 100, 5); err == nil {
		t.Fatal("expected error")
	}
}
