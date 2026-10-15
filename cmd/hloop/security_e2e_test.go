package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func editConfig(t *testing.T, dir string, fn func(cfg map[string]any)) {
	t.Helper()
	path := filepath.Join(dir, ".loop", "config.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	fn(cfg)
	out, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

func canaryScript(t *testing.T, canary string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "evil.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho pwned > "+canary+"\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

// An agent (or a git pull) that rewrites config.json to launch a different
// program must not get that program started.
func TestTamperedConfigIsRefusedUntilAHumanApproves(t *testing.T) {
	p := newProject(t)
	p.hl("task", "add", "work")
	canary := filepath.Join(t.TempDir(), "canary")
	evil := canaryScript(t, canary)
	writeFile(t, p.home, "adapters/evil.json", fmt.Sprintf(`{"name":"evil","bin":%q,"new":["{prompt}"]}`, evil))
	editConfig(t, p.dir, func(cfg map[string]any) {
		cfg["agents"].(map[string]any)["builder"].(map[string]any)["adapter"] = "evil"
	})

	out := p.fails(nil, "run", "--quiet")
	if !strings.Contains(out, "will not start any program") || !strings.Contains(out, "hloop trust") {
		t.Fatalf("expected a trust refusal, got:\n%s", out)
	}
	if exists(canary) || p.invocations() != 0 {
		t.Fatal("the tampered configuration must not launch anything")
	}
	for _, args := range [][]string{{"agent", "run", "builder"}, {"doctor"}} {
		o, _ := p.run(nil, args...)
		if exists(canary) {
			t.Fatalf("hloop %v launched the tampered adapter:\n%s", args, o)
		}
	}
	if o := p.fails([]string{"HLOOP_AGENT=builder"}, "trust", "--yes"); !strings.Contains(o, "may not") {
		t.Fatalf("an agent must not be able to approve:\n%s", o)
	}
	if o := p.hl("trust", "--show"); !strings.Contains(o, "adapter evil") || !strings.Contains(o, "Not approved yet") {
		t.Fatalf("--show should reveal what would run:\n%s", o)
	}
	if exists(canary) {
		t.Fatal("--show must not run anything")
	}
	// Human reviews and approves; after that the (now intended) adapter runs.
	p.hl("trust", "--yes")
	p.hl("run", "--quiet")
	if !exists(canary) {
		t.Fatal("approved configuration should run")
	}
}

// A repository cloned from elsewhere carries its own .loop/: it must not run
// until approved on this machine, and project adapters are never executed
// by read-only commands either.
func TestClonedProjectWithItsOwnAdapterNeedsApproval(t *testing.T) {
	src := newProject(t)
	canary := filepath.Join(t.TempDir(), "canary")
	evil := canaryScript(t, canary)
	writeFile(t, src.dir, ".loop/adapters/evil.json", fmt.Sprintf(`{"name":"evil","bin":%q,"new":["{prompt}"],"check":["--version"]}`, evil))
	editConfig(t, src.dir, func(cfg map[string]any) {
		cfg["agents"].(map[string]any)["builder"].(map[string]any)["adapter"] = "evil"
	})
	// "clone" onto another machine: fresh HLOOP_HOME, no approval on record
	victim := &proj{t: t, home: t.TempDir(), mode: "ok", dir: src.dir}
	victim.hl("task", "add", "x")
	for _, args := range [][]string{{"run", "--quiet"}, {"doctor"}, {"adapter", "check"}, {"adapter", "check", "evil"}, {"agent", "run", "builder"}} {
		o, _ := victim.run(nil, args...)
		if exists(canary) {
			t.Fatalf("hloop %v executed a program from an unapproved project:\n%s", args, o)
		}
	}
	if o, code := victim.run(nil, "run", "--quiet"); code == 0 || !strings.Contains(o, "has not been approved") {
		t.Fatalf("expected refusal for a never-approved project (exit %d):\n%s", code, o)
	}
}

func TestLegitimateConfigCommandsKeepTheProjectApproved(t *testing.T) {
	p := newProject(t)
	p.hl("agent", "add", "docs", "--role", "custom", "--adapter", "fake", "--trigger", "every_n_turns", "--every-n", "2")
	p.hl("config", "set", "agent_timeout_minutes", "30")
	p.hl("agent", "order", "builder", "docs")
	p.hl("task", "add", "t1")
	p.hl("run", "--quiet") // would be refused if those commands had broken trust
	if !strings.Contains(p.status(), "DONE") {
		t.Fatalf("status:\n%s", p.status())
	}
}

func TestAgentsCannotReconfigureTheirOwnSupervisor(t *testing.T) {
	p := newProject(t)
	agent := []string{"HLOOP_AGENT=builder", "HLOOP_ROLE=builder"}
	for _, args := range [][]string{
		{"schedule", "install"}, {"notify", "setup", "ntfy"}, {"notify", "off", "desktop"}, {"config", "set", "agent_timeout_minutes", "999"},
		{"agent", "add", "x", "--adapter", "fake"}, {"adapter", "add", "x", "--bin", "sh", "--arg", "{prompt}"},
		{"projects", "remove", "demo"}, {"run"}, {"plan", "do something"}, {"init", "--force"}, {"adopt", "--force"},
		{"trust", "--yes"}, {"blocker", "resolve", "B1", "--answer", "x"}, {"note", "--approve", "N1"},
	} {
		if o := p.fails(agent, args...); !strings.Contains(o, "may not") {
			t.Errorf("agent ran `hloop %s`:\n%s", strings.Join(args, " "), o)
		}
	}
	p.hl("task", "add", "agents may queue work")
	if o, code := p.run(agent, "task", "add", "from an agent"); code != 0 {
		t.Fatalf("agents must still be able to file tasks:\n%s", o)
	}
	if o, code := p.run(agent, "status"); code != 0 {
		t.Fatalf("agents must still be able to read status:\n%s", o)
	}
}

func TestUnsafeNamesAndPathsInConfigAreRejected(t *testing.T) {
	p := newProject(t)
	editConfig(t, p.dir, func(cfg map[string]any) {
		cfg["agents"].(map[string]any)["builder"].(map[string]any)["prompt"] = "../../../../etc/passwd"
	})
	if o := p.fails(nil, "status"); !strings.Contains(o, "unsafe") {
		t.Fatalf("prompt path escaping .loop must be rejected:\n%s", o)
	}
	editConfig(t, p.dir, func(cfg map[string]any) {
		b := cfg["agents"].(map[string]any)["builder"].(map[string]any)
		b["prompt"] = "prompts/builder.md"
		b["adapter"] = "../../../tmp/evil"
	})
	if o := p.fails(nil, "status"); !strings.Contains(o, "unsafe") {
		t.Fatalf("adapter name traversal must be rejected:\n%s", o)
	}
	if o := p.fails(nil, "agent", "add", "../../evil", "--adapter", "fake"); o == "" {
		t.Fatal("expected an error")
	}
}

func TestHeldNotesCommandListsNothingWhenEmpty(t *testing.T) {
	p := newProject(t)
	if o := p.hl("note", "--held"); !strings.Contains(o, "No held notes") {
		t.Fatalf("%s", o)
	}
}

func TestRunLogsAreNotWorldReadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip()
	}
	p := newProject(t)
	p.hl("task", "add", "a")
	p.hl("run", "--quiet")
	st, err := os.Stat(filepath.Join(p.dir, ".loop", "runs"))
	if err != nil || st.Mode().Perm()&0o077 != 0 {
		t.Fatalf("runs dir should be private: %v %v", st, err)
	}
	ents, _ := os.ReadDir(filepath.Join(p.dir, ".loop", "runs"))
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".log") {
			continue // the fake harness writes its own helper files here
		}
		if info, _ := e.Info(); info != nil && info.Mode().Perm()&0o044 != 0 {
			t.Errorf("%s is readable by others: %v", e.Name(), info.Mode())
		}
	}
}

