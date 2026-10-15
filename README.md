# hloop

Run an autonomous coding loop with **any** AI coding harness: Claude Code, Codex CLI, OpenCode, Gemini CLI, GitHub Copilot CLI, Cursor, Devin, Muse, OpenClaw, Antigravity `agy`, or anything else with a command-line mode.

`hloop` is the supervisor; your harness is the worker. The harness writes code. `hloop` owns the project definition, the task list, scheduling, rate-limit handling, review agents, phone notifications, and the guardrails. It is a single static binary for macOS, Linux and Windows, with no runtime dependencies.

**New to hloop?** Run `hloop tour`: an interactive walkthrough on a throwaway demo project (no AI, nothing touched; rerun it any time). The full reference is built in: `hloop docs` (also in [docs/reference/](docs/reference/)).

Since 0.4.0 hloop also decides when work is **actually** done instead of taking the agent's word for it: tasks carry acceptance criteria, hloop runs your tests itself (gates), can require proof that tests failed before the code was written (red/green), watches git for the things agents must never do, and writes a completion report for every finished task. Defining the work borrows from spec-driven development ([SpecSpark](#defining-the-work)): `hloop spec` turns a request (or, with `--project`, your whole idea) into a written spec and checkable tasks. The documents follow industry conventions: a PRD for the product, Spec Kit/Kiro-style `specs/<feature>/spec.md` for features, a decision log, and an `AGENTS.md` pointer for any harness. See [CHANGELOG.md](CHANGELOG.md).

It replaces the two earlier projects (`antigravity-harness`, `harness-runner`). See [docs/MIGRATING.md](docs/MIGRATING.md).

## Install

