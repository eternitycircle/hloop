package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"hloop/docs"
	"hloop/internal/store"
)

// ---------- docs: the embedded reference ----------

func cmdDocs(args []string) error {
	fs := newFS("docs")
	all := fs.Bool("all", false, "print every page")
	export := fs.String("export", "", "write the pages as markdown files into this folder")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	topics := docs.Topics()
	switch {
	case *export != "":
		if err := os.MkdirAll(*export, 0o755); err != nil {
			return err
		}
		for _, t := range topics {
			if err := store.WriteFile(filepath.Join(*export, t.File), []byte(t.Body), 0o644); err != nil {
				return err
			}
		}
		fmt.Printf("Wrote %d pages to %s (start with %s).\n", len(topics), *export, topics[0].File)
		return nil
	case *all:
		for i, t := range topics {
			if i > 0 {
				fmt.Println("\n" + strings.Repeat("=", 72) + "\n")
			}
			fmt.Print(t.Body)
		}
		return nil
	case len(pos) >= 1 && pos[0] == "find":
		word := strings.ToLower(strings.TrimSpace(strings.Join(pos[1:], " ")))
		if word == "" {
			return fmt.Errorf("usage: hloop docs find <word>")
		}
		n := 0
		for _, t := range topics {
			for _, line := range strings.Split(t.Body, "\n") {
				if strings.Contains(strings.ToLower(line), word) {
					fmt.Printf("%-16s %s\n", t.Name, strings.TrimSpace(line))
					n++
				}
			}
		}
		if n == 0 {
			fmt.Printf("Nothing found for %q. Topics: hloop docs\n", word)
		}
		return nil
	case len(pos) >= 1:
		t, ok := docs.Find(pos[0])
		if !ok {
			return fmt.Errorf("no reference page %q; run `hloop docs` for the list", pos[0])
		}
		fmt.Print(t.Body)
		return nil
	}
	fmt.Println("hloop reference (also in docs/reference/ in the source). Read one with `hloop docs <topic>`.")
	fmt.Println()
	for _, t := range topics {
		fmt.Printf("  %-16s %s\n", t.Name, t.Title)
	}
	fmt.Println()
	fmt.Println("  hloop docs find <word>     search all pages")
	fmt.Println("  hloop docs --all           everything, in reading order")
	fmt.Println("  hloop docs --export DIR    save the pages as files to read in an editor or browser")
	fmt.Println("  hloop tour                 interactive walkthrough on a demo project")
	return nil
}

// helpTopics maps commands to the reference page that explains them.
var helpTopics = map[string]string{
	"init": "getting-started", "adopt": "getting-started", "doctor": "troubleshooting", "trust": "safety",
	"spec": "defining-work", "plan": "defining-work", "task": "defining-work", "decision": "defining-work",
	"idea": "defining-work", "lesson": "defining-work", "blocker": "running", "note": "running",
	"run": "running", "status": "running", "pause": "running", "resume": "running", "logs": "running",
	"schedule": "running", "projects": "running", "gate": "verification", "review": "verification",
	"agent": "agents", "adapter": "agents", "skill": "agents", "notify": "phone", "config": "commands",
	"tour": "overview", "docs": "overview", "files": "files", "release": "releases",
}

func cmdHelp(args []string) error {
	if len(args) == 0 {
		usage(os.Stdout)
		return nil
	}
	topic := args[0]
	if t, ok := helpTopics[topic]; ok {
		topic = t
	}
	t, ok := docs.Find(topic)
	if !ok {
		usage(os.Stdout)
		return fmt.Errorf("no help for %q", args[0])
	}
	if args[0] != t.Name {
		fmt.Printf("(`hloop %s --help` lists the flags; this is the reference page that covers it)\n\n", args[0])
	}
	fmt.Print(t.Body)
	return nil
}

// ---------- AGENTS.md / CLAUDE.md pointer ----------

const agentsBlockStart = "<!-- hloop:start (managed by `hloop skill install --agents-md`; edit outside these markers) -->"
const agentsBlockEnd = "<!-- hloop:end -->"

const agentsBlockBody = `## Autonomous loop (hloop)

This repository is worked on by an hloop loop. Its state lives in ` + "`.loop/`" + ` and changes only through the ` + "`hloop`" + ` command.

- What the product is: ` + "`.loop/PRD.md`" + `. Feature specs: ` + "`.loop/specs/<feature>/spec.md`" + `.
- Rules every agent follows: ` + "`.loop/GUARDRAILS.md`" + `. Current state: ` + "`.loop/CURRENT.md`" + `. Progress: ` + "`.loop/PROGRESS.md`" + ` (full history in ` + "`.loop/PROGRESS_LOG.md`" + `).
- Never edit ` + "`.loop/ledger.json`" + ` or the generated views (PROGRESS, PROGRESS_LOG, DECISIONS, LESSONS, reports, specs/*/tasks.md).
- To add or inspect work: ` + "`hloop status`" + `, ` + "`hloop task add \"...\" --ac \"statement :: how verified\"`" + `, ` + "`hloop docs`" + `.`

// upsertManagedBlock adds or replaces hloop's block in a markdown file,
// leaving everything outside the markers untouched.
func upsertManagedBlock(path, body string) (string, error) {
	block := agentsBlockStart + "\n" + body + "\n" + agentsBlockEnd + "\n"
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	s := string(old)
	i, j := strings.Index(s, agentsBlockStart), strings.Index(s, agentsBlockEnd)
	var out, verb string
	switch {
	case i >= 0 && j > i:
		out = s[:i] + block + strings.TrimPrefix(s[j+len(agentsBlockEnd):], "\n")
		verb = "updated"
	case len(strings.TrimSpace(s)) == 0:
		out, verb = block, "created"
	default:
		out, verb = strings.TrimRight(s, "\n")+"\n\n"+block, "added a section to"
	}
	if out == s {
		return "unchanged", nil
	}
	return verb, store.WriteFile(path, []byte(out), 0o644)
}
