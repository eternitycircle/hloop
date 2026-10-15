package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"hloop/internal/adapter"
	"hloop/internal/store"
)

const adapterUsage = `usage: hloop adapter <command>

  list                         harnesses hloop can drive, and whether they are installed
  show <name>                  print an adapter definition (copy it to start your own)
  check [name...]              is the harness installed and does it start?
  test <name> [--continue]     run a tiny real task through the harness to prove it works here
  add <name> --bin <exe> ...   define a harness from flags (or --from <existing> to modify one)
  import <file|->              save an adapter from a JSON file
  learn <exe>                  turn a CLI's --help into an adapter (prompt for any AI, or --use <harness>)
  guide                        how to connect a harness hloop does not know`

const adapterGuide = `Connecting any coding-agent CLI to hloop
=========================================

hloop can drive any program that (1) takes a prompt on its command line or stdin, (2) edits files and
runs shell commands in the current directory without asking you, and (3) exits 0 when its turn is done.
Most coding CLIs have such a headless mode: look for -p/--print/--prompt, exec, or run.

1. Find the headless command. Run:   <tool> --help     (and <tool> exec --help / <tool> run --help)
   You need: the flag that takes the prompt, the flag that auto-approves actions, and optionally the
   flag that continues the previous session.

2. Describe it. Either let a model do it:
       hloop adapter learn <tool>                      (prints a prompt; give it to any AI, save its JSON reply)
       hloop adapter import reply.json
   or write it yourself:
       hloop adapter add mytool --bin mytool --arg run --arg --auto --arg '{prompt}' \
            --continue-arg run --continue-arg --continue --continue-arg '{prompt}' --check-arg --version
   or start from a built-in:
       hloop adapter add mytool --from codex --arg exec --arg '{prompt}'

   Placeholders in --arg/--continue-arg: {prompt} is the full prompt text; {prompt_file} is a path to a
   temporary file holding it (for tools that read a file; also avoids command-line length limits on Windows).
   Add --stdin if the tool reads its prompt from standard input instead.

3. Prove it works:   hloop adapter test mytool     (a tiny real task in a throwaway folder; uses a little of your quota)
   Use it:           hloop init --adapter mytool   or   hloop agent add reviewer --adapter mytool

If the tool has no command-line mode (a web agent, an editor plugin), wrap its API in a small script that takes
the prompt on stdin, does the work, and exits 0: see docs/ADAPTERS.md, "Wrapper scripts".
Full field reference: docs/ADAPTERS.md.`

