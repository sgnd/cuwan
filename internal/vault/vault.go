// Package vault stores secrets (API keys, wallet keys) double-sealed:
// once as a sealdb row, again with a vault HKDF key bound to the secret name.
package vault

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/scrypt"

	"github.com/sgnd/cuwan/internal/sealdb"
	"github.com/sgnd/cuwan/internal/store"
)

const (
	secretType     = "secret"
	vaultHKDFLabel = "cuwan vault entries v1"
	keyLen         = 32
	MinPassphrase  = 8
)

// PassphraseN is scrypt N (OWASP minimum 2^17). Tests may lower it.
var PassphraseN = 1 << 17

var (
	ErrLocked          = errors.New("vault: locked (set CUWAN_VAULT_PASS or unlock keychain)")
	ErrKey             = errors.New("vault: key problem")
	ErrWrongKey        = errors.New("vault: wrong key or passphrase")
	ErrNotFound        = errors.New("vault: secret not found")
	ErrExists          = errors.New("vault: secret already exists")
	ErrDecrypt         = errors.New("vault: cannot decrypt secret")
	ErrShortPassphrase = errors.New("vault: passphrase too short")
	ErrNoPassphrase    = errors.New("vault: no passphrase")
)

type entry struct {
	Nonce     []byte    `json:"nonce"`
	Data      []byte    `json:"data"`
	UpdatedAt time.Time `json:"updated_at"`
	id        string
}

