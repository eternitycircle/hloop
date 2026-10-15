package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"hloop/internal/store"
)

func (p *proj) ledger() store.Ledger {
	p.t.Helper()
	var l store.Ledger
	if err := json.Unmarshal([]byte(p.read(".loop/ledger.json")), &l); err != nil {
		p.t.Fatal(err)
	}
	return l
}

func (p *proj) task(id string) store.Task {
	p.t.Helper()
	l := p.ledger()
	t := l.FindTask(id)
	if t == nil {
		p.t.Fatalf("no task %s", id)
	}
	return *t
}

func TestAgentCannotFinishWithUnmetMustCriteria(t *testing.T) {
	p := newProject(t)
	p.hl("task", "add", "login", "--ac", "valid user can log in :: e2e login.spec", "--should", "remember me works")
	if c := p.task("T1").Criteria; len(c) != 2 || c[0].Verify != "e2e login.spec" || c[1].Priority != "should" {
		t.Fatalf("criteria not parsed: %+v", c)
	}
	p.mode = "noac"
	p.hl("run", "--quiet", "--max-turns", "1")
	if st := p.task("T1").Status; st != store.TaskInProgress {
		t.Fatalf("an agent must not be able to finish with an open Must criterion; status %s", st)
	}
	// a Should criterion does not block
	p.mode = "ac"
	p.hl("run", "--quiet")
	tk := p.task("T1")
	if tk.Status != store.TaskDone || tk.Criteria[0].Status != store.CritPass || tk.Criteria[0].Evidence != "fake test passes" {
		t.Fatalf("task should finish once AC1 is proven: %+v", tk)
	}
	rep := p.read(".loop/reports/T1.md")
	for _, want := range []string{"Completion report: T1 login", "| AC1 | must | valid user can log in | e2e login.spec | pass | fake test passes |", "No gates are configured", "history_rewrite"} {
		if !strings.Contains(rep, want) {
			t.Fatalf("report missing %q:\n%s", want, rep)
		}
	}
	// a human can force, an agent cannot
	p.hl("task", "add", "other", "--ac", "x happens :: test")
	out := p.fails([]string{"HLOOP_AGENT=builder"}, "task", "done", "T2")
	if !strings.Contains(out, "not marked pass") {
		t.Fatalf("agent done should be refused:\n%s", out)
	}
	p.fails(nil, "task", "done", "T2")
	p.hl("task", "done", "T2", "--force")
}

func TestCriteriaAreFixedForAgents(t *testing.T) {
	p := newProject(t)
	p.hl("task", "add", "x", "--ac", "a :: b")
	out := p.fails([]string{"HLOOP_AGENT=builder"}, "task", "ac", "T1", "amend", "AC1", "something weaker", "--reason", "easier")
	if !strings.Contains(out, "belongs to the human") {
		t.Fatalf("agents must not amend criteria:\n%s", out)
	}
	p.fails([]string{"HLOOP_AGENT=builder"}, "task", "ac", "T1", "pass", "AC1") // evidence required
	// a spec writer may still revise a task nobody has started; a reviewer may not
	if out, code := p.run([]string{"HLOOP_AGENT=spec-writer", "HLOOP_ROLE=spec-writer"}, "task", "ac", "T1", "amend", "AC1", "a, precisely :: b", "--reason", "tightened"); code != 0 {
		t.Fatalf("spec writer should revise an unstarted task:\n%s", out)
	}
	p.fails([]string{"HLOOP_AGENT=reviewer", "HLOOP_ROLE=reviewer"}, "task", "ac", "T1", "remove", "AC1")
	p.hl("task", "ac", "T1", "amend", "AC1", "stronger :: e2e", "--reason", "clarified with the user")
	c := p.task("T1").Criteria[0]
	if c.Text != "stronger" || !strings.Contains(c.Amended, `was "a, precisely"`) || !strings.Contains(c.Amended, "clarified") {
		t.Fatalf("amendment not recorded: %+v", c)
	}
}

