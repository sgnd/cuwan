package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sgnd/cuwan/internal/store"
)

// exportTo fills the setup home with a secret, a live runtime and audit rows, then exports.
func exportTo(t *testing.T) string {
	t.Helper()
	setup(t, &fakeEx{})
	if code, env := callIn(t, "vault-value-must-not-leak\n", "secret", "set", "x"); code != 0 {
		t.Fatalf("secret set: %+v", env)
	}
	if err := store.PutRuntime(&store.RuntimeState{BotName: "g", Mode: "live", Status: "running",
		Orders: []store.RuntimeOrder{{ClientOID: "c1", Side: "buy", Price: 95, Qty: 0.01, Status: "open"}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendAudit("test.entry", map[string]any{"n": 1}); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "backup.json")
	if code, env := call(t, "export", file, "--with-audit"); code != 0 {
		t.Fatalf("export: %+v", env)
	}
	return file
}

// freshHome points cuwan at an empty home, keeping setup's key source.
func freshHome(t *testing.T) {
	t.Helper()
	store.ResetForTest()
	t.Setenv("CUWAN_HOME", filepath.Join(t.TempDir(), "home2"))
}

func plan(t *testing.T, data any) importPlan {
	t.Helper()
	b, _ := json.Marshal(data)
	var p importPlan
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExportHasNoSecretValues(t *testing.T) {
	file := exportTo(t)
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "vault-value-must-not-leak") {
		t.Fatal("export file holds a vault value")
	}
	if !strings.Contains(string(b), `"secret_ref": "secret:x"`) {
		t.Fatalf("export file lacks the secret ref: %s", b)
	}
	st, _ := os.Stat(file)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, want 0600", st.Mode().Perm())
	}
}

func TestImportRoundTripIntoEmptyHome(t *testing.T) {
	file := exportTo(t)
	freshHome(t)
	code, env := call(t, "import", file)
	if code != 0 {
		t.Fatalf("import: %+v", env)
	}
	p := plan(t, env.Data)
	if len(p.Added) != 3 || len(p.Skipped) != 0 {
		t.Fatalf("plan %+v", p)
	}
	if len(p.MissingSecrets) != 1 || p.MissingSecrets[0] != "x" {
		t.Fatalf("missing secrets %+v", p.MissingSecrets)
	}
	if p.AuditAdded == 0 {
		t.Fatal("audit not imported")
	}
	b, err := store.GetBot("g")
	if err != nil || b.Symbol != "BTCUSDT" {
		t.Fatalf("bot %+v %v", b, err)
	}
	e, err := store.GetExchange("fx")
	if err != nil || e.SecretRef != "secret:x" {
		t.Fatalf("exchange %+v %v", e, err)
	}
	// A second import adds no audit rows twice.
	code, env = callIn(t, "", "import", file, "--yes")
	if code != 0 || plan(t, env.Data).AuditAdded != 0 {
		t.Fatalf("second import: %+v", env)
	}
}

func TestImportLiveRuntimeAsStopped(t *testing.T) {
	file := exportTo(t)
	freshHome(t)
	code, env := call(t, "import", file)
	if code != 0 {
		t.Fatalf("import: %+v", env)
	}
	if p := plan(t, env.Data); len(p.StoppedOnImport) != 1 || p.StoppedOnImport[0] != "g" {
		t.Fatalf("plan %+v", p)
	}
	rt, err := store.GetRuntime("g")
	if err != nil || rt.Status != "stopped" || rt.Meta["import_note"] == "" || len(rt.Orders) != 1 {
		t.Fatalf("runtime %+v %v", rt, err)
	}
}

func TestImportAgentWithoutYesRefusesClash(t *testing.T) {
	file := exportTo(t)
	code, env := call(t, "import", file)
	if code == 0 || !strings.Contains(env.Error, "bot:g") {
		t.Fatalf("want clash error, got %+v", env)
	}
}

func TestImportPersonSkipsExisting(t *testing.T) {
	file := exportTo(t)
	t.Setenv("CUWAN_AGENT", "")
	if err := store.PutBot(&store.Bot{Name: "g", Exchange: "fx", Symbol: "ETHUSDT", Lower: 1, Upper: 2, Levels: 2}); err != nil {
		t.Fatal(err)
	}
	// out.Agent treats a pipe as an agent, so a person is a char device on stdout.
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = null
	code := run([]string{"import", file})
	os.Stdout = old
	null.Close()
	if code != 0 {
		t.Fatalf("person import: code %d", code)
	}
	if b, _ := store.GetBot("g"); b.Symbol != "ETHUSDT" {
		t.Fatal("existing bot was replaced without --yes")
	}
}

func TestImportYesReplaces(t *testing.T) {
	file := exportTo(t)
	if err := store.PutBot(&store.Bot{Name: "g", Exchange: "fx", Symbol: "ETHUSDT", Lower: 1, Upper: 2, Levels: 2}); err != nil {
		t.Fatal(err)
	}
	code, env := call(t, "import", file, "--yes")
	if code != 0 || len(plan(t, env.Data).Replaced) != 3 {
		t.Fatalf("import: %+v", env)
	}
	if b, _ := store.GetBot("g"); b.Symbol != "BTCUSDT" {
		t.Fatalf("bot not replaced: %+v", b)
	}
	if bots, _ := store.ListBots(); len(bots) != 1 {
		t.Fatalf("replace made a duplicate: %d bots", len(bots))
	}
}

func TestImportDryRunWritesNothing(t *testing.T) {
	file := exportTo(t)
	freshHome(t)
	code, env := call(t, "import", file, "--dry-run")
	if code != 0 || len(plan(t, env.Data).Added) != 3 {
		t.Fatalf("dry run: %+v", env)
	}
	if _, err := store.GetBot("g"); err == nil {
		t.Fatal("dry run wrote a bot")
	}
}

func TestImportBadVersion(t *testing.T) {
	setup(t, &fakeEx{})
	file := filepath.Join(t.TempDir(), "old.json")
	if err := os.WriteFile(file, []byte(`{"version": 99}`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, env := call(t, "import", file)
	if code == 0 || !strings.Contains(env.Error, "version 99") {
		t.Fatalf("want version error, got %+v", env)
	}
}

// Imported audit entries are marked, so a file cannot pass off its history
// as work done on this machine.
func TestImportedAuditIsMarked(t *testing.T) {
	file := exportTo(t)
	freshHome(t)
	if code, env := call(t, "import", file, "--yes"); code != 0 {
		t.Fatalf("import: %+v", env)
	}
	rows, err := store.ListAudit(1000)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rows {
		if strings.Contains(string(r), "test.entry") {
			found = true
			if !strings.Contains(string(r), `"imported":true`) {
				t.Fatalf("imported entry not marked: %s", r)
			}
		}
	}
	if !found {
		t.Fatal("audit entry not imported")
	}
}