func cmdAdapter(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Println(adapterUsage)
		return nil
	}
	sub, rest := args[0], args[1:]
	fs := newFS("adapter " + sub)
	dir := fs.String("C", "", "project directory or name (for project-level adapters)")
	bin := fs.String("bin", "", "executable name")
	from := fs.String("from", "", "start from an existing adapter and change only what you pass")
	stdin := fs.Bool("stdin", false, "send the prompt on stdin instead of as an argument")
	projectLevel := fs.Bool("project", false, "save into this project's .loop/adapters instead of your user config")
	skips := fs.Bool("skips-permissions", false, "this unattended mode disables the harness's approval prompts")
	docs := fs.String("docs", "", "URL of the harness's headless-mode documentation")
	auth := fs.String("auth", "", "how to sign in for unattended use (shown when the harness fails)")
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	cont := fs.Bool("continue", false, "test: also test resuming the previous session")
	timeout := fs.Int("timeout", 5, "test: minutes before giving up")
	use := fs.String("use", "", "learn: run this adapter's harness to write the new adapter")
	name := fs.String("name", "", "learn: name for the new adapter (default: the executable name)")
	printPrompt := fs.Bool("print-prompt", false, "learn: print the prompt and stop")
	var newArgs, contArgs, interArgs, checkArgs, patterns, altBins, oses, envs, also strList
	fs.Var(&newArgs, "arg", "argument for a fresh non-interactive run; use {prompt} or {prompt_file} (repeatable)")
	fs.Var(&contArgs, "continue-arg", "argument for resuming the previous session (repeatable)")
	fs.Var(&interArgs, "interactive-arg", "argument for an interactive session (repeatable)")
	fs.Var(&checkArgs, "check-arg", "argument for the install check, e.g. --version (repeatable)")
	fs.Var(&patterns, "rate-limit-pattern", "extra text that means a usage limit was hit (repeatable)")
	fs.Var(&altBins, "alt-bin", "another executable name to try if --bin is not found (repeatable)")
	fs.Var(&oses, "os", "operating system this works on: linux, darwin, windows (repeatable)")
	fs.Var(&envs, "env", "KEY=VALUE set for every run (repeatable)")
	fs.Var(&also, "also", "learn: extra help command to include, e.g. \"exec --help\" (repeatable)")
	pos, err := parseArgs(fs, rest)
	if err != nil {
		return err
	}
	var proj store.Project
	projDir := ""
	var cfg store.Config
	if p, c, err := loadProject(*dir); err == nil {
		proj, cfg = p, c
		if p.IsTrusted() || sub == "add" || sub == "import" || sub == "learn" {
			projDir = p.Dir()
		} else if sub != "list" && sub != "ls" && sub != "guide" {
			fmt.Fprintln(os.Stderr, "note: this project's loop configuration is not approved, so its project-level adapters and path_extra are ignored (hloop trust --show)")
			cfg.PathExtra = nil
		}
	}
	saveTarget := func() (string, func(), error) {
		if !*projectLevel {
			return adapter.UserDir(), func() {}, nil
		}
		if projDir == "" {
			return "", nil, fmt.Errorf("--project needs to be run inside a project")
		}
		return filepath.Join(projDir, "adapters"), proj.PreserveTrust(), nil
	}

	switch sub {
	case "guide":
		fmt.Println(adapterGuide)
		return nil

	case "list", "ls":
		fmt.Printf("%-13s %-15s %-14s %s\n", "NAME", "COMMAND", "STATUS", "ON THIS MACHINE")
		for _, n := range adapter.List(projDir) {
			a, err := adapter.Load(n, projDir)
			if err != nil {
				fmt.Printf("%-13s ERROR %v\n", n, err)
				continue
			}
			state := "installed"
			bin, rerr := a.Resolve(cfg.PathExtra)
			if rerr != nil {
				state = "not installed"
			} else if w := a.Warnings(bin); len(w) > 0 {
				state = "installed (see `hloop adapter check " + n + "`)"
			}
			fmt.Printf("%-13s %-15s %-14s %s\n", n, a.Bin, orNone(a.Status), state)
		}
		fmt.Println("\nStatus: verified = hloop ran a loop with it; flags-checked = flags confirmed against the CLI's --help;")
		fmt.Println("documented = from the vendor's docs only; unverified = defined by you. Prove yours with `hloop adapter test <name>`.")
		fmt.Println("Not listed? `hloop adapter guide`.")
		return nil

	case "show":
		if len(pos) != 1 {
			return fmt.Errorf("usage: hloop adapter show <name>")
		}
		a, err := adapter.Load(pos[0], projDir)
		if err != nil {
			return err
		}
		b, _ := json.MarshalIndent(a, "", "  ")
		fmt.Println(string(b))
		return nil

	case "check":
		names := pos
		if len(names) == 0 {
			names = adapter.List(projDir)
		}
		for _, n := range names {
			a, err := adapter.Load(n, projDir)
			if err != nil {
				fmt.Printf("%-13s ERROR %v\n", n, err)
				continue
			}
			out, err := a.CheckInstalled(cfg.PathExtra)
			if err != nil {
				fmt.Printf("%-13s not available: %v\n", n, err)
				if len(pos) > 0 {
					printAdapterHints(a)
				}
				continue
			}
			fmt.Printf("%-13s ok  %s\n", n, firstLineOf(out))
			if b, e := a.Resolve(cfg.PathExtra); e == nil {
				for _, w := range a.Warnings(b) {
					fmt.Printf("%-13s warn %s\n", "", w)
				}
			}
		}
		return nil

	case "add":
		if len(pos) != 1 {
			return fmt.Errorf("usage: hloop adapter add <name> --bin <exe> --arg -p --arg {prompt} [--continue-arg ...] [--check-arg --version]  (or --from <existing>)")
		}
		a := adapter.Adapter{Status: "unverified"}
		if *from != "" {
			base, err := adapter.Load(*from, projDir)
			if err != nil {
				return err
			}
			a = base
			a.Status = "unverified"
			a.Notes = "Based on " + *from + "."
		} else if *bin == "" {
			return fmt.Errorf("--bin <exe> is required (or start from an existing adapter with --from)")
		}
		a.Name = store.SafeName(pos[0])
		a.Display = ""
		if *bin != "" {
			a.Bin = *bin
		}
		set := func(dst *[]string, v strList) {
			if len(v) > 0 {
				*dst = v
			}
		}
		set(&a.New, newArgs)
		set(&a.Continue, contArgs)
		set(&a.Interactive, interArgs)
		set(&a.Check, checkArgs)
		set(&a.RateLimitPatterns, patterns)
		set(&a.Bins, altBins)
		set(&a.OS, oses)
		if *docs != "" {
			a.Docs = *docs
		}
		if *auth != "" {
			a.Auth = *auth
		}
		if *stdin {
			a.PromptVia = "stdin"
		}
		fs.Visit(func(f *flag.Flag) { // an explicit --skips-permissions=false also counts (needed with --from)
			if f.Name == "skips-permissions" {
				a.SkipsPermissions = *skips
			}
		})
		for _, kv := range envs {
			k, v, ok := strings.Cut(kv, "=")
			if !ok || k == "" {
				return fmt.Errorf("--env needs KEY=VALUE, got %q", kv)
			}
			if a.Env == nil {
				a.Env = map[string]string{}
			}
			a.Env[k] = v
		}
		target, keep, err := saveTarget()
		if err != nil {
			return err
		}
		defer keep()
		if err := adapter.Save(target, a); err != nil {
			return err
		}
		fmt.Printf("Saved adapter %q in %s\nProve it works: hloop adapter test %s\n", a.Name, target, a.Name)
		return nil

	case "import":
		if len(pos) != 1 {
			return fmt.Errorf("usage: hloop adapter import <file.json | ->")
		}
		var raw []byte
		if pos[0] == "-" {
			raw, err = io.ReadAll(os.Stdin)
		} else {
			raw, err = os.ReadFile(pos[0])
		}
		if err != nil {
			return err
		}
		a, err := parseAdapterJSON(raw)
		if err != nil {
			return err
		}
		return confirmAndSave(a, saveTarget, *yes)

	case "learn":
		if len(pos) != 1 {
			return fmt.Errorf("usage: hloop adapter learn <executable> [--use <harness>] [--print-prompt] [--also \"exec --help\"]")
		}
		return adapterLearn(pos[0], *name, *use, *printPrompt, also, projDir, cfg.PathExtra, saveTarget, *yes)

	case "test":
		if len(pos) != 1 {
			return fmt.Errorf("usage: hloop adapter test <name> [--continue] [--yes]")
		}
		a, err := adapter.Load(pos[0], projDir)
		if err != nil {
			return err
		}
		return adapterTest(a, cfg.PathExtra, *cont, time.Duration(*timeout)*time.Minute, *yes)
	}
	return fmt.Errorf("unknown adapter command %q\n\n%s", sub, adapterUsage)
}

