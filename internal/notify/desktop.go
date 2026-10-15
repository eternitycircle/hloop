package notify

import (
	"os/exec"
	"runtime"
	"strings"
)

// Desktop shows a best-effort local notification. It is silent when the
// platform has no notifier or no desktop session (for example under cron).
func Desktop(title, body string) {
	title, body = cleanOneLine(title, 120), cleanOneLine(body, 240)
	switch runtime.GOOS {
	case "linux":
		if p, err := exec.LookPath("notify-send"); err == nil {
			_ = exec.Command(p, title, body).Run()
		}
	case "darwin":
		if p, err := exec.LookPath("osascript"); err == nil {
			script := `display notification "` + escAS(body) + `" with title "` + escAS(title) + `"`
			_ = exec.Command(p, "-e", script).Run()
		}
	}
}

func escAS(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`)
}

func cleanOneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		s = s[:max-3] + "..."
	}
	return s
}
