// Package engine runs the autonomous loop: it picks the current task, drives
// the configured agents through their adapters, enforces guardrails, tracks
// rate limits and blockers, and tells the human what happened.
package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"hloop/internal/adapter"
	"hloop/internal/notify"
	"hloop/internal/scaffold"
	"hloop/internal/store"
)

// Outcomes of a run.
const (
	OutcomeDone        = "done"         // nothing left to do
	OutcomeBlocked     = "blocked"      // waiting for a human
	OutcomeRateLimited = "rate_limited" // harness quota exhausted
	OutcomeError       = "error"        // repeated failures or bad setup
	OutcomeMaxTurns    = "max_turns"    // hit the per-run turn cap; more work remains
	OutcomePaused      = "paused"
	OutcomeWaiting     = "waiting" // still inside a rate-limit backoff
	OutcomeBusy        = "busy"    // another run holds the lock
	OutcomeInterrupted = "interrupted"
)

// Notifier receives events; *notify.Dispatcher satisfies it.
type Notifier interface{ Send(notify.Event) }

type Engine struct {
	P           store.Project
	Cfg         store.Config
	Name        string // registry name (used for phone buttons)
	N           Notifier
	Log         io.Writer
	Exe         string // path to the hloop binary, exposed to agents as $HLOOP_BIN
	Force       bool   // ignore a rate-limit backoff
	MaxTurns    int    // 0 = from config
	Now         func() time.Time
	Sleep       func(time.Duration)
	LoadAdapter func(name string) (adapter.Adapter, error)
}

// New builds an engine with sensible defaults.
func New(p store.Project, cfg store.Config) *Engine {
	exe, _ := os.Executable()
	name := store.RegisteredName(p.Root)
	if name == "" {
		name = store.SafeName(cfg.Name)
	}
	return &Engine{
		P: p, Cfg: cfg, Name: name, Log: io.Discard, Exe: exe,
		Now: time.Now, Sleep: time.Sleep,
		LoadAdapter: func(n string) (adapter.Adapter, error) { return adapter.Load(n, p.Dir()) },
	}
}

func (e *Engine) logf(format string, a ...any) {
	fmt.Fprintf(e.Log, "[%s] %s\n", e.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, a...))
}

func (e *Engine) enabled(kind string) bool {
	for _, k := range e.Cfg.Notify.Events {
		if k == kind {
			return true
		}
	}
	return false
}

func (e *Engine) notify(kind, title, body, ref string) {
	if e.N == nil || !e.enabled(kind) {
		return
	}
	e.N.Send(notify.Event{Kind: kind, Title: title, Body: body, Project: e.Name, Ref: ref})
}

func (e *Engine) pause(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	done := make(chan struct{})
	go func() { e.Sleep(d); close(done) }()
	select {
	case <-ctx.Done():
	case <-done:
	}
}

// Run executes turns until the work is done, a human is needed, a limit is
// hit, or the per-run turn cap is reached.
func (e *Engine) Run(ctx context.Context) (string, error) {
	if err := e.P.RequireTrust(); err != nil {
		e.notify("error", "Loop needs your approval", "The loop configuration changed or was never approved on this machine. Run `hloop trust --show` on your computer.", "")
		return OutcomeError, err
	}
	if probs := e.Cfg.Validate(); len(probs) > 0 {
		return OutcomeError, fmt.Errorf("invalid config: %s", strings.Join(probs, "; "))
	}
	lock, err := e.P.AcquireRun()
	if err != nil {
		if errors.Is(err, store.ErrBusy) {
			e.logf("another run is active for %s; exiting", e.P.Root)
			return OutcomeBusy, nil
		}
		return OutcomeError, err
	}
	defer lock.Release()

	st := e.P.LoadState()
	now := e.Now()
	if st.Paused {
		e.logf("project is paused (hloop resume to continue)")
		return OutcomePaused, nil
	}
	if !e.Force && st.RetryAfter != nil && st.RetryAfter.After(now) {
		e.logf("rate-limit backoff active until %s; skipping", st.RetryAfter.Local().Format("Jan 2 15:04"))
		return OutcomeWaiting, nil
	}

	runStart := now.UTC()
	_ = e.P.UpdateState(func(s *store.State) {
		s.Status, s.LastRunStart, s.RetryAfter = "running", &runStart, nil
	})
	e.logf("=== run started in %s (%s) ===", e.P.Root, e.Cfg.Name)

	outcome, msg, turns := e.loop(ctx)

	// agents that run once at the end of a run
	if turns > 0 && (outcome == OutcomeDone || outcome == OutcomeMaxTurns || outcome == OutcomeBlocked) {
		e.runEndAgents(ctx)
	}

	end := e.Now().UTC()
	_ = e.P.UpdateState(func(s *store.State) {
		s.LastRunEnd, s.LastOutcome, s.LastMessage = &end, outcome, msg
		switch outcome {
		case OutcomeError:
			s.Status = "error"
		case OutcomeRateLimited:
			s.Status = "rate_limited"
		case OutcomeBlocked:
			s.Status = "blocked"
		case OutcomeDone:
			s.Status = "done"
		default:
			s.Status = "idle"
		}
	})
	e.sendDigest(runStart, outcome, turns)
	e.pruneRuns()
	e.logf("=== run ended: %s %s===", outcome, strings.TrimSpace(msg+" "))
	return outcome, nil
}

