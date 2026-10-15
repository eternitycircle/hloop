// Package scaffold creates a project's .loop directory (config, planning
// documents, agent prompts, initial ledger) and, for projects that already
// exist, discovers what is in the repository so a harness can be told exactly
// how to write the loop files from it.
package scaffold

import (
	"bufio"
	"bytes"
	"embed"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"hloop/internal/adapter"
	"hloop/internal/store"
)

//go:embed templates/*.md templates/gitignore templates/prompts/*.md profiles/*.md
var fs embed.FS

const fillMarker = "[[FILL"

// Answers are the facts gathered about a project during setup.
type Answers struct {
	Name        string
	Purpose     string
	Audience    string
	Stack       string
	Backend     string
	Hosting     string
	Constraints []string
	Milestones  []string
}

// Options controls Create.
type Options struct {
	Root    string
	Adapter string
	Answers Answers
	Force   bool
}

// DefaultPrompt returns the embedded prompt for a role.
func DefaultPrompt(role string) string {
	name := role
	switch role {
	case "builder", "reviewer", "planner", "interviewer", "adopter", "spec-writer", "tech-writer":
	default:
		name = "custom"
	}
	b, err := fs.ReadFile("templates/prompts/" + name + ".md")
	if err != nil {
		return ""
	}
	return string(b)
}

// PromptPath is the conventional prompt file (relative to .loop/) for a role.
func PromptPath(role string) string {
	switch role {
	case "builder", "reviewer", "planner", "interviewer", "adopter", "spec-writer", "tech-writer":
		return "prompts/" + role + ".md"
	}
	return "prompts/" + store.SafeName(role) + ".md"
}

func fill(s, hint string) string {
	if strings.TrimSpace(s) == "" {
		return fmt.Sprintf("[[FILL: %s]]", hint)
	}
	return s
}

// Create writes a new .loop directory.
func Create(o Options) (store.Project, error) {
	root, err := filepath.Abs(o.Root)
	if err != nil {
		return store.Project{}, err
	}
	p := store.Project{Root: root}
	if p.Exists() && !o.Force {
		return p, fmt.Errorf("%s already contains a .loop project (use --force to re-create documents)", root)
	}
	for _, d := range []string{"", "prompts", "runs", "reviews", "adapters", "specs", "reports"} {
		mode := os.FileMode(0o755)
		if d == "runs" {
			mode = 0o700 // agent output can contain anything the agent printed
		}
		if err := os.MkdirAll(p.Path(d), mode); err != nil {
			return p, err
		}
	}
	a := o.Answers
	if a.Name == "" {
		a.Name = filepath.Base(root)
	}
	if o.Adapter == "" {
		o.Adapter = DetectAdapter()
	}

	cfg := store.DefaultConfig(a.Name, o.Adapter)
	if p.Exists() {
		if old, err := p.LoadConfig(); err == nil {
			cfg = old
		}
	}
	if err := p.SaveConfig(cfg); err != nil {
		return p, err
	}

	scope, err := renderScope(a)
	if err != nil {
		return p, err
	}
	if err := writeIfAbsent(p.Path(p.PRDName()), []byte(scope), o.Force); err != nil {
		return p, err
	}
	arch, _ := fs.ReadFile("templates/ARCHITECTURE.md")
	if err := writeIfAbsent(p.Path("ARCHITECTURE.md"), arch, false); err != nil {
		return p, err
	}
	gi, _ := fs.ReadFile("templates/gitignore")
	if err := writeIfAbsent(p.Path(".gitignore"), gi, false); err != nil {
		return p, err
	}
	for _, f := range []string{store.GuardrailsFile, "CURRENT.md"} {
		b, _ := fs.ReadFile("templates/" + f)
		if err := writeIfAbsent(p.Path(f), b, false); err != nil {
			return p, err
		}
	}
	for _, role := range DefaultRoles {
		if err := writeIfAbsent(p.Path(PromptPath(role)), []byte(DefaultPrompt(role)), false); err != nil {
			return p, err
		}
	}

	if err := store.UpdateLedger(p, func(l *store.Ledger) error {
		if len(l.Tasks) == 0 {
			for _, m := range a.Milestones {
				if strings.TrimSpace(m) != "" {
					l.AddTask(m, "", "init", store.TaskPending, false)
				}
			}
		}
		return nil
	}); err != nil {
		return p, err
	}
	return p, nil
}

