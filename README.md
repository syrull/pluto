# pluto

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

## Releases

Releases are published automatically from `main`. Every push bumps the patch
version (starting at `v0.0.1`) and creates a tagged GitHub release with
prebuilt binaries. Add `[skip release]` to a commit message to skip it.
