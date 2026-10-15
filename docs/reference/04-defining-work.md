# Defining the work

The builder starts every turn with no memory and nobody to ask. Anything left unsaid gets guessed. These tools remove the guessing. The approach is spec-driven development: define, then build, then check against the definition.

## Two levels

| Level | Document | Made with |
|---|---|---|
| The whole project | `PRD.md` (+ `GUARDRAILS.md`, `ARCHITECTURE.md`, first milestones) | `hloop init --ai`, `hloop spec --project`, `hloop adopt` |
| One feature | `specs/<feature>/spec.md` + tasks | `hloop spec "<feature>"`, `hloop spec --task T5` |

Both have the same three modes:

```sh
hloop spec --project -i                 # interview me, then write it
hloop spec --project --from notes/      # no interview: read my raw material and the repo, mark what only I can answer
hloop spec "CSV export" -i --from wireframe.png --from research.pdf
```

- `-i` starts an interactive session with your harness: it asks one question at a time, offers defaults, and writes the documents when it could hand them to a stranger.
- Without `-i` it works unattended and never invents answers: anything only you can answer becomes a `[[FILL: question]]` marker or a decision.
- `--from` (repeatable, files or folders) hands it your raw material first: notes, PDFs, research, wireframes, transcripts. It reports what it took from each source and which conflicts it found.

`hloop init --ai` is `spec --project -i` for a brand-new project. Run `spec --project` again any time the product changes; it revises what exists instead of starting over.

## What a feature spec contains

`## Problem` · `## User stories` · `## Out of scope` · `## Behaviour` (including what happens when each step fails) · `## Field mapping` (when data moves between places) · `## Test boundaries` (where tests observe the behaviour and the command that runs them) · `## Stop conditions` · `## Tasks` · `## Open questions`

hloop writes `specs/<feature>/tasks.md` next to it from the ledger.

## Sorting the work

The spec writer sorts everything it hears:

| It is... | It becomes |
|---|---|
| something that runs or can be tested | a task (kind `code`) with criteria |
| something written or configured | a task with `--kind docs` or `--kind config` |
| a question to answer before building | a task with `--kind research` |
| a choice only you can make | a decision (`hloop decision add`), not a task |
| a wish for someday | an idea (`hloop idea add`) |

## Acceptance criteria

```sh
hloop task add "Add CSV export" \
  --ac "Export downloads rows.csv with the visible rows :: e2e test export.spec.ts" \
  --ac "The first line is a header row :: unit test csv_test.go" \
  --should "The file name contains today's date" \
  --could "A progress bar for large tables"
hloop task ac T5 add "Empty table exports a header only :: unit test"
hloop task show T5
```

- Text before `::` is the statement; after it, how it is verified (a test, a command, an observation).
- **Must** (`--ac`) blocks completion; **Should** and **Could** do not.
- The builder proves each Must criterion and records the evidence (`hloop task ac T5 pass AC1 --evidence "..."`); `task done` is refused until every Must is marked pass.
- Criteria are fixed once a task starts: the builder and reviewers cannot change them. Planning agents may revise criteria of a task nobody has started. You can always amend: `hloop task ac T5 amend AC2 "new text :: how" --reason "why"` (the old text is kept).

Good criteria are observable. Not "errors are handled properly" but "submitting an empty email shows 'Email is required' and saves nothing :: e2e signup.spec".

## Checking the definition

```sh
hloop spec check          # all open tasks and specs
hloop spec check T5       # one task
```

No AI involved. It reports:

- **errors**: a spec file that is missing, or unanswered `[[FILL]]` questions in a spec;
- **warnings**: a code task with no Must criterion; a criterion with no "verify by"; vague wording (properly, robust, fast without a number, ...); a task that depends on an open decision; a task too big for one session; a spec missing a standard section; no gates; no GUARDRAILS.md; a CURRENT.md that has turned into a diary.

`hloop init --verify` and `hloop doctor` include it.

## Decisions, ideas, lessons

```sh
hloop decision add "Which database" --options "Postgres | SQLite" --needs "expected data size"
hloop decision resolve D1 --decision "SQLite" --rationale "single user, local"
hloop idea add "dark mode"            # parked; never runs
hloop idea promote I1                 # now it is a task
hloop lesson add "run docker compose up -d before the tests"
```

Agents may add decisions, ideas and lessons; only you resolve decisions, promote ideas, and approve lessons an agent proposed.

## Smaller changes

`hloop plan "split the importer into two milestones"` has a planner agent edit the documents and the queue. `hloop task add` and `hloop note` are for quick, direct changes.