// DefaultRoles are the roles whose prompts every project gets.
// (interviewer and adopter prompts are written when first used.)
var DefaultRoles = []string{"builder", "reviewer", "planner", "spec-writer"}

// Upgrade adds what a project created by an older hloop is missing: new
// documents, prompts for new roles, folders and notification events. It never
// overwrites a file. With refreshPrompts it replaces the built-in role prompts
// with this version's, keeping the old ones as .bak files. It returns what it
// changed.
func Upgrade(p store.Project, refreshPrompts bool) ([]string, error) {
	var did []string
	if _, err := os.Stat(p.Path(store.PRDFile)); err != nil {
		if _, err := os.Stat(p.Path(store.LegacyScopeFile)); err == nil {
			if err := os.Rename(p.Path(store.LegacyScopeFile), p.Path(store.PRDFile)); err != nil {
				return did, err
			}
			did = append(did, "renamed .loop/PROJECT_SCOPE.md to .loop/PRD.md (Product Requirements Document; content unchanged)")
		}
	}
	for _, d := range []string{"specs", "reports"} {
		if _, err := os.Stat(p.Path(d)); err != nil {
			if err := os.MkdirAll(p.Path(d), 0o755); err != nil {
				return did, err
			}
			did = append(did, "created .loop/"+d+"/")
		}
	}
	for _, f := range []string{store.GuardrailsFile, "CURRENT.md"} {
		if _, err := os.Stat(p.Path(f)); err == nil {
			continue
		}
		b, _ := fs.ReadFile("templates/" + f)
		if err := store.WriteFile(p.Path(f), b, 0o644); err != nil {
			return did, err
		}
		did = append(did, "added .loop/"+f)
	}
	for _, role := range DefaultRoles {
		path := p.Path(filepath.FromSlash(PromptPath(role)))
		old, err := os.ReadFile(path)
		if err != nil {
			if err := store.WriteFile(path, []byte(DefaultPrompt(role)), 0o644); err != nil {
				return did, err
			}
			did = append(did, "added .loop/"+PromptPath(role))
			continue
		}
		if refreshPrompts && string(old) != DefaultPrompt(role) {
			if err := store.WriteFile(path+".bak", old, 0o644); err != nil {
				return did, err
			}
			if err := store.WriteFile(path, []byte(DefaultPrompt(role)), 0o644); err != nil {
				return did, err
			}
			did = append(did, "refreshed .loop/"+PromptPath(role)+" (old one kept as .bak)")
		}
	}
	cfg, err := p.LoadConfig()
	if err != nil {
		return did, err
	}
	if refreshPrompts {
		// prompts that exist only once used: refresh them if present
		refresh := map[string]string{}
		for _, role := range []string{"interviewer", "adopter", "tech-writer"} {
			refresh[PromptPath(role)] = DefaultPrompt(role)
		}
		// installed built-in profiles
		ps, _ := Profiles()
		for _, pr := range ps {
			if a, ok := cfg.Agents[pr.Name]; ok && pr.Source == "built-in" && a.Prompt != "" {
				refresh[a.Prompt] = pr.Body
			}
		}
		var keys []string
		for k := range refresh {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, rel := range keys {
			path := p.Path(filepath.FromSlash(rel))
			old, err := os.ReadFile(path)
			if err != nil || string(old) == refresh[rel] {
				continue
			}
			if err := store.WriteFile(path+".bak", old, 0o644); err != nil {
				return did, err
			}
			if err := store.WriteFile(path, []byte(refresh[rel]), 0o644); err != nil {
				return did, err
			}
			did = append(did, "refreshed .loop/"+rel+" (old one kept as .bak)")
		}
	}
	has := false
	for _, e := range cfg.Notify.Events {
		if e == "check" {
			has = true
		}
	}
	if !has {
		cfg.Notify.Events = append(cfg.Notify.Events, "check")
		if err := p.SaveConfig(cfg); err != nil {
			return did, err
		}
		did = append(did, "enabled the new 'check' notification (rejected finishes, guardrail alerts)")
	}
	// re-render the generated views (adds LESSONS.md)
	if err := store.UpdateLedger(p, func(*store.Ledger) error { return nil }); err != nil {
		return did, err
	}
	return did, nil
}

// SuggestGate returns a test command (argv) that looks right for the
// repository, for offering as the first gate. Nil if nothing obvious.
func SuggestGate(root string) []string {
	has := func(f string) bool { _, ok := statOK(filepath.Join(root, f)); return ok }
	switch {
	case has("go.mod"):
		return []string{"go", "test", "./..."}
	case has("Cargo.toml"):
		return []string{"cargo", "test"}
	case has("package.json"):
		if b, err := os.ReadFile(filepath.Join(root, "package.json")); err == nil && strings.Contains(string(b), `"test"`) && !strings.Contains(string(b), `no test specified`) {
			return []string{"npm", "test"}
		}
	case has("pubspec.yaml"):
		return []string{"flutter", "test"}
	case has("pytest.ini") || has("pyproject.toml") && fileHas(filepath.Join(root, "pyproject.toml"), "pytest"):
		return []string{"pytest"}
	}
	return nil
}

func fileHas(path, sub string) bool {
	b, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(b), sub)
}

