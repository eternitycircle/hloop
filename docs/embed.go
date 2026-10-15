// Package docs embeds hloop's reference documentation so `hloop docs` works
// offline, from the binary, and always matches the installed version.
package docs

import (
	"embed"
	"sort"
	"strings"
)

//go:embed reference/*.md
var files embed.FS

// Topic is one reference page.
type Topic struct {
	Name    string // file name without .md and without the numeric prefix
	File    string
	Title   string // first heading
	Summary string // first paragraph line after the heading
	Body    string
}

// Topics returns the reference pages in reading order.
func Topics() []Topic {
	ents, _ := files.ReadDir("reference")
	var out []Topic
	for _, e := range ents {
		b, err := files.ReadFile("reference/" + e.Name())
		if err != nil {
			continue
		}
		body := string(b)
		name := strings.TrimSuffix(e.Name(), ".md")
		if i := strings.IndexByte(name, '-'); i > 0 && strings.Trim(name[:i], "0123456789") == "" {
			name = name[i+1:]
		}
		t := Topic{Name: name, File: e.Name(), Body: body}
		for _, line := range strings.Split(body, "\n") {
			line = strings.TrimSpace(line)
			switch {
			case t.Title == "" && strings.HasPrefix(line, "# "):
				t.Title = strings.TrimPrefix(line, "# ")
			case t.Title != "" && t.Summary == "" && line != "" && !strings.HasPrefix(line, "#"):
				t.Summary = line
			}
			if t.Summary != "" {
				break
			}
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].File < out[j].File })
	return out
}

// Find returns the topic with the given name (or a unique prefix of it).
func Find(name string) (Topic, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	var hit []Topic
	for _, t := range Topics() {
		if t.Name == name {
			return t, true
		}
		if strings.HasPrefix(t.Name, name) {
			hit = append(hit, t)
		}
	}
	if len(hit) == 1 {
		return hit[0], true
	}
	return Topic{}, false
}
