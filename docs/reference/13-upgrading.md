# Upgrading

Install the new binary, then in each project:

```sh
hloop init --upgrade                    # adds what is missing, never overwrites
hloop init --upgrade --refresh-prompts  # also replaces the built-in role prompts (old ones kept as .bak)
hloop doctor
```

Use `--refresh-prompts` unless you edited `prompts/builder.md`, `reviewer.md`, `planner.md`, `spec-writer.md` or `interviewer.md` yourself; then compare by hand. The prompts are what teach agents about new features.

## From 0.5.x to 0.6.0

Nothing to migrate. New: `hloop release` (see `hloop docs releases`). `init --upgrade --refresh-prompts` also refreshes the technical writer's prompt once it exists.

## From 0.4.x to 0.5.0

- `PROJECT_SCOPE.md` is renamed to `PRD.md` (the content is unchanged; the new PRD template adds goals and non-goals, non-functional requirements and success metrics; add them with `hloop spec --project`). Until you upgrade, hloop keeps reading `PROJECT_SCOPE.md`.
- New: `PROGRESS_LOG.md`, the complete history (it is generated, so it covers everything already in the ledger).
- Feature specs now live in folders: `specs/<feature>/spec.md`, with a generated `tasks.md`. Specs written by 0.4.0 as `specs/<feature>.md` keep working.

## From 0.3.x

Everything above, plus `GUARDRAILS.md`, `CURRENT.md`, `specs/`, `reports/`, the spec-writer prompt and the `check` notification. Then add a gate (`hloop gate add test -- ...`) and see which tasks need criteria (`hloop spec check`). A project approved by 0.3.0 stays approved.

## From antigravity-harness / harness-runner

Run `hloop adopt` in each project and remove the old cron entry for `run_antigravity.sh`; `hloop schedule install` replaces it. See `docs/MIGRATING.md`.
