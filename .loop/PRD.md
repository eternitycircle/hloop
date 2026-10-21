# Product Requirements Document (PRD): hloop

What this project is and what it must do. Stable: change it when the product changes, not every turn (current state goes in CURRENT.md, feature detail in specs/, choices in DECISIONS.md).

## 1. Overview
- **Problem / purpose**: Run an autonomous coding loop with **any** AI coding harness (Claude Code, Codex CLI, OpenCode, Gemini CLI, Copilot CLI, Cursor, Devin, Muse, OpenClaw, `agy`, or any CLI). hloop is the supervisor: it owns the project definition, task ledger, scheduling, rate-limit handling, review agents, phone notifications and guardrails; the harness is the worker (README.md).
- **Users**: Developers who run AI coding harnesses unattended on their own repositories (README.md, SECURITY.md). Replaces the earlier `antigravity-harness` and `harness-runner` projects (docs/MIGRATING.md).

## 2. Goals and non-goals
### Goals
- Harness-agnostic: one adapter layer, ten built-in adapters, user-defined adapters (docs/ADAPTERS.md).
- hloop decides when work is actually done (acceptance criteria, gates, red/green proof, guardrail detectors, completion reports) instead of trusting the agent (CHANGELOG 0.4.0).
- Single static binary for macOS, Linux and Windows with no runtime or third-party Go dependencies (README.md, go.mod).
- [[FILL: any measurable success target, e.g. adoption or number of verified adapters?]]
### Non-goals
- Not a sandbox: default adapters run with approval prompts disabled; users are told to use a VM/container/dedicated user (SECURITY.md).
- Does not protect against a determined attacker with a shell in an agent run, or against a hijacked Telegram account (SECURITY.md).
- No free-form shell from the phone; phone control is a fixed command list for one paired user (README.md).

## 3. Requirements
### Functional
- Project definition in `.loop/` (PRD, CURRENT, GUARDRAILS, ARCHITECTURE, DECISIONS, LESSONS, generated PROGRESS and PROGRESS_LOG); `hloop init`, `init --ai`, `adopt`, `init --upgrade`.
- Task ledger with acceptance criteria, ideas, lessons, decisions, blockers (`hloop task|idea|lesson|decision|blocker`).
- Spec flow: `hloop spec [-i] [--project] [--from]`, `spec check`.
- Loop engine: `hloop run`, turns, rate-limit backoff, multiple agents/roles/pipeline, model tiers, scheduling (cron / launchd / Task Scheduler).
- Verification: gates, red/green TDD proof, guardrail detectors, completion reports, reviewers.
- Adapters: list/show/check/test/learn/import/add (`hloop adapter`).
- Phone: Telegram two-way control and ntfy alerts, desktop notifications.
- Trust: config fingerprinting, `hloop trust`.
- Releases: `hloop release` with technical-writer agent producing release notes, changelog, docs.
- Learning: `hloop tour`, `hloop docs`, `hloop skill install`.
### Non-functional
- Platforms: macOS, Linux, Windows (amd64, arm64) cross-compiled (README.md, scripts/build-all.sh). Runtime behaviour on macOS/Windows not yet exercised (README "Verification status").
- Security: no shell interpolation (adapters are argv arrays), secrets scrubbed and stored 0600, private run logs, config fingerprint approval (SECURITY.md).

## 4. Tech stack and infrastructure
- **Runtime / framework**: Go (`go.mod`: `go 1.22`, module `hloop`), standard library only. Packages: `cmd/hloop`, `internal/{adapter,engine,notify,scaffold,sched,store}`.
- **Data / backend**: none; state is files in `.loop/` (ledger.json, config.json, Markdown) plus git.
- **Hosting / deployment**: distributed as GitHub Release binaries built by `.github/workflows/release.yml` on `v*` tags using `scripts/build-all.sh`; install via `scripts/install.sh` / `install.ps1`. CI (`.github/workflows/ci.yml`) runs `go vet` and `go test` on ubuntu/macos/windows with Go 1.24. The workflows have not been run on GitHub yet (docs/PUBLISHING.md).

## 5. Constraints and principles
- Tests: `go test ./...` (CI uses `-race` off Windows); unit and end-to-end tests in `cmd/hloop/*_test.go` and each `internal` package.
- No third-party dependencies (SECURITY.md supply-chain row).
- Keep `.loop/` backward compatible: older ledgers and configs must load unchanged (CHANGELOG "Compatibility").
- Human-owned config (config.json, adapters, prompts, GUARDRAILS.md) is fingerprinted; agents must not edit it.
- Changelog follows Keep a Changelog style with semantic versions (CHANGELOG.md); licence in LICENSE.
- Work on feature branches; do not commit to master/main (GUARDRAILS.md).
- Prefer lightweight, token-efficient CLI tooling over browser automation.

## 6. Milestones
Current version is 0.6.0 (all of 0.3.0 to 0.6.0 shipped; see the task history).
- Verify the build hygiene: vet and static scanners (govulncheck, gosec) were never run.
- Run CI and release workflows on GitHub and fix what fails; resolve the `OWNER` placeholder repo name and the `main`/`master` mismatch (see decisions).
- Verify adapters beyond Claude Code (Cursor, Devin, Muse, OpenClaw, agy) and macOS/Windows runtime; needs vendor accounts and machines (ideas).
- Drive the interactive flows (`init --ai`, `spec -i`, `adopt --interactive`) with a person; real Telegram bot and ntfy server testing.

## 7. Success metrics
- [[FILL: none stated in the documents; what would count as success for hloop?]]

## 8. Legacy documents
- `README.md`: full user guide, safety model and verification status (including what is NOT verified).
- `CHANGELOG.md`: release history 0.3.0 to 0.6.0.
- `SECURITY.md`: threat model, what is and is not protected, hardening checklist.
- `docs/ADAPTERS.md`: adapter status table, how to add a harness, field reference.
- `docs/INSTALL.md`: installing from zip or source.
- `docs/MIGRATING.md`: migration from antigravity-harness and harness-runner.
- `docs/PUBLISHING.md`: pushing to GitHub on a review branch, tagging pre-releases.
- `docs/reference/01-overview.md` to `13-upgrading.md`: reference pages built into the binary (`hloop docs`).
- `LICENSE`: licence text.
