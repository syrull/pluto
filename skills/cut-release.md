# Cut a release

Releases are automatic — there is no manual tag step.

- Every push to `main` runs `.github/workflows/release.yml`, which bumps the
  patch version (starting at `v0.0.1`) and publishes a tagged GitHub release with
  prebuilt binaries.
- The version is injected at build time via `-ldflags "-X main.version=…"`.
- To land a change without cutting a release, include `[skip release]` in the
  commit message; the release job skips it.

Users update in place with `pluto update` and check the running build with
`pluto version`. So: to release, merge to `main`; to hold back, add
`[skip release]`.
