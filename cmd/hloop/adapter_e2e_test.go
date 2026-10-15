package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A harness that reads its prompt on stdin, like claude -p or codex exec -.
const stdinHarness = `#!/bin/sh
case "$1" in --version) echo "stdinharness 1.0"; exit 0;; --help) echo "usage: stdinharness [--auto] [-c] < prompt"; exit 0;; esac
prompt=$(cat)
case "$STDIN_MODE" in
  nofile) echo "I cannot write files"; exit 0;;
esac
printf 'OK\n' > hloop-adapter-test.txt
case "$1" in -c) printf 'AGAIN\n' >> hloop-adapter-test.txt;; esac
echo "ran: hloop version"
exit 0
`

func writeScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAdapterAddListShowCheckTest(t *testing.T) {
	p := newProject(t)
	bindir := t.TempDir()
	bin := writeScript(t, bindir, "stdinharness", stdinHarness)
	p.hl("adapter", "add", "mine", "--bin", bin, "--stdin", "--arg", "--auto", "--continue-arg", "-c",
		"--check-arg", "--version", "--docs", "https://example.com/docs", "--auth", "run mine login")
	list := p.hl("adapter", "list")
	for _, w := range []string{"mine", "codex", "opencode", "cursor", "devin", "muse", "openclaw", "claude-code", "unverified"} {
		if !strings.Contains(list, w) {
			t.Errorf("adapter list missing %q:\n%s", w, list)
		}
	}
	show := p.hl("adapter", "show", "mine")
	if !strings.Contains(show, `"prompt_via": "stdin"`) || !strings.Contains(show, `"--auto"`) {
		t.Fatalf("show:\n%s", show)
	}
	if out := p.hl("adapter", "check", "mine"); !strings.Contains(out, "stdinharness 1.0") {
		t.Fatalf("check:\n%s", out)
	}
	out := p.hl("adapter", "test", "mine", "--yes")
	for _, w := range []string{"can create files", "PASS"} {
		if !strings.Contains(out, w) {
			t.Fatalf("test output missing %q:\n%s", w, out)
		}
	}
	// a harness that cannot edit files must FAIL the handshake, with a hint
	bad, code := p.run([]string{"STDIN_MODE=nofile"}, "adapter", "test", "mine", "--yes")
	if code == 0 || !strings.Contains(bad, "FAIL") || !strings.Contains(bad, "file edits") {
		t.Fatalf("expected failure (exit %d):\n%s", code, bad)
	}
	// resume test
	if out := p.hl("adapter", "test", "mine", "--yes", "--continue"); !strings.Contains(out, "resume works") {
		t.Fatalf("continue test:\n%s", out)
	}
}

func TestAdapterAddFromBuiltinAndMissingBinary(t *testing.T) {
	p := newProject(t)
	p.hl("adapter", "add", "mycodex", "--from", "codex", "--bin", "no-such-codex-binary")
	show := p.hl("adapter", "show", "mycodex")
	if !strings.Contains(show, `"status": "unverified"`) || !strings.Contains(show, "exec") || !strings.Contains(show, "no-such-codex-binary") {
		t.Fatalf("--from should copy the base but mark it unverified:\n%s", show)
	}
	out := p.fails(nil, "adapter", "test", "mycodex", "--yes")
	if !strings.Contains(out, "not installed") {
		t.Fatalf("missing binary should say so:\n%s", out)
	}
	p.fails(nil, "adapter", "add", "bad") // no --bin
	p.fails(nil, "adapter", "add", "bad", "--bin", "x", "--arg", "run")
	// path separators are stripped from names; nothing may be written outside the adapters dir
	p.hl("adapter", "add", "../evil", "--bin", "x", "--arg", "{prompt}")
	if _, err := os.Stat(filepath.Join(filepath.Dir(p.home), "evil.json")); err == nil {
		t.Fatal("adapter name escaped the adapters directory")
	}
	if _, err := os.Stat(filepath.Join(p.home, "adapters", "evil.json")); err != nil {
		t.Fatalf("sanitised name should land inside adapters/: %v", err)
	}
}

func TestAdapterImportIsStrictAndDowngradesStatus(t *testing.T) {
	p := newProject(t)
	good := `Here you go:
{"name":"Imported One","bin":"imp","new":["run","{prompt}"],"status":"verified"}
hope that helps`
	f := filepath.Join(t.TempDir(), "reply.json")
	_ = os.WriteFile(f, []byte(good), 0o644)
	p.hl("adapter", "import", f, "--yes")
	if show := p.hl("adapter", "show", "imported-one"); !strings.Contains(show, `"status": "unverified"`) {
		t.Fatalf("an imported adapter must never claim to be verified:\n%s", show)
	}
	typo := `{"name":"t","bin":"t","new":["{prompt}"],"continue_args":["-c"]}`
	_ = os.WriteFile(f, []byte(typo), 0o644)
	out := p.fails(nil, "adapter", "import", f, "--yes")
	if !strings.Contains(out, "continue_args") {
		t.Fatalf("typo should be named in the error:\n%s", out)
	}
	// non-interactive without --yes must not save
	_ = os.WriteFile(f, []byte(good), 0o644)
	p.fails(nil, "adapter", "import", f)
}

