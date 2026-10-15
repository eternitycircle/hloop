# Security

hloop runs AI coding agents unattended and (optionally) lets you steer them from a phone. That is powerful, so this page says plainly what is protected, what is not, and how to run it safely. Report vulnerabilities privately to the maintainers (use GitHub's "Report a vulnerability" on the repository).

## The one thing to understand

The built-in adapters start the harness with its own approval prompts **disabled** (`--dangerously-skip-permissions` for Claude Code, `--dangerously-bypass-approvals-and-sandbox` for Codex, `--yolo` for Gemini CLI and Muse, `--auto` for OpenCode, and so on) because an unattended agent cannot answer prompts. Anything the agent can be talked into doing, it can do as your user: read files, run commands, use your credentials, reach the network. Text the agent reads (a README, an issue, a web page, a dependency) can try to talk it into things. hloop reduces the blast radius and gives you checkpoints, but it is not a sandbox.

**Recommended setup:** run unattended loops in a VM, container, or a separate OS user that has only the repository and the credentials the project genuinely needs (no cloud admin keys, no SSH keys to production, no browser profile).

## What hloop protects

| Risk | Protection |
|---|---|
| A cloned/pulled `.loop/` launches a hostile program | hloop launches nothing until you approve the project's `config.json` (including gates, which hloop runs itself), `GUARDRAILS.md`, `adapters/` and `prompts/` (`hloop trust`). The approval is a fingerprint stored outside the repo (`~/.config/harness-loop/trust.json`). `doctor`, `adapter check` and every run refuse or skip project-defined adapters until approved. |
| An agent edits its own config/adapters/prompts to escalate | The next run fails the fingerprint check and tells you (and your phone). hloop's own commands (`agent add`, `config set`, ...) keep an already-approved project approved, but never approve a project that was already changed. |
| An agent runs hloop commands that reconfigure the supervisor | Inside an agent run (`HLOOP_AGENT` set) only task, blocker, decision, note, review submit, status, logs and read-only listings work. Scheduling, notification setup, approvals, `run`, `plan`, `init`, `config set`, adapter and agent changes are refused. |
| An agent resolves its own blockers or approves its own tasks | Refused for agents; only a human command resolves them. The same goes for promoting ideas, approving lessons, adding or removing gates, and amending criteria of a task that has started. |
| An agent loosens its own rules | `GUARDRAILS.md` is part of the approved fingerprint: an edit stops the next run until you review it. Lessons an agent proposes are not shown to any agent until you approve them. |
| An agent declares work done that isn't | Gates are run by hloop, not the agent; a failing gate reopens the task. With `tdd on`, hloop checks on clean copies that the tests failed before the implementation and pass after it. |
| An agent rewrites history or commits to `main` | Detected after every turn of a write-mode agent and turned into a blocker or notification (`guard.*` settings). |
| Hostile content in an exported commit | Exports for gates are extracted with path checks (no entries outside the temporary folder; symlinks pointing outside it are skipped). |
| A reviewer/planner changes product code | `loop-only` agents are snapshotted before/after; out-of-scope edits are reverted and reported, a moved `HEAD` is reported. |
| Path traversal via names | Agent, adapter and project names are validated (`[a-z0-9._-]`, no `..`); prompt paths must stay inside `.loop/`; bad config is rejected on load. |
| Stolen/forged phone commands | One paired Telegram user id **and** chat id, private chats only, fixed command list, no free-form shell. Pairing uses a random one-time 8-character code with a 5-attempt limit. Tasks and notes from the phone are held until approved. |
| Secrets leaking through hloop | Telegram/ntfy secrets live in a `0600` file; the bot token is scrubbed from error messages; run logs are `0600` in a `0700` directory; `adopt` records only the *names* of secret-like files and tells agents never to open them. |
| Shell injection | No shell is used to start harnesses; adapters are argv arrays. Cron/launchd/Task Scheduler entries quote and escape paths, and refuse control characters. |
| Tampered installer download | `install.sh` / `install.ps1` verify the SHA-256 of the archive against `checksums.txt` from the same release. |
| A new harness definition (imported, or written by an AI) runs something unexpected | `adapter import` and `adapter learn` parse strictly (unknown fields are errors), refuse an adapter for a different program than the one you named, mark the result `unverified`, and save only after you have seen the JSON. `adapter` changes are refused to agents. |
| Supply chain | hloop has no third-party Go dependencies. |

## What hloop does NOT protect

- **An agent with a shell can bypass the guard rails.** `HLOOP_AGENT` is an environment variable the agent could unset, and an agent can edit `.loop/ledger.json` or any file directly. The command allow-list and `loop-only` revert catch mistakes and simple prompt injection, not a determined attacker. The config fingerprint *does* catch edits to the execution config on the next run, but it cannot stop an agent doing damage during the run it already has. Use a VM/container.
- **Criteria evidence is written by the agent.** hloop checks that every Must criterion is *marked* pass with evidence, not that the evidence is true. What hloop verifies itself is the gates and the red/green proof; the reviewer is told to re-check the evidence. Criteria without a gate behind them are only as good as the reviewer.
- **Guardrail detectors detect; they do not prevent.** A force-push or a commit on `main` has already happened when it is reported. A detector cannot see what a turn did outside git (network calls, files outside the repository).
- **Gates are programs you configured, run as you.** `hloop gate add test -- sh -c "..."` is as powerful as any shell command, and it runs code the agent just wrote (your tests import it). Run unattended loops in a VM/container, as for the harness itself.
- **Prompt injection.** Anything in your repo, dependencies or fetched pages can be read by the agent. hloop labels steering notes and human answers as task input, not instructions, but cannot make a model immune.
- **Your Telegram account is the key to two-way control.** Anyone who controls the paired account (SIM swap, unlocked phone, stolen session) can approve held items and answer blockers, and thereby steer an unattended agent. Turn on Telegram two-step verification and a device lock. If you only want alerts, use ntfy (one-way) and nothing can be sent back.
- **ntfy alerts are not end-to-end encrypted.** They carry task titles and blocker text through the ntfy server (public ntfy.sh by default, where the random topic name is the only protection). Self-host ntfy with an access token for anything sensitive, or use Telegram. Telegram bot messages are not end-to-end encrypted either.
- **`adapter learn --use <harness>` reads untrusted text with an unattended AI.** The help output of a program is given to a harness that runs without approval prompts. A hostile program could put instructions in its `--help`. Use it only for tools you installed on purpose, read the JSON before saying yes, and do not pass `--yes` for a tool you do not trust. The copy-and-paste route (`--print-prompt`, then `import`) avoids running anything unattended.
- **Wrapper scripts and `.cmd` shims.** A wrapper script you point an adapter at is arbitrary code run as you. On Windows, npm-installed harnesses are `.cmd` files run through `cmd.exe`, which mishandles quotes and long prompts; adapters for them should use stdin or `{prompt_file}` (hloop warns).
- **Vendor flag names change.** Only Claude Code has been run end to end; the other built-in adapters are labelled by how much was checked. A wrong flag fails visibly; a changed meaning of an approval or sandbox flag may not, so read the vendor's notes for any harness you rely on.
- **git itself.** hloop runs `git` in your repository; a hostile repository can carry git configuration (for example hooks or `core.fsmonitor`) that git executes. Do not run any tool, hloop included, in a repository you have not looked at.
- **The loop-only guard only sees the working tree.** It does not watch `.git/` internals, files outside the repo, or network activity.
- **macOS and Windows runtime behaviour** has not been exercised yet (see the README's verification status).

## Hardening checklist

1. Run unattended loops in a VM/container/dedicated user.
2. Give the harness the minimum credentials; prefer a fine-scoped token to your personal login.
3. Keep the repository on a branch you can discard; review diffs before merging (the `security-auditor` and `release-manager` profiles help, but you are the final reviewer).
4. Enable Telegram two-step verification, or use ntfy only.
5. After `git clone`/`pull` of a repo that has `.loop/`, run `hloop trust --show` and read the adapters and prompts before approving.
6. Set `agent_timeout_minutes` and `max_turns_per_run` to bound a runaway agent; consider the harness's own budget flags (for example `--max-budget-usd` via an agent's `extra_args`).
7. Build or install releases made with a current Go toolchain.
