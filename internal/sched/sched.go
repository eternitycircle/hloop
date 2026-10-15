// Package sched installs OS-level schedules so the loop resumes on its own:
// cron on Linux, launchd on macOS, Task Scheduler on Windows. It also installs
// the phone listener as a background job started at login/boot.
package sched

import (
	"bytes"
	"errors"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Spec describes one scheduled job.
type Spec struct {
	Kind       string // "run" (periodic project run) or "listen" (phone listener)
	Name       string // project registry name (ignored for listen)
	Root       string // project directory (run only)
	Exe        string // absolute path to the hloop binary
	EveryHours int    // run only; 0 means 8
	AtBoot     bool   // also start at boot/login (run only; listen always does)
}

func (s Spec) label() string {
	if s.Kind == "listen" {
		return "hloop-listener"
	}
	return "hloop-" + s.Name
}

func (s Spec) hours() int {
	if s.EveryHours <= 0 {
		return 8
	}
	return s.EveryHours
}

func (s Spec) argv() []string {
	if s.Kind == "listen" {
		return []string{s.Exe, "notify", "listen"}
	}
	return []string{s.Exe, "run", "-C", s.Root}
}

// ---------- cron (Linux and other Unix) ----------

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func cronEscape(s string) string { return strings.ReplaceAll(s, "%", `\%`) }

func (s Spec) cronCommand() string {
	var parts []string
	for _, a := range s.argv() {
		parts = append(parts, shQuote(a))
	}
	return cronEscape(strings.Join(parts, " "))
}

func marker(s Spec) string { return "# hloop:" + s.label() }

// CronLines returns the crontab lines for a spec.
func CronLines(s Spec) []string {
	var out []string
	if s.Kind == "listen" {
		return []string{"@reboot " + s.cronCommand() + " " + marker(s)}
	}
	out = append(out, fmt.Sprintf("0 */%d * * * %s %s", s.hours(), s.cronCommand(), marker(s)))
	if s.AtBoot {
		out = append(out, "@reboot "+s.cronCommand()+" "+marker(s))
	}
	return out
}

// RemoveFromCrontab strips every line belonging to a label.
func RemoveFromCrontab(existing, label string) string {
	var keep []string
	m := "# hloop:" + label
	for _, l := range strings.Split(strings.TrimRight(existing, "\n"), "\n") {
		if strings.HasSuffix(strings.TrimSpace(l), m) {
			continue
		}
		keep = append(keep, l)
	}
	out := strings.Join(keep, "\n")
	if strings.TrimSpace(out) == "" {
		return ""
	}
	return out + "\n"
}

// MergeCrontab replaces any existing entries for the spec with fresh ones.
func MergeCrontab(existing string, s Spec) string {
	base := RemoveFromCrontab(existing, s.label())
	return base + strings.Join(CronLines(s), "\n") + "\n"
}

func readCrontab() (string, error) {
	cmd := exec.Command("crontab", "-l")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if _, lookErr := exec.LookPath("crontab"); lookErr != nil {
			return "", errors.New("cron is not installed (Debian/Ubuntu: sudo apt install cron; Fedora: sudo dnf install cronie), then re-run")
		}
		if strings.Contains(strings.ToLower(errb.String()), "no crontab") {
			return "", nil
		}
		return "", fmt.Errorf("crontab -l: %v: %s", err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

func writeCrontab(content string) error {
	cmd := exec.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(content)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("crontab: %v: %s", err, strings.TrimSpace(errb.String()))
	}
	return nil
}

// ---------- launchd (macOS) ----------

func plistPath(s Spec) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", "com.hloop."+strings.TrimPrefix(s.label(), "hloop-")+".plist")
}

// Plist renders the launchd property list for a spec.
func Plist(s Spec) string {
	var args strings.Builder
	for _, a := range s.argv() {
		fmt.Fprintf(&args, "    <string>%s</string>\n", html.EscapeString(a))
	}
	home, _ := os.UserHomeDir()
	logp := filepath.Join(home, "Library", "Logs", s.label()+".log")
	extra := ""
	if s.Kind == "listen" {
		extra = "  <key>KeepAlive</key>\n  <true/>\n"
	} else {
		extra = fmt.Sprintf("  <key>StartInterval</key>\n  <integer>%d</integer>\n", s.hours()*3600)
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.hloop.%s</string>
  <key>ProgramArguments</key>
  <array>
%s  </array>
%s  <key>RunAtLoad</key>
  <true/>
  <key>StandardOutPath</key>
  <string>%s</string>
  <key>StandardErrorPath</key>
  <string>%s</string>
</dict>
</plist>
`, html.EscapeString(strings.TrimPrefix(s.label(), "hloop-")), args.String(), extra, html.EscapeString(logp), html.EscapeString(logp))
}

func launchctl(args ...string) error {
	out, err := exec.Command("launchctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ---------- Task Scheduler (Windows) ----------

func winQuote(a string) string { return `"` + strings.ReplaceAll(a, `"`, `\"`) + `"` }

// SchtasksArgs returns the schtasks arguments that create the primary task.
func SchtasksArgs(s Spec) [][]string {
	var parts []string
	for _, a := range s.argv() {
		parts = append(parts, winQuote(a))
	}
	tr := strings.Join(parts, " ")
	if s.Kind == "listen" {
		return [][]string{{"/Create", "/F", "/SC", "ONLOGON", "/TN", s.label(), "/TR", tr}}
	}
	cmds := [][]string{{"/Create", "/F", "/SC", "HOURLY", "/MO", fmt.Sprint(s.hours()), "/TN", s.label(), "/TR", tr}}
	if s.AtBoot {
		cmds = append(cmds, []string{"/Create", "/F", "/SC", "ONLOGON", "/TN", s.label() + "-logon", "/TR", tr})
	}
	return cmds
}

func schtasks(args ...string) error {
	out, err := exec.Command("schtasks", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("schtasks %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ---------- public API ----------

// Install creates or replaces the schedule and returns a human description.
func Install(s Spec) (string, error) {
	if s.Exe == "" {
		return "", errors.New("cannot determine the hloop executable path")
	}
	for _, v := range []string{s.Exe, s.Root, s.Name} {
		if strings.ContainsAny(v, "\n\r\x00") {
			return "", errors.New("refusing to schedule: a path or name contains a control character")
		}
	}
	switch runtime.GOOS {
	case "windows":
		for _, a := range SchtasksArgs(s) {
			if err := schtasks(a...); err != nil {
				return "", err
			}
		}
		return "Task Scheduler task " + s.label(), nil
	case "darwin":
		path := plistPath(s)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, []byte(Plist(s)), 0o644); err != nil {
			return "", err
		}
		uid := fmt.Sprint(os.Getuid())
		_ = launchctl("bootout", "gui/"+uid, path) // ignore: may not be loaded
		if err := launchctl("bootstrap", "gui/"+uid, path); err != nil {
			return "", err
		}
		return "launchd agent " + path, nil
	default:
		cur, err := readCrontab()
		if err != nil {
			return "", err
		}
		if err := writeCrontab(MergeCrontab(cur, s)); err != nil {
			return "", err
		}
		return "crontab entry (" + strings.Join(CronLines(s), " | ") + ")", nil
	}
}

// Remove deletes the schedule for a spec (only Kind, Name matter).
func Remove(s Spec) error {
	switch runtime.GOOS {
	case "windows":
		_ = schtasks("/Delete", "/F", "/TN", s.label()+"-logon")
		return schtasks("/Delete", "/F", "/TN", s.label())
	case "darwin":
		path := plistPath(s)
		_ = launchctl("bootout", "gui/"+fmt.Sprint(os.Getuid()), path)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	default:
		cur, err := readCrontab()
		if err != nil {
			return err
		}
		return writeCrontab(RemoveFromCrontab(cur, s.label()))
	}
}

// Installed reports whether a schedule exists.
func Installed(s Spec) bool {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("schtasks", "/Query", "/TN", s.label()).Run() == nil
	case "darwin":
		_, err := os.Stat(plistPath(s))
		return err == nil
	default:
		cur, err := readCrontab()
		return err == nil && strings.Contains(cur, "# hloop:"+s.label())
	}
}