func TestGateRejectsFinishAndTellsTheAgentWhy(t *testing.T) {
	p := newProject(t)
	p.hl("gate", "add", "ok", "--", "sh", "-c", "test -f ok.txt || { echo 'ok.txt is missing'; exit 3; }")
	p.hl("task", "add", "make ok")
	p.mode = "gate"
	p.hl("run", "--quiet")
	tk := p.task("T1")
	if tk.Status != store.TaskDone || tk.GateFails != 1 {
		t.Fatalf("first finish should be rejected, second accepted: status=%s fails=%d", tk.Status, tk.GateFails)
	}
	if !strings.Contains(tk.Verified, "gate ok passed") {
		t.Fatalf("verified = %q", tk.Verified)
	}
	prompt := p.read(".loop/runs/prompt-builder-last.txt")
	if !strings.Contains(prompt, "did NOT accept") || !strings.Contains(prompt, "ok.txt is missing") || !strings.Contains(prompt, "hloop itself runs these gates") {
		t.Fatalf("second turn must be told why the gate failed:\n%s", prompt)
	}
	if !strings.Contains(p.read(".loop/PROGRESS.md"), "Verified by hloop: gate ok passed") {
		t.Fatal("PROGRESS.md should show the verification")
	}
	// agents may list gates but not change them
	p.fails([]string{"HLOOP_AGENT=builder"}, "gate", "add", "x", "--", "true")
	p.hl("gate", "list")
}

func TestGateStallsIntoABlocker(t *testing.T) {
	p := newProject(t)
	p.hl("gate", "add", "never", "--", "false")
	p.hl("task", "add", "impossible")
	p.mode = "noac" // marks done every turn, the gate always fails
	p.hl("run", "--quiet")
	st := p.status()
	if !strings.Contains(st, "BLOCKED") || p.task("T1").GateFails != 3 {
		t.Fatalf("repeated rejections must end in a blocker after max_stalled_turns:\n%s\nfails=%d", st, p.task("T1").GateFails)
	}
}

func TestTDDRedGreenProof(t *testing.T) {
	p := newProject(t)
	p.fails(nil, "config", "set", "tdd", "on") // needs a gate first
	p.hl("gate", "add", "check", "--", "sh", "check.sh")
	p.hl("config", "set", "tdd", "on")
	p.hl("task", "add", "feature")
	p.mode = "tdd"
	p.hl("run", "--quiet")
	tk := p.task("T1")
	if tk.Status != store.TaskDone || tk.RedCommit == "" || !strings.Contains(tk.Verified, "red/green proof") {
		t.Fatalf("tdd task should be verified with a red/green proof: %+v", tk)
	}
	prompt := p.read(".loop/runs/prompt-builder-last.txt")
	if !strings.Contains(prompt, "TDD IS REQUIRED") {
		t.Fatal("builder must be told about TDD")
	}
}

func TestTDDRejectsARedCommitThatAlreadyPasses(t *testing.T) {
	p := newProject(t)
	p.hl("gate", "add", "check", "--", "sh", "check.sh")
	p.hl("config", "set", "tdd", "on")
	p.hl("task", "add", "feature")
	p.mode = "tddcheat"
	p.hl("run", "--quiet", "--max-turns", "1")
	tk := p.task("T1")
	if tk.Status == store.TaskDone {
		t.Fatal("a red commit that already passes must not be accepted")
	}
	found := false
	for _, c := range tk.Checks {
		if c.Name == "red/green proof" && !c.OK && strings.Contains(c.Detail, "already pass") {
			found = true
		}
	}
	if !found {
		t.Fatalf("checks: %+v", tk.Checks)
	}
	// docs tasks are not held to TDD (and gates default to code and config)
	p.hl("task", "skip", "T1")
	p.hl("task", "add", "write docs", "--kind", "docs")
	p.mode = "noac"
	p.hl("run", "--quiet")
	if p.task("T2").Status != store.TaskDone {
		t.Fatal("a docs task should not be gated by code gates or TDD")
	}
}