func writeIfAbsent(path string, data []byte, overwrite bool) error {
	if _, err := os.Stat(path); err == nil && !overwrite {
		return nil
	}
	return store.WriteFile(path, data, 0o644)
}

func renderScope(a Answers) (string, error) {
	raw, _ := fs.ReadFile("templates/PRD.md")
	t, err := template.New("scope").Parse(string(raw))
	if err != nil {
		return "", err
	}
	cons := ""
	for _, c := range a.Constraints {
		if strings.TrimSpace(c) != "" {
			cons += "- " + strings.TrimSpace(c) + "\n"
		}
	}
	road := "[[FILL: phased roadmap; see the task queue for milestone detail]]"
	if len(a.Milestones) > 0 {
		var b strings.Builder
		b.WriteString("### Milestone 1\n")
		for _, m := range a.Milestones {
			if strings.TrimSpace(m) != "" {
				b.WriteString("- " + strings.TrimSpace(m) + "\n")
			}
		}
		road = b.String()
	}
	data := map[string]string{
		"Name":        a.Name,
		"Purpose":     fill(a.Purpose, "what problem does this solve?"),
		"Audience":    fill(a.Audience, "who uses this?"),
		"Stack":       fill(a.Stack, "language, framework, runtime"),
		"Backend":     fill(a.Backend, "database / backend / none"),
		"Hosting":     fill(a.Hosting, "where it runs and how it is deployed"),
		"Constraints": cons,
		"Roadmap":     road,
	}
	var out bytes.Buffer
	if err := t.Execute(&out, data); err != nil {
		return "", err
	}
	return out.String(), nil
}

// Verify lists problems that would stop an autonomous agent from working well.
func Verify(p store.Project) []string {
	var out []string
	prd := p.PRDName()
	if b, err := os.ReadFile(p.Path(prd)); err != nil {
		out = append(out, prd+" is missing")
	} else if n := strings.Count(string(b), fillMarker); n > 0 {
		out = append(out, fmt.Sprintf("%s still has %d unfilled [[FILL]] marker(s) (answer them with `hloop spec --project -i` or `hloop plan`)", prd, n))
	}
	l, _ := store.LoadLedger(p)
	if l.Remaining() == 0 {
		out = append(out, "no tasks queued: add one with `hloop task add \"...\"`")
	}
	return out
}

