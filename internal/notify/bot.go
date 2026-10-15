package notify

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"hloop/internal/store"
)

// Reply is what the bot sends back.
type Reply struct {
	Text string
	Rows [][]Button
}

// Bot listens for commands from exactly one paired Telegram account. It only
// ever calls the fixed set of operations below: never a shell, never free-form
// prompts to an agent.
type Bot struct {
	TG    *TG
	G     store.Global
	Exe   string
	Out   io.Writer
	Now   func() time.Time
	Spawn func(exe string, args ...string) error
}

func NewBot(g store.Global, exe string, out io.Writer) *Bot {
	return &Bot{TG: NewTG(g.Telegram.Token), G: g, Exe: exe, Out: out, Now: time.Now, Spawn: spawnDetached}
}

// SpawnDetached starts a background process with no terminal attached.
func SpawnDetached(exe string, args ...string) error { return spawnDetached(exe, args...) }

func spawnDetached(exe string, args ...string) error {
	cmd := exec.Command(exe, args...)
	devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err == nil {
		cmd.Stdin, cmd.Stdout, cmd.Stderr = devnull, devnull, devnull
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		_ = cmd.Wait()
		if devnull != nil {
			devnull.Close()
		}
	}()
	return nil
}

func (b *Bot) logf(format string, a ...any) {
	if b.Out != nil {
		fmt.Fprintf(b.Out, format+"\n", a...)
	}
}

func (b *Bot) allowed(chat Chat, from *User) bool {
	return from != nil && !from.IsBot && chat.Type == "private" &&
		from.ID == b.G.Telegram.UserID && chat.ID == b.G.Telegram.ChatID
}

// Listen long-polls until ctx is cancelled.
func (b *Bot) Listen(ctx context.Context) error {
	if _, err := b.TG.GetMe(ctx); err != nil {
		return fmt.Errorf("cannot reach Telegram: %w", err)
	}
	b.logf("listening for Telegram commands (paired user only). Ctrl+C to stop.")
	backoff := 2 * time.Second
	for ctx.Err() == nil {
		off := store.LoadTGState().Offset
		ups, err := b.TG.GetUpdates(ctx, off, 30)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			b.logf("telegram: %v (retrying in %s)", err, backoff)
			select {
			case <-ctx.Done():
			case <-time.After(backoff):
			}
			if backoff < time.Minute {
				backoff *= 2
			}
			continue
		}
		backoff = 2 * time.Second
		for _, u := range ups {
			next := u.UpdateID + 1
			_ = store.UpdateTGState(func(s *store.TGState) {
				if next > s.Offset {
					s.Offset = next
				}
			})
			b.HandleUpdate(ctx, u)
		}
	}
	return nil
}

