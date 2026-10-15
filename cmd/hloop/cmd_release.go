package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"hloop/internal/engine"
	"hloop/internal/notify"
	"hloop/internal/scaffold"
	"hloop/internal/store"
)

const releaseUsage = `usage:
  hloop release [VERSION] [--bump patch|minor|major]   check, write release notes + documentation, commit
  hloop release --dry-run [VERSION]                   show the checks and the brief the writer would get
  hloop release list                                  past releases
  hloop release notes [VERSION]                       print a release's notes (default: the latest)

Options: --allow-pending (release what is done), --no-checks (skip the gate run),
--no-commit, --tag (annotated git tag vVERSION, never pushed), -i (work with the writer interactively),
--adapter NAME (use another harness for the writer)`

func cmdRelease(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "list", "ls":
			return releaseList(args[1:])
		case "notes":
			return releaseNotes(args[1:])
		}
	}
	if err := humanOnly("make a release"); err != nil {
		return err
	}
	fs := newFS("release")
	dir := fs.String("C", "", "project directory or name")
	bump := fs.String("bump", "", "major, minor or patch (default: patch, or 0.1.0 for the first release)")
	dry := fs.Bool("dry-run", false, "run the checks and print the brief; change nothing")
	allowPending := fs.Bool("allow-pending", false, "release even though some tasks are not finished")
	noChecks := fs.Bool("no-checks", false, "do not run the gates first")
	noCommit := fs.Bool("no-commit", false, "leave the documents uncommitted for you to review")
	tag := fs.Bool("tag", false, "create an annotated git tag vVERSION (never pushed)")
	interactive := fs.Bool("i", false, "work with the writer in an interactive session")
	adapterName := fs.String("adapter", "", "harness for the writer")
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	p, cfg, err := loadProject(*dir)
	if err != nil {
		return err
	}
	if err := p.RequireTrust(); err != nil {
		return err
	}
	l, _ := store.LoadLedger(p)

	// version
	var version string
	switch {
	case len(pos) > 0:
		if version, err = engine.NormalizeVersion(pos[0]); err != nil {
			return err
		}
	case l.LastRelease() != nil:
		if version, err = engine.BumpVersion(l.LastRelease().Version, *bump); err != nil {
			return err
		}
	case engine.LatestTagVersion(p.Root) != "":
		if version, err = engine.BumpVersion(engine.LatestTagVersion(p.Root), *bump); err != nil {
			return err
		}
	default:
		version = "0.1.0"
		if *bump == "major" {
			version = "1.0.0"
		}
	}
	if l.FindRelease(version) != nil {
		return fmt.Errorf("v%s was already released (hloop release list)", version)
	}

	// checks
	fmt.Printf("Release v%s of %s\n\nChecking that it is ready...\n", version, cfg.Name)
	pf := engine.ReleasePreflight(contextBackground(), p, cfg, *allowPending, !*noChecks)
	for _, g := range pf.Gates {
		fmt.Println("  " + g.Summary())
	}
	for _, w := range pf.Warnings {
		fmt.Println("  warning:", w)
	}
	for _, x := range pf.Problems {
		fmt.Println("  NOT READY:", x)
	}
	if !pf.OK() {
		fmt.Println("\nFix these first (or see `hloop release --help`).")
		return errSilent
	}
	if len(pf.Problems) == 0 {
		fmt.Println("  ready")
	}

	rp, err := engine.PlanRelease(p, version, time.Now())
	if err != nil {
		return err
	}
	brief := engine.ReleaseBrief(p, cfg, rp, pf)
	if *dry {
		fmt.Println()
		fmt.Print(brief)
		fmt.Printf("\n(dry run: nothing written. Release notes would go to %s.)\n", rp.Notes)
		return nil
	}
	scope := "everything since v" + func() string {
		if rp.Previous != nil {
			return rp.Previous.Version
		}
		return ""
	}()
	if rp.First {
		scope = "the whole project (first release)"
	}
	fmt.Printf("\nCovers %s: %d finished task(s).\n", scope, len(rp.Tasks))
	fmt.Printf("The technical writer will write %s, update CHANGELOG.md, and create or update the user and developer documentation (README.md, docs/).\n", rp.Notes)
	if !*yes && isTTY() {
		fmt.Print("Go ahead? [Y/n]: ")
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); err != nil || (a != "" && a != "y" && a != "yes") {
			fmt.Println("Cancelled.")
			return nil
		}
	}

	// the writer
	briefRel := filepath.ToSlash(filepath.Join("release", "brief-v"+version+".md"))
	keep := p.PreserveTrust()
	if err := writeIfMissing(p.Path("prompts", "tech-writer.md"), scaffold.DefaultPrompt("tech-writer")); err != nil {
		return err
	}
	keep()
	if err := store.WriteFile(p.Path(filepath.FromSlash(briefRel)), []byte(brief), 0o644); err != nil {
		return err
	}
	ag, ok := cfg.Agents["tech-writer"]
	if !ok {
		ad := ""
		if len(cfg.Pipeline) > 0 {
			ad = cfg.Agents[cfg.Pipeline[0]].Adapter
		}
		ag = store.Agent{Adapter: ad, Role: "tech-writer", Prompt: "prompts/tech-writer.md", Mode: "docs", Trigger: "manual", Session: "fresh", Tier: "standard", TimeoutMinutes: 90}
	}
	ag.Mode = "docs" // whatever the config says, a release writer stays in its lane
	if *adapterName != "" {
		ag.Adapter = *adapterName
	}
	cfg.Agents["tech-writer"] = ag
	instr := fmt.Sprintf("Release v%s. The full release brief is in .loop/%s; it is also below.\nWrite the release notes to %s (create the folder if needed). Today's date: %s.\n\n%s", version, briefRel, rp.Notes, rp.Date.Format("2006-01-02"), brief)
	e := engine.New(p, cfg)
	e.Exe, e.Log = exePath(), os.Stdout
	if *interactive {
		fmt.Println("\nStarting an interactive session with the technical writer (it may change only docs/, root *.md and .loop/)...")
		if err := e.RunInteractiveAgent("tech-writer", instr); err != nil {
			return err
		}
	} else {
		fmt.Println("\nThe technical writer is working (it may change only docs/, root *.md and .loop/; this can take a while)...")
		res := e.RunAgent(contextBackground(), "tech-writer", instr)
		if res.Tail != "" {
			fmt.Println(lastN(res.Tail, 30))
		}
		if res.Class != "ok" {
			return fmt.Errorf("the technical writer did not finish (%s); nothing was recorded. Run `hloop release %s` again", res.Class, version)
		}
	}

	// verify what was written
	rc := engine.CheckReleaseOutput(p, rp)
	if len(rc.Problems) > 0 {
		fmt.Println("\nThe release is NOT recorded:")
		for _, x := range rc.Problems {
			fmt.Println("  -", x)
		}
		fmt.Printf("Documents written so far are left in place. Run `hloop release %s` again, or `hloop release %s -i` to work through it with the writer.\n", version, version)
		return errSilent
	}
	fmt.Printf("\nDocuments created or updated (%d):\n", len(rc.Changed))
	for _, f := range rc.Changed {
		mark := ""
		if n := rc.Unfinished[f]; n > 0 {
			mark = fmt.Sprintf("   (%d open [[FILL]]/TODO marker(s) for you)", n)
		}
		fmt.Printf("  %s%s\n", f, mark)
	}

	rel := store.Release{Version: version, Date: rp.Date.UTC(), CodeHead: rp.Head, From: rp.From, Notes: rp.Notes, Docs: rc.Changed, Warnings: pf.Warnings}
	for _, t := range rp.Tasks {
		rel.Tasks = append(rel.Tasks, t.ID)
	}
	commit := rp.Head
	if !*noCommit {
		if commit, err = engine.CommitRelease(p, "docs(release): v"+version+" release notes and documentation", rc.Changed); err != nil {
			return err
		}
		fmt.Printf("\nCommitted the documents (%s).\n", shortHash(commit))
		if *tag {
			t, err := engine.TagRelease(p, version)
			if err != nil {
				return err
			}
			rel.Tag = t
			fmt.Printf("Tagged %s (not pushed: `git push origin %s` when you are ready).\n", t, t)
		}
	}
	rel.Commit = commit
	if err := store.UpdateLedger(p, func(l *store.Ledger) error {
		l.Releases = append(l.Releases, rel)
		return nil
	}); err != nil {
		return err
	}
	if !*noCommit {
		_, _ = engine.CommitRelease(p, "chore(loop): record release v"+version, nil)
	} else {
		fmt.Println("\nDocuments left uncommitted for your review. The release is recorded; commit when you are happy.")
	}
	if len(rc.Unfinished) > 0 {
		fmt.Println("\nSome documents have questions only you can answer ([[FILL: ...]]). Search for [[FILL and fill them in.")
	}
	fmt.Printf("\nReleased v%s. Notes: %s\n", version, filepath.Join(p.Root, filepath.FromSlash(rp.Notes)))
	if hasEvent(cfg, "complete") {
		notify.NewDispatcher(os.Stderr).Send(notify.Event{Kind: notify.KindComplete, Title: fmt.Sprintf("%s: v%s released", cfg.Name, version),
			Body: fmt.Sprintf("%d task(s). Release notes and docs written (%d files).", len(rp.Tasks), len(rc.Changed)), Project: projectName(p, cfg)})
	}
	return nil
}

