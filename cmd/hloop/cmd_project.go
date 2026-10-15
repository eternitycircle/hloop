package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"hloop/internal/adapter"
	"hloop/internal/engine"
	"hloop/internal/notify"
	"hloop/internal/scaffold"
	"hloop/internal/sched"
	"hloop/internal/store"
)

func agentEnv(p store.Project, role string) []string {
	return []string{"HLOOP_BIN=" + exePath(), "HLOOP_PROJECT=" + p.Root, "HLOOP_AGENT=" + role, "HLOOP_ROLE=" + role}
}

func warnPermissions(ad adapter.Adapter) {
	if ad.SkipsPermissions {
		fmt.Printf("Note: %s runs unattended with approval prompts disabled. It can run any command as you.\n"+
			"      Use a dedicated machine, VM, container or user, and keep powerful credentials out of reach.\n", ad.Name)
	}
}

// ---------- init ----------

func cmdInit(args []string) error {
	fs := newFS("init")
	adapterName := fs.String("adapter", "", "harness adapter (agy, claude-code, or your own)")
	name := fs.String("name", "", "project name")
	ai := fs.Bool("ai", false, "let your harness interview you interactively")
	yes := fs.Bool("yes", false, "accept detected defaults without asking questions")
	verify := fs.Bool("verify", false, "check that the project documents are complete")
	force := fs.Bool("force", false, "re-create the documents of an existing project")
	upgrade := fs.Bool("upgrade", false, "add what a project made by an older hloop is missing (never overwrites)")
	refresh := fs.Bool("refresh-prompts", false, "with --upgrade: replace the built-in role prompts with this version's (old ones kept as .bak)")
	var from strList
	fs.Var(&from, "from", "with --ai: a source document or folder for the interviewer to read first (repeatable)")
	dir := fs.String("C", "", "project directory")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}

	if *upgrade {
		p, err := resolveProject(*dir)
		if err != nil {
			return err
		}
		if !p.IsTrusted() {
			return p.RequireTrust()
		}
		keep := p.PreserveTrust()
		did, err := scaffold.Upgrade(p, *refresh)
		keep()
		for _, d := range did {
			fmt.Println("-", d)
		}
		if err != nil {
			return err
		}
		if len(did) == 0 {
			fmt.Println("Already up to date.")
		} else {
			fmt.Println("Done. Review .loop/GUARDRAILS.md (the rules every agent follows) and fill in its project-specific section; after you edit it, approve it with `hloop trust`.")
		}
		offerGate(p, false)
		return nil
	}

	if *verify {
		p, err := resolveProject(*dir)
		if err != nil {
			return err
		}
		cfg, err := p.LoadConfig()
		if err != nil {
			return err
		}
		problems := append(cfg.Validate(), scaffold.Verify(p)...)
		findings := scaffold.SpecCheck(p, cfg, "")
		for _, f := range findings {
			if f.Level == "error" {
				problems = append(problems, fmt.Sprintf("%s: %s", f.Where, f.Text))
			}
		}
		if len(problems) == 0 {
			fmt.Println("Project documents look complete.")
			if n := len(findings); n > 0 {
				fmt.Printf("(%d spec-check warning(s): see `hloop spec check`)\n", n)
			}
			return nil
		}
		for _, x := range problems {
			fmt.Println("-", x)
		}
		return errSilent
	}

	root := "."
	if *dir != "" {
		root = *dir
	}
	if len(pos) > 0 {
		root = pos[0]
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		return fmt.Errorf("%s is not a directory", abs)
	}

	d := scaffold.Discover(abs)
	def := d.Prefill(*name)
	ans := def
	switch {
	case *yes || *ai || !isTTY():
	default:
		if len(d.Commits) >= 5 || len(d.Docs) >= 2 {
			fmt.Println("This looks like a project that is already under way. `hloop adopt` can read it and fill in these files for you.")
			fmt.Println("Continuing with the questionnaire; press Ctrl+C to switch.")
			fmt.Println()
		}
		ans = scaffold.Ask(os.Stdin, os.Stdout, def)
	}

	ad := *adapterName
	if ad == "" {
		ad = scaffold.DetectAdapter()
	}
	if _, err := adapter.Load(ad, ""); err != nil {
		return err
	}
	if prev := (store.Project{Root: abs}); prev.Exists() && !prev.IsTrusted() {
		return store.ErrUntrusted{Root: abs, Changed: prev.HasTrustRecord()}
	}
	p, err := scaffold.Create(scaffold.Options{Root: abs, Adapter: ad, Answers: ans, Force: *force})
	if err != nil {
		return err
	}
	reg, err := store.Register(ans.Name, abs)
	if err != nil {
		return err
	}
	if err := p.Trust(); err != nil {
		return err
	}
	fmt.Printf("Created %s (registered as %q, harness: %s)\n", p.Dir(), reg, ad)

	if *ai {
		a, _ := adapter.Load(ad, p.Dir())
		warnPermissions(a)
		cfg, err := p.LoadConfig()
		if err != nil {
			return err
		}
		sources, err := sourcesBlock(from)
		if err != nil {
			return err
		}
		if err := defineProject(p, cfg, true, sources, "", ""); err != nil {
			fmt.Println(err)
			fmt.Printf("Paste the contents of %s into your harness instead, then run: hloop init --verify\n", p.Path("prompts", "interviewer.md"))
			return nil
		}
	}
	offerGate(p, *yes)

	problems := scaffold.Verify(p)
	if len(problems) > 0 {
		fmt.Println("\nStill to do:")
		for _, x := range problems {
			fmt.Println(" -", x)
		}
		fmt.Println("\nTip: `hloop plan \"<what you want>\"` has an agent fill these in; `hloop task add \"...\"` queues work.")
	}
	fmt.Println("\nNext steps:")
	fmt.Println("  hloop doctor              check your harness is installed")
	fmt.Println("  hloop run                 start the loop")
	fmt.Println("  hloop schedule install    keep it running automatically")
	fmt.Println("  hloop notify setup telegram   (optional) alerts and control from your phone")
	return nil
}

