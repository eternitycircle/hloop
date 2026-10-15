package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"hloop/internal/adapter"
	"hloop/internal/engine"
	"hloop/internal/scaffold"
	"hloop/internal/store"
)

func contextBackground() context.Context {
	ctx, _ := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	return ctx
}

// ---------- run ----------

func cmdRun(args []string) error {
	fs := newFS("run")
	dir := fs.String("C", "", "project directory or name")
	force := fs.Bool("force", false, "ignore a rate-limit backoff")
	maxTurns := fs.Int("max-turns", 0, "override the turn cap for this run")
	quiet := fs.Bool("quiet", false, "write to the log file only")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	p, _, err := loadProject(*dir)
	if err != nil {
		return err
	}
	var out io.Writer = os.Stdout
	if lf := openLog(p); lf != nil {
		defer lf.Close()
		if *quiet {
			out = lf
		} else {
			out = io.MultiWriter(os.Stdout, lf)
		}
	}
	e, err := newEngine(p, out)
	if err != nil {
		return err
	}
	e.Force, e.MaxTurns = *force, *maxTurns
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	outcome, err := e.Run(ctx)
	if err != nil {
		return err
	}
	if outcome == engine.OutcomeError {
		return fmt.Errorf("the run ended with an error; see `hloop logs`")
	}
	return nil
}

// ---------- status ----------

