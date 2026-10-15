// Package adapter describes how to drive a coding-agent CLI (a "harness")
// non-interactively and classifies how a run ended. Adapters are data, not
// code: argv arrays with a {prompt} placeholder. No shell, no eval.
package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Adapter is one harness CLI definition.
type Adapter struct {
	Name              string            `json:"name"`
	Display           string            `json:"display,omitempty"`
	Bin               string            `json:"bin"`
	Bins              []string          `json:"alt_bins,omitempty"`            // other executable names to try if bin is not found
	OS                []string          `json:"os,omitempty"`                  // supported operating systems (linux, darwin, windows); empty means any
	Docs              string            `json:"docs,omitempty"`                // where the harness documents its headless mode
	Auth              string            `json:"auth,omitempty"`                // how to sign in for unattended use (shown by doctor/show)
	New               []string          `json:"new"`                           // fresh session, non-interactive
	Continue          []string          `json:"continue,omitempty"`            // resume most recent session
	Interactive       []string          `json:"interactive,omitempty"`         // interactive session with an initial prompt
	Check             []string          `json:"check,omitempty"`               // e.g. ["--version"]
	PromptVia         string            `json:"prompt_via,omitempty"`          // "arg" (default) or "stdin"
	RateLimitPatterns []string          `json:"rate_limit_patterns,omitempty"` // extra substrings
	Env               map[string]string `json:"env,omitempty"`
	SkipsPermissions  bool              `json:"skips_permissions,omitempty"` // true if unattended mode disables approval prompts
	Status            string            `json:"status,omitempty"`            // verified | unverified (informational)
	Notes             string            `json:"notes,omitempty"`
	// Tiers maps an agent's tier (fast, standard, critical) to extra arguments
	// that select a model, e.g. {"fast": ["--model", "haiku"]}. A tier with no
	// entry uses the harness's default model.
	Tiers map[string][]string `json:"tiers,omitempty"`
}

// TierArgs returns the extra arguments for a tier (nil if none are defined).
func (a Adapter) TierArgs(tier string) []string {
	if tier == "" || a.Tiers == nil {
		return nil
	}
	return append([]string(nil), a.Tiers[tier]...)
}

// defaultRateLimitPatterns are matched (case-insensitively) against the tail
// of the output, but only when the process exited non-zero.
var defaultRateLimitPatterns = []string{
	"rate limit", "rate-limit", "ratelimit", "usage limit", "quota",
	"too many requests", "resource_exhausted", "limit reached", "credit", "429",
}