func writeIfMissing(path, content string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	return store.WriteFile(path, []byte(content), 0o644)
}

// ---------- adopt ----------

func cmdAdopt(args []string) error {
	fs := newFS("adopt")
	adapterName := fs.String("adapter", "", "harness adapter to do the reading and writing")
	name := fs.String("name", "", "project name (default: folder name)")
	printPrompt := fs.Bool("print-prompt", false, "print the exact instructions and exit")
	interactive := fs.Bool("interactive", false, "run the harness interactively so you can answer its questions")
	noRun := fs.Bool("no-run", false, "create .loop/ and the instructions file, but do not run a harness")
	force := fs.Bool("force", false, "re-run adoption on a project that already has .loop/ (existing documents are edited in place)")
	dir := fs.String("C", "", "project directory")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	root := "."
	if *dir != "" {
		root = *dir
	}
	if len(pos) > 0 {
		root = pos[0]
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		return fmt.Errorf("%s is not a directory", abs)
	}
	d := scaffold.Discover(abs)

	if *printPrompt {
		fmt.Print(scaffold.AdoptPrompt(d))
		return nil
	}
	if d.HasLoop && !*force {
		return fmt.Errorf("%s already has a .loop project; use `hloop status`, or --force to run adoption again (existing documents are kept and edited in place)", abs)
	}

	adName := *adapterName
	if adName == "" {
		adName = scaffold.DetectAdapter()
	}
	ad, err := adapter.Load(adName, "")
	if err != nil {
		return err
	}

	p := store.Project{Root: abs}
	if d.HasLoop && !p.IsTrusted() {
		return store.ErrUntrusted{Root: abs, Changed: p.HasTrustRecord()}
	}
	if !d.HasLoop {
		ans := d.Prefill(*name)
		p, err = scaffold.Create(scaffold.Options{Root: abs, Adapter: adName, Answers: ans})
		if err != nil {
			return err
		}
		reg, err := store.Register(ans.Name, abs)
		if err != nil {
			return err
		}
		if err := p.Trust(); err != nil {
			return err
		}
		fmt.Printf("Created %s (registered as %q)\n", p.Dir(), reg)
	}
	keepTrust := p.PreserveTrust()
	if err := store.WriteFile(p.Path("prompts", "adopter.md"), []byte(scaffold.DefaultPrompt("adopter")), 0o644); err != nil {
		return err
	}
	instrPath := p.Path("adopt-instructions.md")
	if err := store.WriteFile(instrPath, []byte(scaffold.AdoptPrompt(d)), 0o644); err != nil {
		return err
	}
	keepTrust()

	fmt.Printf("Scanned: %d document(s), %d recent commit(s), stack: %s\n", len(d.Docs), len(d.Commits), orNone(d.Stack))
	if !d.IsGit {
		fmt.Println("Warning: this is not a git repository, so the safety guard that reverts out-of-scope edits is off. Consider `git init` first.")
	}
	if d.DirtyFiles > 0 {
		fmt.Printf("Note: %d uncommitted file(s) in the working tree. Adoption does not modify them, but a clean tree makes later review easier.\n", d.DirtyFiles)
	}
	if len(d.SecretLike) > 0 {
		fmt.Printf("Heads up: secret-like files found (%s). They are listed by name only; the agent is told never to read or copy them.\n", strings.Join(d.SecretLike, ", "))
	}

	if *noRun {
		fmt.Printf("\nInstructions written to %s\n", instrPath)
		fmt.Println("Give them to any harness, for example:")
		fmt.Printf("  open your harness in %s and say: \"Read .loop/adopt-instructions.md and follow it exactly.\"\n", abs)
		fmt.Println("(The harness will call `hloop task add`, `hloop decision add`, etc. Make sure `hloop` is on its PATH.)")
		fmt.Println("When it finishes, run: hloop init --verify")
		return nil
	}

	if out, err := ad.CheckInstalled(nil); err != nil {
		return fmt.Errorf("%v\nInstall/sign in to %s first, pick another with --adapter, or use --no-run to get the instructions file for any harness", err, ad.Bin)
	} else if out != "" {
		fmt.Printf("Harness: %s (%s)\n", ad.Name, firstLineOf(out))
	}
	warnPermissions(ad)

	cfg, err := p.LoadConfig()
	if err != nil {
		return err
	}
	cfg.Agents["adopter"] = store.Agent{Adapter: adName, Role: "adopter", Prompt: "prompts/adopter.md", Mode: "loop-only", Trigger: "manual", Session: "fresh", TimeoutMinutes: 60}
	wasTrusted := p.IsTrusted()
	parts, _ := p.FingerprintParts()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *interactive {
		fmt.Println("\nStarting an interactive adoption session...")
		if err := ad.RunInteractive(abs, scaffold.AdoptPrompt(d), cfg.PathExtra, agentEnv(p, "adopter")); err != nil {
			fmt.Println("Could not start an interactive session:", err)
			fmt.Printf("Use the instructions in %s with your harness, or drop --interactive.\n", instrPath)
			return nil
		}
	} else {
		fmt.Println("\nReading your project and writing the loop files (this can take several minutes)...")
		eng := engine.New(p, cfg)
		eng.Exe = exePath()
		eng.Log = os.Stdout
		res := eng.RunAgent(ctx, "adopter", d.AdoptInstruction())
		if res.Class != "ok" {
			fmt.Printf("\nThe harness did not finish (%s).\n", res.Class)
			if res.Tail != "" {
				fmt.Println("Last output:\n" + lastN(res.Tail, 10))
			}
			if res.Class == "rate_limited" {
				fmt.Println("It looks like a usage limit. Run `hloop adopt --force` later; work already written is kept.")
			} else {
				fmt.Printf("Try `hloop adopt --force --interactive`, or use %s with your harness by hand.\n", instrPath)
			}
			return errSilent
		}
	}

	approveSetupChanges(p, parts, wasTrusted)
	if err := printAdoptReport(p); err != nil {
		return err
	}
	offerGate(p, false)
	return nil
}

