package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"hloop/internal/engine"
	"hloop/internal/store"
)

// ---------- task ac: acceptance criteria ----------

const taskACUsage = `usage:
  hloop task ac <task> list
  hloop task ac <task> add "statement :: how it is verified" [--priority must|should|could]
  hloop task ac <task> pass|fail <AC id> --evidence "test name, command output or observation"
  hloop task ac <task> reset <AC id>
  hloop task ac <task> amend <AC id> "new statement" --reason "why"     (humans only)
  hloop task ac <task> remove <AC id>                                  (humans only)`

func cmdTaskAC(args []string) error {
	fs := newFS("task ac")
	dir := fs.String("C", "", "project directory or name")
	evidence := fs.String("evidence", "", "what proves it")
	prio := fs.String("priority", "must", "must, should or could")
	reason := fs.String("reason", "", "amend: why the criterion changes")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) < 2 {
		return fmt.Errorf("%s", taskACUsage)
	}
	id, verb := pos[0], pos[1]
	rest := pos[2:]
	p, _, err := loadProject(*dir)
	if err != nil {
		return err
	}
	switch verb {
	case "list", "ls":
		l, _ := store.LoadLedger(p)
		t := l.FindTask(id)
		if t == nil {
			return fmt.Errorf("no task %s", store.NormID("T", id))
		}
		if len(t.Criteria) == 0 {
			fmt.Printf("%s has no acceptance criteria.\n", t.ID)
			return nil
		}
		fmt.Print(store.RenderCriteria(t.Criteria, ""))
		return nil
	case "add":
		text := strings.TrimSpace(strings.Join(rest, " "))
		if text == "" {
			return fmt.Errorf("%s", taskACUsage)
		}
		switch *prio {
		case store.PrioMust, store.PrioShould, store.PrioCould:
		default:
			return fmt.Errorf("--priority must be must, should or could")
		}
		var c store.Criterion
		err := store.UpdateLedger(p, func(l *store.Ledger) error {
			t := l.FindTask(id)
			if t == nil {
				return fmt.Errorf("no task %s", store.NormID("T", id))
			}
			if t.Status == store.TaskDone {
				return fmt.Errorf("%s is done; criteria are fixed once a task is finished", t.ID)
			}
			c = *t.AddCriterion(store.ParseCriterion(*prio, text))
			return nil
		})
		if err != nil {
			return err
		}
		fmt.Printf("Added %s (%s): %s\n", c.ID, c.Priority, c.Text)
		return nil
	case "pass", "fail", "reset":
		if len(rest) != 1 {
			return fmt.Errorf("%s", taskACUsage)
		}
		if verb != "reset" && strings.TrimSpace(*evidence) == "" {
			return fmt.Errorf("--evidence is required: name the test, the command and its result, or what you observed")
		}
		var c store.Criterion
		err := store.UpdateLedger(p, func(l *store.Ledger) error {
			t := l.FindTask(id)
			if t == nil {
				return fmt.Errorf("no task %s", store.NormID("T", id))
			}
			x := t.FindCriterion(rest[0])
			if x == nil {
				return fmt.Errorf("%s has no criterion %s", t.ID, store.NormID("AC", rest[0]))
			}
			switch verb {
			case "pass":
				x.Status = store.CritPass
			case "fail":
				x.Status = store.CritFail
			default:
				x.Status = store.CritOpen
			}
			x.Evidence = strings.TrimSpace(*evidence)
			c = *x
			return nil
		})
		if err != nil {
			return err
		}
		fmt.Printf("%s is now %s\n", c.ID, c.Status)
		return nil
	case "amend", "remove", "rm":
		if !planningAgentMayEdit(p, id) {
			if err := humanOnly("change acceptance criteria (they are fixed during implementation; file a blocker to ask)"); err != nil {
				return err
			}
		}
		if len(rest) < 1 || (verb == "amend" && (len(rest) < 2 || strings.TrimSpace(*reason) == "")) {
			return fmt.Errorf("%s", taskACUsage)
		}
		err := store.UpdateLedger(p, func(l *store.Ledger) error {
			t := l.FindTask(id)
			if t == nil {
				return fmt.Errorf("no task %s", store.NormID("T", id))
			}
			x := t.FindCriterion(rest[0])
			if x == nil {
				return fmt.Errorf("%s has no criterion %s", t.ID, store.NormID("AC", rest[0]))
			}
			if verb == "amend" {
				nc := store.ParseCriterion(x.Priority, strings.Join(rest[1:], " "))
				note := fmt.Sprintf("%s: was %q; %s", time.Now().Format("2006-01-02"), x.Text, strings.TrimSpace(*reason))
				x.Amended = strings.TrimSpace(strings.TrimSpace(x.Amended + "\n" + note))
				x.Text = nc.Text
				if nc.Verify != "" {
					x.Verify = nc.Verify
				}
				x.Status, x.Evidence = store.CritOpen, ""
				return nil
			}
			var keep []store.Criterion
			for _, c := range t.Criteria {
				if c.ID != x.ID {
					keep = append(keep, c)
				}
			}
			t.Criteria = keep
			return nil
		})
		if err != nil {
			return err
		}
		fmt.Println("Saved.")
		return nil
	}
	return fmt.Errorf("%s", taskACUsage)
}

