# Harness adapters

An **adapter** tells hloop how to call one coding-agent CLI (a "harness") without a human at the keyboard. It is data, not code: a small JSON file of argument lists. hloop never passes anything through a shell.

hloop ships with adapters for the popular harnesses. If yours is not listed, or a flag changed in a newer release, you can add or edit one in a few minutes. You do not need to change hloop itself.

- [Built-in adapters](#built-in-adapters)
- [Prove an adapter works: `hloop adapter test`](#prove-an-adapter-works)
- [Add a harness that is not listed](#add-a-harness-that-is-not-listed)
- [Field reference](#field-reference)
- [Where the prompt goes: argument, stdin or file](#where-the-prompt-goes)
- [Windows notes](#windows-notes)
- [Wrapper scripts, for tools with no command-line mode](#wrapper-scripts)
- [Security notes](#security-notes)

## Built-in adapters

`hloop adapter list` shows these and whether each is installed on this machine.

| Name | Program | Prompt via | Status | Notes |
|---|---|---|---|---|
| `claude-code` | `claude` | stdin | **verified** | Full loop run with Claude Code 2.1.294 on Linux: builder, reviewer, blocker, specialist agents. |
| `codex` | `codex` | stdin | flags-checked | OpenAI Codex CLI (ChatGPT sign-in or API key). Uses `exec --dangerously-bypass-approvals-and-sandbox`; copy the adapter and use `--sandbox workspace-write` for a tighter setup. |
| `opencode` | `opencode` | argument | flags-checked | `run --auto`. Older versions without `--auto` need the `OPENCODE_PERMISSION` environment variable. |
| `gemini-cli` | `gemini` | argument | flags-checked | `--yolo -p`. Sets `GEMINI_CLI_TRUST_WORKSPACE=true`, because Gemini silently ignores `--yolo` in folders it does not trust. Exit code 53 means the turn limit was hit. |
| `copilot-cli` | `copilot` | argument | flags-checked | GitHub Copilot CLI, `-p` with `--allow-all-tools`. |
| `cursor` | `cursor-agent` or `agent` | argument | documented | Cursor's headless CLI. Both binary names are tried. No resume flag is documented, so each turn starts fresh and continuity comes from the ledger. |
| `devin` | `devin` | argument | documented | The local Devin terminal CLI. If `--permission-mode dangerous` is rejected, try `bypass`. The cloud Devin web agent has no command line; see [Wrapper scripts](#wrapper-scripts). |
| `muse` | `muse` | argument | documented | Linux and macOS only (use WSL2 on Windows). `--yolo` also turns off Muse's own OS sandbox. |
| `openclaw` | `openclaw` | argument | documented, experimental | OpenClaw is a general assistant rather than a coding-first harness, and `agent exec` only exists in newer releases. Read its note: `hloop adapter show openclaw`. |
| `agy` | `agy` | argument | documented | Flags come from the original `run_antigravity.sh`. |

**What the status words mean**

- **verified**: the hloop authors ran a real loop with this harness.
- **flags-checked**: every flag was confirmed against the installed CLI's own `--help` (and the vendor's headless-mode docs), but no loop was run, because that needs the vendor's account.
- **documented**: the flags come from vendor documentation only.
- **unverified**: you defined it, or an AI wrote it for you, and it has not been tested.

Vendors rename flags often. Treat anything other than *verified* as "probably right" and run `hloop adapter test <name>` once on your machine before you leave a loop running overnight.

All built-in unattended modes **turn off the harness's approval prompts** (that is what lets them run while you sleep). Run loops in a VM, container or dedicated user account. `hloop doctor` reminds you.

## Prove an adapter works

```sh
hloop adapter test codex            # asks before it starts anything
hloop adapter test codex --yes      # no question (use in scripts)
hloop adapter test codex --continue # also checks that "continue" resumes the previous session
```

This runs the harness once in a throwaway folder with a tiny real task: create a file containing `OK`, and run `hloop version`. It then checks, in order:

1. the program is installed and starts (otherwise it prints the install link and sign-in instructions);
2. it exits cleanly without hanging on an approval prompt or login (5 minute limit, change with `--timeout`);
3. it really created the file (so file edits are not blocked by a sandbox);
4. it ran a shell command (a harness that cannot run `hloop task done` cannot report progress).

It uses a little of your usage allowance. When it passes, the harness is ready for `hloop init --adapter <name>` or `hloop agent add ... --adapter <name>`.

If a harness works for the test but cannot run shell commands during a real loop, hloop notices: after `max_stalled_turns` turns (default 3) on the same task without it being marked done, the loop stops and raises a blocker explaining why, instead of repeating the same turn forever. Change it with `hloop config set max_stalled_turns 5`, or `off`.

## Add a harness that is not listed

Any command-line tool qualifies if it can (1) take a prompt from the command line, stdin or a file, (2) edit files and run shell commands in the current folder **without asking**, and (3) exit with code 0 when its turn is finished. Most coding CLIs have this under a name like `-p`, `--print`, `exec`, `run` or `--prompt`.

There are three ways to describe it. All three end with `hloop adapter test`.

### 1. Let an AI read the tool's help and write the adapter

```sh
hloop adapter learn mytool
```

This collects `mytool --help` and prints a prompt for any AI (ChatGPT, Claude, Gemini, or a harness you already have). Paste the prompt in, save the JSON it replies with to a file, then:

```sh
hloop adapter import reply.json
hloop adapter test mytool
```

If the tool has subcommands (`mytool exec --help`), add them: `hloop adapter learn mytool --also "exec --help"`.

To skip the copy and paste, name an adapter you already have:

```sh
hloop adapter learn mytool --use claude-code
```

hloop starts that harness in an empty temporary folder, shows you the JSON it produced, and asks before saving. Be aware that the help text of an unknown program is untrusted input given to an AI that runs without approval prompts, so use this with a tool you installed deliberately, read the JSON before you accept it, and do not pass `--yes` for tools you do not trust. Imports are strict: unknown field names are rejected, an adapter for a different program than you asked about is refused, and anything that is imported or learned is saved as `unverified`.

### 2. Describe it with flags

```sh
hloop adapter add mytool --bin mytool \
  --arg run --arg --auto --arg '{prompt}' \
  --continue-arg run --continue-arg --auto --continue-arg --continue --continue-arg '{prompt}' \
  --check-arg --version \
  --docs https://mytool.example/docs --auth "run mytool login"
```

- `--arg` builds the "fresh run" command line, one argument per flag, in order.
- `--continue-arg` builds the "resume the last session" command line. Leave it out if the tool cannot resume; each turn then starts fresh and the ledger provides continuity.
- `--stdin` sends the prompt on standard input instead of in an argument.
- `--skips-permissions` marks that the unattended mode disables approval prompts (so hloop can warn you).
- `--project` saves it inside this project (`.loop/adapters/`) instead of your user config, so the project carries its own definition.

### 3. Start from a built-in and change what differs

```sh
# Codex, but inside its own workspace sandbox instead of bypassing it
hloop adapter add codex-sandboxed --from codex --skips-permissions=false \
  --arg exec --arg --sandbox --arg workspace-write --arg --skip-git-repo-check --arg - \
  --continue-arg exec --continue-arg resume --continue-arg --last --continue-arg --sandbox \
  --continue-arg workspace-write --continue-arg --skip-git-repo-check --continue-arg -
```

Note the sandbox may block `git commit` or network access, which the loop needs; `hloop adapter test` shows whether it does.

Only the parts you pass are changed. `hloop adapter show codex` prints the full definition to copy from.

### 4. Edit the JSON yourself

Put a file at `~/.config/harness-loop/adapters/<name>.json` (macOS and Linux) or `%AppData%\harness-loop\adapters\<name>.json` (Windows), or `.loop/adapters/<name>.json` inside one project. `hloop adapter show claude-code` is a good template. If you edit a project file by hand, run `hloop trust` afterwards (see [Security notes](#security-notes)).

Lookup order: the project's `.loop/adapters/`, then your user folder, then the built-ins. So you can override a built-in just by saving an adapter with the same name.

## Field reference

```json
{
  "name": "mytool",
  "display": "My Tool",
  "bin": "mytool",
  "alt_bins": ["mytool-cli"],
  "new": ["run", "--auto", "{prompt}"],
  "continue": ["run", "--auto", "--continue", "{prompt}"],
  "interactive": ["--prompt", "{prompt}"],
  "check": ["--version"],
  "prompt_via": "arg",
  "env": {"MYTOOL_NO_UPDATE": "1"},
  "rate_limit_patterns": ["out of credits"],
  "skips_permissions": true,
  "os": ["linux", "darwin"],
  "docs": "https://mytool.example/docs/headless",
  "auth": "Run `mytool login` once.",
  "status": "unverified",
  "notes": "Anything a future you should know."
}
```

| Field | Required | Meaning |
|---|---|---|
| `name` | yes | Letters, digits, `-` and `_`. Used in `--adapter <name>`. |
| `bin` | yes | The program name, one word. Arguments go in `new`, not here. |
| `alt_bins` | no | Other names the program is known by. Tried in order if `bin` is not on your PATH. |
| `new` | yes | Arguments for a fresh, non-interactive run. Must contain `{prompt}` or `{prompt_file}`, unless `prompt_via` is `stdin`. |
| `continue` | no | Arguments for resuming the most recent session. Used by agents whose session mode is `continue`. |
| `interactive` | no | Arguments for an interactive session that starts with a prompt. Used by `hloop init --ai` and `hloop adopt --interactive`. |
| `check` | no | Arguments that print a version, for `hloop doctor` and `hloop adapter check`. |
| `prompt_via` | no | `arg` (default) or `stdin`. See below. |
| `env` | no | Extra environment variables for every run. |
| `rate_limit_patterns` | no | Extra text that means "usage limit reached". A run counts as rate-limited only when it exits non-zero **and** its last output contains one of these or a built-in pattern. The loop then waits and retries instead of failing. |
| `skips_permissions` | no | `true` if the unattended mode disables approval prompts. Triggers a warning in `hloop doctor`. |
| `os` | no | Operating systems it supports. hloop warns on others. |
| `docs`, `auth` | no | Shown when the harness is missing or fails, so the next person knows what to do. |
| `status`, `notes` | no | Informational. |

Unknown field names are an error, not ignored. A misspelling such as `continue_args` would otherwise silently drop the setting.

Placeholders, in `new`, `continue` and `interactive`:

- `{prompt}`: the full prompt text as one argument. Quotes, newlines and `$` are safe because there is no shell.
- `{prompt_file}`: the path of a private temporary file holding the prompt. hloop deletes it after the run.

hloop also sets these environment variables for every harness run, which agents use to report back: `HLOOP_BIN` (the path to hloop), `HLOOP_PROJECT`, `HLOOP_AGENT`, `HLOOP_ROLE`.

## Where the prompt goes

A turn's prompt is long (the role instructions plus the generated run context), multi-line, and may contain quotes. Three ways to hand it over, from best to worst:

1. **stdin** (`"prompt_via": "stdin"`, no placeholder). No length limit, nothing visible in the process list, nothing for a shell to mangle. Use it when the tool supports it. `claude-code` and `codex` do.
2. **`{prompt_file}`**. For tools that take a path, such as `--message-file`, `--prompt-file` or `@file`. No length limit.
3. **`{prompt}`** as an argument. Simple and what most CLIs offer. Fine on macOS and Linux for prompts of reasonable size; see the Windows notes for its limits.

## Windows notes

- Many harnesses are installed through npm and appear on Windows as `name.cmd` scripts. A `.cmd` script is run by `cmd.exe`, which is poor at multi-line and quote-heavy arguments and has a command-line limit of about 32,000 characters. hloop's prompts can approach that. Prefer stdin or `{prompt_file}` for those tools. `hloop adapter check <name>` and `hloop doctor` warn when an argument-based adapter resolves to a `.cmd` file.
- For tools that offer no stdin or file option, the options are: install the tool's native `.exe` build if it has one, use WSL2 (the Linux build of hloop and the harness run well there), or write a wrapper script (below).
- `muse` has no native Windows build.

## Wrapper scripts

Some tools have no usable command line: a web-only agent such as cloud Devin, an editor plugin, or an API. And some command-line tools can edit code but cannot run shell commands, so they cannot call `hloop task done`. A **wrapper script** bridges the gap. It is a small program that follows a simple contract, and you point an adapter at it:

> It receives the prompt on standard input, does the work in the current folder, and exits 0 on success (non-zero on failure). It may call `"$HLOOP_BIN" task done` itself for the builder role, because the tool cannot.

Example for [aider](https://aider.chat), which edits files and commits but cannot run `hloop`. This is **unverified**: aider could not be installed in the environment where hloop was built, so check the flags against `aider --help` and run `hloop adapter test aider` first.

```sh
#!/bin/sh
# ~/bin/aider-wrap.sh   (chmod +x)
set -e
prompt_file=$(mktemp)
trap 'rm -f "$prompt_file"' EXIT
cat > "$prompt_file"                      # hloop sends the prompt on stdin
aider --yes-always --message-file "$prompt_file"
if [ "$HLOOP_ROLE" = "builder" ]; then
  "$HLOOP_BIN" task done --summary "completed by aider"   # aider cannot report progress itself
fi
```

```sh
hloop adapter add aider --bin "$HOME/bin/aider-wrap.sh" --stdin --arg x --check-arg --version --skips-permissions
```

(`--arg x` is a placeholder argument so the wrapper has something to be called with; the script ignores it.) Note that this reports every successful turn as "task done" even if aider changed nothing, so use hloop's review agent to catch that.

For a cloud agent such as the Devin web app, the wrapper would call the vendor's API with the prompt, wait for the session to finish, then fetch the resulting branch or pull request into the working folder. That depends on the vendor's API and your account, and hloop does not ship one. An AI can write it for you from the vendor's API documentation, using the contract above.

## Security notes

- An adapter decides **what program hloop starts**, so a project's adapters are part of what you approve with `hloop trust`. `hloop adapter add --project` and the other adapter commands keep a project approved automatically. If you hand-edit `.loop/adapters/*.json`, or pull someone else's, hloop refuses to run until you review the change with `hloop trust --show` and approve it with `hloop trust`.
- Adapters are argument lists. There is no shell and no variable expansion, so a prompt cannot escape into a command.
- Agents (the harnesses hloop starts) cannot add or change adapters: `hloop adapter` is a human-only command.
- A wrapper script is a program you chose to run. Read it like any script you download.
- `learn --use` and `import` take their content from an AI that read untrusted text. They save only after you see the JSON, they refuse a different program than the one named, and they mark the result `unverified`.
