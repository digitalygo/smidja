---
status: completed
created_at: 2026-08-25
files_edited:
  - .gitignore
  - go.mod
  - cmd/smidja/main.go
  - internal/agent/types.go
  - internal/agent/tools.go
  - internal/agent/client.go
  - internal/agent/loop.go
  - internal/cli/cli.go
  - internal/config/config.go
  - internal/openrouter/client.go
  - internal/openrouter/stream.go
  - internal/openrouter/types.go
  - internal/session/session.go
  - internal/session/uuid.go
  - internal/tools/tools.go
  - internal/workspace/workspace.go
  - bench/build.sh
  - bench/metrics.sh
  - bench/run-task.sh
  - docs/benchmarks/phase-0.md
rationale: Execute Fase 0 (spike) of the Smidja harness plan: minimal Go agentic loop on OpenRouter, Pi-aligned JSONL sessions, tools, CLI, and measured comparison against Pi
supporting_docs:
  - substrate/traces/plans/2026-08-24-smidja-harness-plan.md
  - docs/benchmarks/phase-0.md
  - docs/decision-record-draft.md
---

# Operation: Smidja Fase 0 spike implementation

## Summary of changes

Implemented the complete Fase 0 spike of the Smidja agentic harness in Go: a single static binary (`cmd/smidja` + `internal/{agent,cli,config,openrouter,session,tools,workspace}`) with an OpenRouter SSE streaming client, four built-in tools (read/write/edit/exec), Pi-v3-aligned JSONL session persistence under `~/.smidja/sessions/`, a minimal line-oriented CLI, plus a benchmark harness (`bench/`) and the recorded comparison against Pi 0.84.2 in `docs/benchmarks/phase-0.md`.

## Technical reasoning

The plan baseline froze Go, MIT, zero external dependencies, OpenRouter-only for day 0, and JSONL sessions aligned to Pi format version 3. The solution-architect proposal accepted direct `net/http` SSE parsing over the openai-go SDK for the spike to keep a pure standard-library footprint and full control over OpenRouter-specific wire shapes (tolerant cost decoding, index-keyed tool-call accumulation). The workspace directory question planned for closure in Fase 0 was resolved in favor of `<repo>/.smidja/`. Alternatives considered and rejected: openai-go SDK (extra deps, weaker reasoning-field fidelity), generic `.agent/` workspace (second undefined namespace), full Pi v3 feature parity (branching, compaction, import deferred to Fase 1).

Implementation ran in delegated waves: foundation contracts first (frozen interfaces), then parallel openrouter/session/tools packages, then agent loop + CLI integration, then the bench harness. A real-stream bug (OpenRouter sending `usage.cost` as a bare number) was caught by the live demo and fixed with tolerant decoders plus regression tests.

## Impact assessment

- The repository now contains the working spike base that Fase 1 (MVP interno) builds on: smart context management, extension hook registry, Pi import command, Digitalygo packaging, self-update.
- Session files written by the spike are readable by installed Pi clients (format v3 verified both directions of observation); no import compatibility is claimed yet.
- Bench tooling debt: `bench/metrics.sh` idle phase reuses startup arguments (manual procedure documented and substituted); one paired trial per task only (indicative numbers).
- Security hardening narrowed bench result details to generic reason strings; transcripts remain in `out.log`.

## Validation steps

- Per-wave verification by direct inspection: `go build ./...`, `go vet ./...`, `gofmt -l .`, `go test -count=1 ./...` all green after every wave (final coverage: agent 90.8%, cli 79.2%, config 83.9%, openrouter 90.0%, session 75.8%, tools 87.0%, workspace 83.8%).
- Live end-to-end demo against real OpenRouter (key from local Pi auth store, never committed): model created and executed code through the exec tool; resulting session JSONL verified Pi-v3-conformant.
- Static binary verified via `file(1)`: CGO_ENABLED=0 stripped ELF, 6,443,170 bytes.
- Paired benchmarks vs pi 0.84.2 recorded in docs/benchmarks/phase-0.md: startup median 1.49 ms vs 584.5 ms; idle RSS median 5,364 KB vs 182,496 KB tree; task1/2/3 solved by both harnesses with wall times within provider variance.
- Quality gate: dedicated quality-gate subagent PASS after metadata-complete refreezes (full-session package v3 hash `8161e0bf…`, final incremental cursor `ddd94a16…`).
- Security gate: dedicated security-review-specialist PASS after seven correction rounds on cumulative package `064cd7a5…` (51 files); blocking findings were concentrated in bench runner script hardening and SSE stream bounds, all resolved with adversarial suites (31/31 final) and orchestrator-recomputed hashes at every step.
