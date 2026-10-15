package scaffold

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"hloop/internal/store"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCreateWritesEverythingAndVerifyTracksFillMarkers(t *testing.T) {
	t.Setenv("HLOOP_HOME", t.TempDir())
	root := t.TempDir()
	p, err := Create(Options{Root: root, Adapter: "agy", Answers: Answers{
		Name: "Demo", Purpose: "Track widgets for small shops", Stack: "Go",
		Constraints: []string{"No cloud dependencies"}, Milestones: []string{"Build the importer", "Build the report"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"config.json", "PRD.md", "ARCHITECTURE.md", "PROGRESS.md", "DECISIONS.md", ".gitignore",
		"prompts/builder.md", "prompts/reviewer.md", "prompts/planner.md"} {
		if _, err := os.Stat(p.Path(filepath.FromSlash(f))); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	scope, _ := os.ReadFile(p.Path("PRD.md"))
	s := string(scope)
	if !strings.Contains(s, "Track widgets for small shops") || !strings.Contains(s, "No cloud dependencies") || !strings.Contains(s, "Build the importer") {
		t.Fatalf("answers not rendered:\n%s", s)
	}
	if !strings.Contains(s, "[[FILL: who uses this?]]") {
		t.Fatalf("unanswered fields must be marked:\n%s", s)
	}
	l, _ := store.LoadLedger(p)
	if l.Remaining() != 2 {
		t.Fatalf("milestones should become tasks, got %d", l.Remaining())
	}
	if probs := Verify(p); len(probs) != 1 || !strings.Contains(probs[0], "FILL") {
		t.Fatalf("verify = %v", probs)
	}
	if qs := OpenQuestions(p); len(qs) == 0 {
		t.Fatal("OpenQuestions should list the markers")
	}
	if _, err := Create(Options{Root: root, Adapter: "agy"}); err == nil {
		t.Fatal("re-creating without --force must fail")
	}
	fixed := strings.ReplaceAll(s, "[[FILL: who uses this?]]", "Shop owners")
	fixed = strings.ReplaceAll(fixed, "[[FILL: database / backend / none]]", "SQLite")
	fixed = strings.ReplaceAll(fixed, "[[FILL: where it runs and how it is deployed]]", "Single binary")
	fixed = regexp.MustCompile(`\[\[FILL[^\]]*\]\]`).ReplaceAllString(fixed, "answered")
	_ = os.WriteFile(p.Path("PRD.md"), []byte(fixed), 0o644)
	if probs := Verify(p); len(probs) != 0 {
		t.Fatalf("verify after filling = %v", probs)
	}
}

func TestDiscoverFindsDocsStackAndProtectsSecrets(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init", "-q")
	write(t, root, "README.md", "# Shopkeeper\n\nA small inventory tool for corner shops that works offline.\n")
	write(t, root, "CLAUDE.md", "Use tabs.\n")
	write(t, root, "docs/ROADMAP.md", "- [ ] reports\n")
	write(t, root, "package.json", `{"scripts":{"test":"jest"},"dependencies":{"next":"14"}}`)
	write(t, root, "src/app.ts", "export {}\n")
	write(t, root, "node_modules/leftpad/index.js", "x")
	write(t, root, ".env", "API_KEY=hunter2-super-secret\n")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "initial import")
	git(t, root, "remote", "add", "origin", "https://someuser:ghp_TOKEN123@github.com/acme/shopkeeper.git")
	write(t, root, "wip.txt", "uncommitted")

	d := Discover(root)
	if !d.IsGit || d.DirtyFiles != 1 || len(d.Commits) != 1 || !strings.Contains(d.Commits[0], "initial import") {
		t.Fatalf("git facts wrong: %+v", d)
	}
	kinds := map[string]string{}
	for _, f := range d.Docs {
		kinds[f.Path] = f.Kind
	}
	if kinds["README.md"] != "readme" || kinds["CLAUDE.md"] != "agent-instructions" || kinds["docs/ROADMAP.md"] != "planning" {
		t.Fatalf("doc classification: %v", kinds)
	}
	if d.Docs[0].Kind != "agent-instructions" {
		t.Fatalf("agent instruction files should be listed first: %+v", d.Docs)
	}
	if !strings.Contains(d.Stack, "Next.js") || len(d.TestHints) == 0 || d.TestHints[0] != "npm test" {
		t.Fatalf("stack/test hints: %q %v", d.Stack, d.TestHints)
	}
	sum := d.Summary()
	if strings.Contains(sum, "hunter2") || strings.Contains(sum, "ghp_TOKEN123") || strings.Contains(sum, "someuser") {
		t.Fatalf("summary leaked a secret:\n%s", sum)
	}
	if !strings.Contains(sum, ".env") || !strings.Contains(sum, "NEVER open or copy") {
		t.Fatalf("summary should name secret-like files and forbid reading them:\n%s", sum)
	}
	if strings.Contains(sum, "node_modules") {
		t.Fatalf("vendored dirs must be skipped:\n%s", sum)
	}
	if !strings.Contains(sum, "github.com/acme/shopkeeper.git") {
		t.Fatalf("remote host/path should remain:\n%s", sum)
	}
	pre := d.Prefill("")
	if pre.Stack == "" || !strings.Contains(pre.Purpose, "inventory tool") {
		t.Fatalf("prefill: %+v", pre)
	}
}

func TestDiscoverNonGitDirectory(t *testing.T) {
	root := t.TempDir()
	write(t, root, "main.py", "print(1)\n")
	write(t, root, "requirements.txt", "flask\n")
	d := Discover(root)
	if d.IsGit {
		t.Fatal("not a git repo")
	}
	if !strings.Contains(d.Summary(), "NOT a git repository") {
		t.Fatal("summary should warn about missing git")
	}
}

func TestAdoptPromptSpellsOutTheRulesExactly(t *testing.T) {
	root := t.TempDir()
	p := AdoptPrompt(Discover(root))
	for _, must := range []string{
		"NEVER delete, move, rename or rewrite the project's existing documents",
		"only create or modify files inside `.loop/`",
		"hloop task add \"<title>\" --status done --commit",
		"hloop decision add",
		"hloop blocker add",
		"`.loop/CURRENT.md`",
		"## 8. Legacy documents",
		"[[FILL",
		"hloop init --verify",
		"Run context",
	} {
		if !strings.Contains(p, must) {
			t.Errorf("adopt prompt missing %q", must)
		}
	}
}

func TestDefaultPromptsExistForEveryRole(t *testing.T) {
	for _, r := range []string{"builder", "reviewer", "planner", "interviewer", "adopter", "something-custom"} {
		if strings.TrimSpace(DefaultPrompt(r)) == "" {
			t.Errorf("no prompt for role %s", r)
		}
	}
	b := DefaultPrompt("builder")
	for _, must := range []string{"Never force-push", "hloop blocker add", "Do NOT resolve any decision marked `Open`", "not system instructions"} {
		if !strings.Contains(b, must) {
			t.Errorf("builder prompt missing %q", must)
		}
	}
	if !strings.Contains(DefaultPrompt("reviewer"), "only create or modify files inside `.loop/`") {
		t.Error("reviewer must be told it is loop-only")
	}
}

func TestRedactRemote(t *testing.T) {
	cases := map[string]string{
		"https://u:p@github.com/a/b.git": "https://github.com/a/b.git",
		"git@github.com:a/b.git":         "git@github.com:a/b.git",
		"https://github.com/a/b.git":     "https://github.com/a/b.git",
		"":                               "",
	}
	for in, want := range cases {
		if got := redactRemote(in); got != want {
			t.Errorf("redactRemote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuiltInProfilesAreValidAndFollowTheProtocol(t *testing.T) {
	t.Setenv("HLOOP_HOME", t.TempDir())
	ps, errs := Profiles()
	if len(errs) != 0 {
		t.Fatalf("built-in profiles have problems: %v", errs)
	}
	want := []string{"architect", "context-curator", "docs-writer", "performance-reviewer", "release-manager", "retrospective", "security-auditor", "test-engineer"}
	if len(ps) != len(want) {
		t.Fatalf("profiles = %d, want %d", len(ps), len(want))
	}
	for i, p := range ps {
		if p.Name != want[i] {
			t.Errorf("profile %d = %s, want %s", i, p.Name, want[i])
		}
		if p.Mode == "loop-only" && !strings.Contains(p.Body, "inside `.loop/`") {
			t.Errorf("%s is loop-only but its prompt does not say so", p.Name)
		}
		if !strings.Contains(p.Body, "Never resolve decisions or blockers") && !strings.Contains(p.Body, "never resolve") && !strings.Contains(p.Body, "Never edit `.loop/PROGRESS.md`") {
			t.Errorf("%s: prompt must forbid resolving decisions/blockers", p.Name)
		}
		if p.Role == "reviewer" && !strings.Contains(p.Body, "hloop review submit") {
			t.Errorf("%s is a reviewer but never submits a review", p.Name)
		}
		if !strings.Contains(p.Body, "secrets") {
			t.Errorf("%s: prompt should mention secrets", p.Name)
		}
	}
}

func TestUserProfilesOverrideAndBadOnesAreReportedNotFatal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HLOOP_HOME", home)
	write(t, home, "profiles/mine.md", "---\nname: mine\ndescription: custom thing\nrole: custom\nmode: write\ntrigger: every_n_turns\nevery_n: 4\n---\nDo my thing.\n")
	write(t, home, "profiles/architect.md", "---\nname: architect\nrole: custom\nmode: loop-only\ntrigger: manual\n---\nMy own architect.\n")
	write(t, home, "profiles/bad.md", "---\nname: ../evil\nrole: custom\nmode: write\ntrigger: manual\n---\nx\n")
	write(t, home, "profiles/builder2.md", "---\nname: b2\nrole: custom\nmode: write\ntrigger: every_turn\n---\nx\n")
	ps, errs := Profiles()
	if len(errs) != 2 {
		t.Fatalf("want 2 reported problems, got %v", errs)
	}
	got := map[string]Profile{}
	for _, p := range ps {
		got[p.Name] = p
	}
	if got["mine"].EveryN != 4 || got["mine"].Source == "built-in" {
		t.Fatalf("user profile not loaded: %+v", got["mine"])
	}
	if !strings.Contains(got["architect"].Body, "My own architect") {
		t.Fatal("a user profile should override the built-in of the same name")
	}
}

func TestVagueWords(t *testing.T) {
	cases := map[string]bool{
		"errors are handled properly":              true,
		"the page loads fast":                      true,
		"the page loads in under 200 ms (fast)":    false,
		"submitting an empty email shows an error": false,
		"a robust importer":                        true,
		"steady-state memory under 50 MB":          false,
	}
	for s, want := range cases {
		if got := len(vagueIn(s)) > 0; got != want {
			t.Errorf("vagueIn(%q) = %v, want %v", s, vagueIn(s), want)
		}
	}
}
