# Troubleshooting

Start with `hloop doctor`: it checks git, the harness, the configuration, approval, gates, the definition and the schedule.

| Symptom | Likely cause | Fix |
|---|---|---|
| "the loop configuration ... has not been approved / changed since you last approved it" | a pull, an agent, or your editor changed `config.json`, `GUARDRAILS.md`, `prompts/` or `adapters/` | `hloop trust --show`, read it, `hloop trust` |
| a run ends `error` right away | harness not installed, not signed in, or a flag changed | `hloop doctor`, `hloop adapter test <name>`, `hloop logs --agent` |
| the same task keeps coming back | hloop rejects the finish (a gate fails, a Must criterion is not marked) | `hloop task show T5` (see the failed checks), `hloop gate run`, `hloop logs --agent` |
| "red/green proof" fails with "fail on a clean copy of HEAD" | the gate needs installed dependencies, which a clean export does not have | make the gate self-contained: `hloop gate add test --replace -- sh -c "npm ci && npm test"`, or `hloop config set tdd off` |
| the builder never marks tasks done | the harness cannot run shell commands (sandbox) or cannot find `hloop` | check the adapter's flags; set `path_extra`; the stall blocker will tell you after 3 turns |
| a blocker about `history_rewrite` | an agent amended, reset or force-pushed | inspect `git reflog`, restore if needed, then `hloop blocker resolve` |
| `rate_limited` | harness quota used up | wait, or `hloop run --force` later |
| nothing runs on schedule | not installed, paused, blocked, or in backoff | `hloop status`, `hloop schedule status`, `hloop doctor` |
| phone commands are ignored | listener not running, or a different user/chat | `hloop notify status`, `hloop notify service install`, re-pair with `hloop notify setup telegram` |
| agents ignore your edits to a prompt | the prompt changed, so the project needs approval | `hloop trust` |
| `spec check` complains about vague wording | a criterion cannot be verified as written | rewrite it as something observable, with `:: how it is verified` |

Logs: `.loop/runs/hloop.log` (the loop) and `.loop/runs/<time>-t<turn>-<agent>.log` (each agent session). They are private (readable only by you) and not committed.
