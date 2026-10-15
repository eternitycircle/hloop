package engine

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"hloop/internal/store"
)

// Detectors: after every turn of an agent that may change the project, hloop
// looks at git for the high-risk actions GUARDRAILS.md forbids or reserves for
// the human. They catch an agent that ignored its instructions; they do not
// prevent the action (an agent with a shell can do anything you can).

// gitPoint is the repository state before a turn.
type gitPoint struct {
	ok        bool
	branch    string
	head      string            // full hash
	protected map[string]string // protected branch -> tip
}

func takeGitPoint(root string, protected []string) gitPoint {
	if !IsGitRepo(root) {
		return gitPoint{}
	}
	gp := gitPoint{ok: true, protected: map[string]string{}}
	gp.branch, _ = gitOut(root, "rev-parse", "--abbrev-ref", "HEAD")
	gp.head, _ = gitOut(root, "rev-parse", "HEAD")
	for _, b := range protected {
		if tip, err := gitOut(root, "rev-parse", "--verify", "-q", "refs/heads/"+b); err == nil && tip != "" {
			gp.protected[b] = tip
		}
	}
	return gp
}

// Detection is one detector that fired.
type Detection struct {
	Name   string // history_rewrite | protected_commit | dependency_change | mass_delete
	Action string // block | notify
	Text   string
}

// dependencyFiles are manifests and lock files whose change means a
// dependency was added, removed or upgraded.
var dependencyFiles = map[string]bool{
	"package.json": true, "package-lock.json": true, "npm-shrinkwrap.json": true, "yarn.lock": true, "pnpm-lock.yaml": true, "bun.lockb": true,
	"go.mod": true, "go.sum": true, "cargo.toml": true, "cargo.lock": true,
	"pyproject.toml": true, "poetry.lock": true, "pipfile": true, "pipfile.lock": true, "uv.lock": true, "setup.py": true, "setup.cfg": true,
	"gemfile": true, "gemfile.lock": true, "composer.json": true, "composer.lock": true,
	"pubspec.yaml": true, "pubspec.lock": true, "pom.xml": true, "build.gradle": true, "build.gradle.kts": true,
	"packages.config": true, "directory.packages.props": true,
}

func isDependencyFile(p string) bool {
	base := strings.ToLower(path.Base(p))
	if dependencyFiles[base] || strings.HasSuffix(base, ".csproj") {
		return true
	}
	return strings.HasPrefix(base, "requirements") && strings.HasSuffix(base, ".txt")
}

// detect compares the repository after a turn with the point taken before it.
func detect(root string, before gitPoint, g store.Guards) []Detection {
	if !before.ok {
		return nil
	}
	var out []Detection
	fire := func(name, action, text string) {
		if action == store.GuardBlock || action == store.GuardNotify {
			out = append(out, Detection{Name: name, Action: action, Text: text})
		}
	}
	afterHead, _ := gitOut(root, "rev-parse", "HEAD")

	// 1. history rewrite: the commit that was the tip of the branch before the
	// turn is no longer contained in that branch.
	if before.head != "" && before.branch != "" && before.branch != "HEAD" {
		tip, err := gitOut(root, "rev-parse", "--verify", "-q", "refs/heads/"+before.branch)
		switch {
		case err != nil || tip == "":
			fire("history_rewrite", g.HistoryRewrite, fmt.Sprintf("branch %q was deleted during the turn (it pointed at %s).", before.branch, short(before.head)))
		case !isAncestor(root, before.head, tip):
			fire("history_rewrite", g.HistoryRewrite, fmt.Sprintf("history of branch %q was rewritten: commit %s is no longer part of it (reset, rebase, amend or force push).", before.branch, short(before.head)))
		}
	}

	// 2. commits on a protected branch.
	for b, oldTip := range before.protected {
		tip, err := gitOut(root, "rev-parse", "--verify", "-q", "refs/heads/"+b)
		if err != nil || tip == "" || tip == oldTip {
			continue
		}
		if b == before.branch && !isAncestor(root, oldTip, tip) {
			continue // already reported as a rewrite
		}
		// commits that only touch .loop/ (hloop's own ledger commits) are fine
		n, _ := gitOut(root, "rev-list", "--count", oldTip+".."+tip, "--", ".", ":(exclude)"+store.DirName)
		if n == "0" {
			continue
		}
		fire("protected_commit", g.ProtectedCommit, fmt.Sprintf("%s new commit(s) landed directly on protected branch %q (%s..%s). The rules ask for a feature branch.", orStr(n, "some"), b, short(oldTip), short(tip)))
	}

	// 3 and 4: what changed, committed or not.
	changed := map[string]string{} // path -> status letter
	if before.head != "" && afterHead != "" && before.head != afterHead {
		// only commits made during the turn: switching to another branch is not
		// a change by itself, and an amended or rebased commit whose patch
		// already existed is not a new change (--cherry-pick drops it)
		if s, err := gitOut(root, "log", "--no-renames", "--name-status", "--format=", "--cherry-pick", "--right-only", before.head+"..."+afterHead); err == nil {
			parseNameStatus(s, changed)
		}
	}
	if s, err := gitOut(root, "status", "--porcelain", "--no-renames"); err == nil {
		for _, line := range strings.Split(s, "\n") {
			if len(line) < 4 {
				continue
			}
			st := strings.TrimSpace(line[:2])
			p := strings.Trim(line[3:], `"`)
			if strings.Contains(st, "D") {
				changed[p] = "D"
			} else if _, ok := changed[p]; !ok {
				changed[p] = "M"
			}
		}
	}
	var deps, deleted []string
	for p, st := range changed {
		if strings.HasPrefix(p, store.DirName+"/") || strings.Contains(p, "/"+store.DirName+"/") {
			continue
		}
		if isDependencyFile(p) {
			deps = append(deps, p)
		}
		if st == "D" {
			deleted = append(deleted, p)
		}
	}
	if len(deps) > 0 {
		sortStrings(deps)
		fire("dependency_change", g.DependencyChange, "dependency files changed: "+strings.Join(capList(deps, 8), ", ")+". Check what was added or upgraded.")
	}
	if g.MassDeleteCount > 0 && len(deleted) >= g.MassDeleteCount {
		sortStrings(deleted)
		fire("mass_delete", g.MassDelete, fmt.Sprintf("%d files were deleted in one turn, for example %s.", len(deleted), strings.Join(capList(deleted, 5), ", ")))
	}
	return out
}

func parseNameStatus(s string, into map[string]string) {
	for _, line := range strings.Split(s, "\n") {
		f := strings.SplitN(line, "\t", 2)
		if len(f) == 2 && f[0] != "" {
			if _, seen := into[f[1]]; !seen { // newest commit first: keep its status
				into[f[1]] = f[0][:1]
			}
		}
	}
}

func isAncestor(root, a, b string) bool {
	_, err := gitOut(root, "merge-base", "--is-ancestor", a, b)
	return err == nil
}

func short(h string) string {
	if len(h) > 9 {
		return h[:9]
	}
	return h
}

func orStr(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func sortStrings(s []string) { sort.Strings(s) }