func (e *Engine) loop(ctx context.Context) (outcome, msg string, turns int) {
	maxTurns := e.Cfg.Limits.MaxTurnsPerRun
	if e.MaxTurns > 0 {
		maxTurns = e.MaxTurns
	}
	var steer []store.InboxItem
	failures := 0
	disabled := map[string]bool{}

	for turn := 1; turn <= maxTurns; turn++ {
		if ctx.Err() != nil {
			return OutcomeInterrupted, "stopped by signal", turns
		}
		if e.P.LoadState().Paused {
			return OutcomePaused, "paused during run", turns
		}

		// 1. pre-turn: collect steering notes, pick the task
		if items, err := e.P.DrainInbox(); err == nil {
			steer = append(steer, items...)
		}
		var cur store.Task
		var have bool
		head := Head(e.P.Root)
		if err := store.UpdateLedger(e.P, func(l *store.Ledger) error {
			if len(l.OpenBlockers()) > 0 {
				return nil
			}
			if t := l.Promote(); t != nil {
				if t.StartCommit == "" {
					t.StartCommit = head
				}
				cur, have = *t, true
			}
			return nil
		}); err != nil {
			return OutcomeError, err.Error(), turns
		}
		l, _ := store.LoadLedger(e.P)
		if len(l.OpenBlockers()) > 0 {
			return OutcomeBlocked, fmt.Sprintf("%d open blocker(s)", len(l.OpenBlockers())), turns
		}
		if !have {
			e.notify(notify.KindComplete, e.Cfg.Name+": all tasks complete", e.completionBody(), "")
			return OutcomeDone, "all tasks complete", turns
		}
		doneBefore := map[string]bool{}
		for _, t := range l.Tasks {
			if t.Status == store.TaskDone {
				doneBefore[t.ID] = true
			}
		}
		turnStart := e.Now().UTC()

		// 2. the primary agent
		primary := e.Cfg.Pipeline[0]
		e.logf("--- turn %d: %s on %s %s ---", turn, primary, cur.ID, cur.Title)
		res := e.runAgent(ctx, primary, turn, &cur, steer)
		turns++
		_ = e.P.UpdateState(func(s *store.State) { s.TotalTurns++ })

		switch res.Class {
		case "ok":
			failures = 0
			steer = nil
			_ = e.P.UpdateState(func(s *store.State) { s.ConsecutiveFailures = 0 })
		case "rate_limited":
			return e.handleRateLimit(), "harness quota exhausted", turns
		case "missing":
			e.notify(notify.KindError, e.Cfg.Name+": setup problem", res.Err.Error(), "")
			return OutcomeError, res.Err.Error(), turns
		default: // error | timeout
			failures++
			_ = e.P.UpdateState(func(s *store.State) { s.ConsecutiveFailures = failures })
			e.logf("turn failed (%s): %v", res.Class, res.Err)
			if failures >= e.Cfg.Limits.MaxConsecutiveFailures {
				body := fmt.Sprintf("%s failed %d times in a row (%s). Last output:\n%s", primary, failures, res.Class, lastLines(res.Tail, 12))
				e.notify(notify.KindError, e.Cfg.Name+": loop stopped after repeated failures", body, "")
				return OutcomeError, fmt.Sprintf("%d consecutive failures", failures), turns
			}
			e.pause(ctx, time.Duration(e.Cfg.Limits.TurnPauseSeconds)*time.Second)
			continue
		}

		// 3. after the primary: verify any "done" ourselves, record, check
		// blockers, run secondary agents
		verified := e.verifyFinished(ctx, doneBefore)
		milestone := len(verified) > 0
		e.commitLedger(fmt.Sprintf("chore(loop): update ledger after turn %d", turn))
		l, _ = store.LoadLedger(e.P)
		if e.announceBlockers(l, turnStart) {
			return OutcomeBlocked, "agent raised a blocker", turns
		}
		if msg := e.checkStall(cur, milestone); msg != "" {
			l, _ = store.LoadLedger(e.P)
			e.announceBlockers(l, turnStart)
			return OutcomeBlocked, msg, turns
		}
		total := e.P.LoadState().TotalTurns
		for _, name := range e.Cfg.Pipeline[1:] {
			ag := e.Cfg.Agents[name]
			if disabled[name] || !e.due(ag, total, milestone) {
				continue
			}
			r := e.runAgent(ctx, name, turn, nil, nil)
			switch r.Class {
			case "rate_limited":
				e.logf("%s hit its quota; skipping it for the rest of this run", name)
				disabled[name] = true
			case "ok":
				e.commitLedger(fmt.Sprintf("chore(loop): %s update after turn %d", name, turn))
			default:
				e.logf("%s did not complete (%s): %v", name, r.Class, r.Err)
			}
		}
		for _, id := range verified { // reports again, now with the reviews
			_, _ = WriteReport(e.P, e.Cfg, id)
		}
		if len(verified) > 0 {
			e.commitLedger(fmt.Sprintf("chore(loop): completion report for %s", strings.Join(verified, ", ")))
		}
		l, _ = store.LoadLedger(e.P)
		if e.announceBlockers(l, turnStart) {
			return OutcomeBlocked, "a blocker was raised", turns
		}
		if l.Remaining() == 0 {
			e.notify(notify.KindComplete, e.Cfg.Name+": all tasks complete", e.completionBody(), "")
			return OutcomeDone, "all tasks complete", turns
		}
		e.pause(ctx, time.Duration(e.Cfg.Limits.TurnPauseSeconds)*time.Second)
	}
	return OutcomeMaxTurns, fmt.Sprintf("reached the %d-turn cap for one run", maxTurns), turns
}