func printAdoptReport(p store.Project) error {
	l, _ := store.LoadLedger(p)
	fmt.Printf("\nAdoption result: %d finished task(s) recorded, %d queued, %d open decision(s), %d blocker(s).\n",
		l.Count(store.TaskDone), l.Remaining(), len(l.OpenDecisions()), len(l.OpenBlockers()))
	if qs := scaffold.OpenQuestions(p); len(qs) > 0 {
		fmt.Println("\nThings the agent could not determine from the repository (answer with `hloop plan \"...\"`):")
		for _, q := range qs {
			fmt.Println(" -", q)
		}
	}
	if probs := scaffold.Verify(p); len(probs) > 0 {
		fmt.Println("\nStill to do:")
		for _, x := range probs {
			fmt.Println(" -", x)
		}
	}
	noAC := 0
	for _, t := range l.Tasks {
		if (t.Status == store.TaskPending || t.Status == store.TaskInProgress) && len(t.Criteria) == 0 && t.EffKind() == store.KindCode {
			noAC++
		}
	}
	if noAC > 0 {
		fmt.Printf("\n%d queued code task(s) have no acceptance criteria, so the builder would invent its own finish line. Run `hloop spec check`, then `hloop spec --task <ID>` (or `hloop task ac <ID> add`) for the ones that matter.\n", noAC)
	}
	fmt.Printf("\nReview the files in %s, especially PRD.md, GUARDRAILS.md, CURRENT.md and PROGRESS.md.\n", p.Dir())
	fmt.Println("Then: hloop run   (and hloop schedule install to keep it going)")
	return nil
}

