---
status: completed
created_at: 2026-08-26
files_edited:
  - sdk/
  - smidja.go
  - internal/cli/root.go
  - internal/cli/chat.go
  - internal/cli/auth.go
  - internal/cli/import.go
  - internal/cli/update.go
  - internal/extensions/
  - internal/contextmanager/
  - internal/subagent/
  - internal/retry/
  - internal/loopdetector/
  - internal/session/codec.go
  - internal/session/loader.go
  - internal/sessionimport/
  - internal/providers/
  - internal/authstore/
  - internal/models/
  - internal/ui/
  - internal/update/
  - internal/buildinfo/
  - scripts/build-release.sh
  - .github/workflows/release.yml
  - docs/sdk-parity-matrix.md
  - docs/benchmarks/phase-0.md
rationale: Implement Fase 1 (MVP interno) and Fase 2 (distribuzione) of the Smidja harness plan with Pi-parity decisions recorded in the living plan ledger
supporting_docs:
  - substrate/traces/plans/2026-08-24-smidja-harness-plan.md
  - docs/sdk-parity-matrix.md
  - docs/providers-manifest.md
  - docs/auth.md
---

# Operation: Smidja Fase 1 and Fase 2 implementation

## Summary of changes

Fase 1: smart context management as core (double-criterion prune/compact, safety compaction, pins, verbatim selection), extension registry with option B interfaces and full-parity context API, Pi-exact retry with default 10 attempts, loop detector ported from the user's Pi extension as core, complete Pi v3 session codec with import command, model registry, line UI, deterministic self-update, golden compatibility fixtures validated in both directions against installed Pi.

Fase 2: release pipeline for four targets, public composition seam (`smidja.Run`), the Digitalygo bundle repository (github.com/digitalygo/smidja-digitalygo), provider core refactor with isolated Pi-shaped auth store, three new wire protocol drivers (anthropic-messages, gemini, openai-responses with codex/azure variants), a frozen 32-provider API-key manifest, OAuth flows for OpenRouter/Claude Pro-Max/Codex/xAI/Kimi, and auth CLI (`auth login/logout/status`, `-provider` selection) plus brew formula template.

## Technical reasoning

All scope decisions were negotiated with the user and recorded as plan variations V-004..V-012: context management and loop detection moved to core; loop unbounded like Pi; every memory limit aligned to Pi; retry identical to Pi except default 10; extension error policy copied from Pi; Bedrock and Copilot excluded from provider parity; Radius dropped (requires the pi-messages protocol). The solution-architect challenge validation corrected the original sequencing (protocol drivers before config variants), froze the auth.json format to full Pi compatibility, and supplied exact OAuth callback specifications per provider.

## Impact assessment

- Both repos published: harness on `alpha`, bundle on `main`; release workflows publish assets matching `internal/update` expectations.
- macOS updates are brew-only by design; Linux binaries self-update via GitHub Releases.
- Manual validation pending (post-development per user decision): team daily use, multi-machine update, external install test.
- Deferred follow-ups: content resolver for bundle FS, custom-entry provenance for steering messages, GitHub Actions SHA pinning (hardening advice).

## Validation steps

- Per-block gates: go build/vet/gofmt clean and full test suite green before each commit (25 packages at close); -race on touched packages.
- Live smoke through the complete stack against real OpenRouter credentials.
- Golden fixtures: byte-exact round-trip of a sanitized real Pi session; import of the unmodified private session verified byte-exact locally; installed Pi 0.84.2 reads smidja-written sessions.
- Quality gate PASS (direct mode after dedicated-review measurement nondeterminism; one real finding fixed: sessionimport check-then-rename race replaced by link(2) atomic commit).
- Security gate PASS after correction cycles fixing five real findings (argument-patch chain to execution/recording/detection, session id traversal, detector steer provenance, terminal control-char injection, stale batch authorization).