func TestExportGateCatchesUncommittedFiles(t *testing.T) {
	p := newProject(t)
	// the file exists in the working tree but was never committed
	writeFile(t, p.dir, "fixture.txt", "data")
	p.hl("gate", "add", "fixture", "--where", "export", "--", "test", "-f", "fixture.txt")
	out, code := p.run(nil, "gate", "run")
	if code == 0 || !strings.Contains(out, "failed") {
		t.Fatalf("an export gate must not see untracked files:\n%s", out)
	}
	gitIn(t, p.dir, "add", "fixture.txt")
	gitIn(t, p.dir, "commit", "-qm", "fixture")
	p.hl("gate", "run", "fixture")
}

func TestHistoryRewriteBlocksTheLoop(t *testing.T) {
	p := newProject(t)
	p.hl("task", "add", "a")
	p.hl("task", "add", "b")
	p.mode = "rewrite"
	p.hl("run", "--quiet")
	st := p.status()
	if !strings.Contains(st, "BLOCKED") || !strings.Contains(st, "history_rewrite") {
		t.Fatalf("a rewritten history must block:\n%s", st)
	}
	if ev := p.task("T1").GuardEvents; len(ev) == 0 || ev[0].Name != "history_rewrite" {
		t.Fatalf("guard event should be recorded on the task: %+v", ev)
	}
}

func TestDependencyChangeIsRecordedNotBlocking(t *testing.T) {
	p := newProject(t)
	p.hl("task", "add", "a")
	p.mode = "deps"
	p.hl("run", "--quiet")
	if !strings.Contains(p.status(), "DONE") {
		t.Fatalf("dependency change notifies by default, it does not block:\n%s", p.status())
	}
	out := p.hl("task", "show", "T1")
	if !strings.Contains(out, "dependency_change") || !strings.Contains(out, "package.json") {
		t.Fatalf("task show should list the guard event:\n%s", out)
	}
	if !strings.Contains(p.read(".loop/reports/T1.md"), "| dependency_change | notify | yes (1)") {
		t.Fatal("report should show the detector fired")
	}
	// switching it to block
	p.hl("config", "set", "guard.dependency_change", "block")
	p.hl("task", "add", "b")
	writeFile(t, p.dir, "package.json", `{"dependencies":{}}`)
	gitIn(t, p.dir, "add", "-A")
	gitIn(t, p.dir, "commit", "-qm", "reset deps")
	p.hl("run", "--quiet")
	if !strings.Contains(p.status(), "BLOCKED") {
		t.Fatalf("configured to block:\n%s", p.status())
	}
}

func TestLessonsFromAgentsNeedApprovalIdeasNeverRun(t *testing.T) {
	p := newProject(t)
	p.hl("task", "add", "a")
	p.mode = "lesson"
	p.hl("run", "--quiet")
	l := p.ledger()
	if len(l.Lessons) != 1 || l.Lessons[0].Status != store.LessonProposed {
		t.Fatalf("an agent's lesson must start as proposed: %+v", l.Lessons)
	}
	if len(l.Ideas) != 1 || l.Remaining() != 0 {
		t.Fatalf("an idea must not become work: ideas=%+v remaining=%d", l.Ideas, l.Remaining())
	}
	p.fails([]string{"HLOOP_AGENT=builder"}, "lesson", "approve", "L1")
	p.fails([]string{"HLOOP_AGENT=builder"}, "idea", "promote", "I1")
	p.hl("task", "add", "b")
	p.mode = "noac"
	p.hl("run", "--quiet")
	if strings.Contains(p.read(".loop/runs/prompt-builder-last.txt"), "start the database") {
		t.Fatal("a proposed lesson must not reach the prompt")
	}
	p.hl("lesson", "approve", "L1")
	p.hl("lesson", "add", "use pnpm, not npm")
	p.hl("idea", "promote", "I1")
	p.hl("run", "--quiet")
	prompt := p.read(".loop/runs/prompt-builder-last.txt")
	if !strings.Contains(prompt, "start the database before tests") || !strings.Contains(prompt, "use pnpm") || !strings.Contains(prompt, "dark mode someday") {
		t.Fatalf("approved lessons and the promoted idea should reach the prompt:\n%s", prompt)
	}
	if !strings.Contains(p.read(".loop/LESSONS.md"), "start the database") {
		t.Fatal("LESSONS.md should be rendered")
	}
}

