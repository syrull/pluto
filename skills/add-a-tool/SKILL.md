---
name: add-a-tool
description: How to add a new tool to Pluto — implement the tool.Tool interface, build its args schema, register it in main.go, ship debug instrumentation, and add tests. Use when creating or wiring up a new agent tool.
---
# Add a new tool

A tool is a type implementing `tool.Tool` (`internal/tool/tool.go`):
`Name`, `Description`, `Schema`, and `Execute`.

1. Create `internal/tools/<name>.go` with a struct and the four methods. Keep
   per-tool usage rules (e.g. "prefer this over cat/grep", output bounds) in
   `Description` — it rides in the prompt every turn, so the base prompt does
   not repeat them.
2. Build the args schema with `tool.ObjectSchema(props, required...)`; unmarshal
   raw JSON args in `Execute` and validate them.
3. Resolve filesystem paths with `workdir.Resolve(ctx, path)` so multi-agent
   worktrees stay isolated. Bound any output that could be large.
4. Register it in `main.go` with `reg.MustRegister(tools.<Name>{})`.
5. Ship debug instrumentation in the same change (see CLAUDE.md): tag events
   `tool`, wrap I/O/subprocess/network in a `debug.NewTimer`, log the decision,
   the success, and the error branch. Never log secrets.
6. Add tests in `internal/tools/<name>_test.go`, including invalid-args and
   empty/missing cases. Copy the shape of `read_test.go`.

Look at `internal/tools/read.go` and `skill.go` as templates.
