package store

import (
	"os"
	"path/filepath"
	"testing"
)

func testStore(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CUWAN_HOME", filepath.Join(dir, "home"))
	t.Setenv("CUWAN_KEYCHAIN", "off")
	ResetForTest()
	master := make([]byte, 32)
	for i := range master {
		master[i] = byte(i + 1)
	}
	KeySource = func(meta map[string]string, create bool) ([]byte, map[string]string, error) {
		out := append([]byte(nil), master...)
		m := map[string]string{"key_mode": "test"}
		return out, m, nil
	}
	t.Cleanup(func() {
		ResetForTest()
		KeySource = nil
	})
}

func TestExchangeBotRuntimeRoundTrip(t *testing.T) {
	testStore(t)

	ex := &Exchange{
		Name:      "bn",
		Venue:     "binance",
		Market:    "spot",
		SecretRef: "secret:demo",
	}
	if err := PutExchange(ex); err != nil {
		t.Fatal(err)
	}
	got, err := GetExchange("bn")
	if err != nil || got.Venue != "binance" || got.SecretRef != "secret:demo" {
		t.Fatalf("exchange %+v err %v", got, err)
	}

	bot := &Bot{
		Name:     "g1",
		Exchange: "bn",
		Symbol:   "BTCUSDT",
		Lower:    90_000,
		Upper:    110_000,
		Levels:   5,
		Leverage: 25, // store accepts; risk package caps at runtime
	}
	if err := PutBot(bot); err != nil {
		t.Fatal(err)
	}
	gb, err := GetBot("g1")
	if err != nil || gb.Levels != 5 || gb.Symbol != "BTCUSDT" {
		t.Fatalf("bot %+v err %v", gb, err)
	}

	rt := &RuntimeState{
		BotName: "g1",
		Mode:    "paper",
		Status:  "running",
		Orders: []RuntimeOrder{{
			ClientOID: "oid-1",
			Side:      "buy",
			Price:     100_000,
			Qty:       0.01,
			Status:    "open",
		}},
	}
	if err := PutRuntime(rt); err != nil {
		t.Fatal(err)
	}
	gr, err := GetRuntime("g1")
	if err != nil || gr.Mode != "paper" || len(gr.Orders) != 1 {
		t.Fatalf("runtime %+v err %v", gr, err)
	}

	if err := AppendAudit("bot.add", map[string]any{"name": "g1"}); err != nil {
		t.Fatal(err)
	}
	aud, err := ListAudit(10)
	if err != nil || len(aud) == 0 {
		t.Fatalf("audit %v err %v", aud, err)
	}

	if err := DeleteBot("g1"); err != nil {
		t.Fatal(err)
	}
	if _, err := GetBot("g1"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestRefuseWorldReadable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "key")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RefuseWorldReadable(path); err == nil {
		t.Fatal("expected error for 0644")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RefuseWorldReadable(path); err != nil {
		t.Fatal(err)
	}
}
