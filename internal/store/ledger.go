package store

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Task statuses.
const (
	TaskProposed   = "proposed" // added remotely, waiting for human approval
	TaskPending    = "pending"
	TaskInProgress = "in_progress"
	TaskDone       = "done"
	TaskSkipped    = "skipped"
)

type Task struct {
	ID      string     `json:"id"`
	Title   string     `json:"title"`
	Detail  string     `json:"detail,omitempty"`
	Status  string     `json:"status"`
	Source  string     `json:"source,omitempty"` // cli | phone | agent | reviewer | init | adopt
	Commit  string     `json:"commit,omitempty"`
	Summary string     `json:"summary,omitempty"`
	Added   time.Time  `json:"added"`
	Done    *time.Time `json:"done,omitempty"`

	// Added in 0.4.0; all optional so 0.3.0 ledgers load unchanged.
	Kind        string      `json:"kind,omitempty"`         // code (default) | docs | config | research
	Spec        string      `json:"spec,omitempty"`         // spec document, relative to .loop/ (e.g. specs/csv-export.md)
	Criteria    []Criterion `json:"criteria,omitempty"`     // acceptance criteria
	StartCommit string      `json:"start_commit,omitempty"` // HEAD when the task was first started
	RedCommit   string      `json:"red_commit,omitempty"`   // tests-only commit recorded by the builder (TDD)
	GateFails   int         `json:"gate_fails,omitempty"`   // times hloop's gates rejected a "done"
	Verified    string      `json:"verified,omitempty"`     // how hloop verified the finish (gate summary)
	Report      string      `json:"report,omitempty"`       // completion report, relative to .loop/
	Checks      []Check     `json:"checks,omitempty"`       // hloop's latest verification of a "done"
	GuardEvents []Check     `json:"guard_events,omitempty"` // guard detectors that fired while this task was current
	Rejections  []Check     `json:"rejections,omitempty"`   // every failed check of every rejected finish (history)
}

// Check is one result of hloop verifying work itself.
type Check struct {
	Name   string    `json:"name"`
	Kind   string    `json:"kind"` // criteria | gate | tdd | guard
	OK     bool      `json:"ok"`
	Detail string    `json:"detail,omitempty"`
	At     time.Time `json:"at"`
}

// Task kinds. Only code tasks are held to the TDD red/green rule.
const (
	KindCode     = "code"
	KindDocs     = "docs"
	KindConfig   = "config"
	KindResearch = "research"
)

// ValidKind reports whether k is a known task kind ("" means code).
func ValidKind(k string) bool {
	switch k {
	case "", KindCode, KindDocs, KindConfig, KindResearch:
		return true
	}
	return false
}

// EffKind returns the task's kind, defaulting to code.
func (t Task) EffKind() string {
	if t.Kind == "" {
		return KindCode
	}
	return t.Kind
}

// Criterion priorities (MoSCoW). Only Must blocks completion.
const (
	PrioMust   = "must"
	PrioShould = "should"
	PrioCould  = "could"
)

// Criterion statuses.
const (
	CritOpen = "open"
	CritPass = "pass"
	CritFail = "fail"
)

// Criterion is one independently verifiable acceptance criterion.
type Criterion struct {
	ID       string `json:"id"`               // AC1, AC2, ... (per task)
	Text     string `json:"text"`             // the verifiable statement
	Priority string `json:"priority"`         // must | should | could
	Verify   string `json:"verify,omitempty"` // how it is proved: a test name, a command, an observation
	Status   string `json:"status"`           // open | pass | fail
	Evidence string `json:"evidence,omitempty"`
	Amended  string `json:"amended,omitempty"` // human-approved changes to the text: date, old text, reason
}

// ParseCriterion reads "statement :: how to verify" (the verify part is optional).
func ParseCriterion(prio, s string) Criterion {
	text, verify, _ := strings.Cut(s, "::")
	return Criterion{Text: strings.TrimSpace(text), Verify: strings.TrimSpace(verify), Priority: prio, Status: CritOpen}
}

