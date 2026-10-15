// Package store holds everything hloop persists: per-project config, the
// ledger (tasks, blockers, decisions, reviews), runner state, the inbox, the
// global project registry and the global notification settings.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// DirName is the hidden directory inside a project that holds all hloop files.
const DirName = ".loop"

const (
	ConfigFile = "config.json"
	LedgerFile = "ledger.json"
	StateFile  = "state.json"

	// PRDFile is the Product Requirements Document. Before 0.5.0 it was
	// called PROJECT_SCOPE.md; projects that still have that file keep working.
	PRDFile         = "PRD.md"
	LegacyScopeFile = "PROJECT_SCOPE.md"
)

// PRDName returns the name of the project's requirements document inside
// .loop/: PRD.md, or PROJECT_SCOPE.md for a project not yet upgraded.
func (p Project) PRDName() string {
	if _, err := os.Stat(p.Path(PRDFile)); err != nil {
		if _, err := os.Stat(p.Path(LegacyScopeFile)); err == nil {
			return LegacyScopeFile
		}
	}
	return PRDFile
}

var ErrNoProject = errors.New("no .loop project found here (run `hloop init` or `hloop adopt` in your repository)")

// Project identifies a project by its root directory.
type Project struct{ Root string }

func (p Project) Dir() string { return filepath.Join(p.Root, DirName) }

func (p Project) Path(parts ...string) string {
	return filepath.Join(append([]string{p.Dir()}, parts...)...)
}

// Exists reports whether the project has been initialised.
func (p Project) Exists() bool {
	_, err := os.Stat(p.Path(ConfigFile))
	return err == nil
}

// Find walks upward from start until it finds a directory containing .loop/config.json.
func Find(start string) (Project, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return Project{}, err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, DirName, ConfigFile)); err == nil {
			return Project{Root: dir}, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return Project{}, ErrNoProject
		}
		dir = parent
	}
}

// ConfigHome is where global (per-user) settings live. HLOOP_HOME overrides it.
func ConfigHome() string {
	if h := os.Getenv("HLOOP_HOME"); h != "" {
		return h
	}
	d, err := os.UserConfigDir()
	if err != nil || d == "" {
		home, _ := os.UserHomeDir()
		d = filepath.Join(home, ".config")
	}
	return filepath.Join(d, "harness-loop")
}

// ---------- low-level file helpers (cross-platform, stdlib only) ----------

func writeAtomic(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	_ = os.Chmod(name, perm)
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// WriteFile atomically writes a file (exported for other packages).
func WriteFile(path string, data []byte, perm os.FileMode) error {
	return writeAtomic(path, data, perm)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func writeJSON(path string, v any, perm os.FileMode) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(b, '\n'), perm)
}

