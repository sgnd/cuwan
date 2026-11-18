package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sgnd/cuwan/internal/out"
	"github.com/sgnd/cuwan/internal/sealdb"
	"github.com/sgnd/cuwan/internal/store"
	"github.com/sgnd/cuwan/internal/vault"
)

// exportVersion changes when the file layout changes in a way import must know.
const exportVersion = 1

// exportFile is the backup layout. It holds secret names (secret_ref), never values.
type exportFile struct {
	Version    int                  `json:"version"`
	ExportedAt time.Time            `json:"exported_at"`
	Exchanges  []store.Exchange     `json:"exchanges"`
	Bots       []store.Bot          `json:"bots"`
	Runtimes   []store.RuntimeState `json:"runtimes"`
	Audit      []json.RawMessage    `json:"audit,omitempty"`
}

// noStoreYet lets export and import work on a fresh home.
func noStoreYet(err error) bool { return errors.Is(err, store.ErrNoStore) }

// firstArg is used instead of positional, which would eat FILE after a bare flag like --yes.
func firstArg(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

func cmdExport(args []string) int {
	path := firstArg(args)
	if path == "" {
		return out.Fail("export", fmt.Errorf("usage: cuwan export FILE [--with-audit]"), 2)
	}
	f := exportFile{Version: exportVersion, ExportedAt: time.Now().UTC()}
	var err error
	if f.Exchanges, err = store.ListExchanges(); err != nil && !noStoreYet(err) {
		return out.Fail("export", err, 1)
	}
	if f.Bots, err = store.ListBots(); err != nil && !noStoreYet(err) {
		return out.Fail("export", err, 1)
	}
	if f.Runtimes, err = store.ListRuntimes(); err != nil && !noStoreYet(err) {
		return out.Fail("export", err, 1)
	}
	if flagBool(args, "--with-audit") {
		rows, err := store.ListAudit(1 << 30)
		if err != nil && !noStoreYet(err) {
			return out.Fail("export", err, 1)
		}
		// The store lists newest first; the file keeps oldest first so import appends in order.
		for i := len(rows) - 1; i >= 0; i-- {
			f.Audit = append(f.Audit, rows[i])
		}
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return out.Fail("export", err, 1)
	}
	if err := store.WriteFileAtomic(path, append(b, '\n'), 0o600); err != nil {
		return out.Fail("export", err, 1)
	}
	data := map[string]any{
		"file": path, "version": exportVersion,
		"exchanges": len(f.Exchanges), "bots": len(f.Bots),
		"runtimes": len(f.Runtimes), "audit": len(f.Audit),
	}
	text := fmt.Sprintf("wrote %s: %d exchanges, %d bots, %d runtimes, %d audit entries (no secret values)",
		path, len(f.Exchanges), len(f.Bots), len(f.Runtimes), len(f.Audit))
	out.Print(os.Stdout, "export", data, text)
	return 0
}

// importPlan lists what import did (or would do with --dry-run), as kind:name keys.
type importPlan struct {
	DryRun          bool     `json:"dry_run"`
	Added           []string `json:"added"`
	Replaced        []string `json:"replaced"`
	Skipped         []string `json:"skipped"`
	StoppedOnImport []string `json:"stopped_on_import"`
	MissingSecrets  []string `json:"missing_secrets"`
	AuditAdded      int      `json:"audit_added"`
}

func cmdImport(args []string) int {
	path := firstArg(args)
	if path == "" {
		return out.Fail("import", fmt.Errorf("usage: cuwan import FILE [--dry-run] [--yes]"), 2)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return out.Fail("import", err, 1)
	}
	var f exportFile
	if err := json.Unmarshal(b, &f); err != nil {
		return out.Fail("import", fmt.Errorf("not a cuwan export file: %w", err), 1)
	}
	if f.Version != exportVersion {
		return out.Fail("import", fmt.Errorf("export version %d is not supported (want %d)", f.Version, exportVersion), 1,
			"export again with this cuwan version")
	}
	yes := flagBool(args, "--yes")
	dry := flagBool(args, "--dry-run")
	p := importPlan{DryRun: dry, Added: []string{}, Replaced: []string{}, Skipped: []string{},
		StoppedOnImport: []string{}, MissingSecrets: []string{}}

	have, err := existingNames()
	if err != nil {
		return out.Fail("import", err, 1)
	}

	// Collect clashes first so an agent gets one clear error before anything is written.
	var clashes []string
	for _, k := range fileNames(f) {
		if have[k] {
			clashes = append(clashes, k)
		}
	}
	if len(clashes) > 0 && !yes && !dry && out.Agent() {
		return out.Fail("import", fmt.Errorf("names already exist: %s", strings.Join(clashes, ", ")), 1,
			"a person can rerun with --yes to replace them", "use --dry-run to see the plan")
	}

	// decide records the outcome for one name and says whether to write it.
	decide := func(key string) bool {
		switch {
		case !have[key]:
			p.Added = append(p.Added, key)
			return !dry
		case yes:
			p.Replaced = append(p.Replaced, key)
			return !dry
		default:
			p.Skipped = append(p.Skipped, key)
			return false
		}
	}

	secrets := map[string]bool{}
	if infos, err := vault.List(); err == nil {
		for _, i := range infos {
			secrets[i.Name] = true
		}
	}
	for _, e := range f.Exchanges {
		if name, ok := vault.ParseRef(e.SecretRef); ok && !secrets[name] {
			p.MissingSecrets = append(p.MissingSecrets, name)
		}
		if !decide("exchange:" + e.Name) {
			continue
		}
		e.ID = ""
		if err := store.PutExchange(&e); err != nil {
			return out.Fail("import", err, 1)
		}
	}
	for _, x := range f.Bots {
		if !decide("bot:" + x.Name) {
			continue
		}
		x.ID = ""
		if err := store.PutBot(&x); err != nil {
			return out.Fail("import", err, 1)
		}
	}
	for _, r := range f.Runtimes {
		key := "runtime:" + r.BotName
		write := decide(key)
		if !have[key] || yes {
			// This machine did not start the bot, so it must not look running here.
			// Its orders may still rest on the venue; the note tells the person to check.
			if r.Status != "stopped" {
				p.StoppedOnImport = append(p.StoppedOnImport, r.BotName)
				if r.Meta == nil {
					r.Meta = map[string]string{}
				}
				r.Meta["import_note"] = fmt.Sprintf("imported as stopped (was %s); its orders may still be on the venue", r.Status)
				r.Status = "stopped"
			}
		}
		if !write {
			continue
		}
		r.ID = ""
		if err := store.PutRuntime(&r); err != nil {
			return out.Fail("import", err, 1)
		}
	}
	n, err := importAudit(f.Audit, dry)
	if err != nil {
		return out.Fail("import", err, 1)
	}
	p.AuditAdded = n
	if !dry {
		_ = store.AppendAudit("import", map[string]any{
			"file": path, "added": len(p.Added), "replaced": len(p.Replaced), "skipped": len(p.Skipped),
		})
	}
	out.Print(os.Stdout, "import", p, importText(p))
	return 0
}

// existingNames returns kind:name keys already in this home.
func existingNames() (map[string]bool, error) {
	have := map[string]bool{}
	exs, err := store.ListExchanges()
	if err != nil && !noStoreYet(err) {
		return nil, err
	}
	bots, err := store.ListBots()
	if err != nil && !noStoreYet(err) {
		return nil, err
	}
	rts, err := store.ListRuntimes()
	if err != nil && !noStoreYet(err) {
		return nil, err
	}
	for _, e := range exs {
		have["exchange:"+e.Name] = true
	}
	for _, x := range bots {
		have["bot:"+x.Name] = true
	}
	for _, r := range rts {
		have["runtime:"+r.BotName] = true
	}
	return have, nil
}

func fileNames(f exportFile) []string {
	var keys []string
	for _, e := range f.Exchanges {
		keys = append(keys, "exchange:"+e.Name)
	}
	for _, x := range f.Bots {
		keys = append(keys, "bot:"+x.Name)
	}
	for _, r := range f.Runtimes {
		keys = append(keys, "runtime:"+r.BotName)
	}
	return keys
}

// importAudit appends entries this home does not hold yet, so a second import adds nothing.
func importAudit(rows []json.RawMessage, dry bool) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	seen := map[string]bool{}
	existing, err := store.ListAudit(1 << 30)
	if err != nil && !noStoreYet(err) {
		return 0, err
	}
	for _, r := range existing {
		seen[string(r)] = true
	}
	var add [][]byte
	for _, r := range rows {
		if !seen[string(r)] {
			seen[string(r)] = true
			add = append(add, r)
		}
	}
	if dry || len(add) == 0 {
		return len(add), nil
	}
	d, err := store.DB(true)
	if err != nil {
		return 0, err
	}
	err = d.Update(context.Background(), func(tx *sealdb.Tx) error {
		for _, r := range add {
			if err := tx.AppendAudit(r); err != nil {
				return err
			}
		}
		return nil
	})
	return len(add), err
}

func importText(p importPlan) string {
	var s strings.Builder
	if p.DryRun {
		s.WriteString("dry run: nothing written\n")
	}
	line := func(label string, v []string) {
		if len(v) > 0 {
			fmt.Fprintf(&s, "%s: %s\n", label, strings.Join(v, ", "))
		}
	}
	line("added", p.Added)
	line("replaced", p.Replaced)
	line("skipped (exists; a person can rerun with --yes to replace)", p.Skipped)
	line("imported as stopped (its orders may still be on the venue; check with cuwan orders)", p.StoppedOnImport)
	line("secrets missing here (set each with cuwan secret set NAME)", p.MissingSecrets)
	fmt.Fprintf(&s, "audit entries added: %d", p.AuditAdded)
	return s.String()
}
