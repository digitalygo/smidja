# Smidja

Agentic coding harness written in Go, distributed as a single static binary with zero runtime dependencies. MIT licensed.

Smidja (Icelandic spelling: Smiðja) means "forge" in Old Norse. It is the forge for code and AI agents: one binary that carries your entire agentic workflow inside it.

## Why

Existing harnesses proved that a minimal core where everything is an extension works well. But they ship as TypeScript on Node.js: heavy runtime, fragile dependency trees, installs that require an ecosystem. Smidja keeps the minimal-core idea and compiles it down.

Two distribution shapes exist, and they are not interchangeable. A compiled bundle is a full build of the binary with skills, agents, prompts, and Go extensions baked in: installing someone's bundle means running their binary with their workflow already inside, and updating means updating the binary, never syncing scattered files. You create one from the public [smidja-bundle-template](https://github.com/digitalygo/smidja-bundle-template) and distribute it as release binaries. A content-only package is a versioned public GitHub repository of markdown content and config defaults with no code at all: `smidja pkg` installs it into an existing harness and it extends the workflow without touching the binary.

## Principles

- Single static binary, zero dependencies, install and done.
- Everything is an extension, compiled into the build.
- Binary-first distribution: a bundle carries its skills, agents, prompts, and extensions inside the binary, and optional packages extend the harness after install.
- Smart context management built in: tool pruning below thresholds, verbatim message selection instead of lossy summaries.
- Deterministic updates: every build knows its origin, rollback is reinstalling the previous version.
- Respect existing standards: reads `AGENTS.md`, keeps its own state in `~/.smidja/`.

Content precedence, highest first: bundle > trusted workspace > user content in `~/.smidja` > active packages > core defaults.

## Status

Fasi 0-4 are complete, and the Fase 5 technical creator tooling is now available: the creator guides for compiled bundles and content packages plus the public [smidja-bundle-template](https://github.com/digitalygo/smidja-bundle-template) are published, the public [smidja-packages](https://github.com/digitalygo/smidja-packages) package catalog is available for discovery, and real external clean-room creator validation is still pending.

- Fase 0 (spike): a working Go harness on OpenRouter with streaming, tools, and Pi-aligned JSONL sessions, benchmarked against Pi (see `docs/benchmarks/phase-0.md`).
- Fase 1 (internal MVP): smart context management, extension hooks, `smidja import` for Pi sessions, and deterministic self-update.
- Fase 2 (distribution): extended providers through the API-key manifest and OAuth flows, the GitHub release pipeline, the brew formula, and `smidja pkg` installs from public repositories.
- Fase 3 (optional packages): skills, agents, prompts, and config defaults ship in opt-in packages activated with `smidja pkg`, keeping the base binary lean.
- Fase 4 (remote gateway): `smidja gateway` runs a Telegram bot and a local web server over the same session primitives as the CLI.

Final gate details and the phase-by-phase execution ledger live in the plan under [substrate/traces/plans](substrate/traces/plans/2026-08-24-smidja-harness-plan.md).

## Docs

- [Auth](docs/auth.md): how provider credentials work, `smidja auth` commands, where tokens live
- [Settings](docs/settings.md): the settings files, supported fields, and configuration precedence
- [Brew tap](docs/brew.md): the future `github.com/digitalygo/homebrew-smidja` tap and its formula
- [Providers manifest](docs/providers-manifest.md): the frozen API-key provider catalogue
- [Creating compiled bundles](docs/creating-bundles.md): the bundle template, the `sdk.Bundle` contract, build identity, and release assets
- [Creating content packages](docs/creating-content-packages.md): the package manifest, validation rules, and the `smidja pkg` lifecycle
- [Public package catalog](https://github.com/digitalygo/smidja-packages): the publisher-maintained discovery index for content packages; listing is not endorsement and installation never depends on it

## License

MIT. Every fork builds its own binaries; there is no central build authority.
