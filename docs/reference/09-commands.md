# Command reference

Every command accepts `-C <dir-or-project-name>` to act on a project other than the current folder. `hloop <command> --help` shows a command's flags.

## Learning

| Command | Does |
|---|---|
| `hloop tour [--step N] [--list] [--keep]` | interactive walkthrough on a throwaway demo project; rerun any time |
| `hloop docs [topic] [--all] [--export DIR]` | this reference; `hloop docs find <word>` searches it |
| `hloop help [command]` | usage, or the reference page for a command |
| `hloop version` | installed version |

## Setting up

| Command | Does |
|---|---|
| `hloop init [--ai] [--from PATH]... [--yes] [--adapter NAME] [--name NAME]` | new project: questionnaire, or an AI interview (`--ai`) |
| `hloop init --verify` | are the documents complete enough to start? |
| `hloop init --upgrade [--refresh-prompts]` | add what a project made by an older hloop is missing |
| `hloop adopt [--interactive] [--print-prompt] [--no-run]` | existing project: an agent writes `.loop/` from the repo and its docs |
| `hloop projects [list\|add DIR\|remove NAME]` | the project registry |
| `hloop doctor` | check the whole setup |
| `hloop trust [--show] [--yes]` | review and approve the loop configuration |
| `hloop skill install [--agents-md] [--global]` | let interactive harness sessions drive hloop |

## Defining work

| Command | Does |
|---|---|
| `hloop spec "FEATURE" [-i] [--from PATH]...` | spec + tasks with criteria for one feature |
| `hloop spec --task T5 [-i]` | specify an existing task |
| `hloop spec --project [-i] [--from PATH]...` | define or revise the whole project (PRD, guardrails, first milestones) |
| `hloop spec check [T5]` | lint the definition (no AI) |
| `hloop spec list` | specs and the tasks that use them |
| `hloop plan "INSTRUCTION"` | a planner agent revises documents and the queue |

## Tasks and criteria

| Command | Does |
|---|---|
| `hloop task add "TITLE" [--ac "S :: V"]... [--should ..] [--could ..] [--kind code\|docs\|config\|research] [--spec PATH] [--detail ..] [--top]` | queue a task |
| `hloop task list [--all]` / `show [T5]` | the queue / one task in full |
| `hloop task ac T5 list\|add\|pass\|fail\|reset\|amend\|remove ...` | acceptance criteria (`pass`/`fail` need `--evidence`; `amend` needs `--reason`) |
| `hloop task done [T5] [--commit H] [--summary ..] [--force]` | mark finished (hloop verifies it during a run) |
| `hloop task red [T5] [--commit H]` | record the tests-only commit (TDD) |
| `hloop task report T5` | regenerate and print the completion report |
| `hloop task start\|skip\|reopen\|top\|approve T5` | move a task around |

## Human decisions

| Command | Does |
|---|---|
| `hloop blocker add\|list\|resolve [B2] [--answer ..] [--no-run]` | things only you can do; resolving resumes the loop |
| `hloop decision add\|list\|resolve\|revisit ...` | open choices |
| `hloop idea add\|list\|promote\|drop` | parked ideas |
| `hloop lesson add\|list\|approve\|retire` | durable facts for every agent turn |
| `hloop note "TEXT"` / `note --held` / `--approve N2` / `--discard N2` | steering notes |
| `hloop review run\|submit\|list` | independent review |

## Running

| Command | Does |
|---|---|
| `hloop run [--max-turns N] [--force] [--quiet]` | run the loop now |
| `hloop status [--all] [--json]` | where things stand |
| `hloop pause` / `resume` | stop / continue (resume also clears backoff and failure counters) |
| `hloop logs [-f] [-n N] [--agent]` | loop log, or the latest agent output |
| `hloop schedule install [--every HOURS] \| remove \| status` | run automatically |

## Releasing

| Command | Does |
|---|---|
| `hloop release [VERSION] [--bump major\|minor\|patch] [--allow-pending] [--no-commit] [--tag] [-i] [--dry-run]` | checks, release notes, CHANGELOG, user and developer docs (`hloop docs releases`) |
| `hloop release list` / `release notes [VERSION]` | past releases / a release's notes |

## Verification

| Command | Does |
|---|---|
| `hloop gate add NAME [--where worktree\|export] [--kinds ..] [--timeout MIN] [--replace] -- COMMAND ARGS...` | a check hloop runs itself |
| `hloop gate list\|remove NAME\|run [NAME]` | manage and try gates |

## Agents and harnesses

| Command | Does |
|---|---|
| `hloop agent catalog\|install NAME...` | ready-made agents |
| `hloop agent add NAME --role .. --adapter .. [--trigger ..] [--mode ..] [--tier ..]` | your own agent |
| `hloop agent list\|order ..\|remove NAME\|run NAME [instruction]\|tier NAME TIER` | manage agents |
| `hloop adapter list\|show\|check\|test\|add\|import\|learn\|guide` | harness definitions |

## Phone

| Command | Does |
|---|---|
| `hloop notify setup ntfy\|telegram` | connect a phone |
| `hloop notify test\|status\|off\|listen` | try, inspect, disable, run the chat listener |
| `hloop notify service install\|remove` | keep the listener running |

## Configuration

`hloop config show` / `hloop config set KEY VALUE`:

| Key | Default | Meaning |
|---|---|---|
| `name` | folder name | project name |
| `max_turns_per_run` | 25 | turns before a run stops |
| `turn_pause_seconds` | 5 | pause between turns |
| `rate_limit_backoff_minutes` | 300 | wait after the harness quota runs out |
| `max_consecutive_failures` | 2 | harness failures before the run stops |
| `agent_timeout_minutes` | 120 | one agent session's limit |
| `max_stalled_turns` | 3 | turns on one task without finishing before you are asked (`off` to disable) |
| `remote_tasks` | approve | `auto` lets phone tasks and notes skip approval |
| `auto_resume` | true | start a run as soon as you answer the last blocker |
| `commit_ledger` | true | commit `.loop/` after each turn |
| `notify_events` | all | see `hloop docs phone` |
| `path_extra` | | extra PATH entries for harnesses and gates |
| `tdd` | off | red/green proof for code tasks |
| `guard.protected_branches` | main,master | for `protected_commit` |
| `guard.history_rewrite` | block | block, notify or off |
| `guard.protected_commit` | notify | |
| `guard.dependency_change` | notify | |
| `guard.mass_delete` | notify | |
| `guard.mass_delete_count` | 10 | |

`HLOOP_HOME` relocates the user-level settings (default `~/.config/harness-loop`).
