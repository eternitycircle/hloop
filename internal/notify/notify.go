// Package notify delivers project events to a phone (ntfy and/or Telegram)
// and, for Telegram, accepts a fixed set of commands back.
package notify

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"hloop/internal/store"
)

// Event kinds.
const (
	KindBlocker     = "blocker"
	KindComplete    = "complete"
	KindRateLimited = "rate_limited"
	KindError       = "error"
	KindReview      = "review"
	KindDigest      = "digest"
	KindCheck       = "check" // a finish hloop rejected, or a guardrail detector fired
)

type Button struct{ Text, Data string }

// Event is one notification.
type Event struct {
	Kind     string
	Title    string
	Body     string
	Priority int    // 1 (min) to 5 (max); 0 means default for the kind
	Project  string // registry name, used for buttons and reply routing
	Ref      string // e.g. a blocker id: replying to the message answers it
}

var tags = map[string]string{
	KindBlocker: "warning", KindComplete: "white_check_mark", KindRateLimited: "hourglass_flowing_sand",
	KindError: "rotating_light", KindReview: "mag", KindDigest: "memo", KindCheck: "no_entry",
}

var emoji = map[string]string{
	KindBlocker: "⚠️", KindComplete: "✅", KindRateLimited: "⏳",
	KindError: "\U0001f6a8", KindReview: "\U0001f50d", KindDigest: "\U0001f4dd", KindCheck: "\u26d4",
}

// DefaultPriority returns the priority used when an event does not set one.
func DefaultPriority(kind string) int {
	switch kind {
	case KindBlocker, KindError:
		return 4
	case KindDigest:
		return 2
	}
	return 3
}

// Dispatcher fans an event out to every configured channel.
type Dispatcher struct {
	G      store.Global
	Out    io.Writer
	HTTP   *http.Client
	TGBase string // override for tests

	// DesktopFn shows a local notification; nil disables it (tests).
	DesktopFn func(title, body string)
}

func NewDispatcher(out io.Writer) *Dispatcher {
	g, _ := store.LoadGlobal()
	return &Dispatcher{G: g, Out: out, HTTP: &http.Client{Timeout: 20 * time.Second}, DesktopFn: Desktop}
}

// Channels lists configured channels.
func (d *Dispatcher) Channels() []string {
	var c []string
	if d.G.Ntfy.Topic != "" {
		c = append(c, "ntfy")
	}
	if d.G.Telegram.Token != "" && d.G.Telegram.ChatID != 0 {
		c = append(c, "telegram")
	}
	return c
}

func (d *Dispatcher) logf(format string, a ...any) {
	if d.Out != nil {
		fmt.Fprintf(d.Out, format+"\n", a...)
	}
}

// Send is best-effort: failures are logged, never fatal.
func (d *Dispatcher) Send(ev Event) {
	if ev.Priority == 0 {
		ev.Priority = DefaultPriority(ev.Kind)
	}
	if !d.G.NoDesktop && d.DesktopFn != nil && ev.Priority >= 3 {
		d.DesktopFn(ev.Title, ev.Body)
	}
	if d.G.Ntfy.Topic != "" {
		if err := d.SendNtfy(ev); err != nil {
			d.logf("notify: ntfy failed: %v", err)
		}
	}
	if d.G.Telegram.Token != "" && d.G.Telegram.ChatID != 0 {
		if err := d.SendTelegram(context.Background(), ev); err != nil {
			d.logf("notify: telegram failed: %v", err)
		}
	}
}

// ---------- ntfy ----------

// NewTopic generates an unguessable ntfy topic. On the public server a topic
// name is the only protection, so treat it like a password.
func NewTopic() string {
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	return "hloop-" + hex.EncodeToString(b)
}