// Builtin returns the adapters that ship with hloop.
//
// Status "verified" means hloop's authors ran a loop with that harness.
// "flags-checked" means every flag was confirmed against the installed CLI's
// --help (and the vendor's headless documentation) but no loop was run, because
// that needs the vendor's account. "documented" means the flags come from the
// vendor's documentation only. Use `hloop adapter test <name>` to check one on
// your machine in about a minute.
func Builtin() map[string]Adapter {
	return map[string]Adapter{
		"agy": {
			Name: "agy", Display: "Antigravity (agy)", Bin: "agy",
			New:              []string{"-p", "{prompt}", "--print-timeout", "0", "--dangerously-skip-permissions"},
			Continue:         []string{"-c", "-p", "{prompt}", "--print-timeout", "0", "--dangerously-skip-permissions"},
			Check:            []string{"--version"},
			SkipsPermissions: true,
			Status:           "documented",
			Notes:            "Flags taken from the original run_antigravity.sh, which ran agy in practice; hloop's own tests could not run the real agy. Prove it with `hloop adapter test agy`. No interactive mode is defined; add one with `hloop adapter add` if your agy supports it.",
		},
		"claude-code": {
			Name: "claude-code", Display: "Claude Code", Bin: "claude", PromptVia: "stdin",
			New:              []string{"-p", "--dangerously-skip-permissions"},
			Continue:         []string{"-c", "-p", "--dangerously-skip-permissions"},
			Interactive:      []string{"{prompt}"},
			Check:            []string{"--version"},
			SkipsPermissions: true,
			Status:           "verified",
			Docs:             "https://docs.claude.com/en/docs/claude-code/headless",
			Auth:             "run `claude` once and sign in, or set ANTHROPIC_API_KEY",
			Tiers:            map[string][]string{"fast": {"--model", "haiku"}, "standard": {"--model", "sonnet"}, "critical": {"--model", "opus"}},
			Notes:            "Verified end to end on Linux with Claude Code 2.1.294 (builder, reviewer, blocker, timeouts). The prompt is sent on stdin, which avoids command-line length limits and keeps it out of the process list. Consider --max-budget-usd via an agent's extra_args.",
		},
		"codex": {
			Name: "codex", Display: "OpenAI Codex CLI", Bin: "codex", PromptVia: "stdin",
			New:              []string{"exec", "--dangerously-bypass-approvals-and-sandbox", "--skip-git-repo-check", "-"},
			Continue:         []string{"exec", "resume", "--last", "--dangerously-bypass-approvals-and-sandbox", "--skip-git-repo-check", "-"},
			Interactive:      []string{"{prompt}"},
			Check:            []string{"--version"},
			SkipsPermissions: true,
			Status:           "flags-checked",
			Docs:             "https://developers.openai.com/codex/noninteractive",
			Auth:             "run `codex login` once, or set CODEX_API_KEY",
			Notes:            "Flags confirmed against codex-cli 0.162.1 --help. Uses the bypass flag because the default read-only/workspace sandboxes block git commits and network. For a sandboxed variant copy this adapter (`hloop adapter show codex`) and replace the bypass flag with `--sandbox workspace-write`.",
		},
		"opencode": {
			Name: "opencode", Display: "OpenCode", Bin: "opencode",
			New:              []string{"run", "--auto", "{prompt}"},
			Continue:         []string{"run", "--auto", "--continue", "{prompt}"},
			Interactive:      []string{"--prompt", "{prompt}"},
			Check:            []string{"--version"},
			SkipsPermissions: true,
			Status:           "flags-checked",
			Docs:             "https://opencode.ai/docs/cli",
			Auth:             "run `opencode auth login`, or set your provider's API key variable",
			Notes:            "Flags confirmed against opencode 1.18.35 --help. --auto auto-approves permissions that are not explicitly denied in your opencode config. Older versions lack --auto; use the OPENCODE_PERMISSION environment variable instead (see the opencode docs).",
		},
		"gemini-cli": {
			Name: "gemini-cli", Display: "Gemini CLI", Bin: "gemini",
			New:              []string{"--yolo", "-p", "{prompt}"},
			Continue:         []string{"--yolo", "--resume", "latest", "-p", "{prompt}"},
			Interactive:      []string{"-i", "{prompt}"},
			Check:            []string{"--version"},
			SkipsPermissions: true,
			Status:           "flags-checked",
			Docs:             "https://github.com/google-gemini/gemini-cli/blob/main/docs/cli/headless.md",
			Auth:             "run `gemini` once and sign in, or set GEMINI_API_KEY",
			Env:              map[string]string{"GEMINI_CLI_TRUST_WORKSPACE": "true"},
			Notes:            "Flags confirmed against gemini 0.63.0 --help. Gemini silently ignores --yolo in a folder it does not trust (it falls back to asking for approval, which hangs an unattended run); GEMINI_CLI_TRUST_WORKSPACE=true marks the project folder trusted for these runs. Exit code 53 means the turn limit was hit.",
		},
		"copilot-cli": {
			Name: "copilot-cli", Display: "GitHub Copilot CLI", Bin: "copilot",
			New:              []string{"-p", "{prompt}", "--allow-all-tools"},
			Continue:         []string{"--continue", "-p", "{prompt}", "--allow-all-tools"},
			Interactive:      []string{"-i", "{prompt}"},
			Check:            []string{"--version"},
			SkipsPermissions: true,
			Status:           "flags-checked",
			Docs:             "https://docs.github.com/en/copilot/concepts/agents/about-copilot-cli",
			Auth:             "run `copilot` once and use /login, or set a GitHub token with Copilot access",
			Notes:            "Flags confirmed against GitHub Copilot CLI 1.0.95 --help. --allow-all-tools lets it run any tool without asking; add --allow-all-paths if it must touch files outside the project.",
		},
		"cursor": {
			Name: "cursor", Display: "Cursor CLI", Bin: "cursor-agent", Bins: []string{"agent"},
			New:              []string{"-p", "--force", "{prompt}"},
			Interactive:      []string{"{prompt}"},
			Check:            []string{"--version"},
			SkipsPermissions: true,
			Status:           "documented",
			Docs:             "https://cursor.com/docs/cli/headless",
			Auth:             "run `cursor-agent login`, or set CURSOR_API_KEY",
			Notes:            "From Cursor's headless documentation (not run here: the installer is not available in hloop's build environment). The binary is `cursor-agent` on older installs and `agent` on newer ones; both are tried. The docs describe no session-resume flag, so every turn starts fresh and relies on the ledger for continuity.",
		},
		"devin": {
			Name: "devin", Display: "Devin for Terminal", Bin: "devin",
			New:              []string{"-p", "{prompt}", "--permission-mode", "dangerous"},
			Continue:         []string{"--continue", "-p", "{prompt}", "--permission-mode", "dangerous"},
			Check:            []string{"--version"},
			SkipsPermissions: true,
			Status:           "documented",
			Docs:             "https://cli.devin.ai",
			Auth:             "run `devin configure` and paste your API token once",
			Notes:            "From vendor and sandbox-provider documentation (not run here). Third-party docs also call the permission mode `bypass`; if `dangerous` is rejected, edit the adapter. This is the local terminal CLI; the cloud Devin web agent has no command-line mode (see docs/ADAPTERS.md, wrapper scripts).",
		},
		"muse": {
			Name: "muse", Display: "Muse Code (Meta)", Bin: "muse", OS: []string{"linux", "darwin"},
			New:              []string{"exec", "--yolo", "{prompt}"},
			Check:            []string{"--version"},
			SkipsPermissions: true,
			Status:           "documented",
			Docs:             "https://docs.e2b.dev/agents/muse",
			Auth:             "run `muse login`, or set META_API_KEY",
			Notes:            "From sandbox-provider documentation (not run here). --yolo disables Muse's own OS sandbox as well as approvals. No native Windows build; use WSL2. No documented resume flag, so each turn starts fresh.",
		},
		"openclaw": {
			Name: "openclaw", Display: "OpenClaw", Bin: "openclaw",
			New:    []string{"agent", "exec", "{prompt}"},
			Check:  []string{"--version"},
			Status: "documented",
			Docs:   "https://docs.openclaw.ai/cli/agent",
			Auth:   "configure a model provider with `openclaw` onboarding; the agent runs through your OpenClaw gateway",
			Notes:  "EXPERIMENTAL. `agent exec` is described in OpenClaw's 2026.9 documentation but is absent from 2026.6.35, the version hloop checked. OpenClaw is a general assistant rather than a coding-first harness and its approval model is not documented for headless runs, so review what it is allowed to do. On older versions try `hloop adapter add` with: agent --local --agent main --message {prompt}.",
		},
	}
}

