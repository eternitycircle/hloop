# Changelog

## 0.6.0

### Added
- **`hloop release [VERSION]`**: one command for when the work is done. It does the following, in order:
  - **Checks:** nothing in progress, no open blockers, nothing uncommitted outside `.loop/`, every gate passing on the code as it is now. Unfinished tasks stop the release unless `--allow-pending`.
  - **Brief:** writes a factual release brief (`.loop/release/brief-v<version>.md`). It covers everything since the previous release, or the whole project the first time: tasks with criteria and verification, decisions, open decisions, review findings, lessons, answered blockers, commits, files, and the docs that already exist.
  - **Writing:** runs the technical writer.
  - **Verification:** checks that the release notes exist and the changelog has the version.
  - **Recording:** commits the documents, records the release, and adds an optional `--tag` (annotated, never pushed).
  - Also: `--dry-run`, `--bump major|minor|patch` (from the last release or the highest `vX.Y.Z` tag; 0.1.0 the first time), `--no-commit`, `-i`, `release list`, `release notes [VERSION]`.
- **The technical writer and support agent** (`tech-writer`) writes the documents industry practice expects with a release:
  - release notes per version (`docs/releases/v<version>.md`);
  - `CHANGELOG.md` in the Keep a Changelog format with Semantic Versioning;
  - user and developer documentation organised by the Diátaxis framework: README, INSTALL, GETTING_STARTED (tutorial), how-to guides, reference, TROUBLESHOOTING with FAQ, UPGRADING when needed, DEVELOPER (architecture, setup, build, test, release), CONTRIBUTING and SECURITY when they apply, and a docs index.
  It verifies commands against the code, never invents features, and leaves `[[FILL]]` where only you can answer.
- **`docs` agent mode:** an agent may change only `docs/`, the project's root `*.md` files and `.loop/`; anything else is reverted and reported.
- Releases appear in `PROGRESS_LOG.md` (a Releases table and the timeline). New reference page: `hloop docs releases`.
- `hloop agent add NAME --role tech-writer` (docs mode) to give the writer its own harness or tier permanently.

### Fixed
- Found while testing: a `docs`-mode agent would first have been held to the stricter loop-only scope. Exactly one guard now applies per agent.

## 0.5.0

### Added
- **`PROGRESS_LOG.md`**: the complete, never-trimmed history, generated from the ledger. It lists every finished task (commit, how hloop verified it, link to its report) and skipped tasks, plus a dated timeline of everything: tasks queued, finishes rejected (with the reason), finishes, blockers raised and answered, reviews, guardrail events, ideas and lessons. PROGRESS.md stays small because agents read it every turn, and it points here.
- **`hloop spec --project [-i]`**: the spec flow for the whole project. It writes or revises the PRD, guardrails, CURRENT.md and the first milestones, interviewing you (`-i`) or working unattended from the repo. `hloop init --ai` now uses the same path, so the interview is guarded like every other agent.
- **`--from <file|folder>`** (repeatable) on `spec`, `spec --project` and `init --ai`: hands the agent your raw material first (notes, PDFs, research, wireframes). The agent reports what it took from each source and which conflicts it found. Secret-like files are refused.
- **`hloop tour`**: an interactive walkthrough on a throwaway demo project, rerunnable (`--step N`, `--list`, `--keep`). It runs real hloop commands, including a real loop, with a built-in demo agent instead of an AI. It never touches your projects, registry, trust records or phone settings.
- **`hloop docs [topic]`**: twelve reference pages built into the binary (also in `docs/reference/`), plus `docs find <word>`, `docs --all`, `docs --export DIR`, and `hloop help <command>`.
- **`hloop skill install --agents-md`**: a clearly marked hloop section in the repo's `AGENTS.md` (the cross-tool standard most harnesses read) and `CLAUDE.md`, pointing any session at `.loop/`. Content outside the markers is preserved; reruns update the block.
- `rejections` on tasks: every failed check of every rejected finish is kept for the log and the report.

### Changed
- **`PROJECT_SCOPE.md` is now `PRD.md`**, a Product Requirements Document. The template adds goals and non-goals, functional and non-functional requirements, and success metrics. Projects that still have `PROJECT_SCOPE.md` keep working; `hloop init --upgrade` renames it.
- **Feature specs use the Spec Kit / Kiro folder layout:** `specs/<feature>/spec.md` (with a new User stories section and optional `design.md`). hloop generates `specs/<feature>/tasks.md` from the ledger. 0.4.0 flat specs (`specs/<feature>.md`) keep working.
- The interviewer prompt became the project definer, which works with or without a human. The architect profile writes into `specs/<name>/spec.md`.
- `init --upgrade --refresh-prompts` also refreshes the interviewer and adopter prompts and installed built-in profiles.
- `adopt` discovery recognises existing PRD, product, brief and spec documents.

