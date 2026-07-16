<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/pluto-lockup-dark.svg">
    <source media="(prefers-color-scheme: light)" srcset="assets/pluto-lockup-light.svg">
    <img alt="pluto" src="assets/pluto-lockup-light.svg" width="360">
  </picture>
</p>

Pluto is an AI harness. I started this project solely because I don't trust any of the AI harnesses nowadays, for various reasons. I never understood the TypeScript ones, although there are some good ones like `ohmypi` and `pi`.

Claude Code is a total mess that I don't want to deal with. It also doesn't support other models, it's closed source so you can't edit it, and it has useless features (at least for me). Also VERY slow.

Codex is probably the only one that I like in terms of code quality and usefulness, but again it supports only GPT models. That's fine, because you can basically edit it, but then you have to maintain a fork, which is not ideal if they keep pushing features that you don't use.

I wanted a simple thing with the features that I use and like from other AI harnesses, and I wanted to write it in a language that I "know", or at least make my best effort to learn.

Anyway, it's also a fun project for creating agents.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/syrull/pluto/refs/heads/main/install.sh | sh
```

This downloads the latest release binary for your OS/arch into `~/.local/bin`
(override with `PLUTO_INSTALL_DIR`). Make sure that directory is on your `PATH`.

## Update

Pluto updates itself in place to the latest release:

```sh
pluto update
```

Check the current version with `pluto version`.

## Multi-agent workspace

The right sidebar has an **Agents** pane listing independent conversations that
run in parallel. Tab reaches it; `↑/↓` move, `↵` switches, `n` (or the
`＋ new agent` row) creates one, and `d` (or `/close`) closes the active agent. A
new agent opens on a fresh dashboard and can run in its own **git worktree** so
parallel agents don't clobber each other — switching between them never
interrupts a running turn. Closing an agent cancels any in-flight run, offers to
remove its worktree, and switches to a neighbor; closing the last one resets it
to a fresh agent. The **Files**, **Changes**, and **Agents** panes are
collapsible with `-` (expanded by default), and the Files tree, Changes list,
fuzzy finder, and dashboard all follow the active agent's directory. `/new`
clears only the active agent, and agents (with their transcripts and worktrees)
persist across restarts via `/save` and `/resume`.

## Conversation pane

In the chat pane, `↑`/`↓` (and `PgUp`/`PgDn`, `ctrl+u`/`ctrl+d`) always scroll the
transcript — they never touch the input. Input **history** is recalled with
readline-style keys instead: `ctrl+p` walks back to older submitted messages (only
from an empty buffer) and `ctrl+n` walks forward, clearing the buffer once you step
past the newest entry. On a multi-line draft, `ctrl+p`/`ctrl+n` fall through to the
editor and move the cursor between lines, so they never clobber an unsent draft.

## Command review (auto mode)

Before running a `bash` command, pluto reviews it: an offline **guard** denylist
blocks catastrophic commands outright, and an LLM **judge** assesses the rest.
Trivially safe read-only commands take a fast path and skip the judge. Toggle the
whole thing with `/auto on|off`.

When the **judge itself fails** (provider unreachable, timeout, unparseable
verdict), pluto no longer silently guesses from config. Instead it pauses the
command and asks you:

```
⚠ judge unavailable — approve this command?
[y] yes   ·   [a] allow this pattern   ·   [n] no
```

- **`y` — yes**: run it once.
- **`a` — allow this pattern**: run it now and remember an allowlist entry so
  matching commands skip approval for the rest of the session. The remembered
  pattern generalizes to `program subcommand` for common dev tools (e.g.
  `go test`, `git status`) and is otherwise the exact command, so it can't
  over-match. Guard denylist hits are always blocked, even if allowlisted.
- **`n` — no** (or `esc`): block it, reported back to the model like any refusal.

Only a *judge error* triggers the prompt — a guard block or an explicit judge
`block` is never downgradable by a human. When no interactive prompt is available
(a background or headless run), pluto falls back to the non-interactive policy set
by `PLUTO_AUTO_ON_JUDGE_ERR` (`block` by default, `allow` to fail open).

## Skills

Skills are short, single-topic playbooks the agent pulls into context only when
a task needs them, so the base system prompt stays small. Drop plain-text files
in a `skills/` directory where you run pluto (typically your repository root):

```
skills/
  run-tests.md
  cut-release.md
  add-a-tool.md
```

- One skill per file, named `<skill-name>.md` (or `.txt`). The filename (without
  extension) is the skill's name.
- The first non-empty line is the one-line **summary** shown in the always-on
  index; a leading Markdown `#` is stripped. Keep it short.
- Everything in the file is the **body**, loaded on demand.

Only the compact index (name + summary) rides in the system prompt. The model
loads a full body when it's relevant via the `skill` tool: calling it with no
arguments lists the available skills, and passing a `name` returns that skill's
full text. Because skills are plain text, you can also open them with the
`read`/`find` tools or your editor.

## Debugging

Pluto can record a structured, timestamped log of *everything* that happens in a
session — invocation details, every subsystem event, and (optionally) every TUI
frame render — so a graphical glitch or a subtle behavioral bug can be
reconstructed after the fact. The log is designed to be handed straight to an AI
agent for diagnosis.

Enable it with an environment variable and pluto prints where it's writing:

```sh
PLUTO_DEBUG=1 pluto
# pluto: debug logging to pluto-debug.log
```

Each line looks like `HH:MM:SS.ffffff LEVEL [component] message key=value …`,
e.g. `[tui] update key key=tab` followed by the resulting `[tui] state …`. Keys
are emitted in a fixed order so two runs can be diffed. **Secrets (OAuth tokens,
API keys, auth headers) are never written** — they are redacted in the auth path.

### Configuration

| Variable | Values | Default | Purpose |
| --- | --- | --- | --- |
| `PLUTO_DEBUG` | `1`/`true`/`on` | off | Master switch. When off, logging is a no-op. |
| `PLUTO_DEBUG_FILE` | path | `pluto-debug.log` | Where the log is appended. |
| `PLUTO_DEBUG_LEVEL` | `trace`\|`debug`\|`info`\|`warn`\|`error` | `debug` | Minimum severity. `trace` unlocks the per-frame render firehose. |
| `PLUTO_DEBUG_COMPONENTS` | comma list, `-` to exclude | all | Filter by subsystem, e.g. `tui,agent` (only those) or `-llm` (all but llm). |
| `PLUTO_DEBUG_FRAMES` | `off`\|`coalesced`\|`full` | `coalesced` | UI frame logging (needs `trace`). `coalesced` collapses identical frames to `frame unchanged repeated=N`; `full` also dumps the rendered screen. |

### Capturing a log for an issue

To trace a graphical bug frame by frame without drowning in LLM payloads:

```sh
PLUTO_DEBUG=1 PLUTO_DEBUG_LEVEL=trace PLUTO_DEBUG_COMPONENTS=tui \
  PLUTO_DEBUG_FILE=/tmp/pluto-bug.log pluto
```

Reproduce the glitch, quit, then attach `/tmp/pluto-bug.log` to the issue (skim
it first to confirm it contains nothing sensitive — it shouldn't). The file is
appended across runs, so start from a fresh path per capture.

## Releases

Releases are published automatically from `main`. Every push bumps the patch
version (starting at `v0.0.1`) and creates a tagged GitHub release with
prebuilt binaries. Add `[skip release]` to a commit message to skip it.
