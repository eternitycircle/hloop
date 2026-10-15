# Overview

hloop runs an autonomous coding loop with any AI coding harness (Claude Code, Codex CLI, Gemini CLI, OpenCode, Copilot CLI, agy, or your own).

## The idea in one paragraph

Your harness is the **worker**: it writes code. hloop is the **supervisor**: it owns the project's definition, the task queue, the schedule, the rules agents must follow, and the checks that decide whether work is really done. Each *turn*, hloop gives a fresh harness session one task plus everything it needs to know, then checks the result itself before moving on. You steer from your computer or your phone, and you are asked only when a decision is yours to make.

## The lifecycle

```
 define            run                      verify                 review            you
 PRD + specs  ->   builder turn on    ->    criteria marked,  ->   independent  ->   blockers, decisions,
 tasks with        the current task         gates pass,            reviewer           approvals, phone
 criteria                                   red/green proof        agents
                         ^                        |
                         +---- rejected: task reopened, agent told why
```

1. **Define** the work: a Product Requirements Document (`PRD.md`) for the project, a spec per feature (`specs/<feature>/spec.md`), and tasks with acceptance criteria. See `hloop docs defining-work`.
2. **Run**: `hloop run` (or on a schedule). The builder agent takes the current task. See `hloop docs running`.
3. **Verify**: when the builder says a task is done, hloop checks the criteria, runs your gates (tests), and optionally the red/green proof. A failure reopens the task. See `hloop docs verification`.
4. **Review**: optional reviewer agents judge each finished task in a separate session. See `hloop docs agents`.
5. **You**: blockers, decisions, approvals and progress reach you on the computer or the phone. See `hloop docs phone`.
6. **Release**: when the work is done, `hloop release` checks everything once more and has a technical-writer agent produce release notes, the changelog, and user and developer documentation. See `hloop docs releases`.

## Words hloop uses

| Word | Meaning |
|---|---|
| project | a repository with a `.loop/` folder |
| harness | the coding-agent CLI that does the work (Claude Code, Codex, ...) |
| adapter | how hloop starts a harness: its command line, as data |
| agent | a harness + instructions (prompt) + when it runs (trigger) + what it may touch (mode) |
| builder | the main agent; it works on the current task every turn |
| turn | one harness session on one task |
| run | a series of turns, until done, blocked, out of quota or the turn cap |
| task | one unit of work small enough for a session, with acceptance criteria |
| criterion (AC) | a checkable statement that defines done for a task; Must, Should or Could |
| gate | a command hloop runs itself (usually your tests) before it accepts "done" |
| blocker | something only the human can do or decide; the loop pauses until it is answered |
| decision | an open choice recorded for the human; agents must not make it |
| idea | parked for someday; never worked on until promoted |
| lesson | a durable fact every agent turn is told |
| guardrails | `GUARDRAILS.md`: what agents may do alone, what needs asking, what is never allowed |

## Where to go next

- New here: `hloop tour` (an interactive walkthrough on a throwaway demo project; rerun it any time).
- Setting up a real project: `hloop docs getting-started`.
- Every command: `hloop docs commands`.
