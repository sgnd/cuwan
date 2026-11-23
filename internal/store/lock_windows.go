//go:build windows

package store

import (
	"errors"
	"os"
	"time"
)

// lock holds path, created exclusively, until the returned func is called.
// A lock file left by a crashed process older than ten minutes is taken over.
func lock(path string) (func(), error) {
	for start := time.Now(); ; {
		f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- fixed name in the store folder
		if err == nil {
			_ = f.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if fi, serr := os.Stat(path); serr == nil && time.Since(fi.ModTime()) > 10*time.Minute {
			_ = os.Remove(path)
			continue
		}
		if time.Since(start) > time.Minute {
			return nil, errors.New("store: another cuwan holds the store for over a minute")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
