package engine

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"hloop/internal/adapter"
	"hloop/internal/notify"
	"hloop/internal/store"
)

// This file holds the checks hloop makes itself, without trusting an agent's
// word: gates (commands the human configured) run when a task is marked done,
// the TDD red/green proof, and the acceptance-criteria rule.

// GateRun is the outcome of running one gate once.
type GateRun struct {
	Gate     string
	OK       bool
	Exit     int
	TimedOut bool
	Tail     string
	Duration time.Duration
	Where    string // worktree | export@<commit>
	Err      error  // set when the gate could not be started at all
}

func (g GateRun) Summary() string {
	switch {
	case g.Err != nil:
		return fmt.Sprintf("%s could not run (%v)", g.Gate, g.Err)
	case g.TimedOut:
		return fmt.Sprintf("%s timed out after %s", g.Gate, g.Duration.Round(time.Second))
	case g.OK:
		return fmt.Sprintf("%s passed (%s, %s)", g.Gate, g.Where, g.Duration.Round(time.Second))
	}
	return fmt.Sprintf("%s failed with exit code %d (%s)", g.Gate, g.Exit, g.Where)
}

// RunGate runs one gate in dir. dir is the project root for worktree gates or
// an exported copy for export gates.
func RunGate(ctx context.Context, g store.Gate, dir, where string, pathExtra []string) GateRun {
	r := GateRun{Gate: g.Name, Where: where}
	if len(g.Run) == 0 {
		r.Err = fmt.Errorf("no command")
		return r
	}
	timeout := time.Duration(g.TimeoutMinutes) * time.Minute
	if timeout <= 0 {
		timeout = 20 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	bin := g.Run[0]
	// Resolve the program with the project's extra PATH entries, the way the
	// harness itself is found.
	if !strings.ContainsAny(bin, `/\`) {
		old := os.Getenv("PATH")
		os.Setenv("PATH", adapter.AugmentedPath(pathExtra))
		if p, err := exec.LookPath(bin); err == nil {
			bin = p
		}
		os.Setenv("PATH", old)
	}
	cmd := exec.CommandContext(ctx, bin, g.Run[1:]...)
	adapter.PrepareCmd(cmd)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+adapter.AugmentedPath(pathExtra), "HLOOP_GATE="+g.Name, "CI=true")
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	start := time.Now()
	err := cmd.Run()
	r.Duration = time.Since(start)
	r.Tail = tailString(buf.String(), 3000)
	if ctx.Err() == context.DeadlineExceeded {
		r.TimedOut = true
		r.Exit = -1
		return r
	}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			r.Exit = ee.ExitCode()
			return r
		}
		r.Err = err
		r.Exit = -1
		return r
	}
	r.OK = true
	return r
}

func tailString(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		s = "..." + s[len(s)-n:]
	}
	return s
}

// ExportCommit writes a clean copy of commit (tracked files only) into a new
// temporary directory. It returns the project directory inside the copy and a
// function that removes the copy.
func ExportCommit(repo, commit string) (string, func(), error) {
	dir, tmp, err := exportCommit(repo, commit)
	if err != nil {
		return "", func() {}, err
	}
	return dir, func() { os.RemoveAll(tmp) }, nil
}

func exportCommit(repo, commit string) (string, string, error) {
	top, err := gitOut(repo, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", "", err
	}
	if r, err := filepath.EvalSymlinks(repo); err == nil {
		repo = r
	}
	if r, err := filepath.EvalSymlinks(top); err == nil {
		top = r
	}
	tmp, err := os.MkdirTemp("", "hloop-export-")
	if err != nil {
		return "", "", err
	}
	cmd := exec.Command("git", "archive", "--format=tar", commit)
	cmd.Dir = top
	var errb bytes.Buffer
	cmd.Stderr = &errb
	out, err := cmd.StdoutPipe()
	if err != nil {
		os.RemoveAll(tmp)
		return "", "", err
	}
	if err := cmd.Start(); err != nil {
		os.RemoveAll(tmp)
		return "", "", err
	}
	xerr := untar(out, tmp)
	_, _ = io.Copy(io.Discard, out) // let git finish if extraction stopped early
	werr := cmd.Wait()
	if werr != nil {
		os.RemoveAll(tmp)
		return "", "", fmt.Errorf("git archive %s: %v: %s", commit, werr, strings.TrimSpace(errb.String()))
	}
	if xerr != nil {
		os.RemoveAll(tmp)
		return "", "", xerr
	}
	// The project may live in a subdirectory of the repository.
	if rel, err := filepath.Rel(top, repo); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		return filepath.Join(tmp, rel), tmp, nil
	}
	return tmp, tmp, nil
}

// untar extracts a git-archive stream. It refuses entries that would land
// outside dest and skips symlinks that point outside the export.
func untar(r io.Reader, dest string) error {
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.FromSlash(h.Name)
		if rooted(h.Name) || strings.HasPrefix(filepath.Clean(name), "..") {
			return fmt.Errorf("unsafe path in archive: %s", h.Name)
		}
		target := filepath.Join(dest, name)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(0o644)
			if h.Mode&0o111 != 0 {
				mode = 0o755
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		case tar.TypeSymlink:
			link := h.Linkname
			resolved := filepath.Join(filepath.Dir(target), filepath.FromSlash(link))
			if rooted(link) || !strings.HasPrefix(resolved, filepath.Clean(dest)+string(os.PathSeparator)) {
				continue
			}
			_ = os.MkdirAll(filepath.Dir(target), 0o755)
			_ = os.Symlink(link, target)
		}
	}
}

// rooted reports whether a path from an archive is anchored anywhere other
// than the export folder, on any OS: "/x" and "\x" (the root of the current
// drive on Windows, which filepath.IsAbs does not count), "C:..." and UNC
// paths.
func rooted(p string) bool {
	return strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) || filepath.IsAbs(p) ||
		filepath.VolumeName(p) != "" || (len(p) >= 2 && p[1] == ':')
}

// gatesFor returns the configured gates that apply to a task kind.
func gatesFor(cfg store.Config, kind string) []store.Gate {
	var out []store.Gate
	for _, g := range cfg.Gates {
		if g.AppliesTo(kind) {
			out = append(out, g)
		}
	}
	return out
}

// runGatesAt runs gates at HEAD: worktree gates in the project, export gates
// in a clean copy of the last commit.
func runGatesAt(ctx context.Context, root string, gates []store.Gate, pathExtra []string) []GateRun {
	var runs []GateRun
	var exportDir string
	cleanup := func() {}
	defer func() { cleanup() }()
	for _, g := range gates {
		if g.Where == "export" {
			if exportDir == "" {
				d, c, err := ExportCommit(root, "HEAD")
				if err != nil {
					runs = append(runs, GateRun{Gate: g.Name, Where: "export@HEAD", Err: err, Exit: -1})
					continue
				}
				exportDir, cleanup = d, c
			}
			runs = append(runs, RunGate(ctx, g, exportDir, "export@"+Head(root), pathExtra))
			continue
		}
		runs = append(runs, RunGate(ctx, g, root, "worktree", pathExtra))
	}
	return runs
}

// runGatesExport runs every gate against a clean copy of one commit.
func runGatesExport(ctx context.Context, root, commit string, gates []store.Gate, pathExtra []string) ([]GateRun, error) {
	dir, cleanup, err := ExportCommit(root, commit)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	var runs []GateRun
	for _, g := range gates {
		runs = append(runs, RunGate(ctx, g, dir, "export@"+commit, pathExtra))
	}
	return runs, nil
}

func allOK(runs []GateRun) bool {
	for _, r := range runs {
		if !r.OK {
			return false
		}
	}
	return true
}

// TDDResult is the outcome of checking the red/green rule for one task.
type TDDResult struct {
	OK      bool
	Blocker bool   // a human must fix the setup; retrying will not help
	Detail  string // for the report and for the agent
}

var testPathHints = []string{"test", "spec", "__tests__", "testdata", "fixture", "e2e"}

func looksLikeTestPath(p string) bool {
	low := strings.ToLower(filepath.ToSlash(p))
	for _, h := range testPathHints {
		if strings.Contains(low, h) {
			return true
		}
	}
	return false
}

// checkTDD proves that the suite failed at the recorded red commit and passes
// at HEAD, both measured on clean exports so the comparison is fair.
func checkTDD(ctx context.Context, root string, t store.Task, gates []store.Gate, pathExtra []string) TDDResult {
	if t.RedCommit == "" {
		return TDDResult{Detail: "no red commit recorded. TDD is on: commit the failing tests on their own first, then run `hloop task red " + t.ID + " --commit <hash>`, then implement."}
	}
	if _, err := gitOut(root, "rev-parse", "--verify", t.RedCommit+"^{commit}"); err != nil {
		return TDDResult{Detail: fmt.Sprintf("red commit %s does not exist in this repository", t.RedCommit)}
	}
	if _, err := gitOut(root, "merge-base", "--is-ancestor", t.RedCommit, "HEAD"); err != nil {
		return TDDResult{Detail: fmt.Sprintf("red commit %s is not an ancestor of HEAD; the tests-only commit must be part of this branch's history", t.RedCommit)}
	}
	if t.StartCommit != "" {
		if _, err := gitOut(root, "merge-base", "--is-ancestor", t.StartCommit, t.RedCommit); err != nil || sameCommit(root, t.StartCommit, t.RedCommit) {
			return TDDResult{Detail: fmt.Sprintf("red commit %s was not made during this task (it must come after %s)", t.RedCommit, t.StartCommit)}
		}
	}
	if sameCommit(root, t.RedCommit, "HEAD") {
		return TDDResult{Detail: "the red commit is HEAD: there is no implementation commit after the failing tests"}
	}
	var note string
	if files, err := gitOut(root, "diff-tree", "--no-commit-id", "--name-only", "-r", t.RedCommit); err == nil {
		var other []string
		for _, f := range strings.Split(files, "\n") {
			if f != "" && !looksLikeTestPath(f) && !strings.HasPrefix(f, store.DirName+"/") {
				other = append(other, f)
			}
		}
		if len(other) > 0 {
			note = fmt.Sprintf(" Note: the red commit also changed files that do not look like tests: %s.", strings.Join(capList(other, 5), ", "))
		}
	}
	red, err := runGatesExport(ctx, root, t.RedCommit, gates, pathExtra)
	if err != nil {
		return TDDResult{Detail: "could not export the red commit: " + err.Error()}
	}
	green, err := runGatesExport(ctx, root, "HEAD", gates, pathExtra)
	if err != nil {
		return TDDResult{Detail: "could not export HEAD: " + err.Error()}
	}
	if !allOK(green) {
		var fails []string
		for _, g := range green {
			if !g.OK {
				fails = append(fails, g.Summary())
			}
		}
		return TDDResult{Blocker: true, Detail: "the gates fail on a clean copy of HEAD (" + strings.Join(fails, "; ") + "), so the red/green comparison proves nothing. Usually the clean copy is missing installed dependencies. Make the test gate self-contained, for example `hloop gate add test --replace -- sh -c \"npm ci && npm test\"`, or turn TDD off with `hloop config set tdd off`." + note}
	}
	if allOK(red) {
		return TDDResult{Detail: fmt.Sprintf("the gates already pass at the red commit %s, so its tests do not fail before the implementation. Write tests that fail without the change, commit them alone, record that commit with `hloop task red`.", t.RedCommit) + note}
	}
	return TDDResult{OK: true, Detail: fmt.Sprintf("gates fail at %s (tests only) and pass at %s.%s", t.RedCommit, Head(root), note)}
}