// checkStall counts consecutive turns spent on one task without finishing it.
// A harness that cannot run `hloop task done` (a sandbox that blocks commands,
// a CLI without a shell tool) would otherwise repeat the same task forever.
// It opens a blocker when the limit is reached and returns a message.
func (e *Engine) checkStall(cur store.Task, finished bool) string {
	limit := e.Cfg.Limits.MaxStalledTurns
	var n int
	_ = e.P.UpdateState(func(s *store.State) {
		if finished || s.StalledTask != cur.ID {
			s.StalledTask, s.StalledTurns = cur.ID, 0
		}
		if !finished {
			s.StalledTurns++
		}
		n = s.StalledTurns
	})
	if limit <= 0 || finished || n < limit {
		return ""
	}
	text := fmt.Sprintf("%s (%s) has had %d turns in a row without being marked done. Either the task is too large, the agent is stuck, or the harness cannot run `hloop task done` (for example a sandbox that blocks commands, or a CLI with no shell tool). Look at the latest logs in .loop/runs/ (hloop logs --agent). Answer with what to change (for example 'split it into smaller tasks' or 'mark it done, it is finished') and the loop will continue.", cur.ID, oneLine(cur.Title, 80), n)
	_ = store.UpdateLedger(e.P, func(l *store.Ledger) error {
		l.AddBlocker(text, "hloop")
		return nil
	})
	_ = e.P.UpdateState(func(s *store.State) { s.StalledTurns = 0 })
	return fmt.Sprintf("%s made no visible progress for %d turns", cur.ID, n)
}