// Load resolves an adapter by name: project-level file, then user-level file,
// then built-ins.
func Load(name, projectDir string) (Adapter, error) {
	if !safeName(name) {
		return Adapter{}, fmt.Errorf("invalid adapter name %q", name)
	}
	candidates := []string{}
	if projectDir != "" {
		candidates = append(candidates, filepath.Join(projectDir, "adapters", name+".json"))
	}
	candidates = append(candidates, filepath.Join(userAdapterDir(), name+".json"))
	for _, c := range candidates {
		b, err := os.ReadFile(c)
		if err != nil {
			continue
		}
		a, err := Parse(b)
		if err != nil {
			return a, fmt.Errorf("%s: %w", c, err)
		}
		if a.Name == "" {
			a.Name = name
		}
		return a, a.validate()
	}
	if a, ok := Builtin()[name]; ok {
		return a, nil
	}
	return Adapter{}, fmt.Errorf("unknown adapter %q (see `hloop adapter list`; add your own with `hloop adapter learn` or `hloop adapter guide`)", name)
}

// Parse decodes adapter JSON strictly: a misspelled field is an error rather
// than silently ignored, because a dropped "continue" or "stdin" changes how
// the harness is run.
func Parse(b []byte) (Adapter, error) {
	var a Adapter
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return a, fmt.Errorf("%w (run `hloop adapter guide` for the field reference)", err)
	}
	return a, nil
}

