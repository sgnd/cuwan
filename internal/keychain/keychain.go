// Package keychain keeps cuwan's 256-bit store key in the system key store:
// the macOS keychain or the Secret Service on Linux (secret-tool). The key
// is generated, never chosen by a person, and never written to a file. When
// the key store fails, cuwan stops.
package keychain

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

const (
	service = "cuwan"
	account = "store"
	keyLen  = 32
)

// run runs a key store tool with stdin; tests replace it.
var run = func(stdin, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...) // #nosec G204 -- fixed tool names and arguments
	cmd.Stdin = strings.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// Key returns the store key. With create it makes and saves a new one when
// there is none.
func Key(create bool) ([]byte, error) {
	return key(runtime.GOOS, create)
}

func key(goos string, create bool) ([]byte, error) {
	get, put, err := tools(goos)
	if err != nil {
		return nil, err
	}
	k, gerr := get()
	if gerr == nil || !create {
		return k, gerr
	}
	nk := make([]byte, keyLen)
	if _, err := rand.Read(nk); err != nil {
		return nil, err
	}
	if err := put(hex.EncodeToString(nk)); err != nil {
		return nil, fmt.Errorf("keychain: cannot save the store key (%v); cuwan does not fall back to a key file", err)
	}
	return get() // read back what the key store holds
}

func tools(goos string) (get func() ([]byte, error), put func(string) error, err error) {
	switch goos {
	case "darwin":
		get = func() ([]byte, error) {
			return parse(run("", "security", "find-generic-password", "-s", service, "-a", account, "-w"))
		}
		// security -i reads the command from stdin, so the key is not on a command line.
		put = func(h string) error {
			// -T "" trusts no app, so every read of the key asks the person.
			_, err := run(fmt.Sprintf("add-generic-password -s %s -a %s -T \"\" -w %s\n", service, account, h), "security", "-i")
			return err
		}
	case "linux":
		get = func() ([]byte, error) {
			return parse(run("", "secret-tool", "lookup", "service", service, "account", account))
		}
		put = func(h string) error {
			_, err := run(h, "secret-tool", "store", "--label", "cuwan store key", "service", service, "account", account)
			return err
		}
	default:
		return nil, nil, fmt.Errorf("keychain: cuwan keeps its key in the macOS keychain or the Linux Secret Service; %s is not supported yet", goos)
	}
	return get, put, nil
}

func parse(out []byte, err error) ([]byte, error) {
	if err != nil {
		return nil, fmt.Errorf("keychain: no store key: %w", err)
	}
	k, derr := hex.DecodeString(strings.TrimSpace(string(out)))
	if derr != nil || len(k) != keyLen {
		return nil, errors.New("keychain: the stored item is not a 256-bit key")
	}
	return k, nil
}
