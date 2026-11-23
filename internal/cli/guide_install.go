package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sgnd/cuwan/internal/out"
	"github.com/sgnd/cuwan/skills"
)

// skillDirs are the folders AI agents read skills from: Claude Code reads
// .claude/skills; Codex, Cursor, Gemini CLI and others read .agents/skills.
var skillDirs = []string{".claude/skills", ".agents/skills"}

type skillFile struct {
	Path   string `json:"path"`
	Action string `json:"action"` // created | updated | unchanged
	Backup string `json:"backup,omitempty"`
}

const guideInstallUsage = `usage: cuwan guide install [--project] [--dry-run]

Adds the cuwan skill (cuwan/SKILL.md) to ~/.claude/skills (Claude Code) and
~/.agents/skills (Codex, Cursor, Gemini CLI and others). --project writes
into the current folder instead. A changed old copy is kept as .bak.
An AI agent may run --project and --dry-run; the home-wide install needs a
person.`

func cmdGuideInstall(args []string) int {
	f, err := parse(args, nil, []string{"project", "dry-run", "help"})
	if err != nil || len(f.pos) > 0 {
		return out.Fail("guide.install", fmt.Errorf("usage: cuwan guide install [--project] [--dry-run]"), 2)
	}
	if f.set["help"] {
		fmt.Fprintln(os.Stdout, guideInstallUsage)
		return 0
	}
	project, dry := f.set["project"], f.set["dry-run"]
	if out.Agent() && !project && !dry {
		return out.Fail("guide.install", fmt.Errorf("NEEDS_PERSON: the home-wide install changes every project on this computer; a person runs it"), 1,
			"an agent may run `cuwan guide install --project` or `--dry-run`")
	}
	base, err := os.UserHomeDir()
	if project {
		base, err = os.Getwd()
	}
	if err != nil {
		return out.Fail("guide.install", fmt.Errorf("cannot find the folder to install into: %w", err), 1)
	}
	var paths []string
	for _, d := range skillDirs {
		paths = append(paths, filepath.Join(base, d, "cuwan", "SKILL.md"))
	}
	if project {
		if err := noLinksUnder(base, paths); err != nil {
			return out.Fail("guide.install", err, 1)
		}
	}
	files := []skillFile{}
	var lines []string
	for _, p := range paths {
		f, err := installSkill(p, dry)
		if err != nil {
			return out.Fail("guide.install", err, 1)
		}
		files = append(files, f)
		lines = append(lines, f.Action+"  "+f.Path)
		if f.Backup != "" {
			lines = append(lines, "  old version kept as "+f.Backup)
		}
	}
	if !dry {
		lines = append(lines, "Restart your AI agent so it sees the skill.")
	}
	out.Print(os.Stdout, "guide.install", map[string]any{"files": files, "dry_run": dry}, strings.Join(lines, "\n"))
	return 0
}

// installSkill writes the skill to path, keeping a different old copy as a
// backup. Writes go through a temporary file renamed into place.
func installSkill(path string, dryRun bool) (skillFile, error) {
	f := skillFile{Path: path, Action: "created"}
	old, err := os.ReadFile(path) // #nosec G304 -- path is built from the home or working folder and a fixed name
	switch {
	case err == nil && string(old) == skills.Cuwan:
		f.Action = "unchanged"
		return f, nil
	case err == nil:
		f.Action, f.Backup = "updated", backupName(path, time.Now())
	case !errors.Is(err, fs.ErrNotExist):
		return f, err
	}
	if dryRun {
		return f, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return f, err
	}
	if f.Backup != "" {
		if err := writeAtomic(f.Backup, old, 0o600, false); err != nil {
			return f, err
		}
	}
	return f, writeAtomic(path, []byte(skills.Cuwan), 0o644, true)
}

// backupName is PATH.bak, or PATH.bak.<UTC time> when that is taken.
func backupName(path string, now time.Time) string {
	name := path + ".bak"
	if _, err := os.Lstat(name); err == nil {
		name = path + ".bak." + now.UTC().Format("20060102T150405Z")
	}
	return name
}

// writeAtomic writes b next to path and renames it into place. With
// replace false it refuses a name that exists by then.
func writeAtomic(path string, b []byte, mode os.FileMode, replace bool) error {
	if !replace {
		if _, err := os.Lstat(path); err == nil {
			return fmt.Errorf("%s appeared while cuwan wrote it; nothing was changed, run it again", path)
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // no-op after a successful rename
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// noLinksUnder refuses a link on the way from base to any of paths: a link
// committed in a project folder could point the write elsewhere.
func noLinksUnder(base string, paths []string) error {
	for _, p := range paths {
		rel, err := filepath.Rel(base, p)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("%s is not in %s", p, base)
		}
		cur := base
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			cur = filepath.Join(cur, part)
			fi, err := os.Lstat(cur)
			if errors.Is(err, fs.ErrNotExist) {
				break
			}
			if err != nil {
				return err
			}
			if fi.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
				return fmt.Errorf("%s is a link, and cuwan does not follow links in a project folder; nothing was changed", cur)
			}
		}
	}
	return nil
}