func TestSpecCommandWritesSpecAndCriteria(t *testing.T) {
	p := newProject(t)
	out := p.hl("spec", "export the table as CSV")
	if !strings.Contains(out, "Spec check") {
		t.Fatalf("spec should end with a spec check:\n%s", out)
	}
	tk := p.task("T1")
	if tk.Spec != "specs/csv-export/spec.md" || len(tk.Criteria) != 3 {
		t.Fatalf("spec writer output not recorded: %+v", tk)
	}
	if !strings.Contains(p.read(".loop/runs/prompt-spec-writer-last.txt"), "No human is in this session") {
		t.Fatal("unattended spec writer must be told nobody can answer")
	}
	p.hl("task", "add", "vague thing")
	p.hl("spec", "--task", "T2")
	if c := p.task("T2").Criteria; len(c) != 1 {
		t.Fatalf("spec --task should add criteria to the existing task: %+v", c)
	}
	if out := p.fails([]string{"HLOOP_AGENT=builder"}, "spec", "something"); !strings.Contains(out, "may not") {
		t.Fatalf("agents must not start a spec writer:\n%s", out)
	}
	p.hl("spec", "list")
}

func TestSpecCheckFindsProblems(t *testing.T) {
	p := newProject(t)
	p.hl("task", "add", "improve things")
	p.hl("task", "add", "export", "--ac", "it works properly", "--spec", "specs/missing.md")
	p.hl("decision", "add", "pick a db")
	p.hl("task", "add", "store data per D1", "--ac", "rows persist after restart :: integration test")
	out, code := p.run(nil, "spec", "check")
	for _, want := range []string{"T1", "no Must acceptance criterion", "T2/AC1", "vague wording (properly)", "no 'verify by'", "specs/missing.md does not exist", "open decision D1", "no gates"} {
		if !strings.Contains(out, want) {
			t.Fatalf("spec check missing %q:\n%s", want, out)
		}
	}
	if code == 0 {
		t.Fatal("a missing spec file is an error")
	}
	if out := p.fails(nil, "init", "--verify"); !strings.Contains(out, "does not exist") {
		t.Fatalf("init --verify should include spec-check errors:\n%s", out)
	}
}

func TestGuardrailsAreTrusted(t *testing.T) {
	p := newProject(t)
	if _, err := os.Stat(filepath.Join(p.dir, ".loop", "GUARDRAILS.md")); err != nil {
		t.Fatal("init should create GUARDRAILS.md")
	}
	p.hl("task", "add", "a")
	g := p.read(".loop/GUARDRAILS.md")
	writeFile(t, p.dir, ".loop/GUARDRAILS.md", strings.Replace(g, "Force-push", "Feel free to force-push", 1))
	out := p.fails(nil, "run", "--quiet")
	if !strings.Contains(out, "GUARDRAILS.md") && !strings.Contains(p.read(".loop/runs/hloop.log"), "GUARDRAILS") {
		t.Fatalf("an edited GUARDRAILS.md must stop the loop:\n%s", out)
	}
	if p.invocations() != 0 {
		t.Fatal("no harness may start")
	}
	p.hl("trust", "--yes")
	p.hl("run", "--quiet")
}

