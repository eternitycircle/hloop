package docs

import "testing"

func TestEveryPageHasATitleAndSummary(t *testing.T) {
	ts := Topics()
	if len(ts) < 10 {
		t.Fatalf("only %d pages", len(ts))
	}
	seen := map[string]bool{}
	for _, p := range ts {
		if p.Title == "" || p.Summary == "" {
			t.Errorf("%s: title %q summary %q", p.File, p.Title, p.Summary)
		}
		if seen[p.Name] {
			t.Errorf("duplicate topic %s", p.Name)
		}
		seen[p.Name] = true
	}
	if _, ok := Find("over"); !ok {
		t.Error("prefix lookup")
	}
}