// planningAgentMayEdit: criteria are fixed against the agents that implement
// or judge the work. A planning agent (spec writer, planner, interviewer,
// adopter) may still revise criteria of a task nobody has started.
func planningAgentMayEdit(p store.Project, id string) bool {
	switch os.Getenv("HLOOP_ROLE") {
	case "spec-writer", "planner", "interviewer", "adopter":
	default:
		return false
	}
	if asAgent() == "" {
		return false
	}
	l, _ := store.LoadLedger(p)
	t := l.FindTask(id)
	return t != nil && t.StartCommit == "" && (t.Status == store.TaskPending || t.Status == store.TaskProposed)
}

// ---------- task red: record the tests-only commit ----------

func cmdTaskRed(args []string) error {
	fs := newFS("task red")
	dir := fs.String("C", "", "project directory or name")
	commit := fs.String("commit", "", "the commit that adds the failing tests (default: HEAD)")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	p, cfg, err := loadProject(*dir)
	if err != nil {
		return err
	}
	c := strings.TrimSpace(*commit)
	if c == "" {
		c = engine.Head(p.Root)
	}
	if c == "" {
		return fmt.Errorf("no commit given and HEAD is unknown (is this a git repository?)")
	}
	var t store.Task
	err = store.UpdateLedger(p, func(l *store.Ledger) error {
		x := l.Current()
		if len(pos) > 0 {
			x = l.FindTask(pos[0])
		}
		if x == nil {
			return fmt.Errorf("no task given and none is in progress")
		}
		x.RedCommit = c
		t = *x
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Printf("Recorded %s as the tests-only (red) commit of %s.\n", c, t.ID)
	if !cfg.TDDOn() {
		fmt.Println("(TDD is off for this project, so hloop records it but does not check it.)")
	}
	return nil
}

// ---------- task show / report ----------

func cmdTaskShow(args []string) error {
	fs := newFS("task show")
	dir := fs.String("C", "", "project directory or name")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	p, _, err := loadProject(*dir)
	if err != nil {
		return err
	}
	l, _ := store.LoadLedger(p)
	var t *store.Task
	if len(pos) > 0 {
		t = l.FindTask(pos[0])
	} else {
		t = l.Current()
	}
	if t == nil {
		return fmt.Errorf("usage: hloop task show <ID>")
	}
	fmt.Printf("%s %s\nStatus: %s   Kind: %s   Source: %s\n", t.ID, t.Title, t.Status, t.EffKind(), orString(t.Source, "-"))
	if t.Detail != "" {
		fmt.Printf("Details: %s\n", t.Detail)
	}
	if t.Spec != "" {
		fmt.Printf("Spec: .loop/%s\n", t.Spec)
	}
	fmt.Print(store.RenderCriteria(t.Criteria, ""))
	if t.StartCommit != "" || t.RedCommit != "" || t.Commit != "" {
		fmt.Printf("Commits: start %s, red %s, done %s\n", orString(t.StartCommit, "-"), orString(t.RedCommit, "-"), orString(t.Commit, "-"))
	}
	if t.GateFails > 0 {
		fmt.Printf("Finishes rejected by hloop: %d\n", t.GateFails)
	}
	for _, c := range t.Checks {
		res := "pass"
		if !c.OK {
			res = "FAIL"
		}
		fmt.Printf("Check %-22s %s  %s\n", c.Name, res, firstLineOf(c.Detail))
	}
	for _, g := range t.GuardEvents {
		fmt.Printf("Guardrail %s: %s\n", g.Name, g.Detail)
	}
	if t.Report != "" {
		fmt.Printf("Report: %s\n", p.Path(t.Report))
	}
	return nil
}

func cmdTaskReport(args []string) error {
	fs := newFS("task report")
	dir := fs.String("C", "", "project directory or name")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("usage: hloop task report <ID>")
	}
	p, cfg, err := loadProject(*dir)
	if err != nil {
		return err
	}
	rel, err := engine.WriteReport(p, cfg, pos[0])
	if err != nil {
		return err
	}
	b, _ := os.ReadFile(p.Path(rel))
	fmt.Print(string(b))
	return nil
}

// ---------- ideas ----------

func cmdIdea(args []string) error {
	if len(args) == 0 {
		args = []string{"list"}
	}
	sub, rest := args[0], args[1:]
	fs := newFS("idea " + sub)
	dir := fs.String("C", "", "project directory or name")
	all := fs.Bool("all", false, "list: include promoted and dropped ideas")
	pos, err := parseArgs(fs, rest)
	if err != nil {
		return err
	}
	p, _, err := loadProject(*dir)
	if err != nil {
		return err
	}
	switch sub {
	case "add":
		text := strings.TrimSpace(strings.Join(pos, " "))
		if text == "" {
			return fmt.Errorf("usage: hloop idea add \"something you might want later\"")
		}
		var x store.Idea
		if err := store.UpdateLedger(p, func(l *store.Ledger) error { x = *l.AddIdea(text, source()); return nil }); err != nil {
			return err
		}
		fmt.Printf("Parked %s: %s\nIdeas never run. Promote one to a task with `hloop idea promote %s`.\n", x.ID, x.Text, x.ID)
		return nil
	case "list", "ls":
		l, _ := store.LoadLedger(p)
		n := 0
		for _, x := range l.Ideas {
			if !*all && x.Status != store.IdeaOpen {
				continue
			}
			extra := ""
			if x.Task != "" {
				extra = " -> " + x.Task
			}
			fmt.Printf("%-4s [%s] %s%s\n", x.ID, x.Status, x.Text, extra)
			n++
		}
		if n == 0 {
			fmt.Println("No ideas parked.")
		}
		return nil
	case "promote", "drop":
		if err := humanOnly(sub + " ideas"); err != nil {
			return err
		}
		if len(pos) != 1 {
			return fmt.Errorf("usage: hloop idea %s <ID>", sub)
		}
		var msg string
		err := store.UpdateLedger(p, func(l *store.Ledger) error {
			if sub == "drop" {
				x := l.FindIdea(pos[0])
				if x == nil {
					return fmt.Errorf("no idea %s", store.NormID("I", pos[0]))
				}
				x.Status = store.IdeaDropped
				msg = "Dropped " + x.ID + "."
				return nil
			}
			x, t, err := l.PromoteIdea(pos[0], store.TaskPending)
			if err != nil {
				return err
			}
			msg = fmt.Sprintf("Promoted %s to task %s (pending). Add acceptance criteria with `hloop task ac %s add \"...\"`, or have an agent write a spec: `hloop spec --task %s`.", x.ID, t.ID, t.ID, t.ID)
			return nil
		})
		if err != nil {
			return err
		}
		fmt.Println(msg)
		return nil
	}
	return fmt.Errorf("usage: hloop idea add|list|promote|drop")
}

// ---------- lessons ----------

func cmdLesson(args []string) error {
	if len(args) == 0 {
		args = []string{"list"}
	}
	sub, rest := args[0], args[1:]
	fs := newFS("lesson " + sub)
	dir := fs.String("C", "", "project directory or name")
	pos, err := parseArgs(fs, rest)
	if err != nil {
		return err
	}
	p, _, err := loadProject(*dir)
	if err != nil {
		return err
	}
	switch sub {
	case "add":
		text := strings.TrimSpace(strings.Join(pos, " "))
		if text == "" {
			return fmt.Errorf("usage: hloop lesson add \"a short, durable fact (e.g. start the DB with docker compose up -d before running tests)\"")
		}
		if len(text) > 400 {
			return fmt.Errorf("keep a lesson under 400 characters: one fact, not a document")
		}
		st := store.LessonActive
		if asAgent() != "" {
			st = store.LessonProposed
		}
		var x store.Lesson
		if err := store.UpdateLedger(p, func(l *store.Ledger) error { x = *l.AddLesson(text, source(), st); return nil }); err != nil {
			return err
		}
		if st == store.LessonProposed {
			fmt.Printf("Proposed %s. A human must approve it before agents are told about it.\n", x.ID)
		} else {
			fmt.Printf("Added %s. Every agent turn is told about it from now on.\n", x.ID)
		}
		return nil
	case "list", "ls":
		l, _ := store.LoadLedger(p)
		if len(l.Lessons) == 0 {
			fmt.Println("No lessons yet.")
			return nil
		}
		for _, x := range l.Lessons {
			fmt.Printf("%-4s [%s] %s\n", x.ID, x.Status, x.Text)
		}
		return nil
	case "approve", "retire":
		if err := humanOnly(sub + " lessons"); err != nil {
			return err
		}
		if len(pos) != 1 {
			return fmt.Errorf("usage: hloop lesson %s <ID>", sub)
		}
		var x store.Lesson
		err := store.UpdateLedger(p, func(l *store.Ledger) error {
			y := l.FindLesson(pos[0])
			if y == nil {
				return fmt.Errorf("no lesson %s", store.NormID("L", pos[0]))
			}
			if sub == "approve" {
				y.Status = store.LessonActive
			} else {
				y.Status = store.LessonRetired
			}
			x = *y
			return nil
		})
		if err != nil {
			return err
		}
		fmt.Printf("%s is now %s.\n", x.ID, x.Status)
		return nil
	}
	return fmt.Errorf("usage: hloop lesson add|list|approve|retire")
}

// ---------- gates ----------

const gateUsage = `usage:
  hloop gate add <name> [--where worktree|export] [--timeout MIN] [--kinds code,config] [--replace] -- <command> [args...]
  hloop gate list
  hloop gate remove <name>
  hloop gate run [name]          run the gates now and show the result

A gate is a command hloop runs itself after an agent marks a task done; if it
fails, the task is reopened and the agent is told why. The command runs
without a shell. For pipes or "&&", name the shell yourself:
  hloop gate add test -- go test ./...
  hloop gate add test --where export -- sh -c "npm ci && npm test"`

func cmdGate(args []string) error {
	if len(args) == 0 {
		args = []string{"list"}
	}
	sub, rest := args[0], args[1:]
	var command []string
	for i, a := range rest {
		if a == "--" {
			command = append([]string{}, rest[i+1:]...)
			rest = rest[:i]
			break
		}
	}
	fs := newFS("gate " + sub)
	dir := fs.String("C", "", "project directory or name")
	where := fs.String("where", "worktree", "worktree or export (a clean copy of the last commit)")
	timeout := fs.Int("timeout", 0, "minutes (default 20)")
	kinds := fs.String("kinds", "", "task kinds it applies to, comma-separated (default code,config; 'all' for every kind)")
	replace := fs.Bool("replace", false, "replace a gate with the same name")
	pos, err := parseArgs(fs, rest)
	if err != nil {
		return err
	}
	p, cfg, err := loadProject(*dir)
	if err != nil {
		return err
	}
	switch sub {
	case "list", "ls":
		if len(cfg.Gates) == 0 {
			fmt.Println("No gates. hloop accepts a finished task on the agent's word. Add one:\n  hloop gate add test -- <your test command>")
			return nil
		}
		for _, g := range cfg.Gates {
			k := "code,config"
			if len(g.Kinds) > 0 {
				k = strings.Join(g.Kinds, ",")
			}
			fmt.Printf("%-12s %-8s kinds=%-12s %s\n", g.Name, orString(g.Where, "worktree"), k, strings.Join(g.Run, " "))
		}
		fmt.Printf("TDD (red/green proof): %s\n", cfg.TDD)
		return nil
	case "add":
		if err := humanOnly("change gates"); err != nil {
			return err
		}
		if len(pos) != 1 || len(command) == 0 {
			return fmt.Errorf("%s", gateUsage)
		}
		g := store.Gate{Name: pos[0], Run: command, Where: *where, TimeoutMinutes: *timeout}
		if g.Where == "worktree" {
			g.Where = ""
		}
		for _, k := range strings.Split(*kinds, ",") {
			if k = strings.TrimSpace(k); k != "" {
				g.Kinds = append(g.Kinds, k)
			}
		}
		next := cfg
		next.Gates = nil
		found := false
		for _, x := range cfg.Gates {
			if x.Name == g.Name {
				if !*replace {
					return fmt.Errorf("gate %q exists; use --replace", g.Name)
				}
				next.Gates = append(next.Gates, g)
				found = true
				continue
			}
			next.Gates = append(next.Gates, x)
		}
		if !found {
			next.Gates = append(next.Gates, g)
		}
		if probs := next.Validate(); len(probs) > 0 {
			return fmt.Errorf("%s", strings.Join(probs, "; "))
		}
		keep := p.PreserveTrust()
		if err := p.SaveConfig(next); err != nil {
			return err
		}
		keep()
		fmt.Printf("Gate %q saved: %s\nTry it now with `hloop gate run %s`.\n", g.Name, strings.Join(g.Run, " "), g.Name)
		return nil
	case "remove", "rm":
		if err := humanOnly("change gates"); err != nil {
			return err
		}
		if len(pos) != 1 {
			return fmt.Errorf("usage: hloop gate remove <name>")
		}
		next := cfg
		next.Gates = nil
		for _, x := range cfg.Gates {
			if x.Name != pos[0] {
				next.Gates = append(next.Gates, x)
			}
		}
		if len(next.Gates) == len(cfg.Gates) {
			return fmt.Errorf("no gate %q", pos[0])
		}
		if next.TDDOn() && len(next.Gates) == 0 {
			next.TDD = "off"
			fmt.Println("That was the last gate, so TDD checking is now off.")
		}
		keep := p.PreserveTrust()
		if err := p.SaveConfig(next); err != nil {
			return err
		}
		keep()
		fmt.Println("Removed.")
		return nil
	case "run":
		if err := humanOnly("run gates"); err != nil {
			return err
		}
		if err := p.RequireTrust(); err != nil {
			return err
		}
		bad := 0
		ran := 0
		for _, g := range cfg.Gates {
			if len(pos) > 0 && g.Name != pos[0] {
				continue
			}
			ran++
			fmt.Printf("Running gate %s: %s ...\n", g.Name, strings.Join(g.Run, " "))
			var r engine.GateRun
			if g.Where == "export" {
				d, cleanup, err := engine.ExportCommit(p.Root, "HEAD")
				if err != nil {
					return err
				}
				r = engine.RunGate(contextBackground(), g, d, "export@HEAD", cfg.PathExtra)
				cleanup()
			} else {
				r = engine.RunGate(contextBackground(), g, p.Root, "worktree", cfg.PathExtra)
			}
			fmt.Println("  " + r.Summary())
			if !r.OK {
				bad++
				fmt.Println(indent(lastN(r.Tail, 20), "    "))
			}
		}
		if ran == 0 {
			return fmt.Errorf("no gate to run (see `hloop gate list`)")
		}
		if bad > 0 {
			return errSilent
		}
		return nil
	}
	return fmt.Errorf("%s", gateUsage)
}

func indent(s, pre string) string {
	return pre + strings.ReplaceAll(s, "\n", "\n"+pre)
}
