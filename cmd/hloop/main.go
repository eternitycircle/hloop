// Command hloop supervises autonomous coding agents: it owns the project
// definition files, the task ledger, scheduling, notifications and review,
// and drives any coding-agent CLI through a small adapter.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"hloop/internal/engine"
	"hloop/internal/notify"
	"hloop/internal/store"
)

var version = "dev"

var (
	errSilent = errors.New("") // already reported; exit 1
	errHelp   = errors.New("help")
)

const usageText = `hloop - supervise autonomous coding agents with any harness

New here?  hloop tour          interactive walkthrough on a demo project (rerun any time)
Reference: hloop docs [topic]  built-in documentation;  hloop help <command>

Set up
  hloop init [dir]              new project: questionnaire (or --ai interview) -> .loop/ files
  hloop adopt [dir]             existing project: scan the repo and have your harness write the .loop/ files
  hloop projects                list/add/remove registered projects
  hloop doctor                  check your setup

Run
  hloop run                     run the loop now (resumes where it stopped)
  hloop status [--all]          where things stand
  hloop pause | resume          stop or continue scheduled runs
  hloop logs [-f]               loop log (add --agent for the latest agent output)
  hloop schedule install        run automatically (cron / launchd / Task Scheduler)

Defining the work
  hloop spec "feature" [-i]     an agent writes a spec and queues tasks with acceptance criteria (-i: interview me)
  hloop spec check              lint the project definition: missing/vague criteria, unfinished specs (no AI)
  hloop plan "instruction"      have a planner agent revise scope/architecture/tasks

Project files (edited through commands, never by hand)
  hloop task add "title" --ac "statement :: how verified" [--should ...] [--kind docs]
  hloop task list|show|done|ac|red|report|skip|top|approve
  hloop blocker add|list|resolve
  hloop decision add|list|resolve
  hloop idea add|list|promote|drop     parked ideas: never worked on until promoted
  hloop lesson add|list|approve|retire durable facts every agent turn is told
  hloop note "text"             steering note for the next agent turn

Verification
  hloop gate add|list|remove|run      commands hloop runs itself before accepting "done"
  hloop config set tdd on             require a failing tests-only commit first (red/green proof)

Releasing
  hloop release [VERSION]       when the work is done: checks, release notes, CHANGELOG, user + developer docs
  hloop release list|notes      past releases

Agents
  hloop agent catalog|install <profile>...   ready-made agents: architect, security-auditor, test-engineer, docs-writer, ...
  hloop agent list|add|remove|run|order      builder, reviewer, planner, or your own
  hloop review run|submit|list              independent review of recent work
  hloop adapter list|check|show|add         harness CLIs hloop can drive

Phone
  hloop notify setup ntfy|telegram          one-way alerts or two-way chat (pair with a one-time code)
  hloop notify test|status|off|listen
  hloop notify service install|remove       keep the chat listener running

Other
  hloop trust [--show]          review and approve a project's loop configuration (needed after a clone/pull or if it changed)
  hloop skill install           let interactive Claude Code / Codex sessions drive hloop (writes an Agent Skill)
  hloop init --upgrade          add what a project made by an older hloop is missing
  hloop config show|set key value
  hloop version

Use -C <dir-or-project-name> to target a project other than the current directory.
`

func usage(w io.Writer) { fmt.Fprint(w, usageText) }