// HandleUpdate processes one update from Telegram.
func (b *Bot) HandleUpdate(ctx context.Context, u Update) {
	switch {
	case u.Callback != nil:
		cb := u.Callback
		if cb.Message == nil || !b.allowed(cb.Message.Chat, &cb.From) {
			b.logf("ignored callback from unauthorised sender %d", cb.From.ID)
			return
		}
		r := b.execCallback(ctx, cb.Data)
		_ = b.TG.AnswerCallback(ctx, cb.ID, firstLine(r.Text))
		if _, err := b.TG.SendMessage(ctx, cb.Message.Chat.ID, r.Text, false, r.Rows); err != nil {
			b.logf("telegram send: %v", err)
		}
	case u.Message != nil:
		m := u.Message
		if !b.allowed(m.Chat, m.From) {
			id := int64(0)
			if m.From != nil {
				id = m.From.ID
			}
			b.logf("ignored message from unauthorised sender %d", id)
			return
		}
		var ref *store.MsgRef
		if m.ReplyTo != nil {
			if r, ok := store.LoadTGState().Messages[fmt.Sprintf("%d:%d", m.Chat.ID, m.ReplyTo.MessageID)]; ok {
				ref = &r
			}
		}
		r := b.Exec(ctx, m.Text, ref)
		if _, err := b.TG.SendMessage(ctx, m.Chat.ID, r.Text, false, r.Rows); err != nil {
			b.logf("telegram send: %v", err)
		}
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

const helpText = `hloop commands:
/status  /tasks  /blockers  /decisions
/add <task>  queue a new task
/note <text>  steering note for the next turn
/answer <B1> <text>  answer a blocker (or just reply to its alert)
/decide <D1> <decision>  resolve an open decision
/approve <T1|N1|L1|I1>  /discard <...>  proposed tasks, held notes, proposed lessons, ideas (approve = promote)
/idea <text>  park an idea (never runs until promoted)
/ideas  /lessons  /show <T1>  task details and criteria
/run  /pause  /resume  /review
/projects  /use <name>`

func splitCmd(text string) (string, string) {
	parts := strings.SplitN(strings.TrimSpace(text), " ", 2)
	cmd := strings.ToLower(strings.SplitN(parts[0], "@", 2)[0])
	arg := ""
	if len(parts) > 1 {
		arg = strings.TrimSpace(parts[1])
	}
	return cmd, arg
}

// project resolves which project commands apply to.
func (b *Bot) project() (string, store.Project, error) {
	st := store.LoadTGState()
	if st.Current != "" {
		if p, err := store.Lookup(st.Current); err == nil {
			return st.Current, p, nil
		}
	}
	reg, err := store.LoadRegistry()
	if err != nil {
		return "", store.Project{}, err
	}
	switch len(reg) {
	case 0:
		return "", store.Project{}, fmt.Errorf("no projects registered; run `hloop init` in a repo first")
	case 1:
		return reg[0].Name, store.Project{Root: reg[0].Path}, nil
	}
	var names []string
	for _, e := range reg {
		names = append(names, e.Name)
	}
	return "", store.Project{}, fmt.Errorf("several projects: pick one with /use <name> (%s)", strings.Join(names, ", "))
}

func errReply(err error) Reply { return Reply{Text: "Error: " + err.Error()} }

// Exec runs one text command. rr is set when the message replies to an alert.
func (b *Bot) Exec(ctx context.Context, text string, rr *store.MsgRef) Reply {
	text = strings.TrimSpace(text)
	if len(text) > 2000 {
		text = text[:2000]
	}
	if text == "" {
		return Reply{Text: helpText}
	}
	if !strings.HasPrefix(text, "/") {
		if rr != nil && strings.HasPrefix(rr.Ref, "B") {
			return b.answer(rr.Project, rr.Ref, text)
		}
		return Reply{Text: "I only act on commands. Try /add <task> or /help."}
	}
	cmd, arg := splitCmd(text)
	switch cmd {
	case "/start", "/help":
		return Reply{Text: helpText}
	case "/projects":
		reg, _ := store.LoadRegistry()
		cur := store.LoadTGState().Current
		var sb strings.Builder
		for _, e := range reg {
			mark := " "
			if e.Name == cur {
				mark = "*"
			}
			fmt.Fprintf(&sb, "%s %s\n", mark, e.Name)
		}
		if sb.Len() == 0 {
			return Reply{Text: "No projects registered."}
		}
		return Reply{Text: strings.TrimSpace(sb.String())}
	case "/use":
		if _, err := store.Lookup(arg); err != nil {
			return errReply(err)
		}
		_ = store.UpdateTGState(func(s *store.TGState) { s.Current = arg })
		return Reply{Text: "Now controlling " + arg}
	}

	name, p, err := b.project()
	if err != nil {
		return errReply(err)
	}
	cfg, err := p.LoadConfig()
	if err != nil {
		return errReply(err)
	}

	switch cmd {
	case "/status":
		return Reply{Text: store.StatusText(p, name, b.Now()), Rows: [][]Button{{{"Run now", "run:" + name}, {"Pause", "pause:" + name}, {"Resume", "resume:" + name}}}}
	case "/tasks":
		l, _ := store.LoadLedger(p)
		var sb strings.Builder
		if c := l.Current(); c != nil {
			fmt.Fprintf(&sb, "Now: %s %s\n", c.ID, c.Title)
		}
		n := 0
		for _, t := range l.Tasks {
			if t.Status == store.TaskPending && n < 8 {
				fmt.Fprintf(&sb, "Next: %s %s\n", t.ID, t.Title)
				n++
			}
		}
		for _, t := range l.Tasks {
			if t.Status == store.TaskProposed {
				fmt.Fprintf(&sb, "Awaiting approval: %s %s\n", t.ID, t.Title)
			}
		}
		if sb.Len() == 0 {
			return Reply{Text: "No open tasks."}
		}
		return Reply{Text: strings.TrimSpace(sb.String())}
	case "/add":
		if arg == "" {
			return Reply{Text: "Usage: /add <task description>"}
		}
		status := store.TaskPending
		if cfg.RemoteTasks != "auto" {
			status = store.TaskProposed
		}
		var t store.Task
		if err := store.UpdateLedger(p, func(l *store.Ledger) error {
			t = *l.AddTask(arg, "", "phone", status, false)
			return nil
		}); err != nil {
			return errReply(err)
		}
		if status == store.TaskProposed {
			return Reply{Text: fmt.Sprintf("Proposed %s: %s\nIt will not run until you approve it.", t.ID, t.Title),
				Rows: [][]Button{{{"Approve " + t.ID, "approve:" + name + ":" + t.ID}, {"Discard", "discard:" + name + ":" + t.ID}}}}
		}
		return Reply{Text: fmt.Sprintf("Queued %s: %s", t.ID, t.Title)}
	case "/approve", "/discard":
		return b.triage(p, cmd == "/approve", arg)
	case "/note":
		if arg == "" {
			return Reply{Text: "Usage: /note <text>"}
		}
		if cfg.RemoteTasks != "auto" {
			it, err := p.AddHeldInbox("note", arg, "phone")
			if err != nil {
				return errReply(err)
			}
			return Reply{Text: fmt.Sprintf("Held %s: %s\nThe agent will not see it until you approve it.", it.ID, truncate(it.Text, 200)),
				Rows: [][]Button{{{"Approve " + it.ID, "approve:" + name + ":" + it.ID}, {"Discard", "discard:" + name + ":" + it.ID}}}}
		}
		if err := p.AddInbox("note", arg, "phone"); err != nil {
			return errReply(err)
		}
		return Reply{Text: "Noted. The agent will see it at the start of its next turn."}
	case "/blockers":
		l, _ := store.LoadLedger(p)
		open := l.OpenBlockers()
		if len(open) == 0 {
			return Reply{Text: "No open blockers."}
		}
		var sb strings.Builder
		for _, x := range open {
			fmt.Fprintf(&sb, "%s: %s\n\n", x.ID, x.Text)
		}
		sb.WriteString("Answer with /answer <id> <text>, or reply to the alert message.")
		return Reply{Text: sb.String()}
	case "/answer":
		parts := strings.SplitN(arg, " ", 2)
		if len(parts) < 2 {
			return Reply{Text: "Usage: /answer <B1> <your answer>"}
		}
		return b.answer(name, parts[0], parts[1])
	case "/decisions":
		l, _ := store.LoadLedger(p)
		od := l.OpenDecisions()
		if len(od) == 0 {
			return Reply{Text: "No open decisions."}
		}
		var sb strings.Builder
		for _, d := range od {
			fmt.Fprintf(&sb, "%s: %s\n", d.ID, d.Topic)
			if d.Options != "" {
				fmt.Fprintf(&sb, "  options: %s\n", d.Options)
			}
		}
		sb.WriteString("\nResolve with /decide <id> <decision>.")
		return Reply{Text: sb.String()}
	case "/decide":
		parts := strings.SplitN(arg, " ", 2)
		if len(parts) < 2 {
			return Reply{Text: "Usage: /decide <D1> <decision>"}
		}
		var d store.Decision
		if err := store.UpdateLedger(p, func(l *store.Ledger) error {
			x, err := l.ResolveDecision(parts[0], parts[1], "decided from phone")
			if err == nil {
				d = *x
			}
			return err
		}); err != nil {
			return errReply(err)
		}
		return Reply{Text: fmt.Sprintf("Recorded %s as Decided: %s", d.ID, d.Decision)}
	case "/pause", "/resume", "/run", "/review":
		return b.control(name, p, cmd[1:])
	case "/idea":
		if arg == "" {
			return Reply{Text: "Usage: /idea <something you might want later>"}
		}
		var x store.Idea
		if err := store.UpdateLedger(p, func(l *store.Ledger) error { x = *l.AddIdea(arg, "phone"); return nil }); err != nil {
			return errReply(err)
		}
		return Reply{Text: fmt.Sprintf("Parked %s. It will not run until you promote it.", x.ID),
			Rows: [][]Button{{{"Promote to task", "approve:" + name + ":" + x.ID}}}}
	case "/ideas":
		l, _ := store.LoadLedger(p)
		oi := l.OpenIdeas()
		if len(oi) == 0 {
			return Reply{Text: "No ideas parked. Add one with /idea <text>."}
		}
		var sb strings.Builder
		for _, x := range oi {
			fmt.Fprintf(&sb, "%s: %s\n", x.ID, truncate(x.Text, 160))
		}
		sb.WriteString("\n/approve <I1> promotes one to a task, /discard <I1> drops it.")
		return Reply{Text: sb.String()}
	case "/lessons":
		l, _ := store.LoadLedger(p)
		if len(l.Lessons) == 0 {
			return Reply{Text: "No lessons yet."}
		}
		var sb strings.Builder
		var rows [][]Button
		for _, x := range l.Lessons {
			if x.Status == store.LessonRetired {
				continue
			}
			fmt.Fprintf(&sb, "%s [%s] %s\n", x.ID, x.Status, truncate(x.Text, 200))
			if x.Status == store.LessonProposed && len(rows) < 4 {
				rows = append(rows, []Button{{"Approve " + x.ID, "approve:" + name + ":" + x.ID}, {"Reject", "discard:" + name + ":" + x.ID}})
			}
		}
		return Reply{Text: strings.TrimSpace(sb.String()), Rows: rows}
	case "/show":
		l, _ := store.LoadLedger(p)
		var t *store.Task
		if arg == "" {
			t = l.Current()
		} else {
			t = l.FindTask(arg)
		}
		if t == nil {
			return Reply{Text: "Usage: /show <T1> (or no argument for the current task)"}
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "%s %s\n%s, %s", t.ID, t.Title, t.Status, t.EffKind())
		if t.GateFails > 0 {
			fmt.Fprintf(&sb, ", %d rejected finish(es)", t.GateFails)
		}
		sb.WriteString("\n")
		for _, c := range t.Criteria {
			mark := map[string]string{store.CritPass: "✓", store.CritFail: "✗"}[c.Status]
			if mark == "" {
				mark = "·"
			}
			fmt.Fprintf(&sb, "%s %s (%s) %s\n", mark, c.ID, c.Priority, truncate(c.Text, 140))
		}
		if t.Verified != "" {
			fmt.Fprintf(&sb, "hloop: %s\n", t.Verified)
		}
		for _, c := range t.Checks {
			if !c.OK {
				fmt.Fprintf(&sb, "failed: %s\n", c.Name)
			}
		}
		return Reply{Text: strings.TrimSpace(sb.String())}
	}
	return Reply{Text: "Unknown command. Try /help."}
}

func (b *Bot) triage(p store.Project, approve bool, id string) Reply {
	if id == "" {
		return Reply{Text: "Usage: /approve <T1> or /discard <T1>"}
	}
	up := strings.ToUpper(strings.TrimSpace(id))
	if strings.HasPrefix(up, "L") || strings.HasPrefix(up, "I") {
		var msg string
		err := store.UpdateLedger(p, func(l *store.Ledger) error {
			if strings.HasPrefix(up, "L") {
				x := l.FindLesson(id)
				if x == nil {
					return fmt.Errorf("no lesson %s", store.NormID("L", id))
				}
				if approve {
					x.Status = store.LessonActive
					msg = fmt.Sprintf("Lesson %s is active: every agent turn is told about it.", x.ID)
				} else {
					x.Status = store.LessonRetired
					msg = fmt.Sprintf("Lesson %s retired.", x.ID)
				}
				return nil
			}
			if approve {
				x, t, err := l.PromoteIdea(id, store.TaskPending)
				if err != nil {
					return err
				}
				msg = fmt.Sprintf("Idea %s is now task %s (queued). Give it acceptance criteria from your computer with `hloop spec --task %s`.", x.ID, t.ID, t.ID)
				return nil
			}
			x := l.FindIdea(id)
			if x == nil {
				return fmt.Errorf("no idea %s", store.NormID("I", id))
			}
			x.Status = store.IdeaDropped
			msg = fmt.Sprintf("Idea %s dropped.", x.ID)
			return nil
		})
		if err != nil {
			return errReply(err)
		}
		return Reply{Text: msg}
	}
	if strings.HasPrefix(up, "N") {
		it, err := p.ResolveHeld(id, approve)
		if err != nil {
			return errReply(err)
		}
		if approve {
			return Reply{Text: fmt.Sprintf("Approved note %s. The agent will see it at the start of its next turn.", it.ID)}
		}
		return Reply{Text: fmt.Sprintf("Discarded note %s.", it.ID)}
	}
	var t store.Task
	err := store.UpdateLedger(p, func(l *store.Ledger) error {
		x := l.FindTask(id)
		if x == nil {
			return fmt.Errorf("no task %s", store.NormID("T", id))
		}
		if x.Status != store.TaskProposed {
			return fmt.Errorf("%s is %s, not proposed", x.ID, x.Status)
		}
		if approve {
			x.Status = store.TaskPending
		} else {
			x.Status = store.TaskSkipped
		}
		t = *x
		return nil
	})
	if err != nil {
		return errReply(err)
	}
	if approve {
		return Reply{Text: fmt.Sprintf("Approved %s: %s", t.ID, t.Title)}
	}
	return Reply{Text: fmt.Sprintf("Discarded %s", t.ID)}
}

func (b *Bot) answer(projName, id, text string) Reply {
	p, err := store.Lookup(projName)
	if err != nil {
		return errReply(err)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return Reply{Text: "An answer can't be empty."}
	}
	if err := store.UpdateLedger(p, func(l *store.Ledger) error {
		_, err := l.ResolveBlocker(id, text)
		return err
	}); err != nil {
		return errReply(err)
	}
	msg := fmt.Sprintf("Answered %s.", store.NormID("B", id))
	if cfg, err := p.LoadConfig(); err == nil && cfg.AutoResume {
		l, _ := store.LoadLedger(p)
		if len(l.OpenBlockers()) == 0 && !p.RunActive() && !p.LoadState().Paused {
			if err := b.Spawn(b.Exe, "run", "-C", p.Root, "--force"); err != nil {
				msg += " (could not start the loop: " + err.Error() + ")"
			} else {
				msg += " Resuming the loop now."
			}
		}
	}
	return Reply{Text: msg}
}

func (b *Bot) control(name string, p store.Project, what string) Reply {
	switch what {
	case "pause":
		if err := p.UpdateState(func(s *store.State) { s.Paused = true }); err != nil {
			return errReply(err)
		}
		return Reply{Text: "Paused. A run in progress will stop after its current turn."}
	case "resume":
		if err := p.UpdateState(func(s *store.State) { s.Paused = false }); err != nil {
			return errReply(err)
		}
		return Reply{Text: "Resumed. The next scheduled run (or /run) will continue."}
	case "run":
		if p.LoadState().Paused {
			return Reply{Text: "The project is paused. Send /resume first."}
		}
		if p.RunActive() {
			return Reply{Text: "A run is already active."}
		}
		if err := b.Spawn(b.Exe, "run", "-C", p.Root, "--force"); err != nil {
			return errReply(err)
		}
		return Reply{Text: "Started a run for " + name + "."}
	case "review":
		if err := b.Spawn(b.Exe, "review", "run", "-C", p.Root); err != nil {
			return errReply(err)
		}
		return Reply{Text: "Started a review for " + name + "."}
	}
	return Reply{Text: "Unknown command."}
}

// execCallback handles inline-button presses ("action:project[:ref]").
func (b *Bot) execCallback(ctx context.Context, data string) Reply {
	parts := strings.SplitN(data, ":", 3)
	if len(parts) < 2 {
		return Reply{Text: "Unrecognised button."}
	}
	action, proj := parts[0], parts[1]
	p, err := store.Lookup(proj)
	if err != nil {
		return errReply(err)
	}
	ref := ""
	if len(parts) == 3 {
		ref = parts[2]
	}
	switch action {
	case "status":
		return Reply{Text: store.StatusText(p, proj, b.Now())}
	case "approve", "discard":
		return b.triage(p, action == "approve", ref)
	case "run", "pause", "resume":
		return b.control(proj, p, action)
	}
	return Reply{Text: "Unrecognised button."}
}
