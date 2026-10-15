package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"hloop/internal/store"
)

// Profile is a ready-made agent definition: who it is, when it runs, and the
// instructions it is given. Profiles ship with hloop and users can add their
// own as markdown files in $HLOOP_HOME/profiles/ (same format).
type Profile struct {
	Name        string
	Title       string
	Description string
	Role        string // reviewer | custom | planner
	Mode        string // write | loop-only
	Trigger     string
	EveryN      int
	Tier        string // fast | standard | critical (optional)
	Body        string // the agent's instructions
	Source      string // "built-in" or the file path
}

// Pipeline notes shown by the catalog for the built-in builder, which is
// configured at init and is not an installable profile.
const CoderNote = "coder: this is your builder agent, created by `hloop init`. Edit .loop/prompts/builder.md to change its style rules."

func parseProfile(raw, source string) (Profile, error) {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	if !strings.HasPrefix(raw, "---\n") {
		return Profile{}, fmt.Errorf("%s: missing front matter (--- ... ---)", source)
	}
	rest := raw[4:]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		return Profile{}, fmt.Errorf("%s: front matter is not closed with ---", source)
	}
	p := Profile{Source: source, Body: strings.TrimSpace(rest[end+5:]) + "\n"}
	for _, line := range strings.Split(rest[:end], "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "name":
			p.Name = v
		case "title":
			p.Title = v
		case "description":
			p.Description = v
		case "role":
			p.Role = v
		case "mode":
			p.Mode = v
		case "trigger":
			p.Trigger = v
		case "every_n":
			p.EveryN, _ = strconv.Atoi(v)
		case "tier":
			p.Tier = v
		}
	}
	switch {
	case !store.ValidName(p.Name):
		return p, fmt.Errorf("%s: invalid profile name %q", source, p.Name)
	case p.Body == "\n":
		return p, fmt.Errorf("%s: profile has no instructions", source)
	case p.Role != "reviewer" && p.Role != "custom" && p.Role != "planner":
		return p, fmt.Errorf("%s: role must be reviewer, planner or custom", source)
	case p.Mode != "write" && p.Mode != "loop-only" && p.Mode != "docs":
		return p, fmt.Errorf("%s: mode must be write, docs or loop-only", source)
	case !store.ValidTier(p.Tier):
		return p, fmt.Errorf("%s: tier must be fast, standard or critical", source)
	}
	switch p.Trigger {
	case "every_turn", "after_milestone", "every_n_turns", "on_run_end", "manual":
	default:
		return p, fmt.Errorf("%s: invalid trigger %q", source, p.Trigger)
	}
	if p.Trigger == "every_turn" {
		return p, fmt.Errorf("%s: only the builder runs every turn", source)
	}
	if p.EveryN > 0 && p.Trigger == "manual" {
		p.Trigger = "every_n_turns"
	}
	if p.Trigger == "every_n_turns" && p.EveryN <= 0 {
		return p, fmt.Errorf("%s: every_n_turns needs every_n", source)
	}
	if p.Title == "" {
		p.Title = p.Name
	}
	return p, nil
}

// UserProfileDir is where a user's own profiles live.
func UserProfileDir() string { return filepath.Join(store.ConfigHome(), "profiles") }

// Profiles returns the built-in profiles plus the user's own (which win on a
// name clash), sorted by name. Unreadable user profiles are reported, not fatal.
func Profiles() ([]Profile, []error) {
	byName := map[string]Profile{}
	var errs []error
	ents, _ := fs.ReadDir("profiles")
	for _, e := range ents {
		b, err := fs.ReadFile("profiles/" + e.Name())
		if err != nil {
			continue
		}
		p, err := parseProfile(string(b), "built-in "+e.Name())
		if err != nil {
			errs = append(errs, err)
			continue
		}
		p.Source = "built-in"
		byName[p.Name] = p
	}
	user, _ := os.ReadDir(UserProfileDir())
	for _, e := range user {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(UserProfileDir(), e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		p, err := parseProfile(string(b), path)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		byName[p.Name] = p
	}
	var out []Profile
	for _, p := range byName {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, errs
}

// FindProfile looks a profile up by name.
func FindProfile(name string) (Profile, bool) {
	ps, _ := Profiles()
	for _, p := range ps {
		if p.Name == name {
			return p, true
		}
	}
	return Profile{}, false
}
