# Migrating from antigravity-harness / harness-runner

Use `hloop adopt` in each project. It reads your existing `PROJECT_SCOPE`, `PROGRESS`, `DECISIONS`, and similar files and has your harness translate them into the new `.loop/` layout, including the history of completed tasks, open blockers and decisions. Your old files are never deleted or rewritten; they are listed as "Legacy documents" in the new scope file so you can remove them when you're happy.

```sh
cd my-project
hloop adopt --adapter agy        # or --print-prompt to review the instructions first
hloop status
hloop run
```

Old cron entries or launchd jobs that call `run_antigravity.sh` should be removed; `hloop schedule install` replaces them.

Differences to be aware of:

- `PROGRESS.md` and `DECISIONS.md` are now **generated**. Change them with `hloop task` / `hloop decision`, not an editor.
- Rate-limit detection is based on exit code plus output patterns per adapter, not a single shell grep.
- The loop file lock and state live in `.loop/` instead of the repo root.

## Publishing this repo

1. Run `scripts/set-repo.sh <user>/<repo>` (or set `HLOOP_REPO`). Full steps are in [PUBLISHING.md](PUBLISHING.md).
2. Optionally rename the Go module in `go.mod` and the imports (`hloop/internal/...`).
3. Tag a release (`git tag v0.6.0 && git push --tags`); `.github/workflows/release.yml` builds and attaches the archives and checksums.

## From hloop 0.3.0 to 0.4.0

Install the new binary, then in each project:

```sh
hloop init --upgrade --refresh-prompts   # adds GUARDRAILS.md, CURRENT.md, specs/, reports/, the spec-writer prompt; refreshes role prompts (.bak kept)
hloop gate add test -- <your test command>
hloop spec check                          # see which queued tasks need acceptance criteria
```

Leave out `--refresh-prompts` if you edited `.loop/prompts/builder.md` (or reviewer/planner) yourself, and compare with the new ones by hand: the 0.4.0 prompts are what teach agents about criteria, gates and CURRENT.md. Fill in the "Project-specific boundaries" section of `.loop/GUARDRAILS.md`, then `hloop trust`. Existing tasks keep working without criteria; `hloop spec --task <ID>` adds them.

## From hloop 0.4.0 to 0.5.0

```sh
hloop init --upgrade --refresh-prompts   # renames PROJECT_SCOPE.md to PRD.md; refreshes prompts (.bak kept)
hloop spec --project                      # optional: let an agent fill the new PRD sections (goals, non-goals, metrics)
```

`PROGRESS_LOG.md` appears by itself the next time the ledger changes. Specs written by 0.4.0 as `specs/<name>.md` keep working; new ones go in `specs/<name>/spec.md`.
