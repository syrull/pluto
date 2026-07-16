---
name: run-tests
description: How to run Pluto's test suite and pre-commit checks (make test/vet/fmt-check/all), scope a single package or test, and read failures. Use before pushing or when verifying a change.
---
# Run the test suite and pre-commit checks

Match what CI enforces before pushing (`.github/workflows/ci.yml`):

- `make test` — runs `go test ./...` across every package.
- `make vet` — `go vet ./...`.
- `make fmt-check` — fails if any file needs `gofmt`; run `make fmt` to fix.
- `make all` — fmt-check, vet, test, then build in one shot.

Scope a single package while iterating: `go test ./internal/skills/`.
Run one test by name: `go test ./internal/tools/ -run TestSkillLoadsBody -v`.

`find` (the search tool) and CI both need `ripgrep` (`rg`) on PATH.
Read failures top-down: the first failing assertion usually explains the rest.
