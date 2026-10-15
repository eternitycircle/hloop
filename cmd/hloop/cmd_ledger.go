package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"hloop/internal/engine"
	"hloop/internal/notify"
	"hloop/internal/store"
)

func nowFn() time.Time { return time.Now() }

func loadProject(dir string) (store.Project, store.Config, error) {
	p, err := resolveProject(dir)
	if err != nil {
		return p, store.Config{}, err
	}
	cfg, err := p.LoadConfig()
	return p, cfg, err
}

// ---------- tasks ----------

func cmdTask(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: hloop task add|list|show|done|ac|red|report|start|skip|reopen|top|approve ...")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "ac":
		return cmdTaskAC(rest)
	case "red":
		return cmdTaskRed(rest)
	case "show":
		return cmdTaskShow(rest)
	case "report":
		return cmdTaskReport(rest)
	}
	fs := newFS("task " + sub)
	dir := fs.String("C", "", "project directory or name")
	detail := fs.String("detail", "", "objective / acceptance criteria")
	top := fs.Bool("top", false, "put the task ahead of other waiting tasks")
	status := fs.String("status", "", "pending (default), done or proposed")
	commit := fs.String("commit", "", "commit hash")
	summary := fs.String("summary", "", "what changed")
	all := fs.Bool("all", false, "include done and skipped tasks")
	kind := fs.String("kind", "", "code (default), docs, config or research")
	spec := fs.String("spec", "", "spec document inside .loop/ (e.g. specs/csv-export/spec.md)")
	force := fs.Bool("force", false, "done: accept even with unmet Must criteria (humans only)")
	var must, should, could strList
	fs.Var(&must, "ac", "a Must acceptance criterion: \"statement :: how it is verified\" (repeatable)")
	fs.Var(&should, "should", "a Should criterion (does not block completion)")
	fs.Var(&could, "could", "a Could criterion (nice to have)")
	pos, err := parseArgs(fs, rest)
	if err != nil {
		return err
	}
	p, _, err := loadProject(*dir)
	if err != nil {
		return err
	}
	if !store.ValidKind(*kind) {
		return fmt.Errorf("--kind must be code, docs, config or research")
	}
	if *spec != "" && !store.ValidPromptPath(*spec) {
		return fmt.Errorf("--spec must be a path inside .loop/ (for example specs/feature.md)")
	}

	switch sub {
	case "add":
		title := strings.TrimSpace(strings.Join(pos, " "))
		if title == "" {
			return fmt.Errorf("usage: hloop task add \"title\" [--detail ...] [--top]")
		}
		st := *status
		switch st {
		case "", store.TaskPending, store.TaskDone, store.TaskProposed:
		default:
			return fmt.Errorf("--status must be pending, done or proposed")
		}
		var t store.Task
		err := store.UpdateLedger(p, func(l *store.Ledger) error {
			if st == store.TaskDone {
				c := *commit
				if c == "" {
					c = ""
				}
				t = *l.AddDoneTask(title, c, *summary, sourceOrAdopt())
				return nil
			}
			x := l.AddTask(title, *detail, source(), st, *top)
			x.Kind, x.Spec = *kind, filepathSlash(*spec)
			for _, c := range must {
				x.AddCriterion(store.ParseCriterion(store.PrioMust, c))
			}
			for _, c := range should {
				x.AddCriterion(store.ParseCriterion(store.PrioShould, c))
			}
			for _, c := range could {
				x.AddCriterion(store.ParseCriterion(store.PrioCould, c))
			}
			t = *x
			return nil
		})
		if err != nil {
			return err
		}
		fmt.Printf("Added %s: %s", t.ID, t.Title)
		if n := len(t.Criteria); n > 0 {
			fmt.Printf(" (%d acceptance criteria)", n)
		}
		fmt.Println()
		return nil
	case "list", "ls":
		l, err := store.LoadLedger(p)
		if err != nil {
			return err
		}
		n := 0
		for _, t := range l.Tasks {
			if !*all && (t.Status == store.TaskDone || t.Status == store.TaskSkipped) {
				continue
			}
			mark := map[string]string{store.TaskPending: "[ ]", store.TaskInProgress: "[~]", store.TaskDone: "[x]", store.TaskSkipped: "[-]", store.TaskProposed: "[?]"}[t.Status]
			extra := ""
			if n := len(t.Criteria); n > 0 {
				extra = fmt.Sprintf("  (%d AC)", n)
			}
			if t.Kind != "" && t.Kind != store.KindCode {
				extra += "  [" + t.Kind + "]"
			}
			fmt.Printf("%s %-5s %s%s\n", mark, t.ID, t.Title, extra)
			n++
		}
		if n == 0 {
			fmt.Println("No open tasks.")
		}
		return nil
	case "done":
		id := ""
		if len(pos) > 0 {
			id = pos[0]
		}
		c := *commit
		if c == "" && engine.IsGitRepo(p.Root) {
			c = engine.Head(p.Root)
		}
		var t store.Task
		var unmet []store.Criterion
		if err := store.UpdateLedger(p, func(l *store.Ledger) error {
			x := l.Current()
			if id != "" {
				x = l.FindTask(id)
			}
			if x != nil {
				unmet = x.UnmetMust()
				if len(unmet) > 0 && (asAgent() != "" || !*force) {
					return errUnmet{id: x.ID, unmet: unmet, agent: asAgent() != ""}
				}
			}
			x, err := l.Complete(id, c, *summary)
			if err == nil {
				t = *x
			}
			return err
		}); err != nil {
			return err
		}
		fmt.Printf("Completed %s: %s\n", t.ID, t.Title)
		if asAgent() != "" {
			fmt.Println("hloop will now verify it (criteria and gates). If a check fails the task is reopened and you will be told why.")
		} else if len(unmet) > 0 {
			fmt.Printf("Accepted with --force; %d Must criteria were not marked pass.\n", len(unmet))
		}
		return nil
	case "start", "skip", "reopen", "approve", "top":
		if len(pos) != 1 {
			return fmt.Errorf("usage: hloop task %s <ID>", sub)
		}
		if sub == "approve" {
			if err := humanOnly("approve proposed tasks"); err != nil {
				return err
			}
		}
		var t store.Task
		err := store.UpdateLedger(p, func(l *store.Ledger) error {
			x := l.FindTask(pos[0])
			if x == nil {
				return fmt.Errorf("no task %s", store.NormID("T", pos[0]))
			}
			switch sub {
			case "start":
				if c := l.Current(); c != nil && c.ID != x.ID {
					return fmt.Errorf("%s is already in progress", c.ID)
				}
				x.Status = store.TaskInProgress
			case "skip":
				x.Status = store.TaskSkipped
			case "reopen":
				x.Status, x.Done = store.TaskPending, nil
			case "approve":
				if x.Status != store.TaskProposed {
					return fmt.Errorf("%s is %s, not proposed", x.ID, x.Status)
				}
				x.Status = store.TaskPending
			case "top":
				if x.Status != store.TaskPending {
					return fmt.Errorf("%s is %s; only pending tasks can be moved", x.ID, x.Status)
				}
				l.MoveToFront(x.ID)
			}
			t = *l.FindTask(pos[0])
			return nil
		})
		if err != nil {
			return err
		}
		fmt.Printf("%s is now %s\n", t.ID, t.Status)
		return nil
	}
	return fmt.Errorf("unknown task command %q", sub)
}

