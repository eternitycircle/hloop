# Agents and harnesses

An **agent** is a harness + instructions + a trigger + a mode. The builder is always first and runs every turn; any others run when their trigger fires. Agents never talk to each other directly: they communicate through the ledger and git.

## Ready-made agents

```sh
hloop agent catalog
hloop agent install security-auditor test-engineer
hloop agent install architect --adapter codex      # on a different harness
```

| Profile | Does | Runs | Tier |
|---|---|---|---|
| `architect` | turns a vague request into a spec and tasks with criteria; no code | on demand (`hloop agent run architect "..."`) | critical |
| `context-curator` | keeps `.loop/context/map.md`, a compact map of the code | after each finished task | fast |
| `test-engineer` | writes independent tests for what the builder finished | after each finished task | standard |
| `security-auditor` | security review, Pass/Fail with fix-tasks | after each finished task | standard |
| `performance-reviewer` | flags only performance problems that matter at your scale | after each finished task | standard |
| `docs-writer` | keeps README/API docs accurate | every 3 turns | fast |
| `release-manager` | drafts commit message, PR text, changelog in `.loop/release/` | end of each run | fast |
| `retrospective` | finds repeating mistakes and proposes lessons, decisions, fixes | end of each run | standard |

The technical writer (`tech-writer`) is not installed from the catalog: `hloop release` runs it. It works in `docs` mode (it may change only `docs/`, root `*.md` files and `.loop/`). See `hloop docs releases`.

Each is a plain prompt in `.loop/prompts/<name>.md` once installed: edit it, then `hloop trust`. Add your own profiles as markdown files in `~/.config/harness-loop/profiles/` (same front-matter format). Every extra agent is another harness run, so it costs usage allowance.

## Your own agents

```sh
hloop agent add reviewer --role reviewer --adapter claude-code --tier fast
hloop agent add docs --role custom --trigger every_n_turns --every-n 5 --prompt-file docs-prompt.md
hloop agent list | order | remove | run <name> | tier <name> <tier>
```

| Setting | Values |
|---|---|
| role | `builder` (only the first), `reviewer`, `planner`, `custom` |
| trigger | `every_turn` (builder), `after_milestone` (a task was verified), `every_n_turns`, `on_run_end`, `manual` |
| mode | `write` (may change the project), `docs` (only `docs/`, root `*.md` and `.loop/`), or `loop-only` (only `.loop/`); anything outside is reverted and reported |
| tier | `fast`, `standard`, `critical`, or default |
| session | `fresh` (default) or `continue` |

## Model tiers

The adapter maps a tier to model flags. Built in for Claude Code: fast → `--model haiku`, standard → `--model sonnet`, critical → `--model opus`. Other harnesses use their default model unless their adapter has a `"tiers"` entry, for example:

```json
"tiers": { "fast": ["-m", "small-model"], "critical": ["-m", "big-model"] }
```

## Harness adapters

Built in: `claude-code`, `codex`, `opencode`, `gemini-cli`, `copilot-cli`, `cursor`, `devin`, `muse`, `openclaw`, `agy`.

```sh
hloop adapter list            # installed? how well verified?
hloop adapter test codex      # a tiny real task in a throwaway folder
hloop adapter guide           # adding a harness that is not listed
hloop adapter learn mytool    # build a prompt from mytool --help for an AI to write the adapter
hloop adapter add mytool --bin mytool --arg run --arg '{prompt}'
```

Verification levels: `verified` (a full loop was run), `flags-checked`, `documented`, `unverified`. Run `hloop adapter test <name>` once on your machine. Details: `docs/ADAPTERS.md` in the source.

## Interactive sessions

`hloop skill install` writes an Agent Skill into the project (`.claude/skills/hloop/` for Claude Code, `.agents/skills/hloop/` for harnesses that read the Agent Skills folder), so when you chat with your harness yourself it knows how to drive hloop: "add a task for X", "what is blocked?". `hloop skill install --agents-md` also adds a short, clearly marked section to the repository's `AGENTS.md` (the cross-tool instructions file most harnesses read) and `CLAUDE.md`, pointing any session at `.loop/`.
