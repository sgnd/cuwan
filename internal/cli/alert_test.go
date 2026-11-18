package cli

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	alertpkg "github.com/sgnd/cuwan/internal/alert"
	"github.com/sgnd/cuwan/internal/store"
)

type sent struct{ title, msg string }

var desktopSent []sent
var desktopErr error

// TestMain swaps the desktop sender so no test shows a real notification.
func TestMain(m *testing.M) {
	alertpkg.Desktop = func(title, msg string) error {
		desktopSent = append(desktopSent, sent{title, msg})
		return desktopErr
	}
	os.Exit(m.Run())
}

func resetDesktop(t *testing.T) {
	desktopSent, desktopErr = nil, nil
	t.Cleanup(func() { desktopSent, desktopErr = nil, nil })
}

// hookServer records webhook bodies and makes alertpkg.Client trust it.
func hookServer(t *testing.T, status int) (*httptest.Server, *[][]byte) {
	var bodies [][]byte
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if r.Method == http.MethodPost && r.Header.Get("Content-Type") == "application/json" {
			bodies = append(bodies, b)
		}
		w.WriteHeader(status)
	}))
	prev := alertpkg.Client
	alertpkg.Client = srv.Client()
	t.Cleanup(func() { srv.Close(); alertpkg.Client = prev })
	return srv, &bodies
}

func auditText(t *testing.T) string {
	rows, err := store.ListAudit(100)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, r := range rows {
		b.Write(r)
	}
	return b.String()
}

func TestAlertDefaults(t *testing.T) {
	setup(t, nil)
	_, env := call(t, "alert", "show")
	b, _ := json.Marshal(env.Data)
	if !env.OK || !strings.Contains(string(b), `"desktop":true`) || !strings.Contains(string(b), `"on":["guard","halt","unguarded"]`) {
		t.Fatalf("defaults: %s", b)
	}
}

func TestAlertFilterAndDesktop(t *testing.T) {
	setup(t, nil)
	resetDesktop(t)
	alertpkg.Notify(alertpkg.Event{Bot: "g", Kind: alertpkg.KindFill, Message: "1 order(s) filled"})
	if len(desktopSent) != 0 {
		t.Fatalf("fills are off by default, got %v", desktopSent)
	}
	alertpkg.Notify(alertpkg.Event{Bot: "g", Kind: alertpkg.KindHalt, Message: "HALTED"})
	if len(desktopSent) != 1 || desktopSent[0].title != "cuwan: g" || desktopSent[0].msg != "HALTED" {
		t.Fatalf("halt: %v", desktopSent)
	}
	if code, _ := call(t, "alert", "set", "--on", "fill", "--desktop", "on"); code != 0 {
		t.Fatal("set failed")
	}
	alertpkg.Notify(alertpkg.Event{Bot: "g", Kind: alertpkg.KindHalt, Message: "HALTED"})
	alertpkg.Notify(alertpkg.Event{Bot: "g", Kind: alertpkg.KindFill, Message: "filled"})
	if len(desktopSent) != 2 || desktopSent[1].msg != "filled" {
		t.Fatalf("after --on fill: %v", desktopSent)
	}
	if code, _ := call(t, "alert", "set", "--on", "nope"); code == 0 {
		t.Fatal("unknown kind accepted")
	}
}

func TestAlertWebhook(t *testing.T) {
	setup(t, nil)
	resetDesktop(t)
	srv, bodies := hookServer(t, http.StatusOK)
	url := srv.URL + "/hook?token=topsecret123"
	if code, _ := callIn(t, url, "secret", "set", "hook"); code != 0 {
		t.Fatal("secret set failed")
	}
	if code, _ := call(t, "alert", "set", "--webhook", url); code == 0 {
		t.Fatal("raw URL accepted")
	}
	code, env := call(t, "alert", "set", "--desktop", "off", "--webhook", "secret:hook")
	b, _ := json.Marshal(env)
	if code != 0 || strings.Contains(string(b), "topsecret123") || !strings.Contains(string(b), "[secret:hook]") {
		t.Fatalf("set: %s", b)
	}
	_, env = call(t, "alert", "show")
	b, _ = json.Marshal(env)
	if strings.Contains(string(b), "topsecret123") {
		t.Fatalf("show leaked the URL: %s", b)
	}

	alertpkg.Notify(alertpkg.Event{Bot: "g", Kind: alertpkg.KindGuard, Message: "loss guard", Time: time.Unix(0, 0).UTC()})
	if len(*bodies) != 1 || len(desktopSent) != 0 {
		t.Fatalf("bodies %d desktop %d", len(*bodies), len(desktopSent))
	}
	var got map[string]any
	if err := json.Unmarshal((*bodies)[0], &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || got["bot"] != "g" || got["kind"] != "guard" || got["message"] != "loss guard" || got["time"] != "1970-01-01T00:00:00Z" {
		t.Fatalf("body: %v", got)
	}
	if code, _ := call(t, "alert", "test"); code != 0 || len(*bodies) != 2 {
		t.Fatal("alert test did not send")
	}
	if strings.Contains(auditText(t), "topsecret123") {
		t.Fatal("audit holds the URL")
	}
}

func TestAlertFailureAudited(t *testing.T) {
	setup(t, nil)
	resetDesktop(t)
	desktopErr = errors.New("no display")
	srv, _ := hookServer(t, http.StatusInternalServerError)
	if code, _ := callIn(t, srv.URL+"/x?token=topsecret123", "secret", "set", "hook"); code != 0 {
		t.Fatal("secret set failed")
	}
	if code, _ := call(t, "alert", "set", "--webhook", "secret:hook"); code != 0 {
		t.Fatal("set failed")
	}
	// Notify returns nothing, so a failure cannot reach the loop.
	alertpkg.Notify(alertpkg.Event{Bot: "g", Kind: alertpkg.KindHalt, Message: "HALTED"})
	a := auditText(t)
	if strings.Count(a, "alert.failed") != 2 || !strings.Contains(a, "no display") || !strings.Contains(a, "500") {
		t.Fatalf("audit: %s", a)
	}
	if strings.Contains(a, "topsecret123") {
		t.Fatal("audit holds the URL")
	}
	code, env := call(t, "alert", "test")
	b, _ := json.Marshal(env)
	if code == 0 || strings.Contains(string(b), "topsecret123") {
		t.Fatalf("test: %d %s", code, b)
	}
}

func TestAlertWebhookHTTPSOnly(t *testing.T) {
	setup(t, nil)
	resetDesktop(t)
	if code, _ := callIn(t, "http://example.invalid/?token=topsecret123", "secret", "set", "hook"); code != 0 {
		t.Fatal("secret set failed")
	}
	call(t, "alert", "set", "--desktop", "off", "--webhook", "secret:hook")
	code, env := call(t, "alert", "test")
	b, _ := json.Marshal(env)
	if code == 0 || !strings.Contains(string(b), "not an https URL") || strings.Contains(string(b), "topsecret123") {
		t.Fatalf("http accepted or leaked: %s", b)
	}
}

// A webhook that redirects to plain HTTP is refused, so the body never
// leaves in clear text.
func TestWebhookRedirectToHTTPRefused(t *testing.T) {
	hits := 0
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	t.Cleanup(plain.Close)
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(tlsSrv.Close)
	c := tlsSrv.Client()
	c.CheckRedirect = alertpkg.Client.CheckRedirect
	req, _ := http.NewRequest(http.MethodPost, tlsSrv.URL, nil)
	if _, err := c.Do(req); err == nil || hits != 0 {
		t.Fatalf("redirect to http followed: err=%v hits=%d", err, hits)
	}
}
