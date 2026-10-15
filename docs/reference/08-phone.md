# Phone: notifications and control

No coding harness runs on a phone, so the phone is a remote: alerts out, answers and small requests in.

## One-way alerts (ntfy)

```sh
hloop notify setup ntfy       # random, unguessable topic; subscribe to it in the ntfy app
hloop notify test
```

## Two-way chat (Telegram)

```sh
hloop notify setup telegram   # create a bot with @BotFather, paste the token, open the link, press Start
hloop notify service install  # keep the chat listener running at login/boot
```

Pairing uses a one-time code and binds one Telegram user in one private chat; everyone else is ignored.

| Command | Does |
|---|---|
| `/status` | where things stand (with Run / Pause / Resume buttons) |
| `/tasks` | current and next tasks |
| `/show [T5]` | a task's criteria, status and verification |
| `/add <task>` | propose a task (held for approval unless `remote_tasks` is `auto`) |
| `/note <text>` | steering note for the next turn (held for approval by default) |
| `/idea <text>` | park an idea (never runs) |
| `/ideas`, `/lessons` | list them; lessons proposed by agents come with Approve buttons |
| `/blockers` | open blockers |
| `/answer B2 <text>` | answer a blocker (or simply reply to the alert); the loop resumes |
| `/decisions`, `/decide D1 <text>` | open decisions, resolve one |
| `/approve <id>`, `/discard <id>` | proposed task (T), held note (N), proposed lesson (L), idea (I: approve promotes it to a task) |
| `/run`, `/pause`, `/resume`, `/review` | control the loop |
| `/projects`, `/use <name>` | switch project |

## Which events notify you

`hloop config set notify_events blocker,complete,rate_limited,error,review,digest,check`

| Event | When |
|---|---|
| `blocker` | the loop needs you |
| `complete` | all tasks done |
| `rate_limited` | quota used up; when it will retry |
| `error` | repeated failures or a setup problem |
| `review` | a reviewer's verdict |
| `digest` | summary at the end of a run |
| `check` | hloop rejected a finish, or a guardrail detector fired |

Security: anyone who controls the paired Telegram account can steer the loop. Turn on Telegram two-step verification, or use ntfy only.