func printAdapterHints(a adapter.Adapter) {
	if a.Docs != "" {
		fmt.Printf("%-13s docs: %s\n", "", a.Docs)
	}
	if a.Auth != "" {
		fmt.Printf("%-13s sign-in: %s\n", "", a.Auth)
	}
	if len(a.Tiers) > 0 {
		var parts []string
		for _, t := range []string{"fast", "standard", "critical"} {
			if v, ok := a.Tiers[t]; ok {
				parts = append(parts, t+"="+strings.Join(v, " "))
			}
		}
		fmt.Printf("%-13s model tiers: %s\n", "", strings.Join(parts, ", "))
	}
}

// parseAdapterJSON is strict: unknown fields are errors, so a typo such as
// "continue_args" is reported instead of silently ignored.
func parseAdapterJSON(raw []byte) (adapter.Adapter, error) {
	raw = extractJSONObject(raw)
	var a adapter.Adapter
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return a, fmt.Errorf("not a valid adapter definition: %v (see `hloop adapter show claude-code` for the format)", err)
	}
	a.Name = store.SafeName(a.Name)
	if a.Status == "" || a.Status == "verified" || a.Status == "flags-checked" {
		a.Status = "unverified" // only hloop's own tests earn a better label
	}
	return a, nil
}

// extractJSONObject pulls the outermost {...} out of text (a model may wrap it in prose or a code fence).
func extractJSONObject(b []byte) []byte {
	i, j := bytes.IndexByte(b, '{'), bytes.LastIndexByte(b, '}')
	if i < 0 || j < i {
		return b
	}
	return b[i : j+1]
}

