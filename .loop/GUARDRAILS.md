# Guardrails

Owned by the human. Agents read this before acting and never edit it: a change here makes hloop stop until a human approves it (`hloop trust`). To propose a change, an agent files a decision (`hloop decision add`).

When an action fits more than one tier, or you are not sure which tier it is in, treat it as the higher one.

## Tier 1: do it, no need to ask
- Edit source, tests and documentation within the current task's scope.
- Add new files the task needs.
- Run the project's build, tests, linters and formatters.
- Create a feature branch and commit to it.

## Tier 2: do it, and say so in the task summary
- Refactor code next to what the task touches, when it is needed for the task.
- Change a test that was wrong (say why; never weaken a test just to make it pass).
- Add a follow-up task or idea for work you noticed but did not do.

## Tier 3: stop and ask first (`hloop blocker add "..."`, then end your turn)
- Add, remove or upgrade a dependency.
- Delete or rename files that other code or people may rely on.
- Change a public interface: an API route, an exported function's signature, a file format, a database schema or migration.
- Change CI/CD, build, deployment or security configuration.
- Anything involving credentials, authentication, permissions or personal data.
- A requirement is ambiguous, two documents disagree, or you are less than about 80% sure of the right approach.
- The work would go beyond the current task's criteria (scope expansion).
- Calling paid services, sending email or messages, or touching any shared or production system.

## Tier 4: never, unless the human gives that exact instruction for that exact action
- Force-push, rewrite or delete git history, or delete branches.
- Commit directly to `main`/`master`, or merge into it.
- Drop or wipe data; run destructive commands outside the project directory.
- Overwrite or discard uncommitted work you did not create.
- Disable, skip or weaken tests, gates, linters or security checks.
- Print, log or commit secrets.

## Project-specific boundaries
- No third-party Go dependencies: the project advertises none (SECURITY.md supply-chain row, go.mod has no requires).
- `.loop/` file formats must stay backward compatible: 0.3.0 ledgers and configs must keep loading and stay approved (CHANGELOG.md "Compatibility").
- Do not push commits or tags: hloop release tags are annotated and never pushed, and the workflows are unrun (CHANGELOG.md 0.6.0, docs/PUBLISHING.md).
- Do not weaken security checks in `internal/store/trust.go` or the agent command allow-list; SECURITY.md describes them as the product's core protections.

## Stop conditions
Stop, file a blocker that says exactly what you need, and end the turn when:
- the current task cannot be finished without a Tier 3 action;
- the same approach has failed twice;
- a test or gate fails for a reason outside the task;
- the code contradicts PRD.md, ARCHITECTURE.md or a Decided entry in DECISIONS.md.

hloop also checks git after every turn of an agent that may change the project (history rewrites, commits on protected branches, dependency changes, mass deletions) and stops or notifies the human as configured (`hloop config show`).