func sameCommit(root, a, b string) bool {
	x, e1 := gitOut(root, "rev-parse", a+"^{commit}")
	y, e2 := gitOut(root, "rev-parse", b+"^{commit}")
	return e1 == nil && e2 == nil && x == y
}

func capList(s []string, n int) []string {
	if len(s) > n {
		return append(s[:n:n], fmt.Sprintf("and %d more", len(s)-n))
	}
	return s
}

// Verification is hloop's verdict on one "done".
type Verification struct {
	OK       bool
	Blocker  string // non-empty: stop and ask the human
	Checks   []store.Check
	Feedback string // for the agent's next turn when it failed
	Summary  string // one line for PROGRESS.md
}

// VerifyDone checks a task the builder marked done: acceptance criteria,
// gates, and (for code tasks with TDD on) the red/green proof.
func (e *Engine) VerifyDone(ctx context.Context, t store.Task) Verification {
	now := e.Now().UTC()
	v := Verification{OK: true}
	var feedback, passed []string
	add := func(name, kind string, ok bool, detail string) {
		v.Checks = append(v.Checks, store.Check{Name: name, Kind: kind, OK: ok, Detail: detail, At: now})
		if !ok {
			v.OK = false
			feedback = append(feedback, fmt.Sprintf("- %s: %s", name, detail))
		} else if kind != "criteria" {
			passed = append(passed, name)
		}
	}

	if unmet := t.UnmetMust(); len(unmet) > 0 {
		var ids []string
		for _, c := range unmet {
			ids = append(ids, c.ID+" ("+c.Status+")")
		}
		add("acceptance criteria", "criteria", false, "Must criteria not marked pass: "+strings.Join(ids, ", "))
	} else if len(t.Criteria) > 0 {
		add("acceptance criteria", "criteria", true, fmt.Sprintf("%d criteria, every Must marked pass", len(t.Criteria)))
	}

	gates := gatesFor(e.Cfg, t.EffKind())
	if len(gates) > 0 {
		for _, r := range runGatesAt(ctx, e.P.Root, gates, e.Cfg.PathExtra) {
			detail := r.Summary()
			if !r.OK && r.Tail != "" {
				detail += "\nLast output:\n" + tailString(r.Tail, 1500)
			}
			add("gate "+r.Gate, "gate", r.OK, detail)
		}
	}

	if e.Cfg.TDDOn() && t.EffKind() == store.KindCode && len(gates) > 0 && IsGitRepo(e.P.Root) && v.OK {
		res := checkTDD(ctx, e.P.Root, t, gates, e.Cfg.PathExtra)
		add("red/green proof", "tdd", res.OK, res.Detail)
		if res.Blocker {
			v.Blocker = fmt.Sprintf("hloop cannot check the TDD rule for %s: %s", t.ID, res.Detail)
		}
	}

	switch {
	case !v.OK:
		v.Feedback = fmt.Sprintf("hloop did NOT accept `task done` for %s. It reopened the task. Fix these and mark it done again:\n%s", t.ID, strings.Join(feedback, "\n"))
	case len(gates) == 0 && len(e.Cfg.Gates) > 0:
		v.Summary = fmt.Sprintf("no gate applies to %s tasks; finish accepted on the agent's word", t.EffKind())
	case len(gates) == 0:
		v.Summary = "no gates configured; finish accepted on the agent's word"
	default:
		v.Summary = strings.Join(passed, ", ") + " passed"
	}
	return v
}