func orNone(s string) string {
	if s == "" {
		return "not detected"
	}
	return s
}

func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func lastN(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// ---------- projects ----------

func cmdProjects(args []string) error {
	sub := "list"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "list", "ls":
		reg, err := store.LoadRegistry()
		if err != nil {
			return err
		}
		if len(reg) == 0 {
			fmt.Println("No projects registered. Run `hloop init` or `hloop adopt` in a repository.")
			return nil
		}
		for _, e := range reg {
			p := store.Project{Root: e.Path}
			state := "missing"
			if p.Exists() {
				l, _ := store.LoadLedger(p)
				state = store.EffectiveStatus(p, p.LoadState(), l, nowFn())
			}
			fmt.Printf("%-20s %-12s %s\n", e.Name, state, e.Path)
		}
		return nil
	case "add":
		fs := newFS("projects add")
		nm := fs.String("name", "", "registry name")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		root := "."
		if len(pos) > 0 {
			root = pos[0]
		}
		p, err := store.Find(root)
		if err != nil {
			return err
		}
		cfg, err := p.LoadConfig()
		if err != nil {
			return err
		}
		n := *nm
		if n == "" {
			n = cfg.Name
		}
		reg, err := store.Register(n, p.Root)
		if err != nil {
			return err
		}
		fmt.Printf("Registered %s as %q\n", p.Root, reg)
		return nil
	case "remove", "rm":
		if len(args) != 1 {
			return fmt.Errorf("usage: hloop projects remove <name>")
		}
		if e, err := store.Lookup(args[0]); err == nil {
			_ = e.Untrust()
		}
		if err := store.Unregister(args[0]); err != nil {
			return err
		}
		fmt.Println("Removed from the registry (project files were not touched).")
		return nil
	}
	return fmt.Errorf("usage: hloop projects [list|add [dir]|remove <name>]")
}

// ---------- config ----------

