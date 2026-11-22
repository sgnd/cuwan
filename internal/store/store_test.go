package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func testKey(b byte) []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = b
	}
	return k
}

func open(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := Open(dir, testKey(7))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestUpdateThenView(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir)
	if err := s.Update(func(st *State) error {
		st.Bots["g"] = Bot{Name: "g", Exchange: "fx", Symbol: "BTCUSDT", Lower: 90, Upper: 110, Levels: 5, OrderSize: 1}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var got Bot
	if err := open(t, dir).View(func(st *State) error { got = st.Bots["g"]; return nil }); err != nil {
		t.Fatal(err)
	}
	if got.Symbol != "BTCUSDT" || got.Levels != 5 {
		t.Fatalf("got %+v", got)
	}
}

// Old defect: upsert by name swallowed errors and could leave two runtime
// rows for one bot. Now a runtime is keyed by its bot.
func TestOneRuntimePerBot(t *testing.T) {
	s := open(t, t.TempDir())
	for i := 0; i < 3; i++ {
		if err := s.Update(func(st *State) error {
			st.Runtimes["g"] = Runtime{Bot: "g", Status: "running", Meta: map[string]string{"n": string(rune('a' + i))}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.View(func(st *State) error {
		if len(st.Runtimes) != 1 || st.Runtimes["g"].Meta["n"] != "c" {
			t.Fatalf("runtimes %+v", st.Runtimes)
		}
		return nil
	})
}

func TestErrorsAreReturned(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir)
	if err := s.Update(func(st *State) error { st.Secrets["k"] = "v"; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	called := false
	err := s.Update(func(*State) error { called = true; return nil })
	if err == nil || called {
		t.Fatalf("a damaged store was updated: err=%v called=%v", err, called)
	}
	want := errors.New("stop")
	s2 := open(t, t.TempDir())
	if err := s2.Update(func(*State) error { return want }); !errors.Is(err, want) {
		t.Fatalf("the callback's error was lost: %v", err)
	}
}

func TestWrongKeyDoesNotOpen(t *testing.T) {
	dir := t.TempDir()
	if err := open(t, dir).Update(func(st *State) error { st.Secrets["k"] = "v"; return nil }); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir, testKey(8))
	if err == nil {
		err = s.View(func(*State) error { return nil })
	}
	if !errors.Is(err, ErrOpen) {
		t.Fatalf("err %v", err)
	}
}

// The state is bound to its generation: the generation grows on every
// save, and a changed generation does not open.
func TestGenerationIsBound(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir)
	var gens []uint64
	for i := 0; i < 3; i++ {
		_ = s.Update(func(st *State) error { st.Secrets["k"] = "v"; return nil })
		_ = s.View(func(st *State) error { gens = append(gens, st.Gen); return nil })
	}
	if !(gens[0] < gens[1] && gens[1] < gens[2]) {
		t.Fatalf("generations %v", gens)
	}
	p := filepath.Join(dir, fileName)
	b, _ := os.ReadFile(p)
	b[7]++ // the generation header
	_ = os.WriteFile(p, b, 0o600)
	if err := s.View(func(*State) error { return nil }); !errors.Is(err, ErrOpen) {
		t.Fatalf("a changed generation opened: %v", err)
	}
}

func TestAuditIsChained(t *testing.T) {
	s := open(t, t.TempDir())
	for _, c := range []string{"a", "b", "c"} {
		if err := s.Update(func(st *State) error { return st.Append(map[string]any{"command": c}) }); err != nil {
			t.Fatal(err)
		}
	}
	var entries []Entry
	_ = s.View(func(st *State) error { entries = append(entries, st.Audit...); return nil })
	if err := VerifyAudit(entries); err != nil || len(entries) != 3 || entries[2].Seq != 3 {
		t.Fatalf("err %v entries %+v", err, entries)
	}
	changed := append([]Entry(nil), entries...)
	changed[1].Data = []byte(`{"command":"x"}`)
	if err := VerifyAudit(changed); !errors.Is(err, ErrTampered) {
		t.Fatalf("changed entry: %v", err)
	}
	dropped := []Entry{entries[0], entries[2]}
	if err := VerifyAudit(dropped); !errors.Is(err, ErrTampered) {
		t.Fatalf("dropped entry: %v", err)
	}
}

func TestNoLostUpdates(t *testing.T) {
	dir := t.TempDir()
	_ = open(t, dir).Update(func(st *State) error { return nil })
	var wg sync.WaitGroup
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			s := open(t, dir)
			for i := 0; i < 20; i++ {
				if err := s.Update(func(st *State) error { st.Secrets[fmt.Sprint(w, "-", i)] = "x"; return nil }); err != nil {
					t.Error(err)
				}
			}
		}(w)
	}
	wg.Wait()
	_ = open(t, dir).View(func(st *State) error {
		if len(st.Secrets) != 40 {
			t.Errorf("%d updates kept, want 40", len(st.Secrets))
		}
		return nil
	})
}

func TestPrivateFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	s := open(t, dir)
	_ = s.Update(func(st *State) error { return nil })
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Errorf("home %v", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(filepath.Join(dir, fileName)); fi.Mode().Perm() != 0o600 {
		t.Errorf("store %v", fi.Mode().Perm())
	}
}