// AddCriterion appends a criterion to the task and numbers it.
func (t *Task) AddCriterion(c Criterion) *Criterion {
	if c.Priority == "" {
		c.Priority = PrioMust
	}
	if c.Status == "" {
		c.Status = CritOpen
	}
	max := 0
	for _, x := range t.Criteria {
		if n, err := strconv.Atoi(strings.TrimPrefix(x.ID, "AC")); err == nil && n > max {
			max = n
		}
	}
	c.ID = fmt.Sprintf("AC%d", max+1)
	t.Criteria = append(t.Criteria, c)
	return &t.Criteria[len(t.Criteria)-1]
}

// FindCriterion returns a criterion by id ("1", "ac1" or "AC1").
func (t *Task) FindCriterion(id string) *Criterion {
	id = NormID("AC", id)
	for i := range t.Criteria {
		if t.Criteria[i].ID == id {
			return &t.Criteria[i]
		}
	}
	return nil
}

// UnmetMust lists the Must criteria that are not marked pass.
func (t Task) UnmetMust() []Criterion {
	var out []Criterion
	for _, c := range t.Criteria {
		if c.Priority == PrioMust && c.Status != CritPass {
			out = append(out, c)
		}
	}
	return out
}

// Idea statuses. Ideas are a parking lot: they never run until promoted.
const (
	IdeaOpen     = "open"
	IdeaPromoted = "promoted"
	IdeaDropped  = "dropped"
)

type Idea struct {
	ID     string    `json:"id"`
	Text   string    `json:"text"`
	Source string    `json:"source,omitempty"`
	Added  time.Time `json:"added"`
	Status string    `json:"status"`
	Task   string    `json:"task,omitempty"` // task created when promoted
}

// Lesson statuses. Lessons are injected into agent prompts, so lessons that
// an agent proposes wait for a human before they take effect.
const (
	LessonActive   = "active"
	LessonProposed = "proposed"
	LessonRetired  = "retired"
)

type Lesson struct {
	ID     string    `json:"id"`
	Text   string    `json:"text"`
	Source string    `json:"source,omitempty"`
	Added  time.Time `json:"added"`
	Status string    `json:"status"`
}