func shortHash(h string) string {
	if len(h) > 9 {
		return h[:9]
	}
	return h
}

func hasEvent(cfg store.Config, kind string) bool {
	for _, e := range cfg.Notify.Events {
		if e == kind {
			return true
		}
	}
	return false
}

func releaseList(args []string) error {
	fs := newFS("release list")
	dir := fs.String("C", "", "project directory or name")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	p, _, err := loadProject(*dir)
	if err != nil {
		return err
	}
	l, _ := store.LoadLedger(p)
	if len(l.Releases) == 0 {
		fmt.Println("No releases yet. When the tasks are finished: hloop release")
		return nil
	}
	rs := append([]store.Release{}, l.Releases...)
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].Date.After(rs[j].Date) })
	for _, r := range rs {
		tag := ""
		if r.Tag != "" {
			tag = "  tag " + r.Tag
		}
		fmt.Printf("v%-10s %s  %2d task(s)  %s%s\n", r.Version, r.Date.Local().Format("2006-01-02"), len(r.Tasks), r.Notes, tag)
	}
	return nil
}

func releaseNotes(args []string) error {
	fs := newFS("release notes")
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
	r := l.LastRelease()
	if len(pos) > 0 {
		r = l.FindRelease(pos[0])
	}
	if r == nil {
		return fmt.Errorf("no such release (hloop release list)")
	}
	b, err := os.ReadFile(filepath.Join(p.Root, filepath.FromSlash(r.Notes)))
	if err != nil {
		return err
	}
	fmt.Print(string(b))
	return nil
}
