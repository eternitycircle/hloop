package store

import (
	"os"
	"strings"
	"testing"
)

func TestSafeNameAndValidName(t *testing.T) {
	for in, want := range map[string]string{"..": "project", "../../etc": "etc", ".hidden": "hidden", "My App!": "my-app", "a..b": "a.b", "": "project"} {
		if got := SafeName(in); got != want {
			t.Errorf("SafeName(%q) = %q, want %q", in, got, want)
		}
		if !ValidName(SafeName(in)) {
			t.Errorf("SafeName(%q) = %q is not a ValidName", in, SafeName(in))
		}
	}
	for _, bad := range []string{"", "..", "../x", "a/b", `a\b`, ".x", "Upper", strings.Repeat("a", 60), "a b"} {
		if ValidName(bad) {
			t.Errorf("ValidName(%q) should be false", bad)
		}
	}
	for _, bad := range []string{"/etc/passwd", "../x", "prompts/../../x", `..\x`, `C:\x`, `\\server\share`} {
		if ValidPromptPath(bad) {
			t.Errorf("ValidPromptPath(%q) should be false", bad)
		}
	}
	for _, good := range []string{"", "prompts/builder.md", "prompts/sub/x.md"} {
		if !ValidPromptPath(good) {
			t.Errorf("ValidPromptPath(%q) should be true", good)
		}
	}
}

func TestTrustFingerprintCoversConfigAdaptersAndPrompts(t *testing.T) {
	t.Setenv("HLOOP_HOME", t.TempDir())
	p := Project{Root: t.TempDir()}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(p.Path("adapters"), 0o755))
	must(os.MkdirAll(p.Path("prompts"), 0o755))
	must(os.WriteFile(p.Path("config.json"), []byte(`{"a":1}`), 0o644))
	must(os.WriteFile(p.Path("prompts", "builder.md"), []byte("do work"), 0o644))
	if p.IsTrusted() || p.HasTrustRecord() {
		t.Fatal("a new project must start untrusted")
	}
	if _, ok := p.RequireTrust().(ErrUntrusted); !ok {
		t.Fatal("RequireTrust should return ErrUntrusted")
	}
	must(p.Trust())
	if !p.IsTrusted() || p.RequireTrust() != nil {
		t.Fatal("should be trusted after Trust()")
	}
	// state files must not affect trust
	must(os.WriteFile(p.Path("ledger.json"), []byte(`{}`), 0o644))
	must(os.WriteFile(p.Path("PROGRESS.md"), []byte("x"), 0o644))
	if !p.IsTrusted() {
		t.Fatal("ledger and generated views must not change the fingerprint")
	}
	for name, mutate := range map[string]func(){
		"config":      func() { os.WriteFile(p.Path("config.json"), []byte(`{"a":2}`), 0o644) },
		"prompt":      func() { os.WriteFile(p.Path("prompts", "builder.md"), []byte("do evil"), 0o644) },
		"new prompt":  func() { os.WriteFile(p.Path("prompts", "x.md"), []byte("x"), 0o644) },
		"new adapter": func() { os.WriteFile(p.Path("adapters", "e.json"), []byte(`{}`), 0o644) },
	} {
		must(p.Trust())
		mutate()
		if p.IsTrusted() {
			t.Errorf("changing %s must revoke trust", name)
		}
		if e, ok := p.RequireTrust().(ErrUntrusted); !ok || !e.Changed {
			t.Errorf("%s: error should say the config changed", name)
		}
		_ = os.Remove(p.Path("prompts", "x.md"))
		_ = os.Remove(p.Path("adapters", "e.json"))
		must(os.WriteFile(p.Path("config.json"), []byte(`{"a":1}`), 0o644))
		must(os.WriteFile(p.Path("prompts", "builder.md"), []byte("do work"), 0o644))
	}
	// PreserveTrust never blesses a tampered project
	must(p.Trust())
	done := p.PreserveTrust()
	must(os.WriteFile(p.Path("config.json"), []byte(`{"a":3}`), 0o644))
	done()
	if !p.IsTrusted() {
		t.Fatal("a command run on a trusted project keeps it trusted")
	}
	must(os.WriteFile(p.Path("config.json"), []byte(`{"a":4}`), 0o644)) // tamper
	done2 := p.PreserveTrust()
	must(os.WriteFile(p.Path("config.json"), []byte(`{"a":5}`), 0o644))
	done2()
	if p.IsTrusted() {
		t.Fatal("PreserveTrust must not approve a project that was already untrusted")
	}
}

func TestHeldInboxIsInvisibleUntilApproved(t *testing.T) {
	p := Project{Root: t.TempDir()}
	_ = os.MkdirAll(p.Dir(), 0o755)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(p.AddInbox("note", "from me", "cli"))
	h, err := p.AddHeldInbox("note", "from phone", "phone")
	must(err)
	if got := p.PeekInbox(); len(got) != 1 || got[0].Text != "from me" {
		t.Fatalf("agent view: %v", got)
	}
	drained, err := p.DrainInbox()
	must(err)
	if len(drained) != 1 || len(p.HeldInbox()) != 1 {
		t.Fatalf("draining must leave held notes in place: drained=%v held=%v", drained, p.HeldInbox())
	}
	if _, err := p.ResolveHeld("N99", true); err == nil {
		t.Fatal("unknown id must fail")
	}
	_, err = p.ResolveHeld(h.ID, true)
	must(err)
	if got := p.PeekInbox(); len(got) != 1 || got[0].Text != "from phone" {
		t.Fatalf("approved note should now be visible: %v", got)
	}
}