// verifyFinished checks every task that became done during the turn. Tasks
// that fail are reopened with the reason sent to the next turn; the ids of the
// ones that passed are returned (only those count as a milestone).
func (e *Engine) verifyFinished(ctx context.Context, doneBefore map[string]bool) []string {
	l, _ := store.LoadLedger(e.P)
	var fresh []store.Task
	for _, t := range l.Tasks {
		if t.Status == store.TaskDone && !doneBefore[t.ID] {
			fresh = append(fresh, t)
		}
	}
	var ok []string
	for _, t := range fresh {
		v := e.VerifyDone(ctx, t)
		_ = store.UpdateLedger(e.P, func(l *store.Ledger) error {
			x := l.FindTask(t.ID)
			if x == nil {
				return nil
			}
			x.Checks = v.Checks
			if v.OK {
				x.Verified = v.Summary
				return nil
			}
			x.GateFails++
			for _, c := range v.Checks {
				if !c.OK {
					c.Detail = firstLineStr(c.Detail)
					x.Rejections = append(x.Rejections, c)
				}
			}
			x.Done, x.Verified = nil, ""
			x.Status = store.TaskPending
			if l.Current() == nil {
				x.Status = store.TaskInProgress
			}
			if v.Blocker != "" {
				l.AddBlocker(v.Blocker, "hloop")
			}
			return nil
		})
		if v.OK {
			e.logf("verified %s: %s", t.ID, v.Summary)
			if rel, err := WriteReport(e.P, e.Cfg, t.ID); err == nil {
				e.logf("report: .loop/%s", rel)
			}
			ok = append(ok, t.ID)
			continue
		}
		e.logf("REJECTED %s: hloop's checks failed; the task is reopened", t.ID)
		_ = e.P.AddInbox("note", v.Feedback, "hloop")
		var failed []string
		for _, c := range v.Checks {
			if !c.OK {
				failed = append(failed, c.Name)
			}
		}
		e.notify(notify.KindCheck, fmt.Sprintf("%s: %s not accepted", e.Cfg.Name, t.ID),
			fmt.Sprintf("%s was marked done but failed: %s. Reopened; the agent sees why on its next turn.", t.Title, strings.Join(failed, ", ")), "")
	}
	return ok
}

