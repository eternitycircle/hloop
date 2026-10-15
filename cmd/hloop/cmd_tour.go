package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// The tour is an interactive walkthrough on a throwaway demo project. It runs
// real hloop commands, in a temporary folder, with its own temporary hloop
// settings (so it never touches your projects, registry, trust records or
// phone settings), and it runs a real loop with a built-in demo agent instead
// of an AI, so it costs nothing and needs no harness.

type tour struct {
	exe, dir, home string
	out            io.Writer
	in             *bufio.Reader
	quiet          bool // replaying earlier steps for --step
}

type tourStep struct {
	title string
	run   func(t *tour)
}

func (t *tour) say(paragraphs ...string) {
	if t.quiet {
		return
	}
	for _, p := range paragraphs {
		fmt.Fprintln(t.out, wrap(p, 78))
		fmt.Fprintln(t.out)
	}
}

func wrap(s string, width int) string {
	var out []string
	for _, para := range strings.Split(s, "\n") {
		if strings.HasPrefix(para, "  ") || strings.HasPrefix(para, "|") || len(para) <= width {
			out = append(out, para)
			continue
		}
		cont := ""
		if strings.HasPrefix(para, "- ") {
			cont = "  "
		}
		line := ""
		for _, w := range strings.Fields(para) {
			if len(line)+len(w)+1 > width && line != "" {
				out = append(out, line)
				line = cont
			}
			if line != "" && line != cont {
				line += " "
			}
			line += w
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func (t *tour) env() []string {
	return append(os.Environ(),
		"HLOOP_HOME="+t.home, "HLOOP_TOUR=1",
		"GIT_AUTHOR_NAME=hloop tour", "GIT_AUTHOR_EMAIL=tour@hloop.invalid",
		"GIT_COMMITTER_NAME=hloop tour", "GIT_COMMITTER_EMAIL=tour@hloop.invalid",
		"HLOOP_AGENT=", "HLOOP_PROJECT=", "HLOOP_ROLE=")
}

// hl runs an hloop command in the demo project and shows it.
func (t *tour) hl(maxLines int, args ...string) string {
	cmd := exec.Command(t.exe, args...)
	cmd.Dir, cmd.Env = t.dir, t.env()
	var b bytes.Buffer
	cmd.Stdout, cmd.Stderr = &b, &b
	cmd.Stdin = strings.NewReader("")
	_ = cmd.Run()
	if !t.quiet {
		fmt.Fprintf(t.out, "  $ hloop %s\n", quoteArgs(args))
		t.print(b.String(), maxLines)
	}
	return b.String()
}

func (t *tour) git(args ...string) {
	cmd := exec.Command("git", args...)
	cmd.Dir, cmd.Env = t.dir, t.env()
	out, err := cmd.CombinedOutput()
	if !t.quiet {
		fmt.Fprintf(t.out, "  $ git %s\n", quoteArgs(args))
		if err != nil {
			t.print(string(out), 6)
		}
	}
}

func (t *tour) print(s string, maxLines int) {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if maxLines > 0 && len(lines) > maxLines {
		lines = append(lines[:maxLines], fmt.Sprintf("  ... (%d more lines)", len(lines)-maxLines))
	}
	for _, l := range lines {
		fmt.Fprintln(t.out, "    "+l)
	}
	fmt.Fprintln(t.out)
}

// show prints part of a file in the demo project.
func (t *tour) show(rel string, from, maxLines int) {
	if t.quiet {
		return
	}
	b, err := os.ReadFile(filepath.Join(t.dir, filepath.FromSlash(rel)))
	if err != nil {
		fmt.Fprintf(t.out, "    (%s not found)\n\n", rel)
		return
	}
	lines := strings.Split(string(b), "\n")
	if from > len(lines) {
		from = len(lines)
	}
	fmt.Fprintf(t.out, "  --- %s ---\n", rel)
	t.print(strings.Join(lines[from:], "\n"), maxLines)
}

func quoteArgs(args []string) string {
	var out []string
	for _, a := range args {
		if strings.ContainsAny(a, " \"'") {
			a = strconv.Quote(a)
		}
		out = append(out, a)
	}
	return strings.Join(out, " ")
}

func tourSteps() []tourStep {
	return []tourStep{
		{"Welcome", func(t *tour) {
			t.say("hloop runs an AI coding agent (Claude Code, Codex, Gemini CLI, ...) in a loop on your project, while it keeps the plan, the rules and the checks. Your harness is the worker; hloop is the supervisor.",
				"This tour builds a throwaway project in a temporary folder and runs real hloop commands on it. It uses its own temporary settings, so it never touches your projects, and instead of an AI it uses a tiny built-in demo agent, so it costs nothing. Rerun it any time with `hloop tour`, or jump to a step with `hloop tour --step 5`.",
				"Demo project: "+t.dir)
			t.git("init", "-q")
			_ = os.WriteFile(filepath.Join(t.dir, "README.md"), []byte("# Greeter\n\nA tiny demo project for the hloop tour.\n"), 0o644)
			t.git("add", "-A")
			t.git("commit", "-q", "-m", "initial commit")
		}},
		{"Create the loop for a project", func(t *tour) {
			t.say("`hloop init` adds a hidden `.loop/` folder to a project. In a real project you would run `hloop init --ai` (an agent interviews you and writes the documents) or, for a project already under way, `hloop adopt` (an agent reads your repo and docs).")
			t.hl(6, "init", "--yes", "--name", "greeter", "--adapter", "tour")
			t.hl(0, "config", "set", "turn_pause_seconds", "0")
			t.say("Everything hloop knows lives in .loop/:")
			if !t.quiet {
				ents, _ := os.ReadDir(filepath.Join(t.dir, ".loop"))
				var names []string
				for _, e := range ents {
					if e.IsDir() {
						names = append(names, e.Name()+"/")
					} else if !strings.HasPrefix(e.Name(), ".") {
						names = append(names, e.Name())
					}
				}
				t.print(strings.Join(names, "  "), 0)
			}
			t.say("The ones you will care about:\n" +
				"- PRD.md: the Product Requirements Document: what you are building, for whom, and the goals and constraints.\n" +
				"- GUARDRAILS.md: what agents may do alone, what they must ask about, and what they must never do.\n" +
				"- PROGRESS.md: the task board. PROGRESS_LOG.md: the complete history, never trimmed.\n" +
				"- CURRENT.md: a short note the agent rewrites each turn for the next one, which starts with no memory.\n" +
				"Full list: `hloop docs files`.")
			t.git("add", "-A")
			t.git("commit", "-q", "-m", "add the loop")
		}},
		{"The rules agents follow", func(t *tour) {
			t.say("GUARDRAILS.md sorts actions into four tiers. Agents read it every turn. It is part of the configuration you approve: if an agent (or a git pull) changes it, hloop starts nothing until you review it with `hloop trust --show` and approve with `hloop trust`.")
			if !t.quiet {
				b, _ := os.ReadFile(filepath.Join(t.dir, ".loop", "GUARDRAILS.md"))
				var heads []string
				for _, l := range strings.Split(string(b), "\n") {
					if strings.HasPrefix(l, "## ") {
						heads = append(heads, l)
					}
				}
				t.print(strings.Join(heads, "\n"), 0)
			}
			t.say("You fill in the project-specific boundaries yourself, for example \"never touch the production database\".")
		}},
		{"Define work with acceptance criteria", func(t *tour) {
			t.say("A task says what to do, and its acceptance criteria say what done means. Each criterion is a checkable statement, then `::`, then how it is verified. Must criteria block completion; Should and Could do not.")
			t.hl(3, "task", "add", "Add a greeting file", "--ac", "hello.txt exists and contains the word hello :: gate greeting", "--should", "the file ends with a newline")
			t.hl(0, "task", "show", "T1")
			t.say("For anything bigger, `hloop spec \"<feature>\"` has your AI write a full spec (problem, out of scope, behaviour, test boundaries, stop conditions) plus tasks like this one. Add -i and it interviews you first; --from notes.md and it reads your notes. `hloop spec --project -i` does the same for the whole project's PRD. The tour does not run it because it needs a real AI.",
				"`hloop spec check` reviews the definition without any AI:")
			t.hl(0, "spec", "check")
			t.say("It warns that there are no gates yet. Next step.")
		}},
		{"Gates: hloop checks the work itself", func(t *tour) {
			t.say("A gate is a command hloop runs itself when an agent says a task is done. Usually it is your test suite (`hloop gate add test -- npm test`). If it fails, the task is reopened and the agent is told why.",
				"The tour's gate checks hello.txt. `--where export` runs it on a fresh copy of the last commit, so a file the agent created but never committed does not count:")
			t.hl(2, "gate", "add", "greeting", "--where", "export", "--", t.exe, "__tour-agent", "check", "hello.txt", "hello")
			t.hl(4, "gate", "run")
			t.say("It fails, correctly: there is no hello.txt yet. Optionally `hloop config set tdd on` goes further: the agent must commit failing tests before the code, and hloop proves the tests failed before and pass after.")
			t.git("add", "-A")
			t.git("commit", "-q", "-m", "gate")
		}},
		{"Run the loop", func(t *tour) {
			t.say("`hloop run` hands the current task to your harness, one turn at a time. Here the demo agent plays the harness. Watch it make a classic mistake on the first turn: it writes hello.txt but forgets to commit it.")
			t.hl(40, "run")
			t.say("The agent's own output from its last turn:")
			t.hl(12, "logs", "--agent", "-n", "8")
			t.say("What happened:\n" +
				"- Turn 1: the agent marked the task done. hloop ran the gate on a clean copy of the last commit, hello.txt was not there, so hloop REJECTED the finish and reopened the task.\n" +
				"- Turn 2: the agent was told exactly why (the gate's output is in its prompt), committed the file and finished again. This time the gate passed, so the task is verified and hloop wrote a completion report.\n" +
				"- The GUARD line: the demo agent committed straight onto master, which GUARDRAILS.md says not to do. hloop checks git after every turn and noticed. Here it only notifies; a rewritten history would stop the loop. (`hloop docs verification`)\n" +
				"With a real harness this is the same loop, and `hloop schedule install` runs it every 8 hours.")
		}},
		{"What hloop recorded", func(t *tour) {
			t.say("Status at a glance:")
			t.hl(0, "status")
			t.say("The completion report is written by hloop, not by the agent: criteria with evidence, which checks ran, which guardrail detectors fired, commits and files changed, reviews.")
			t.show(".loop/reports/T1.md", 0, 28)
			t.say("PROGRESS_LOG.md keeps the complete history, including the rejected attempt:")
			t.show(".loop/PROGRESS_LOG.md", 3, 22)
		}},
		{"When the loop needs you", func(t *tour) {
			t.say("Agents never guess at things that are yours to decide. They raise a blocker (the loop pauses and notifies you), or record a decision for later. You can do the same:")
			t.hl(2, "blocker", "add", "Create an API key for the mail service and put it in .env")
			t.hl(4, "status")
			t.hl(2, "blocker", "resolve", "B1", "--answer", "Done, it is in .env as MAIL_KEY", "--no-run")
			t.say("Your answer goes into the next turn's prompt. More ways to steer:")
			t.hl(2, "decision", "add", "Which mail provider", "--options", "Postmark | SES", "--needs", "expected volume")
			t.hl(2, "idea", "add", "Greet in several languages")
			t.hl(2, "lesson", "add", "Run the gate before marking a task done")
			t.hl(2, "note", "focus on the README next")
			t.say("Ideas never run until you promote them (`hloop idea promote I1`). Lessons are told to every agent turn; lessons an agent proposes wait for your approval.")
		}},
		{"More agents, your phone, and the schedule", func(t *tour) {
			t.say("Besides the builder you can add ready-made agents: reviewers run in their own session after each finished task and judge it against its criteria.")
			t.hl(14, "agent", "catalog")
			t.say("Your phone:\n" +
				"  hloop notify setup telegram     two-way: alerts, answer blockers, /add tasks, /idea\n" +
				"  hloop notify setup ntfy         one-way alerts\n" +
				"Running on its own:\n" +
				"  hloop schedule install          cron / launchd / Task Scheduler\n" +
				"The tour does not run these: they would change your real settings.")
		}},
		{"Your turn", func(t *tour) {
			t.say("On a real project:\n"+
				"  hloop init --ai                 (or: hloop adopt, for a project already under way)\n"+
				"  hloop gate add test -- <your test command>\n"+
				"  hloop spec -i \"<first feature>\"\n"+
				"  hloop doctor\n"+
				"  hloop run\n"+
				"Read SECURITY.md (`hloop docs safety`) before leaving it unattended: harnesses run with their own approval prompts off, so use a VM, container or separate user.",
				"Reference: `hloop docs` (topics), `hloop docs find <word>`, `hloop help <command>`. This tour: `hloop tour` again any time.")
		}},
	}
}

func cmdTour(args []string) error {
	fs := newFS("tour")
	step := fs.Int("step", 1, "start at this step (earlier steps are replayed silently)")
	list := fs.Bool("list", false, "list the steps")
	keep := fs.Bool("keep", false, "keep the demo project afterwards and print where it is")
	noPause := fs.Bool("no-pause", false, "do not wait for Enter between steps")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	if err := humanOnly("take the tour"); err != nil {
		return err
	}
	steps := tourSteps()
	if *list {
		for i, s := range steps {
			fmt.Printf("%2d. %s\n", i+1, s.title)
		}
		return nil
	}
	if *step < 1 || *step > len(steps) {
		return fmt.Errorf("--step must be between 1 and %d", len(steps))
	}
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("the tour needs git on your PATH")
	}
	root, err := os.MkdirTemp("", "hloop-tour-")
	if err != nil {
		return err
	}
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	t := &tour{exe: exePath(), dir: filepath.Join(root, "greeter"), home: filepath.Join(root, "settings"), out: os.Stdout, in: bufio.NewReader(os.Stdin)}
	_ = os.MkdirAll(t.dir, 0o755)
	_ = os.MkdirAll(filepath.Join(t.home, "adapters"), 0o755)
	adj, _ := json.MarshalIndent(map[string]any{
		"name": "tour", "display": "hloop tour demo agent (no AI)", "bin": t.exe,
		"new": []string{"__tour-agent", "{prompt}"}, "check": []string{"version"},
		"env": map[string]string{"HLOOP_TOUR": "1"}, "status": "verified",
		"notes": "Built into hloop for `hloop tour`. Pretends to be a coding agent.",
	}, "", "  ")
	_ = os.WriteFile(filepath.Join(t.home, "adapters", "tour.json"), adj, 0o644)
	_ = os.WriteFile(filepath.Join(t.home, "notify.json"), []byte(`{"no_desktop": true}`), 0o600)
	defer func() {
		if *keep {
			fmt.Printf("Demo project kept at %s (its hloop settings are in %s; use HLOOP_HOME=%s to work with it).\n", t.dir, t.home, t.home)
			return
		}
		os.RemoveAll(root)
	}()

	interactive := !*noPause && isTTY()
	for i, s := range steps {
		if i+1 < *step {
			t.quiet = true
			s.run(t)
			t.quiet = false
			continue
		}
		fmt.Printf("\n== Step %d of %d: %s ==\n\n", i+1, len(steps), s.title)
		s.run(t)
		if i+1 < len(steps) && interactive {
			fmt.Print("[Enter] next step   [q] quit > ")
			line, err := t.in.ReadString('\n')
			if err != nil || strings.EqualFold(strings.TrimSpace(line), "q") {
				fmt.Printf("\nStopped. Continue later with `hloop tour --step %d`.\n", i+2)
				return nil
			}
		}
	}
	return nil
}

var tourTaskRe = regexp.MustCompile(`CURRENT TASK: (T\d+)`)

// cmdTourAgent is the demo "harness" the tour runs instead of an AI, and the
// tour's gate. It refuses to do anything outside the tour.
func cmdTourAgent(args []string) error {
	if os.Getenv("HLOOP_TOUR") != "1" {
		return fmt.Errorf("this is an internal command used by `hloop tour`")
	}
	if len(args) == 3 && args[0] == "check" {
		b, err := os.ReadFile(args[1])
		if err != nil || !strings.Contains(string(b), args[2]) {
			fmt.Printf("%s is missing from this copy of the project, or does not contain %q\n", args[1], args[2])
			return errSilent
		}
		fmt.Printf("%s contains %q\n", args[1], args[2])
		return nil
	}
	proj, bin := os.Getenv("HLOOP_PROJECT"), os.Getenv("HLOOP_BIN")
	if proj == "" || bin == "" {
		return fmt.Errorf("the tour agent only runs inside `hloop run`")
	}
	if cfg, err := (loadCfg(proj)); err != nil || cfg != "greeter" {
		return fmt.Errorf("the tour agent only works on the tour's demo project")
	}
	m := tourTaskRe.FindStringSubmatch(strings.Join(args, " "))
	if m == nil {
		fmt.Println("demo agent: no current task in the prompt; nothing to do")
		return nil
	}
	id := m[1]
	run := func(name string, a ...string) {
		c := exec.Command(name, a...)
		c.Dir = proj
		out, _ := c.CombinedOutput()
		if s := strings.TrimSpace(string(out)); s != "" {
			fmt.Println("  " + strings.ReplaceAll(s, "\n", "\n  "))
		}
	}
	marker := filepath.Join(proj, ".loop", "runs", "tour-first-turn-done")
	if _, err := os.Stat(marker); err != nil {
		fmt.Println("demo agent: writing hello.txt ... (and forgetting to commit it)")
		_ = os.WriteFile(filepath.Join(proj, "hello.txt"), []byte("hello, world\n"), 0o644)
		_ = os.WriteFile(marker, []byte("1"), 0o600)
	} else {
		fmt.Println("demo agent: hloop says the gate failed on the committed copy; committing hello.txt")
		run("git", "add", "hello.txt")
		run("git", "commit", "-q", "-m", "feat: add greeting file")
	}
	run(bin, "task", "ac", id, "pass", "AC1", "--evidence", "hello.txt contains 'hello' (checked by the greeting gate)")
	run(bin, "task", "done", id, "--summary", "Added hello.txt with a greeting.")
	return nil
}

func loadCfg(root string) (string, error) {
	var c struct {
		Name string `json:"name"`
	}
	b, err := os.ReadFile(filepath.Join(root, ".loop", "config.json"))
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return "", err
	}
	return c.Name, nil
}
