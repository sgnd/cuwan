//go:build unix

package live

import (
	"errors"
	"net/url"
	"os"
	"syscall"

	"github.com/sgnd/cuwan/internal/home"
)

// ErrLocked means another process holds the bot's live lock.
var ErrLocked = errors.New("live: another process is running this bot")

// Lock is one bot's live lock. Only its holder may place or cancel orders
// for that bot. The OS drops it when the process dies.
type Lock struct{ f *os.File }

// TryLock takes the bot's lock without waiting.
func TryLock(bot string) (*Lock, error) {
	if _, err := home.Ensure(); err != nil {
		return nil, err
	}
	p, err := home.Path("run-" + url.PathEscape(bot) + ".lock")
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
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
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	_ = l.f.Close()
	l.f = nil
}

func stopPath(bot string) (string, error) {
	return home.Path("run-" + url.PathEscape(bot) + ".stop")
}

// RequestStop asks the process holding the lock to stop the bot.
func RequestStop(bot string) error {
	p, err := stopPath(bot)
	if err != nil {
		return err
	}
	return os.WriteFile(p, []byte("stop\n"), 0o600)
}

// StopRequested reports whether a stop was requested.
func StopRequested(bot string) bool {
	p, err := stopPath(bot)
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// ClearStop removes a stop request.
func ClearStop(bot string) {
	if p, err := stopPath(bot); err == nil {
		_ = os.Remove(p)
	}
}
