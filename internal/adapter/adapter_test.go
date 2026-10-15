package adapter

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestClassify(t *testing.T) {
	a := Adapter{RateLimitPatterns: []string{"out of tokens"}}
	cases := []struct {
		exit int
		out  string
		want string
	}{
		{0, "rate limit mentioned in a normal run", "ok"},
		{1, "Error 429: Too Many Requests", "rate_limited"},
		{1, "You have hit your usage limit", "rate_limited"},
		{1, "RESOURCE_EXHAUSTED: quota", "rate_limited"},
		{1, "sorry, out of tokens for today", "rate_limited"},
		{1, "segmentation fault", "error"},
		{2, "", "error"},
	}
	for _, c := range cases {
		if got := a.Classify(c.exit, c.out); got != c.want {
			t.Errorf("Classify(%d,%q) = %s, want %s", c.exit, c.out, got, c.want)
		}
	}
}

func TestExpandArgsKeepsPromptAsSingleArgument(t *testing.T) {
	got := ExpandArgs([]string{"-p", "{prompt}", "--flag"}, "do it; rm -rf / && echo \"hi\"")
	if len(got) != 3 || got[1] != "do it; rm -rf / && echo \"hi\"" {
		t.Fatalf("prompt must stay one argv element (no shell): %q", got)
	}
}

func TestBuiltinsAreValid(t *testing.T) {
	for n, a := range Builtin() {
		if err := a.validate(); err != nil {
			t.Errorf("%s: %v", n, err)
		}
	}
}

func TestLoadUserAdapterAndValidation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HLOOP_HOME", home)
	good := Adapter{Name: "mine", Bin: "mycli", New: []string{"run", "{prompt}"}}
	if err := Save(UserDir(), good); err != nil {
		t.Fatal(err)
	}
	got, err := Load("mine", "")
	if err != nil || got.Bin != "mycli" {
		t.Fatalf("load: %v %v", got, err)
	}
	if err := Save(UserDir(), Adapter{Name: "bad", Bin: "x", New: []string{"run"}}); err == nil {
		t.Fatal("adapter without {prompt} or stdin must be rejected")
	}
	if err := Save(UserDir(), Adapter{Name: "stdin", Bin: "x", New: []string{"run"}, PromptVia: "stdin"}); err != nil {
		t.Fatalf("stdin adapter should be valid: %v", err)
	}
	if _, err := Load("nope", ""); err == nil {
		t.Fatal("unknown adapter must error")
	}
	names := List("")
	joined := strings.Join(names, ",")
	for _, w := range []string{"agy", "claude-code", "mine"} {
		if !strings.Contains(joined, w) {
			t.Errorf("List missing %s: %s", w, joined)
		}
	}
}

func TestRunClassesAndTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	dir := t.TempDir()
	ctx := context.Background()
	ok := Adapter{Name: "t", Bin: "sh", New: []string{"-c", "echo hi $0", "{prompt}"}}
	if r := ok.Run(ctx, RunOpts{Dir: dir, Prompt: "world"}); r.Class != "ok" || !strings.Contains(r.Tail, "hi world") {
		t.Fatalf("ok run: %+v", r)
	}
	rl := Adapter{Name: "t", Bin: "sh", New: []string{"-c", "echo 'Error 429 rate limit' >&2; exit 1", "{prompt}"}}
	if r := rl.Run(ctx, RunOpts{Dir: dir, Prompt: "x"}); r.Class != "rate_limited" || r.ExitCode != 1 {
		t.Fatalf("rate limit run: %+v", r)
	}
	bad := Adapter{Name: "t", Bin: "sh", New: []string{"-c", "echo kaboom; exit 3", "{prompt}"}}
	if r := bad.Run(ctx, RunOpts{Dir: dir, Prompt: "x"}); r.Class != "error" || r.ExitCode != 3 {
		t.Fatalf("error run: %+v", r)
	}
	slow := Adapter{Name: "t", Bin: "sh", New: []string{"-c", "sleep 5", "{prompt}"}}
	start := time.Now()
	if r := slow.Run(ctx, RunOpts{Dir: dir, Prompt: "x", Timeout: 300 * time.Millisecond}); r.Class != "timeout" {
		t.Fatalf("timeout run: %+v", r)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("timeout did not stop the process promptly")
	}
	missing := Adapter{Name: "t", Bin: "definitely-not-installed-xyz", New: []string{"{prompt}"}}
	if r := missing.Run(ctx, RunOpts{Dir: dir, Prompt: "x"}); r.Class != "missing" {
		t.Fatalf("missing run: %+v", r)
	}
}

func TestRunPassesWorkingDirAndEnvAndStdin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	dir := t.TempDir()
	a := Adapter{Name: "t", Bin: "sh", New: []string{"-c", "pwd; echo $HLOOP_X; cat"}, PromptVia: "stdin"}
	r := a.Run(context.Background(), RunOpts{Dir: dir, Prompt: "from-stdin", Env: []string{"HLOOP_X=envval"}})
	real, _ := filepath.EvalSymlinks(dir)
	if r.Class != "ok" || !strings.Contains(r.Tail, "envval") || !strings.Contains(r.Tail, "from-stdin") || !strings.Contains(r.Tail, filepath.Base(real)) {
		t.Fatalf("got %+v", r)
	}
}

func TestResolveUsesExtraPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses chmod")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "mytool-xyz")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho v1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := Adapter{Name: "t", Bin: "mytool-xyz", New: []string{"{prompt}"}, Check: []string{"--version"}}
	if _, err := a.Resolve(nil); err == nil {
		t.Fatal("should not be found without the extra path")
	}
	out, err := a.CheckInstalled([]string{dir})
	if err != nil || !strings.Contains(out, "v1") {
		t.Fatalf("check: %q %v", out, err)
	}
}

func TestBuiltinCatalogHasPopularHarnesses(t *testing.T) {
	b := Builtin()
	for _, n := range []string{"claude-code", "codex", "opencode", "gemini-cli", "copilot-cli", "cursor", "devin", "muse", "openclaw", "agy"} {
		a, ok := b[n]
		if !ok {
			t.Errorf("missing built-in adapter %s", n)
			continue
		}
		if a.Status == "" {
			t.Errorf("%s has no status label", n)
		}
		if a.Docs == "" && n != "agy" { // agy has no public docs page to link
			t.Errorf("%s has no docs link", n)
		}
		if len(a.Check) == 0 {
			t.Errorf("%s has no check args", n)
		}
	}
	if b["claude-code"].PromptVia != "stdin" || b["codex"].PromptVia != "stdin" {
		t.Error("claude-code and codex must read the prompt on stdin (no argv length / quoting limits)")
	}
}

func TestParseIsStrict(t *testing.T) {
	if _, err := Parse([]byte(`{"name":"x","bin":"x","new":["{prompt}"],"continue_args":["-c"]}`)); err == nil {
		t.Fatal("a misspelled field must be rejected, not silently dropped")
	}
	a, err := Parse([]byte(`{"name":"x","bin":"x","new":["{prompt}"],"alt_bins":["y"]}`))
	if err != nil || len(a.Bins) != 1 {
		t.Fatalf("valid adapter: %v %v", a, err)
	}
	home := t.TempDir()
	t.Setenv("HLOOP_HOME", home)
	_ = os.MkdirAll(UserDir(), 0o755)
	_ = os.WriteFile(filepath.Join(UserDir(), "typo.json"), []byte(`{"bin":"x","new":["{prompt}"],"promptvia":"stdin"}`), 0o644)
	if _, err := Load("typo", ""); err == nil || !strings.Contains(err.Error(), "guide") {
		t.Fatalf("Load must reject unknown fields with a pointer to the guide, got %v", err)
	}
}

func TestPromptFilePlaceholder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "reader.sh")
	// prints the contents of the file named by $1
	_ = os.WriteFile(script, []byte("#!/bin/sh\ncat \"$1\"\n"), 0o755)
	a := Adapter{Name: "pf", Bin: script, New: []string{"{prompt_file}"}}
	if err := a.validate(); err != nil {
		t.Fatalf("{prompt_file} alone must satisfy validation: %v", err)
	}
	prompt := "line one\nline \"two\" with $dollar and 'quotes'\n"
	res := a.Run(context.Background(), RunOpts{Dir: dir, Prompt: prompt, Timeout: 20 * time.Second})
	if res.Class != "ok" || !strings.Contains(res.Tail, "line \"two\" with $dollar") {
		t.Fatalf("prompt file not delivered intact: %s %q", res.Class, res.Tail)
	}
	// the temp prompt file is removed after the run
	m, _ := filepath.Glob(filepath.Join(os.TempDir(), "hloop-prompt-*"))
	for _, f := range m {
		if b, _ := os.ReadFile(f); strings.Contains(string(b), "line one") {
			t.Errorf("prompt file left behind: %s", f)
		}
	}
}

func TestAltBinsAndWarnings(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	dir := t.TempDir()
	alt := filepath.Join(dir, "agent")
	_ = os.WriteFile(alt, []byte("#!/bin/sh\necho hi\n"), 0o755)
	a := Adapter{Name: "c", Bin: "cursor-agent-does-not-exist", Bins: []string{"agent"}, New: []string{"{prompt}"}}
	got, err := a.Resolve([]string{dir})
	if err != nil || got != alt {
		t.Fatalf("alt_bins should be tried in order: %q %v", got, err)
	}
	if _, err := (Adapter{Name: "c", Bin: "nope-1", Bins: []string{"nope-2"}, New: []string{"{prompt}"}}).Resolve(nil); err == nil {
		t.Fatal("no candidate installed must error")
	}
	other := "windows"
	if runtime.GOOS == "windows" {
		other = "linux"
	}
	w := Adapter{Name: "m", Bin: "m", OS: []string{other}, New: []string{"{prompt}"}}.Warnings("/x/m")
	if len(w) != 1 || !strings.Contains(w[0], other+" only") {
		t.Fatalf("OS restriction should warn: %v", w)
	}
	if w := (Adapter{Name: "m", Bin: "m", New: []string{"{prompt}"}}).Warnings("/x/m"); len(w) != 0 {
		t.Fatalf("no warnings expected: %v", w)
	}
}

func TestUnsafeAdapterNamesRejected(t *testing.T) {
	t.Setenv("HLOOP_HOME", t.TempDir())
	for _, n := range []string{"../evil", "a/b", "", "x y", `..\x`} {
		if _, err := Load(n, ""); err == nil {
			t.Errorf("Load(%q) should fail", n)
		}
		if err := Save(UserDir(), Adapter{Name: n, Bin: "x", New: []string{"{prompt}"}}); err == nil {
			t.Errorf("Save(%q) should fail", n)
		}
	}
	if err := Save(UserDir(), Adapter{Name: "ok", Bin: "two words", New: []string{"{prompt}"}}); err == nil {
		t.Error("executable names with spaces must be rejected")
	}
}
