//go:build !unix

package live

import "errors"

// ErrLocked means another process holds the bot's live lock.
var ErrLocked = errors.New("live: another process is running this bot")

// Lock is not available here; live runs need a unix system.
type Lock struct{}

// TryLock always fails: live runs need a unix system.
func TryLock(string, string) (*Lock, error) {
	return nil, errors.New("live: live runs need macOS or Linux")
}

// Unlock does nothing.
func (l *Lock) Unlock() {}

// RequestStop always fails here.
func RequestStop(string, string) error { return errors.New("live: live runs need macOS or Linux") }

// StopRequested is always false here.
func StopRequested(string, string) bool { return false }

// ClearStop does nothing.
func ClearStop(string, string) {}
