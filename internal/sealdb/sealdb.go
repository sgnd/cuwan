// Package sealdb is cuwan's local store: one SQLite file whose records are
// sealed rows (package sealrow). Cloud sync is out of scope; this package
// keeps a single-machine sealed inventory.
package sealdb

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/sgnd/cuwan/internal/sealrow"
)

// Format is the database format this cuwan writes.
const Format = 1

// BusyTimeout is how long a writer waits for another writer.
const BusyTimeout = 10 * time.Second

var (
	ErrNewerFormat = errors.New("sealdb: store made by a newer cuwan; update cuwan")
	ErrNotFound    = errors.New("sealdb: record not found")
	ErrReadOnly    = errors.New("sealdb: change inside a read step")
	ErrTampered    = errors.New("sealdb: store was changed outside cuwan")
	ErrWrongKey    = errors.New("sealdb: the key does not open this store")
	ErrKeyChanged  = errors.New("sealdb: the store's key was changed; open it again")
)

const keycheckID, keycheckType = "keycheck", "keycheck"

const schema = `
CREATE TABLE IF NOT EXISTS meta (k TEXT PRIMARY KEY, v TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS records (
	id      TEXT PRIMARY KEY,
	type    TEXT NOT NULL,
	version INTEGER NOT NULL,
	mtime   INTEGER NOT NULL,
	deleted INTEGER NOT NULL DEFAULT 0,
	data    BLOB NOT NULL
);
CREATE INDEX IF NOT EXISTS records_type ON records(type);
CREATE TABLE IF NOT EXISTS audit (
	seq  INTEGER PRIMARY KEY AUTOINCREMENT,
	id   TEXT NOT NULL UNIQUE,
	time INTEGER NOT NULL,
	data BLOB NOT NULL
);`

// DB is an open store.
type DB struct {
	db     *sql.DB
	key    *sealrow.Key
	mu     sync.Mutex
	opened bool
}

func fileURI(path string, q url.Values) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs), RawQuery: q.Encode()}
	if filepath.VolumeName(abs) != "" {
		u.Path = "/" + u.Path
	}
	return u.String(), nil
}

