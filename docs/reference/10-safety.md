# Safety

The full statement is `SECURITY.md` in the source. The short version:

## The one thing to understand

Unattended harnesses run with their own approval prompts **disabled**: anything the agent can be talked into, it can do as your user. hloop narrows what goes wrong and makes it visible; it is not a sandbox. **Run unattended loops in a VM, a container, or a separate user account** with only the credentials the project needs.

## What hloop does for you

- **Nothing runs without your approval.** `config.json` (including gates), `GUARDRAILS.md`, `prompts/` and `adapters/` are fingerprinted. After a `git pull`, an agent's edit or your own, hloop starts nothing until `hloop trust`.
- **Agents get a short command list.** Inside a run they can add and update tasks, criteria, blockers, decisions, ideas, notes and reviews, and read status. They cannot run the loop, change the configuration, approve anything, resolve blockers or decisions, change gates, or change criteria of a started task.
- **Lessons and phone input wait for you.** Lessons an agent proposes are not shown to any agent until you approve them. Tasks and notes from the phone are held by default.
- **Planning agents are boxed in.** `loop-only` agents (reviewers, planners, spec writer, project definer) may change only `.loop/`; anything else is reverted and reported.
- **"Done" is checked.** Gates and the red/green proof are run by hloop itself.
- **Risky git actions are detected.** History rewrites stop the loop by default; commits on `main`, dependency changes and mass deletions notify you.

## What it cannot do

- An agent with a shell can still edit files directly (including `.loop/ledger.json`), unset variables, or reach the network. The checks catch mistakes and much prompt injection, not a determined attacker.
- Detectors notice things after they happen.
- Criteria evidence is the agent's own claim; gates and reviewers are what check it.
- Gates are programs you configured, and they run the code the agent just wrote.

## Checklist

1. VM / container / dedicated user for unattended runs.
2. Minimal credentials; no production keys.
3. Review diffs before merging; you are the final reviewer.
4. Telegram two-step verification, or ntfy only.
5. After cloning a repo that has `.loop/`: `hloop trust --show` and read it before approving.
6. Bound runaway agents: `agent_timeout_minutes`, `max_turns_per_run`, and the harness's own budget flags.
