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

While a turn is in flight the input stays live: a plain message is queued to
steer the running turn, and the background-safe commands `/gh` and `/auto` run
immediately without interrupting it — `/auto off`, for instance, drops the judge
mid-run. Every other slash command waits until the agent is idle.

## Command review (auto mode)

Before running a `bash` command, pluto reviews it: an offline **guard** denylist
blocks catastrophic commands outright, and an LLM **judge** assesses the rest.
Trivially safe read-only commands take a fast path and skip the judge. Toggle the
whole thing with `/auto on|off`.

To avoid paying for a decision it already made, pluto **memoizes judge verdicts**
in a small per-process LRU keyed by the normalized command and working directory
(not the model-supplied intent/why, so those can't bust or poison the cache). A
repeated identical command reuses its allow/block verdict — the guard still runs
on every call, and errors are never cached. The cache is in-memory only: it does
not persist across sessions and has no TTL, so a command whose *effect* changed
(e.g. a script edited in the worktree) can reuse a stale verdict until eviction.

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

The prompt is **per-agent**: it appears inline at the bottom of the transcript of
the agent that raised it, with the input box still on screen, and is answered with
`y`/`a`/`n` there. A background agent's prompt never takes over the agent you are
looking at — it waits on that agent (flagged unread in the Agents pane) until you
switch to it.

## Goals (keep working until a condition is met)

Normally the agent stops when *it* decides the work is done. `/goal <condition>`
flips that around: it sets a **completion condition** and keeps the active agent
working across turns — without you re-typing "continue" — until a **separate,
small, fast evaluator model** (Haiku by default) judges the condition met.

```
/goal all tests under ./internal/tui pass
```

After every turn, the condition plus the conversation so far go to the evaluator,
which returns a **yes/no** decision and a short reason. A **no** starts another
turn automatically and feeds the reason back as guidance; a **yes** clears the
goal and records an *achieved* entry. The evaluator has **no tools** — it can't
run commands or read files, it only judges what the agent has **surfaced in the
transcript**. So phrase the condition as something the agent's own output can
demonstrate (test output landing in the transcript works; "the code is correct"
does not).

There is **one goal per agent**. The same command sets, inspects, and clears:

- `/goal <condition>` — set/replace and start a turn immediately (max 4,000 chars).
- `/goal` — show status: condition, elapsed time, turns, tokens, and the last
  evaluator reason.
- `/goal clear` — clear early (aliases: `stop`, `off`, `reset`, `none`, `cancel`).

A `◎ goal <elapsed>` chip shows on the status line while a goal is active.
Pressing `esc` (or `ctrl+c`) **pauses** the loop, and `/new` clears the goal.
If the evaluator itself errors, the loop **pauses** (rather than spinning) and
keeps the goal so you can inspect it with `/goal` and retry.

`/goal` doesn't change permissions — it removes per-*turn* prompts, while auto
mode removes per-*tool* prompts. Pair them (`/auto on` + `/goal …`) for an
unattended run to completion. An active goal is restored on `/resume` (its
turn/timer/token counters reset); achieved goals are not.

By default a goal runs until met or interrupted (no built-in budget) — either
bake a turn/time cap into the condition text, or set `PLUTO_GOAL_MAX_TURNS` to a
positive number to pause the loop after that many turns. `PLUTO_GOAL_MODEL`
overrides the evaluator model (default: the judge model), and `PLUTO_GOAL=off`
disables the feature.

## MCP servers

Pluto can load [Model Context Protocol](https://modelcontextprotocol.io) servers
and expose their tools to the agent alongside the built-ins. Both **local**
servers (a subprocess pluto spawns and talks to over stdio) and **remote**
servers (an HTTP/SSE endpoint) are supported.

Declare servers in `mcp.json`. Pluto looks for it, in order, at:

1. `$PLUTO_MCP_CONFIG` (an explicit path override), then
2. `~/.pluto/mcp.json` (next to the credential store), then
3. `~/.config/pluto/mcp.json` (or `$XDG_CONFIG_HOME/pluto/mcp.json`).

The first file found wins. The format matches the widely used `mcp.json` shape:

```json
{
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/path"],
      "env": { "SOME_API_KEY": "..." }
    },
    "remote": {
      "type": "http",
      "url": "https://example.com/mcp",
      "headers": { "Authorization": "Bearer ..." }
    }
  }
}
```

Local servers set `command`/`args`/`env`; remote servers set `url`/`headers`.
The transport is inferred (`command` ⇒ stdio, `url` ⇒ http) unless you set
`type` explicitly (`stdio`, `http`, or `sse`). Add `"disabled": true` to keep a
server in the file without loading it.

A local server inherits only a curated, secret-free slice of pluto's environment
(`PATH`, `HOME`, and the like) — never your API keys or OAuth tokens. Give a
server a secret explicitly through its `env` block; nothing else leaks into the
subprocess. Its stderr goes to the debug log, not the terminal, so it can't
corrupt the TUI.

Servers connect **once at startup**: pluto prints a `loading N MCP server(s)…`
notice before dialing (so a slow server doesn't look like a hang) and a
`loaded N server(s), N tool(s)` line once done. Each server's tools are
registered as `mcp__<server>__<tool>` (namespaced so they never collide with
built-ins or each other) and the connections stay open for the session. Loading
is best-effort — a missing config is silent, and an unreachable server is logged
and skipped so pluto still starts. Add a new server (or change one) and
**restart pluto** to pick it up. Set `PLUTO_DEBUG=1 PLUTO_DEBUG_COMPONENTS=mcp`
to trace the load, handshake, and every tool call.

Run `/mcp` at any time to list the configured servers with their transport,
per-server tool count and names, plus any that failed or are disabled — read
straight from the startup load, so it reflects exactly what the session has.

Because an MCP tool is opaque third-party code with no shell command for the
guard or judge to inspect, auto mode asks you to approve each MCP tool the first
time the agent calls it (`y` once, `a` to allow that tool for the rest of the
session, `n` to block) — the same prompt used for `bash`. Approvals are skipped
entirely when auto mode is off (`PLUTO_AUTO=off`).

### Installing a server from a repo

`/install-mcp <repo>` points the agent at a GitHub repository and has it work
out the install for you:

```
/install-mcp https://github.com/owner/some-mcp-server
/install-mcp owner/some-mcp-server
```

The agent explores the repo, figures out the transport and launch command,
checks the prerequisites (Node/npx, Python/uvx, Docker, a prebuilt binary, …)
against what you actually have installed, and then either **merges** an entry
into your `mcp.json` (preserving existing servers) or — when something needs a
decision only you can make, like providing an API key or installing a runtime —
**walks you through** the remaining steps rather than guessing. It never invents
secrets. Restart pluto afterwards to load the new server.

## Skills

Skills are on-demand playbooks the agent can pull in only when a task calls for
them. Each lives in its own folder under `skills/` in the working directory as a
`SKILL.md` file: YAML frontmatter with a `name` and `description`, then Markdown
instructions. Only the compact index (name + description) rides in the system
prompt; the full body is loaded lazily via the `skill` tool, keeping the
always-on prompt small (progressive disclosure).

Run `/skills` to list the skills discovered under the active agent's directory
with their descriptions — the same set the agent sees. Skills follow the active
agent, so each worktree can carry its own.

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