func confirmAndSave(a adapter.Adapter, target func() (string, func(), error), yes bool) error {
	b, _ := json.MarshalIndent(a, "", "  ")
	fmt.Println(string(b))
	if a.SkipsPermissions {
		fmt.Println("\nNote: this adapter runs the tool without approval prompts. Use a VM/container for unattended loops.")
	}
	if !yes {
		if !isTTY() {
			return fmt.Errorf("not saved: add --yes to save without asking")
		}
		if ans := strings.ToLower(prompt(bufio.NewReader(os.Stdin), "\nSave this adapter? [y/N]", "n")); ans != "y" && ans != "yes" {
			fmt.Println("Not saved.")
			return errSilent
		}
	}
	dir, keep, err := target()
	if err != nil {
		return err
	}
	defer keep()
	if err := adapter.Save(dir, a); err != nil {
		return err
	}
	fmt.Printf("Saved adapter %q in %s\nProve it works: hloop adapter test %s\n", a.Name, dir, a.Name)
	return nil
}

// ---------- learn ----------

func captureHelp(binPath string, args []string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binPath, args...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	_ = cmd.Run()
	s := out.String()
	if len(s) > 24000 {
		s = s[:24000] + "\n[... truncated ...]"
	}
	return s
}

func adapterLearnPrompt(bin string, helps map[string]string, order []string) string {
	var ex strings.Builder
	for _, n := range []string{"claude-code", "codex", "opencode"} {
		a := adapter.Builtin()[n]
		a.Notes, a.Docs, a.Auth, a.Tiers = "", "", "", nil
		b, _ := json.MarshalIndent(a, "", "  ")
		ex.WriteString(string(b) + "\n\n")
	}
	var h strings.Builder
	for _, k := range order {
		fmt.Fprintf(&h, "$ %s\n%s\n\n", k, strings.TrimSpace(helps[k]))
	}
	return `You are writing a small JSON "adapter" that lets a supervisor program (hloop) run the command-line coding agent "` + bin + `" unattended, one task at a time.

Write ONE JSON object and nothing else (no prose, no code fence). Fields:
- "name": short lowercase name.
- "bin": the executable name. "alt_bins": other executable names it is known by (optional).
- "new": argv array (no executable) for a fresh NON-INTERACTIVE run. It must contain the placeholder "{prompt}" exactly where the prompt text goes. Use "{prompt_file}" instead if the tool takes a path to a file holding the prompt. If the tool reads the prompt from standard input, omit the placeholder and set "prompt_via": "stdin".
- "continue": same, but resuming the most recent session (omit if the tool cannot).
- "interactive": argv array that starts the normal interactive mode with an initial prompt "{prompt}" (omit if unsure).
- "check": argv array that prints the version, e.g. ["--version"].
- "skips_permissions": true if "new" disables the tool's approval prompts / sandbox (unattended runs need this; the tool must edit files and run shell commands without asking).
- "os": ["linux","darwin","windows"] subset it supports (omit if all). "docs": URL (only if present in the help). "auth": one sentence on how to sign in for unattended use (only if stated in the help).
- "rate_limit_patterns": extra lowercase phrases the tool prints when a usage limit is hit (optional).
- "notes": one or two sentences of caveats. "status": "unverified".

Rules: use ONLY flags that appear in the help text below; never invent a flag. If the help does not show how to run non-interactively or how to auto-approve actions, say so in "notes" and leave "new" using the closest documented option. The prompt is long and multi-line, so prefer stdin or a prompt file over a command-line argument when the tool supports it.

Examples of finished adapters:

` + ex.String() + `Help output of the tool:

` + h.String()
}

