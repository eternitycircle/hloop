package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPRDReplacesProjectScopeAndUpgradeRenames(t *testing.T) {
	p := newProject(t)
	prd := p.read(".loop/PRD.md")
	for _, want := range []string{"# Product Requirements Document (PRD): demo", "## 2. Goals and non-goals", "## 7. Success metrics"} {
		if !strings.Contains(prd, want) {
			t.Fatalf("PRD template missing %q:\n%s", want, prd)
		}
	}
	// a 0.4.0 project still has PROJECT_SCOPE.md: it keeps working, and upgrade renames it
	os.Rename(filepath.Join(p.dir, ".loop", "PRD.md"), filepath.Join(p.dir, ".loop", "PROJECT_SCOPE.md"))
	if out := p.fails(nil, "init", "--verify"); !strings.Contains(out, "PROJECT_SCOPE.md still has") {
		t.Fatalf("legacy scope file should still be read:\n%s", out)
	}
	out := p.hl("init", "--upgrade")
	if !strings.Contains(out, "renamed .loop/PROJECT_SCOPE.md to .loop/PRD.md") {
		t.Fatalf("upgrade should rename:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(p.dir, ".loop", "PROJECT_SCOPE.md")); err == nil {
		t.Fatal("old file should be gone")
	}
	p.read(".loop/PRD.md")
}

func TestProgressLogKeepsEverything(t *testing.T) {
	p := newProject(t)
	p.hl("gate", "add", "ok", "--", "sh", "-c", "test -f ok.txt || { echo 'ok.txt is missing'; exit 3; }")
	for i := 0; i < 12; i++ {
		p.hl("task", "add", "task number "+string(rune('a'+i)))
	}
	p.mode = "gate" // first finish rejected, then every finish passes
	p.hl("run", "--quiet")
	prog := p.read(".loop/PROGRESS.md")
	if !strings.Contains(prog, "2 earlier finished task(s) not shown here; the complete history is in PROGRESS_LOG.md") {
		t.Fatalf("PROGRESS.md should point to the log:\n%s", prog)
	}
	log := p.read(".loop/PROGRESS_LOG.md")
	if strings.Count(log, "| **T") != 12 {
		t.Fatalf("the log must list every finished task:\n%s", log)
	}
	for _, want := range []string{"T1 queued", "T1 finish rejected by hloop: gate ok", "after 1 rejected attempt(s)", "[T12](reports/T12.md)", "## Timeline"} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q:\n%s", want, log)
		}
	}
}

func TestSpecFoldersGetGeneratedTasks(t *testing.T) {
	p := newProject(t)
	p.hl("spec", "export the table as CSV")
	tasks := p.read(".loop/specs/csv-export/tasks.md")
	if !strings.Contains(tasks, "GENERATED") || !strings.Contains(tasks, "**T1** Export visible rows as CSV") || !strings.Contains(tasks, "AC1 (Must)") {
		t.Fatalf("tasks.md:\n%s", tasks)
	}
	if out := p.hl("spec", "list"); !strings.Contains(out, "specs/csv-export/spec.md") || !strings.Contains(out, "T1") {
		t.Fatalf("spec list:\n%s", out)
	}
	// a 0.4.0 flat spec is still checked
	writeFile(t, p.dir, ".loop/specs/old.md", "# Old\n[[FILL: question]]\n")
	if out, _ := p.run(nil, "spec", "check"); !strings.Contains(out, "specs/old.md") {
		t.Fatalf("flat specs should still be checked:\n%s", out)
	}
}

func TestSpecProjectRunsTheProjectDefiner(t *testing.T) {
	p := newProject(t)
	notes := filepath.Join(t.TempDir(), "notes.md")
	os.WriteFile(notes, []byte("users want a shopping list"), 0o644)
	out := p.hl("spec", "--project", "--from", notes, "focus on the MVP")
	if !strings.Contains(out, "Spec check") {
		t.Fatalf("output:\n%s", out)
	}
	prompt := p.read(".loop/runs/prompt-interviewer-last.txt")
	for _, want := range []string{"PROJECT DEFINER", "No human is in this session", notes, "focus on the MVP", "DISCOVERY SUMMARY"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("project definer prompt missing %q", want)
		}
	}
	if strings.Contains(p.read(".loop/PRD.md"), "[[FILL") {
		t.Fatal("the definer should have filled the PRD")
	}
	if l := p.ledger(); len(l.Tasks) != 1 || len(l.Tasks[0].Criteria) != 2 {
		t.Fatalf("tasks: %+v", l.Tasks)
	}
	// agents may run `init --verify`, nothing else of init
	if v := p.read(".loop/runs/interviewer-verify.txt"); strings.Contains(v, "may not") {
		t.Fatalf("an agent must be allowed `hloop init --verify`:\n%s", v)
	}
	if v := p.read(".loop/runs/interviewer-init-force.txt"); !strings.Contains(v, "may not") || !strings.Contains(v, "exit=1") {
		t.Fatalf("an agent must not re-init:\n%s", v)
	}
	p.fails(nil, "spec", "--project", "--from", filepath.Join(t.TempDir(), "missing.md"))
	secret := filepath.Join(t.TempDir(), ".env")
	os.WriteFile(secret, []byte("KEY=1"), 0o600)
	if out := p.fails(nil, "spec", "--project", "--from", secret); !strings.Contains(out, "secrets") {
		t.Fatalf("secret files must not be handed to an agent:\n%s", out)
	}
}

