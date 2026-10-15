package engine

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// gitOut runs git and returns trimmed stdout.
func gitOut(dir string, args ...string) (string, error) {
	s, err := gitRaw(dir, args...)
	return strings.TrimSpace(s), err
}

func gitRaw(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// IsGitRepo reports whether dir is inside a git work tree.
func IsGitRepo(dir string) bool {
	out, err := gitOut(dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && out == "true"
}

// Head returns the short hash of HEAD ("" if none).
func Head(dir string) string {
	out, err := gitOut(dir, "rev-parse", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return out
}

// snapshot captures what is currently changed in the working tree, so the
// difference after an agent run shows exactly what that agent touched.
type snapshot struct {
	files   map[string]string // repo-relative path (forward slashes) -> content hash
	head    string
	top     string // repo top-level directory
	loopRel string // path of .loop/ relative to top, with trailing slash
	projRel string // the project directory relative to top ("" or "sub/dir/")
}

func takeSnapshot(root string) (*snapshot, error) {
	top, err := gitOut(root, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	prefix, err := gitOut(root, "rev-parse", "--show-prefix")
	if err != nil {
		return nil, err
	}
	raw, err := gitRaw(root, "status", "--porcelain=v1", "-z", "-uall")
	if err != nil {
		return nil, err
	}
	s := &snapshot{files: map[string]string{}, top: filepath.FromSlash(top), loopRel: prefix + ".loop/", projRel: prefix}
	s.head, _ = gitOut(root, "rev-parse", "HEAD")
	entries := strings.Split(raw, "\x00")
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if len(e) < 4 {
			continue
		}
		xy, path := e[:2], e[3:]
		if xy[0] == 'R' || xy[0] == 'C' {
			i++ // the original path follows as its own entry
		}
		s.files[path] = hashFile(filepath.Join(s.top, filepath.FromSlash(path)))
	}
	return s, nil
}

func hashFile(path string) string {
	st, err := os.Stat(path)
	if err != nil {
		return "deleted"
	}
	if st.IsDir() {
		return "dir"
	}
	if st.Size() > 64<<20 {
		return fmt.Sprintf("large:%d:%d", st.Size(), st.ModTime().UnixNano())
	}
	f, err := os.Open(path)
	if err != nil {
		return "unreadable"
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "unreadable"
	}
	return hex.EncodeToString(h.Sum(nil))
}

// GuardReport is the outcome of checking a loop-only agent.
type GuardReport struct {
	Reverted   []string
	Unresolved []string
}

func (g GuardReport) Clean() bool { return len(g.Reverted) == 0 && len(g.Unresolved) == 0 }

// enforceLoopOnly compares the tree before and after an agent that may only
// touch .loop/. Files that were clean before are restored; anything we cannot
// restore safely (the tree was already dirty, or HEAD moved) is reported.
// enforceGuard applies the guard for an agent's mode (exactly one of them).
func enforceGuard(mode string, before, after *snapshot) GuardReport {
	if mode == "docs" {
		return enforceDocsOnly(before, after)
	}
	return enforceLoopOnly(before, after)
}

func enforceLoopOnly(before, after *snapshot) GuardReport {
	return enforceScope(before, after, func(path string) bool { return strings.HasPrefix(path, after.loopRel) })
}

// DocsAllowed reports whether a path (relative to the project root, forward
// slashes) is one a "docs" mode agent may change: anything under .loop/ or
// docs/, and markdown files in the project root (README.md, CHANGELOG.md,
// CONTRIBUTING.md, ...).
func DocsAllowed(rel string) bool {
	switch {
	case strings.HasPrefix(rel, ".loop/"), strings.HasPrefix(rel, "docs/"):
		return true
	case !strings.Contains(rel, "/") && strings.HasSuffix(strings.ToLower(rel), ".md"):
		return true
	}
	return false
}

// enforceDocsOnly is the guard for agents in "docs" mode.
func enforceDocsOnly(before, after *snapshot) GuardReport {
	return enforceScope(before, after, func(path string) bool {
		if !strings.HasPrefix(path, after.projRel) {
			return false
		}
		return DocsAllowed(strings.TrimPrefix(path, after.projRel))
	})
}

// enforceScope reverts changes to paths an agent was not allowed to touch.
func enforceScope(before, after *snapshot, allowed func(path string) bool) GuardReport {
	var rep GuardReport
	if before == nil || after == nil {
		return rep
	}
	for path, h := range after.files {
		if allowed(path) {
			continue
		}
		if prev, was := before.files[path]; was && prev == h {
			continue
		}
		if _, was := before.files[path]; was {
			rep.Unresolved = append(rep.Unresolved, path+" (already had uncommitted changes; cannot restore)")
			continue
		}
		if err := revertPath(after.top, path); err != nil {
			rep.Unresolved = append(rep.Unresolved, fmt.Sprintf("%s (%v)", path, err))
		} else {
			rep.Reverted = append(rep.Reverted, path)
		}
	}
	if before.head != after.head {
		rep.Unresolved = append(rep.Unresolved, "git HEAD moved (the agent committed); review the history")
	}
	return rep
}

func revertPath(top, rel string) error {
	if _, err := gitOut(top, "ls-files", "--error-unmatch", "--", rel); err == nil {
		_, err := gitOut(top, "checkout", "HEAD", "--", rel)
		return err
	}
	return os.Remove(filepath.Join(top, filepath.FromSlash(rel)))
}