// OpenQuestions returns every unfilled [[FILL: ...]] marker with its file, so
// the human can be shown exactly what an agent could not determine.
func OpenQuestions(p store.Project) []string {
	var out []string
	for _, f := range []string{p.PRDName(), "ARCHITECTURE.md", store.GuardrailsFile} {
		b, err := os.ReadFile(p.Path(f))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			if strings.Contains(line, fillMarker) {
				out = append(out, fmt.Sprintf("%s: %s", f, strings.TrimSpace(line)))
			}
		}
	}
	return out
}

// ArchitectureIncomplete reports whether the optional ARCHITECTURE.md still has markers.
func ArchitectureIncomplete(p store.Project) bool {
	b, err := os.ReadFile(p.Path("ARCHITECTURE.md"))
	return err == nil && strings.Contains(string(b), fillMarker)
}

// DetectAdapter picks a harness that is installed. The order puts the ones
// hloop has run itself first.
func DetectAdapter() string {
	for _, name := range []string{"agy", "claude-code", "codex", "opencode", "gemini-cli", "copilot-cli", "cursor", "devin", "muse"} {
		a := adapter.Builtin()[name]
		if _, err := a.Resolve(nil); err == nil {
			return name
		}
	}
	return "claude-code"
}

// DetectStack guesses the tech stack from manifest files.
func DetectStack(root string) string {
	has := func(f string) bool { _, err := os.Stat(filepath.Join(root, f)); return err == nil }
	read := func(f string) string { b, _ := os.ReadFile(filepath.Join(root, f)); return string(b) }
	var found []string
	if has("package.json") {
		pj := read("package.json")
		switch {
		case strings.Contains(pj, `"next"`):
			found = append(found, "Next.js (Node.js)")
		case strings.Contains(pj, `"react"`):
			found = append(found, "React (Node.js)")
		default:
			found = append(found, "Node.js")
		}
	}
	if has("go.mod") {
		found = append(found, "Go")
	}
	if has("Cargo.toml") {
		found = append(found, "Rust")
	}
	if has("pyproject.toml") || has("requirements.txt") {
		found = append(found, "Python")
	}
	if has("pubspec.yaml") {
		found = append(found, "Flutter/Dart")
	}
	if has("pom.xml") || has("build.gradle") || has("build.gradle.kts") {
		found = append(found, "JVM (Java/Kotlin)")
	}
	if has("Gemfile") {
		found = append(found, "Ruby")
	}
	return strings.Join(found, " + ")
}

// DetectPurpose returns the first meaningful README line, if any.
func DetectPurpose(root string) string {
	for _, f := range []string{"README.md", "readme.md", "README"} {
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(strings.TrimLeft(line, "# "))
			if len(line) > 20 && !strings.HasPrefix(line, "![") && !strings.HasPrefix(line, "[") && !strings.HasPrefix(line, "<") {
				if len(line) > 160 {
					line = line[:160]
				}
				return line
			}
		}
	}
	return ""
}

// Ask runs the plain questionnaire (no AI needed). Blank input keeps the default.
func Ask(in io.Reader, out io.Writer, def Answers) Answers {
	r := bufio.NewReader(in)
	ask := func(q, d string) string {
		if d != "" {
			fmt.Fprintf(out, "%s [%s]: ", q, d)
		} else {
			fmt.Fprintf(out, "%s: ", q)
		}
		line, _ := r.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" {
			return d
		}
		return line
	}
	a := def
	a.Name = ask("Project name", def.Name)
	a.Purpose = ask("What problem does it solve (one sentence)", def.Purpose)
	a.Audience = ask("Who uses it", def.Audience)
	a.Stack = ask("Tech stack", def.Stack)
	a.Backend = ask("Database / backend", def.Backend)
	a.Hosting = ask("Hosting / deployment", def.Hosting)
	fmt.Fprintln(out, "Constraints agents must respect (one per line, blank line to finish):")
	a.Constraints = readLines(r)
	fmt.Fprintln(out, "First milestones in priority order, with acceptance criteria if you can (one per line, blank line to finish):")
	a.Milestones = readLines(r)
	return a
}

