# CLAUDE.md

Guidance for AI agents working in this repo (`github.com/syrull/pluto`).

## Debugging (CORE requirement)

Debugging is a first-class, non-optional part of every feature. The structured
logger in `internal/debug` (see the README "Debugging" section) exists so an
enabled session log tells the complete story — enough to reconstruct even a
graphical bug. A feature that isn't observable in that log is incomplete.

- EVERY new feature MUST ship with comprehensive debug instrumentation in the
  same change. Do not defer it to a follow-up.
- Instrument each meaningful decision, state transition, and outcome — including
  the "nothing happened" and error branches — and wrap anything that does I/O,
  spawns a subprocess, or calls the network/LLM in a `debug.NewTimer` so latency
  is recorded.
- Use the structured API — `debug.Trace/Debug/Info/Warn/Error` (or `Event`) with
  stable, ordered `key=value` fields — never bare formatted strings. Tag each
  event with the right component (`lifecycle`, `reposcan`, `tui`, `agent`,
  `tool`, `llm`, `session`, `auth`, `policy`, `judge`, `guard`, `update`, `mcp`);
  reuse an existing tag when the feature belongs to that subsystem.
- Choose the level deliberately: `Info` for user-visible actions and lifecycle,
  `Debug` for internal steps, `Trace` for high-volume/per-frame firehose, and
  `Warn`/`Error` for failures. Gate expensive field building behind
  `debug.Should`.
- NEVER log secrets — run tokens, API keys, and auth headers through
  `debug.Redact`; truncate large payloads (see `truncate` in `internal/agent`).
- Keep the disabled path a no-op: don't do work solely to build a log field
  outside a `debug.Should` / `debug.Enabled` guard.
- Cover the instrumentation with tests where practical (level/component filtering
  and redaction have precedent in `internal/debug` and `internal/tui`).

## No emoji

NEVER put emoji in code, output, TUI text, comments, commit messages, or docs.
Always use a monochrome, terminal-native Unicode glyph instead.

- Emoji render inconsistently across terminals, break column-width math, and
  clash with the TUI aesthetic — treat them as forbidden, no exceptions.
- Reuse the glyph vocabulary already in the TUI: `✓` success, `✗` failure,
  `⚠` warning, `⎇` git branch, `§` referenced context, `▤` image attachment,
  `●` busy, `▸` selection. Pick a single-width glyph in the same spirit for
  anything new.

## Comments

Keep comments minimal. Only write what is essential.

- Prefer no comment over an obvious one. Let clear names and code speak.
- When a comment is needed, keep it to a single short line describing the essential *why*, not the *what*.
- NEVER write multi-line prose blocks narrating how code works, restating the code, or explaining test steps line by line.
- Keep the idiomatic Go one-line doc comment on exported identifiers (packages, types, funcs, vars) — godoc and linters expect them. Keep these terse.
- No decorative dividers, no changelog/history comments, no commented-out code.