func userAdapterDir() string {
	if h := os.Getenv("HLOOP_HOME"); h != "" {
		return filepath.Join(h, "adapters")
	}
	d, err := os.UserConfigDir()
	if err != nil {
		home, _ := os.UserHomeDir()
		d = filepath.Join(home, ".config")
	}
	return filepath.Join(d, "harness-loop", "adapters")
}

// UserDir exposes the user-level adapter directory.
func UserDir() string { return userAdapterDir() }

func (a Adapter) validate() error {
	if a.Bin == "" {
		return fmt.Errorf("adapter %q has no bin", a.Name)
	}
	if len(a.New) == 0 {
		return fmt.Errorf("adapter %q has no \"new\" command", a.Name)
	}
	if a.PromptVia != "" && a.PromptVia != "arg" && a.PromptVia != "stdin" {
		return fmt.Errorf("adapter %q: prompt_via must be arg or stdin", a.Name)
	}
	if a.PromptVia != "stdin" && !containsPlaceholder(a.New) {
		return fmt.Errorf("adapter %q: \"new\" must contain {prompt} or {prompt_file} (or set prompt_via to stdin)", a.Name)
	}
	for _, b := range append([]string{a.Bin}, a.Bins...) {
		if b == "" || strings.ContainsAny(b, " \t\n") {
			return fmt.Errorf("adapter %q: executable name %q must be a single word (arguments go in \"new\")", a.Name, b)
		}
	}
	return nil
}

func containsPlaceholder(args []string) bool {
	for _, s := range args {
		if strings.Contains(s, "{prompt}") || strings.Contains(s, "{prompt_file}") {
			return true
		}
	}
	return false
}

