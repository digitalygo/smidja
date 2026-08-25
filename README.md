# Smidja

Agentic coding harness written in Go, distributed as a single static binary with zero runtime dependencies. MIT licensed.

Smidja (Icelandic spelling: Smiðja) means "forge" in Old Norse. It is the forge for code and AI agents: one binary that carries your entire agentic workflow inside it.

## Why

Existing harnesses proved that a minimal core where everything is an extension works well. But they ship as TypeScript on Node.js: heavy runtime, fragile dependency trees, installs that require an ecosystem. Smidja keeps the minimal-core idea and compiles it down.

A "package" here is simply a build of the binary with content baked in: skills, agents, subagents, extensions, prompts. Installing someone's package means getting a harness born with their workflow inside. Updating means updating the binary, never syncing scattered files.

## Principles

- Single static binary, zero dependencies, install and done.
- Everything is an extension, compiled into the build.
- Content ships inside the binary via embed, never through external sync tools.
- Smart context management built in: tool pruning below thresholds, verbatim message selection instead of lossy summaries.
- Deterministic updates: every build knows its origin, rollback is reinstalling the previous version.
- Respect existing standards: reads `AGENTS.md` and `~/.agents/skills/`, keeps its own state in `~/.smidja/`.

Content precedence, highest first: repository workspace > baked-in package > dynamic skills in `~/.smidja/` > `~/.agents/` > core defaults.

## Status

Fase 0 (spike) complete: a working Go harness with OpenRouter streaming, tools, Pi-aligned JSONL sessions, and measured benchmarks against Pi (see `docs/benchmarks/phase-0.md`). Execution ledger lives in the plan under [substrate/traces/plans](substrate/traces/plans/2026-08-24-smidja-harness-plan.md). Next: Fase 1, MVP interno.

## License

MIT. Every fork builds its own binaries; there is no central build authority.
