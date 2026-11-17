package sealrow

import (
	"bytes"
	"errors"
	"testing"
)

func testKey(t *testing.T, fill byte) *Key {
	t.Helper()
	k, err := NewKey(bytes.Repeat([]byte{fill}, MasterLen))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestRoundTrip(t *testing.T) {
	k := testKey(t, 1)
	r := Row{ID: "a1", Type: "bot", Version: 3}
	for _, plain := range [][]byte{nil, []byte("btc-grid"), bytes.Repeat([]byte("x"), 1<<16)} {
		s, err := k.Seal(r, plain)
		if err != nil {
			t.Fatal(err)
		}
		got, err := k.Open(r, s)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, plain) {
			t.Fatalf("got %q", got)
		}
	}
}

func TestPlainNotVisible(t *testing.T) {
	k := testKey(t, 1)
	s, _ := k.Seal(Row{ID: "a", Type: "secret", Version: 1}, []byte("api-key-secret-value"))
	if bytes.Contains(s, []byte("api-key")) {
		t.Fatal("plaintext readable in sealed row")
	}
}

func TestSealIsRandomized(t *testing.T) {
	k := testKey(t, 1)
	r := Row{ID: "a", Type: "bot", Version: 1}
	a, _ := k.Seal(r, []byte("same"))
	b, _ := k.Seal(r, []byte("same"))
	if bytes.Equal(a, b) {
		t.Fatal("two seals of the same row are equal")
	}
}

func TestMovedRowDoesNotOpen(t *testing.T) {
	k := testKey(t, 1)
	r := Row{ID: "a", Type: "bot", Version: 2}
	s, _ := k.Seal(r, []byte("v"))
	for _, moved := range []Row{
		{ID: "b", Type: "bot", Version: 2},
		{ID: "a", Type: "secret", Version: 2},
		{ID: "a", Type: "bot", Version: 1},
		{ID: "as", Type: "ot", Version: 2},
	} {
		if _, err := k.Open(moved, s); !errors.Is(err, ErrOpen) {
			t.Fatalf("%+v: err %v, want ErrOpen", moved, err)
		}
	}
}

func TestWrongKeyAndDamage(t *testing.T) {
	r := Row{ID: "a", Type: "bot", Version: 1}
	s, _ := testKey(t, 1).Seal(r, []byte("v"))
	if _, err := testKey(t, 2).Open(r, s); !errors.Is(err, ErrOpen) {
		t.Fatalf("other key: %v", err)
	}
	k := testKey(t, 1)
	for i := range s {
		d := bytes.Clone(s)
		d[i] ^= 1
		if _, err := k.Open(r, d); err == nil {
			t.Fatalf("flip at %d opened", i)
		}
	}
}

func TestNewerFormat(t *testing.T) {
	k := testKey(t, 1)
	r := Row{ID: "a", Type: "bot", Version: 1}
	s, _ := k.Seal(r, []byte("v"))
	d := bytes.Clone(s)
	d[0] = Format + 1
	if _, err := k.Open(r, d); !errors.Is(err, ErrNewerFormat) {
		t.Fatalf("newer format: %v", err)
	}
}

func TestBadInput(t *testing.T) {
	if _, err := NewKey(make([]byte, 16)); err == nil {
		t.Fatal("short master key accepted")
	}
	k := testKey(t, 1)
	if _, err := k.Seal(Row{Type: "bot"}, nil); err == nil {
		t.Fatal("empty ID accepted")
	}
}

func TestNewID(t *testing.T) {
	a, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewID()
	if len(a) != 32 || a == b {
		t.Fatalf("ids %q %q", a, b)
	}
}
