# How hloop checks the work

hloop does not take an agent's word that a task is finished. When the builder runs `hloop task done`, hloop checks, in this order:

1. **Acceptance criteria**: every Must criterion is marked pass with evidence.
2. **Gates**: your commands, run by hloop itself.
3. **Red/green proof** (when `tdd` is on, code tasks only).

If anything fails, the task is reopened, the failure (with the end of the command's output) goes into the next turn's prompt, and you get a `check` notification. After `max_stalled_turns` rejections the loop asks you.

## Gates

```sh
hloop gate add test -- go test ./...
hloop gate add lint -- npm run lint
hloop gate add test --replace --where export -- sh -c "npm ci && npm test"
hloop gate add docs-build --kinds docs -- mkdocs build --strict
hloop gate list | run [name] | remove <name>
```

- A gate is a command line run **without a shell**. For pipes or `&&`, name the shell yourself (`sh -c "..."`).
- `--where worktree` (default) runs it in the project folder. `--where export` runs it on a fresh copy of the last commit (`git archive`), which catches tests that pass only because of files nobody committed. An export has no installed dependencies, so install them in the command.
- `--kinds`: which task kinds it applies to (default `code,config`; `all` for every kind).
- `--timeout` minutes (default 20).
- Gates are part of the approved configuration: only you can add or remove them.

## Red/green proof (TDD)

```sh
hloop config set tdd on      # needs at least one gate
```

For code tasks the builder must:

1. write tests for the criteria;
2. see them fail;
3. commit only the tests;
4. record that commit: `hloop task red T5 --commit <hash>`;
5. implement and commit.

hloop then exports both commits and checks that the gates **fail** at the red commit and **pass** at the end. It also checks that the red commit belongs to this task and is not the last commit. A red commit whose tests already pass is rejected. If the gates fail even on a clean copy of the end commit (usually missing dependencies), hloop opens a blocker telling you how to make the gate self-contained.

## Guardrail detectors

After every turn of an agent that may change the project, hloop compares git before and after:

| Detector | Fires when | Default |
|---|---|---|
| `history_rewrite` | a commit that was on the branch is gone (amend, reset, rebase, force push, branch deleted) | block |
| `protected_commit` | new commits landed directly on a protected branch (`main`, `master`); commits touching only `.loop/` do not count | notify |
| `dependency_change` | a manifest or lock file changed in a new commit or the working tree (package.json, go.mod, Cargo.toml, requirements*.txt, ...) | notify |
| `mass_delete` | at least `mass_delete_count` (10) files deleted in one turn | notify |

```sh
hloop config set guard.dependency_change block
hloop config set guard.protected_branches main,release
hloop config set guard.mass_delete off
```

`block` opens a blocker and stops the loop; `notify` sends a `check` notification; both are recorded on the task and in its report. Detectors detect after the fact; they do not prevent anything.

## Reviews

Reviewer agents (`hloop agent install security-auditor`, or `hloop agent add reviewer --role reviewer`) run in their own session after each finished task. They get each task's criteria and hloop's verification, and they judge it as a stranger would. `changes_requested` turns their findings into new tasks at the top of the queue. After `max_cycles` rounds in a row they hand the question to you.

## Completion reports

`.loop/reports/T5.md`, written by hloop (not an agent) when a task is verified and again after its reviews:

- criteria with evidence;
- each check and its result;
- the four detectors and whether they fired;
- commits and files changed;
- reviews.

```sh
hloop task report T5      # regenerate and print
hloop task show T5        # quick summary
```

`PROGRESS_LOG.md` links every report.

## What is and is not proven

hloop runs the gates and the red/green proof itself, so those cannot be faked by an agent's words. Criteria evidence is written by the agent: hloop checks it was recorded, the reviewer checks it is true. Criteria with a gate behind them are the strongest.
