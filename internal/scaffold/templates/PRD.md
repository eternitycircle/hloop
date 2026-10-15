# Product Requirements Document (PRD): {{.Name}}

What this project is and what it must do. Stable: change it when the product changes, not every turn (current state goes in CURRENT.md, feature detail in specs/, choices in DECISIONS.md).

## 1. Overview
- **Problem / purpose**: {{.Purpose}}
- **Users**: {{.Audience}}

## 2. Goals and non-goals
### Goals
- [[FILL: what success looks like, measurable where possible]]
### Non-goals
- [[FILL: what this project deliberately does not do]]

## 3. Requirements
### Functional
- Feature-level requirements live in `.loop/specs/<feature>/spec.md`; list the main capabilities here in a line each.
### Non-functional
- [[FILL: performance, security, accessibility, supported platforms, or "none beyond the defaults"]]

## 4. Tech stack and infrastructure
- **Runtime / framework**: {{.Stack}}
- **Data / backend**: {{.Backend}}
- **Hosting / deployment**: {{.Hosting}}

## 5. Constraints and principles
- Prefer lightweight, token-efficient CLI tooling over heavy browser automation.
- All code additions must include tests and pass security checks before committing.
- Work must be modular and committed to feature branches.
{{.Constraints}}
## 6. Milestones
{{.Roadmap}}

## 7. Success metrics
- [[FILL: how you will know it works for its users, e.g. "imports a 10 MB CSV in under 5 s"]]
