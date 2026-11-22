//go:build unix

package live

import (
	"errors"
	"net/url"
	"os"
	"syscall"

	"path/filepath"
)

// ErrLocked means another process holds the bot's live lock.
var ErrLocked = errors.New("live: another process is running this bot")

// Lock is one bot's live lock. Only its holder may place or cancel orders
// for that bot. The OS drops it when the process dies.
type Lock struct{ f *os.File }

// TryLock takes the bot's lock in dir without waiting.
func TryLock(dir, bot string) (*Lock, error) {
	p := filepath.Join(dir, "run-"+url.PathEscape(bot)+".lock")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304 -- the cuwan folder plus an escaped bot name
	if err != nil {
		return nil, err
	}
	fd := int(f.Fd()) // #nosec G115 -- a file descriptor fits an int
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return &Lock{f: f}, nil
}

// Unlock releases the lock.
func (l *Lock) Unlock() {
	if l == nil || l.f == nil {
		return
	}
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN) // #nosec G115 -- a file descriptor fits an int
	_ = l.f.Close()
	l.f = nil
}

func stopPath(dir, bot string) string {
	return filepath.Join(dir, "run-"+url.PathEscape(bot)+".stop")
}

// RequestStop asks the process holding the lock to stop the bot.
func RequestStop(dir, bot string) error {
	return os.WriteFile(stopPath(dir, bot), []byte("stop\n"), 0o600)
}

// StopRequested reports whether a stop was requested.
func StopRequested(dir, bot string) bool {
	_, err := os.Stat(stopPath(dir, bot))
	return err == nil
}

// ClearStop removes a stop request.
func ClearStop(dir, bot string) { _ = os.Remove(stopPath(dir, bot)) }