// Open opens the store at path, making it when it does not exist (mode 0600).
func Open(ctx context.Context, path string, key *sealrow.Key) (*DB, error) {
	if key == nil {
		return nil, errors.New("sealdb: no key")
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	err = f.Chmod(0o600)
	f.Close()
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	for _, p := range []string{
		"busy_timeout(" + strconv.Itoa(int(BusyTimeout/time.Millisecond)) + ")",
		"journal_mode(DELETE)",
		"synchronous(FULL)",
		"foreign_keys(ON)",
		"secure_delete(ON)",
	} {
		q.Add("_pragma", p)
	}
	dsn, err := fileURI(path, q)
	if err != nil {
		return nil, err
	}
	sdb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	sdb.SetMaxOpenConns(1)
	d := &DB{db: sdb, key: key}
	if err := d.init(ctx); err != nil {
		sdb.Close()
		return nil, err
	}
	d.opened = true
	return d, nil
}

func (d *DB) init(ctx context.Context) error {
	ready := false
	err := d.run(ctx, false, func(tx *Tx) error {
		var n int
		if err := tx.c.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('meta','records','audit')`).Scan(&n); err != nil || n < 3 {
			return err
		}
		var v string
		if err := tx.c.QueryRowContext(ctx, `SELECT v FROM meta WHERE k='format'`).Scan(&v); err != nil {
			return nil
		}
		ready = true
		return d.checkOpened(tx, v)
	})
	if err != nil || ready {
		return err
	}
	return d.run(ctx, true, func(tx *Tx) error {
		if _, err := tx.c.ExecContext(ctx, schema); err != nil {
			return err
		}
		var v string
		err := tx.c.QueryRowContext(ctx, `SELECT v FROM meta WHERE k='format'`).Scan(&v)
		if errors.Is(err, sql.ErrNoRows) {
			if _, err = tx.c.ExecContext(ctx, `INSERT INTO meta(k,v) VALUES('format',?)`, strconv.Itoa(Format)); err != nil {
				return err
			}
			_, err = tx.Put(keycheckType, keycheckID, []byte("cuwan"))
			return err
		}
		if err != nil {
			return err
		}
		return d.checkOpened(tx, v)
	})
}

func (d *DB) checkOpened(tx *Tx, v string) error {
	n, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("sealdb: format %q is not a number", v)
	}
	if n > Format {
		return ErrNewerFormat
	}
	if _, err := tx.Get(keycheckID); err != nil {
		if errors.Is(err, sealrow.ErrOpen) {
			return ErrWrongKey
		}
		if errors.Is(err, ErrNotFound) {
			return fmt.Errorf("record %s: %w", keycheckID, ErrTampered)
		}
		return err
	}
	return nil
}

// Peek reads readable meta without a key.
func Peek(ctx context.Context, path string) (map[string]string, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("mode", "ro")
	q.Add("_pragma", "busy_timeout("+strconv.Itoa(int(BusyTimeout/time.Millisecond))+")")
	dsn, err := fileURI(path, q)
	if err != nil {
		return nil, err
	}
	sdb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	defer sdb.Close()
	rows, err := sdb.QueryContext(ctx, `SELECT k,v FROM meta`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		m[k] = v
	}
	return m, rows.Err()
}

// Close closes the store.
func (d *DB) Close() error { return d.db.Close() }

// View runs fn in a read step.
func (d *DB) View(ctx context.Context, fn func(*Tx) error) error {
	return d.run(ctx, false, fn)
}

// Update runs fn in a write step.
func (d *DB) Update(ctx context.Context, fn func(*Tx) error) error {
	return d.run(ctx, true, fn)
}

func (d *DB) run(ctx context.Context, write bool, fn func(*Tx) error) (err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	c, err := d.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	begin := "BEGIN DEFERRED"
	if write {
		begin = "BEGIN IMMEDIATE"
	}
	if _, err := c.ExecContext(ctx, begin); err != nil {
		return err
	}
	done := false
	defer func() {
		if !done {
			_, _ = c.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	tx := &Tx{ctx: ctx, c: c, key: d.key, write: write}
	if d.opened {
		if err := tx.checkKey(); err != nil {
			return err
		}
	}
	if err := fn(tx); err != nil {
		return err
	}
	if _, err := c.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	done = true
	return nil
}

// Record is one record, opened.
type Record struct {
	ID      string
	Type    string
	Version uint64
	MTime   time.Time
	Deleted bool
	Data    []byte
}

// Tx is one read or write step.
type Tx struct {
	ctx   context.Context
	c     *sql.Conn
	key   *sealrow.Key
	write bool
}

const headLen = 9

func head(del bool, mtime int64, data []byte) []byte {
	b := make([]byte, headLen, headLen+len(data))
	if del {
		b[0] = 1
	}
	binary.BigEndian.PutUint64(b[1:], uint64(mtime))
	return append(b, data...)
}

func (t *Tx) open(id, typ string, ver uint64, mtime int64, del int, sealed []byte) (Record, error) {
	plain, err := t.key.Open(sealrow.Row{ID: id, Type: typ, Version: ver}, sealed)
	if err != nil {
		return Record{}, fmt.Errorf("record %s: %w", id, err)
	}
	if len(plain) < headLen || (plain[0] != 0) != (del != 0) || int64(binary.BigEndian.Uint64(plain[1:])) != mtime {
		return Record{}, fmt.Errorf("record %s: %w", id, ErrTampered)
	}
	return Record{ID: id, Type: typ, Version: ver, MTime: time.Unix(0, mtime), Deleted: del != 0, Data: plain[headLen:]}, nil
}

// Get returns the record with id.
func (t *Tx) Get(id string) (Record, error) {
	return t.rawGet(id)
}

func (t *Tx) rawGet(id string) (Record, error) {
	var (
		typ    string
		ver    uint64
		mtime  int64
		del    int
		sealed []byte
	)
	err := t.c.QueryRowContext(t.ctx, `SELECT type,version,mtime,deleted,data FROM records WHERE id=?`, id).
		Scan(&typ, &ver, &mtime, &del, &sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, err
	}
	return t.open(id, typ, ver, mtime, del, sealed)
}

// List returns every non-deleted record of type typ.
func (t *Tx) List(typ string) ([]Record, error) {
	rows, err := t.c.QueryContext(t.ctx, `SELECT id,version,mtime,data FROM records WHERE type=? AND deleted=0 ORDER BY mtime,id`, typ)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		var (
			id     string
			ver    uint64
			mtime  int64
			sealed []byte
		)
		if err := rows.Scan(&id, &ver, &mtime, &sealed); err != nil {
			return nil, err
		}
		r, err := t.open(id, typ, ver, mtime, 0, sealed)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Put writes data as record id of type typ.
func (t *Tx) Put(typ, id string, data []byte) (Record, error) {
	if !t.write {
		return Record{}, ErrReadOnly
	}
	var ver uint64
	if id == "" {
		var err error
		if id, err = sealrow.NewID(); err != nil {
			return Record{}, err
		}
	} else {
		var old string
		err := t.c.QueryRowContext(t.ctx, `SELECT type,version FROM records WHERE id=?`, id).Scan(&old, &ver)
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return Record{}, err
		case old != typ:
			return Record{}, fmt.Errorf("sealdb: record %s is a %s, not a %s", id, old, typ)
		}
	}
	return t.write1(id, typ, ver+1, false, data)
}

// Delete marks record id deleted.
func (t *Tx) Delete(id string) error {
	if !t.write {
		return ErrReadOnly
	}
	var (
		typ string
		ver uint64
	)
	err := t.c.QueryRowContext(t.ctx, `SELECT type,version FROM records WHERE id=?`, id).Scan(&typ, &ver)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	_, err = t.write1(id, typ, ver+1, true, nil)
	return err
}

func (t *Tx) write1(id, typ string, ver uint64, del bool, data []byte) (Record, error) {
	now := time.Now()
	sealed, err := t.key.Seal(sealrow.Row{ID: id, Type: typ, Version: ver}, head(del, now.UnixNano(), data))
	if err != nil {
		return Record{}, err
	}
	d := 0
	if del {
		d = 1
	}
	_, err = t.c.ExecContext(t.ctx, `INSERT INTO records(id,type,version,mtime,deleted,data) VALUES(?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET version=excluded.version, mtime=excluded.mtime, deleted=excluded.deleted, data=excluded.data`,
		id, typ, ver, now.UnixNano(), d, sealed)
	if err != nil {
		return Record{}, err
	}
	return Record{ID: id, Type: typ, Version: ver, MTime: time.Unix(0, now.UnixNano()), Deleted: del, Data: data}, nil
}

// Meta returns one readable setting.
func (t *Tx) Meta(k string) (string, error) {
	var v string
	err := t.c.QueryRowContext(t.ctx, `SELECT v FROM meta WHERE k=?`, k).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetMeta sets one readable setting.
func (t *Tx) SetMeta(k, v string) error {
	if !t.write {
		return ErrReadOnly
	}
	if k == "format" {
		return errors.New("sealdb: the format is set by the store itself")
	}
	_, err := t.c.ExecContext(t.ctx, `INSERT INTO meta(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, k, v)
	return err
}

func (t *Tx) checkKey() error {
	_, err := t.rawGet(keycheckID)
	switch {
	case err == nil, errors.Is(err, ErrNotFound):
		return nil
	case errors.Is(err, sealrow.ErrOpen):
		return ErrKeyChanged
	}
	return err
}

// AppendAudit writes a sealed audit entry.
func (t *Tx) AppendAudit(payload []byte) error {
	if !t.write {
		return ErrReadOnly
	}
	id, err := sealrow.NewID()
	if err != nil {
		return err
	}
	now := time.Now().UnixNano()
	// provisional seq for sealing; rewrite after insert if needed — bind to id only
	sealed, err := t.key.Seal(sealrow.Row{ID: id, Type: "audit", Version: 1}, payload)
	if err != nil {
		return err
	}
	_, err = t.c.ExecContext(t.ctx, `INSERT INTO audit(id,time,data) VALUES(?,?,?)`, id, now, sealed)
	return err
}

// ListAudit returns recent audit payloads (newest last), up to limit.
func (t *Tx) ListAudit(limit int) ([][]byte, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := t.c.QueryContext(t.ctx, `SELECT id,data FROM audit ORDER BY seq DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][]byte
	for rows.Next() {
		var id string
		var sealed []byte
		if err := rows.Scan(&id, &sealed); err != nil {
			return nil, err
		}
		plain, err := t.key.Open(sealrow.Row{ID: id, Type: "audit", Version: 1}, sealed)
		if err != nil {
			return nil, err
		}
		out = append(out, plain)
	}
	// reverse to chronological
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, rows.Err()
}
