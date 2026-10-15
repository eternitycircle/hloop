package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// State is owned by the runner. Humans change it only via pause/resume.
type State struct {
	Status              string     `json:"status"` // idle | running | error | rate_limited | blocked | done
	Paused              bool       `json:"paused"`
	UpdatedAt           time.Time  `json:"updated_at"`
	LastRunStart        *time.Time `json:"last_run_start,omitempty"`
	LastRunEnd          *time.Time `json:"last_run_end,omitempty"`
	RetryAfter          *time.Time `json:"retry_after,omitempty"`
	LastOutcome         string     `json:"last_outcome,omitempty"`
	LastMessage         string     `json:"last_message,omitempty"`
	TotalTurns          int        `json:"total_turns"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	StalledTask         string     `json:"stalled_task,omitempty"`
	StalledTurns        int        `json:"stalled_turns,omitempty"`
	LastReviewedCommit  string     `json:"last_reviewed_commit,omitempty"` // legacy single value; per-agent values live in LastReviewed
	// LastReviewed records, per reviewer agent, the commit it last reviewed up
	// to, so each reviewer sees everything since its own previous review.
	LastReviewed map[string]string `json:"last_reviewed,omitempty"`
}

func (p Project) LoadState() State {
	var s State
	_ = readJSON(p.Path(StateFile), &s)
	if s.Status == "" {
		s.Status = "idle"
	}
	return s
}

func (p Project) SaveState(s State) error {
	s.UpdatedAt = time.Now().UTC()
	return writeJSON(p.Path(StateFile), s, 0o644)
}

// UpdateState loads, mutates and saves state under a lock.
func (p Project) UpdateState(fn func(*State)) error {
	return withFileLock(p.Path("state.lock"), func() error {
		s := p.LoadState()
		fn(&s)
		return p.SaveState(s)
	})
}

// ---------- run lock with heartbeat (no OS-specific process checks) ----------

var ErrBusy = errors.New("another hloop run is already active for this project")

const lockStaleAfter = 3 * time.Minute

type RunLock struct {
	path string
	stop chan struct{}
}

func (p Project) runLockPath() string { return p.Path("run.lock") }

// RunActive reports whether a runner currently holds a fresh lock.
func (p Project) RunActive() bool {
	st, err := os.Stat(p.runLockPath())
	return err == nil && time.Since(st.ModTime()) < lockStaleAfter
}

// AcquireRun takes the exclusive run lock and keeps it fresh with a heartbeat.
func (p Project) AcquireRun() (*RunLock, error) {
	path := p.runLockPath()
	if err := os.MkdirAll(p.Dir(), 0o755); err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			host, _ := os.Hostname()
			fmt.Fprintf(f, "pid=%d host=%s started=%s\n", os.Getpid(), host, time.Now().UTC().Format(time.RFC3339))
			f.Close()
			l := &RunLock{path: path, stop: make(chan struct{})}
			go func() {
				t := time.NewTicker(30 * time.Second)
				defer t.Stop()
				for {
					select {
					case <-l.stop:
						return
					case now := <-t.C:
						_ = os.Chtimes(path, now, now)
					}
				}
			}()
			return l, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		if st, e := os.Stat(path); e == nil && time.Since(st.ModTime()) >= lockStaleAfter {
			os.Remove(path) // previous runner died without cleaning up
			continue
		}
		return nil, ErrBusy
	}
	return nil, ErrBusy
}

func (l *RunLock) Release() {
	close(l.stop)
	os.Remove(l.path)
}

// ---------- inbox: one-shot messages from the CLI or phone ----------

type InboxItem struct {
	ID   string    `json:"id,omitempty"`
	Time time.Time `json:"time"`
	Kind string    `json:"kind"` // note
	Text string    `json:"text"`
	From string    `json:"from,omitempty"`
	// Held notes came from a remote channel while remote_tasks is "approve";
	// they are not shown to any agent until a human approves them.
	Held bool `json:"held,omitempty"`
}

func (p Project) readInbox() []InboxItem {
	var out []InboxItem
	f, err := os.Open(p.Path("inbox.jsonl"))
	if err != nil {
		return nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var it InboxItem
		if json.Unmarshal(sc.Bytes(), &it) == nil && it.Text != "" {
			out = append(out, it)
		}
	}
	return out
}

func (p Project) writeInbox(items []InboxItem) error {
	if len(items) == 0 {
		if err := os.Remove(p.Path("inbox.jsonl")); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	var buf []byte
	for _, it := range items {
		b, _ := json.Marshal(it)
		buf = append(append(buf, b...), '\n')
	}
	return writeAtomic(p.Path("inbox.jsonl"), buf, 0o600)
}

func (p Project) archiveInbox(items ...InboxItem) error {
	if len(items) == 0 {
		return nil
	}
	a, err := os.OpenFile(p.Path("inbox.archive.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer a.Close()
	for _, it := range items {
		b, _ := json.Marshal(it)
		if _, err := a.Write(append(b, '\n')); err != nil {
			return err
		}
	}
	return nil
}

// AddInbox queues a one-shot note for the next agent turn.
func (p Project) AddInbox(kind, text, from string) error { return p.addInbox(kind, text, from, false) }

// AddHeldInbox queues a note that stays invisible to agents until approved.
func (p Project) AddHeldInbox(kind, text, from string) (InboxItem, error) {
	var out InboxItem
	err := p.addInboxItem(kind, text, from, true, &out)
	return out, err
}

func (p Project) addInbox(kind, text, from string, held bool) error {
	return p.addInboxItem(kind, text, from, held, nil)
}

func (p Project) addInboxItem(kind, text, from string, held bool, out *InboxItem) error {
	return withFileLock(p.Path("inbox.lock"), func() error {
		items := p.readInbox()
		n := len(items) + 1
		if raw, err := os.ReadFile(p.Path("inbox.archive.jsonl")); err == nil {
			n += strings.Count(string(raw), "\n")
		}
		it := InboxItem{ID: fmt.Sprintf("N%d", n), Time: time.Now().UTC(), Kind: kind, Text: strings.TrimSpace(text), From: from, Held: held}
		if out != nil {
			*out = it
		}
		return p.writeInbox(append(items, it))
	})
}

// PeekInbox returns pending items an agent may see, without consuming them.
func (p Project) PeekInbox() []InboxItem {
	var out []InboxItem
	for _, it := range p.readInbox() {
		if !it.Held {
			out = append(out, it)
		}
	}
	return out
}

// HeldInbox returns notes waiting for human approval.
func (p Project) HeldInbox() []InboxItem {
	var out []InboxItem
	for _, it := range p.readInbox() {
		if it.Held {
			out = append(out, it)
		}
	}
	return out
}

// DrainInbox returns all visible pending items and moves them to the archive.
// Held items stay where they are.
func (p Project) DrainInbox() ([]InboxItem, error) {
	var items []InboxItem
	err := withFileLock(p.Path("inbox.lock"), func() error {
		var keep []InboxItem
		for _, it := range p.readInbox() {
			if it.Held {
				keep = append(keep, it)
			} else {
				items = append(items, it)
			}
		}
		if len(items) == 0 {
			return nil
		}
		if err := p.archiveInbox(items...); err != nil {
			return err
		}
		return p.writeInbox(keep)
	})
	return items, err
}

// ResolveHeld approves (the note becomes visible to the next turn) or discards
// a held note.
func (p Project) ResolveHeld(id string, approve bool) (InboxItem, error) {
	var got InboxItem
	id = NormID("N", id)
	err := withFileLock(p.Path("inbox.lock"), func() error {
		items := p.readInbox()
		for i := range items {
			if items[i].ID == id && items[i].Held {
				got = items[i]
				if approve {
					items[i].Held = false
					got.Held = false
					return p.writeInbox(items)
				}
				items = append(items[:i], items[i+1:]...)
				got.Kind = "discarded-" + got.Kind
				if err := p.archiveInbox(got); err != nil {
					return err
				}
				return p.writeInbox(items)
			}
		}
		return fmt.Errorf("no held note %s", id)
	})
	return got, err
}

// ---------- status ----------

// EffectiveStatus computes the status a human should see right now.
func EffectiveStatus(p Project, s State, l Ledger, now time.Time) string {
	switch {
	case s.Paused:
		return "paused"
	case p.RunActive():
		return "running"
	case len(l.OpenBlockers()) > 0:
		return "blocked"
	case s.RetryAfter != nil && s.RetryAfter.After(now):
		return "rate_limited"
	case l.Remaining() == 0:
		return "done"
	case s.Status == "error":
		return "error"
	}
	return "idle"
}

// StatusText is the shared summary used by `hloop status` and the phone bot.
func StatusText(p Project, name string, now time.Time) string {
	l, _ := LoadLedger(p)
	s := p.LoadState()
	eff := EffectiveStatus(p, s, l, now)
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s\n", name, strings.ToUpper(eff))
	if c := l.Current(); c != nil {
		fmt.Fprintf(&b, "Current: %s %s\n", c.ID, c.Title)
	} else if n := l.NextPending(); n != nil {
		fmt.Fprintf(&b, "Next: %s %s\n", n.ID, n.Title)
	}
	fmt.Fprintf(&b, "Tasks: %d done, %d in progress, %d pending", l.Count(TaskDone), l.Count(TaskInProgress), l.Count(TaskPending))
	if np := l.Count(TaskProposed); np > 0 {
		fmt.Fprintf(&b, ", %d awaiting approval", np)
	}
	b.WriteString("\n")
	for _, x := range l.OpenBlockers() {
		fmt.Fprintf(&b, "Blocker %s: %s\n", x.ID, x.Text)
	}
	if od := l.OpenDecisions(); len(od) > 0 {
		fmt.Fprintf(&b, "Open decisions: %d\n", len(od))
	}
	if pl := l.LessonsWith(LessonProposed); len(pl) > 0 {
		fmt.Fprintf(&b, "Lessons waiting for approval: %d\n", len(pl))
	}
	if oi := l.OpenIdeas(); len(oi) > 0 {
		fmt.Fprintf(&b, "Ideas parked: %d\n", len(oi))
	}
	if c := l.Current(); c != nil && c.GateFails > 0 {
		fmt.Fprintf(&b, "%s: hloop rejected %d finish(es) so far\n", c.ID, c.GateFails)
	}
	if s.RetryAfter != nil && s.RetryAfter.After(now) {
		fmt.Fprintf(&b, "Rate limited; retry after %s\n", s.RetryAfter.Local().Format("Jan 2 15:04"))
	}
	if s.LastRunEnd != nil {
		fmt.Fprintf(&b, "Last run: %s (%s)", s.LastRunEnd.Local().Format("Jan 2 15:04"), s.LastOutcome)
		if s.LastMessage != "" {
			fmt.Fprintf(&b, ", %s", s.LastMessage)
		}
		b.WriteString("\n")
	}
	if n := len(p.PeekInbox()); n > 0 {
		fmt.Fprintf(&b, "Inbox: %d note(s) waiting for the next turn\n", n)
	}
	return strings.TrimRight(b.String(), "\n")
}

// ReviewedUpTo returns the commit the named reviewer last reviewed to.
func (s State) ReviewedUpTo(agent string) string {
	if h := s.LastReviewed[agent]; h != "" {
		return h
	}
	return ""
}
