# Files in .loop/

Everything hloop knows about a project lives in the hidden `.loop/` folder, which you commit with your code. Generated files are rebuilt from `ledger.json` on every change: edit them through `hloop` commands, never by hand.

## Documents you (and planning agents) write

| File | Standard it follows | What it holds | Who writes it |
|---|---|---|---|
| `PRD.md` | Product Requirements Document | overview, goals and non-goals, requirements, tech stack, constraints, milestones, success metrics. Stable | `init`, `adopt`, `spec --project`, `plan`, you |
| `ARCHITECTURE.md` | the common ARCHITECTURE.md convention | components, data flow, technical risks. Optional | the same, the architect agent |
| `specs/<feature>/spec.md` | spec-driven development (the GitHub Spec Kit / Kiro layout) | one feature: problem, user stories, out of scope, behaviour, field mapping, test boundaries, stop conditions, open questions | `hloop spec`, the architect agent |
| `specs/<feature>/design.md` | as above | optional deeper design (data model, API contract) | the spec writer when needed |
| `GUARDRAILS.md` | (hloop) | approval tiers: do alone / say so / ask first / never; project-specific boundaries; stop conditions. **Part of the approved configuration** | you; init/adopt propose, you approve |
| `CURRENT.md` | (hloop) | a short snapshot for the next turn: what works, what is half-done, what to watch out for. Rewritten every turn, never appended | the builder |

## Generated views (read-only)

| File | What it shows | Who reads it |
|---|---|---|
| `PROGRESS.md` | blockers, current task with criteria, queue, the last 10 finished tasks, ideas, recent reviews | every agent turn; you |
| `PROGRESS_LOG.md` | the **complete** history: every finished task (with commit, verification, report link), skipped tasks, and a dated timeline of everything that happened. Never trimmed | you; agents when they need history |
| `DECISIONS.md` | decision log: Open, Decided, Revisit | agents before touching a decided area |
| `LESSONS.md` | active and proposed lessons | you (agents get active ones in their prompt) |
| `specs/<feature>/tasks.md` | the tasks that carry out that spec, with criteria and status | you, the builder |
| `reports/<task>.md` | completion report: criteria and evidence, gates, red/green, guardrail detectors, commits, files, reviews | you, reviewers |

## Configuration (approved by you)

| Path | What it holds |
|---|---|
| `config.json` | agents and pipeline, gates, TDD, guard settings, limits, notification events (`hloop config`, `hloop agent`, `hloop gate`) |
| `prompts/<role>.md` | instructions for each agent (edit freely, then `hloop trust`) |
| `adapters/<name>.json` | project-specific harness definitions (optional) |
| `GUARDRAILS.md` | see above |

hloop fingerprints these. If anything changes outside an hloop command (a `git pull`, an agent, your editor), no harness starts until you review it: `hloop trust --show`, then `hloop trust`.

## The source of truth

`ledger.json` holds tasks (with criteria, verification results, guardrail events), blockers, decisions, reviews, ideas and lessons. Change it only with `hloop task|blocker|decision|idea|lesson|review` commands.

## Release documents (in the project, not in .loop/)

Written by `hloop release` (see `hloop docs releases`): `docs/releases/v<version>.md` (release notes), `CHANGELOG.md`, `README.md`, and `docs/` (INSTALL, GETTING_STARTED, how-to/, reference/, TROUBLESHOOTING, UPGRADING, DEVELOPER, README index). `.loop/release/brief-v<version>.md` is the brief the writer was given.

## Notes written by agents

`context/map.md` (context-curator), `reviews/`, `release/` (release-manager), `retro/` (retrospective), `plans/` (older architect notes).

## Private, not committed

`runs/` (agent logs and the loop log, readable only by you), `state.json`, lock files, `inbox.jsonl` (steering notes waiting for the next turn).
