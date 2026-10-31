# Revamp verification evidence

## Core CI snapshot

Smidja commit `e2ff613b7aed17ac1c68bb7a25c2809ba11d4c1e` passed all seven checks on 2026-10-30:

- Native Linux amd64 and macOS 26 arm64 race tests for eleven new packages, scoped CLI tests, changed-code coverage gates and vet.
- Cross-builds for Linux/macOS amd64/arm64. Native Linux arm64 and macOS amd64 execution are not implied by cross-builds.
- Sandboxed Chromium browser smoke on macOS, including login/logout, chat/read-only state, task actions, PR review submission, file editing/conflicts, Git, approvals, packages, devices, and mobile overflow assertions.

Go workflow: https://github.com/digitalygo/smidja/actions/runs/41108124025

Browser workflow: https://github.com/digitalygo/smidja/actions/runs/41108123995

The browser job produced the `manager-browser-smoke` artifact, ID `13002516050`. Automated rendered interactions passed. Downloading the artifact for separate human-style visual inspection was not yet available at the time of this record; screenshot pixels are not claimed inspected.

The first Ubuntu 24 browser runner could not initialize Chromium's sandbox before application execution. Browser verification moved to a supported macOS runner with sandboxing retained; no sandbox disable flag or host security-policy change was used.

## Actual cross-repository offline integration

A separate Envai execution environment completed the combined smoke with Envai commit `c907371b252244cf7a2475d6326e90a28da82880` and Smidja commit `d0f8ebf929e4d0ad46ff19887d2496fab0d2b7ba`. Smidja production code is identical at `e2ff613b7aed17ac1c68bb7a25c2809ba11d4c1e`; intervening changes affect tests and CI only.

The smoke exercised real Smidja manager/client processes, real Envai HTTP handlers, orchestration runtime, native job lifecycle, client-tool broker and SSE. The provider and catalog/ownership/accounting used offline fixtures. It did not certify live providers, PostgreSQL, JWT infrastructure, GitHub APIs or a deployed service.

Observed results:

- The Smidr remained available while native child work ran.
- A four-phase Verk blocked at phase 2 and resumed with attempts `[1, 2, 1, 1]`, without repeating phase 1.
- Ten child executions, five synthesis turns, five client-tool calls and five classifications completed.
- Integral phase outputs were preserved; only blocked/final Verk notifications reached the Smidr through native delivery.
- Unavailable strict model admission returned 422 and paused the task without an extra provider call.

The executing environment retained the evidence archive `smidja-envai-offline-smoke-evidence.tar.gz`. A durable report is recorded at https://github.com/digitalygo/envai-teams/pull/56#issuecomment-6530739444. This is offline integration evidence, not paid-model validation or deployment approval. Later production changes require their own affected-path validation; this historical result must not be silently attributed to a newer runtime.

## Remaining limits

The full legacy suite has environment-sensitive PTY/theme/socket and MCP timing failures outside the revamp delta. These failures were not removed or counted as passing. Organization/template/discovery migration remains separate work. Homebrew waits for a real authorized release. No release, merge, deployment or repository archival is implied by these checks.

## Final admission and cancellation revision

After an execution-workspace reset, the unpublished local revision was reconstructed from the previously reviewed contracts on published baseline `313259e07d43decf9a99519c5d5c9a5692edcc1b`. The reconstruction requires fresh review and exact-head CI; equivalence to the lost Git tree is not claimed.

A real subprocess regression exercises the authenticated task-cancel API with manager-local execution, an HTTP-connected client and a failed remote cancellation acknowledgement. It checks that the helper process is gone, the exclusive phase observer stops, unrelated Smidr observation remains live, and cancelled observation cannot restart. The same tests fail against the baseline cancellation implementation, including cancellation before the remote phase-attempt attachment.

Prior cross-repository timeout checks passed the 40-second declaration/31-second helper and one-second declaration/two-second helper cases on `313259e`. A task-cancellation check on that baseline exposed a live helper after 15 seconds; this is a confirmed failure repaired by the new observer-context cancellation path, pending final cross-repository verification.
