# CLAUDE.md

Guidance for AI agents working in this repo (`github.com/syrull/pluto`).

## Comments

Keep comments minimal. Only write what is essential.

- Prefer no comment over an obvious one. Let clear names and code speak.
- When a comment is needed, keep it to a single short line describing the essential *why*, not the *what*.
- NEVER write multi-line prose blocks narrating how code works, restating the code, or explaining test steps line by line.
- Keep the idiomatic Go one-line doc comment on exported identifiers (packages, types, funcs, vars) — godoc and linters expect them. Keep these terse.
- No decorative dividers, no changelog/history comments, no commented-out code.