macOS / Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/eternitycircle/hloop/main/scripts/install.sh | sh
```

Windows (PowerShell):

```powershell
irm https://raw.githubusercontent.com/eternitycircle/hloop/main/scripts/install.ps1 | iex
```

Or download an archive from the Releases page and put `hloop` on your PATH. To build from source: `go build -o hloop ./cmd/hloop` (Go 1.22+).

> Not published yet? See [docs/INSTALL.md](docs/INSTALL.md) for installing from the zip or from source, and [docs/PUBLISHING.md](docs/PUBLISHING.md) for pushing to GitHub. Run `scripts/set-repo.sh <user>/<repo>` once to point the one-liners at your repo.

## Quick start: new project

```sh
cd my-project
hloop init                 # asks a few questions, writes .loop/, offers your test command as a gate
hloop spec -i "first feature"   # (optional) an agent interviews you and writes a spec + tasks with criteria
hloop run                  # runs the loop now
hloop schedule install     # runs every 8 hours (cron / launchd / Task Scheduler)
```

`hloop init --ai` lets your harness interview you interactively instead and write the PRD, guardrails and first tasks (`--from notes.md` hands it your raw notes first). `--yes` accepts detected defaults. Anything unanswered is left as a `[[FILL: ...]]` marker; `hloop init --verify` lists what is still open.

## Quick start: project already in development

```sh
cd my-existing-project
hloop adopt
```

`adopt` scans the repo (git history, manifests, docs, test commands; it only records the *names* of secret-like files), then gives your harness exact written instructions to create the `.loop/` files from your existing documents. It may only touch `.loop/`, never rewrites your docs, and records what is already done in the task list so the loop starts from where you actually are.

**What happens to files you already have** (PROGRESS.md, DECISIONS.md, a deploy doc, README, CLAUDE.md...): nothing. hloop never deletes, moves or rewrites them. The harness *reads* them and writes the new, separate files in `.loop/`, translating what they say: finished work becomes done tasks, open questions become decisions, planned work becomes the queue. The old files are listed in `.loop/PRD.md` under "Legacy documents" so agents know they exist and which one `.loop/` now replaces. Once you are happy, delete or archive the old ones yourself, otherwise they will slowly drift out of date next to the generated `.loop/PROGRESS.md`. Anything it could not determine is left as a `[[FILL: ...]]` marker, never invented.

- `hloop adopt --print-prompt` prints the instructions so you can paste them into any harness yourself.
- `hloop adopt --no-run` creates `.loop/` and `.loop/adopt-instructions.md` without running anything.
- `hloop adopt --interactive` runs the harness interactively so it can ask you questions.

**Sharing a project.** `.loop/` is meant to be committed, so teammates (or you on another machine) get the same loop. On a fresh clone hloop refuses to start anything until that person runs `hloop trust --show` and `hloop trust`: the project's adapters and prompts decide what programs run, so they must be reviewed by whoever runs them.

## What lives in `.loop/`

Everything is in one hidden folder, edited through commands rather than by hand.

| File | What it is |
|---|---|
| `config.json` | agents, gates, guards, limits, notification events |
| `PRD.md` | the Product Requirements Document: overview, goals and non-goals, requirements, stack, constraints, milestones, success metrics (written by init/adopt/`spec --project`/plan; was `PROJECT_SCOPE.md` before 0.5.0) |
| `ARCHITECTURE.md` | how it is built (optional) |
| `GUARDRAILS.md` | **yours**: what agents may do alone, what needs a blocker first, what they must never do. Part of the approved configuration: an agent editing it stops the loop until you `hloop trust` |
| `CURRENT.md` | short snapshot the builder **rewrites** (never appends) at the end of every turn, so the next fresh session knows what is half-done |
| `specs/<feature>/spec.md` | feature specifications written by `hloop spec` (the Spec Kit / Kiro layout); hloop generates `tasks.md` beside each |
| `ledger.json` | the source of truth: tasks (with acceptance criteria), blockers, decisions, reviews, ideas, lessons |
| `PROGRESS.md`, `DECISIONS.md`, `LESSONS.md` | **generated** read-only views of the ledger (PROGRESS shows the current state and the last 10 finished tasks, so the file agents read every turn stays small) |
| `PROGRESS_LOG.md` | **generated**: the complete history, never trimmed: every finished task with commit, verification and report link, skipped tasks, and a dated timeline (queued, rejected, finished, blockers, answers, reviews, guardrail events, ideas, lessons) |
| `reports/` | a completion report per finished task, written by hloop itself |
| `prompts/` | instructions per agent role (builder, reviewer, planner, spec-writer, ...) |
| `adapters/` | optional project-specific harness definitions |
| `runs/` | logs and agent output (git-ignored, private) |
| `context/`, `plans/`, `release/`, `reviews/`, `retro/` | notes written by the context-curator, architect, release-manager, reviewer and retrospective agents |

## Defining the work

The builder starts every turn with no memory and nobody to ask, so anything left unsaid gets guessed. hloop's definition tools remove the guessing (the approach is adapted from SpecSpark's discovery → spec → implement → independent check pipeline):

```sh
hloop spec "export the table as CSV"        # an agent reads the code and writes .loop/specs/csv-export/spec.md + tasks
hloop spec -i "export the table as CSV"     # same, but it interviews you first
hloop spec --task T7                        # specify an existing task properly
hloop spec --project -i                     # the same for the whole project: PRD, guardrails, first milestones
hloop spec --project --from notes.md --from research/   # unattended, from your raw material
hloop spec check                            # lint, no AI: missing/vague criteria, unfinished specs, tasks waiting on open decisions
```

Two levels, same tool: `--project` writes or revises the PRD (what `init --ai` does for a new project), a feature request writes a feature spec. `-i` interviews you; without it the agent works from the repository and your `--from` documents and leaves `[[FILL: question]]` markers and decisions for anything only you can answer.

A feature spec has fixed sections: **Problem**, **User stories**, **Out of scope**, **Behaviour** (including what happens when each step fails), **Field mapping** (when data moves between places), **Test boundaries** (where tests observe the behaviour and which command runs them), **Stop conditions**, **Tasks**, **Open questions**. The spec writer sorts work into code, docs, research and *decisions* (questions only you can answer go to `hloop decision add`, not into the queue), and leaves `[[FILL: question]]` instead of inventing answers when you are not there.

**Acceptance criteria.** Every task can carry Must / Should / Could criteria, each a checkable statement plus how it is verified:

```sh
hloop task add "Add CSV export" \
  --ac "Export downloads rows.csv with the visible rows :: e2e test export.spec.ts" \
  --ac "The first line is a header row :: unit test csv_test.go" \
  --should "The file name contains today's date"
