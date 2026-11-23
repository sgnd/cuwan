// Package store keeps cuwan's whole local state in one sealed file.
//
// The file is an 8-byte generation followed by the state sealed with
// AES-256-GCM (sealrow), bound to that generation. Every save raises the
// generation, so a changed generation does not open. Audit entries are
// chained by SHA-256, so a changed or dropped entry is found. A lock file
// makes every read-change-write one step across processes.
//
// Not covered: someone who can write the folder may put back a whole older
// file. That file opens, at its older generation.
package store

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/sgnd/cuwan/internal/sealrow"
)

const (
	fileName = "store.sealed"
	lockName = "store.lock"
	genLen   = 8
)

var (
	// ErrOpen means the store does not open: wrong key, damaged, or changed.
	ErrOpen = errors.New("store: the store does not open (wrong key, damaged, or changed)")
	// ErrTampered means the audit chain is broken.
	ErrTampered = errors.New("store: the audit log was changed")
)

// State is everything cuwan keeps. Maps are keyed by name, so a name is
// stored once.
type State struct {
	Gen       uint64              `json:"gen"`
	Secrets   map[string]string   `json:"secrets"`
	Exchanges map[string]Exchange `json:"exchanges"`
	Bots      map[string]Bot      `json:"bots"`
	Runtimes  map[string]Runtime  `json:"runtimes"` // by bot name: one per bot
	Audit     []Entry             `json:"audit"`
}

// Exchange is one venue account.
type Exchange struct {
	Name      string    `json:"name"`
	Venue     string    `json:"venue"`  // binance | hyperliquid
	Market    string    `json:"market"` // spot | usdm | perp
	Secret    string    `json:"secret"` // name of the secret holding the key
	Testnet   bool      `json:"testnet"`
	CreatedAt time.Time `json:"created_at"`
}

// Bot is one grid bot.
type Bot struct {
	Name        string  `json:"name"`
	Exchange    string  `json:"exchange"`
	Symbol      string  `json:"symbol"`
	Lower       float64 `json:"lower"`
	Upper       float64 `json:"upper"`
	Levels      int     `json:"levels"`
	OrderSize   float64 `json:"order_size,omitempty"`   // base asset per order
	QuoteAmount float64 `json:"quote_amount,omitempty"` // quote asset per order
	Leverage    float64 `json:"leverage,omitempty"`     // futures only
	OnExit      string  `json:"on_exit,omitempty"`      // stop | pause | trail
	TakeProfit  float64 `json:"take_profit,omitempty"`
	StopLoss    float64 `json:"stop_loss,omitempty"`
}

// Runtime is a live bot's saved state, for restart and stop.
type Runtime struct {
	Bot       string            `json:"bot"`
	Status    string            `json:"status"` // running | unguarded | halted | stopped
	Orders    []Order           `json:"orders,omitempty"`
	StartedAt time.Time         `json:"started_at"`
	Meta      map[string]string `json:"meta,omitempty"`
}

// Order is one order a bot sent or is about to send.
type Order struct {
	ClientOID  string    `json:"client_oid"`
	ExchangeID string    `json:"exchange_id,omitempty"`
	Side       string    `json:"side"`
	Price      float64   `json:"price"`
	Qty        float64   `json:"qty"`
	Filled     float64   `json:"filled,omitempty"`
	ReduceOnly bool      `json:"reduce_only,omitempty"`
	Close      bool      `json:"close,omitempty"` // a flatten order, not a grid order
	Status     string    `json:"status"`
	SentAt     time.Time `json:"sent_at"`
	Misses     int       `json:"misses,omitempty"`
	Parent     string    `json:"parent,omitempty"`
	Countered  bool      `json:"countered,omitempty"`
}

// Entry is one audit entry. Sum covers Seq, Time, Data and the previous Sum.
type Entry struct {
	Seq  int64           `json:"seq"`
	Time time.Time       `json:"time"`
	Data json.RawMessage `json:"data"`
	Sum  []byte          `json:"sum"`
}

// Store is the sealed file in one folder.
type Store struct {
	dir string
	key *sealrow.Key
}