func adapterLearn(bin, name, use string, printOnly bool, also []string, projDir string, pathExtra []string, target func() (string, func(), error), yes bool) error {
	probe := adapter.Adapter{Name: bin, Bin: bin}
	binPath, err := probe.Resolve(pathExtra)
	if err != nil {
		return fmt.Errorf("%s was not found on PATH. Install it first, or paste its --help output into `hloop adapter learn` after installing", bin)
	}
	helps := map[string]string{bin + " --help": captureHelp(binPath, []string{"--help"})}
	order := []string{bin + " --help"}
	for _, e := range also {
		k := bin + " " + e
		helps[k] = captureHelp(binPath, strings.Fields(e))
		order = append(order, k)
	}
	p := adapterLearnPrompt(bin, helps, order)
	if printOnly || use == "" {
		fmt.Print(p)
		fmt.Fprintf(os.Stderr, "\n---\nGive the text above to any AI. Save its JSON reply to a file, then:\n  hloop adapter import reply.json\n  hloop adapter test %s\n", store.SafeName(orString(name, bin)))
		return nil
	}
	helper, err := adapter.Load(use, projDir)
	if err != nil {
		return err
	}
	if !yes {
		if !isTTY() {
			return fmt.Errorf("--use runs %s with its approval prompts off to read this help text; add --yes to allow", helper.Name)
		}
		fmt.Printf("This will start %s (%s) in an empty temporary folder to write the adapter.\n", helper.Name, helper.Bin)
		if helper.SkipsPermissions {
			fmt.Println("It runs with approval prompts disabled; the only input it sees is the help text from " + bin + ".")
		}
		if ans := strings.ToLower(prompt(bufio.NewReader(os.Stdin), "Continue? [y/N]", "n")); ans != "y" && ans != "yes" {
			return errSilent
		}
	}
	tmp, err := os.MkdirTemp("", "hloop-learn-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	fmt.Println("Asking", helper.Name, "to write the adapter...")
	res := helper.Run(context.Background(), adapter.RunOpts{Dir: tmp, Prompt: p, PathExtra: pathExtra, Timeout: 10 * time.Minute})
	if res.Class != "ok" {
		return fmt.Errorf("%s did not finish (%s); use --print-prompt and try another AI.\n%s", helper.Name, res.Class, lastN(res.Tail, 8))
	}
	a, err := parseAdapterJSON([]byte(res.Tail))
	if err != nil {
		return fmt.Errorf("%v\nLast output:\n%s", err, lastN(res.Tail, 12))
	}
	if name != "" {
		a.Name = store.SafeName(name)
	}
	if err := validateLearned(a, bin); err != nil {
		return err
	}
	return confirmAndSave(a, target, yes)
}

func orString(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func validateLearned(a adapter.Adapter, bin string) error {
	if a.Bin == "" {
		return fmt.Errorf("the AI's answer has no \"bin\"")
	}
	if a.Bin != bin {
		found := false
		for _, b := range a.Bins {
			if b == bin {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("the AI wrote bin %q, not %q; refusing to save an adapter for a different program", a.Bin, bin)
		}
	}
	return nil
}

// ---------- test ----------

func adapterTest(a adapter.Adapter, pathExtra []string, doContinue bool, timeout time.Duration, yes bool) error {
	binPath, err := a.Resolve(pathExtra)
	if err != nil {
		fmt.Printf("FAIL  %s is not installed (looked for %s on PATH)\n", a.Name, strings.Join(append([]string{a.Bin}, a.Bins...), ", "))
		printAdapterHints(a)
		return errSilent
	}
	fmt.Printf("%s found at %s\n", a.Name, binPath)
	for _, w := range a.Warnings(binPath) {
		fmt.Println("warn ", w)
	}
	if doContinue && len(a.Continue) == 0 {
		return fmt.Errorf("%s has no \"continue\" command to test", a.Name)
	}
	if !yes {
		if !isTTY() {
			return fmt.Errorf("add --yes to run the test non-interactively")
		}
		fmt.Printf("\nThis runs %s once in a throwaway folder with a tiny task (it creates one file and prints a version).\n", a.Name)
		fmt.Println("It uses a little of your usage allowance" + map[bool]string{true: " and runs with approval prompts disabled", false: ""}[a.SkipsPermissions] + ".")
		if ans := strings.ToLower(prompt(bufio.NewReader(os.Stdin), "Continue? [y/N]", "n")); ans != "y" && ans != "yes" {
			return errSilent
		}
	}
	tmp, err := os.MkdirTemp("", "hloop-adapter-test-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if _, e := exec.LookPath("git"); e == nil {
		c := exec.Command("git", "init", "-q")
		c.Dir = tmp
		_ = c.Run()
	}
	exe := exePath()
	file := filepath.Join(tmp, "hloop-adapter-test.txt")
	prompt1 := "This is a connectivity test from the hloop supervisor. Do exactly this and nothing else:\n" +
		"1. Create a file named hloop-adapter-test.txt in the current directory containing exactly the text OK.\n" +
		"2. Run this shell command and include its output in your final answer: " + shellQuoteForPrompt(exe) + " version\n" +
		"3. Reply with the word DONE."
	env := []string{"HLOOP_BIN=" + exe, "HLOOP_AGENT=adapter-test", "HLOOP_ROLE=custom"}
	fmt.Println("\nRunning (this can take up to a minute)...")
	res := a.Run(context.Background(), adapter.RunOpts{Dir: tmp, Prompt: prompt1, PathExtra: pathExtra, Env: env, Timeout: timeout})
	ok := report(a, res, file, "OK", exe)
	if ok && doContinue {
		fmt.Println("\nResuming the previous session...")
		prompt2 := "Append a second line containing exactly the text AGAIN to hloop-adapter-test.txt, then reply DONE."
		res2 := a.Run(context.Background(), adapter.RunOpts{Dir: tmp, Prompt: prompt2, Continue: true, PathExtra: pathExtra, Env: env, Timeout: timeout})
		if res2.Class != "ok" {
			fmt.Printf("FAIL  resume run ended as %s\n%s\n", res2.Class, lastN(res2.Tail, 8))
			ok = false
		} else if b, _ := os.ReadFile(file); !strings.Contains(string(b), "AGAIN") {
			fmt.Println("FAIL  the resumed run did not update the file: the \"continue\" arguments probably start a fresh session or are wrong")
			ok = false
		} else {
			fmt.Println("ok    resume works")
		}
	}
	if !ok {
		return errSilent
	}
	fmt.Printf("\nPASS  %s works with hloop on this machine (%s).\n", a.Name, runtime.GOOS)
	return nil
}

func shellQuoteForPrompt(s string) string {
	if strings.ContainsAny(s, " \t") {
		return `"` + s + `"`
	}
	return s
}

func report(a adapter.Adapter, res adapter.Result, file, want, exe string) bool {
	switch res.Class {
	case "ok":
	case "missing":
		fmt.Printf("FAIL  could not start %s: %v\n", a.Bin, res.Err)
		printAdapterHints(a)
		return false
	case "rate_limited":
		fmt.Printf("FAIL  the harness reports a usage limit; try later.\n%s\n", lastN(res.Tail, 6))
		return false
	case "timeout":
		fmt.Printf("FAIL  no answer within the timeout. It may be waiting for an approval prompt or a login.\n%s\n", lastN(res.Tail, 8))
		printAdapterHints(a)
		return false
	default:
		fmt.Printf("FAIL  exit code %d.\n%s\n", res.ExitCode, lastN(res.Tail, 12))
		fmt.Println("Common causes: not signed in, a wrong flag, or an unattended mode that needs an extra flag. Compare with `" + a.Bin + " --help`.")
		printAdapterHints(a)
		return false
	}
	fmt.Printf("ok    the harness started and exited cleanly in %s\n", res.Duration.Round(time.Second))
	b, err := os.ReadFile(file)
	if err != nil || strings.TrimSpace(string(b)) != want {
		fmt.Println("FAIL  it did not create hloop-adapter-test.txt with the expected content: file edits are probably blocked (read-only sandbox or approval needed)")
		fmt.Println("      what it said:\n" + lastN(res.Tail, 8))
		return false
	}
	fmt.Println("ok    it can create files in the working directory")
	if strings.Contains(res.Tail, "hloop ") {
		fmt.Println("ok    it can run shell commands (needed to report back with `hloop task done`)")
	} else {
		fmt.Println("warn  could not confirm it ran the hloop command. If it cannot run shell commands it cannot report progress, and the loop will stop with a 'no visible progress' blocker. Output:\n" + lastN(res.Tail, 8))
	}
	return true
}
