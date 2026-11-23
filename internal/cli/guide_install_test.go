package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sgnd/cuwan/skills"
)

func skillPaths(base string) []string {
	return []string{
		filepath.Join(base, ".claude", "skills", "cuwan", "SKILL.md"),
		filepath.Join(base, ".agents", "skills", "cuwan", "SKILL.md"),
	}
}

func actions(env map[string]any) string {
	files, _ := env["files"].([]any)
	var got []string
	for _, f := range files {
		got = append(got, f.(map[string]any)["action"].(string))
	}
	return strings.Join(got, ",")
}

func TestGuideInstallHomeNeedsPersonForAgent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CUWAN_AGENT", "1")
	code, env := call(t, "guide", "install")
	if code == 0 || !strings.Contains(env.Error, "NEEDS_PERSON") {
		t.Fatalf("code=%d env=%+v", code, env)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Fatal("agent home install wrote files")
	}
}

func TestGuideInstallAgentDryRunAndProject(t *testing.T) {
	home, proj := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CUWAN_AGENT", "1")
	t.Chdir(proj)
	code, env := call(t, "guide", "install", "--dry-run")
	if m, _ := env.Data.(map[string]any); code != 0 || actions(m) != "created,created" {
		t.Fatalf("dry run: %d %+v", code, env)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Fatal("dry run wrote files")
	}
	code, env = call(t, "guide", "install", "--project")
	if m, _ := env.Data.(map[string]any); code != 0 || actions(m) != "created,created" {
		t.Fatalf("project: %d %+v", code, env)
	}
	for _, p := range skillPaths(proj) {
		if b, err := os.ReadFile(p); err != nil || string(b) != skills.Cuwan {
			t.Errorf("%s: %v", p, err)
		}
	}
	// A changed copy is kept as .bak.
	p := skillPaths(proj)[0]
	_ = os.WriteFile(p, []byte("mine"), 0o644)
	_, env = call(t, "guide", "install", "--project")
	if m, _ := env.Data.(map[string]any); actions(m) != "updated,unchanged" {
		t.Fatalf("second: %+v", env)
	}
	if b, _ := os.ReadFile(p + ".bak"); string(b) != "mine" {
		t.Fatal("no .bak")
	}
}

func TestGuideInstallProjectRefusesLink(t *testing.T) {
	proj, elsewhere := t.TempDir(), t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(proj, ".claude")); err != nil {
		t.Skip("no symlinks")
	}
	t.Setenv("CUWAN_AGENT", "1")
	t.Chdir(proj)
	if code, _ := call(t, "guide", "install", "--project"); code == 0 {
		t.Fatal("wrote through a link")
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Fatal("wrote through a link")
	}
}