// handleDetections records guard detections on the task they happened under
// and raises a blocker or a notification as configured.
func (e *Engine) handleDetections(agent string, ds []Detection) {
	if len(ds) == 0 {
		return
	}
	now := e.Now().UTC()
	_ = store.UpdateLedger(e.P, func(l *store.Ledger) error {
		t := l.Current()
		if t == nil {
			for i := len(l.Tasks) - 1; i >= 0; i-- {
				if l.Tasks[i].Status == store.TaskDone {
					t = &l.Tasks[i]
					break
				}
			}
		}
		for _, d := range ds {
			if t != nil {
				t.GuardEvents = append(t.GuardEvents, store.Check{Name: d.Name, Kind: "guard", OK: false, Detail: d.Text, At: now})
			}
			if d.Action == store.GuardBlock {
				l.AddBlocker(fmt.Sprintf("Guardrail %s (after agent %q): %s Check `git log` / `git reflog`, repair if needed, then answer this blocker.", d.Name, agent, d.Text), "guard")
			}
		}
		return nil
	})
	for _, d := range ds {
		e.logf("GUARD %s (%s) after %s: %s", d.Name, d.Action, agent, d.Text)
		if d.Action == store.GuardNotify {
			e.notify(notify.KindCheck, fmt.Sprintf("%s: %s", e.Cfg.Name, strings.ReplaceAll(d.Name, "_", " ")), d.Text, "")
		}
	}
}
