//go:build !windows

package store

import (
	"os"
	"syscall"
)

// lock holds an exclusive lock on path until the returned func is called.
func lock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600) // #nosec G304 -- path is the store folder plus a fixed name
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil { // #nosec G115 -- a file descriptor fits an int
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) // #nosec G115 -- a file descriptor fits an int
		_ = f.Close()
	}, nil
}