func (e *Engine) due(ag store.Agent, totalTurns int, milestone bool) bool {
	switch ag.Trigger {
	case "every_turn":
		return true
	case "after_milestone":
		return milestone
	case "every_n_turns":
		return ag.EveryN > 0 && totalTurns%ag.EveryN == 0
	}
	return false
}

func (e *Engine) runEndAgents(ctx context.Context) {
	for _, name := range e.Cfg.Pipeline[1:] {
		if ag := e.Cfg.Agents[name]; ag.Trigger == "on_run_end" {
			r := e.runAgent(ctx, name, 0, nil, nil)
			if r.Class != "ok" {
				e.logf("%s (end of run) did not complete (%s): %v", name, r.Class, r.Err)
			}
		}
	}
}

// RunAgent runs one named agent outside the loop (used by `review run`, `plan`
// and `adopt`). extra is appended to the prompt's run context.
func (e *Engine) RunAgent(ctx context.Context, name string, extra string) adapter.Result {
	return e.runAgentWith(ctx, name, 0, nil, nil, extra)
}

func (e *Engine) runAgent(ctx context.Context, name string, turn int, cur *store.Task, steer []store.InboxItem) adapter.Result {
	return e.runAgentWith(ctx, name, turn, cur, steer, "")
}

func (e *Engine) runAgentWith(ctx context.Context, name string, turn int, cur *store.Task, steer []store.InboxItem, instruction string) adapter.Result {
	if err := e.P.RequireTrust(); err != nil {
		return adapter.Result{Class: "error", ExitCode: -1, Err: err, Tail: err.Error()}
	}
	ag, ok := e.Cfg.Agents[name]
	if !ok {
		return adapter.Result{Class: "missing", ExitCode: -1, Err: fmt.Errorf("unknown agent %q", name)}
	}
	ad, err := e.LoadAdapter(ag.Adapter)
	if err != nil {
		return adapter.Result{Class: "missing", ExitCode: -1, Err: err}
	}
	prompt, err := e.buildPrompt(name, ag, turn, cur, steer, instruction)
	if err != nil {
		return adapter.Result{Class: "missing", ExitCode: -1, Err: err}
	}

	var before *snapshot
	guarded := (ag.Mode == "loop-only" || ag.Mode == "docs") && IsGitRepo(e.P.Root)
	if guarded {
		before, _ = takeSnapshot(e.P.Root)
	}
	var point gitPoint
	if ag.Mode == "write" {
		point = takeGitPoint(e.P.Root, e.Cfg.Guards.ProtectedBranches)
	}

	logPath := e.P.Path("runs", fmt.Sprintf("%s-t%d-%s.log", e.Now().Format("20060102-150405"), turn, store.SafeName(name)))
	_ = os.MkdirAll(filepath.Dir(logPath), 0o700)
	lf, lerr := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	var lw io.Writer
	if lerr == nil {
		defer lf.Close()
		lw = lf
	}

	timeout := time.Duration(e.Cfg.Limits.AgentTimeoutMinutes) * time.Minute
	if ag.TimeoutMinutes > 0 {
		timeout = time.Duration(ag.TimeoutMinutes) * time.Minute
	}
	opts := adapter.RunOpts{
		Dir: e.P.Root, Prompt: prompt, Extra: append(ad.TierArgs(ag.Tier), ag.ExtraArgs...), PathExtra: e.Cfg.PathExtra, Log: lw, Timeout: timeout,
		Env: []string{
			"HLOOP_BIN=" + e.Exe, "HLOOP_PROJECT=" + e.P.Root, "HLOOP_AGENT=" + name, "HLOOP_ROLE=" + ag.Role,
			fmt.Sprintf("HLOOP_MAX_REVIEW_CYCLES=%d", maxCycles(ag)),
		},
	}
	if ag.Tier != "" && len(ad.TierArgs(ag.Tier)) == 0 {
		e.logf("note: adapter %s defines no %q tier; using the harness default model", ad.Name, ag.Tier)
	}
	e.logf("running agent %q via %s (log: %s)", name, ad.Name, relOr(e.P.Root, logPath))
	if ag.Session == "continue" && len(ad.Continue) > 0 {
		opts.Continue = true
	}
	res := ad.Run(ctx, opts)
	if opts.Continue && res.Class == "error" {
		e.logf("continuing the previous session failed; starting a fresh one")
		opts.Continue = false
		res = ad.Run(ctx, opts)
	}
	e.logf("agent %q finished: %s in %s", name, res.Class, res.Duration.Round(time.Second))

	if guarded && before != nil {
		if after, err := takeSnapshot(e.P.Root); err == nil {
			rep := enforceGuard(ag.Mode, before, after)
			if !rep.Clean() {
				e.reportViolation(name, rep)
			}
		}
	}
	if point.ok {
		e.handleDetections(name, detect(e.P.Root, point, e.Cfg.Guards))
	}
	if ag.Role == "reviewer" && res.Class == "ok" {
		if h := Head(e.P.Root); h != "" {
			_ = e.P.UpdateState(func(s *store.State) {
				if s.LastReviewed == nil {
					s.LastReviewed = map[string]string{}
				}
				s.LastReviewed[name] = h
				s.LastReviewedCommit = h
			})
		}
	}
	return res
}

