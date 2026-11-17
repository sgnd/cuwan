package vault

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/sgnd/cuwan/internal/store"
)

func keychainService() string {
	h := sha256.Sum256([]byte(store.Path("")))
	return "cuwan vault " + hex.EncodeToString(h[:6])
}

const keychainAccount = "cuwan"

var keychainOS = runtime.GOOS

func keychainOff() bool {
	v := os.Getenv("CUWAN_KEYCHAIN")
	return v == "off" || v == "0" || v == "false"
}

var newKeyTimeout = 5 * time.Second

func keychainNewKey() ([]byte, bool) {
	if keychainOff() {
		return nil, false
	}
	key := make([]byte, keyLen)
	if _, err := rand.Read(key); err != nil {
		return nil, false
	}
	if err := keychainSet(key); err != nil {
		return nil, false
	}
	return key, true
}

func keychainGet() ([]byte, error) {
	if keychainOff() {
		return nil, fmt.Errorf("%w: keychain disabled (CUWAN_KEYCHAIN=off)", ErrKey)
	}
	switch keychainOS {
	case "darwin":
		cmd := exec.Command("/usr/bin/security", "find-generic-password", "-s", keychainService(), "-a", keychainAccount, "-w")
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("%w: keychain get: %v", ErrKey, err)
		}
		raw, err := hex.DecodeString(strings.TrimSpace(string(out)))
		if err != nil {
			// security -w may return the password as UTF-8 hex already, or raw
			b := bytes.TrimSpace(out)
			if len(b) >= keyLen {
				return b[:keyLen], nil
			}
			return nil, fmt.Errorf("%w: keychain value malformed", ErrKey)
		}
		if len(raw) < keyLen {
			return nil, fmt.Errorf("%w: keychain value too short", ErrKey)
		}
		return raw[:keyLen], nil
	default:
		return nil, errors.New("vault: keychain not available on this OS; use keyfile or passphrase")
	}
}

func keychainSet(key []byte) error {
	if keychainOff() {
		return errors.New("vault: keychain disabled")
	}
	switch keychainOS {
	case "darwin":
		hexKey := hex.EncodeToString(key)
		_ = exec.Command("/usr/bin/security", "delete-generic-password", "-s", keychainService(), "-a", keychainAccount).Run()
		cmd := exec.Command("/usr/bin/security", "-i")
		cmd.Stdin = strings.NewReader(fmt.Sprintf(
			"add-generic-password -U -s %q -a %q -w %s\n",
			keychainService(), keychainAccount, hexKey,
		))
		done := make(chan error, 1)
		go func() { done <- cmd.Run() }()
		select {
		case err := <-done:
			return err
		case <-time.After(newKeyTimeout):
			_ = cmd.Process.Kill()
			return errors.New("vault: keychain timed out")
		}
	default:
		return errors.New("vault: keychain not available")
	}
}