// withFileLock serialises short critical sections across processes using an
// O_EXCL lock file. Locks older than 30s are treated as stale.
func withFileLock(path string, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			f.Close()
			defer os.Remove(path)
			return fn()
		}
		if !os.IsExist(err) {
			return err
		}
		if st, e := os.Stat(path); e == nil && time.Since(st.ModTime()) > 30*time.Second {
			os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for lock %s", path)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// ---------- per-project configuration ----------

type Limits struct {
	MaxTurnsPerRun          int `json:"max_turns_per_run"`
	TurnPauseSeconds        int `json:"turn_pause_seconds"`
	RateLimitBackoffMinutes int `json:"rate_limit_backoff_minutes"`
	MaxConsecutiveFailures  int `json:"max_consecutive_failures"`
	AgentTimeoutMinutes     int `json:"agent_timeout_minutes"`
	MaxStalledTurns         int `json:"max_stalled_turns"` // turns on one task without finishing it before asking a human; 0 disables
}

// Agent is one participant in the project's pipeline.
//
// Mode "write" lets the agent change the project; "loop-only" means it may
// only change files under .loop/; "docs" adds docs/ and the project's root
// markdown files (hloop reverts and reports anything else).
// Trigger is one of: every_turn, after_milestone, every_n_turns, on_run_end, manual.
type Agent struct {
	Adapter        string   `json:"adapter"`
	Role           string   `json:"role"` // builder | reviewer | planner | custom
	Prompt         string   `json:"prompt"`
	Mode           string   `json:"mode"`
	Trigger        string   `json:"trigger"`
	EveryN         int      `json:"every_n,omitempty"`
	Session        string   `json:"session,omitempty"` // fresh (default) | continue
	ExtraArgs      []string `json:"extra_args,omitempty"`
	TimeoutMinutes int      `json:"timeout_minutes,omitempty"`
	MaxCycles      int      `json:"max_cycles,omitempty"` // reviewers: change-request rounds before escalating
	Tier           string   `json:"tier,omitempty"`       // fast | standard | critical: mapped to model flags by the adapter
}

// Tiers an agent can ask for. The adapter decides what each means (for
// Claude Code: haiku, sonnet, opus). Empty means the harness default.
func ValidTier(t string) bool {
	switch t {
	case "", "fast", "standard", "critical":
		return true
	}
	return false
}

// Gate is a check hloop runs itself, without asking an agent, before it
// accepts that a task is finished. Run is an argv array: no shell is involved
// unless you name one (for example ["sh", "-c", "npm ci && npm test"]).
type Gate struct {
	Name           string   `json:"name"`
	Run            []string `json:"run"`
	Where          string   `json:"where,omitempty"`           // worktree (default) | export: a clean copy of the last commit
	TimeoutMinutes int      `json:"timeout_minutes,omitempty"` // default 20
	Kinds          []string `json:"kinds,omitempty"`           // task kinds it applies to; default code and config
}

// AppliesTo reports whether the gate runs for a task of the given kind.
func (g Gate) AppliesTo(kind string) bool {
	if kind == "" {
		kind = KindCode
	}
	if len(g.Kinds) == 0 {
		return kind == KindCode || kind == KindConfig
	}
	for _, k := range g.Kinds {
		if k == kind || k == "all" {
			return true
		}
	}
	return false
}

// Guard actions.
const (
	GuardBlock  = "block"  // stop the loop and ask the human
	GuardNotify = "notify" // tell the human, keep going
	GuardOff    = "off"
)

// Guards are checks hloop makes on git after every agent turn. They are the
// detectors for the high-risk rows in GUARDRAILS.md: an agent that ignores its
// instructions still gets caught.
type Guards struct {
	ProtectedBranches []string `json:"protected_branches,omitempty"` // default main, master
	HistoryRewrite    string   `json:"history_rewrite,omitempty"`    // earlier commits vanished from the branch (default block)
	ProtectedCommit   string   `json:"protected_commit,omitempty"`   // new commits on a protected branch (default notify)
	DependencyChange  string   `json:"dependency_change,omitempty"`  // a manifest or lock file changed (default notify)
	MassDelete        string   `json:"mass_delete,omitempty"`        // many files deleted in one turn (default notify)
	MassDeleteCount   int      `json:"mass_delete_count,omitempty"`  // default 10
}

func validGuardAction(s string) bool { return s == GuardBlock || s == GuardNotify || s == GuardOff }

type NotifyCfg struct {
	Events []string `json:"events"` // blocker, complete, rate_limited, error, review, digest, check
}

type Config struct {
	Version      int              `json:"version"`
	Name         string           `json:"name"`
	Limits       Limits           `json:"limits"`
	Agents       map[string]Agent `json:"agents"`
	Pipeline     []string         `json:"pipeline"` // pipeline[0] is the primary (builder) agent
	Notify       NotifyCfg        `json:"notify"`
	RemoteTasks  string           `json:"remote_tasks"` // "approve" (default) or "auto": tasks added from a phone
	AutoResume   bool             `json:"auto_resume"`  // start a run right after a blocker is answered
	CommitLedger bool             `json:"commit_ledger"`
	PathExtra    []string         `json:"path_extra,omitempty"`

	// Added in 0.4.0.
	Gates  []Gate `json:"gates,omitempty"`
	TDD    string `json:"tdd,omitempty"` // off (default) | on: code tasks must record a failing tests-only commit first
	Guards Guards `json:"guards"`
}

// TDDOn reports whether the red/green rule is enforced.
func (c Config) TDDOn() bool { return c.TDD == "on" }

func DefaultLimits() Limits {
	return Limits{
		MaxTurnsPerRun:          25,
		TurnPauseSeconds:        5,
		RateLimitBackoffMinutes: 300,
		MaxConsecutiveFailures:  2,
		AgentTimeoutMinutes:     120,
		MaxStalledTurns:         3,
	}
}

func DefaultConfig(name, adapter string) Config {
	return Config{
		Version: 1,
		Name:    name,
		Limits:  DefaultLimits(),
		Agents: map[string]Agent{
			"builder": {Adapter: adapter, Role: "builder", Prompt: "prompts/builder.md", Mode: "write", Trigger: "every_turn", Session: "fresh"},
		},
		Pipeline:     []string{"builder"},
		Notify:       NotifyCfg{Events: []string{"blocker", "complete", "rate_limited", "error", "review", "digest", "check"}},
		RemoteTasks:  "approve",
		AutoResume:   true,
		CommitLedger: true,
	}
}

func (c *Config) fillDefaults() {
	d := DefaultLimits()
	if c.Limits.MaxTurnsPerRun <= 0 {
		c.Limits.MaxTurnsPerRun = d.MaxTurnsPerRun
	}
	if c.Limits.TurnPauseSeconds < 0 {
		c.Limits.TurnPauseSeconds = d.TurnPauseSeconds
	}
	if c.Limits.RateLimitBackoffMinutes <= 0 {
		c.Limits.RateLimitBackoffMinutes = d.RateLimitBackoffMinutes
	}
	if c.Limits.MaxConsecutiveFailures <= 0 {
		c.Limits.MaxConsecutiveFailures = d.MaxConsecutiveFailures
	}
	if c.Limits.MaxStalledTurns == 0 {
		c.Limits.MaxStalledTurns = d.MaxStalledTurns // -1 disables
	}
	if c.Limits.AgentTimeoutMinutes <= 0 {
		c.Limits.AgentTimeoutMinutes = d.AgentTimeoutMinutes
	}
	if c.RemoteTasks == "" {
		c.RemoteTasks = "approve"
	}
	if c.Agents == nil {
		c.Agents = map[string]Agent{}
	}
	g := &c.Guards
	if g.ProtectedBranches == nil {
		g.ProtectedBranches = []string{"main", "master"}
	}
	if g.HistoryRewrite == "" {
		g.HistoryRewrite = GuardBlock
	}
	if g.ProtectedCommit == "" {
		g.ProtectedCommit = GuardNotify
	}
	if g.DependencyChange == "" {
		g.DependencyChange = GuardNotify
	}
	if g.MassDelete == "" {
		g.MassDelete = GuardNotify
	}
	if g.MassDeleteCount <= 0 {
		g.MassDeleteCount = 10
	}
	if c.TDD == "" {
		c.TDD = "off"
	}
}

func (p Project) LoadConfig() (Config, error) {
	var c Config
	if err := readJSON(p.Path(ConfigFile), &c); err != nil {
		if os.IsNotExist(err) {
			return c, ErrNoProject
		}
		return c, err
	}
	c.fillDefaults()
	for _, g := range c.Gates {
		if !ValidName(g.Name) || len(g.Run) == 0 {
			return c, fmt.Errorf("%s: gate %q is invalid; fix it with `hloop gate list` / `hloop gate remove`", p.Path(ConfigFile), g.Name)
		}
	}
	for n, a := range c.Agents {
		if !ValidName(n) || (a.Adapter != "" && !ValidName(a.Adapter)) || !ValidPromptPath(a.Prompt) {
			return c, fmt.Errorf("%s: agent %q has an unsafe name, adapter or prompt path; fix it with `hloop config show` / `hloop agent remove`", p.Path(ConfigFile), n)
		}
	}
	return c, nil
}

func (p Project) SaveConfig(c Config) error { return writeJSON(p.Path(ConfigFile), c, 0o644) }

// Validate returns human-readable problems with a configuration.
func (c Config) Validate() []string {
	var out []string
	if len(c.Pipeline) == 0 {
		out = append(out, "pipeline is empty: add at least a builder agent")
	}
	for n, a := range c.Agents {
		if !ValidName(n) {
			out = append(out, fmt.Sprintf("agent name %q is invalid (lowercase letters, digits, . _ - only)", n))
		}
		if a.Adapter != "" && !ValidName(a.Adapter) {
			out = append(out, fmt.Sprintf("agent %q: adapter name %q is invalid", n, a.Adapter))
		}
		if !ValidPromptPath(a.Prompt) {
			out = append(out, fmt.Sprintf("agent %q: prompt path %q must stay inside .loop/", n, a.Prompt))
		}
	}
	for _, n := range c.Pipeline {
		a, ok := c.Agents[n]
		if !ok {
			out = append(out, fmt.Sprintf("pipeline references unknown agent %q", n))
			continue
		}
		if a.Adapter == "" {
			out = append(out, fmt.Sprintf("agent %q has no adapter", n))
		}
		if a.Mode != "write" && a.Mode != "loop-only" && a.Mode != "docs" {
			out = append(out, fmt.Sprintf("agent %q has invalid mode %q (use write, docs or loop-only)", n, a.Mode))
		}
		switch a.Trigger {
		case "every_turn", "after_milestone", "every_n_turns", "on_run_end", "manual":
		default:
			out = append(out, fmt.Sprintf("agent %q has invalid trigger %q", n, a.Trigger))
		}
		if a.Trigger == "every_n_turns" && a.EveryN <= 0 {
			out = append(out, fmt.Sprintf("agent %q uses every_n_turns but every_n is not set", n))
		}
	}
	for n, a := range c.Agents {
		if !ValidTier(a.Tier) {
			out = append(out, fmt.Sprintf("agent %q has invalid tier %q (fast, standard or critical)", n, a.Tier))
		}
	}
	seenGate := map[string]bool{}
	for _, g := range c.Gates {
		if !ValidName(g.Name) {
			out = append(out, fmt.Sprintf("gate name %q is invalid", g.Name))
		}
		if seenGate[g.Name] {
			out = append(out, fmt.Sprintf("gate %q is defined twice", g.Name))
		}
		seenGate[g.Name] = true
		if len(g.Run) == 0 || strings.TrimSpace(g.Run[0]) == "" {
			out = append(out, fmt.Sprintf("gate %q has no command", g.Name))
		}
		if g.Where != "" && g.Where != "worktree" && g.Where != "export" {
			out = append(out, fmt.Sprintf("gate %q: where must be worktree or export", g.Name))
		}
		for _, k := range g.Kinds {
			if k != "all" && (k == "" || !ValidKind(k)) {
				out = append(out, fmt.Sprintf("gate %q: unknown task kind %q", g.Name, k))
			}
		}
	}
	if c.TDD != "" && c.TDD != "on" && c.TDD != "off" {
		out = append(out, "tdd must be on or off")
	}
	if c.TDDOn() && len(c.Gates) == 0 {
		out = append(out, "tdd is on but no gates are defined: hloop needs a test gate to check the red and green commits (hloop gate add test -- <your test command>)")
	}
	for _, a := range []string{c.Guards.HistoryRewrite, c.Guards.ProtectedCommit, c.Guards.DependencyChange, c.Guards.MassDelete} {
		if a != "" && !validGuardAction(a) {
			out = append(out, fmt.Sprintf("guard action %q is invalid (block, notify or off)", a))
		}
	}
	if len(c.Pipeline) > 0 {
		if a, ok := c.Agents[c.Pipeline[0]]; ok && a.Trigger != "every_turn" {
			out = append(out, "the first pipeline agent must use trigger every_turn")
		}
	}
	return out
}

// ---------- global project registry ----------

type RegEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

func registryPath() string { return filepath.Join(ConfigHome(), "projects.json") }

var badNameChars = regexp.MustCompile(`[^a-z0-9._-]+`)

// SafeName turns arbitrary text into a registry-safe project name. The result
// never starts with a dot or contains "..", so it is safe inside file names.
func SafeName(s string) string {
	s = badNameChars.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-")
	for strings.Contains(s, "..") {
		s = strings.ReplaceAll(s, "..", ".")
	}
	s = strings.Trim(s, "-.")
	if s == "" {
		s = "project"
	}
	return s
}

var validNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,47}$`)

// ValidName reports whether s is acceptable as an agent or adapter name. Such
// names become file names, so path separators and leading dots are refused.
func ValidName(s string) bool { return validNameRe.MatchString(s) && !strings.Contains(s, "..") }

// ValidPromptPath reports whether a prompt path stays inside the .loop folder.
func ValidPromptPath(s string) bool {
	if s == "" {
		return true
	}
	if filepath.IsAbs(s) || strings.HasPrefix(s, "/") || strings.HasPrefix(s, `\`) || filepath.VolumeName(s) != "" {
		return false
	}
	if len(s) >= 2 && s[1] == ':' { // drive-letter path, whatever OS we are on
		return false
	}
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return false
		}
	}
	return true
}

func LoadRegistry() ([]RegEntry, error) {
	var r []RegEntry
	if err := readJSON(registryPath(), &r); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return r, nil
}

// Register adds (or updates) a project and returns the name it was stored under.
func Register(name, root string) (string, error) {
	name = SafeName(name)
	var final string
	err := withFileLock(registryPath()+".lock", func() error {
		r, err := LoadRegistry()
		if err != nil {
			return err
		}
		for _, e := range r {
			if e.Path == root {
				final = e.Name
				return nil
			}
		}
		final = name
		for i := 2; ; i++ {
			clash := false
			for _, e := range r {
				if e.Name == final {
					clash = true
				}
			}
			if !clash {
				break
			}
			final = fmt.Sprintf("%s-%d", name, i)
		}
		r = append(r, RegEntry{Name: final, Path: root})
		return writeJSON(registryPath(), r, 0o644)
	})
	return final, err
}

func Unregister(name string) error {
	return withFileLock(registryPath()+".lock", func() error {
		r, err := LoadRegistry()
		if err != nil {
			return err
		}
		var out []RegEntry
		found := false
		for _, e := range r {
			if e.Name == name {
				found = true
				continue
			}
			out = append(out, e)
		}
		if !found {
			return fmt.Errorf("no registered project named %q", name)
		}
		return writeJSON(registryPath(), out, 0o644)
	})
}

func Lookup(name string) (Project, error) {
	r, err := LoadRegistry()
	if err != nil {
		return Project{}, err
	}
	for _, e := range r {
		if e.Name == name {
			return Project{Root: e.Path}, nil
		}
	}
	return Project{}, fmt.Errorf("no registered project named %q", name)
}

// RegisteredName returns the registry name for a project root, if any.
func RegisteredName(root string) string {
	r, _ := LoadRegistry()
	for _, e := range r {
		if e.Path == root {
			return e.Name
		}
	}
	return ""
}

// ---------- global notification settings (contain secrets: 0600) ----------

type NtfyCfg struct {
	Server string `json:"server"`
	Topic  string `json:"topic"`
	Token  string `json:"token,omitempty"`
}

type TelegramCfg struct {
	Token       string `json:"token"`
	BotUsername string `json:"bot_username"`
	UserID      int64  `json:"user_id"`
	ChatID      int64  `json:"chat_id"`
}

type Global struct {
	Ntfy      NtfyCfg     `json:"ntfy"`
	Telegram  TelegramCfg `json:"telegram"`
	NoDesktop bool        `json:"no_desktop,omitempty"` // disable local desktop notifications
}

func globalPath() string { return filepath.Join(ConfigHome(), "notify.json") }

func LoadGlobal() (Global, error) {
	var g Global
	if err := readJSON(globalPath(), &g); err != nil && !os.IsNotExist(err) {
		return g, err
	}
	return g, nil
}

func SaveGlobal(g Global) error { return writeJSON(globalPath(), g, 0o600) }

// MsgRef links a Telegram message to the thing it announced, so a reply to it
// can be routed (for example, answering a blocker).
type MsgRef struct {
	Project string `json:"project"`
	Ref     string `json:"ref"`
}

// TGState is the bot's mutable state (kept apart from the secrets file).
type TGState struct {
	Offset   int64             `json:"offset"`
	Current  string            `json:"current,omitempty"`
	Messages map[string]MsgRef `json:"messages,omitempty"`
}

func tgStatePath() string { return filepath.Join(ConfigHome(), "telegram-state.json") }

func LoadTGState() TGState {
	var s TGState
	_ = readJSON(tgStatePath(), &s)
	if s.Messages == nil {
		s.Messages = map[string]MsgRef{}
	}
	return s
}

func SaveTGState(s TGState) error {
	if len(s.Messages) > 300 { // keep the map small
		s.Messages = map[string]MsgRef{}
	}
	return writeJSON(tgStatePath(), s, 0o600)
}

// UpdateTGState loads the bot state fresh, applies fn, and saves it under a
// lock, so the runner and the listener can both touch it safely.
func UpdateTGState(fn func(*TGState)) error {
	return withFileLock(tgStatePath()+".lock", func() error {
		s := LoadTGState()
		fn(&s)
		return SaveTGState(s)
	})
}