func maxCycles(ag store.Agent) int {
	if ag.MaxCycles > 0 {
		return ag.MaxCycles
	}
	return 2
}

func relOr(base, path string) string {
	if r, err := filepath.Rel(base, path); err == nil {
		return r
	}
	return path
}

func (e *Engine) reportViolation(agent string, rep GuardReport) {
	var b strings.Builder
	if len(rep.Reverted) > 0 {
		fmt.Fprintf(&b, "Reverted changes outside .loop/: %s\n", strings.Join(rep.Reverted, ", "))
	}
	if len(rep.Unresolved) > 0 {
		fmt.Fprintf(&b, "Could not revert: %s\n", strings.Join(rep.Unresolved, "; "))
	}
	e.logf("GUARD: agent %q changed files outside its scope. %s", agent, strings.TrimSpace(b.String()))
	e.notify(notify.KindError, fmt.Sprintf("%s: agent %q broke its scope", e.Cfg.Name, agent), b.String(), "")
	if len(rep.Unresolved) > 0 {
		_ = store.UpdateLedger(e.P, func(l *store.Ledger) error {
			l.AddBlocker(fmt.Sprintf("Agent %q changed files outside its allowed scope that could not be reverted automatically: %s. Inspect `git status`/`git log`, clean up, then run `hloop blocker resolve`.", agent, strings.Join(rep.Unresolved, "; ")), "guard")
			return nil
		})
	}
}

// handleRateLimit records the backoff and tells the human.
func (e *Engine) handleRateLimit() string {
	until := e.Now().UTC().Add(time.Duration(e.Cfg.Limits.RateLimitBackoffMinutes) * time.Minute)
	_ = e.P.UpdateState(func(s *store.State) { s.RetryAfter = &until })
	e.notify(notify.KindRateLimited, e.Cfg.Name+": paused, harness quota used up",
		fmt.Sprintf("Will retry automatically after %s. Use `hloop run --force` to try sooner.", until.Local().Format("Jan 2 15:04")), "")
	return OutcomeRateLimited
}

// announceBlockers notifies about blockers opened since `since` and reports
// whether any blocker is open.
func (e *Engine) announceBlockers(l store.Ledger, since time.Time) bool {
	open := l.OpenBlockers()
	for _, b := range open {
		if !b.Opened.Before(since) {
			e.notify(notify.KindBlocker, fmt.Sprintf("%s: needs you (%s)", e.Cfg.Name, b.ID), b.Text, b.ID)
		}
	}
	return len(open) > 0
}

func (e *Engine) completionBody() string {
	l, _ := store.LoadLedger(e.P)
	var b strings.Builder
	fmt.Fprintf(&b, "%d tasks completed.", l.Count(store.TaskDone))
	if od := l.OpenDecisions(); len(od) > 0 {
		fmt.Fprintf(&b, " %d decision(s) still open.", len(od))
	}
	return b.String()
}

