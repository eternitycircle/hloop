# Current state

## Where things stand
- Branch `master`, working tree clean (only `.loop/` untracked, created by adoption). Origin is a local path.
- Version 0.6.0 is complete and committed: ledger, gates, red/green, guardrail detectors, spec flow, docs pages, tour, releases and technical writer.
- `go test ./...` passes on Linux (all packages ok; cmd/hloop takes about 50 s).
- History: 5 commits (baseline 0.3.0, 0.4.0, 0.5.0, 0.6.0 and a docs commit).

## Half-done / in flight
- Nothing found: only the `master` branch exists, no uncommitted changes.

## Watch out for
- README, install scripts and docs use the placeholder repo `OWNER/harness-loop`; `scripts/set-repo.sh` rewrites it.
- CI triggers on branch `main` but this repo's branch is `master`; CI/release workflows have never run on GitHub.
- CI uses Go 1.24 while go.mod says 1.22.
- Only Claude Code is verified end to end; macOS/Windows runtime, Telegram and ntfy against real servers are untested (README).
- Static scanners (govulncheck, gosec) have not been run.

## Next step
- Run `go vet ./...` and the static scanners, then decide the open questions in DECISIONS.md.