type sealedEntry struct {
	Name      string    `json:"name"`
	Nonce     []byte    `json:"nonce"`
	Data      []byte    `json:"data"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Info is public metadata for a secret.
type Info struct {
	Name      string    `json:"name"`
	UpdatedAt time.Time `json:"updated_at"`
}

var (
	passMu sync.Mutex
	pass   string
)

func init() { store.KeySource = storeMaster }

// Passphrase returns CUWAN_VAULT_PASS and removes it from the environment.
func Passphrase() string {
	passMu.Lock()
	defer passMu.Unlock()
	if p, ok := os.LookupEnv("CUWAN_VAULT_PASS"); ok {
		pass = p
		_ = os.Unsetenv("CUWAN_VAULT_PASS")
	}
	return pass
}

// UnlockVars stripped at start so children never inherit them.
var UnlockVars = []string{"CUWAN_VAULT_PASS", "CUWAN_KEYFILE"}

var unlock = map[string]string{}

// TakeUnlockEnv strips unlock env from the process.
func TakeUnlockEnv() {
	Passphrase()
	passMu.Lock()
	defer passMu.Unlock()
	for _, n := range UnlockVars {
		if n == "CUWAN_VAULT_PASS" {
			continue
		}
		if v, ok := os.LookupEnv(n); ok {
			unlock[n] = v
			_ = os.Unsetenv(n)
		}
	}
}

func vaultAEAD(master []byte) (cipher.AEAD, error) {
	k, err := hkdf.Key(sha256.New, master, nil, vaultHKDFLabel, 32)
	if err != nil {
		return nil, err
	}
	blk, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(blk)
}

func currentMaster() ([]byte, error) {
	meta, err := sealdb.Peek(context.Background(), store.DBPath())
	if errors.Is(err, os.ErrNotExist) {
		m, _, err := storeMaster(nil, true)
		return m, err
	}
	if err != nil {
		return nil, err
	}
	m, _, err := storeMaster(meta, false)
	return m, err
}

func sealValue(aead cipher.AEAD, name, value string) (nonce, data []byte, err error) {
	nonce = make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	data = aead.Seal(nil, nonce, []byte(value), []byte(name))
	return nonce, data, nil
}

func openValue(aead cipher.AEAD, name string, nonce, data []byte) (string, error) {
	pt, err := aead.Open(nil, nonce, data, []byte(name))
	if err != nil {
		return "", fmt.Errorf("%w %q", ErrDecrypt, name)
	}
	return string(pt), nil
}

// Set stores or overwrites a secret. value is never logged.
func Set(name, value string) error {
	name = strings.TrimSpace(name)
	if name == "" || strings.Contains(name, ":") {
		return errors.New("vault: invalid secret name")
	}
	if utf8.RuneCountInString(value) == 0 {
		return errors.New("vault: empty secret value")
	}
	master, err := currentMaster()
	if err != nil {
		return err
	}
	defer clear(master)
	aead, err := vaultAEAD(master)
	if err != nil {
		return err
	}
	nonce, data, err := sealValue(aead, name, value)
	if err != nil {
		return err
	}
	d, err := store.DB(true)
	if err != nil {
		return err
	}
	return d.Update(context.Background(), func(tx *sealdb.Tx) error {
		rows, err := tx.List(secretType)
		if err != nil {
			return err
		}
		id := ""
		for _, r := range rows {
			var e sealedEntry
			if err := json.Unmarshal(r.Data, &e); err != nil {
				return err
			}
			if e.Name == name {
				id = r.ID
				break
			}
		}
		b, err := json.Marshal(sealedEntry{
			Name: name, Nonce: nonce, Data: data, UpdatedAt: time.Now().UTC(),
		})
		if err != nil {
			return err
		}
		_, err = tx.Put(secretType, id, b)
		return err
	})
}

// Get returns the plaintext secret (caller must not print it in agent mode).
func Get(name string) (string, error) {
	master, err := currentMaster()
	if err != nil {
		return "", err
	}
	defer clear(master)
	aead, err := vaultAEAD(master)
	if err != nil {
		return "", err
	}
	d, err := store.DB(false)
	if err != nil {
		return "", err
	}
	var out string
	err = d.View(context.Background(), func(tx *sealdb.Tx) error {
		rows, err := tx.List(secretType)
		if err != nil {
			return err
		}
		for _, r := range rows {
			var e sealedEntry
			if err := json.Unmarshal(r.Data, &e); err != nil {
				return err
			}
			if e.Name == name {
				out, err = openValue(aead, name, e.Nonce, e.Data)
				return err
			}
		}
		return ErrNotFound
	})
	return out, err
}

// List returns secret names (no values).
func List() ([]Info, error) {
	d, err := store.DB(false)
	if errors.Is(err, store.ErrNoStore) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Info
	err = d.View(context.Background(), func(tx *sealdb.Tx) error {
		rows, err := tx.List(secretType)
		if err != nil {
			return err
		}
		for _, r := range rows {
			var e sealedEntry
			if err := json.Unmarshal(r.Data, &e); err != nil {
				return err
			}
			out = append(out, Info{Name: e.Name, UpdatedAt: e.UpdatedAt})
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, err
}

// Rename renames a secret (reseals under the new name).
func Rename(old, newName string) error {
	v, err := Get(old)
	if err != nil {
		return err
	}
	if _, err := Get(newName); err == nil {
		return ErrExists
	} else if !errors.Is(err, ErrNotFound) && !errors.Is(err, store.ErrNoStore) {
		return err
	}
	if err := Set(newName, v); err != nil {
		return err
	}
	return Delete(old)
}

// Delete removes a secret.
func Delete(name string) error {
	d, err := store.DB(false)
	if err != nil {
		return err
	}
	return d.Update(context.Background(), func(tx *sealdb.Tx) error {
		rows, err := tx.List(secretType)
		if err != nil {
			return err
		}
		for _, r := range rows {
			var e sealedEntry
			if err := json.Unmarshal(r.Data, &e); err != nil {
				return err
			}
			if e.Name == name {
				return tx.Delete(r.ID)
			}
		}
		return ErrNotFound
	})
}

// ParseRef returns the secret name from "secret:NAME", or ("", false).
func ParseRef(ref string) (string, bool) {
	if strings.HasPrefix(ref, "secret:") {
		n := strings.TrimPrefix(ref, "secret:")
		if n != "" {
			return n, true
		}
	}
	return "", false
}

// ResolveRef loads a secret:NAME reference.
func ResolveRef(ref string) (string, error) {
	name, ok := ParseRef(ref)
	if !ok {
		return "", fmt.Errorf("vault: not a secret: ref (%q)", ref)
	}
	return Get(name)
}

func clear(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// scryptKey is exported for tests that lower PassphraseN.
func scryptKey(pass string, salt []byte, n int) ([]byte, error) {
	return scrypt.Key([]byte(pass), salt, n, 8, 1, keyLen)
}