func TestDocsAndHelp(t *testing.T) {
	p := newProject(t)
	out := p.hl("docs")
	for _, want := range []string{"overview", "getting-started", "files", "defining-work", "verification", "commands", "troubleshooting"} {
		if !strings.Contains(out, want) {
			t.Fatalf("docs index missing %q:\n%s", want, out)
		}
	}
	if out := p.hl("docs", "verif"); !strings.Contains(out, "# How hloop checks the work") {
		t.Fatalf("prefix lookup:\n%s", out)
	}
	if out := p.hl("docs", "find", "PROGRESS_LOG"); !strings.Contains(out, "files") {
		t.Fatalf("find:\n%s", out)
	}
	if out := p.hl("help", "gate"); !strings.Contains(out, "# How hloop checks the work") {
		t.Fatalf("help gate should show the verification page:\n%s", out)
	}
	dir := filepath.Join(t.TempDir(), "ref")
	p.hl("docs", "--export", dir)
	if _, err := os.Stat(filepath.Join(dir, "01-overview.md")); err != nil {
		t.Fatal("export should write the pages")
	}
	p.hl("docs", "--all")
	p.fails(nil, "docs", "nope")
	// agents may read docs but not write files with them
	p.hl("docs", "files")
	if out := p.fails([]string{"HLOOP_AGENT=builder"}, "docs", "--export", dir); !strings.Contains(out, "may not") {
		t.Fatalf("agents must not export docs:\n%s", out)
	}
}

func TestEveryCommandHasAHelpPage(t *testing.T) {
	for cmd := range map[string]bool{"init": true, "spec": true, "gate": true, "task": true, "run": true, "agent": true, "notify": true, "tour": true, "docs": true, "idea": true, "lesson": true, "adopt": true} {
		if _, ok := helpTopics[cmd]; !ok {
			t.Errorf("no help topic for %s", cmd)
		}
	}
}

func TestTourRunsEndToEnd(t *testing.T) {
	p := newProject(t)
	out := p.hl("tour", "--no-pause")
	for _, want := range []string{"Step 10 of 10", "REJECTED T1", "verified T1: gate greeting passed", "greeter: DONE", "finish rejected by hloop", "Parked I1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("tour output missing %q:\n%s", want, out)
		}
	}
	// it must not touch the user's registry
	if strings.Contains(p.hl("projects"), "greeter") {
		t.Fatal("the tour registered its demo project in the user's registry")
	}
	if out := p.hl("tour", "--list"); !strings.Contains(out, "10. Your turn") {
		t.Fatalf("list:\n%s", out)
	}
	if out := p.hl("tour", "--no-pause", "--step", "9"); strings.Contains(out, "Step 8 of 10") || !strings.Contains(out, "Step 9 of 10") {
		t.Fatalf("--step should replay earlier steps silently:\n%s", out)
	}
	// the demo agent refuses to work outside the tour
	if out := p.fails(nil, "__tour-agent", "x"); !strings.Contains(out, "internal command") {
		t.Fatalf("tour agent outside the tour:\n%s", out)
	}
	if out := p.fails([]string{"HLOOP_AGENT=builder"}, "__tour-agent"); !strings.Contains(out, "may not") {
		t.Fatalf("agents must not call the tour agent:\n%s", out)
	}
}

func TestAgentsMDBlockIsManagedAndPreservesContent(t *testing.T) {
	p := newProject(t)
	writeFile(t, p.dir, "AGENTS.md", "# House rules\n\nUse tabs.\n")
	p.hl("skill", "install", "--agents-md")
	a := p.read("AGENTS.md")
	if !strings.HasPrefix(a, "# House rules\n\nUse tabs.\n") || !strings.Contains(a, "<!-- hloop:start") || !strings.Contains(a, ".loop/PRD.md") {
		t.Fatalf("AGENTS.md:\n%s", a)
	}
	if !strings.Contains(p.read("CLAUDE.md"), "<!-- hloop:end -->") {
		t.Fatal("CLAUDE.md should get the block too")
	}
	p.hl("skill", "install", "--agents-md")
	if strings.Count(p.read("AGENTS.md"), "<!-- hloop:start") != 1 {
		t.Fatal("rerunning must update the block, not add a second one")
	}
}