func cmdStatus(args []string) error {
	fs := newFS("status")
	dir := fs.String("C", "", "project directory or name")
	all := fs.Bool("all", false, "show every registered project")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	if *all {
		reg, err := store.LoadRegistry()
		if err != nil {
			return err
		}
		if len(reg) == 0 {
			fmt.Println("No projects registered.")
			return nil
		}
		for i, e := range reg {
			if i > 0 {
				fmt.Println()
			}
			p := store.Project{Root: e.Path}
			if !p.Exists() {
				fmt.Printf("%s: MISSING (%s)\n", e.Name, e.Path)
				continue
			}
			fmt.Println(store.StatusText(p, e.Name, time.Now()))
		}
		return nil
	}
	p, cfg, err := loadProject(*dir)
	if err != nil {
		return err
	}
	name := projectName(p, cfg)
	if *asJSON {
		l, _ := store.LoadLedger(p)
		s := p.LoadState()
		cur := ""
		if c := l.Current(); c != nil {
			cur = c.ID + " " + c.Title
		}
		var blockers []string
		for _, b := range l.OpenBlockers() {
			blockers = append(blockers, b.ID+": "+b.Text)
		}
		b, _ := json.MarshalIndent(map[string]any{
			"project": name, "status": store.EffectiveStatus(p, s, l, time.Now()), "current": cur,
			"done": l.Count(store.TaskDone), "pending": l.Count(store.TaskPending), "proposed": l.Count(store.TaskProposed),
			"open_blockers": blockers, "open_decisions": len(l.OpenDecisions()), "total_turns": s.TotalTurns,
			"last_outcome": s.LastOutcome,
		}, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	fmt.Println(store.StatusText(p, name, time.Now()))
	return nil
}

// ---------- pause / resume ----------

func cmdPause(args []string) error {
	fs := newFS("pause")
	dir := fs.String("C", "", "project directory or name")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	p, _, err := loadProject(*dir)
	if err != nil {
		return err
	}
	if err := p.UpdateState(func(s *store.State) { s.Paused = true }); err != nil {
		return err
	}
	fmt.Println("Paused. A run in progress stops after its current turn; scheduled runs are skipped until `hloop resume`.")
	return nil
}

func cmdResume(args []string) error {
	fs := newFS("resume")
	dir := fs.String("C", "", "project directory or name")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	p, _, err := loadProject(*dir)
	if err != nil {
		return err
	}
	if err := p.UpdateState(func(s *store.State) {
		s.Paused, s.RetryAfter, s.ConsecutiveFailures = false, nil, 0
		if s.Status == "error" || s.Status == "rate_limited" {
			s.Status = "idle"
		}
	}); err != nil {
		return err
	}
	fmt.Println("Resumed. Backoff and failure counters cleared. Run `hloop run` or wait for the next scheduled run.")
	return nil
}

// ---------- logs ----------

func cmdLogs(args []string) error {
	fs := newFS("logs")
	dir := fs.String("C", "", "project directory or name")
	n := fs.Int("n", 60, "number of lines")
	follow := fs.Bool("f", false, "follow the log")
	agentLog := fs.Bool("agent", false, "show the latest agent output instead of the loop log")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	p, _, err := loadProject(*dir)
	if err != nil {
		return err
	}
	path := p.Path("runs", "hloop.log")
	if *agentLog {
		ents, _ := os.ReadDir(p.Path("runs"))
		var names []string
		for _, e := range ents {
			if strings.HasSuffix(e.Name(), ".log") && e.Name() != "hloop.log" {
				names = append(names, e.Name())
			}
		}
		if len(names) == 0 {
			return fmt.Errorf("no agent logs yet")
		}
		sort.Strings(names)
		path = p.Path("runs", names[len(names)-1])
		fmt.Println("==>", path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("no log yet (%v)", err)
	}
	fmt.Println(lastN(string(b), *n))
	if !*follow {
		return nil
	}
	off := int64(len(b))
	ctx := contextBackground()
	for ctx.Err() == nil {
		time.Sleep(700 * time.Millisecond)
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		st, _ := f.Stat()
		if st.Size() < off {
			off = 0
		}
		_, _ = f.Seek(off, io.SeekStart)
		nb, _ := io.ReadAll(bufio.NewReader(f))
		f.Close()
		if len(nb) > 0 {
			fmt.Print(string(nb))
			off += int64(len(nb))
		}
	}
	return nil
}

// ---------- plan ----------

func plannerAgent(cfg store.Config) store.Agent {
	if a, ok := cfg.Agents["planner"]; ok {
		return a
	}
	ad := ""
	if len(cfg.Pipeline) > 0 {
		ad = cfg.Agents[cfg.Pipeline[0]].Adapter
	}
	return store.Agent{Adapter: ad, Role: "planner", Prompt: "prompts/planner.md", Mode: "loop-only", Trigger: "manual", Session: "fresh", TimeoutMinutes: 45}
}

func cmdPlan(args []string) error {
	fs := newFS("plan")
	dir := fs.String("C", "", "project directory or name")
	adapterName := fs.String("adapter", "", "use a different harness for this request")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	p, cfg, err := loadProject(*dir)
	if err != nil {
		return err
	}
	instr := strings.TrimSpace(strings.Join(pos, " "))
	if instr == "" && isTTY() {
		instr = prompt(bufio.NewReader(os.Stdin), "What should change in the plan or documents?", "")
	}
	if instr == "" {
		return fmt.Errorf("usage: hloop plan \"what you want changed or added\"")
	}
	ag := plannerAgent(cfg)
	if *adapterName != "" {
		ag.Adapter = *adapterName
	}
	cfg.Agents["planner"] = ag
	keepTrust := p.PreserveTrust()
	if err := writeIfMissing(p.Path("prompts", "planner.md"), scaffold.DefaultPrompt("planner")); err != nil {
		return err
	}
	keepTrust()
	e := engine.New(p, cfg)
	e.Exe, e.Log = exePath(), os.Stdout
	fmt.Println("Asking the planner agent (it may only change files in .loop/)...")
	res := e.RunAgent(contextBackground(), "planner", instr)
	if res.Class != "ok" {
		if res.Tail != "" {
			fmt.Println(lastN(res.Tail, 10))
		}
		return fmt.Errorf("the planner did not finish (%s)", res.Class)
	}
	fmt.Println(lastN(res.Tail, 15))
	l, _ := store.LoadLedger(p)
	fmt.Printf("\nQueue now: %d task(s). See `hloop task list`.\n", l.Remaining())
	return nil
}

// ---------- agents ----------

func cmdAgent(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: hloop agent list|catalog|install|add|remove|order|run|tier ...")
	}
	sub, rest := args[0], args[1:]
	fs := newFS("agent " + sub)
	dir := fs.String("C", "", "project directory or name")
	adName := fs.String("adapter", "", "harness adapter")
	role := fs.String("role", "custom", "reviewer | planner | tech-writer | custom")
	trigger := fs.String("trigger", "", "every_turn | after_milestone | every_n_turns | on_run_end | manual")
	everyN := fs.Int("every-n", 0, "for every_n_turns")
	mode := fs.String("mode", "", "write | docs | loop-only")
	promptFile := fs.String("prompt-file", "", "file with this agent's instructions (copied into .loop/prompts/)")
	session := fs.String("session", "", "fresh (default) | continue")
	timeout := fs.Int("timeout", 0, "minutes before this agent is stopped")
	maxCycles := fs.Int("max-cycles", 0, "reviewers: change-request rounds before asking you")
	tier := fs.String("tier", "", "fast | standard | critical: which model the harness uses for this agent")
	installAll := fs.Bool("all", false, "install: install every profile in the catalog")
	var extra strList
	fs.Var(&extra, "extra-arg", "extra argument passed to the harness (repeatable)")
	pos, err := parseArgs(fs, rest)
	if err != nil {
		return err
	}
	p, cfg, err := loadProject(*dir)
	if err != nil {
		if sub == "catalog" { // browsing the catalog needs no project
			return agentCatalog(store.Config{})
		}
		return err
	}

	switch sub {
	case "add", "remove", "rm", "order", "install", "tier":
		defer p.PreserveTrust()()
	}
	if !store.ValidTier(*tier) {
		return fmt.Errorf("--tier must be fast, standard or critical")
	}
	switch sub {
	case "list", "ls":
		for i, n := range cfg.Pipeline {
			a := cfg.Agents[n]
			fmt.Printf("%d. %-14s role=%-9s adapter=%-12s mode=%-9s trigger=%s", i+1, n, a.Role, a.Adapter, a.Mode, a.Trigger)
			if a.Trigger == "every_n_turns" {
				fmt.Printf("(%d)", a.EveryN)
			}
			if a.Tier != "" {
				fmt.Printf(" tier=%s", a.Tier)
			}
			fmt.Println()
		}
		return nil
	case "add":
		if len(pos) != 1 {
			return fmt.Errorf("usage: hloop agent add <name> --adapter <harness> --role reviewer|planner|custom [--trigger ...]")
		}
		name := store.SafeName(pos[0])
		if !store.ValidName(name) {
			return fmt.Errorf("%q is not a usable agent name (use lowercase letters, digits, - _ .)", pos[0])
		}
		if _, exists := cfg.Agents[name]; exists {
			return fmt.Errorf("agent %q already exists (remove it first)", name)
		}
		ad := *adName
		if ad == "" {
			ad = cfg.Agents[cfg.Pipeline[0]].Adapter
		}
		if _, err := adapter.Load(ad, p.Dir()); err != nil {
			return err
		}
		a := store.Agent{Adapter: ad, Role: *role, Session: *session, EveryN: *everyN, TimeoutMinutes: *timeout, MaxCycles: *maxCycles, ExtraArgs: extra, Tier: *tier}
		switch *role {
		case "reviewer":
			a.Mode, a.Trigger = "loop-only", "after_milestone"
		case "planner":
			a.Mode, a.Trigger = "loop-only", "manual"
		case "tech-writer":
			a.Mode, a.Trigger = "docs", "manual"
		case "builder":
			return fmt.Errorf("the builder is the first agent and is created by init; add reviewers, planners or custom agents")
		default:
			a.Role, a.Mode, a.Trigger = "custom", "write", "manual"
		}
		if *mode != "" {
			a.Mode = *mode
		}
		if *trigger != "" {
			a.Trigger = *trigger
		}
		a.Prompt = "prompts/" + name + ".md"
		content := scaffold.DefaultPrompt(a.Role)
		if *promptFile != "" {
			b, err := os.ReadFile(*promptFile)
			if err != nil {
				return err
			}
			content = string(b)
		}
		next := cfg
		next.Agents = copyAgents(cfg.Agents)
		next.Agents[name] = a
		next.Pipeline = append(append([]string{}, cfg.Pipeline...), name)
		if probs := next.Validate(); len(probs) > 0 {
			return fmt.Errorf("%s", strings.Join(probs, "; "))
		}
		if err := store.WriteFile(p.Path(filepath.FromSlash(a.Prompt)), []byte(content), 0o644); err != nil {
			return err
		}
		if err := p.SaveConfig(next); err != nil {
			return err
		}
		fmt.Printf("Added agent %q (%s via %s, %s, runs %s).\n", name, a.Role, a.Adapter, a.Mode, a.Trigger)
		fmt.Printf("Its instructions are in %s: edit them to taste.\n", p.Path(filepath.FromSlash(a.Prompt)))
		if a.Role == "custom" {
			fmt.Println("Custom agents default to trigger=manual; set --trigger every_n_turns --every-n 5 (or after_milestone) to run it automatically, or use `hloop agent run`.")
		}
		return nil
	case "catalog":
		return agentCatalog(cfg)
	case "tier":
		if len(pos) != 2 || (pos[1] != "default" && !store.ValidTier(pos[1])) {
			return fmt.Errorf("usage: hloop agent tier <name> fast|standard|critical|default")
		}
		a, ok := cfg.Agents[pos[0]]
		if !ok {
			return fmt.Errorf("no agent %q", pos[0])
		}
		a.Tier = pos[1]
		if a.Tier == "default" {
			a.Tier = ""
		}
		cfg.Agents[pos[0]] = a
		if err := p.SaveConfig(cfg); err != nil {
			return err
		}
		if ad, err := adapter.Load(a.Adapter, p.Dir()); err == nil && a.Tier != "" && len(ad.TierArgs(a.Tier)) == 0 {
			fmt.Printf("Saved, but adapter %s defines no %q tier, so the harness default model is used. Add one to the adapter (\"tiers\": {\"%s\": [\"--model\", \"...\"]}) in .loop/adapters/%s.json or your user adapters.\n", ad.Name, a.Tier, a.Tier, ad.Name)
			return nil
		}
		fmt.Printf("%s now uses tier %s.\n", pos[0], orString(a.Tier, "default"))
		return nil
	case "install":
		return agentInstall(p, cfg, pos, *adName, *installAll)
	case "remove", "rm":
		if len(pos) != 1 {
			return fmt.Errorf("usage: hloop agent remove <name>")
		}
		if len(cfg.Pipeline) > 0 && cfg.Pipeline[0] == pos[0] {
			return fmt.Errorf("%q is the primary agent and cannot be removed", pos[0])
		}
		if _, ok := cfg.Agents[pos[0]]; !ok {
			return fmt.Errorf("no agent %q", pos[0])
		}
		delete(cfg.Agents, pos[0])
		var np []string
		for _, n := range cfg.Pipeline {
			if n != pos[0] {
				np = append(np, n)
			}
		}
		cfg.Pipeline = np
		if err := p.SaveConfig(cfg); err != nil {
			return err
		}
		fmt.Println("Removed (its prompt file was kept).")
		return nil
	case "order":
		if len(pos) != len(cfg.Pipeline) {
			return fmt.Errorf("list all %d agents in the order you want: %s", len(cfg.Pipeline), strings.Join(cfg.Pipeline, " "))
		}
		next := cfg
		next.Pipeline = pos
		if probs := next.Validate(); len(probs) > 0 {
			return fmt.Errorf("%s", strings.Join(probs, "; "))
		}
		if err := p.SaveConfig(next); err != nil {
			return err
		}
		fmt.Println("Pipeline:", strings.Join(pos, " -> "))
		return nil
	case "run":
		if len(pos) < 1 {
			return fmt.Errorf("usage: hloop agent run <name> [instruction]")
		}
		if _, ok := cfg.Agents[pos[0]]; !ok {
			return fmt.Errorf("no agent %q", pos[0])
		}
		e, err := newEngine(p, os.Stdout)
		if err != nil {
			return err
		}
		res := e.RunAgent(contextBackground(), pos[0], strings.Join(pos[1:], " "))
		fmt.Println(lastN(res.Tail, 15))
		if res.Class != "ok" {
			return fmt.Errorf("agent %q did not finish (%s)", pos[0], res.Class)
		}
		return nil
	}
	return fmt.Errorf("unknown agent command %q", sub)
}

func copyAgents(m map[string]store.Agent) map[string]store.Agent {
	out := make(map[string]store.Agent, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	return out
}

// ---------- agent profiles ----------

func agentCatalog(cfg store.Config) error {
	ps, errs := scaffold.Profiles()
	fmt.Println("Agent profiles you can add to a project (hloop agent install <name>...):")
	fmt.Println()
	for _, pr := range ps {
		mark := "  "
		if _, ok := cfg.Agents[pr.Name]; ok {
			mark = "* "
		}
		trig := pr.Trigger
		if pr.Trigger == "every_n_turns" {
			trig = fmt.Sprintf("every %d turns", pr.EveryN)
		}
		src := ""
		if pr.Source != "built-in" {
			src = "  [yours: " + pr.Source + "]"
		}
		fmt.Printf("%s%-20s %-10s %-9s runs %s%s\n    %s\n", mark, pr.Name, pr.Role, pr.Mode, trig, src, pr.Description)
	}
	fmt.Println("\n* already installed in this project")
	fmt.Println(scaffold.CoderNote)
	fmt.Println("\nEach installed agent is one more harness run after the builder's turns, so it uses more of your usage allowance.")
	fmt.Println("Edit the instructions of any installed agent in .loop/prompts/<name>.md. Add your own profiles as markdown files in " + scaffold.UserProfileDir())
	for _, e := range errs {
		fmt.Fprintln(os.Stderr, "profile problem:", e)
	}
	return nil
}

func agentInstall(p store.Project, cfg store.Config, names []string, adName string, all bool) error {
	ps, errs := scaffold.Profiles()
	for _, e := range errs {
		fmt.Fprintln(os.Stderr, "profile problem:", e)
	}
	if all {
		names = nil
		for _, pr := range ps {
			names = append(names, pr.Name)
		}
	}
	if len(names) == 0 {
		if !isTTY() {
			return fmt.Errorf("usage: hloop agent install <profile>... (see `hloop agent catalog`), or --all")
		}
		fmt.Println("Which agents do you want to add?")
		for i, pr := range ps {
			state := ""
			if _, ok := cfg.Agents[pr.Name]; ok {
				state = " (installed)"
			}
			fmt.Printf("  %d. %-20s %s%s\n", i+1, pr.Name, pr.Description, state)
		}
		line := prompt(bufio.NewReader(os.Stdin), "Numbers or names, separated by commas (empty to cancel)", "")
		for _, tok := range strings.FieldsFunc(line, func(r rune) bool { return r == ',' || r == ' ' }) {
			if n, err := strconv.Atoi(tok); err == nil && n >= 1 && n <= len(ps) {
				names = append(names, ps[n-1].Name)
			} else {
				names = append(names, tok)
			}
		}
		if len(names) == 0 {
			fmt.Println("Nothing installed.")
			return nil
		}
	}
	ad := adName
	if ad == "" {
		ad = cfg.Agents[cfg.Pipeline[0]].Adapter
	}
	if _, err := adapter.Load(ad, p.Dir()); err != nil {
		return err
	}
	next := cfg
	next.Agents = copyAgents(cfg.Agents)
	next.Pipeline = append([]string{}, cfg.Pipeline...)
	var added []string
	for _, n := range names {
		if n == "coder" || n == "builder" {
			fmt.Println(scaffold.CoderNote)
			continue
		}
		pr, ok := findProfile(ps, n)
		if !ok {
			return fmt.Errorf("no profile %q (see `hloop agent catalog`)", n)
		}
		if _, exists := next.Agents[pr.Name]; exists {
			fmt.Printf("%s is already installed; skipped (edit .loop/prompts/%s.md to change it).\n", pr.Name, pr.Name)
			continue
		}
		a := store.Agent{Adapter: ad, Role: pr.Role, Prompt: "prompts/" + pr.Name + ".md", Mode: pr.Mode, Trigger: pr.Trigger, EveryN: pr.EveryN, Session: "fresh", Tier: pr.Tier}
		next.Agents[pr.Name] = a
		next.Pipeline = append(next.Pipeline, pr.Name)
		if err := store.WriteFile(p.Path(filepath.FromSlash(a.Prompt)), []byte(pr.Body), 0o644); err != nil {
			return err
		}
		added = append(added, pr.Name)
		fmt.Printf("Installed %-20s (%s, %s, runs %s)\n", pr.Name, pr.Role, pr.Mode, pr.Trigger)
		if pr.Mode == "write" {
			fmt.Printf("  note: %s may edit project files; its limits are in its instructions, and the reviewer will see what it changed.\n", pr.Name)
		}
	}
	if len(added) == 0 {
		return nil
	}
	if probs := next.Validate(); len(probs) > 0 {
		return fmt.Errorf("%s", strings.Join(probs, "; "))
	}
	if err := p.SaveConfig(next); err != nil {
		return err
	}
	fmt.Printf("\nPipeline: %s\n", strings.Join(next.Pipeline, " -> "))
	fmt.Println("Edit any agent's instructions in .loop/prompts/<name>.md. Run one by hand with `hloop agent run <name>`.")
	return nil
}

func findProfile(ps []scaffold.Profile, name string) (scaffold.Profile, bool) {
	for _, p := range ps {
		if p.Name == name {
			return p, true
		}
	}
	return scaffold.Profile{}, false
}
