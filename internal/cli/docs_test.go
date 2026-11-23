package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func repoFile(t *testing.T, parts ...string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(append([]string{"..", ".."}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var commandLine = regexp.MustCompile("(?m)^- `cuwan ([a-z-]+(?: [a-z-]+)?)`")

// listed returns the commands named in the file's "## Commands" section.
func listed(t *testing.T, body string) []string {
	t.Helper()
	i := strings.Index(body, "\n## Commands\n")
	if i < 0 {
		t.Fatal(`no "## Commands" section`)
	}
	rest := body[i+len("\n## Commands\n"):]
	if j := strings.Index(rest, "\n## "); j >= 0 {
		rest = rest[:j]
	}
	var got []string
	for _, m := range commandLine.FindAllStringSubmatch(rest, -1) {
		got = append(got, m[1])
	}
	return got
}

func binaryCommands() []string {
	return Commands()
}

func sameSet(t *testing.T, where string, got []string) {
	t.Helper()
	want := binaryCommands()
	for _, w := range want {
		if !slices.Contains(got, w) {
			t.Errorf("%s does not list `cuwan %s`", where, w)
		}
	}
	for _, g := range got {
		if !slices.Contains(want, g) {
			t.Errorf("%s lists `cuwan %s`, which the binary does not have", where, g)
		}
	}
}

func TestSkillListsExactlyTheCommands(t *testing.T) {
	sameSet(t, "skills/cuwan/SKILL.md", listed(t, repoFile(t, "skills", "cuwan", "SKILL.md")))
}

func TestAgentsListsExactlyTheCommands(t *testing.T) {
	sameSet(t, "AGENTS.md", listed(t, repoFile(t, "AGENTS.md")))
}

// Every listed command is dispatched: none fails as unknown.
func TestEveryCommandIsDispatched(t *testing.T) {
	setup(t, &fakeEx{})
	t.Setenv("HOME", t.TempDir())
	for _, c := range binaryCommands() {
		_, env := call(t, strings.Fields(c)...)
		if strings.Contains(env.Error, "unknown") {
			t.Errorf("`cuwan %s`: %s", c, env.Error)
		}
	}
}

func TestChangelogHeading(t *testing.T) {
	body := repoFile(t, "CHANGELOG.md")
	if !regexp.MustCompile(`(?m)^## v0\.0\.1 - \d{4}-\d{2}-\d{2}$`).MatchString(body) {
		t.Fatal("CHANGELOG.md needs a heading `## v0.0.1 - YYYY-MM-DD`")
	}
	if regexp.MustCompile(`(?m)^## \[`).MatchString(body) {
		t.Fatal("CHANGELOG.md has a bracketed heading")
	}
}

func TestReleaseWorkflowReadsTagHeading(t *testing.T) {
	wf := repoFile(t, ".github", "workflows", "release.yml")
	for _, s := range []string{`"## $GITHUB_REF_NAME "`, `--title "$GITHUB_REF_NAME"`, "make dist"} {
		if !strings.Contains(wf, s) {
			t.Errorf("release.yml lacks %s", s)
		}
	}
}