func readLines(r *bufio.Reader) []string {
	var out []string
	for {
		line, err := r.ReadString('\n')
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
		if line == "" || err != nil {
			return out
		}
	}
}

// ======================= adopt mode: discovery =======================

// DocFile is an existing document the harness should read.
type DocFile struct {
	Path string
	Size int64
	Kind string // readme | agent-instructions | planning | history | other
}

// Discovery is a bounded, factual summary of an existing repository.
type Discovery struct {
	Root       string
	IsGit      bool
	Branch     string
	DirtyFiles int
	Remote     string
	Commits    []string // "hash subject", newest first
	Branches   []string
	Manifests  []string
	Stack      string
	TestHints  []string
	Docs       []DocFile
	Tree       []string
	SecretLike []string
	HasLoop    bool
}

var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "target": true, "dist": true, "build": true,
	".venv": true, "venv": true, "__pycache__": true, ".next": true, ".nuxt": true, ".dart_tool": true,
	".gradle": true, ".idea": true, ".vscode": true, ".loop": true, "coverage": true, ".cache": true,
}

var manifestNames = []string{
	"package.json", "go.mod", "Cargo.toml", "pyproject.toml", "requirements.txt", "setup.py", "pubspec.yaml",
	"pom.xml", "build.gradle", "build.gradle.kts", "Gemfile", "composer.json", "Makefile", "Dockerfile",
	"docker-compose.yml", "docker-compose.yaml", "vercel.json", "netlify.toml", "fly.toml", "supabase/config.toml",
	"tsconfig.json", ".github/workflows",
}

func gitIn(dir string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	if cmd.Run() != nil {
		return ""
	}
	return strings.TrimSpace(out.String())
}

func classifyDoc(name string) (string, bool) {
	low := strings.ToLower(name)
	base := strings.TrimSuffix(strings.TrimSuffix(low, ".md"), ".txt")
	switch {
	case strings.HasPrefix(base, "readme"):
		return "readme", true
	case base == "claude" || base == "agents" || base == "gemini" || low == ".cursorrules" || low == "copilot-instructions.md" || base == "codex":
		return "agent-instructions", true
	case hasAnyPrefix(base, "todo", "roadmap", "plan", "progress", "spec", "decisions", "architecture", "design", "tasks", "backlog", "scope", "project_scope", "requirements", "notes", "prd", "product", "brief", "spec"):
		return "planning", true
	case hasAnyPrefix(base, "changelog", "history", "releases"):
		return "history", true
	case hasAnyPrefix(base, "contributing", "security", "license"):
		return "other", true
	}
	return "", false
}

func hasAnyPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// Discover inspects an existing project without modifying it.
func Discover(root string) Discovery {
	d := Discovery{Root: root}
	_, d.HasLoop = statOK(filepath.Join(root, store.DirName, store.ConfigFile))
	if gitIn(root, "rev-parse", "--is-inside-work-tree") == "true" {
		d.IsGit = true
		d.Branch = gitIn(root, "rev-parse", "--abbrev-ref", "HEAD")
		if s := gitIn(root, "status", "--porcelain"); s != "" {
			d.DirtyFiles = len(strings.Split(s, "\n"))
		}
		d.Remote = redactRemote(gitIn(root, "remote", "get-url", "origin"))
		if l := gitIn(root, "log", "-n", "25", "--pretty=format:%h %s"); l != "" {
			d.Commits = strings.Split(l, "\n")
		}
		if b := gitIn(root, "branch", "--format=%(refname:short)"); b != "" {
			d.Branches = strings.Split(b, "\n")
			if len(d.Branches) > 20 {
				d.Branches = d.Branches[:20]
			}
		}
	}
	for _, m := range manifestNames {
		if _, ok := statOK(filepath.Join(root, filepath.FromSlash(m))); ok {
			d.Manifests = append(d.Manifests, m)
		}
	}
	d.Stack = DetectStack(root)
	d.TestHints = testHints(root)

	seen := map[string]bool{}
	addDoc := func(rel string, kind string) {
		if seen[rel] {
			return
		}
		if st, ok := statOK(filepath.Join(root, filepath.FromSlash(rel))); ok && !st.IsDir() {
			seen[rel] = true
			d.Docs = append(d.Docs, DocFile{Path: rel, Size: st.Size(), Kind: kind})
		}
	}
	for _, dir := range []string{"", "docs", "doc", ".github", ".cursor/rules", ".claude"} {
		ents, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir)))
		if err != nil {
			continue
		}
		n := 0
		for _, e := range ents {
			if e.IsDir() {
				continue
			}
			kind, ok := classifyDoc(e.Name())
			if !ok && dir != "" && (strings.HasSuffix(strings.ToLower(e.Name()), ".md")) {
				kind, ok = "other", true
			}
			if ok && n < 25 {
				rel := e.Name()
				if dir != "" {
					rel = dir + "/" + e.Name()
				}
				addDoc(rel, kind)
				n++
			}
		}
	}
	sort.SliceStable(d.Docs, func(i, j int) bool { return docRank(d.Docs[i].Kind) < docRank(d.Docs[j].Kind) })

	d.Tree = walkTree(root, 2, 90)
	d.SecretLike = secretLike(root)
	return d
}

func docRank(k string) int {
	switch k {
	case "agent-instructions":
		return 0
	case "planning":
		return 1
	case "readme":
		return 2
	case "history":
		return 3
	}
	return 4
}

func statOK(p string) (os.FileInfo, bool) {
	st, err := os.Stat(p)
	return st, err == nil
}

// redactRemote removes credentials embedded in a remote URL.
func redactRemote(u string) string {
	if i := strings.Index(u, "://"); i >= 0 {
		rest := u[i+3:]
		if at := strings.Index(rest, "@"); at >= 0 && !strings.Contains(rest[:at], "/") {
			return u[:i+3] + rest[at+1:]
		}
	}
	return u
}

func testHints(root string) []string {
	var h []string
	has := func(f string) bool { _, ok := statOK(filepath.Join(root, f)); return ok }
	if b, err := os.ReadFile(filepath.Join(root, "package.json")); err == nil && strings.Contains(string(b), `"test"`) {
		h = append(h, "npm test")
	}
	if has("go.mod") {
		h = append(h, "go test ./...")
	}
	if has("Cargo.toml") {
		h = append(h, "cargo test")
	}
	if has("pyproject.toml") || has("requirements.txt") || has("pytest.ini") {
		h = append(h, "pytest")
	}
	if has("pubspec.yaml") {
		h = append(h, "flutter test")
	}
	if has("Makefile") {
		h = append(h, "make test (if defined)")
	}
	return h
}

func walkTree(root string, maxDepth, maxEntries int) []string {
	var out []string
	var walk func(dir, prefix string, depth int)
	walk = func(dir, prefix string, depth int) {
		ents, err := os.ReadDir(dir)
		if err != nil || len(out) >= maxEntries {
			return
		}
		for _, e := range ents {
			if len(out) >= maxEntries {
				out = append(out, prefix+"...")
				return
			}
			name := e.Name()
			if e.IsDir() {
				if skipDirs[name] {
					continue
				}
				out = append(out, prefix+name+"/")
				if depth < maxDepth {
					walk(filepath.Join(dir, name), prefix+"  ", depth+1)
				}
			} else if depth == 1 || strings.HasSuffix(name, ".md") || manifestIs(name) {
				out = append(out, prefix+name)
			}
		}
	}
	walk(root, "", 1)
	return out
}

func manifestIs(name string) bool {
	for _, m := range manifestNames {
		if m == name {
			return true
		}
	}
	return false
}