func cmdConfig(args []string) error {
	fs := newFS("config")
	dir := fs.String("C", "", "project directory or name")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	p, err := resolveProject(*dir)
	if err != nil {
		return err
	}
	cfg, err := p.LoadConfig()
	if err != nil {
		return err
	}
	if len(pos) > 0 && pos[0] != "show" {
		defer p.PreserveTrust()()
	}
	if len(pos) == 0 || pos[0] == "show" {
		fmt.Printf("name: %s\nmax_turns_per_run: %d\nturn_pause_seconds: %d\nrate_limit_backoff_minutes: %d\nmax_consecutive_failures: %d\nagent_timeout_minutes: %d\nmax_stalled_turns: %d\n",
			cfg.Name, cfg.Limits.MaxTurnsPerRun, cfg.Limits.TurnPauseSeconds, cfg.Limits.RateLimitBackoffMinutes, cfg.Limits.MaxConsecutiveFailures, cfg.Limits.AgentTimeoutMinutes, cfg.Limits.MaxStalledTurns)
		fmt.Printf("remote_tasks: %s\nauto_resume: %v\ncommit_ledger: %v\nnotify_events: %s\npath_extra: %s\npipeline: %s\n",
			cfg.RemoteTasks, cfg.AutoResume, cfg.CommitLedger, strings.Join(cfg.Notify.Events, ","), strings.Join(cfg.PathExtra, string(os.PathListSeparator)), strings.Join(cfg.Pipeline, " -> "))
		g := cfg.Guards
		fmt.Printf("tdd: %s\ngates: %d (hloop gate list)\nguard.protected_branches: %s\nguard.history_rewrite: %s\nguard.protected_commit: %s\nguard.dependency_change: %s\nguard.mass_delete: %s\nguard.mass_delete_count: %d\n",
			cfg.TDD, len(cfg.Gates), strings.Join(g.ProtectedBranches, ","), g.HistoryRewrite, g.ProtectedCommit, g.DependencyChange, g.MassDelete, g.MassDeleteCount)
		return nil
	}
	if pos[0] != "set" || len(pos) < 3 {
		return fmt.Errorf("usage: hloop config show | hloop config set <key> <value>")
	}
	key, val := pos[1], strings.Join(pos[2:], " ")
	atoi := func() (int, error) {
		n, err := strconv.Atoi(val)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("%s needs a non-negative whole number", key)
		}
		return n, nil
	}
	atob := func() (bool, error) {
		b, err := strconv.ParseBool(val)
		if err != nil {
			return false, fmt.Errorf("%s needs true or false", key)
		}
		return b, nil
	}
	switch key {
	case "name":
		cfg.Name = val
	case "max_turns_per_run":
		n, err := atoi()
		if err != nil || n == 0 {
			return fmt.Errorf("max_turns_per_run must be at least 1")
		}
		cfg.Limits.MaxTurnsPerRun = n
	case "turn_pause_seconds":
		n, err := atoi()
		if err != nil {
			return err
		}
		cfg.Limits.TurnPauseSeconds = n
	case "rate_limit_backoff_minutes":
		n, err := atoi()
		if err != nil {
			return err
		}
		cfg.Limits.RateLimitBackoffMinutes = n
	case "max_consecutive_failures":
		n, err := atoi()
		if err != nil {
			return err
		}
		cfg.Limits.MaxConsecutiveFailures = n
	case "max_stalled_turns":
		if val == "off" {
			cfg.Limits.MaxStalledTurns = -1
			break
		}
		n, err := atoi()
		if err != nil || n == 0 {
			return fmt.Errorf("max_stalled_turns needs a whole number of at least 1, or off")
		}
		cfg.Limits.MaxStalledTurns = n
	case "agent_timeout_minutes":
		n, err := atoi()
		if err != nil {
			return err
		}
		cfg.Limits.AgentTimeoutMinutes = n
	case "remote_tasks":
		if val != "approve" && val != "auto" {
			return fmt.Errorf("remote_tasks must be approve or auto")
		}
		cfg.RemoteTasks = val
	case "auto_resume":
		b, err := atob()
		if err != nil {
			return err
		}
		cfg.AutoResume = b
	case "commit_ledger":
		b, err := atob()
		if err != nil {
			return err
		}
		cfg.CommitLedger = b
	case "notify_events":
		var evs []string
		for _, e := range strings.Split(val, ",") {
			e = strings.TrimSpace(e)
			switch e {
			case "blocker", "complete", "rate_limited", "error", "review", "digest", "check":
				evs = append(evs, e)
			case "":
			default:
				return fmt.Errorf("unknown event %q (blocker, complete, rate_limited, error, review, digest, check)", e)
			}
		}
		cfg.Notify.Events = evs
	case "tdd":
		if val != "on" && val != "off" {
			return fmt.Errorf("tdd must be on or off")
		}
		cfg.TDD = val
		if val == "on" && len(cfg.Gates) == 0 {
			return fmt.Errorf("add a test gate first, so hloop can check the red and green commits: hloop gate add test -- <your test command>")
		}
	case "guard.protected_branches":
		cfg.Guards.ProtectedBranches = []string{}
		for _, b := range strings.Split(val, ",") {
			if b = strings.TrimSpace(b); b != "" && b != "none" {
				cfg.Guards.ProtectedBranches = append(cfg.Guards.ProtectedBranches, b)
			}
		}
	case "guard.history_rewrite", "guard.protected_commit", "guard.dependency_change", "guard.mass_delete":
		if val != store.GuardBlock && val != store.GuardNotify && val != store.GuardOff {
			return fmt.Errorf("%s must be block, notify or off", key)
		}
		switch key {
		case "guard.history_rewrite":
			cfg.Guards.HistoryRewrite = val
		case "guard.protected_commit":
			cfg.Guards.ProtectedCommit = val
		case "guard.dependency_change":
			cfg.Guards.DependencyChange = val
		default:
			cfg.Guards.MassDelete = val
		}
	case "guard.mass_delete_count":
		n, err := atoi()
		if err != nil || n == 0 {
			return fmt.Errorf("guard.mass_delete_count needs a whole number of at least 1")
		}
		cfg.Guards.MassDeleteCount = n
	case "path_extra":
		cfg.PathExtra = nil
		for _, e := range strings.Split(val, string(os.PathListSeparator)) {
			if strings.TrimSpace(e) != "" {
				cfg.PathExtra = append(cfg.PathExtra, strings.TrimSpace(e))
			}
		}
	default:
		return fmt.Errorf("unknown setting %q", key)
	}
	if err := p.SaveConfig(cfg); err != nil {
		return err
	}
	fmt.Println("Saved.")
	return nil
}

