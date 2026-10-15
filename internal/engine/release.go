package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"hloop/internal/store"
)

// Releases: `hloop release` checks that the work is finished and verified,
// assembles a factual brief of everything since the previous release (or of
// the whole project the first time), and hands it to the technical writer.

var semverRe = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)([-+][0-9A-Za-z.+-]+)?$`)

// NormalizeVersion validates a semantic version and strips a leading "v".
func NormalizeVersion(v string) (string, error) {
	v = strings.TrimSpace(v)
	if !semverRe.MatchString(v) {
		return "", fmt.Errorf("%q is not a version like 1.2.0 (semantic versioning: MAJOR.MINOR.PATCH)", v)
	}
	return strings.TrimPrefix(v, "v"), nil
}

// BumpVersion increments a version: part is major, minor or patch.
func BumpVersion(v, part string) (string, error) {
	m := semverRe.FindStringSubmatch(v)
	if m == nil {
		return "", fmt.Errorf("cannot bump %q", v)
	}
	a, _ := strconv.Atoi(m[1])
	b, _ := strconv.Atoi(m[2])
	c, _ := strconv.Atoi(m[3])
	switch part {
	case "major":
		a, b, c = a+1, 0, 0
	case "minor":
		b, c = b+1, 0
	case "patch", "":
		c++
	default:
		return "", fmt.Errorf("--bump must be major, minor or patch")
	}
	return fmt.Sprintf("%d.%d.%d", a, b, c), nil
}

// LatestTagVersion returns the highest semantic-version git tag, if any.
func LatestTagVersion(root string) string {
	out, err := gitOut(root, "tag", "--list")
	if err != nil {
		return ""
	}
	best, bestKey := "", [3]int{-1, -1, -1}
	for _, t := range strings.Split(out, "\n") {
		m := semverRe.FindStringSubmatch(strings.TrimSpace(t))
		if m == nil || m[4] != "" {
			continue
		}
		var k [3]int
		for i := 0; i < 3; i++ {
			k[i], _ = strconv.Atoi(m[i+1])
		}
		if k[0] > bestKey[0] || k[0] == bestKey[0] && (k[1] > bestKey[1] || k[1] == bestKey[1] && k[2] > bestKey[2]) {
			best, bestKey = strings.TrimPrefix(strings.TrimSpace(t), "v"), k
		}
	}
	return best
}

// Preflight is the readiness check before a release.
type Preflight struct {
	Problems []string // stop the release unless overridden
	Warnings []string // shown, recorded, never blocking
	Gates    []GateRun
}

func (p Preflight) OK() bool { return len(p.Problems) == 0 }

// ReleasePreflight checks that the project is ready to release: no task in
// flight, no open blockers, nothing uncommitted outside .loop/, and every
// gate passing at HEAD.
func ReleasePreflight(ctx context.Context, p store.Project, cfg store.Config, allowPending, runGates bool) Preflight {
	var pf Preflight
	l, _ := store.LoadLedger(p)
	if c := l.Current(); c != nil {
		pf.Problems = append(pf.Problems, fmt.Sprintf("%s is still in progress: %s", c.ID, c.Title))
	}
	for _, b := range l.OpenBlockers() {
		pf.Problems = append(pf.Problems, fmt.Sprintf("blocker %s is open: %s", b.ID, oneLine(b.Text, 120)))
	}
	if n := l.Count(store.TaskPending) + l.Count(store.TaskProposed); n > 0 {
		msg := fmt.Sprintf("%d task(s) are not finished (hloop task list)", n)
		if allowPending {
			pf.Warnings = append(pf.Warnings, msg+"; releasing what is done")
		} else {
			pf.Problems = append(pf.Problems, msg+"; finish them, skip them, or release anyway with --allow-pending")
		}
	}
	since := time.Time{}
	if r := l.LastRelease(); r != nil {
		since = r.Date
	}
	for _, t := range l.Tasks {
		if t.Status == store.TaskDone && t.Done != nil && t.Done.After(since) && t.Verified == "" && t.Source != "adopt" {
			pf.Warnings = append(pf.Warnings, fmt.Sprintf("%s was finished without hloop's verification (marked done by hand, or before gates existed)", t.ID))
		}
	}
	if od := l.OpenDecisions(); len(od) > 0 {
		pf.Warnings = append(pf.Warnings, fmt.Sprintf("%d open decision(s); the writer lists them as known limitations where they affect users", len(od)))
	}
	if !IsGitRepo(p.Root) {
		pf.Problems = append(pf.Problems, "not a git repository: a release needs history to describe")
		return pf
	}
	if Head(p.Root) == "" {
		pf.Problems = append(pf.Problems, "the repository has no commits yet")
		return pf
	}
	if st, _ := gitOut(p.Root, "status", "--porcelain", "--", ".", ":(exclude)"+store.DirName); st != "" {
		pf.Problems = append(pf.Problems, "uncommitted changes outside .loop/ (commit or stash them; the release describes committed code):\n    "+strings.ReplaceAll(capLines(st, 8), "\n", "\n    "))
	}
	if len(cfg.Gates) == 0 {
		pf.Warnings = append(pf.Warnings, "no gates configured, so nothing checks that this version works (hloop gate add test -- <command>)")
	} else if runGates {
		pf.Gates = runGatesAt(ctx, p.Root, cfg.Gates, cfg.PathExtra)
		for _, g := range pf.Gates {
			if !g.OK {
				pf.Problems = append(pf.Problems, "gate "+g.Summary())
			}
		}
	}
	return pf
}

func capLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = append(lines[:n], fmt.Sprintf("... and %d more", len(lines)-n))
	}
	return strings.Join(lines, "\n")
}

// ReleasePlan is what a release covers.
type ReleasePlan struct {
	Version  string
	Previous *store.Release
	First    bool
	From     string // commit after which the range starts ("" = whole history)
	Head     string
	Tasks    []store.Task
	Notes    string // release notes path, relative to the project root
	Date     time.Time
}

// PlanRelease works out the range and the tasks a release covers.
func PlanRelease(p store.Project, version string, now time.Time) (ReleasePlan, error) {
	l, err := store.LoadLedger(p)
	if err != nil {
		return ReleasePlan{}, err
	}
	rp := ReleasePlan{Version: version, Date: now, Notes: "docs/releases/v" + version + ".md"}
	rp.Head, _ = gitOut(p.Root, "rev-parse", "HEAD")
	since := time.Time{}
	if r := l.LastRelease(); r != nil {
		prev := *r
		rp.Previous = &prev
		since = r.Date
		if _, err := gitOut(p.Root, "rev-parse", "--verify", r.Commit+"^{commit}"); err == nil {
			rp.From = r.Commit
		}
	} else {
		rp.First = true
	}
	for _, t := range l.Tasks {
		if t.Status == store.TaskDone && (rp.First || t.Done != nil && t.Done.After(since)) {
			rp.Tasks = append(rp.Tasks, t)
		}
	}
	return rp, nil
}

// docInventory lists the documentation that already exists.
func docInventory(root string) []string {
	var out []string
	ents, _ := os.ReadDir(root)
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			out = append(out, e.Name())
		}
	}
	_ = filepath.WalkDir(filepath.Join(root, "docs"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && len(out) > 300 {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			if rel, e := filepath.Rel(root, path); e == nil {
				out = append(out, filepath.ToSlash(rel))
			}
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// ReleaseBrief renders the factual brief handed to the technical writer.
func ReleaseBrief(p store.Project, cfg store.Config, rp ReleasePlan, pf Preflight) string {
	l, _ := store.LoadLedger(p)
	var b strings.Builder
	fmt.Fprintf(&b, "# Release brief: %s v%s\n\nGenerated by hloop from the ledger and git. Facts only; verify against the code before you write.\n\n", cfg.Name, rp.Version)
	fmt.Fprintf(&b, "- Version: %s (semantic versioning)\n- Date: %s\n", rp.Version, rp.Date.Format("2006-01-02"))
	if rp.First {
		b.WriteString("- This is the FIRST release: document the whole product as it stands. In the release notes and changelog, everything is new.\n")
	} else {
		fmt.Fprintf(&b, "- Previous release: v%s on %s (commit %s). Describe only what changed since then; but make every document accurate for v%s.\n", rp.Previous.Version, rp.Previous.Date.Format("2006-01-02"), short(rp.Previous.Commit), rp.Version)
	}
	fmt.Fprintf(&b, "- Code being released: %s\n- Release notes go to: %s\n", short(rp.Head), rp.Notes)
	fmt.Fprintf(&b, "- Product definition: .loop/%s", p.PRDName())
	if _, err := os.Stat(p.Path("ARCHITECTURE.md")); err == nil {
		b.WriteString(", .loop/ARCHITECTURE.md")
	}
	b.WriteString("\n")
	if len(cfg.Gates) > 0 {
		b.WriteString("- How the project is tested (hloop's gates; all passed for this release):\n")
		for _, g := range cfg.Gates {
			fmt.Fprintf(&b, "  - %s: `%s`\n", g.Name, strings.Join(g.Run, " "))
		}
	}

	fmt.Fprintf(&b, "\n## Work in this release (%d task(s))\n", len(rp.Tasks))
	if len(rp.Tasks) == 0 {
		b.WriteString("\nNo task was finished through hloop in this range; rely on the commit log below.\n")
	}
	for _, t := range rp.Tasks {
		fmt.Fprintf(&b, "\n### %s %s (%s)\n", t.ID, t.Title, t.EffKind())
		if t.Summary != "" {
			fmt.Fprintf(&b, "- Summary: %s\n", oneLine(t.Summary, 600))
		}
		if t.Detail != "" {
			fmt.Fprintf(&b, "- Details: %s\n", oneLine(t.Detail, 600))
		}
		if t.Spec != "" {
			fmt.Fprintf(&b, "- Spec: .loop/%s\n", t.Spec)
		}
		if t.Commit != "" {
			fmt.Fprintf(&b, "- Commit: %s\n", t.Commit)
		}
		if t.Verified != "" {
			fmt.Fprintf(&b, "- Verified by hloop: %s\n", t.Verified)
		}
		if t.Report != "" {
			fmt.Fprintf(&b, "- Completion report: .loop/%s\n", t.Report)
		}
		for _, c := range t.Criteria {
			fmt.Fprintf(&b, "- %s (%s, %s): %s\n", c.ID, c.Priority, c.Status, c.Text)
		}
		for _, g := range t.GuardEvents {
			if g.Name == "dependency_change" {
				fmt.Fprintf(&b, "- Dependencies changed: %s\n", oneLine(g.Detail, 300))
			}
		}
	}

	var decided []store.Decision
	for _, d := range l.Decisions {
		if d.Status == store.DecisionDecided && (rp.First || rp.Previous == nil || d.Date >= rp.Previous.Date.Format("2006-01-02")) {
			decided = append(decided, d)
		}
	}
	if len(decided) > 0 {
		b.WriteString("\n## Decisions made\n")
		for _, d := range decided {
			fmt.Fprintf(&b, "- %s: %s. %s\n", d.Topic, d.Decision, d.Rationale)
		}
	}
	if od := l.OpenDecisions(); len(od) > 0 {
		b.WriteString("\n## Open decisions (possible known limitations)\n")
		for _, d := range od {
			fmt.Fprintf(&b, "- %s", d.Topic)
			if d.Needs != "" {
				fmt.Fprintf(&b, " (needs: %s)", d.Needs)
			}
			b.WriteString("\n")
		}
	}
	var revs []store.Review
	for _, r := range l.Reviews {
		if rp.First || r.Time.After(rp.Previous.Date) {
			revs = append(revs, r)
		}
	}
	if len(revs) > 0 {
		if len(revs) > 15 {
			revs = revs[len(revs)-15:]
		}
		b.WriteString("\n## Review findings\n")
		for _, r := range revs {
			fmt.Fprintf(&b, "- %s by %s: %s. %s\n", r.ID, r.Agent, r.Verdict, oneLine(r.Summary, 400))
		}
	}
	if act := l.LessonsWith(store.LessonActive); len(act) > 0 {
		b.WriteString("\n## Lessons (often useful for troubleshooting and developer docs)\n")
		for _, x := range act {
			fmt.Fprintf(&b, "- %s\n", x.Text)
		}
	}
	var answered []store.Blocker
	for _, x := range l.Blockers {
		if x.Resolved && (rp.First || x.ResolvedAt != nil && x.ResolvedAt.After(rp.Previous.Date)) {
			answered = append(answered, x)
		}
	}
	if len(answered) > 0 {
		b.WriteString("\n## Problems that needed a human (possible troubleshooting entries)\n")
		for _, x := range answered {
			fmt.Fprintf(&b, "- %s -> %s\n", oneLine(x.Text, 300), oneLine(x.Answer, 300))
		}
	}

	rng := "HEAD"
	if rp.From != "" {
		rng = rp.From + "..HEAD"
	}
	if log, err := gitOut(p.Root, "log", "--no-merges", "--format=%h %ad %s", "--date=short", "-n", "200", rng, "--", ".", ":(exclude)"+store.DirName); err == nil && log != "" {
		b.WriteString("\n## Commits (newest first, .loop/-only commits left out)\n```\n" + log + "\n```\n")
	}
	statArgs := []string{"diff", "--stat=100", "--stat-count=60"}
	if rp.From != "" {
		statArgs = append(statArgs, rp.From, "HEAD")
	} else if root, err := gitOut(p.Root, "hash-object", "-t", "tree", os.DevNull); err == nil {
		statArgs = append(statArgs, root, "HEAD")
	}
	statArgs = append(statArgs, "--", ".", ":(exclude)"+store.DirName)
	if stat, err := gitOut(p.Root, statArgs...); err == nil && stat != "" {
		if rp.From == "" {
			b.WriteString("\n## Files in the project (first release: the whole tree, shown as added; not a change list)\n```\n" + stat + "\n```\n")
		} else {
			b.WriteString("\n## Files changed since the previous release\n```\n" + stat + "\n```\n")
		}
	}
	if inv := docInventory(p.Root); len(inv) > 0 {
		b.WriteString("\n## Documentation that already exists (update these; do not duplicate them)\n")
		for _, f := range inv {
			fmt.Fprintf(&b, "- %s\n", f)
		}
	}
	if len(pf.Warnings) > 0 {
		b.WriteString("\n## Release warnings accepted by the human\n")
		for _, w := range pf.Warnings {
			fmt.Fprintf(&b, "- %s\n", w)
		}
	}
	return b.String()
}

// unfinishedMarkers counts [[FILL]], TODO and TBD markers in a file.
func unfinishedMarkers(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	s := string(b)
	return strings.Count(s, "[[FILL") + strings.Count(s, "TODO") + strings.Count(s, "TBD")
}

// ReleaseCheck verifies what the writer produced: release notes exist, the
// changelog has the version, and which documents still have open markers.
type ReleaseCheck struct {
	Problems   []string
	Unfinished map[string]int
	Changed    []string // documents created or changed (project-relative)
}

// CheckReleaseOutput inspects the working tree after the writer ran.
func CheckReleaseOutput(p store.Project, rp ReleasePlan) ReleaseCheck {
	rc := ReleaseCheck{Unfinished: map[string]int{}}
	notes := filepath.Join(p.Root, filepath.FromSlash(rp.Notes))
	if st, err := os.Stat(notes); err != nil || st.Size() < 40 {
		rc.Problems = append(rc.Problems, "release notes were not written to "+rp.Notes)
	}
	if b, err := os.ReadFile(filepath.Join(p.Root, "CHANGELOG.md")); err != nil {
		rc.Problems = append(rc.Problems, "CHANGELOG.md was not created")
	} else if !strings.Contains(string(b), rp.Version) {
		rc.Problems = append(rc.Problems, "CHANGELOG.md has no entry for "+rp.Version)
	}
	if st, err := gitRaw(p.Root, "status", "--porcelain=v1", "-uall", "--", "."); err == nil {
		for _, line := range strings.Split(st, "\n") {
			if len(line) < 4 {
				continue
			}
			rel := strings.Trim(line[3:], `"`)
			if prefix, err := gitOut(p.Root, "rev-parse", "--show-prefix"); err == nil && prefix != "" {
				rel = strings.TrimPrefix(rel, prefix)
			}
			if strings.HasPrefix(rel, store.DirName+"/") || !DocsAllowed(rel) {
				continue
			}
			rc.Changed = append(rc.Changed, rel)
			if n := unfinishedMarkers(filepath.Join(p.Root, filepath.FromSlash(rel))); n > 0 {
				rc.Unfinished[rel] = n
			}
		}
	}
	sort.Strings(rc.Changed)
	return rc
}

// CommitRelease commits the release documents (and .loop/) and returns HEAD.
func CommitRelease(p store.Project, message string, files []string) (string, error) {
	if len(files) > 0 {
		args := append([]string{"add", "--"}, files...)
		if _, err := gitOut(p.Root, args...); err != nil {
			return "", err
		}
	}
	_, _ = gitOut(p.Root, "add", "--", store.DirName)
	if st, _ := gitOut(p.Root, "diff", "--cached", "--name-only"); st == "" {
		return gitOut(p.Root, "rev-parse", "HEAD")
	}
	if _, err := gitOut(p.Root, "commit", "-m", message); err != nil {
		return "", fmt.Errorf("%v (is git user.name/user.email set?)", err)
	}
	return gitOut(p.Root, "rev-parse", "HEAD")
}

// TagRelease makes an annotated tag (never pushed).
func TagRelease(p store.Project, version string) (string, error) {
	tag := "v" + version
	if _, err := gitOut(p.Root, "rev-parse", "--verify", "-q", "refs/tags/"+tag); err == nil {
		return "", fmt.Errorf("tag %s already exists", tag)
	}
	_, err := gitOut(p.Root, "tag", "-a", tag, "-m", "Release "+tag)
	return tag, err
}
