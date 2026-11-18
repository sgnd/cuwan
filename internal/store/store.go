// Package store keeps cuwan inventory (exchanges, bots, settings, runtime)
// in the sealed store under $CUWAN_HOME (default ~/.cuwan).
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/sgnd/cuwan/internal/home"
	"github.com/sgnd/cuwan/internal/sealdb"
	"github.com/sgnd/cuwan/internal/sealrow"
)

var (
	ErrNoStore = errors.New("store: no sealed store yet; run cuwan doctor or set a secret first")
	ErrExists  = errors.New("store: already exists")
	ErrNotFound = errors.New("store: not found")
)

// KeySource loads or creates the master key. Set by package vault.
var KeySource func(meta map[string]string, create bool) ([]byte, map[string]string, error)

var (
	mu   sync.Mutex
	open *sealdb.DB
)

// EnsureHome creates ~/.cuwan with mode 0700.
func EnsureHome() error {
	_, err := home.Ensure()
	return err
}

// Path joins under CUWAN_HOME.
func Path(name string) string {
	d, err := home.Dir()
	if err != nil {
		return filepath.Join(".cuwan", name)
	}
	if name == "" {
		return d
	}
	return filepath.Join(d, name)
}

// DBPath is the sealed SQLite file.
func DBPath() string { return Path("store.db") }

// DB opens (or returns) the sealed store. create makes it when missing.
func DB(create bool) (*sealdb.DB, error) {
	mu.Lock()
	defer mu.Unlock()
	if open != nil {
		return open, nil
	}
	if err := EnsureHome(); err != nil {
		return nil, err
	}
	path := DBPath()
	meta, err := sealdb.Peek(context.Background(), path)
	if errors.Is(err, os.ErrNotExist) {
		if !create {
			return nil, ErrNoStore
		}
		if KeySource == nil {
			return nil, errors.New("store: no key source configured")
		}
		master, newMeta, err := KeySource(nil, true)
		if err != nil {
			return nil, err
		}
		defer clear(master)
		key, err := sealrow.NewKey(master)
		if err != nil {
			return nil, err
		}
		d, err := sealdb.Open(context.Background(), path, key)
		if err != nil {
			return nil, err
		}
		err = d.Update(context.Background(), func(tx *sealdb.Tx) error {
			for k, v := range newMeta {
				if err := tx.SetMeta(k, v); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			d.Close()
			return nil, err
		}
		open = d
		return d, nil
	}
	if err != nil {
		return nil, err
	}
	if KeySource == nil {
		return nil, errors.New("store: no key source configured")
	}
	master, _, err := KeySource(meta, false)
	if err != nil {
		return nil, err
	}
	defer clear(master)
	key, err := sealrow.NewKey(master)
	if err != nil {
		return nil, err
	}
	d, err := sealdb.Open(context.Background(), path, key)
	if err != nil {
		return nil, err
	}
	open = d
	return d, nil
}

// CloseDB closes a cached handle (tests).
func CloseDB() {
	mu.Lock()
	defer mu.Unlock()
	if open != nil {
		_ = open.Close()
		open = nil
	}
}

// ResetForTest closes and forgets the store handle.
func ResetForTest() { CloseDB() }

func clear(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// Rekey reseals the store under a new master and updates meta.
func Rekey(newMaster []byte, newMeta map[string]string, then func(*sealdb.Tx) error) error {
	d, err := DB(false)
	if err != nil {
		return err
	}
	newKey, err := sealrow.NewKey(newMaster)
	if err != nil {
		return err
	}
	return d.Rekey(context.Background(), newKey, func(tx *sealdb.Tx) error {
		for k, v := range newMeta {
			if err := tx.SetMeta(k, v); err != nil {
				return err
			}
		}
		if then != nil {
			return then(tx)
		}
		return nil
	})
}

// SetStoreMeta writes readable meta keys.
func SetStoreMeta(m map[string]string) error {
	d, err := DB(false)
	if err != nil {
		return err
	}
	return d.Update(context.Background(), func(tx *sealdb.Tx) error {
		for k, v := range m {
			if err := tx.SetMeta(k, v); err != nil {
				return err
			}
		}
		return nil
	})
}

// WriteFileAtomic writes path with mode, refusing world-readable modes.
func WriteFileAtomic(path string, data []byte, mode os.FileMode) error {
	if mode&0o077 != 0 {
		return fmt.Errorf("store: refusing world/group-readable mode %o", mode)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// RefuseWorldReadable fails if path is group/world readable.
func RefuseWorldReadable(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("store: %s is group/world-readable (%o); fix permissions", path, st.Mode().Perm())
	}
	return nil
}

// --- inventory types ---

const (
	TypeExchange = "exchange"
	TypeBot      = "bot"
	TypeRuntime  = "runtime"
	TypeSetting  = "setting"
)

// Exchange is a venue credential reference.
type Exchange struct {
	Name      string    `json:"name"`
	Venue     string    `json:"venue"` // registered id (cuwan exchange venues)
	Market    string    `json:"market"` // spot | usdm | perp
	SecretRef string    `json:"secret_ref"` // secret:NAME
	Testnet   bool      `json:"testnet,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	ID        string    `json:"-"`
}

// Bot is a grid bot config. Guardrails are always enforced by the engine.
type Bot struct {
	Name         string    `json:"name"`
	Exchange     string    `json:"exchange"`
	Symbol       string    `json:"symbol"`
	Lower        float64   `json:"lower"`
	Upper        float64   `json:"upper"`
	Levels       int       `json:"levels"`
	OrderSize    float64   `json:"order_size"`
	Leverage     float64   `json:"leverage,omitempty"` // futures; capped at 20
	QuoteAmount  float64   `json:"quote_amount,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	ID           string    `json:"-"`
}

// RuntimeState is sealed crash-recovery state for a running bot.
type RuntimeState struct {
	BotName   string            `json:"bot_name"`
	Mode      string            `json:"mode"` // paper | live
	Status    string            `json:"status"` // running | stopped
	Orders    []RuntimeOrder    `json:"orders,omitempty"`
	StartedAt time.Time         `json:"started_at"`
	UpdatedAt time.Time         `json:"updated_at"`
	Meta      map[string]string `json:"meta,omitempty"`
	ID        string            `json:"-"`
}

// RuntimeOrder is one tracked order for restart reconcile.
type RuntimeOrder struct {
	ClientOID string  `json:"client_oid"`
	ExchangeID string `json:"exchange_id,omitempty"`
	Side      string  `json:"side"`
	Price     float64 `json:"price"`
	Qty       float64 `json:"qty"`
	Status    string  `json:"status"`
}

func putJSON(typ, id string, v any) (string, error) {
	d, err := DB(true)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	var outID string
	err = d.Update(context.Background(), func(tx *sealdb.Tx) error {
		r, err := tx.Put(typ, id, b)
		outID = r.ID
		return err
	})
	return outID, err
}

func listJSON[T any](typ string, bind func(*T, sealdb.Record)) ([]T, error) {
	d, err := DB(false)
	if err != nil {
		return nil, err
	}
	var out []T
	err = d.View(context.Background(), func(tx *sealdb.Tx) error {
		rows, err := tx.List(typ)
		if err != nil {
			return err
		}
		for _, r := range rows {
			var v T
			if err := json.Unmarshal(r.Data, &v); err != nil {
				return err
			}
			bind(&v, r)
			out = append(out, v)
		}
		return nil
	})
	return out, err
}

// PutExchange stores or updates an exchange.
func PutExchange(e *Exchange) error {
	now := time.Now().UTC()
	if e.CreatedAt.IsZero() {
		e.CreatedAt = now
	}
	e.UpdatedAt = now
	existing, _ := GetExchange(e.Name)
	id := ""
	if existing != nil {
		id = existing.ID
	}
	outID, err := putJSON(TypeExchange, id, e)
	e.ID = outID
	return err
}

// GetExchange by name.
func GetExchange(name string) (*Exchange, error) {
	all, err := ListExchanges()
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].Name == name {
			return &all[i], nil
		}
	}
	return nil, ErrNotFound
}

// ListExchanges returns all exchanges.
func ListExchanges() ([]Exchange, error) {
	return listJSON(TypeExchange, func(e *Exchange, r sealdb.Record) { e.ID = r.ID })
}

// PutBot stores or updates a bot.
func PutBot(b *Bot) error {
	now := time.Now().UTC()
	if b.CreatedAt.IsZero() {
		b.CreatedAt = now
	}
	b.UpdatedAt = now
	existing, _ := GetBot(b.Name)
	id := ""
	if existing != nil {
		id = existing.ID
	}
	outID, err := putJSON(TypeBot, id, b)
	b.ID = outID
	return err
}

// GetBot by name.
func GetBot(name string) (*Bot, error) {
	all, err := ListBots()
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].Name == name {
			return &all[i], nil
		}
	}
	return nil, ErrNotFound
}

