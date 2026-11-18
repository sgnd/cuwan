// Package alert tells a person about a bot event on the channels they enabled:
// a desktop notification and an optional HTTPS webhook.
//
// A failed notification never stops a bot. Notify writes the failure to the
// audit log and returns nothing.
package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/sgnd/cuwan/internal/sealdb"
	"github.com/sgnd/cuwan/internal/store"
	"github.com/sgnd/cuwan/internal/vault"
)

// Event kinds.
const (
	KindFill      = "fill"
	KindGuard     = "guard"
	KindHalt      = "halt"
	KindUnguarded = "unguarded"
	KindStop      = "stop"
)

// Kinds lists every event kind in a stable order.
var Kinds = []string{KindFill, KindGuard, KindHalt, KindUnguarded, KindStop}

// settingID is the one sealed record that holds the alert settings.
const settingID = "setting-alert"

// Event is one thing a person should hear about.
type Event struct {
	Bot     string    `json:"bot"`
	Kind    string    `json:"kind"`
	Message string    `json:"message"`
	Time    time.Time `json:"time"`
}

// Settings say where alerts go and for which kinds.
type Settings struct {
	Desktop bool `json:"desktop"`
	// Webhook is a secret:NAME reference, because webhook URLs often carry tokens.
	Webhook string   `json:"webhook,omitempty"`
	On      []string `json:"on"`
}

// Defaults: desktop on for events that need a person now; fills stay quiet.
func Defaults() Settings {
	return Settings{Desktop: true, On: []string{KindGuard, KindHalt, KindUnguarded}}
}

// Wants reports whether kind is enabled.
func (s Settings) Wants(kind string) bool {
	for _, k := range s.On {
		if k == kind {
			return true
		}
	}
	return false
}

// ParseKinds checks a comma list such as "guard,halt".
func ParseKinds(list string) ([]string, error) {
	var outKinds []string
	for _, k := range strings.Split(list, ",") {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if !validKind(k) {
			return nil, fmt.Errorf("unknown alert kind %q (use %s)", k, strings.Join(Kinds, ","))
		}
		outKinds = append(outKinds, k)
	}
	return outKinds, nil
}

func validKind(k string) bool {
	for _, v := range Kinds {
		if v == k {
			return true
		}
	}
	return false
}

// Load returns the saved settings, or Defaults when none are saved.
func Load() (Settings, error) {
	d, err := store.DB(false)
	if err != nil {
		return Defaults(), nil // no store yet means nothing was saved
	}
	var s Settings
	found := false
	err = d.View(context.Background(), func(tx *sealdb.Tx) error {
		r, err := tx.Get(settingID)
		if errors.Is(err, sealdb.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		found = !r.Deleted
		return json.Unmarshal(r.Data, &s)
	})
	if err != nil {
		return Defaults(), err
	}
	if !found {
		return Defaults(), nil
	}
	return s, nil
}

// Save writes the settings to the sealed store.
func Save(s Settings) error {
	if s.Webhook != "" {
		if _, ok := vault.ParseRef(s.Webhook); !ok {
			return fmt.Errorf("webhook must be a secret:NAME reference")
		}
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	d, err := store.DB(true)
	if err != nil {
		return err
	}
	return d.Update(context.Background(), func(tx *sealdb.Tx) error {
		_, err := tx.Put(store.TypeSetting, settingID, b)
		return err
	})
}

// Desktop shows a desktop notification. Tests replace it.
var Desktop = desktop

// Client sends webhooks. Tests replace it to trust a test server.
var Client = &http.Client{Timeout: 5 * time.Second}

// Notify sends ev to each enabled channel and audits any failure.
func Notify(ev Event) {
	s, err := Load()
	if err != nil {
		audit(ev, "settings", err)
		return
	}
	if !s.Wants(ev.Kind) {
		return
	}
	_ = Send(s, ev)
}

// Send delivers ev on every enabled channel, ignoring the kind filter.
// Failures go to the audit log; the joined error is for `cuwan alert test`.
func Send(s Settings, ev Event) error {
	if ev.Time.IsZero() {
		ev.Time = time.Now().UTC()
	}
	var errs []error
	if s.Desktop {
		if err := Desktop("cuwan: "+ev.Bot, ev.Message); err != nil {
			audit(ev, "desktop", err)
			errs = append(errs, fmt.Errorf("desktop: %w", err))
		}
	}
	if s.Webhook != "" {
		if err := webhook(s.Webhook, ev); err != nil {
			audit(ev, "webhook", err)
			errs = append(errs, fmt.Errorf("webhook: %w", err))
		}
	}
	return errors.Join(errs...)
}

func audit(ev Event, channel string, err error) {
	_ = store.AppendAudit("alert.failed", map[string]any{
		"bot": ev.Bot, "kind": ev.Kind, "channel": channel, "error": err.Error(),
	})
}

func webhook(ref string, ev Event) error {
	raw, err := vault.ResolveRef(ref)
	if err != nil {
		return err
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" {
		// Never echo the URL: it may hold a token.
		return errors.New("the stored webhook is not an https URL")
	}
	body, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return errors.New("could not build the webhook request")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := Client.Do(req)
	if err != nil {
		// url.Error repeats the full URL; keep only the cause.
		var ue *url.Error
		if errors.As(err, &ue) {
			return ue.Err
		}
		return err
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("webhook answered %s", resp.Status)
	}
	return nil
}

// desktop passes the text as arguments, never as shell or script source.
func desktop(title, msg string) error {
	switch runtime.GOOS {
	case "darwin":
		p, err := exec.LookPath("osascript")
		if err != nil {
			return nil
		}
		script := `on run argv
display notification (item 2 of argv) with title (item 1 of argv)
end run`
		return exec.Command(p, "-e", script, title, msg).Run()
	case "linux":
		p, err := exec.LookPath("notify-send")
		if err != nil {
			return nil
		}
		return exec.Command(p, "--", title, msg).Run()
	}
	return nil
}
