# Getting started

From installing hloop to a project that runs itself.

## 1. Install

Linux / macOS: download the archive for your system from the release (`hloop_<version>_linux_amd64.tar.gz` for most Linux PCs and Chromebooks, `linux_arm64` for ARM boards, `darwin_arm64` for Apple Silicon), then:

```sh
tar -xzf hloop_<version>_linux_amd64.tar.gz
install -m 0755 hloop_<version>_linux_amd64/hloop ~/.local/bin/hloop
hloop version
```

Windows: unzip `hloop_<version>_windows_amd64.zip` and put `hloop.exe` on your PATH. Full instructions, including the one-line installers: `docs/INSTALL.md` in the source.

You also need `git` and at least one harness, signed in (for example run `claude` once).

## 2. Learn it safely (optional)

```sh
hloop tour
```

A walkthrough on a throwaway demo project. It starts no AI and costs nothing.

## 3. Set up a project

New project:

```sh
cd my-project
hloop init --ai                      # an agent interviews you and writes the PRD, guardrails and first tasks
hloop init --ai --from notes.md      # ... after reading your notes, research, wireframes
hloop init                           # or: a short questionnaire, no AI
```

Project already under way:

```sh
cd my-project
hloop adopt                          # an agent reads the repo and its docs and writes .loop/ from them
```

Either way you end up with `.loop/` (see `hloop docs files`).

## 4. Make "done" mean something

```sh
hloop gate add test -- npm test      # hloop runs this itself before accepting a finished task
hloop config set tdd on              # optional: tests must be committed failing before the code
```

Fill in the "Project-specific boundaries" in `.loop/GUARDRAILS.md`, then `hloop trust`.

## 5. Define the first work

```sh
hloop spec -i "user sign-up with email"   # an agent interviews you and writes a spec + tasks with criteria
hloop spec check                           # lint the definition
hloop task list
```

## 6. Run it

```sh
hloop doctor                 # checks everything
hloop run                    # runs now; watch with: hloop logs -f
hloop schedule install       # runs every 8 hours from now on
```

## 7. Stay in touch

```sh
hloop notify setup telegram  # two-way: alerts, answer blockers, add tasks
hloop notify setup ntfy      # one-way alerts only
hloop status                 # anytime
```

Read `SECURITY.md` (or `hloop docs safety`) before leaving a loop unattended: harnesses run with their own approval prompts disabled.