// List returns all known adapter names (built-in plus user and project files).
func List(projectDir string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(n string) {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	for n := range Builtin() {
		add(n)
	}
	for _, dir := range []string{userAdapterDir(), filepath.Join(projectDir, "adapters")} {
		ents, _ := os.ReadDir(dir)
		for _, e := range ents {
			if strings.HasSuffix(e.Name(), ".json") {
				add(strings.TrimSuffix(e.Name(), ".json"))
			}
		}
	}
	return out
}

func safeName(n string) bool {
	if n == "" || len(n) > 48 || strings.Contains(n, "..") || strings.HasPrefix(n, ".") {
		return false
	}
	for _, r := range n {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

// Save writes an adapter definition to a directory.
func Save(dir string, a Adapter) error {
	if !safeName(a.Name) {
		return fmt.Errorf("invalid adapter name %q (lowercase letters, digits, . _ - only)", a.Name)
	}
	if err := a.validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(a, "", "  ")
	return os.WriteFile(filepath.Join(dir, a.Name+".json"), append(b, '\n'), 0o644)
}

// ---------- running ----------

// Result describes how a harness run ended.
type Result struct {
	Class    string // ok | rate_limited | error | timeout | missing
	ExitCode int
	Tail     string
	Duration time.Duration
	Err      error
}

type RunOpts struct {
	Dir       string
	Prompt    string
	Continue  bool
	Extra     []string
	Env       []string // extra KEY=VALUE entries
	PathExtra []string
	Log       io.Writer
	Timeout   time.Duration
}

// ExpandArgs substitutes {prompt} in an argv template.
func ExpandArgs(tmpl []string, prompt string) []string { return expandArgs(tmpl, prompt, "") }

// expandArgs substitutes {prompt_file} first (a path, never user text) and then {prompt}.
func expandArgs(tmpl []string, prompt, promptFile string) []string {
	out := make([]string, 0, len(tmpl))
	for _, s := range tmpl {
		s = strings.ReplaceAll(s, "{prompt_file}", promptFile)
		out = append(out, strings.ReplaceAll(s, "{prompt}", prompt))
	}
	return out
}

func usesPromptFile(tmpl []string) bool {
	for _, s := range tmpl {
		if strings.Contains(s, "{prompt_file}") {
			return true
		}
	}
	return false
}

// writePromptFile stores the prompt in a private temporary file.
func writePromptFile(prompt string) (string, error) {
	f, err := os.CreateTemp("", "hloop-prompt-*.md")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if err := os.Chmod(f.Name(), 0o600); err != nil && runtime.GOOS != "windows" {
		os.Remove(f.Name())
		return "", err
	}
	if _, err := f.WriteString(prompt); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// DefaultPathExtra lists directories where harness CLIs are commonly installed.
// Cron, launchd and Task Scheduler start with a minimal PATH, so hloop adds these.
func DefaultPathExtra() []string {
	home, _ := os.UserHomeDir()
	dirs := []string{
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, "bin"),
		filepath.Join(home, ".npm-global", "bin"),
		filepath.Join(home, ".cargo", "bin"),
		filepath.Join(home, "go", "bin"),
		"/usr/local/bin", "/opt/homebrew/bin",
	}
	if runtime.GOOS == "windows" {
		if ad := os.Getenv("APPDATA"); ad != "" {
			dirs = append(dirs, filepath.Join(ad, "npm"))
		}
		if la := os.Getenv("LOCALAPPDATA"); la != "" {
			dirs = append(dirs, filepath.Join(la, "Programs"))
		}
	}
	return dirs
}

// PrepareCmd gives a command the same treatment as a harness run: its own
// process group, killed as a whole on cancellation (used for hloop's gates).
func PrepareCmd(cmd *exec.Cmd) { prepare(cmd) }

// AugmentedPath is PATH with the project's path_extra entries in front.
func AugmentedPath(extra []string) string { return augmentedPath(extra) }

func augmentedPath(extra []string) string {
	cur := os.Getenv("PATH")
	parts := append([]string{}, extra...)
	parts = append(parts, DefaultPathExtra()...)
	if cur != "" {
		parts = append(parts, cur)
	}
	return strings.Join(parts, string(os.PathListSeparator))
}

// Resolve finds the adapter binary using the augmented PATH.
func (a Adapter) Resolve(extra []string) (string, error) {
	old := os.Getenv("PATH")
	os.Setenv("PATH", augmentedPath(extra))
	defer os.Setenv("PATH", old)
	var firstErr error
	for _, name := range append([]string{a.Bin}, a.Bins...) {
		p, err := exec.LookPath(name)
		if err == nil {
			return p, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return "", firstErr
}

// Warnings lists reasons this adapter may misbehave on this machine.
func (a Adapter) Warnings(resolvedBin string) []string {
	var out []string
	if len(a.OS) > 0 {
		ok := false
		for _, o := range a.OS {
			if o == runtime.GOOS {
				ok = true
			}
		}
		if !ok {
			out = append(out, fmt.Sprintf("%s supports %s only, not %s", a.Name, strings.Join(a.OS, "/"), runtime.GOOS))
		}
	}
	low := strings.ToLower(resolvedBin)
	if runtime.GOOS == "windows" && (strings.HasSuffix(low, ".cmd") || strings.HasSuffix(low, ".bat")) && a.PromptVia != "stdin" && !usesPromptFile(a.New) {
		out = append(out, a.Name+" is installed as a .cmd script; Windows passes long or multi-line prompts through cmd.exe unreliably. Prefer an adapter that reads the prompt from stdin or {prompt_file}")
	}
	return out
}

// tailBuffer keeps the last N bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

// Classify decides how a finished run should be treated.
func (a Adapter) Classify(exit int, tail string) string {
	if exit == 0 {
		return "ok"
	}
	low := strings.ToLower(tail)
	for _, p := range append(append([]string{}, defaultRateLimitPatterns...), a.RateLimitPatterns...) {
		if p != "" && strings.Contains(low, strings.ToLower(p)) {
			return "rate_limited"
		}
	}
	return "error"
}

// Run executes the harness once. It never uses a shell.
func (a Adapter) Run(ctx context.Context, o RunOpts) Result {
	bin, err := a.Resolve(o.PathExtra)
	if err != nil {
		return Result{Class: "missing", ExitCode: -1, Err: fmt.Errorf("%s not found on PATH: %w", a.Bin, err)}
	}
	tmpl := a.New
	if o.Continue && len(a.Continue) > 0 {
		tmpl = a.Continue
	}
	var args []string
	var stdin io.Reader
	promptFile := ""
	if usesPromptFile(tmpl) {
		pf, err := writePromptFile(o.Prompt)
		if err != nil {
			return Result{Class: "error", ExitCode: -1, Err: fmt.Errorf("cannot write the prompt file: %w", err)}
		}
		promptFile = pf
		defer os.Remove(pf)
	}
	if a.PromptVia == "stdin" {
		args = append(args, expandArgs(tmpl, "", promptFile)...)
		stdin = strings.NewReader(o.Prompt)
	} else {
		args = expandArgs(tmpl, o.Prompt, promptFile)
	}
	args = append(args, o.Extra...)

	if o.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.Timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	prepare(cmd)
	cmd.Dir = o.Dir
	cmd.Stdin = stdin
	env := append(os.Environ(), "PATH="+augmentedPath(o.PathExtra))
	for k, v := range a.Env {
		env = append(env, k+"="+v)
	}
	cmd.Env = append(env, o.Env...)

	tail := &tailBuffer{max: 8 * 1024}
	var w io.Writer = tail
	if o.Log != nil {
		w = io.MultiWriter(o.Log, tail)
	}
	cmd.Stdout, cmd.Stderr = w, w

	start := time.Now()
	runErr := cmd.Run()
	res := Result{Duration: time.Since(start), Tail: tail.String()}
	switch {
	case runErr == nil:
		res.Class = "ok"
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		res.Class, res.ExitCode, res.Err = "timeout", -1, fmt.Errorf("timed out after %s", o.Timeout)
	default:
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			res.ExitCode = ee.ExitCode()
		} else {
			res.ExitCode = -1
		}
		res.Class = a.Classify(res.ExitCode, res.Tail)
		res.Err = runErr
	}
	return res
}

// RunInteractive attaches the harness to the user's terminal.
func (a Adapter) RunInteractive(dir, prompt string, pathExtra []string, env []string) error {
	if len(a.Interactive) == 0 {
		return fmt.Errorf("adapter %q has no interactive command defined", a.Name)
	}
	bin, err := a.Resolve(pathExtra)
	if err != nil {
		return fmt.Errorf("%s not found on PATH: %w", a.Bin, err)
	}
	cmd := exec.Command(bin, ExpandArgs(a.Interactive, prompt)...)
	cmd.Dir = dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = append(append(os.Environ(), "PATH="+augmentedPath(pathExtra)), env...)
	return cmd.Run()
}

// CheckInstalled runs the adapter's check command and returns its output.
func (a Adapter) CheckInstalled(pathExtra []string) (string, error) {
	bin, err := a.Resolve(pathExtra)
	if err != nil {
		return "", fmt.Errorf("%s not found on PATH", a.Bin)
	}
	if len(a.Check) == 0 {
		return bin, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, a.Check...)
	cmd.Env = append(os.Environ(), "PATH="+augmentedPath(pathExtra))
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return strings.TrimSpace(out.String()), fmt.Errorf("check failed: %w", err)
	}
	return strings.TrimSpace(out.String()), nil
}
