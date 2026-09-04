---
status: completed
created_at: 2026-09-04
files_edited:
  - internal/gateway/
  - internal/cli/gateway.go
  - internal/cli/gateway_bindings.go
  - internal/cli/gateway_runner.go
  - internal/cli/auth.go
  - internal/cli/chat.go
  - internal/agent/loop.go
  - internal/authstore/
  - internal/config/
  - internal/content/
  - internal/contextmanager/
  - internal/models/
  - internal/providers/
  - internal/session/
  - internal/summary/
  - smidja.go
  - README.md
  - docs/auth.md
  - docs/settings.md
  - substrate/traces/plans/2026-08-24-smidja-harness-plan.md
rationale: Complete and close Fase 4 with Telegram and web gateways, cache-preserving session continuity, Pi-aligned trusted content and configuration, provider replay contracts, and verified quality and security gates
supporting_docs:
  - substrate/traces/plans/2026-08-24-smidja-harness-plan.md
  - README.md
  - docs/auth.md
  - docs/settings.md
---

# Smidja Fase 4 gateway implementation

## Summary of changes

Completed the remote gateway phase with Telegram rich-message delivery, a loopback web gateway, concurrent session routing, durable journal recovery, cache-preserving JSONL reopen, deterministic resume digests, trusted bundle and user configuration, stored gateway credentials, and provider request-prefix replay contracts.

The original implementation landed in commits `9f8789e` through `5e7e364`. Closure corrections added cross-process authstore read-modify-write locking, stored web-token wiring, strict `settings.json` support, canonical content precedence, rooted bundle settings/instructions/models, configurable model-catalog sourcing, provider and retry edge semantics, and replay goldens for OpenAI Completions, Anthropic Messages, Gemini, and OpenAI Responses.

## Technical reasoning

Variation V-015 replaced the baseline Telegram/Discord sequence with Telegram plus web and required every remote turn to reuse the same session prefix. Session files therefore reopen under an exclusive lock, validate strict JSONL input, preserve append continuity, and reset runtime profiles when provider, model, system prompt or tool order changes. The resume digest is delivered to the user but never added to model context.

Telegram uses Bot API rich messages and rich drafts as its primary output path, with legacy chunking only after API 400 or 404 responses. The web gateway binds to loopback, uses token authentication, CSRF and same-origin checks for cookie-authenticated writes, and shares the gateway kernel's journal, scheduler, rate limits and cancellation semantics.

The closure audit found commitments that the initial implementation had not finished. Auth mutations now lock a sidecar file and reload current disk state before writing, so concurrent processes cannot overwrite each other's credentials. Settings and static content resolve from trusted tiers with bundle-first precedence, while credentials and runtime state remain outside bundles. Per-driver golden requests prove that reopening and appending a session preserves every prior request element byte for byte.

## Impact assessment

- Fase 4 is complete. The living plan remains in progress because Fase 5 has not started.
- Discord, the unfinished `smidja run` command and dynamic no-rebuild extensions remain outside Fase 4 scope.
- The phone-workday criterion could not be proven from local journal metadata. The user explicitly approved deferring it as post-phase adoption evidence under V-016.
- User-facing configuration now includes `~/.smidja/settings.json`, bundle settings, `SMIDJA_MODELS_CATALOG_URL`, provider defaults and retry settings. `docs/settings.md` records the supported schema and precedence.
- Non-blocking hardening notes remain for root-symlink defense in depth, stale non-Linux lock recovery, models-store directory-mode consistency, web login throttling, stricter missing-Origin handling, future multi-user transcript filtering and SSE client caps.

## Validation steps

- Frozen review range: `a08ea0b5f1e2cde5075e0db19a66843e80ce61e9..7184165fe3599c343813e954a289d4b3080dc74a`.
- Frozen artifact: tree `1b329e71f65fad3118eed4f2e413ecb20127432d`, changed-file manifest SHA-256 `2c9d6c70469f28e928c002fd0a73dfdc9899edefa852caedff11ee1e738176f3`, binary diff SHA-256 `05b81207373af1aea4e0c7d2a610bf2c2af6b5cafd69f3d7b6c0ccfbcd8d57b1`.
- Orchestrator verification passed: `git diff --check`, `gofmt -l .`, repository comment check, `go build ./...`, Linux and Darwin static cross-builds, targeted Windows builds for authstore/session, `go vet ./...`, and `go test -count=1 ./...`.
- Stress verification passed: 20 auth concurrency repetitions, 20 replay-golden repetitions per provider package, 100 gateway cancellation repetitions, and `go test -race` across all 15 delta packages.
- Aggregate delta-package statement coverage was 89.2%.
- Dedicated quality judgment returned PASS. Focused strict read-only security review returned PASS on the same artifact.
- Full Windows build remains unsupported because unchanged `internal/tools/exec.go` uses Unix process primitives. A repeated pre-existing MCP timing test can fail outside the Fase 4 delta; the required full suite passed.