type errUnmet struct {
	id    string
	unmet []store.Criterion
	agent bool
}

func (e errUnmet) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s has Must acceptance criteria that are not marked pass:\n", e.id)
	for _, c := range e.unmet {
		fmt.Fprintf(&b, "  %s [%s] %s\n", c.ID, c.Status, c.Text)
	}
	fmt.Fprintf(&b, "Prove each one and mark it: hloop task ac %s pass <AC id> --evidence \"...\"", e.id)
	if !e.agent {
		b.WriteString("\n(or accept anyway: hloop task done " + e.id + " --force)")
	}
	return b.String()
}

func filepathSlash(s string) string { return strings.ReplaceAll(strings.TrimSpace(s), "\\", "/") }

func sourceOrAdopt() string {
	if a := asAgent(); a == "adopter" {
		return "adopt"
	}
	return source()
}

// ---------- blockers ----------

func cmdBlocker(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: hloop blocker add|list|resolve ...")
	}
	sub, rest := args[0], args[1:]
	fs := newFS("blocker " + sub)
	dir := fs.String("C", "", "project directory or name")
	answer := fs.String("answer", "", "your answer or what you did")
	noRun := fs.Bool("no-run", false, "do not start the loop after resolving")
	pos, err := parseArgs(fs, rest)
	if err != nil {
		return err
	}
	p, cfg, err := loadProject(*dir)
	if err != nil {
		return err
	}
	switch sub {
	case "add":
		text := strings.TrimSpace(strings.Join(pos, " "))
		if text == "" {
			return fmt.Errorf("usage: hloop blocker add \"what the human must do\"")
		}
		var b store.Blocker
		if err := store.UpdateLedger(p, func(l *store.Ledger) error {
			b = *l.AddBlocker(text, source())
			return nil
		}); err != nil {
			return err
		}
		fmt.Printf("Blocker %s recorded. The loop will pause and notify the human.\n", b.ID)
		return nil
	case "list", "ls":
		l, _ := store.LoadLedger(p)
		open := l.OpenBlockers()
		if len(open) == 0 {
			fmt.Println("No open blockers.")
			return nil
		}
		for _, b := range open {
			fmt.Printf("%s (opened %s)\n  %s\n", b.ID, b.Opened.Local().Format("Jan 2 15:04"), b.Text)
		}
		return nil
	case "resolve":
		if err := humanOnly("resolve blockers"); err != nil {
			return err
		}
		id := "all"
		if len(pos) > 0 {
			id = pos[0]
		}
		ans := *answer
		if ans == "" {
			ans = "resolved by the human"
		}
		var hit []*store.Blocker
		if err := store.UpdateLedger(p, func(l *store.Ledger) error {
			h, err := l.ResolveBlocker(id, ans)
			hit = h
			return err
		}); err != nil {
			return err
		}
		fmt.Printf("Resolved %d blocker(s).\n", len(hit))
		l, _ := store.LoadLedger(p)
		if len(l.OpenBlockers()) == 0 && cfg.AutoResume && !*noRun && !p.RunActive() && !p.LoadState().Paused {
			if err := notify.SpawnDetached(exePath(), "run", "-C", p.Root, "--force"); err == nil {
				fmt.Println("Resuming the loop in the background (see `hloop logs -f`).")
			}
		} else if len(l.OpenBlockers()) == 0 {
			fmt.Println("Run `hloop run` to continue.")
		}
		return nil
	}
	return fmt.Errorf("unknown blocker command %q", sub)
}