### Fixed
- Agents can now run `hloop init --verify`, which the interviewer and adopter prompts tell them to do. It had been refused since 0.3.0. Every other use of `init` stays human-only.
- Agents may read `hloop docs` but not `--export` files with it.
- `spec check` no longer repeats a warning when a task mentions the same open decision more than once.

## 0.4.0

The theme of this release: hloop no longer takes an agent's word that work is done, and defining the work borrows from spec-driven development (SpecSpark's discovery → spec → implement → independent check).

### Added
- **Acceptance criteria on tasks.** Must / Should / Could, each a statement plus how it is verified (`--ac "statement :: how verified"`). Agents must mark every Must criterion `pass` with evidence before `task done` is accepted. Criteria are fixed once work starts; only the human can amend (recorded with the old text and a reason). A planning agent may revise criteria of a task nobody has started.
- **Gates** (`hloop gate add|list|remove|run`): commands hloop runs itself when an agent marks a task done. A failure reopens the task and tells the agent's next turn why; repeated failures become a blocker. `--where export` runs the gate on a clean `git archive` of HEAD to catch uncommitted test inputs. Gates are part of the approved (trusted) configuration.
- **Red/green proof** (`hloop config set tdd on`, `hloop task red`): code tasks must record a tests-only commit; hloop proves on clean copies that the gates fail there and pass at the end.
- **Guardrail detectors** after every turn of a write-mode agent: history rewrite (default block), commits on protected branches (notify), dependency manifest/lock changes (notify), mass deletions (notify). Configurable with `guard.*` settings.
- **`.loop/GUARDRAILS.md`**: four approval tiers plus project-specific boundaries and stop conditions. Human-owned and fingerprinted: an agent editing it stops the loop until `hloop trust`.
- **`.loop/CURRENT.md`**: a short state snapshot the builder rewrites (never appends) every turn.
- **Completion reports** in `.loop/reports/<task>.md`, generated by hloop: criteria and evidence, gate and red/green results, detectors, commits, files, reviews. `hloop task report`.
- **`hloop spec`** (`-i` to be interviewed, `--task` for an existing task): a spec-writer agent writes `.loop/specs/<name>.md` with fixed sections (problem, out of scope, behaviour, field mapping, test boundaries, stop conditions, tasks, open questions) and queues tasks with criteria. **`hloop spec check`**: a deterministic lint for missing or vague criteria, criteria with no way to verify them, missing specs, unanswered `[[FILL]]`s, and tasks that depend on open decisions. It feeds `init --verify` and `doctor`.
- **Ideas** (`hloop idea`, `/idea`): a parking lot that never runs until promoted.
- **Lessons** (`hloop lesson`): durable facts injected into every agent turn; agent-proposed lessons wait for human approval.
- **Model tiers** per agent (`fast`, `standard`, `critical`; `hloop agent tier`, `--tier`); built-in mapping for Claude Code (haiku, sonnet, opus); adapters may define `"tiers"`.
- **`retrospective`** agent profile; tiers on all built-in profiles.
- **`hloop skill install`**: writes an Agent Skill (`.claude/skills/hloop`, `.agents/skills/hloop`) so interactive Claude Code / Codex sessions can drive hloop.
- **`hloop init --upgrade [--refresh-prompts]`** for projects made by an older version.
- Phone: `/show`, `/idea`, `/ideas`, `/lessons`; `/approve` and `/discard` also handle lessons (L) and ideas (I).
- Notification event `check` (rejected finishes, guardrail alerts).
- `init` offers the detected test command as the first gate.

### Changed
- Builder, reviewer, planner, interviewer and adopter prompts rewritten around criteria, gates, GUARDRAILS.md and CURRENT.md. Reviewers are given the criteria of each task they review and told to judge them independently.
- The `architect` profile writes criteria and a spec-shaped plan.
- `PROGRESS.md` keeps the last 10 finished tasks (`hloop task list --all` for the rest).
- `trust --show` lists gates and points at GUARDRAILS.md.

### Compatibility
- 0.3.0 `.loop/` folders load unchanged (all new fields are optional) and stay approved; the trust fingerprint is the same for a project without GUARDRAILS.md.
