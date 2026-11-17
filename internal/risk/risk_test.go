package risk

import "testing"

func TestCapLeverage(t *testing.T) {
	got, err := CapLeverage(50)
	if got != 20 || err == nil {
		t.Fatalf("got %v err %v", got, err)
	}
	got, err = CapLeverage(10)
	if got != 10 || err != nil {
		t.Fatalf("got %v err %v", got, err)
	}
}

func TestPreventiveCloseLong(t *testing.T) {
	entry := 100.0
	lev := 10.0
	liq := EstimateLiq(entry, lev, "buy") // 90
	// 95% of the way from 100 toward 90 is mark=90.5
	mark := entry - 0.95*(entry-liq)
	if !ShouldPreventiveClose(entry, mark, liq) {
		t.Fatalf("expected preventive at mark=%v liq=%v", mark, liq)
	}
	if ShouldPreventiveClose(entry, entry, liq) {
		t.Fatal("should not fire at entry")
	}
}

func TestCheckFutures(t *testing.T) {
	d := CheckFutures(100, 90.5, 25, "buy")
	if d.Leverage != 20 {
		t.Fatalf("lev %v", d.Leverage)
	}
	if !d.Preventive {
		t.Fatalf("expected preventive: %+v", d)
	}
}