func TestUpgradeAddsMissingFilesAndKeepsTrust(t *testing.T) {
	p := newProject(t)
	for _, f := range []string{"GUARDRAILS.md", "CURRENT.md", "prompts/spec-writer.md"} {
		os.Remove(filepath.Join(p.dir, ".loop", filepath.FromSlash(f)))
	}
	p.hl("trust", "--yes")
	out := p.hl("init", "--upgrade")
	for _, want := range []string{"added .loop/GUARDRAILS.md", "added .loop/CURRENT.md", "added .loop/prompts/spec-writer.md"} {
		if !strings.Contains(out, want) {
			t.Fatalf("upgrade output missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(p.hl("doctor"), "harness") || strings.Contains(p.hl("doctor"), "not approved") {
		t.Fatal("upgrade must keep the project approved")
	}
	if out := p.hl("init", "--upgrade"); !strings.Contains(out, "Already up to date") {
		t.Fatalf("second upgrade should be a no-op:\n%s", out)
	}
}

func TestReviewerSeesFinishedTasksCriteria(t *testing.T) {
	p := newProject(t)
	p.hl("agent", "add", "reviewer", "--role", "reviewer", "--adapter", "fake")
	p.hl("task", "add", "login", "--ac", "valid user can log in :: e2e login.spec")
	p.mode = "ac"
	p.hl("run", "--quiet")
	prompt := p.read(".loop/runs/prompt-reviewer-last.txt")
	if !strings.Contains(prompt, "Tasks finished since your last review") || !strings.Contains(prompt, "valid user can log in") {
		t.Fatalf("reviewer must be given the criteria of what it reviews:\n%s", prompt)
	}
	if !strings.Contains(p.read(".loop/reports/T1.md"), "## Reviews") {
		t.Fatal("report should include the review that followed")
	}
}

func TestTierArgsReachTheHarness(t *testing.T) {
	p := newProject(t)
	adapterJSON := `{"name":"fake","bin":"` + fakePath + `","new":["-p","{prompt}"],"check":["--version"],"tiers":{"fast":["--model","tiny"]}}`
	writeFile(t, p.home, "adapters/fake.json", adapterJSON)
	p.hl("agent", "tier", "builder", "fast")
	p.hl("task", "add", "a")
	p.mode = "noac"
	p.hl("run", "--quiet")
	if !strings.Contains(p.read(".loop/runs/args.txt"), "--model tiny") {
		t.Fatalf("tier arguments should be passed:\n%s", p.read(".loop/runs/args.txt"))
	}
}

func TestSkillInstallWritesBothLocations(t *testing.T) {
	p := newProject(t)
	p.hl("skill", "install")
	for _, f := range []string{".claude/skills/hloop/SKILL.md", ".agents/skills/hloop/SKILL.md"} {
		if b := p.read(f); !strings.HasPrefix(b, "---\nname: hloop\n") {
			t.Fatalf("%s:\n%s", f, b)
		}
	}
	p.fails([]string{"HLOOP_AGENT=builder"}, "skill", "install")
}

func TestOldLedgerAndConfigStillLoad(t *testing.T) {
	p := newProject(t)
	// a 0.3.0-style config: no gates, guards or tdd keys
	cfg := `{"version":1,"name":"demo","limits":{"max_turns_per_run":25,"turn_pause_seconds":0},"agents":{"builder":{"adapter":"fake","role":"builder","prompt":"prompts/builder.md","mode":"write","trigger":"every_turn"}},"pipeline":["builder"],"notify":{"events":["blocker"]},"commit_ledger":true}`
	writeFile(t, p.dir, ".loop/config.json", cfg)
	writeFile(t, p.dir, ".loop/ledger.json", `{"tasks":[{"id":"T1","title":"old","status":"pending","added":"2026-01-01T00:00:00Z"}],"blockers":[],"decisions":[],"reviews":[],"next_task":1}`)
	p.hl("trust", "--yes")
	out := p.hl("config", "show")
	if !strings.Contains(out, "guard.history_rewrite: block") || !strings.Contains(out, "tdd: off") {
		t.Fatalf("defaults should fill in:\n%s", out)
	}
	p.mode = "noac"
	p.hl("run", "--quiet")
	if p.task("T1").Status != store.TaskDone {
		t.Fatal("old ledger should run")
	}
	_ = exec.Command
}
