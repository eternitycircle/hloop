package store

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// Trust: a project's .loop/ folder decides which programs hloop starts and
// with what arguments (config.json, adapters/, prompts/). That folder can
// arrive from a git clone, or be edited by an agent. hloop therefore records a
// fingerprint of those files when a human sets the project up or changes it
// through a command, and refuses to start any harness if the files differ from
// what a human last approved. `hloop trust` re-approves after a review.

// GuardrailsFile holds the project's approval tiers and stop conditions.
const GuardrailsFile = "GUARDRAILS.md"

func trustPath() string { return filepath.Join(ConfigHome(), "trust.json") }

func trustKey(root string) string {
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	if a, err := filepath.Abs(root); err == nil {
		root = a
	}
	return filepath.Clean(root)
}

// FingerprintParts hashes each file in .loop/ that controls what gets
// executed or what the agents are told to do, keyed by its path in .loop/.
func (p Project) FingerprintParts() (map[string]string, error) {
	var files []string
	add := func(rel string) { files = append(files, rel) }
	add(ConfigFile)
	// GUARDRAILS.md tells every agent what it may not do; an agent loosening it
	// must not take effect without a human seeing it.
	if _, err := os.Stat(p.Path(GuardrailsFile)); err == nil {
		add(GuardrailsFile)
	}
	for _, sub := range []string{"adapters", "prompts"} {
		_ = filepath.WalkDir(p.Path(sub), func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				if rel, e := filepath.Rel(p.Dir(), path); e == nil {
					add(filepath.ToSlash(rel))
				}
			}
			return nil
		})
	}
	out := map[string]string{}
	for _, rel := range files {
		b, err := os.ReadFile(p.Path(filepath.FromSlash(rel)))
		if err != nil {
			if rel == ConfigFile {
				return nil, err
			}
			continue
		}
		sum := sha256.Sum256(b)
		out[rel] = hex.EncodeToString(sum[:])
	}
	return out, nil
}

// Fingerprint combines the same files into one value. The formula is the one
// 0.3.0 used, so projects approved by 0.3.0 stay approved after an upgrade.
func (p Project) Fingerprint() (string, error) {
	parts, err := p.FingerprintParts()
	if err != nil {
		return "", err
	}
	var names []string
	for k := range parts {
		names = append(names, k)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, rel := range names {
		b, err := os.ReadFile(p.Path(filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		h.Write([]byte(rel))
		h.Write([]byte{0})
		h.Write(b)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ChangedParts lists the fingerprinted files that differ between two
// FingerprintParts results (added, removed or modified).
func ChangedParts(before, after map[string]string) []string {
	var out []string
	for k, v := range after {
		if before[k] != v {
			out = append(out, k)
		}
	}
	for k := range before {
		if _, ok := after[k]; !ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func loadTrust() map[string]string {
	m := map[string]string{}
	_ = readJSON(trustPath(), &m)
	return m
}

// IsTrusted reports whether the project's executable configuration is exactly
// what a human last approved.
func (p Project) IsTrusted() bool {
	fp, err := p.Fingerprint()
	if err != nil {
		return false
	}
	return loadTrust()[trustKey(p.Root)] == fp
}

// HasTrustRecord reports whether the project was ever approved on this machine.
func (p Project) HasTrustRecord() bool {
	_, ok := loadTrust()[trustKey(p.Root)]
	return ok
}

// Trust records the current fingerprint as approved.
func (p Project) Trust() error {
	fp, err := p.Fingerprint()
	if err != nil {
		return err
	}
	return withFileLock(trustPath()+".lock", func() error {
		m := loadTrust()
		m[trustKey(p.Root)] = fp
		return writeJSON(trustPath(), m, 0o600)
	})
}

// Untrust removes the approval (used when a project is unregistered).
func (p Project) Untrust() error {
	return withFileLock(trustPath()+".lock", func() error {
		m := loadTrust()
		delete(m, trustKey(p.Root))
		return writeJSON(trustPath(), m, 0o600)
	})
}

// ErrUntrusted explains how to get out of the untrusted state.
type ErrUntrusted struct {
	Root    string
	Changed bool // true if it was approved before and has changed since
}

func (e ErrUntrusted) Error() string {
	why := "has not been approved on this machine"
	if e.Changed {
		why = "changed since you last approved it (an agent, a git pull or a manual edit modified config.json, GUARDRAILS.md, adapters/ or prompts/)"
	}
	return "the loop configuration in " + filepath.Join(e.Root, DirName) + " " + why + ".\n" +
		"hloop will not start any program from it until you review it:\n" +
		"  hloop trust --show     (what would run)\n" +
		"  hloop trust            (approve it)"
}

// RequireTrust returns ErrUntrusted unless the configuration is approved.
func (p Project) RequireTrust() error {
	if p.IsTrusted() {
		return nil
	}
	return ErrUntrusted{Root: p.Root, Changed: p.HasTrustRecord()}
}

// PreserveTrust is for commands that legitimately change the executable
// configuration. Call it before the change; call the returned function after.
// The new state is approved only if the old state was already approved, so a
// tampered project is never blessed by an unrelated command.
func (p Project) PreserveTrust() func() {
	was := p.IsTrusted()
	return func() {
		if was {
			_ = p.Trust()
		}
	}
}
