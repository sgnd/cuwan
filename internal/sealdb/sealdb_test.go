package sealdb

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/sgnd/cuwan/internal/sealrow"
)

func testKey(t *testing.T, fill byte) *sealrow.Key {
	t.Helper()
	k, err := sealrow.NewKey(bytes.Repeat([]byte{fill}, sealrow.MasterLen))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestOpenRoundTrip(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	k := testKey(t, 1)
	d, err := Open(ctx, path, k)
	if err != nil {
		t.Fatal(err)
	}
	err = d.Update(ctx, func(tx *Tx) error {
		_, err := tx.Put("bot", "", []byte(`{"name":"btc"}`))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var got []Record
	err = d.View(ctx, func(tx *Tx) error {
		var err error
		got, err = tx.List("bot")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || string(got[0].Data) != `{"name":"btc"}` {
		t.Fatalf("got %+v", got)
	}
	d.Close()
}

func TestWrongKey(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	d, err := Open(ctx, path, testKey(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	_, err = Open(ctx, path, testKey(t, 2))
	if !errors.Is(err, ErrWrongKey) {
		t.Fatalf("err %v, want ErrWrongKey", err)
	}
}

func TestTamperReadableColumns(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	k := testKey(t, 1)
	d, err := Open(ctx, path, k)
	if err != nil {
		t.Fatal(err)
	}
	var id string
	err = d.Update(ctx, func(tx *Tx) error {
		r, err := tx.Put("bot", "", []byte("x"))
		id = r.ID
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	// flip mtime column out of band
	_, err = d.db.Exec(`UPDATE records SET mtime=mtime+1 WHERE id=?`, id)
	if err != nil {
		t.Fatal(err)
	}
	err = d.View(ctx, func(tx *Tx) error {
		_, err := tx.Get(id)
		return err
	})
	if !errors.Is(err, ErrTampered) {
		t.Fatalf("err %v, want ErrTampered", err)
	}
	d.Close()
}

func TestRekey(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	old := testKey(t, 1)
	d, err := Open(ctx, path, old)
	if err != nil {
		t.Fatal(err)
	}
	err = d.Update(ctx, func(tx *Tx) error {
		_, err := tx.Put("bot", "", []byte("grid"))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	newKey := testKey(t, 9)
	if err := d.Rekey(ctx, newKey, nil); err != nil {
		t.Fatal(err)
	}
	d.Close()
	d2, err := Open(ctx, path, newKey)
	if err != nil {
		t.Fatal(err)
	}
	err = d2.View(ctx, func(tx *Tx) error {
		rows, err := tx.List("bot")
		if err != nil {
			return err
		}
		if len(rows) != 1 || string(rows[0].Data) != "grid" {
			t.Fatalf("after rekey: %+v", rows)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	d2.Close()
	_, err = Open(ctx, path, old)
	if !errors.Is(err, ErrWrongKey) {
		t.Fatalf("old key still opens: %v", err)
	}
}