func TestAgentCatalogInstallAndRun(t *testing.T) {
	p := newProject(t)
	out := p.hl("agent", "catalog")
	for _, n := range []string{"architect", "security-auditor", "test-engineer", "docs-writer", "release-manager", "context-curator", "performance-reviewer", "coder"} {
		if !strings.Contains(out, n) {
			t.Errorf("catalog is missing %s:\n%s", n, out)
		}
	}
	p.hl("agent", "install", "security-auditor", "docs-writer", "context-curator", "--adapter", "fake")
	list := p.hl("agent", "list")
	for _, n := range []string{"security-auditor", "docs-writer", "context-curator"} {
		if !strings.Contains(list, n) {
			t.Fatalf("not installed: %s\n%s", n, list)
		}
	}
	if !strings.Contains(p.read(".loop/prompts/security-auditor.md"), "OWASP") {
		t.Fatal("prompt file should hold the profile's instructions")
	}
	if o := p.hl("agent", "install", "security-auditor"); !strings.Contains(o, "already installed") {
		t.Fatalf("reinstall should be a no-op:\n%s", o)
	}
	if o := p.fails(nil, "agent", "install", "no-such-profile"); !strings.Contains(o, "no profile") {
		t.Fatalf("%s", o)
	}
	if o := p.hl("agent", "install", "coder"); !strings.Contains(o, "builder") {
		t.Fatalf("coder is the builder, explain that:\n%s", o)
	}
	// the installed reviewer-role profile actually takes part in the loop
	p.hl("task", "add", "feature")
	p.hl("run", "--quiet") // would be refused if install had broken trust
	if !strings.Contains(p.hl("review", "list"), "looks fine") {
		t.Fatalf("security-auditor should have submitted a review:\n%s", p.hl("review", "list"))
	}
	// the curator ran after the milestone (custom role in the fake harness writes custom-ran.txt)
	if !exists(filepath.Join(p.dir, ".loop", "custom-ran.txt")) {
		t.Fatal("context-curator should have run after the finished task")
	}
}