// ---------- doctor ----------

func cmdDoctor(args []string) error {
	fs := newFS("doctor")
	dir := fs.String("C", "", "project directory or name")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	bad := 0
	ok := func(f string, a ...any) { fmt.Printf("  ok    %s\n", fmt.Sprintf(f, a...)) }
	warn := func(f string, a ...any) { fmt.Printf("  warn  %s\n", fmt.Sprintf(f, a...)) }
	fail := func(f string, a ...any) { bad++; fmt.Printf("  FAIL  %s\n", fmt.Sprintf(f, a...)) }

	fmt.Printf("hloop %s\n\nSystem\n", version)
	if _, err := exec.LookPath("git"); err != nil {
		fail("git is not installed or not on PATH")
	} else {
		ok("git found")
	}

	g, _ := store.LoadGlobal()
	d := notify.NewDispatcher(nil)
	d.G = g
	if ch := d.Channels(); len(ch) == 0 {
		warn("no phone notifications configured (hloop notify setup ntfy|telegram)")
	} else {
		ok("notifications: %s", strings.Join(ch, ", "))
	}

	p, err := resolveProject(*dir)
	if err != nil {
		fmt.Println("\nProject\n  (no project here: run `hloop init` or `hloop adopt`)")
		return doctorResult(bad)
	}
	fmt.Printf("\nProject %s\n", p.Root)
	cfg, err := p.LoadConfig()
	if err != nil {
		fail("config: %v", err)
		return doctorResult(bad)
	}
	for _, pr := range cfg.Validate() {
		fail("config: %s", pr)
	}
	if len(cfg.Validate()) == 0 {
		ok("config valid; pipeline: %s", strings.Join(cfg.Pipeline, " -> "))
	}
	if engine.IsGitRepo(p.Root) {
		ok("git repository")
	} else {
		warn("not a git repository: commits, review ranges and the loop-only guard are unavailable")
	}
	seen := map[string]bool{}
	trusted := p.IsTrusted()
	if !trusted {
		fail("the loop configuration is not approved (config.json, adapters/ or prompts/ differ from what you approved): no harness was started. Review it with `hloop trust --show`, then `hloop trust`")
	}
	for _, n := range cfg.Pipeline {
		ag := cfg.Agents[n]
		if !trusted {
			break
		}
		if seen[ag.Adapter] {
			continue
		}
		seen[ag.Adapter] = true
		ad, err := adapter.Load(ag.Adapter, p.Dir())
		if err != nil {
			fail("adapter %q: %v", ag.Adapter, err)
			continue
		}
		if out, err := ad.CheckInstalled(cfg.PathExtra); err != nil {
			fail("harness %s: %v", ad.Name, err)
			if ad.Docs != "" {
				fmt.Printf("      install/docs: %s\n", ad.Docs)
			}
		} else {
			ok("harness %s: %s", ad.Name, firstLineOf(out))
			if rb, err := ad.Resolve(cfg.PathExtra); err == nil {
				for _, w := range ad.Warnings(rb) {
					warn("%s", w)
				}
			}
		}
		switch ad.Status {
		case "", "verified":
		case "flags-checked":
			warn("adapter %s: flags match the installed CLI's --help but hloop has not run a full loop with it; try `hloop adapter test %s`", ad.Name, ad.Name)
		default:
			warn("adapter %s is %s: run `hloop adapter test %s` to confirm it works before relying on it", ad.Name, orString(ad.Status, "unverified"), ad.Name)
		}
		if ad.SkipsPermissions {
			warn("%s runs with approval prompts disabled: use a VM/container/dedicated user", ad.Name)
		}
	}
	for _, pr := range scaffold.Verify(p) {
		warn("%s", pr)
	}
	if len(cfg.Gates) == 0 {
		warn("no gates: hloop accepts a finished task on the agent's word (hloop gate add test -- <test command>)")
	} else {
		var gn []string
		for _, g := range cfg.Gates {
			gn = append(gn, g.Name)
		}
		ok("gates: %s; tdd %s", strings.Join(gn, ", "), cfg.TDD)
	}
	if _, err := os.Stat(p.Path(store.GuardrailsFile)); err != nil {
		warn("no GUARDRAILS.md (hloop init --upgrade adds one)")
	}
	if fs := scaffold.SpecCheck(p, cfg, ""); len(fs) > 0 {
		e := scaffold.CountErrors(fs)
		if e > 0 {
			warn("spec check: %d error(s), %d warning(s) (hloop spec check)", e, len(fs)-e)
		} else {
			warn("spec check: %d warning(s) (hloop spec check)", len(fs))
		}
	} else {
		ok("spec check clean")
	}
	if pl := countProposedLessons(p); pl > 0 {
		warn("%d lesson(s) proposed by agents wait for you (hloop lesson list / approve)", pl)
	}
	name := projectName(p, cfg)
	if sched.Installed(sched.Spec{Kind: "run", Name: name}) {
		ok("scheduled to run automatically")
	} else {
		warn("not scheduled (hloop schedule install)")
	}
	if p.RunActive() {
		ok("a run is active right now")
	}
	return doctorResult(bad)
}

func countProposedLessons(p store.Project) int {
	l, _ := store.LoadLedger(p)
	return len(l.LessonsWith(store.LessonProposed))
}

func doctorResult(bad int) error {
	if bad > 0 {
		fmt.Printf("\n%d problem(s) found.\n", bad)
		return errSilent
	}
	fmt.Println("\nNo blocking problems.")
	return nil
}

var _ = bufio.NewReader // keep bufio available for future prompts