func (e *Engine) sendDigest(runStart time.Time, outcome string, turns int) {
	if turns == 0 || outcome == OutcomeDone {
		return
	}
	l, _ := store.LoadLedger(e.P)
	var lines []string
	for _, t := range l.Tasks {
		if t.Status == store.TaskDone && t.Done != nil && !t.Done.Before(runStart) {
			line := fmt.Sprintf("- %s %s", t.ID, t.Title)
			if t.Commit != "" {
				line += " (" + t.Commit + ")"
			}
			if t.Verified != "" {
				line += "\n  " + t.Verified
			}
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 && outcome != OutcomeBlocked {
		return
	}
	if len(lines) > 8 {
		lines = append(lines[:8], fmt.Sprintf("...and %d more", len(lines)-8))
	}
	body := fmt.Sprintf("%d turn(s), outcome: %s.", turns, outcome)
	if len(lines) > 0 {
		body += "\nCompleted:\n" + strings.Join(lines, "\n")
	}
	if l.Remaining() > 0 {
		body += fmt.Sprintf("\n%d task(s) still queued.", l.Remaining())
	}
	e.notify(notify.KindDigest, e.Cfg.Name+": run summary", body, "")
}

// commitLedger commits only the .loop directory so the working tree is clean
// at turn boundaries and progress history lives in git.
func (e *Engine) commitLedger(msg string) {
	if !e.Cfg.CommitLedger || !IsGitRepo(e.P.Root) {
		return
	}
	if out, _ := gitOut(e.P.Root, "status", "--porcelain", "--", store.DirName); out == "" {
		return
	}
	if _, err := gitOut(e.P.Root, "add", "--", store.DirName); err != nil {
		e.logf("ledger commit skipped: %v", err)
		return
	}
	if _, err := gitOut(e.P.Root, "commit", "-m", msg, "--", store.DirName); err != nil {
		e.logf("ledger commit failed (is git user.name/user.email set?): %v", err)
	}
}

// pruneRuns keeps the newest 100 agent logs.
func (e *Engine) pruneRuns() {
	ents, err := os.ReadDir(e.P.Path("runs"))
	if err != nil {
		return
	}
	var files []os.DirEntry
	for _, en := range ents {
		if !en.IsDir() && strings.HasSuffix(en.Name(), ".log") && en.Name() != "hloop.log" {
			files = append(files, en)
		}
	}
	if len(files) <= 100 {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name() < files[j].Name() })
	for _, f := range files[:len(files)-100] {
		os.Remove(e.P.Path("runs", f.Name()))
	}
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// buildPrompt joins the agent's prompt file with a generated run-context block.
func (e *Engine) buildPrompt(name string, ag store.Agent, turn int, cur *store.Task, steer []store.InboxItem, instruction string) (string, error) {
	base := ""
	if ag.Prompt != "" {
		if b, err := os.ReadFile(e.P.Path(filepath.FromSlash(ag.Prompt))); err == nil {
			base = string(b)
		}
	}
	if base == "" {
		base = scaffold.DefaultPrompt(ag.Role)
	}
	if base == "" {
		return "", fmt.Errorf("agent %q has no prompt (looked for .loop/%s)", name, ag.Prompt)
	}
	l, _ := store.LoadLedger(e.P)
	var b strings.Builder
	b.WriteString(strings.TrimRight(base, "\n"))
	b.WriteString("\n\n---\n## Run context (generated by hloop; authoritative)\n")
	fmt.Fprintf(&b, "- Project: %s\n- Agent: %s (role: %s, mode: %s)\n", e.Cfg.Name, name, ag.Role, ag.Mode)
	if turn > 0 {
		fmt.Fprintf(&b, "- Turn: %d\n", turn)
	}
	fmt.Fprintf(&b, "- Working directory: %s\n- hloop executable: %s (also exported as $HLOOP_BIN)\n", e.P.Root, e.Exe)
	if _, err := os.Stat(e.P.Path(store.GuardrailsFile)); err == nil {
		b.WriteString("- Guardrails: .loop/GUARDRAILS.md (owned by the human; what you may do alone, what needs a blocker first, what you must never do). Read it before acting.\n")
	}
	if cur != nil {
		if fresh, err := store.LoadLedger(e.P); err == nil { // pick up criteria marked during earlier turns
			if t := fresh.FindTask(cur.ID); t != nil {
				cur = t
			}
		}
		fmt.Fprintf(&b, "- CURRENT TASK: %s: %s (kind: %s)\n", cur.ID, cur.Title, cur.EffKind())
		if cur.Detail != "" {
			fmt.Fprintf(&b, "  Details:\n  %s\n", strings.ReplaceAll(cur.Detail, "\n", "\n  "))
		}
		if cur.Spec != "" {
			fmt.Fprintf(&b, "  Spec: .loop/%s (read it: scope, out-of-scope, test boundaries, stop conditions)\n", cur.Spec)
		}
		if len(cur.Criteria) > 0 {
			b.WriteString(store.RenderCriteria(cur.Criteria, "  "))
			fmt.Fprintf(&b, "  Mark each criterion as you prove it: `hloop task ac %s pass AC1 --evidence \"<test name, command output or observation>\"` (or `fail`). `hloop task done` is refused while a Must criterion is not marked pass.\n", cur.ID)
		} else {
			b.WriteString("  This task has no recorded acceptance criteria. Before coding, add the checkable ones you will meet: `hloop task ac " + cur.ID + " add \"<verifiable statement> :: <how it is verified>\"`.\n")
		}
		if gs := gatesFor(e.Cfg, cur.EffKind()); len(gs) > 0 {
			var names []string
			for _, g := range gs {
				where := ""
				if g.Where == "export" {
					where = " on a clean copy of the last commit, so commit everything the tests need"
				}
				names = append(names, fmt.Sprintf("%s (`%s`%s)", g.Name, strings.Join(g.Run, " "), where))
			}
			fmt.Fprintf(&b, "  After you run `hloop task done`, hloop itself runs these gates and reopens the task if any fails: %s. Run them yourself first.\n", strings.Join(names, "; "))
		}
		if e.Cfg.TDDOn() && cur.EffKind() == store.KindCode && len(gatesFor(e.Cfg, cur.EffKind())) > 0 {
			b.WriteString("  TDD IS REQUIRED for this task: (1) write tests for the criteria first; (2) run them and see them fail; (3) commit ONLY the tests; (4) record that commit: `hloop task red " + cur.ID + " --commit <hash>`; (5) implement and commit. hloop checks that the gates fail on the red commit and pass at the end, on clean copies of both.\n")
		}
	}
	if ag.Role == "builder" {
		if _, err := os.Stat(e.P.Path("CURRENT.md")); err == nil {
			b.WriteString("- Current state notes: .loop/CURRENT.md. Read it first. At the end of your turn REWRITE it (do not append): what is true now, what is half-done, what the next turn must know. Keep it under about 60 lines.\n")
		}
		if _, err := os.Stat(e.P.Path("context", "map.md")); err == nil {
			b.WriteString("- Codebase map: .loop/context/map.md (kept up to date by the context-curator agent). Read it first; verify against the code before relying on a detail.\n")
		}
	}
	if act := l.LessonsWith(store.LessonActive); len(act) > 0 {
		if len(act) > 15 {
			act = act[len(act)-15:]
		}
		b.WriteString("- Lessons learned on this project (follow them; propose new ones with `hloop lesson add \"...\"`):\n")
		for _, x := range act {
			fmt.Fprintf(&b, "  - %s: %s\n", x.ID, oneLine(x.Text, 300))
		}
	}
	if od := l.OpenDecisions(); len(od) > 0 {
		b.WriteString("- Open decisions (never resolve these yourself):\n")
		for _, d := range od {
			fmt.Fprintf(&b, "  - %s: %s\n", d.ID, d.Topic)
		}
	}
	var answered []store.Blocker
	for _, x := range l.Blockers {
		if x.Resolved {
			answered = append(answered, x)
		}
	}
	if n := len(answered); n > 0 {
		if n > 3 {
			answered = answered[n-3:]
		}
		b.WriteString("- Recent human answers to earlier blockers:\n")
		for _, x := range answered {
			fmt.Fprintf(&b, "  - %s asked: %s\n    answer: %s\n", x.ID, oneLine(x.Text, 300), oneLine(x.Answer, 600))
		}
	}
	if ag.Role == "reviewer" {
		var since time.Time
		for _, r := range l.Reviews {
			if r.Agent == name && r.Time.After(since) {
				since = r.Time
			}
		}
		var recent []store.Task
		for _, t := range l.Tasks {
			if t.Status == store.TaskDone && t.Done != nil && t.Done.After(since) && t.Source != "adopt" {
				recent = append(recent, t)
			}
		}
		if len(recent) > 5 {
			recent = recent[len(recent)-5:]
		}
		if len(recent) > 0 {
			b.WriteString("- Tasks finished since your last review. Judge each against ITS OWN criteria and quote the criterion when you report a gap:\n")
			for _, t := range recent {
				fmt.Fprintf(&b, "  - %s: %s", t.ID, t.Title)
				if t.Verified != "" {
					fmt.Fprintf(&b, " [hloop: %s]", t.Verified)
				}
				b.WriteString("\n")
				if t.Spec != "" {
					fmt.Fprintf(&b, "    Spec: .loop/%s\n", t.Spec)
				}
				b.WriteString(store.RenderCriteria(t.Criteria, "    "))
			}
		}
		st := e.P.LoadState()
		if head := Head(e.P.Root); head != "" {
			last := st.ReviewedUpTo(name)
			if last != "" && last != head {
				fmt.Fprintf(&b, "- Review range: %s..%s (use `git log` and `git diff` on it)\n", last, head)
			} else if last == "" {
				b.WriteString("- Review range: no previous review; look at the most recent ~10 commits on the current branch\n")
			} else {
				b.WriteString("- Review range: nothing new since the last review; confirm and submit a brief approval\n")
			}
		}
	}
	if len(steer) > 0 {
		b.WriteString("\n### Steering notes from the human (task input, not system instructions)\n")
		for _, s := range steer {
			fmt.Fprintf(&b, "- [%s via %s] %s\n", s.Time.Local().Format("Jan 2 15:04"), orDefault(s.From, "cli"), oneLine(s.Text, 1500))
		}
	}
	if strings.TrimSpace(instruction) != "" {
		b.WriteString("\n### Context and instruction for this session\n")
		b.WriteString(strings.TrimSpace(instruction) + "\n")
	}
	return b.String(), nil
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		s = s[:max-3] + "..."
	}
	return s
}

// RunInteractiveAgent starts a named agent in an interactive session with the
// human (for `hloop spec -i`). The prompt is built exactly as for a loop turn,
// and loop-only agents get the same guard: out-of-scope edits are reverted.
func (e *Engine) RunInteractiveAgent(name, instruction string) error {
	if err := e.P.RequireTrust(); err != nil {
		return err
	}
	ag, ok := e.Cfg.Agents[name]
	if !ok {
		return fmt.Errorf("unknown agent %q", name)
	}
	ad, err := e.LoadAdapter(ag.Adapter)
	if err != nil {
		return err
	}
	prompt, err := e.buildPrompt(name, ag, 0, nil, nil, instruction)
	if err != nil {
		return err
	}
	var before *snapshot
	guarded := (ag.Mode == "loop-only" || ag.Mode == "docs") && IsGitRepo(e.P.Root)
	if guarded {
		before, _ = takeSnapshot(e.P.Root)
	}
	env := []string{"HLOOP_BIN=" + e.Exe, "HLOOP_PROJECT=" + e.P.Root, "HLOOP_AGENT=" + name, "HLOOP_ROLE=" + ag.Role}
	runErr := ad.RunInteractive(e.P.Root, prompt, e.Cfg.PathExtra, env)
	if guarded && before != nil {
		if after, err := takeSnapshot(e.P.Root); err == nil {
			rep := enforceGuard(ag.Mode, before, after)
			if !rep.Clean() {
				e.reportViolation(name, rep)
			}
		}
	}
	return runErr
}