// ---------- decisions ----------

func cmdDecision(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: hloop decision add|list|resolve|revisit ...")
	}
	sub, rest := args[0], args[1:]
	fs := newFS("decision " + sub)
	dir := fs.String("C", "", "project directory or name")
	options := fs.String("options", "", "options under consideration")
	needs := fs.String("needs", "", "information needed to decide")
	decision := fs.String("decision", "", "the decision")
	rationale := fs.String("rationale", "", "why")
	all := fs.Bool("all", false, "include decided items")
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
		topic := strings.TrimSpace(strings.Join(pos, " "))
		if topic == "" {
			return fmt.Errorf("usage: hloop decision add \"topic\" [--options \"a | b\"] [--needs \"...\"]")
		}
		var d store.Decision
		if err := store.UpdateLedger(p, func(l *store.Ledger) error {
			d = *l.AddDecision(topic, *options, *needs)
			return nil
		}); err != nil {
			return err
		}
		fmt.Printf("Recorded %s as Open: %s\n", d.ID, d.Topic)
		return nil
	case "list", "ls":
		l, _ := store.LoadLedger(p)
		n := 0
		for _, d := range l.Decisions {
			if !*all && d.Status == store.DecisionDecided {
				continue
			}
			fmt.Printf("%s [%s] %s\n", d.ID, d.Status, d.Topic)
			if d.Options != "" {
				fmt.Printf("    options: %s\n", d.Options)
			}
			if d.Needs != "" {
				fmt.Printf("    needs: %s\n", d.Needs)
			}
			n++
		}
		if n == 0 {
			fmt.Println("No open decisions.")
		}
		return nil
	case "resolve", "revisit":
		if err := humanOnly(sub + " decisions"); err != nil {
			return err
		}
		if len(pos) < 1 {
			return fmt.Errorf("usage: hloop decision %s <ID> --decision \"...\" [--rationale \"...\"]", sub)
		}
		var d store.Decision
		err := store.UpdateLedger(p, func(l *store.Ledger) error {
			if sub == "revisit" {
				x := l.FindDecision(pos[0])
				if x == nil {
					return fmt.Errorf("no decision %s", store.NormID("D", pos[0]))
				}
				x.Status = store.DecisionRevisit
				d = *x
				return nil
			}
			if strings.TrimSpace(*decision) == "" {
				return fmt.Errorf("--decision is required")
			}
			x, err := l.ResolveDecision(pos[0], *decision, *rationale)
			if err == nil {
				d = *x
			}
			return err
		})
		if err != nil {
			return err
		}
		fmt.Printf("%s is now %s\n", d.ID, d.Status)
		return nil
	}
	return fmt.Errorf("unknown decision command %q", sub)
}

