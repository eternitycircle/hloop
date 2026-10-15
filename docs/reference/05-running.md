# Running the loop

## A run

```sh
hloop run                   # until done, blocked, out of quota, or the turn cap (default 25)
hloop run --max-turns 3
hloop run --force           # ignore a rate-limit backoff
hloop logs -f               # follow the loop log
hloop logs --agent          # the latest agent's own output
```

Each turn:

1. Collect steering notes; pick the current task (or the next pending one).
2. Build the builder's prompt: its instructions plus the run context: the task and its criteria and spec, the gates that will run, guardrails, lessons, recent answers to blockers, notes.
3. Run the harness.
4. Run the guardrail detectors on git (see `hloop docs verification`).
5. If the builder marked the task done: verify it. Failed → the task is reopened and the next turn is told why. Passed → completion report.
6. Run the other agents that are due (reviewers after a finished task, etc.).
7. Commit `.loop/` so progress lives in git history.

## How a run ends

| Outcome | Meaning | What to do |
|---|---|---|
| `done` | no tasks left | add more, or relax |
| `blocked` | an open blocker needs you | `hloop blocker list`, then `hloop blocker resolve B2 --answer "..."` (the loop resumes by itself) |
| `rate_limited` | the harness quota ran out | nothing: it retries after the backoff (default 300 minutes) |
| `error` | the harness failed twice in a row, or a setup problem | `hloop logs --agent`, `hloop doctor`, then `hloop resume` |
| `max_turns` | the per-run cap was reached | the next scheduled run continues |
| `paused` | you paused it | `hloop resume` |

The loop also opens a blocker by itself when one task makes no progress for `max_stalled_turns` turns in a row (default 3), including when hloop keeps rejecting its finish.

## Steering

```sh
hloop note "focus on the login page first"     # read at the start of the next turn; task input, never a rule change
hloop task top T7                               # do this next
hloop task skip T4
hloop pause | hloop resume
```

## Scheduling

```sh
hloop schedule install      # cron (Linux), launchd (macOS), Task Scheduler (Windows); every 8 hours
hloop schedule status | remove
```

A scheduled run is skipped while the project is paused, blocked, or inside a rate-limit backoff.

## Several projects

`hloop projects` lists registered projects; `-C <name-or-dir>` makes any command act on another project; `hloop status --all` shows them all.