// Open uses dir (made private, 0700) and the 32-byte master key.
func Open(dir string, master []byte) (*Store, error) {
	key, err := sealrow.NewKey(master)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil { // #nosec G302 -- a folder needs the x bit; 0700 is owner only
		return nil, err
	}
	return &Store{dir: dir, key: key}, nil
}

// Exists reports whether the store file is there.
func Exists(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, fileName))
	return err == nil
}

// View reads the state.
func (s *Store) View(fn func(*State) error) error {
	unlock, err := lock(filepath.Join(s.dir, lockName))
	if err != nil {
		return err
	}
	defer unlock()
	st, err := s.load()
	if err != nil {
		return err
	}
	return fn(st)
}

// Update reads the state, lets fn change it, and saves it at the next
// generation, all under the lock. Nothing is saved when fn fails.
func (s *Store) Update(fn func(*State) error) error {
	unlock, err := lock(filepath.Join(s.dir, lockName))
	if err != nil {
		return err
	}
	defer unlock()
	st, err := s.load()
	if err != nil {
		return err
	}
	if err := fn(st); err != nil {
		return err
	}
	st.Gen++
	return s.save(st)
}

func row(gen uint64) sealrow.Row {
	return sealrow.Row{ID: "cuwan", Type: "state", Version: gen}
}

func (s *Store) load() (*State, error) {
	b, err := os.ReadFile(filepath.Join(s.dir, fileName))
	if errors.Is(err, os.ErrNotExist) {
		return fill(&State{}), nil
	}
	if err != nil {
		return nil, err
	}
	if len(b) < genLen {
		return nil, ErrOpen
	}
	gen := binary.BigEndian.Uint64(b[:genLen])
	plain, err := s.key.Open(row(gen), b[genLen:])
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOpen, err)
	}
	st := &State{}
	if err := json.Unmarshal(plain, st); err != nil || st.Gen != gen {
		return nil, ErrOpen
	}
	if err := VerifyAudit(st.Audit); err != nil {
		return nil, err
	}
	return fill(st), nil
}

func fill(st *State) *State {
	if st.Secrets == nil {
		st.Secrets = map[string]string{}
	}
	if st.Exchanges == nil {
		st.Exchanges = map[string]Exchange{}
	}
	if st.Bots == nil {
		st.Bots = map[string]Bot{}
	}
	if st.Runtimes == nil {
		st.Runtimes = map[string]Runtime{}
	}
	return st
}

func (s *Store) save(st *State) error {
	plain, err := json.Marshal(st)
	if err != nil {
		return err
	}
	sealed, err := s.key.Seal(row(st.Gen), plain)
	if err != nil {
		return err
	}
	out := make([]byte, genLen, genLen+len(sealed))
	binary.BigEndian.PutUint64(out, st.Gen)
	out = append(out, sealed...)
	tmp, err := os.CreateTemp(s.dir, ".store-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after the rename
	if _, err := tmp.Write(out); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(s.dir, fileName))
}

// Append adds an audit entry chained to the one before it.
func (st *State) Append(data any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	var prev []byte
	if n := len(st.Audit); n > 0 {
		prev = st.Audit[n-1].Sum
	}
	e := Entry{Seq: int64(len(st.Audit)) + 1, Time: time.Now().UTC(), Data: b}
	e.Sum = sum(prev, e)
	st.Audit = append(st.Audit, e)
	return nil
}

func sum(prev []byte, e Entry) []byte {
	h := sha256.New()
	h.Write(prev)
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(e.Seq)) // #nosec G115 -- Seq starts at 1 and only grows
	h.Write(n[:])
	h.Write([]byte(e.Time.UTC().Format(time.RFC3339Nano)))
	h.Write(e.Data)
	return h.Sum(nil)
}

// VerifyAudit checks the numbering and the chain of entries.
func VerifyAudit(entries []Entry) error {
	var prev []byte
	for i, e := range entries {
		if e.Seq != int64(i)+1 || string(sum(prev, e)) != string(e.Sum) {
			return fmt.Errorf("%w at entry %d", ErrTampered, i+1)
		}
		prev = e.Sum
	}
	return nil
}
