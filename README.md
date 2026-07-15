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
`＋ new agent` row) creates one. A new agent opens on a fresh dashboard and can
run in its own **git worktree** so parallel agents don't clobber each other —
switching between them never interrupts a running turn. The **Files**,
**Changes**, and **Agents** panes are collapsible with `-` (expanded by
default), and the Files tree, Changes list, fuzzy finder, and dashboard all
follow the active agent's directory. `/new` clears only the active agent, and
agents (with their transcripts and worktrees) persist across restarts via
`/save` and `/resume`.

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

Components include: `lifecycle`, `reposcan`, `tui`, `agent`, `tool`, `llm`,
`session`, `auth`, `policy`, `judge`, `guard`, `update`.

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
