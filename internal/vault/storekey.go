package vault

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/crypto/scrypt"

	"github.com/sgnd/cuwan/internal/store"
)

func storeKeyFile() string { return store.Path("store.key") }

func storeMaster(meta map[string]string, create bool) ([]byte, map[string]string, error) {
	if create {
		return newStoreMaster()
	}
	switch meta["key_mode"] {
	case "passphrase":
		p := Passphrase()
		if p == "" {
			return nil, nil, ErrLocked
		}
		salt, err := hex.DecodeString(meta["salt"])
		n, nerr := strconv.Atoi(meta["scrypt_n"])
		if err != nil || len(salt) < 16 || nerr != nil || n < 2 {
			return nil, nil, fmt.Errorf("%w: passphrase settings damaged", ErrKey)
		}
		k, err := scrypt.Key([]byte(p), salt, n, 8, 1, keyLen)
		return k, nil, err
	case "keychain":
		k, err := keychainGet()
		if err != nil {
			return nil, nil, err
		}
		if len(k) < keyLen {
			return nil, nil, fmt.Errorf("%w: keychain item too short", ErrKey)
		}
		return k[:keyLen], nil, nil
	case "keyfile":
		kp := storeKeyFile()
		if err := store.RefuseWorldReadable(kp); err != nil && !errors.Is(err, os.ErrNotExist) {
			// if missing, fall through to read error
			if !errors.Is(err, os.ErrNotExist) {
				st, sterr := os.Stat(kp)
				if sterr == nil && st.Mode().Perm()&0o077 != 0 {
					return nil, nil, err
				}
			}
		}
		if st, err := os.Stat(kp); err == nil && st.Mode().Perm()&0o077 != 0 {
			return nil, nil, fmt.Errorf("%w: %s is group/world-readable", ErrKey, kp)
		}
		b, err := os.ReadFile(kp)
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, fmt.Errorf("%w: %s missing", ErrKey, kp)
		}
		if err != nil {
			return nil, nil, err
		}
		if len(b) < keyLen {
			return nil, nil, fmt.Errorf("%w: %s too short", ErrKey, kp)
		}
		return b[:keyLen], nil, nil
	}
	return nil, nil, fmt.Errorf("%w: unknown key mode %q", ErrKey, meta["key_mode"])
}

func newStoreMaster() ([]byte, map[string]string, error) {
	if p := Passphrase(); p != "" {
		if utf8Count(p) < MinPassphrase {
			return nil, nil, ErrShortPassphrase
		}
		salt := make([]byte, 16)
		if _, err := rand.Read(salt); err != nil {
			return nil, nil, err
		}
		k, err := scrypt.Key([]byte(p), salt, PassphraseN, 8, 1, keyLen)
		if err != nil {
			return nil, nil, err
		}
		return k, map[string]string{
			"key_mode": "passphrase",
			"salt":     hex.EncodeToString(salt),
			"scrypt_n": strconv.Itoa(PassphraseN),
		}, nil
	}
	if k, ok := keychainNewKey(); ok && len(k) >= keyLen {
		return k[:keyLen], map[string]string{"key_mode": "keychain"}, nil
	}
	if err := store.EnsureHome(); err != nil {
		return nil, nil, err
	}
	k, err := newKeyFile(storeKeyFile())
	if err != nil {
		return nil, nil, err
	}
	return k[:keyLen], map[string]string{"key_mode": "keyfile"}, nil
}

func utf8Count(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

func newKeyFile(kp string) ([]byte, error) {
	key := make([]byte, keyLen)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(kp), ".tmp-key-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(key); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if err := os.Link(tmp.Name(), kp); err == nil {
		return key, nil
	} else if !errors.Is(err, os.ErrExist) {
		f, err2 := os.OpenFile(kp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err2, os.ErrExist) {
			return os.ReadFile(kp)
		}
		if err2 != nil {
			return nil, err2
		}
		_, err2 = f.Write(key)
		if err2 == nil {
			err2 = f.Sync()
		}
		cerr := f.Close()
		if err2 != nil {
			return nil, err2
		}
		return key, cerr
	}
	return os.ReadFile(kp)
}
