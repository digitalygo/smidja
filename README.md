# Smidja

Agentic coding harness written in Go, distributed as a single static binary with zero runtime dependencies. MIT licensed.

Smidja (Icelandic spelling: Smiðja) means "forge" in Old Norse. It is the forge for code and AI agents: one binary that carries your entire agentic workflow inside it.

## Why

Existing harnesses proved that a minimal core where everything is an extension works well. But they ship as TypeScript on Node.js: heavy runtime, fragile dependency trees, installs that require an ecosystem. Smidja keeps the minimal-core idea and compiles it down.

A "package" here is simply a build of the binary with content baked in: skills, agents, subagents, extensions, prompts. Installing someone's package means getting a harness born with their workflow inside. Updating means updating the binary, never syncing scattered files.

## Principles

- Single static binary, zero dependencies, install and done.
- Everything is an extension, compiled into the build.
- Binary-first distribution: a bundle carries its skills, agents, prompts, and extensions inside the binary, and optional packages extend the harness after install.
- Smart context management built in: tool pruning below thresholds, verbatim message selection instead of lossy summaries.
- Deterministic updates: every build knows its origin, rollback is reinstalling the previous version.
- Respect existing standards: reads `AGENTS.md`, keeps its own state in `~/.smidja/`.

Content precedence, highest first: bundle > trusted workspace > user content in `~/.smidja` > active packages > core defaults.

## Status

Implementation is complete through Fase 4 (remote gateway): the repository builds, `go vet` is clean, and the full test suite is green.

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

## License

MIT. Every fork builds its own binaries; there is no central build authority.