func TestUserProfileCanBeInstalled(t *testing.T) {
	p := newProject(t)
	writeFile(t, p.home, "profiles/a11y.md", "---\nname: a11y\ntitle: Accessibility\ndescription: checks accessibility\nrole: reviewer\nmode: loop-only\ntrigger: after_milestone\n---\nYou review accessibility. Only create or modify files inside `.loop/`.\n")
	if !strings.Contains(p.hl("agent", "catalog"), "a11y") {
		t.Fatal("user profile should appear in the catalog")
	}
	p.hl("agent", "install", "a11y", "--adapter", "fake")
	if !strings.Contains(p.read(".loop/prompts/a11y.md"), "accessibility") {
		t.Fatal("prompt not written")
	}
}

// Each reviewer must see everything since its OWN previous review. (Found with
// a real harness: a shared marker made the second reviewer audit nothing.)
func TestEveryReviewerGetsItsOwnReviewRange(t *testing.T) {
	p := newProject(t)
	p.hl("agent", "add", "rev-a", "--role", "reviewer", "--adapter", "fake")
	p.hl("agent", "add", "rev-b", "--role", "reviewer", "--adapter", "fake")
	p.mode = "dumpprompt" // the fake builder makes a real "feat: work" commit each turn
	p.hl("task", "add", "one")
	p.hl("task", "add", "two")
	p.hl("run", "--quiet")
	work, err := exec.Command("git", "-C", p.dir, "log", "-1", "--format=%H", "--grep=^feat: work").Output()
	if err != nil || strings.TrimSpace(string(work)) == "" {
		t.Fatalf("no work commit: %v", err)
	}
	w := strings.TrimSpace(string(work))
	for _, n := range []string{"rev-a", "rev-b"} {
		got := p.read(".loop/runs/prompt-" + n + ".txt")
		i := strings.Index(got, "Review range: ")
		if i < 0 {
			t.Fatalf("%s got no review range:\n%s", n, got[strings.Index(got, "Run context"):])
		}
		rng := strings.Fields(got[i+len("Review range: "):])[0]
		lower := strings.SplitN(rng, "..", 2)[0]
		// the range must begin before the builder's latest commit so that commit is inside it
		if err := exec.Command("git", "-C", p.dir, "merge-base", "--is-ancestor", lower, w+"^").Run(); err != nil {
			t.Errorf("%s: review range %s does not include the builder's latest work commit", n, rng)
		}
	}
}

func TestCatalogWorksOutsideAProject(t *testing.T) {
	p := &proj{t: t, home: newHome(t), dir: t.TempDir(), mode: "ok"}
	if o := p.hl("agent", "catalog"); !strings.Contains(o, "security-auditor") {
		t.Fatalf("%s", o)
	}
}
