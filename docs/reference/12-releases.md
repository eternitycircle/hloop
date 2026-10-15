# Releases and documentation

When the tasks are finished and verified, one command turns the work into what you hand to the people who use and maintain your software: release notes, a changelog, and up-to-date user and developer documentation.

```sh
hloop release                 # next version (0.1.0 the first time, then the next patch)
hloop release 1.0.0           # a specific version
hloop release --bump minor    # 1.2.3 -> 1.3.0
hloop release --dry-run       # the checks and the brief the writer would get; changes nothing
```

## What happens

1. **Checks.** Nothing is in progress, no blocker is open, there are no uncommitted changes outside `.loop/`, and every gate passes on the code as it is now. Unfinished tasks stop the release unless you pass `--allow-pending` (then only what is done is released). Tasks finished without hloop's verification and open decisions are shown as warnings.
2. **The brief.** hloop writes `.loop/release/brief-v<version>.md`, a factual summary of the release:
   - the tasks finished since the previous release, with their criteria, verification, specs and reports (the first release covers the whole project);
   - decisions made, open decisions (possible known issues), and review findings;
   - lessons and blockers you answered (good troubleshooting material);
   - the commit log and the files changed;
   - the documentation that already exists.
3. **The technical writer** (the `tech-writer` agent, in `docs` mode: it may change only `docs/`, the project's root `*.md` files and `.loop/`) writes:
   - `docs/releases/v<version>.md`: release notes for users (highlights, new, improved, fixed, breaking changes and upgrade steps, known issues, notes for developers);
   - `CHANGELOG.md`: a new section in the Keep a Changelog format (Added, Changed, Deprecated, Removed, Fixed, Security), with Semantic Versioning;
   - the documentation set, organised the Diátaxis way (tutorials, how-to guides, reference, explanation), created where missing and updated where this release changed something:

     | For users | For developers |
     |---|---|
     | `README.md` | `docs/DEVELOPER.md` (architecture, layout, setup, build, test, release) |
     | `docs/INSTALL.md` | `CONTRIBUTING.md` (if the project takes contributions) |
     | `docs/GETTING_STARTED.md` | `SECURITY.md` (if the project handles credentials, network input or personal data) |
     | `docs/how-to/*.md` | |
     | `docs/reference/` (config, CLI, API) | |
     | `docs/TROUBLESHOOTING.md` (with FAQ) | |
     | `docs/UPGRADING.md` (when there are breaking changes) | |

     plus `docs/README.md`, an index of all of it. It checks commands against the code, never invents features, and leaves `[[FILL: question]]` where only you can answer (a download URL, a support contact).
4. **Verification.** The release notes must exist and `CHANGELOG.md` must mention the version, or nothing is recorded. Documents with open `[[FILL]]`/TODO markers are listed.
5. **Commit and record.** The documents are committed (`docs(release): v<version> ...`; `--no-commit` leaves them for you to review), the release is recorded in the ledger, and it appears in `PROGRESS_LOG.md`. `--tag` adds an annotated tag `v<version>` (never pushed).

The next `hloop release` covers only what happened after this one.

## Options

| Option | Does |
|---|---|
| `--bump major\|minor\|patch` | how to derive the next version from the last release (or the highest `vX.Y.Z` tag) |
| `--allow-pending` | release what is finished even though tasks remain |
| `--no-checks` | skip running the gates (not recommended) |
| `--no-commit` | leave the documents uncommitted |
| `--tag` | create the git tag |
| `-i` | work with the writer in an interactive session (review and adjust as it writes) |
| `--adapter NAME` | use another harness for the writer |
| `--yes` | no confirmation prompt |

```sh
hloop release list            # past releases
hloop release notes [1.0.0]   # print a release's notes
```

## Customising the writer

The writer's instructions are in `.loop/prompts/tech-writer.md` after the first release: edit them (house style, extra pages, a docs site layout), then `hloop trust`. To use a different harness or model tier for it permanently: `hloop agent add tech-writer --role tech-writer --mode docs --tier critical --adapter <name>`.