type Blocker struct {
	ID         string     `json:"id"`
	Text       string     `json:"text"`
	Source     string     `json:"source,omitempty"`
	Opened     time.Time  `json:"opened"`
	Resolved   bool       `json:"resolved"`
	Answer     string     `json:"answer,omitempty"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
}

// Decision statuses.
const (
	DecisionOpen    = "Open"
	DecisionDecided = "Decided"
	DecisionRevisit = "Revisit"
)

type Decision struct {
	ID        string `json:"id"`
	Topic     string `json:"topic"`
	Status    string `json:"status"`
	Options   string `json:"options,omitempty"`
	Needs     string `json:"needs,omitempty"`
	Decision  string `json:"decision,omitempty"`
	Rationale string `json:"rationale,omitempty"`
	Date      string `json:"date,omitempty"`
}

type Review struct {
	ID      string    `json:"id"`
	Time    time.Time `json:"time"`
	Agent   string    `json:"agent"`
	Verdict string    `json:"verdict"` // approve | changes_requested | note
	Summary string    `json:"summary"`
	Tasks   []string  `json:"tasks,omitempty"`
}

type Ledger struct {
	Tasks     []Task     `json:"tasks"`
	Blockers  []Blocker  `json:"blockers"`
	Decisions []Decision `json:"decisions"`
	Reviews   []Review   `json:"reviews"`
	Ideas     []Idea     `json:"ideas,omitempty"`
	Releases  []Release  `json:"releases,omitempty"`
	Lessons   []Lesson   `json:"lessons,omitempty"`
	NextTask  int        `json:"next_task"`
	NextBlk   int        `json:"next_blocker"`
	NextDec   int        `json:"next_decision"`
	NextRev   int        `json:"next_review"`
	NextIdea  int        `json:"next_idea,omitempty"`
	NextLsn   int        `json:"next_lesson,omitempty"`
}

// NormID turns "3" or "t3" into "T3" for the given prefix letter.
func NormID(prefix, s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	if _, err := strconv.Atoi(s); err == nil {
		return prefix + s
	}
	return s
}

// FindTask returns a task by id (nil if absent).
func (l *Ledger) FindTask(id string) *Task {
	id = NormID("T", id)
	for i := range l.Tasks {
		if l.Tasks[i].ID == id {
			return &l.Tasks[i]
		}
	}
	return nil
}

// AddTask appends a task; top=true places it ahead of other waiting tasks.
func (l *Ledger) AddTask(title, detail, source, status string, top bool) *Task {
	if status == "" {
		status = TaskPending
	}
	l.NextTask++
	t := Task{ID: fmt.Sprintf("T%d", l.NextTask), Title: strings.TrimSpace(title), Detail: strings.TrimSpace(detail), Status: status, Source: source, Added: time.Now().UTC()}
	idx := len(l.Tasks)
	if top {
		for i, x := range l.Tasks {
			if x.Status == TaskPending || x.Status == TaskProposed {
				idx = i
				break
			}
		}
	}
	l.Tasks = append(l.Tasks, Task{})
	copy(l.Tasks[idx+1:], l.Tasks[idx:])
	l.Tasks[idx] = t
	return &l.Tasks[idx]
}

// AddDoneTask records already-finished work (used when adopting a project).
func (l *Ledger) AddDoneTask(title, commit, summary, source string) *Task {
	t := l.AddTask(title, "", source, TaskDone, false)
	now := time.Now().UTC()
	t.Commit, t.Summary, t.Done = commit, strings.TrimSpace(summary), &now
	return t
}

// MoveToFront places a pending task ahead of the other waiting tasks.
func (l *Ledger) MoveToFront(id string) bool {
	id = NormID("T", id)
	idx := -1
	for i, t := range l.Tasks {
		if t.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false
	}
	t := l.Tasks[idx]
	l.Tasks = append(l.Tasks[:idx], l.Tasks[idx+1:]...)
	pos := len(l.Tasks)
	for i, x := range l.Tasks {
		if x.Status == TaskPending || x.Status == TaskProposed {
			pos = i
			break
		}
	}
	l.Tasks = append(l.Tasks, Task{})
	copy(l.Tasks[pos+1:], l.Tasks[pos:])
	l.Tasks[pos] = t
	return true
}

func (l *Ledger) Current() *Task {
	for i := range l.Tasks {
		if l.Tasks[i].Status == TaskInProgress {
			return &l.Tasks[i]
		}
	}
	return nil
}

func (l *Ledger) NextPending() *Task {
	for i := range l.Tasks {
		if l.Tasks[i].Status == TaskPending {
			return &l.Tasks[i]
		}
	}
	return nil
}

// Promote makes the next pending task current if nothing is in progress.
func (l *Ledger) Promote() *Task {
	if c := l.Current(); c != nil {
		return c
	}
	if n := l.NextPending(); n != nil {
		n.Status = TaskInProgress
		return n
	}
	return nil
}

func (l *Ledger) Count(status string) int {
	n := 0
	for _, t := range l.Tasks {
		if t.Status == status {
			n++
		}
	}
	return n
}

// Remaining is the number of tasks the runner can still work on.
func (l *Ledger) Remaining() int { return l.Count(TaskPending) + l.Count(TaskInProgress) }

// Complete marks a task done. An empty id means the current task.
func (l *Ledger) Complete(id, commit, summary string) (*Task, error) {
	var t *Task
	if strings.TrimSpace(id) == "" {
		t = l.Current()
		if t == nil {
			return nil, fmt.Errorf("no task is in progress; pass a task id")
		}
	} else if t = l.FindTask(id); t == nil {
		return nil, fmt.Errorf("no task %s", NormID("T", id))
	}
	if t.Status == TaskDone {
		return nil, fmt.Errorf("%s is already done", t.ID)
	}
	now := time.Now().UTC()
	t.Status, t.Commit, t.Summary, t.Done = TaskDone, commit, summary, &now
	return t, nil
}

func (l *Ledger) SetTaskStatus(id, status string) (*Task, error) {
	t := l.FindTask(id)
	if t == nil {
		return nil, fmt.Errorf("no task %s", NormID("T", id))
	}
	t.Status = status
	return t, nil
}

func (l *Ledger) OpenBlockers() []*Blocker {
	var out []*Blocker
	for i := range l.Blockers {
		if !l.Blockers[i].Resolved {
			out = append(out, &l.Blockers[i])
		}
	}
	return out
}

func (l *Ledger) AddBlocker(text, source string) *Blocker {
	l.NextBlk++
	l.Blockers = append(l.Blockers, Blocker{ID: fmt.Sprintf("B%d", l.NextBlk), Text: strings.TrimSpace(text), Source: source, Opened: time.Now().UTC()})
	return &l.Blockers[len(l.Blockers)-1]
}

// ResolveBlocker closes one blocker (id) or all open ones (id == "all").
func (l *Ledger) ResolveBlocker(id, answer string) ([]*Blocker, error) {
	var hit []*Blocker
	now := time.Now().UTC()
	id = NormID("B", id)
	for i := range l.Blockers {
		b := &l.Blockers[i]
		if b.Resolved || (id != "ALL" && b.ID != id) {
			continue
		}
		b.Resolved, b.Answer, b.ResolvedAt = true, strings.TrimSpace(answer), &now
		hit = append(hit, b)
	}
	if len(hit) == 0 {
		return nil, fmt.Errorf("no open blocker %s", id)
	}
	return hit, nil
}

func (l *Ledger) FindBlocker(id string) *Blocker {
	id = NormID("B", id)
	for i := range l.Blockers {
		if l.Blockers[i].ID == id {
			return &l.Blockers[i]
		}
	}
	return nil
}

func (l *Ledger) AddDecision(topic, options, needs string) *Decision {
	l.NextDec++
	l.Decisions = append(l.Decisions, Decision{ID: fmt.Sprintf("D%d", l.NextDec), Topic: strings.TrimSpace(topic), Status: DecisionOpen, Options: options, Needs: needs})
	return &l.Decisions[len(l.Decisions)-1]
}

func (l *Ledger) FindDecision(id string) *Decision {
	id = NormID("D", id)
	for i := range l.Decisions {
		if l.Decisions[i].ID == id {
			return &l.Decisions[i]
		}
	}
	return nil
}

func (l *Ledger) ResolveDecision(id, decision, rationale string) (*Decision, error) {
	d := l.FindDecision(id)
	if d == nil {
		return nil, fmt.Errorf("no decision %s", NormID("D", id))
	}
	d.Status, d.Decision, d.Rationale, d.Date = DecisionDecided, decision, rationale, time.Now().UTC().Format("2006-01-02")
	return d, nil
}

func (l *Ledger) OpenDecisions() []*Decision {
	var out []*Decision
	for i := range l.Decisions {
		if l.Decisions[i].Status == DecisionOpen {
			out = append(out, &l.Decisions[i])
		}
	}
	return out
}

func (l *Ledger) AddReview(agent, verdict, summary string, tasks []string) *Review {
	l.NextRev++
	l.Reviews = append(l.Reviews, Review{ID: fmt.Sprintf("R%d", l.NextRev), Time: time.Now().UTC(), Agent: agent, Verdict: verdict, Summary: strings.TrimSpace(summary), Tasks: tasks})
	return &l.Reviews[len(l.Reviews)-1]
}

// ConsecutiveChangeRequests counts trailing "changes_requested" reviews.
func (l *Ledger) ConsecutiveChangeRequests() int {
	n := 0
	for i := len(l.Reviews) - 1; i >= 0; i-- {
		if l.Reviews[i].Verdict != "changes_requested" {
			break
		}
		n++
	}
	return n
}

// Release records one `hloop release`: what it covered and where its
// documents are. The next release covers everything after Commit.
type Release struct {
	Version  string    `json:"version"`
	Date     time.Time `json:"date"`
	Commit   string    `json:"commit"`             // HEAD after the release documents were committed (or HEAD when not committed)
	CodeHead string    `json:"code_head"`          // HEAD of the code being released
	From     string    `json:"from,omitempty"`     // previous release's commit; empty for the first release
	Tasks    []string  `json:"tasks,omitempty"`    // tasks finished in this release
	Notes    string    `json:"notes"`              // release notes, relative to the project root
	Docs     []string  `json:"docs,omitempty"`     // documents created or updated
	Tag      string    `json:"tag,omitempty"`      // git tag, if one was made
	Warnings []string  `json:"warnings,omitempty"` // preflight warnings accepted at release time
}

// LastRelease returns the most recent release, or nil.
func (l *Ledger) LastRelease() *Release {
	if len(l.Releases) == 0 {
		return nil
	}
	return &l.Releases[len(l.Releases)-1]
}

// FindRelease returns the release with the given version ("v" optional).
func (l *Ledger) FindRelease(v string) *Release {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	for i := range l.Releases {
		if l.Releases[i].Version == v {
			return &l.Releases[i]
		}
	}
	return nil
}

// ---------- ideas ----------

func (l *Ledger) AddIdea(text, source string) *Idea {
	l.NextIdea++
	l.Ideas = append(l.Ideas, Idea{ID: fmt.Sprintf("I%d", l.NextIdea), Text: strings.TrimSpace(text), Source: source, Added: time.Now().UTC(), Status: IdeaOpen})
	return &l.Ideas[len(l.Ideas)-1]
}

func (l *Ledger) FindIdea(id string) *Idea {
	id = NormID("I", id)
	for i := range l.Ideas {
		if l.Ideas[i].ID == id {
			return &l.Ideas[i]
		}
	}
	return nil
}

func (l *Ledger) OpenIdeas() []*Idea {
	var out []*Idea
	for i := range l.Ideas {
		if l.Ideas[i].Status == IdeaOpen {
			out = append(out, &l.Ideas[i])
		}
	}
	return out
}

// PromoteIdea turns an open idea into a task with the given status.
func (l *Ledger) PromoteIdea(id, status string) (*Idea, *Task, error) {
	x := l.FindIdea(id)
	if x == nil {
		return nil, nil, fmt.Errorf("no idea %s", NormID("I", id))
	}
	if x.Status != IdeaOpen {
		return nil, nil, fmt.Errorf("%s is %s", x.ID, x.Status)
	}
	t := l.AddTask(x.Text, "Promoted from idea "+x.ID+".", "idea", status, false)
	x.Status, x.Task = IdeaPromoted, t.ID
	return l.FindIdea(id), l.FindTask(t.ID), nil
}

// ---------- lessons ----------

func (l *Ledger) AddLesson(text, source, status string) *Lesson {
	l.NextLsn++
	l.Lessons = append(l.Lessons, Lesson{ID: fmt.Sprintf("L%d", l.NextLsn), Text: strings.TrimSpace(text), Source: source, Added: time.Now().UTC(), Status: status})
	return &l.Lessons[len(l.Lessons)-1]
}

func (l *Ledger) FindLesson(id string) *Lesson {
	id = NormID("L", id)
	for i := range l.Lessons {
		if l.Lessons[i].ID == id {
			return &l.Lessons[i]
		}
	}
	return nil
}

func (l *Ledger) LessonsWith(status string) []*Lesson {
	var out []*Lesson
	for i := range l.Lessons {
		if l.Lessons[i].Status == status {
			out = append(out, &l.Lessons[i])
		}
	}
	return out
}

// ---------- persistence ----------

func LoadLedger(p Project) (Ledger, error) {
	var l Ledger
	if err := readJSON(p.Path(LedgerFile), &l); err != nil && !os.IsNotExist(err) {
		return l, err
	}
	return l, nil
}

// UpdateLedger applies fn under a cross-process lock, saves, and re-renders
// the generated PROGRESS.md and DECISIONS.md views.
func UpdateLedger(p Project, fn func(*Ledger) error) error {
	return withFileLock(p.Path("ledger.lock"), func() error {
		l, err := LoadLedger(p)
		if err != nil {
			return err
		}
		if err := fn(&l); err != nil {
			return err
		}
		if err := writeJSON(p.Path(LedgerFile), l, 0o644); err != nil {
			return err
		}
		return RenderViews(p, l)
	})
}