// secretLike lists file names only (never contents) that look like secrets.
func secretLike(root string) []string {
	var out []string
	ents, _ := os.ReadDir(root)
	for _, e := range ents {
		n := strings.ToLower(e.Name())
		if e.IsDir() {
			continue
		}
		if n == ".env" || strings.HasPrefix(n, ".env.") || strings.HasSuffix(n, ".pem") || strings.HasSuffix(n, ".key") ||
			n == "id_rsa" || n == "credentials.json" || n == "service-account.json" || n == "secrets.json" {
			out = append(out, e.Name())
		}
	}
	return out
}

// Summary renders the discovery as the context block given to the adoption agent.
func (d Discovery) Summary() string {
	var b strings.Builder
	b.WriteString("DISCOVERY SUMMARY (generated by hloop from the repository; facts only, verify by reading the files)\n")
	fmt.Fprintf(&b, "- Root: %s\n", d.Root)
	if d.IsGit {
		fmt.Fprintf(&b, "- Git: yes, branch %q, %d uncommitted file(s)", d.Branch, d.DirtyFiles)
		if d.Remote != "" {
			fmt.Fprintf(&b, ", origin %s", d.Remote)
		}
		b.WriteString("\n")
		if len(d.Branches) > 0 {
			fmt.Fprintf(&b, "- Branches: %s\n", strings.Join(d.Branches, ", "))
		}
	} else {
		b.WriteString("- Git: NOT a git repository (loop-only guard and ledger commits are disabled; consider `git init`)\n")
	}
	if d.Stack != "" {
		fmt.Fprintf(&b, "- Detected stack: %s\n", d.Stack)
	}
	if len(d.Manifests) > 0 {
		fmt.Fprintf(&b, "- Manifests / config found: %s\n", strings.Join(d.Manifests, ", "))
	}
	if len(d.TestHints) > 0 {
		fmt.Fprintf(&b, "- Likely test commands: %s\n", strings.Join(d.TestHints, "; "))
	}
	if len(d.Docs) > 0 {
		b.WriteString("- Existing documents to read (kind, path):\n")
		for _, f := range d.Docs {
			fmt.Fprintf(&b, "  - [%s] %s (%d bytes)\n", f.Kind, f.Path, f.Size)
		}
	} else {
		b.WriteString("- No existing documents found; rely on the code and git history.\n")
	}
	if len(d.SecretLike) > 0 {
		fmt.Fprintf(&b, "- Secret-like files present (NEVER open or copy their contents): %s\n", strings.Join(d.SecretLike, ", "))
	}
	if len(d.Commits) > 0 {
		b.WriteString("- Recent commits (newest first):\n")
		for _, c := range d.Commits {
			fmt.Fprintf(&b, "  - %s\n", c)
		}
	}
	if len(d.Tree) > 0 {
		b.WriteString("- Layout (depth 2):\n")
		for _, t := range d.Tree {
			fmt.Fprintf(&b, "  %s\n", t)
		}
	}
	return b.String()
}

// Prefill converts a discovery into the starting answers for PRD.md.
func (d Discovery) Prefill(name string) Answers {
	if name == "" {
		name = filepath.Base(d.Root)
	}
	return Answers{Name: name, Purpose: DetectPurpose(d.Root), Stack: d.Stack}
}

// AdoptInstruction is the full, exact instruction block sent with the adoption prompt.
func (d Discovery) AdoptInstruction() string {
	return d.Summary() + "\nThe loop directory `.loop/` has been created with a PRD.md that is only pre-filled with the stack and purpose detected above. Follow the procedure in your instructions to complete it and to build the ledger."
}

// AdoptPrompt returns the adoption prompt exactly as an agent will see it, so
// it can be pasted into any harness by hand (`hloop adopt --print-prompt`).
func AdoptPrompt(d Discovery) string {
	return strings.TrimRight(DefaultPrompt("adopter"), "\n") + "\n\n---\n## Run context (generated by hloop; authoritative)\n" + d.AdoptInstruction() + "\n"
}
