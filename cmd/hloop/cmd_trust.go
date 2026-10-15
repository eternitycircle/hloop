package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"hloop/internal/adapter"
)

// allowedForAgents lists what an agent process (HLOOP_AGENT set) may run.
// Everything else (setup, scheduling, notifications, configuration, running
// the loop, approving things) belongs to the human. This is a guard rail
// against mistakes and simple prompt injection: an agent with a shell can
// still edit files directly, so run unattended agents in a VM or container.
var allowedForAgents = map[string][]string{
	"task": {"add", "list", "ls", "done", "skip", "top", "ac", "red", "show", "report"}, "blocker": {"add", "list", "ls"},
	"idea": {"add", "list", "ls"}, "release": {"list", "ls", "notes"}, "lesson": {"add", "list", "ls"}, "spec": {"check", "list", "ls"}, "gate": {"list", "ls"},
	"decision": {"add", "list", "ls"}, "review": {"submit", "list", "ls"}, "agent": {"list", "ls", "catalog"},
	"adapter": {"list", "ls", "show"}, "config": {"show"}, "projects": {"list", "ls"},
	"note": nil, "status": nil, "logs": nil, "version": nil, "help": nil, "doctor": nil, "docs": nil, // nil: any arguments
}

func agentMayRun(cmd string, args []string) bool {
	switch cmd {
	case "__tour-agent": // the tour's demo agent runs as an agent, only inside the tour
		return os.Getenv("HLOOP_TOUR") == "1"
	case "docs":
		for _, a := range args {
			if strings.HasPrefix(a, "--export") || strings.HasPrefix(a, "-export") {
				return false
			}
		}
	}
	if cmd == "init" { // only the read-only check: `hloop init --verify`
		verify := false
		for _, a := range args {
			switch {
			case a == "--verify" || a == "-verify":
				verify = true
			case a == "-C" || strings.HasPrefix(a, "-C=") || strings.HasPrefix(a, "--C="):
			case !strings.HasPrefix(a, "-"):
				// the value of -C, or a positional directory: allowed only after -C
			default:
				return false
			}
		}
		return verify
	}
	subs, ok := allowedForAgents[cmd]
	if !ok {
		return false
	}
	if subs == nil {
		return true
	}
	first := ""
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			first = a
			break
		}
	}
	if first == "" {
		return true // the default action of these commands is read-only listing
	}
	for _, s := range subs {
		if s == first {
			return true
		}
	}
	return false
}

func cmdTrust(args []string) error {
	fs := newFS("trust")
	dir := fs.String("C", "", "project directory or name")
	show := fs.Bool("show", false, "show what would run, without approving")
	yes := fs.Bool("yes", false, "approve without asking")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	if err := humanOnly("approve a loop configuration"); err != nil {
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
	fmt.Printf("Project %s\n\nProgram launches this configuration controls:\n", p.Root)
	names := append([]string{}, cfg.Pipeline...)
	for n := range cfg.Agents {
		found := false
		for _, x := range names {
			if x == n {
				found = true
			}
		}
		if !found {
			names = append(names, n)
		}
	}
	for _, n := range names {
		ag := cfg.Agents[n]
		fmt.Printf("\n  agent %-12s role=%s mode=%s trigger=%s prompt=.loop/%s\n", n, ag.Role, ag.Mode, ag.Trigger, ag.Prompt)
		ad, err := adapter.Load(ag.Adapter, p.Dir())
		if err != nil {
			fmt.Printf("    adapter %q: %v\n", ag.Adapter, err)
			continue
		}
		origin := "built-in or your user config"
		if _, e := os.Stat(filepath.Join(p.Dir(), "adapters", ag.Adapter+".json")); e == nil {
			origin = "THIS PROJECT (.loop/adapters/" + ag.Adapter + ".json)"
		}
		fmt.Printf("    adapter %s [%s]\n    runs: %s %s %s\n", ad.Name, origin, ad.Bin, strings.Join(ad.New, " "), strings.Join(ag.ExtraArgs, " "))
		if ad.SkipsPermissions {
			fmt.Println("    NOTE: this mode disables the harness's own approval prompts")
		}
		if len(ad.Env) > 0 {
			var keys []string
			for k := range ad.Env {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			fmt.Printf("    sets environment: %s\n", strings.Join(keys, ", "))
		}
	}
	if len(cfg.PathExtra) > 0 {
		fmt.Printf("\n  extra PATH entries: %s\n", strings.Join(cfg.PathExtra, ", "))
	}
	if len(cfg.Gates) > 0 {
		fmt.Println("\nGates (commands hloop itself runs when a task is marked done):")
		for _, g := range cfg.Gates {
			fmt.Printf("  gate %-10s runs: %s  [%s]\n", g.Name, strings.Join(g.Run, " "), orString(g.Where, "worktree"))
		}
	}
	fmt.Println("\nPrompts (what the agents are told to do) are in .loop/prompts/, and the rules every agent must follow are in .loop/GUARDRAILS.md. Read them too if this project came from someone else.")

	if p.IsTrusted() {
		fmt.Println("\nThis configuration is already approved.")
		return nil
	}
	if *show {
		fmt.Println("\nNot approved yet. Run `hloop trust` to approve.")
		return nil
	}
	if !*yes {
		if !isTTY() {
			return fmt.Errorf("not approved; run `hloop trust` in a terminal, or add --yes if you have reviewed it")
		}
		fmt.Print("\nApprove this configuration? Only say yes if you trust whoever wrote it. [y/N]: ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			fmt.Println("Not approved.")
			return errSilent
		}
	}
	if err := p.Trust(); err != nil {
		return err
	}
	fmt.Println("Approved.")
	return nil
}