func (d *Dispatcher) SendNtfy(ev Event) error {
	server := strings.TrimRight(d.G.Ntfy.Server, "/")
	if server == "" {
		server = "https://ntfy.sh"
	}
	body := truncate(ev.Body, 3500)
	if body == "" {
		body = ev.Title
	}
	req, err := http.NewRequest("POST", server+"/"+d.G.Ntfy.Topic, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Title", asciiHeader(ev.Title))
	req.Header.Set("Priority", fmt.Sprint(clamp(ev.Priority, 1, 5)))
	if t := tags[ev.Kind]; t != "" {
		req.Header.Set("Tags", t)
	}
	if d.G.Ntfy.Token != "" {
		req.Header.Set("Authorization", "Bearer "+d.G.Ntfy.Token)
	}
	resp, err := d.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("ntfy server returned %s", resp.Status)
	}
	return nil
}

func asciiHeader(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 32 && r < 127 {
			b.WriteRune(r)
		} else {
			b.WriteRune('?')
		}
	}
	return b.String()
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

// ---------- Telegram client ----------

type TG struct {
	Token string
	Base  string
	HTTP  *http.Client
}

func NewTG(token string) *TG {
	return &TG{Token: token, Base: "https://api.telegram.org", HTTP: &http.Client{Timeout: 70 * time.Second}}
}

