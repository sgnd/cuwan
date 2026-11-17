package vault

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sgnd/cuwan/internal/store"
)

func testHome(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CUWAN_HOME", filepath.Join(dir, "home"))
	t.Setenv("CUWAN_KEYCHAIN", "off")
	PassphraseN = 1 << 10 // faster tests
	store.ResetForTest()
	passMu.Lock()
	pass = ""
	passMu.Unlock()
	_ = os.Unsetenv("CUWAN_VAULT_PASS")
}

func TestSecretRoundTrip(t *testing.T) {
	testHome(t)
	if err := Set("binance", "sk-live-secret"); err != nil {
		t.Fatal(err)
	}
	got, err := Get("binance")
	if err != nil {
		t.Fatal(err)
	}
	if got != "sk-live-secret" {
		t.Fatalf("got %q", got)
	}
	list, err := List()
	if err != nil || len(list) != 1 || list[0].Name != "binance" {
		t.Fatalf("list %+v err %v", list, err)
	}
}

func TestWrongPassphrase(t *testing.T) {
	testHome(t)
	t.Setenv("CUWAN_VAULT_PASS", "correct-horse")
	TakeUnlockEnv()
	if err := Set("a", "value"); err != nil {
		t.Fatal(err)
	}
	store.ResetForTest()
	passMu.Lock()
	pass = "wrong-passphrase"
	passMu.Unlock()
	_, err := Get("a")
	if err == nil {
		t.Fatal("wrong passphrase opened vault")
	}
}

func TestParseRef(t *testing.T) {
	n, ok := ParseRef("secret:binance")
	if !ok || n != "binance" {
		t.Fatalf("%q %v", n, ok)
	}
	if _, ok := ParseRef("plain"); ok {
		t.Fatal("plain accepted")
	}
}

func TestRenameDelete(t *testing.T) {
	testHome(t)
	if err := Set("old", "v"); err != nil {
		t.Fatal(err)
	}
	if err := Rename("old", "new"); err != nil {
		t.Fatal(err)
	}
	if _, err := Get("old"); err == nil {
		t.Fatal("old still present")
	}
	got, err := Get("new")
	if err != nil || got != "v" {
		t.Fatalf("%q %v", got, err)
	}
	if err := Delete("new"); err != nil {
		t.Fatal(err)
	}
}

func TestTakeUnlockEnvStrips(t *testing.T) {
	t.Setenv("CUWAN_VAULT_PASS", "strip-me-please")
	TakeUnlockEnv()
	if _, ok := os.LookupEnv("CUWAN_VAULT_PASS"); ok {
		t.Fatal("CUWAN_VAULT_PASS still in env")
	}
	if Passphrase() != "strip-me-please" {
		t.Fatal("passphrase not retained in memory")
	}
}