hloop task ac T5 add "Empty table exports a header only :: unit test"
hloop task show T5
```

The builder must mark each Must criterion `pass` with evidence (`hloop task ac T5 pass AC1 --evidence "..."`) before `task done` is accepted. Only Must blocks. Criteria are fixed once work starts: the builder and reviewers cannot change them; you can (`hloop task ac T5 amend AC2 "..." --reason "..."`, recorded with the old text).

`init --ai`, `adopt`, `plan` and the `architect` profile all write criteria the same way.

## How hloop checks the work

1. **Gates.** Commands hloop runs itself when an agent marks a task done. If one fails, the task is reopened and the agent's next turn is told exactly what failed. After `max_stalled_turns` rejections you get a blocker.
   ```sh
   hloop gate add test -- go test ./...
   hloop gate add test --where export --replace -- sh -c "npm ci && npm test"   # on a clean copy of the last commit
   hloop gate run
   ```
   `--where export` runs the gate on a fresh `git archive` of HEAD, which catches tests that only pass because of files nobody committed. Gates apply to `code` and `config` tasks by default (`--kinds`).
2. **Red/green proof** (`hloop config set tdd on`). For code tasks the builder must commit the failing tests on their own and record that commit (`hloop task red`). hloop then checks, on clean copies, that the gates fail at that commit and pass at the end. A "red" commit that already passes is rejected.
3. **Guardrail detectors.** After every turn of an agent that may change the project, hloop looks at git: history rewritten (amend/reset/force push) → **block**; commits directly on `main`/`master` → notify; dependency manifests or lock files changed → notify; many files deleted → notify. Each is configurable (`hloop config set guard.dependency_change block`, `guard.protected_branches`, ...). They detect; they do not prevent (see SECURITY.md).
4. **Independent review.** Reviewers run in a fresh session and are handed each finished task's own criteria and hloop's verification, and told to judge the work against them as a stranger would.
5. **Completion report.** `.loop/reports/T5.md`: criteria with evidence, gate and red/green results, which detectors fired, commits, files changed, reviews. Written by hloop, not by an agent. Regenerate with `hloop task report T5`.

## Releases and documentation

When the tasks are finished and verified:

```sh
hloop release                # 0.1.0 the first time, then the next patch (--bump minor|major, or give a version)
hloop release --dry-run      # the checks and the brief, without writing anything
```

hloop checks the project is ready (nothing in progress, no blockers, a clean tree, every gate passing). It then hands a factual brief of everything since the last release to a **technical writer and support agent**; the first time, the brief covers the whole project. The writer produces:

- release notes (`docs/releases/v<version>.md`);
- `CHANGELOG.md` (Keep a Changelog, Semantic Versioning);
- user and developer documentation organised the Diátaxis way: README, install guide, getting started, how-to guides, reference, troubleshooting and FAQ, upgrade guide, developer guide, and contributing and security pages when they apply.

The writer may change only docs (`docs` mode). hloop checks the notes and changelog exist, commits the documents, records the release, and tags it with `--tag`. Details: `hloop docs releases`.

## Ideas and lessons

- `hloop idea add "dark mode someday"` (or `/idea` from your phone) parks something without queueing it. Ideas never run. `hloop idea promote I3` turns one into a task.
- `hloop lesson add "start the DB with docker compose up -d before tests"` records a durable fact that every agent turn is told. Lessons an agent proposes (`hloop lesson add` inside a run) wait for your approval (`hloop lesson approve L2`, or `/approve L2` on the phone), because they go into the prompt. The `retrospective` profile looks for repeating mistakes after a run and proposes lessons and fixes.

## Everyday commands

```sh
hloop status [--all]                 # where things stand
hloop task add "Add CSV export" --ac "..." --top
hloop task list | show | done | ac | red | report | skip | top | approve
hloop blocker list | resolve <id> --answer "use Postgres"
hloop decision add | list | resolve
hloop idea add | list | promote | drop
hloop lesson add | list | approve | retire
hloop note "focus on the login page first"   # one-shot steering for the next turn
hloop spec "feature" [-i] | spec check | spec list
hloop plan "split the importer into two milestones"
hloop gate add | list | remove | run
hloop pause | resume
hloop logs -f
hloop doctor
hloop trust --show | trust     # review/approve a project whose .loop/ came from elsewhere or changed
hloop skill install [--agents-md]   # let interactive Claude Code / Codex sessions drive hloop; --agents-md adds a marked pointer section to AGENTS.md and CLAUDE.md
hloop release [VERSION] | release list | release notes   # release notes, CHANGELOG, user + developer docs
hloop tour                     # interactive walkthrough (rerun any time; --step N, --list)
hloop docs [topic] | docs find <word> | help <command>   # built-in reference
```

Use `-C <dir-or-name>` to act on another project (`hloop projects` manages the registry). Agents inside a run get `HLOOP_PROJECT` set, so they never need `-C`.

## How a run works

Each turn: build a prompt (role prompt + generated run context: current task with its criteria and gates, guardrails, lessons, steering notes) → run the harness → run the guardrail detectors → if the agent marked the task done, verify it (criteria, gates, red/green) and reopen it if anything fails → write the completion report → run the agents due after a milestone → commit the `.loop/` ledger → next turn. A run ends as one of:

`done` · `blocked` (waiting on you) · `rate_limited` (backs off, default 300 min) · `error` (stops after 2 consecutive failures) · `max_turns` · `paused`

When the agent needs a human decision it files a blocker and the loop stops; answering it (`hloop blocker resolve`, or from your phone) injects your answer into the next prompt and resumes.

## Multiple agents

An *agent* in hloop is: a harness + instructions + a mode + a trigger. Who does the work (the harness) and who is scheduled to do what (hloop) are separate jobs: hloop decides which agent runs when and with what limits, and passes work between them through the ledger and git, not through a shared chat. The first agent is the builder. Agents can use different harnesses.

**Ready-made agents.** Pick from the catalog; each is a plain prompt file you can edit afterwards.

```sh
hloop agent catalog                              # what's available
hloop agent install security-auditor test-engineer docs-writer
hloop agent install                              # interactive menu
hloop agent install architect --adapter claude-code   # run this one on a different harness
```

| Profile | What it does | Runs |
|---|---|---|
| `architect` | turns a vague request into a precise plan, queued as tasks (no code) | on demand: `hloop agent run architect "add CSV export"` |
| `context-curator` | maintains a compact codebase map the builder reads first | after each finished task |
| `test-engineer` | writes independent tests so the builder doesn't grade its own work | after each finished task |
| `security-auditor` | security review; Pass/Fail with concrete fix-tasks | after each finished task |
| `performance-reviewer` | flags only the performance problems that matter at your scale | after each finished task |
| `docs-writer` | keeps README/API docs/docstrings accurate | every 3 turns |
| `release-manager` | drafts commit message, PR description, changelog in `.loop/release/` | end of each run |
| `retrospective` | finds mistakes that keep repeating (rejected finishes, change requests, blockers) and proposes lessons, decisions or fix-tasks | end of each run |

Each profile has a **model tier** (`fast`, `standard`, `critical`) that the adapter maps to a model: for Claude Code `haiku`, `sonnet` and `opus`. Change one with `hloop agent tier security-auditor critical`, or `hloop agent tier builder default` for the harness's default. Other adapters use their default model unless you add a `"tiers"` entry to the adapter JSON.

The "coder" is your builder (`.loop/prompts/builder.md`). Add your own profiles as markdown files in `~/.config/harness-loop/profiles/` (same format as the built-in ones; see `internal/scaffold/profiles/`). Every extra agent is another harness run, so it uses more of your usage allowance: install the ones you will act on.

**Custom agents** (`hloop agent add`):

```sh
hloop agent add reviewer --role reviewer --adapter claude-code --trigger after_milestone --mode loop-only
hloop agent add docs --role custom --trigger every_n_turns --every-n 5 --prompt-file docs-prompt.md
hloop agent list | order | remove | run <name>
```

- Roles: `builder`, `reviewer`, `planner`, `custom`. Triggers: `every_turn`, `after_milestone` (a task was just finished), `every_n_turns`, `on_run_end`, `manual`.
- `--mode loop-only` agents may only change `.loop/`. hloop snapshots the git working tree before and after, reverts anything out of scope, and tells you. This is a safety net for mistakes, not a sandbox (see Safety).
- Each reviewer is shown everything since *its own* previous review. After more than 2 change-request rounds (`--max-cycles`) hloop opens a blocker for you instead of looping.

## Phone: notifications and two-way control

```sh
hloop notify setup ntfy        # one-way alerts via the ntfy app, random unguessable topic
hloop notify setup telegram    # two-way chat bot, paired with a one-time code
hloop notify test
hloop notify service install   # keep the chat listener running at login/boot
```

Notification events: `blocker`, `complete`, `rate_limited`, `error`, `review`, `digest`, and (0.4.0) `check`: a finish hloop rejected, or a guardrail detector that fired.

Telegram setup: create a bot with @BotFather, paste the token (never stored in shell history), then open the printed link and press Start. The code pairs one user and one private chat; anyone else is ignored. Commands: `/status /tasks /show /add /note /idea /ideas /lessons /blockers /answer /decisions /decide /approve /discard /run /pause /resume /review /projects /use`. `/approve` and `/discard` take a proposed task (T3), a held note (N2), a proposed lesson (L1) or an idea (I4, approve = promote it to a task). Replying to a blocker alert answers it. Tasks and notes sent from the phone are held until you approve them (tap Approve, or `/approve T3` / `/approve N2`; on the computer `hloop task approve`, `hloop note --held`). `hloop config set remote_tasks auto` removes that gate. The bot runs a fixed command allowlist, not arbitrary shell. Choose ntfy if you only want notifications.

## Safety model

Read [SECURITY.md](SECURITY.md) before running this unattended. The short version:

- **An agent run with the default adapters is not sandboxed.** Every built-in harness is started with its approval prompts disabled so they can work unattended. Run hloop in a VM, container or dedicated user, with only the credentials the project needs.
- **Configuration is approved by you.** What programs hloop launches comes from `.loop/config.json` (including gates), `.loop/adapters/` and `.loop/prompts/`, and what agents may do from `.loop/GUARDRAILS.md`. hloop fingerprints these when you set a project up or change it with a command, and refuses to start anything if they differ (a `git clone`/`pull` of someone else's `.loop/`, or an agent editing them). Review with `hloop trust --show`, approve with `hloop trust`.
- **Agents get a short list of commands** (task, blocker, decision, note, review submit, status...). Setup, scheduling, notifications, approvals and running the loop are refused inside an agent run.
- Phone control is limited to one paired Telegram user in a private chat, a fixed command list, and no free-form shell. Tasks and notes from the phone wait for your approval.
- No shell interpolation anywhere: adapters are argv arrays. Bot tokens are scrubbed from errors and stored `0600`; run logs are private.

## Verification status

- **Tests (0.6.0):** 127 unit and end-to-end tests pass on Linux (87 in 0.3.0, counted the same way), also under the race detector. Besides the 0.3.0 suite (tampered config, hostile clones, agent impersonation, path traversal), they cover: agents unable to finish with unmet Must criteria or to change criteria once work starts; gates rejecting a finish and the reason reaching the next turn; repeated rejections ending in a blocker; red/green proof accepted, and rejected when the "red" commit already passes; export gates not seeing uncommitted files; history rewrites blocking the loop; dependency changes recorded; branch switches not mistaken for changes; agent lessons held for approval; ideas never running; an edited GUARDRAILS.md stopping the loop; 0.3.0 configs and ledgers loading unchanged; (0.5.0) the PRD rename and the upgrade path from PROJECT_SCOPE.md; PROGRESS_LOG.md listing every task and every rejection; generated spec tasks.md; `spec --project` with source documents; secret files refused as sources; agents allowed `init --verify` but nothing else of `init`; the docs and help pages; the full tour, run end to end, without touching the user's settings; the AGENTS.md block preserving existing content; (0.6.0) a release refused for unfinished tasks, a dirty tree or a failing gate; dry runs changing nothing; the first release covering the whole project and the next one only what came after; version bumps; the changelog keeping older sections; the writer's code edits reverted while its docs are kept; a release with no notes not recorded; agents unable to release. Cross-compiled and vetted for macOS and Windows (amd64, arm64).
- **Real harness (0.4.0):** with **Claude Code 2.1.300**, `hloop spec` wrote a full spec for a small Go feature (it noticed the repository had no CLI and recorded two decisions instead of guessing), then `hloop run` with TDD on, a test gate and a reviewer: the builder committed failing tests alone, recorded the red commit, implemented on a feature branch and marked each criterion with evidence; hloop proved red/green on clean copies, wrote the report; the reviewer re-ran the checks and approved. That run led to one change: a spec writer may now revise criteria of a task nobody has started.
- **Real harness (0.5.0):** `hloop spec --project --from notes.md` with Claude Code turned a seven-line voice-memo transcript into a complete PRD, project-specific guardrails (never touch the Proxmox host, no internet exposure, no auth until decided), four decisions for the open questions, and seven tasks with 30 criteria between them, leaving precise `[[FILL]]` questions instead of guesses. That run led to one fix (duplicate spec-check warnings).
- **Real harness (0.6.0):** `hloop release --tag` with Claude Code on the 0.4.0-era test project (upgraded with `init --upgrade --refresh-prompts` first). In about 30 seconds the writer produced release notes, a Keep a Changelog CHANGELOG, a getting-started tutorial, an API reference, a developer guide, a docs index and an updated README. It said which pages it skipped and why, left two `[[FILL]]` questions, and flagged one ambiguity in hloop's brief, which led to a fix.
- **Upgrade:** a project created and approved by 0.3.0 stays approved under 0.4.0 and 0.5.0; `hloop init --upgrade` adds the new files (and renames PROJECT_SCOPE.md to PRD.md) without breaking that approval.
- **Earlier (0.3.0):** a full loop with Claude Code 2.1.294; flags checked against Codex CLI 0.162.1, OpenCode 1.18.35, Gemini CLI 0.63.0 and Copilot CLI 1.0.95.
- **Not verified:** the Cursor, Devin, Muse and OpenClaw adapters (vendor documentation only), `agy` against a real `agy`; interactive sessions (`init --ai`, `spec -i`, `adopt --interactive`) were not driven by a person here; a real Telegram bot (tested against a local fake of the API) and a real ntfy server; anything on macOS or Windows at run time (CI on GitHub runs the unit tests on both once pushed). Static scanners (govulncheck, gosec) could not be run in the build environment; the code has no third-party dependencies.

Please report what breaks.

## Harness adapters

Built in: `claude-code`, `codex`, `opencode`, `gemini-cli`, `copilot-cli`, `cursor`, `devin`, `muse`, `openclaw`, `agy`. `hloop adapter list` shows which are installed and how well each is verified.

| Status | Meaning |
|---|---|
| verified | a full loop was run with it (Claude Code only so far) |
| flags-checked | every flag confirmed against the installed CLI's `--help`: Codex, OpenCode, Gemini CLI, Copilot CLI |
| documented | flags from vendor documentation only: Cursor, Devin, Muse, OpenClaw, agy |
| unverified | defined by you or an AI |

Vendors rename flags often, so run `hloop adapter test <name>` once on your machine. It does a tiny real task in a throwaway folder and tells you what is wrong.

**A harness that is not listed:**

```sh
hloop adapter guide                      # step-by-step instructions
hloop adapter learn mytool               # builds a prompt from mytool --help for any AI to turn into an adapter
hloop adapter import reply.json          # strict import of that reply
hloop adapter add mytool --bin mytool --arg run --arg '{prompt}'   # or describe it yourself
hloop adapter test mytool
```

Other commands: `adapter list|show|check`. Full reference, Windows notes and wrapper scripts for tools with no command line: [docs/ADAPTERS.md](docs/ADAPTERS.md).

## Configuration

`hloop config show` / `hloop config set <key> <value>`. Keys: `name`, `max_turns_per_run`, `turn_pause_seconds`, `rate_limit_backoff_minutes`, `max_consecutive_failures`, `agent_timeout_minutes`, `remote_tasks`, `auto_resume`, `commit_ledger`, `notify_events`, `path_extra`, `max_stalled_turns`, `tdd`, `guard.protected_branches`, `guard.history_rewrite`, `guard.protected_commit`, `guard.dependency_change`, `guard.mass_delete`, `guard.mass_delete_count`. Gates are managed with `hloop gate`. Set `HLOOP_HOME` to relocate the user-level config.

Upgrading a project made by an older version: `hloop init --upgrade` (renames PROJECT_SCOPE.md to PRD.md, adds GUARDRAILS.md, CURRENT.md, the spec-writer prompt and the `check` notification; never overwrites). Add `--refresh-prompts` to replace the built-in prompts (builder, reviewer, planner, spec writer, project definer, adopter, installed profiles) with this version's, old ones kept as `.bak`; do this unless you edited them, because the prompts are what teach agents about criteria, gates, CURRENT.md and the PRD. Details: `hloop docs upgrading`.

## License

MIT