type User struct {
	ID       int64  `json:"id"`
	IsBot    bool   `json:"is_bot"`
	Username string `json:"username"`
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type Message struct {
	MessageID int64    `json:"message_id"`
	From      *User    `json:"from"`
	Chat      Chat     `json:"chat"`
	Text      string   `json:"text"`
	ReplyTo   *Message `json:"reply_to_message"`
}

type Callback struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

type Update struct {
	UpdateID int64     `json:"update_id"`
	Message  *Message  `json:"message"`
	Callback *Callback `json:"callback_query"`
}

// scrub removes the bot token from error text (it appears in request URLs).
func (t *TG) scrub(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(strings.ReplaceAll(err.Error(), t.Token, "<token>"))
}

func (t *TG) call(ctx context.Context, method string, params, out any) error {
	b, _ := json.Marshal(params)
	req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/bot%s/%s", t.Base, t.Token, method), bytes.NewReader(b))
	if err != nil {
		return t.scrub(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.HTTP.Do(req)
	if err != nil {
		return t.scrub(err)
	}
	defer resp.Body.Close()
	var env struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return fmt.Errorf("telegram %s: bad response (%s)", method, resp.Status)
	}
	if !env.OK {
		return fmt.Errorf("telegram %s: %s", method, env.Description)
	}
	if out != nil {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

func (t *TG) GetMe(ctx context.Context) (User, error) {
	var u User
	err := t.call(ctx, "getMe", map[string]any{}, &u)
	return u, err
}

func keyboard(rows [][]Button) any {
	if len(rows) == 0 {
		return nil
	}
	var kb [][]map[string]string
	for _, r := range rows {
		var row []map[string]string
		for _, b := range r {
			row = append(row, map[string]string{"text": b.Text, "callback_data": b.Data})
		}
		kb = append(kb, row)
	}
	return map[string]any{"inline_keyboard": kb}
}

// SendMessage sends plain text (no markup parsing) and returns the message id.
func (t *TG) SendMessage(ctx context.Context, chatID int64, text string, silent bool, rows [][]Button) (int64, error) {
	p := map[string]any{"chat_id": chatID, "text": truncate(text, 3900), "disable_notification": silent}
	if kb := keyboard(rows); kb != nil {
		p["reply_markup"] = kb
	}
	var m Message
	if err := t.call(ctx, "sendMessage", p, &m); err != nil {
		return 0, err
	}
	return m.MessageID, nil
}

func (t *TG) GetUpdates(ctx context.Context, offset int64, timeoutSec int) ([]Update, error) {
	var ups []Update
	err := t.call(ctx, "getUpdates", map[string]any{"offset": offset, "timeout": timeoutSec, "allowed_updates": []string{"message", "callback_query"}}, &ups)
	return ups, err
}

func (t *TG) AnswerCallback(ctx context.Context, id, text string) error {
	return t.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": id, "text": truncate(text, 190)}, nil)
}

// ---------- pairing with a one-time code ----------

const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no look-alike characters

// NewPairCode returns a short one-time code.
func NewPairCode() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	out := make([]byte, 8)
	for i := range b {
		out[i] = codeAlphabet[int(b[i])%len(codeAlphabet)]
	}
	return string(out)
}

// Pair waits for a private message containing the code and returns who sent it.
// Accepted forms: "/start CODE" (what the t.me deep link sends), "/pair CODE", "CODE".
func (t *TG) Pair(ctx context.Context, code string, timeout time.Duration) (User, Chat, int64, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	st := store.LoadTGState()
	offset := st.Offset
	wrong := 0
	for {
		ups, err := t.GetUpdates(ctx, offset, 20)
		if err != nil {
			if ctx.Err() != nil {
				return User{}, Chat{}, offset, errors.New("pairing timed out; run the setup command again for a new code")
			}
			return User{}, Chat{}, offset, err
		}
		for _, u := range ups {
			offset = u.UpdateID + 1
			m := u.Message
			if m == nil || m.From == nil || m.From.IsBot || m.Chat.Type != "private" {
				continue
			}
			fields := strings.Fields(m.Text)
			if len(fields) == 0 {
				continue
			}
			cmd := strings.ToLower(strings.SplitN(fields[0], "@", 2)[0])
			cand := ""
			switch {
			case len(fields) == 2 && (cmd == "/start" || cmd == "/pair"):
				cand = fields[1]
			case len(fields) == 1 && !strings.HasPrefix(fields[0], "/"):
				cand = fields[0]
			case len(fields) == 1 && cmd == "/start":
				_, _ = t.SendMessage(ctx, m.Chat.ID, "Hi! To pair this chat, send:  /pair YOURCODE  (the code shown on your computer).", false, nil)
				continue
			}
			if cand != "" && subtle.ConstantTimeCompare([]byte(strings.ToUpper(cand)), []byte(code)) == 1 {
				return *m.From, m.Chat, offset, nil
			}
			wrong++
			_, _ = t.SendMessage(ctx, m.Chat.ID, "That pairing code isn't right. Run the setup command on your computer to get a fresh one.", false, nil)
			if wrong >= 5 {
				return User{}, Chat{}, offset, errors.New("too many wrong pairing attempts; run setup again")
			}
		}
	}
}

// ---------- Telegram delivery ----------

func (d *Dispatcher) tg() *TG {
	t := NewTG(d.G.Telegram.Token)
	if d.TGBase != "" {
		t.Base = d.TGBase
	}
	if d.HTTP != nil {
		t.HTTP = d.HTTP
	}
	return t
}

func (d *Dispatcher) SendTelegram(ctx context.Context, ev Event) error {
	text := ev.Title
	if e := emoji[ev.Kind]; e != "" {
		text = e + " " + text
	}
	if ev.Body != "" {
		text += "\n\n" + ev.Body
	}
	if ev.Kind == KindBlocker && ev.Ref != "" {
		text += "\n\nReply to this message to answer " + ev.Ref + "."
	}
	var rows [][]Button
	if ev.Project != "" {
		switch ev.Kind {
		case KindComplete, KindDigest, KindError:
			rows = [][]Button{{{"Status", "status:" + ev.Project}, {"Run now", "run:" + ev.Project}}}
		case KindRateLimited, KindReview, KindCheck:
			rows = [][]Button{{{"Status", "status:" + ev.Project}}}
		}
	}
	id, err := d.tg().SendMessage(ctx, d.G.Telegram.ChatID, text, ev.Priority <= 2, rows)
	if err != nil {
		return err
	}
	if ev.Ref != "" && ev.Project != "" {
		_ = store.UpdateTGState(func(s *store.TGState) {
			s.Messages[fmt.Sprintf("%d:%d", d.G.Telegram.ChatID, id)] = store.MsgRef{Project: ev.Project, Ref: ev.Ref}
		})
	}
	return nil
}
