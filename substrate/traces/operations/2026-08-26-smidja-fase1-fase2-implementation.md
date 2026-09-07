---
status: completed
created_at: 2026-08-26
updated_at: 2026-09-07
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
  - README.md
  - docs/brew.md
  - internal/models/catalog_test.go
  - digitalygo/homebrew-smidja/Formula/smidja.rb
rationale: Implement Fase 1 and Fase 2 of the Smidja harness plan, then publish the verified v0.3.0 distribution and update the public Homebrew tap.
supporting_docs:
  - substrate/traces/plans/2026-08-24-smidja-harness-plan.md
  - docs/sdk-parity-matrix.md
  - docs/providers-manifest.md
  - docs/auth.md
  - docs/brew.md
  - https://github.com/digitalygo/smidja/releases/tag/v0.3.0
  - https://github.com/digitalygo/homebrew-smidja
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

## Update 2026-09-07: Smidja v0.3.0 and Homebrew

### Summary of new work

Published the annotated `v0.3.0` tag and verified GitHub release, then advanced the public `digitalygo/homebrew-smidja` formula from v0.2.0 to v0.3.0. Corrected the public Brew guide before tagging and fixed one Go 1.26-only test compatibility failure found by the release gate.

### Technical reasoning for the update

The release candidate included all completed Fase 4 and Fase 5 technical work after v0.2.0. The repository declares Go 1.26, so the release gate used Go 1.26.6 rather than accepting a test that passed only with Go 1.27. The Homebrew formula stayed source-based and changed only its tag URL and the SHA-256 of that exact GitHub source archive.

The existing release workflow was used with the literal approved tag. Mutable Action references and direct tag-expression interpolation remain documented non-blocking hardening work outside this release delta.

### Impact assessment for the update

`v0.3.0` is the latest public non-prerelease release and Homebrew resolves formula version 0.3.0. Users on v0.2.0 can update with `brew update && brew upgrade smidja`.

The formula still builds from source, reports origin `github.com/digitalygo/smidja`, version `v0.3.0`, and commit `none` because the source archive has no git metadata. No production Go behavior changed during the release task. Fase 5 external creator acceptance remains pending.

### Validation steps for the update

- Verified annotated tag object `8cc64ca7c3808c2806cd8c30a372098cf66aa926` peels to release commit `474199ff0b68f074579ea45914e727abc474cebc`.
- Ran Go 1.26.6 module verification, formatting, build, vet, the full test suite and the full race suite before tagging.
- Built all four static release targets locally and verified exact inventory, checksums, platform metadata and embedded build identity.
- Verified release workflow run 34111083497 succeeded and downloaded all five remote assets from the public release; checksum and identity checks passed.
- Downloaded the exact Homebrew source URL over HTTPS, verified SHA-256 `b5f068ec8eb3a915d950a64e0f2e701c366db7757085d9eec8c447465a50a447`, and confirmed its extracted tree matched the tagged git tree.
- Built the formula source in isolation with its exact ldflags and verified `smidja v0.3.0` plus JSON origin, version and `commit: none`.
- Recorded quality PASS for test patch `9cee5f1084bfed2abb38cecbd2907046388eccaff30996daf9937956b28c2faf`.
- Recorded quality PASS and focused supply-chain security PASS for formula patch `16f663e7fdb1545e3c08b455b430b80da1278923252e03d2f8d45d505c38af7b`.
- Verified tap commit `5a0d7d3` is published, Brew resolves v0.3.0, and `brew fetch --build-from-source` succeeds without upgrading the user's installed v0.2.0.
- Homebrew audit and style still report two pre-existing formula indentation and component-order findings. They are unchanged from v0.2.0 and outside the two-field metadata delta.
