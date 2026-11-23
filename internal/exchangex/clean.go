package exchangex

import (
	"errors"
	"net/url"
	"strings"
)

// CleanErr makes an HTTP error safe to show: a *url.Error keeps only the
// method, host and path (Go prints the whole URL, query included), and
// every secret given is replaced with "[redacted]". Drivers call it on
// every error that leaves an HTTP call.
func CleanErr(err error, secrets ...string) error {
	if err == nil {
		return nil
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		where := "request"
		if u, perr := url.Parse(ue.URL); perr == nil {
			where = u.Scheme + "://" + u.Host + u.Path
		}
		inner := ue.Err
		if inner == nil {
			inner = errors.New("failed")
		}
		return &cleaned{msg: ue.Op + " " + where + ": " + Redact(inner.Error(), secrets...)}
	}
	var keep error // only the sentinel survives, never the raw error text
	if errors.Is(err, ErrOrderNotFound) {
		keep = ErrOrderNotFound
	}
	return &cleaned{msg: Redact(err.Error(), secrets...), err: keep}
}

// Redact replaces every non-empty secret in s, and any signature or
// timestamp query, with "[redacted]".
func Redact(s string, secrets ...string) string {
	for _, sec := range secrets {
		if len(sec) >= 4 {
			s = strings.ReplaceAll(s, sec, "[redacted]")
		}
	}
	for _, k := range []string{"signature=", "timestamp="} {
		for {
			i := strings.Index(s, k)
			if i < 0 {
				break
			}
			j := i + len(k)
			for j < len(s) && s[j] != '&' && s[j] != ' ' && s[j] != '"' {
				j++
			}
			s = s[:i] + "[redacted]" + s[j:]
		}
	}
	return s
}

// cleaned keeps the error chain (for errors.Is) but prints a safe text.
// It wraps only ErrOrderNotFound, so nothing can unwrap to the raw text.
type cleaned struct {
	msg string
	err error
}

func (c *cleaned) Error() string { return c.msg }
func (c *cleaned) Unwrap() error { return c.err }
