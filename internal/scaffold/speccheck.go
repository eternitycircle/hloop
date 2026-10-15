package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"hloop/internal/store"
)

// Spec check: a fast, deterministic lint of the project definition. It does
// not judge whether the plan is good; it catches the defects that make an
// unattended agent guess: work with no checkable finish line, criteria nobody
// can verify, vague wording, tasks that depend on an open decision, and specs
// that are missing or unfinished.

// Finding is one spec-check result.
type Finding struct {
	Level string // error | warn
	Where string // T5, T5/AC2, specs/x.md, project
	Text  string
}

func (f Finding) String() string { return fmt.Sprintf("%-5s %-14s %s", f.Level, f.Where, f.Text) }

// vagueWords are words that make a criterion impossible to verify as written.
var vagueWords = []string{
	"properly", "correctly", "as needed", "as appropriate", "appropriately", "etc", "and so on",
	"user-friendly", "user friendly", "intuitive", "seamless", "seamlessly", "robust", "nice", "better",
	"improve", "improved", "good", "clean", "cleaner", "fast", "faster", "efficient", "efficiently",
	"optimal", "optimize", "reasonable", "handle errors", "where possible", "if needed", "and more",
}

var decisionRef = regexp.MustCompile(`\bD\d+\b`)
var hasNumber = regexp.MustCompile(`\d`)

var vagueRes = func() []*regexp.Regexp {
	out := make([]*regexp.Regexp, len(vagueWords))
	for i, w := range vagueWords {
		out[i] = regexp.MustCompile(`[^a-z]` + regexp.QuoteMeta(w) + `[^a-z]`)
	}
	return out
}()

func vagueIn(s string) []string {
	low := " " + strings.ToLower(s) + " "
	var hit []string
	for i, w := range vagueWords {
		if vagueRes[i].MatchString(low) {
			// "fast"/"faster" with a number ("under 200 ms") is measurable
			if (w == "fast" || w == "faster" || w == "efficient") && hasNumber.MatchString(s) {
				continue
			}
			hit = append(hit, w)
		}
	}
	return hit
}

// specSections are the headings a spec document written by `hloop spec` has.
var specSections = []string{"## Problem", "## Out of scope", "## Test boundaries", "## Stop conditions"}