// ---------- notes ----------

func cmdNote(args []string) error {
	fs := newFS("note")
	dir := fs.String("C", "", "project directory or name")
	held := fs.Bool("held", false, "list notes from your phone waiting for approval")
	approve := fs.String("approve", "", "approve a held note (e.g. N2)")
	discard := fs.String("discard", "", "discard a held note")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if *held || *approve != "" || *discard != "" {
		if err := humanOnly("review held notes"); err != nil {
			return err
		}
		p, _, err := loadProject(*dir)
		if err != nil {
			return err
		}
		switch {
		case *approve != "":
			it, err := p.ResolveHeld(*approve, true)
			if err != nil {
				return err
			}
			fmt.Printf("Approved %s: the agent will see it at the start of its next turn.\n", it.ID)
		case *discard != "":
			it, err := p.ResolveHeld(*discard, false)
			if err != nil {
				return err
			}
			fmt.Printf("Discarded %s.\n", it.ID)
		default:
			items := p.HeldInbox()
			if len(items) == 0 {
				fmt.Println("No held notes.")
			}
			for _, it := range items {
				fmt.Printf("%s (from %s): %s\n", it.ID, it.From, it.Text)
			}
		}
		return nil
	}
	text := strings.TrimSpace(strings.Join(pos, " "))
	if text == "" {
		return fmt.Errorf("usage: hloop note \"text for the agent's next turn\"")
	}
	p, _, err := loadProject(*dir)
	if err != nil {
		return err
	}
	if err := p.AddInbox("note", text, source()); err != nil {
		return err
	}
	fmt.Println("Noted. The agent sees it at the start of its next turn; it is task input, never a change to its rules.")
	return nil
}

// ---------- reviews ----------

