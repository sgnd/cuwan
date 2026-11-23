package keychain

import (
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
)

type fake struct {
	item  string
	fail  error
	calls []string
	stdin []string
}

func (f *fake) install(t *testing.T) {
	prev := run
	run = func(stdin, name string, args ...string) ([]byte, error) {
		f.calls = append(f.calls, name+" "+strings.Join(args, " "))
		f.stdin = append(f.stdin, stdin)
		if f.fail != nil {
			return nil, f.fail
		}
		if strings.Contains(stdin, "add-generic-password") || (name == "secret-tool" && args[0] == "store") {
			f.item = hexFrom(stdin)
			return nil, nil
		}
		if f.item == "" {
			return nil, errors.New("not found")
		}
		return []byte(f.item + "\n"), nil
	}
	t.Cleanup(func() { run = prev })
}

// hexFrom finds the 64-hex key in what was sent on stdin.
func hexFrom(s string) string {
	for _, w := range strings.Fields(strings.ReplaceAll(s, "'", " ")) {
		if len(w) == 64 {
			if _, err := hex.DecodeString(w); err == nil {
				return w
			}
		}
	}
	return ""
}

func TestCreateThenGet(t *testing.T) {
	for _, goos := range []string{"darwin", "linux"} {
		f := &fake{}
		f.install(t)
		k, err := key(goos, true)
		if err != nil || len(k) != 32 {
			t.Fatalf("%s: create %v %d", goos, err, len(k))
		}
		k2, err := key(goos, false)
		if err != nil || hex.EncodeToString(k2) != hex.EncodeToString(k) {
			t.Fatalf("%s: get %v", goos, err)
		}
		for _, c := range f.calls {
			if strings.Contains(c, f.item) {
				t.Fatalf("%s: the key was on the command line: %s", goos, c)
			}
		}
	}
}

// Old defect: a failed keychain silently wrote a plain key file.
func TestFailingKeychainStops(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	f := &fake{fail: errors.New("keychain locked")}
	f.install(t)
	if _, err := key("darwin", true); err == nil {
		t.Fatal("no error when the keychain fails")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatal("a file was written")
	}
}

func TestOtherSystemsRefused(t *testing.T) {
	if _, err := key("windows", true); err == nil {
		t.Fatal("windows: want an error until a key store is wired")
	}
}

func TestBadItemRefused(t *testing.T) {
	f := &fake{item: "not-hex"}
	f.install(t)
	if _, err := key("darwin", false); err == nil {
		t.Fatal("a non-hex item was taken as the key")
	}
}

func TestCreateIsGenerated(t *testing.T) {
	f := &fake{}
	f.install(t)
	a, _ := key("darwin", true)
	f.item = ""
	b, _ := key("darwin", true)
	if hex.EncodeToString(a) == hex.EncodeToString(b) {
		t.Fatal("two created keys are equal")
	}
}

// Audit MEDIUM: on macOS the item lists no trusted app, so every read of the
// key asks the person.
func TestMacItemTrustsNoApp(t *testing.T) {
	f := &fake{}
	f.install(t)
	if _, err := key("darwin", true); err != nil {
		t.Fatal(err)
	}
	added := false
	for _, in := range f.stdin {
		if strings.Contains(in, "add-generic-password") {
			added = true
			if !strings.Contains(in, ` -T "" `) {
				t.Fatalf("no empty trusted-app list: %q", strings.ReplaceAll(in, f.item, "KEY"))
			}
		}
	}
	if !added {
		t.Fatal("no item was added")
	}
}
