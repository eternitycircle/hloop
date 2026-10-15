package main

import (
	"os/exec"
	"strings"
	"testing"
)

func gitLog(t *testing.T, dir string) string {
	out, _ := exec.Command("git", "-C", dir, "log", "--format=%s").CombinedOutput()
	return string(out)
}

func TestReleaseChecksThenWritesAndRecords(t *testing.T) {
	p := newProject(t)
	p.hl("gate", "add", "ok", "--", "true")
	p.hl("task", "add", "first feature")
	p.hl("task", "add", "second feature")
	p.mode = "noac"
	p.hl("run", "--quiet", "--max-turns", "1")

	// a pending task stops the release
	out := p.fails(nil, "release", "--yes")
	if !strings.Contains(out, "NOT READY") || !strings.Contains(out, "not finished") {
		t.Fatalf("pending tasks must stop a release:\n%s", out)
	}
	// so do uncommitted changes outside .loop
	writeFile(t, p.dir, "src.txt", "changed\n")
	if out := p.fails(nil, "release", "--yes", "--allow-pending"); !strings.Contains(out, "uncommitted changes") {
		t.Fatalf("dirty tree:\n%s", out)
	}
	gitIn(t, p.dir, "checkout", "--", "src.txt")
	// and a failing gate
	p.hl("gate", "add", "bad", "--", "false")
	if out := p.fails(nil, "release", "--yes", "--allow-pending"); !strings.Contains(out, "gate bad failed") {
		t.Fatalf("failing gate:\n%s", out)
	}
	p.hl("gate", "remove", "bad")
	gitIn(t, p.dir, "add", "-A")
	gitIn(t, p.dir, "commit", "-qm", "loop config", "--allow-empty")

	// dry run changes nothing
	out = p.hl("release", "--dry-run", "--allow-pending")
	if !strings.Contains(out, "# Release brief: demo v0.1.0") || !strings.Contains(out, "FIRST release") || !strings.Contains(out, "T1 first feature") {
		t.Fatalf("dry run brief:\n%s", out)
	}
	if strings.Contains(p.hl("release", "list"), "v0.1.0") {
		t.Fatal("dry run must not record a release")
	}

	// the first release
	p.mode = "ok"
	out = p.hl("release", "--yes", "--allow-pending", "--tag")
	for _, want := range []string{"Released v0.1.0", "docs/releases/v0.1.0.md", "CHANGELOG.md", "docs/INSTALL.md   (1 open [[FILL]]/TODO marker(s) for you)", "Tagged v0.1.0"} {
		if !strings.Contains(out, want) {
			t.Fatalf("release output missing %q:\n%s", want, out)
		}
	}
	brief := p.read(".loop/runs/last-brief.md")
	if !strings.Contains(brief, "T1 first feature") || strings.Contains(brief, "### T2") || !strings.Contains(brief, "`true`") {
		t.Fatalf("brief should cover finished work only, with the gates:\n%s", brief)
	}
	if !strings.Contains(gitLog(t, p.dir), "docs(release): v0.1.0 release notes and documentation") {
		t.Fatalf("documents should be committed:\n%s", gitLog(t, p.dir))
	}
	if tags, _ := exec.Command("git", "-C", p.dir, "tag").CombinedOutput(); !strings.Contains(string(tags), "v0.1.0") {
		t.Fatal("tag missing")
	}
	l := p.ledger()
	if len(l.Releases) != 1 || l.Releases[0].Version != "0.1.0" || len(l.Releases[0].Tasks) != 1 || len(l.Releases[0].Warnings) == 0 {
		t.Fatalf("release record: %+v", l.Releases)
	}
	if !strings.Contains(p.read(".loop/PROGRESS_LOG.md"), "RELEASED v0.1.0") {
		t.Fatal("the log should show the release")
	}
	if out := p.hl("release", "notes"); !strings.Contains(out, "# demo 0.1.0") {
		t.Fatalf("notes:\n%s", out)
	}
	p.fails(nil, "release", "0.1.0", "--yes", "--allow-pending")

	// the next release covers only what came after
	p.mode = "noac"
	p.hl("run", "--quiet")
	gitIn(t, p.dir, "add", "-A")
	gitIn(t, p.dir, "commit", "-qm", "ledger", "--allow-empty")
	p.mode = "ok"
	p.hl("release", "--yes", "--bump", "minor")
	brief = p.read(".loop/runs/last-brief.md")
	if !strings.Contains(brief, "Previous release: v0.1.0") || strings.Contains(brief, "### T1") || !strings.Contains(brief, "### T2 second feature") {
		t.Fatalf("second brief should cover only T2:\n%s", brief)
	}
	if v := p.ledger().Releases[1].Version; v != "0.2.0" {
		t.Fatalf("bump minor from 0.1.0 = %s", v)
	}
	cl := p.read("CHANGELOG.md")
	if !strings.Contains(cl, "[0.2.0]") || !strings.Contains(cl, "[0.1.0]") {
		t.Fatalf("changelog should keep older sections:\n%s", cl)
	}
	if out := p.hl("release", "list"); !strings.Contains(out, "v0.2.0") || !strings.Contains(out, "v0.1.0") {
		t.Fatalf("list:\n%s", out)
	}
}

func TestReleaseWriterIsBoxedAndMustWriteNotes(t *testing.T) {
	p := newProject(t)
	p.hl("task", "add", "a")
	p.mode = "noac"
	p.hl("run", "--quiet")
	gitIn(t, p.dir, "add", "-A")
	gitIn(t, p.dir, "commit", "-qm", "work", "--allow-empty")

	p.mode = "trespass"
	p.hl("release", "--yes")
	if strings.Contains(p.read("src.txt"), "hacked") {
		t.Fatal("the writer changed code; docs mode must revert it")
	}

	p2 := newProject(t)
	p2.hl("task", "add", "a")
	p2.mode = "noac"
	p2.hl("run", "--quiet")
	gitIn(t, p2.dir, "add", "-A")
	gitIn(t, p2.dir, "commit", "-qm", "work", "--allow-empty")
	p2.mode = "nonotes"
	out := p2.fails(nil, "release", "--yes")
	if !strings.Contains(out, "NOT recorded") || !strings.Contains(out, "release notes were not written") {
		t.Fatalf("missing notes must fail the release:\n%s", out)
	}
	if len(p2.ledger().Releases) != 0 {
		t.Fatal("nothing should be recorded")
	}
}

func TestOnlyHumansRelease(t *testing.T) {
	p := newProject(t)
	if out := p.fails([]string{"HLOOP_AGENT=builder"}, "release", "--yes"); !strings.Contains(out, "may not") {
		t.Fatalf("agents must not release:\n%s", out)
	}
	p.hl("release", "list")
	if _, code := p.run([]string{"HLOOP_AGENT=builder"}, "release", "list"); code != 0 {
		t.Fatal("agents may list releases")
	}
}