func cmdReview(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: hloop review run|submit|list ...")
	}
	sub, rest := args[0], args[1:]
	fs := newFS("review " + sub)
	dir := fs.String("C", "", "project directory or name")
	verdict := fs.String("verdict", "", "approve or changes_requested")
	summary := fs.String("summary", "", "short summary a human can read on a phone")
	var tasks strList
	fs.Var(&tasks, "task", "a concrete fix the builder must make (repeatable)")
	agentName := fs.String("agent", "", "which reviewer agent to run (default: all reviewers)")
	if _, err := parseArgs(fs, rest); err != nil {
		return err
	}
	p, cfg, err := loadProject(*dir)
	if err != nil {
		return err
	}
	name := projectName(p, cfg)

	switch sub {
	case "run":
		var names []string
		for _, n := range cfg.Pipeline {
			if cfg.Agents[n].Role == "reviewer" && (*agentName == "" || *agentName == n) {
				names = append(names, n)
			}
		}
		if len(names) == 0 {
			return fmt.Errorf("no reviewer agent configured. Add one: hloop agent add reviewer --role reviewer --adapter <harness>")
		}
		lf := openLog(p)
		var out = os.Stdout
		_ = lf
		e, err := newEngine(p, out)
		if err != nil {
			return err
		}
		for _, n := range names {
			fmt.Printf("Running reviewer %q...\n", n)
			res := e.RunAgent(contextBackground(), n, "")
			if res.Class != "ok" {
				return fmt.Errorf("reviewer %q did not finish (%s)", n, res.Class)
			}
		}
		l, _ := store.LoadLedger(p)
		if len(l.Reviews) > 0 {
			r := l.Reviews[len(l.Reviews)-1]
			fmt.Printf("Latest review %s: %s\n  %s\n", r.ID, r.Verdict, r.Summary)
		}
		return nil
	case "submit":
		if *verdict != "approve" && *verdict != "changes_requested" {
			return fmt.Errorf("--verdict must be approve or changes_requested")
		}
		if strings.TrimSpace(*summary) == "" {
			return fmt.Errorf("--summary is required")
		}
		maxC := 2
		if v, err := strconv.Atoi(os.Getenv("HLOOP_MAX_REVIEW_CYCLES")); err == nil && v > 0 {
			maxC = v
		}
		who := asAgent()
		if who == "" {
			who = "human"
		}
		var rv store.Review
		escalated := false
		if err := store.UpdateLedger(p, func(l *store.Ledger) error {
			rv = *l.AddReview(who, *verdict, *summary, tasks)
			if *verdict == "changes_requested" {
				for _, t := range tasks {
					l.AddTask(t, "Raised by review "+rv.ID+": "+*summary, "reviewer", store.TaskPending, true)
				}
				if l.ConsecutiveChangeRequests() > maxC {
					l.AddBlocker(fmt.Sprintf("The reviewer has asked for changes %d times in a row (limit %d). Read the reviews in PROGRESS.md and decide how to proceed (adjust the tasks, override the reviewer, or fix it yourself).", l.ConsecutiveChangeRequests(), maxC), "reviewer")
					escalated = true
				}
			}
			return nil
		}); err != nil {
			return err
		}
		fmt.Printf("Review %s recorded: %s\n", rv.ID, rv.Verdict)
		cfgEvents := map[string]bool{}
		for _, e := range cfg.Notify.Events {
			cfgEvents[e] = true
		}
		if cfgEvents["review"] || (escalated && cfgEvents["blocker"]) {
			title := fmt.Sprintf("%s: review %s", cfg.Name, *verdict)
			if escalated {
				title = fmt.Sprintf("%s: reviewer is stuck, needs you", cfg.Name)
			}
			notify.NewDispatcher(os.Stderr).Send(notify.Event{Kind: notify.KindReview, Title: title, Body: *summary, Project: name})
		}
		return nil
	case "list", "ls":
		l, _ := store.LoadLedger(p)
		if len(l.Reviews) == 0 {
			fmt.Println("No reviews yet.")
			return nil
		}
		start := len(l.Reviews) - 10
		if start < 0 {
			start = 0
		}
		for _, r := range l.Reviews[start:] {
			fmt.Printf("%s %s %s: %s\n  %s\n", r.ID, r.Time.Local().Format("Jan 2 15:04"), r.Agent, r.Verdict, r.Summary)
		}
		return nil
	}
	return fmt.Errorf("unknown review command %q", sub)
}
