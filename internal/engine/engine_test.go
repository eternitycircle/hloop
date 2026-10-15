package engine

import (
	"archive/tar"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"hloop/internal/store"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func repo(t *testing.T) string {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644)
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "one")
	return dir
}

func defaults() store.Guards {
	return store.Guards{ProtectedBranches: []string{"main"}, HistoryRewrite: "block", ProtectedCommit: "notify", DependencyChange: "notify", MassDelete: "notify", MassDeleteCount: 3}
}

func names(ds []Detection) string {
	var out []string
	for _, d := range ds {
		out = append(out, d.Name+":"+d.Action)
	}
	return strings.Join(out, ",")
}

func TestDetectors(t *testing.T) {
	dir := repo(t)
	git(t, dir, "checkout", "-qb", "feature")
	before := takeGitPoint(dir, []string{"main"})
	if got := detect(dir, before, defaults()); len(got) != 0 {
		t.Fatalf("nothing happened, got %s", names(got))
	}

	// commit on the feature branch with a dependency file and three deletions
	for _, f := range []string{"x1", "x2", "x3"} {
		os.WriteFile(filepath.Join(dir, f), []byte(f), 0o644)
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "files")
	before = takeGitPoint(dir, []string{"main"})
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644)
	for _, f := range []string{"x1", "x2", "x3"} {
		os.Remove(filepath.Join(dir, f))
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "deps and deletions")
	got := names(detect(dir, before, defaults()))
	if got != "dependency_change:notify,mass_delete:notify" {
		t.Fatalf("got %s", got)
	}

	// amend = history rewrite
	before = takeGitPoint(dir, []string{"main"})
	git(t, dir, "commit", "--amend", "-q", "--allow-empty", "-m", "amended")
	if got := names(detect(dir, before, defaults())); got != "history_rewrite:block" {
		t.Fatalf("got %s", got)
	}

	// a commit landing on main; a commit touching only .loop/ on main is fine
	before = takeGitPoint(dir, []string{"main"})
	git(t, dir, "checkout", "-q", "main")
	os.MkdirAll(filepath.Join(dir, ".loop"), 0o755)
	os.WriteFile(filepath.Join(dir, ".loop", "ledger.json"), []byte("{}"), 0o644)
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "ledger only")
	if got := names(detect(dir, before, defaults())); got != "" {
		t.Fatalf("ledger-only commit on main should not fire, got %s", got)
	}
	before = takeGitPoint(dir, []string{"main"})
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o644)
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "direct")
	if got := names(detect(dir, before, defaults())); got != "protected_commit:notify" {
		t.Fatalf("got %s", got)
	}
	// off means off
	g := defaults()
	g.ProtectedCommit = "off"
	before = takeGitPoint(dir, []string{"main"})
	os.WriteFile(filepath.Join(dir, "c.txt"), []byte("c"), 0o644)
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "direct2")
	if got := detect(dir, before, g); len(got) != 0 {
		t.Fatalf("got %s", names(got))
	}
}

func TestDependencyFiles(t *testing.T) {
	for _, f := range []string{"package.json", "web/package-lock.json", "go.sum", "Cargo.toml", "requirements-dev.txt", "app/App.csproj", "pnpm-lock.yaml"} {
		if !isDependencyFile(f) {
			t.Errorf("%s should count as a dependency file", f)
		}
	}
	for _, f := range []string{"main.go", "README.md", "requirements.md", "src/package.ts"} {
		if isDependencyFile(f) {
			t.Errorf("%s is not a dependency file", f)
		}
	}
}

func TestUntarRefusesEscapes(t *testing.T) {
	mk := func(entries ...*tar.Header) *bytes.Buffer {
		var b bytes.Buffer
		tw := tar.NewWriter(&b)
		for _, h := range entries {
			_ = tw.WriteHeader(h)
			if h.Typeflag == tar.TypeReg {
				tw.Write(make([]byte, h.Size))
			}
		}
		tw.Close()
		return &b
	}
	dest := t.TempDir()
	if err := untar(mk(&tar.Header{Name: "../evil", Typeflag: tar.TypeReg, Size: 1, Mode: 0o644}), dest); err == nil {
		t.Fatal("a path outside the export must be refused")
	}
	dest = t.TempDir()
	err := untar(mk(
		&tar.Header{Name: "ok.txt", Typeflag: tar.TypeReg, Size: 2, Mode: 0o644},
		&tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
		&tar.Header{Name: "inner", Typeflag: tar.TypeSymlink, Linkname: "ok.txt"},
	), dest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dest, "link")); err == nil {
		t.Fatal("a symlink pointing outside the export must be skipped")
	}
	if _, err := os.Lstat(filepath.Join(dest, "inner")); err != nil && runtime.GOOS != "windows" {
		t.Fatal("an internal symlink should be kept")
	}
}

func TestExportCommitHasOnlyTrackedFiles(t *testing.T) {
	dir := repo(t)
	os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("x"), 0o644)
	d, cleanup, err := ExportCommit(dir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if _, err := os.Stat(filepath.Join(d, "a.txt")); err != nil {
		t.Fatal("tracked file missing from export")
	}
	if _, err := os.Stat(filepath.Join(d, "untracked.txt")); err == nil {
		t.Fatal("untracked file leaked into export")
	}
	cleanup()
	if _, err := os.Stat(d); err == nil {
		t.Fatal("cleanup should remove the export")
	}
}

func TestVersions(t *testing.T) {
	for in, want := range map[string]string{"1.2.3": "1.2.3", "v0.1.0": "0.1.0", "2.0.0-rc.1": "2.0.0-rc.1"} {
		if got, err := NormalizeVersion(in); err != nil || got != want {
			t.Errorf("Normalize(%s) = %s, %v", in, got, err)
		}
	}
	for _, bad := range []string{"1.2", "latest", "v1", "1.2.3.4"} {
		if _, err := NormalizeVersion(bad); err == nil {
			t.Errorf("%s should be rejected", bad)
		}
	}
	cases := [][3]string{{"1.2.3", "patch", "1.2.4"}, {"1.2.3", "minor", "1.3.0"}, {"1.2.3", "major", "2.0.0"}, {"0.9.9", "", "0.9.10"}}
	for _, c := range cases {
		if got, _ := BumpVersion(c[0], c[1]); got != c[2] {
			t.Errorf("bump %s %s = %s", c[0], c[1], got)
		}
	}
	dir := repo(t)
	git(t, dir, "tag", "v1.4.0")
	git(t, dir, "tag", "v1.10.2")
	git(t, dir, "tag", "v2.0.0-beta")
	git(t, dir, "tag", "nonsense")
	if got := LatestTagVersion(dir); got != "1.10.2" {
		t.Fatalf("latest tag = %s", got)
	}
}

func TestDocsAllowed(t *testing.T) {
	for p, want := range map[string]bool{"docs/x.md": true, "docs/img/a.png": true, "README.md": true, "CHANGELOG.md": true, ".loop/x": true,
		"src/README.md": false, "main.go": false, "package.json": false, "docsx/a.md": false} {
		if DocsAllowed(p) != want {
			t.Errorf("DocsAllowed(%s) = %v", p, !want)
		}
	}
}