// SpecCheck lints the open tasks (or only the named one) and the spec files
// they point at.
func SpecCheck(p store.Project, cfg store.Config, onlyTask string) []Finding {
	l, _ := store.LoadLedger(p)
	var out []Finding
	add := func(level, where, format string, a ...any) {
		out = append(out, Finding{Level: level, Where: where, Text: fmt.Sprintf(format, a...)})
	}
	openDec := map[string]bool{}
	for _, d := range l.OpenDecisions() {
		openDec[d.ID] = true
	}
	specs := map[string]bool{}
	for _, t := range l.Tasks {
		if onlyTask != "" && t.ID != store.NormID("T", onlyTask) {
			continue
		}
		if t.Status != store.TaskPending && t.Status != store.TaskInProgress && t.Status != store.TaskProposed {
			continue
		}
		kind := t.EffKind()
		must := 0
		for _, c := range t.Criteria {
			where := t.ID + "/" + c.ID
			if c.Priority == store.PrioMust {
				must++
			}
			if strings.TrimSpace(c.Verify) == "" {
				add("warn", where, "no 'verify by' (write it as \"statement :: how it is verified\")")
			}
			if v := vagueIn(c.Text); len(v) > 0 {
				add("warn", where, "vague wording (%s): say what can be observed or measured", strings.Join(v, ", "))
			}
		}
		if must == 0 && (kind == store.KindCode || kind == store.KindConfig) {
			if strings.Contains(strings.ToLower(t.Detail), "acceptance") {
				add("warn", t.ID, "criteria are only in the free-text details; record them with `hloop task ac %s add` so hloop can hold the agent to them", t.ID)
			} else {
				add("warn", t.ID, "no Must acceptance criterion: the builder will have to invent its own finish line (hloop task ac %s add \"...\", or hloop spec --task %s)", t.ID, t.ID)
			}
		}
		if len(t.Criteria) > 12 {
			add("warn", t.ID, "%d criteria: probably more than one session of work; split the task", len(t.Criteria))
		}
		if len(t.Title) > 110 {
			add("warn", t.ID, "very long title: a task should be one milestone with a short name")
		}
		if v := vagueIn(t.Title); len(v) > 0 && must == 0 {
			add("warn", t.ID, "vague title (%s) and no criteria to pin it down", strings.Join(v, ", "))
		}
		text := t.Title + " " + t.Detail
		for _, c := range t.Criteria {
			text += " " + c.Text
		}
		warned := map[string]bool{}
		for _, ref := range decisionRef.FindAllString(text, -1) {
			if openDec[ref] && !warned[ref] {
				warned[ref] = true
				add("warn", t.ID, "depends on open decision %s: the agent will block on it; decide it first (hloop decision resolve %s)", ref, ref)
			}
		}
		if t.Spec != "" {
			specs[t.Spec] = true
			if _, err := os.Stat(p.Path(filepath.FromSlash(t.Spec))); err != nil {
				add("error", t.ID, "spec .loop/%s does not exist", t.Spec)
			}
		}
	}
	// spec documents: referenced ones, plus every spec when checking the whole project
	if onlyTask == "" {
		for _, s := range SpecDocs(p) {
			specs[s] = true
		}
	}
	var names []string
	for s := range specs {
		names = append(names, s)
	}
	sort.Strings(names)
	for _, s := range names {
		b, err := os.ReadFile(p.Path(filepath.FromSlash(s)))
		if err != nil {
			continue
		}
		body := string(b)
		if n := strings.Count(body, fillMarker); n > 0 {
			add("error", s, "%d unanswered [[FILL]] question(s)", n)
		}
		for _, h := range specSections {
			if !strings.Contains(body, h) {
				add("warn", s, "no %q section", strings.TrimPrefix(h, "## "))
			}
		}
	}
	if onlyTask == "" {
		if len(cfg.Gates) == 0 {
			add("warn", "project", "no gates: hloop accepts \"done\" on the agent's word (hloop gate add test -- <test command>)")
		}
		if _, err := os.Stat(p.Path(store.GuardrailsFile)); err != nil {
			add("warn", "project", "no GUARDRAILS.md: agents have no written approval boundaries (hloop init --upgrade creates one)")
		}
		if b, err := os.ReadFile(p.Path("CURRENT.md")); err == nil {
			if n := strings.Count(string(b), "\n"); n > 90 {
				add("warn", "CURRENT.md", "%d lines: it should be a short snapshot that is rewritten, not a diary", n)
			}
		}
	}
	return out
}

// SpecDocs lists the spec documents in .loop/specs/, relative to .loop/:
// specs/<name>/spec.md (current layout) and specs/<name>.md (0.4.0 layout).
func SpecDocs(p store.Project) []string {
	var out []string
	ents, err := os.ReadDir(p.Path("specs"))
	if err != nil {
		return nil
	}
	for _, e := range ents {
		n := e.Name()
		if strings.HasPrefix(n, "_") || strings.HasPrefix(n, ".") {
			continue
		}
		if e.IsDir() {
			if _, err := os.Stat(p.Path("specs", n, "spec.md")); err == nil {
				out = append(out, "specs/"+n+"/spec.md")
			}
		} else if strings.HasSuffix(n, ".md") {
			out = append(out, "specs/"+n)
		}
	}
	sort.Strings(out)
	return out
}

// CountErrors returns the number of error-level findings.
func CountErrors(fs []Finding) int {
	n := 0
	for _, f := range fs {
		if f.Level == "error" {
			n++
		}
	}
	return n
}