func main() {
	if len(os.Args) < 2 {
		usage(os.Stdout)
		if _, err := os.Stat(filepath.Join(store.ConfigHome(), "projects.json")); err != nil {
			fmt.Println("\nFirst time? Start with `hloop tour`: it walks you through everything on a throwaway project.")
		}
		return
	}
	name, args := os.Args[1], os.Args[2:]
	cmds := map[string]func([]string) error{
		"init": cmdInit, "adopt": cmdAdopt, "projects": cmdProjects, "doctor": cmdDoctor, "config": cmdConfig,
		"run": cmdRun, "status": cmdStatus, "pause": cmdPause, "resume": cmdResume, "logs": cmdLogs,
		"task": cmdTask, "blocker": cmdBlocker, "decision": cmdDecision, "note": cmdNote, "plan": cmdPlan,
		"agent": cmdAgent, "review": cmdReview, "adapter": cmdAdapter,
		"notify": cmdNotify, "schedule": cmdSchedule, "trust": cmdTrust,
		"spec": cmdSpec, "gate": cmdGate, "idea": cmdIdea, "lesson": cmdLesson, "skill": cmdSkill,
		"docs": cmdDocs, "tour": cmdTour, "help": cmdHelp, "__tour-agent": cmdTourAgent, "release": cmdRelease,
	}
	switch name {
	case "-h", "--help":
		usage(os.Stdout)
		return
	case "version", "-v", "--version":
		fmt.Println("hloop", version)
		return
	}
	if who := os.Getenv("HLOOP_AGENT"); who != "" && !agentMayRun(name, args) {
		fmt.Fprintf(os.Stderr, "hloop: agent %q may not run `hloop %s`: that belongs to the human (file a blocker if you need it)\n", who, name)
		os.Exit(1)
	}
	fn, ok := cmds[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "hloop: unknown command %q\n\n", name)
		usage(os.Stderr)
		os.Exit(2)
	}
	if err := fn(args); err != nil {
		if errors.Is(err, errHelp) {
			return
		}
		if err != errSilent {
			fmt.Fprintln(os.Stderr, "hloop:", err)
		}
		os.Exit(1)
	}
}

// ---------- shared helpers ----------

func newFS(name string) *flag.FlagSet {
	fs := flag.NewFlagSet("hloop "+name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	return fs
}

// parseArgs allows flags and positional arguments in any order.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, errHelp
			}
			return nil, errSilent
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return pos, nil
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}

// strList is a repeatable string flag.
type strList []string

func (s *strList) String() string     { return strings.Join(*s, ",") }
func (s *strList) Set(v string) error { *s = append(*s, v); return nil }

// resolveProject finds the project to act on: -C (directory or registered
// name), then $HLOOP_PROJECT (set for agents), then the current directory.
func resolveProject(dirFlag string) (store.Project, error) {
	if dirFlag != "" {
		if st, err := os.Stat(dirFlag); err == nil && st.IsDir() {
			return store.Find(dirFlag)
		}
		if p, err := store.Lookup(dirFlag); err == nil {
			return p, nil
		}
		return store.Project{}, fmt.Errorf("%q is neither a directory nor a registered project (see `hloop projects`)", dirFlag)
	}
	if env := os.Getenv("HLOOP_PROJECT"); env != "" {
		if p, err := store.Find(env); err == nil {
			return p, nil
		}
	}
	wd, err := os.Getwd()
	if err != nil {
		return store.Project{}, err
	}
	return store.Find(wd)
}

func isTTY() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// asAgent reports whether the command was launched by an agent inside a run.
func asAgent() string { return os.Getenv("HLOOP_AGENT") }

func humanOnly(what string) error {
	if a := asAgent(); a != "" {
		return fmt.Errorf("agent %q may not %s: that decision belongs to the human (file a blocker instead)", a, what)
	}
	return nil
}

func source() string {
	if a := asAgent(); a != "" {
		return "agent:" + a
	}
	return "cli"
}

func prompt(r *bufio.Reader, q, def string) string {
	if def != "" {
		fmt.Printf("%s [%s]: ", q, def)
	} else {
		fmt.Printf("%s: ", q)
	}
	line, _ := r.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

func exePath() string {
	exe, err := os.Executable()
	if err != nil {
		return "hloop"
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		return r
	}
	return exe
}

// openLog opens (and rotates) the project's loop log.
func openLog(p store.Project) *os.File {
	path := p.Path("runs", "hloop.log")
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	if st, err := os.Stat(path); err == nil && st.Size() > 5<<20 {
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil
	}
	return f
}

// newEngine builds an engine writing to out with notifications wired up.
func newEngine(p store.Project, out io.Writer) (*engine.Engine, error) {
	cfg, err := p.LoadConfig()
	if err != nil {
		return nil, err
	}
	e := engine.New(p, cfg)
	e.Exe = exePath()
	e.Log = out
	e.N = notify.NewDispatcher(out)
	return e, nil
}

func projectName(p store.Project, cfg store.Config) string {
	if n := store.RegisteredName(p.Root); n != "" {
		return n
	}
	return store.SafeName(cfg.Name)
}