// ListBots returns all bots.
func ListBots() ([]Bot, error) {
	return listJSON(TypeBot, func(b *Bot, r sealdb.Record) { b.ID = r.ID })
}

// DeleteBot removes a bot by name.
func DeleteBot(name string) error {
	b, err := GetBot(name)
	if err != nil {
		return err
	}
	d, err := DB(false)
	if err != nil {
		return err
	}
	return d.Update(context.Background(), func(tx *sealdb.Tx) error {
		return tx.Delete(b.ID)
	})
}

// PutRuntime saves sealed runtime state.
func PutRuntime(rt *RuntimeState) error {
	now := time.Now().UTC()
	if rt.StartedAt.IsZero() {
		rt.StartedAt = now
	}
	rt.UpdatedAt = now
	existing, _ := GetRuntime(rt.BotName)
	id := ""
	if existing != nil {
		id = existing.ID
	}
	outID, err := putJSON(TypeRuntime, id, rt)
	rt.ID = outID
	return err
}

// GetRuntime by bot name.
func GetRuntime(botName string) (*RuntimeState, error) {
	all, err := ListRuntimes()
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].BotName == botName {
			return &all[i], nil
		}
	}
	return nil, ErrNotFound
}

// ListRuntimes returns all runtime states.
func ListRuntimes() ([]RuntimeState, error) {
	return listJSON(TypeRuntime, func(rt *RuntimeState, r sealdb.Record) { rt.ID = r.ID })
}

// AppendAudit writes a local action log entry.
func AppendAudit(action string, detail map[string]any) error {
	d, err := DB(true)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{
		"time":   time.Now().UTC(),
		"action": action,
		"detail": detail,
	})
	if err != nil {
		return err
	}
	return d.Update(context.Background(), func(tx *sealdb.Tx) error {
		return tx.AppendAudit(payload)
	})
}

// ListAudit returns recent audit entries as raw JSON.
func ListAudit(limit int) ([]json.RawMessage, error) {
	d, err := DB(false)
	if err != nil {
		return nil, err
	}
	var out []json.RawMessage
	err = d.View(context.Background(), func(tx *sealdb.Tx) error {
		rows, err := tx.ListAudit(limit)
		if err != nil {
			return err
		}
		for _, r := range rows {
			out = append(out, json.RawMessage(r))
		}
		return nil
	})
	return out, err
}