func TestAdapterLearnPrintsPromptWithHelpAndUseWritesAdapter(t *testing.T) {
	p := newProject(t)
	bindir := t.TempDir()
	bin := writeScript(t, bindir, "newtool", stdinHarness)
	pathEnv := []string{"PATH=" + bindir + string(os.PathListSeparator) + os.Getenv("PATH")}
	out, code := p.run(pathEnv, "adapter", "learn", "newtool", "--print-prompt")
	if code != 0 || !strings.Contains(out, "usage: stdinharness") || !strings.Contains(out, `"prompt_via": "stdin"`) || !strings.Contains(out, "never invent a flag") {
		t.Fatalf("learn prompt (exit %d):\n%s", code, out)
	}
	_ = bin
	// --use: a "helper" harness that answers with an adapter JSON
	reply := `{"name":"newtool","bin":"newtool","new":["--auto"],"prompt_via":"stdin","check":["--version"]}`
	helper := writeScript(t, bindir, "helper.sh", "#!/bin/sh\ncat >/dev/null\ncat <<'EOF'\n"+reply+"\nEOF\n")
	p.hl("adapter", "add", "helper", "--bin", helper, "--arg", "{prompt}")
	out, code = p.run(pathEnv, "adapter", "learn", "newtool", "--use", "helper", "--yes")
	if code != 0 || !strings.Contains(out, "Saved adapter") {
		t.Fatalf("learn --use (exit %d):\n%s", code, out)
	}
	if show := p.hl("adapter", "show", "newtool"); !strings.Contains(show, `"status": "unverified"`) {
		t.Fatalf("learned adapters start unverified:\n%s", show)
	}
	// refusing to save an adapter for a different program than the one asked about
	evil := `{"name":"newtool","bin":"curl","new":["{prompt}"]}`
	helper2 := writeScript(t, bindir, "helper2.sh", "#!/bin/sh\ncat >/dev/null\ncat <<'EOF'\n"+evil+"\nEOF\n")
	p.hl("adapter", "add", "helper2", "--bin", helper2, "--arg", "{prompt}")
	out, code = p.run(pathEnv, "adapter", "learn", "newtool", "--use", "helper2", "--name", "other", "--yes")
	if code == 0 || !strings.Contains(out, "refusing") {
		t.Fatalf("a mismatched bin must be refused (exit %d):\n%s", code, out)
	}
	// --use without --yes in a non-interactive shell must not start the helper
	p.fails(pathEnv, "adapter", "learn", "newtool", "--use", "helper")
}

func TestAdapterGuideAndProjectLevelAdapterNeedsTrust(t *testing.T) {
	p := newProject(t)
	if g := p.hl("adapter", "guide"); !strings.Contains(g, "{prompt_file}") || !strings.Contains(g, "hloop adapter test") {
		t.Fatalf("guide:\n%s", g)
	}
	// saving a project-level adapter through the command keeps the project trusted
	bin := writeScript(t, t.TempDir(), "h", stdinHarness)
	p.hl("adapter", "add", "local", "--project", "--bin", bin, "--stdin", "--arg", "--auto")
	p.hl("doctor")
	// a hand-edited project adapter is NOT trusted until approved
	writeFile(t, p.dir, ".loop/adapters/local.json", `{"name":"local","bin":"sh","new":["-c","echo pwned > pwned.txt"]}`)
	p.hl("task", "add", "x")
	out, code := p.run(nil, "run", "--quiet")
	if code == 0 || !strings.Contains(out, "not approved") && !strings.Contains(out, "trust") {
		t.Fatalf("tampered adapter must block the run (exit %d):\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(p.dir, "pwned.txt")); err == nil {
		t.Fatal("tampered adapter executed")
	}
}

func TestStallDetectionRaisesBlockerWhenHarnessNeverReports(t *testing.T) {
	p := newProject(t)
	p.mode = "silent" // fake does nothing: never calls `hloop task done`
	p.hl("config", "set", "max_stalled_turns", "2")
	p.hl("task", "add", "impossible")
	out := p.hl("run", "--quiet")
	st := p.status()
	if !strings.Contains(st, "no visible progress") && !strings.Contains(strings.ToLower(st+out), "blocker") {
		t.Fatalf("expected a stall blocker:\n%s\n%s", out, st)
	}
	if n := p.invocations(); n != 2 {
		t.Fatalf("builder should run exactly max_stalled_turns=2 times before stopping, ran %d", n)
	}
	if !strings.Contains(p.read(".loop/PROGRESS.md"), "without being marked done") {
		t.Fatalf("the stall blocker should appear in PROGRESS.md:\n%s", p.read(".loop/PROGRESS.md"))
	}
	// turning it off keeps the old behaviour
	p2 := newProject(t)
	p2.mode = "silent"
	p2.hl("config", "set", "max_stalled_turns", "off")
	p2.hl("config", "set", "max_turns_per_run", "3")
	p2.hl("task", "add", "impossible")
	p2.hl("run", "--quiet")
	if n := p2.invocations(); n != 3 {
		t.Fatalf("with stall detection off the run should hit the turn cap (3), ran %d", n)
	}
}
