---
status: completed
created_at: 2026-10-06
files_edited: [sdk/, internal/extensions/, internal/extensionui/, internal/tui/, internal/tui/interactive/, internal/ui/, internal/cli/, internal/config/, internal/content/, internal/agents/, internal/agent/, internal/models/, internal/openrouter/, internal/tools/exec_direct.go, README.md, docs/agents.md, docs/prompts.md, docs/sdk-runtime.md, docs/sdk-ui.md, docs/tui.md, docs/themes.md, docs/keybindings.md, docs/settings.md, docs/auth.md, docs/sdk-parity-matrix.md, docs/tui-handoff.md, docs/transcript-search.md]
updated_at: 2026-10-07
rationale: [Resume preserved P5 work without discarding it, Complete P6 configuration and trusted interactive startup, Preserve print and non-TTY contracts]
supporting_docs: [substrate/traces/plans/2026-09-14-smidja-tui-plan.md, substrate/traces/plans/2026-10-07-smidja-runtime-completion-plan.md, docs/tui-handoff.md, substrate/traces/plans/2026-08-24-smidja-harness-plan.md]
---

# Smidja TUI implementation

## Summary of changes

Recovered and published P5 fullscreen extras as `1a9f71a83a212105f1c98e433d842f3a03a5a8d9`. Completed and independently gated P6 code and documentation for UI configuration, user themes and keybindings, per-run workspace trust, and cancellable OAuth startup. P7 and final installed-binary acceptance remain separate unfinished phases; this record does not claim whole-task completion.

The append-only [TUI living plan](../plans/2026-09-14-smidja-tui-plan.md) owns detailed phase evidence and current status. The [harness plan](../plans/2026-08-24-smidja-harness-plan.md) still requires genuine external-creator ecosystem acceptance.

## Technical reasoning

P5 continues the advanced preserved candidate rather than restarting a race. Corrected fullscreen iTerm2 fallback before file reads, explicit unsupported math warnings, and bounded interrupted search paste cleanup. The accepted degradation matrix keeps unsupported source content intact instead of inventing output.

P6 reuses one terminal runner and the existing P3 dialogs. Workspace trust is per invocation, before project content, instructions and optional MCP startup. Consent does not enable workspace MCP without its existing explicit flag. Missing supported OAuth credentials use masked, cancellable startup dialogs; credential commit and cancellation have one serialized settlement owner. Cooperative provider workers are joined, with a bounded grace for uncooperative injected callbacks. Print and non-TTY paths retain their existing policy.

UI settings follow the existing configuration tiers. Appearance pairs select their light or dark entry after a bounded terminal background query, with a dark fallback. User keybindings and active custom theme hot reload now reach the real runner, and watchers stop on teardown. No new dependencies, persistent trust database or provider-driver rewrite was introduced.

## Impact assessment

The actual TUI now exposes the P0-P6 product surface. The public SDK remains frozen through P6. Extension-facing custom UI surfaces remain P7 work, and known runtime-action gaps discovered during documentation remain an additional audit item rather than a false parity claim.

No gateway, package-system, provider-driver, updater, session-codec or release-script change belongs to these slices. Nothing is merged, tagged, released or published as a package. The only authorized installation is the local test binary, after publication and installed-runtime checks.

## Validation steps

- Inspected actual source and diffs, preserved pre-existing candidate changes and verified immutable file-hash manifests.
- Independently ran formatting, whitespace checks, vet, build, uncached focused tests, affected race tests, real PTY tests, four static release builds, dependency checks and protected-path checks.
- P5 artifact `P5-31ddcc27dedc`: SHA-256 `31ddcc27dedc1dd8b9beaab1336440c5e4353a81dcafc7256b7987cd083068b6`, 70 files. Delegated quality and focused security verdicts both `PASS`. All 36 Linux-instrumented production delta files reach 80.2-100% changed instrumented-line coverage and 82.4-100% overlapping-block statement coverage.
- P6 artifact `P6-880065109aac`: SHA-256 `880065109aac0590bdf10dedae79998fd1c0c99af23eaca2a776bb421544d763`, 48 files excluding traces. Delegated quality and focused security verdicts both `PASS`. A documentation-only follow-up scopes TTY detection wording to non-file test doubles; executable hashes are unchanged.
- P6's new cancellation-result race failed independent verification, was corrected in production without loosening the test, and passed 32 repeated login race runs plus the complete affected race suite afterward.
- A full P6 suite passed once. Later attempts exposed only unchanged MCP restart and session timestamp-order flakes, independently reproduced on untouched `origin/alpha`. The unchanged terminal input-lock assertion also reproduced under race on archived published P5; the latest affected race run passes. These failures are reported, not concealed or fixed in unrelated slices.
- Go remains stdlib-only with unchanged `go.mod` and no `go.sum`. Non-executable Markdown has tests and coverage N/A.

### P6 per-file delta coverage

Coverage intersects changed source lines with Go instrumented blocks. Statement percentages conservatively include complete blocks overlapping those changes. Profile: `/tmp/smidja-p6-final.cover`.

| File | Changed instrumented lines | Overlapping-block statements |
| --- | --- | --- |
| `internal/cli/chat.go` | 88.7% | 93.2% |
| `internal/cli/root.go` | 100.0% | 100.0% |
| `internal/cli/tui_bridge.go` | 100.0% | 100.0% |
| `internal/cli/tui_startup.go` | 95.0% | 96.1% |
| `internal/config/config.go` | 100.0% | 100.0% |
| `internal/config/settings.go` | 100.0% | 100.0% |
| `internal/config/theme.go` | 100.0% | 100.0% |
| `internal/content/content.go` | 100.0% | 100.0% |
| `internal/content/instructions.go` | 100.0% | 100.0% |
| `internal/tui/terminal.go` | 100.0% | 100.0% |
| `internal/tui/theme_loader.go` | 100.0% | 100.0% |
| `internal/ui/oauth_login.go` | 97.8% | 97.6% |
| `internal/ui/selectors.go` | 100.0% | 100.0% |
| `internal/ui/tui_runner.go` | 100.0% | 100.0% |
| `internal/ui/tui_theme.go` | 96.0% | 97.4% |

## Update 2026-10-06: P6 publication and installed-runtime acceptance

### Summary of changes

P6 is committed and published as `b091d74506b4aa98970c3f6f934ca406fd1547a1`, `feat(tui): finish configuration and interactive startup`. Its exact binary is installed at `/home/luca/.local/bin/smidja` with version `v0.3.0-tui.1` and origin `github.com/digitalygo/smidja`.

### Technical reasoning

Installed-runtime acceptance runs the actual binary through a Linux PTY with isolated temporary homes and workspaces, a deterministic local HTTP/SSE fixture and a local egress-blocking proxy. It does not replace live-provider acceptance or external-creator validation with a false claim.

### Impact assessment

P6 is complete and P7 may begin. Nothing is merged or released. `smidja update` can replace this local prerelease with the public release.

### Validation steps

Independent installed-binary rerun passed 15 checks per mode for regular and fullscreen. It proved exact multiline submission, temporally streamed responses, a real workspace read tool and follow-up result, exit zero and exact termios restoration. Regular never entered the alternate screen; fullscreen entry, response and exit occurred in order. Main, origin and PR 1 match the installed commit; installed SHA-256 is `bc9183cc9dc1eeee9deca6ec4cfeb8375da8d69a63ca90102c4e73f41f378d5a`. Report: `/tmp/smidja-p6-independent-installed-report.json`. All temporary test resources were cleaned up and no test process remains.

## Update 2026-10-07: P7 extension UI source accepted

### Summary of changes

P7 adds optional `sdk.ExtendedUI` and `sdk.UIRegistrationAPI` contracts and real per-runner adapters for custom components, modals, widgets, header/footer, distinct message/entry renderers, Markdown transformation, custom editors, autocomplete, terminal hooks, working indicators, themes and expanded tools. All existing SDK signatures remain unchanged. Publication, final installation and expanded acceptance are distinct follow-up gates.

### Technical reasoning

Public types stay independent of internal TUI types. Existing core mechanisms are reused rather than copied. External callbacks execute outside host locks using prepared frames and lifecycle leases. Retirement is immediate; actual disposal waits for in-flight callbacks and runs once. Nested registry notifications coalesce without recursive locks, and unchanged widgets keep their instances.

The first quality and security verdicts blocked publication for real callback deadlocks and OSC 8 control injection. Corrections preserve exact findings and add tests on the SDK wrapper paths. OSC 8 now uses bounded, strict parameter grammar and cleaned-target canonical serialization; arbitrary input slices never reach the terminal. Editor restoration transfers empty text, respects newer replacements and contains callback panics.

### Impact assessment

P7 is runtime-backed rather than a set of declarations or no-op methods. The matrix also corrects older claims: 14 existing core API actions still have no host backing and remain deferred for the broader runtime-completion work, as do the 27 later Pi event waves. Components must protect their own mutable state because raw rendering and input can be concurrent; host leases guarantee safe retirement and disposal, not arbitrary extension-code thread safety.

### Validation steps

The accepted artifact is `P7-b2df38c13ae8`, SHA-256 `b2df38c13ae8e6b84d6de7557f8709ee595689fa533f3bcf3857b0ea96714e02`, 56 files excluding traces. Final independent quality and focused security verdicts are both `PASS`. A documentation-only clarification follows those reviews; executable hashes are unchanged.

Leader reran formatting, whitespace checks, vet, build, fresh upstream-instrumented tests across seven affected packages, complete sequential race tests, 10 repeated actual-wrapper/editor/refresh/panic/OSC 8 regression sets, four static Linux/Darwin amd64/arm64 builds, and 14 explicit PTY tests without skips. Full-suite failures remain the independently alpha-reproduced MCP restart and session timestamp-order flakes. SDK interfaces are source-compatible, dependencies remain stdlib-only, `go.mod` is unchanged and `go.sum` absent.

### P7 per-file delta coverage

Profile `/tmp/smidja-p7-independent-gated.cover` instruments upstream dependencies; duplicate coverage blocks are merged with max/OR before intersecting changed lines. Declarations in the two new SDK type files and Markdown have coverage N/A.

| File | Changed instrumented lines | Overlapping-block statements |
| --- | --- | --- |
| `internal/cli/chat.go` | 100.0% | 100.0% |
| `internal/cli/session_projection.go` | 100.0% | 100.0% |
| `internal/cli/tui_bridge.go` | 100.0% | 100.0% |
| `internal/extensions/api.go` | 100.0% | 100.0% |
| `internal/extensions/api_ui.go` | 100.0% | 100.0% |
| `internal/extensions/context_ui.go` | 100.0% | 100.0% |
| `internal/extensions/runtime.go` | 100.0% | 100.0% |
| `internal/extensionui/registry.go` | 91.5% | 93.9% |
| `internal/tui/editor.go` | 100.0% | 100.0% |
| `internal/tui/editor_autocomplete.go` | 84.8% | 86.8% |
| `internal/tui/editor_extensions.go` | 83.9% | 88.7% |
| `internal/tui/editor_input.go` | 100.0% | 100.0% |
| `internal/tui/editor_mouse.go` | 100.0% | 100.0% |
| `internal/tui/external_frames.go` | 100.0% | 100.0% |
| `internal/tui/interactive/blocks.go` | 100.0% | 100.0% |
| `internal/tui/interactive/extensions.go` | 85.0% | 89.4% |
| `internal/tui/interactive/frame_sanitize.go` | 90.1% | 91.7% |
| `internal/tui/interactive/status.go` | 100.0% | 100.0% |
| `internal/tui/interactive/status_extensions.go` | 85.2% | 85.0% |
| `internal/tui/interactive/surface.go` | 100.0% | 100.0% |
| `internal/tui/interactive/transcript.go` | 90.9% | 96.4% |
| `internal/tui/theme_loader.go` | 100.0% | 100.0% |
| `internal/ui/extension_bound_ui.go` | 96.4% | 95.2% |
| `internal/ui/extension_components.go` | 83.9% | 88.3% |
| `internal/ui/extension_editor.go` | 87.6% | 91.6% |
| `internal/ui/extension_modal.go` | 97.8% | 98.6% |
| `internal/ui/extension_runtime.go` | 86.4% | 89.5% |
| `internal/ui/tui_runner.go` | 100.0% | 100.0% |

## Update 2026-10-07: P7 publication and installed acceptance

### Summary of changes

P7 is published and installed at `80f29353f48116a8c4191709064f19d4402ef718`, version `v0.3.0-tui.1`, origin `github.com/digitalygo/smidja`. Installed SHA-256 is `a5188a8ad33ee295590924788926cbb1e74275288d2f14a857ccf68b62738a70`.

### Technical reasoning

Actual installed-binary product flows and a separately compiled public SDK consumer are independently tested. The consumer does not substitute for the installed identity or a live-provider pass.

### Impact assessment

P0-P7 technical work and isolated acceptance are complete. Real-provider acceptance remains blocked by absent Smidja settings/auth files and the default OpenRouter environment key. Broader runtime work has its own [living plan](../plans/2026-10-07-smidja-runtime-completion-plan.md), preserving the TUI baseline.

### Validation steps

The leader read the harnesses in full and independently passed 116 checks across regular (17), fullscreen core (9), model selector (14), sessions (28), fullscreen UI (26), and a separate public SDK consumer (22). Wire IDs prove model switching; replay precedes requests; forks preserve originals and have valid chains; actual theme colors, dialogs, search, Markdown and image fallback pass. The consumer proves registrations, custom frames, transforms, hooks and modal completion. Main, origin, PR and installed commit match. All test resources are removed. Report: `/tmp/smidja-p7-independent-installed-report.json`.

## Update 2026-10-07: R1 run and prompt runtime accepted

### Summary of changes

R1 implements `smidja run` and consumes resolved prompt templates in one-shot, line and TUI paths. Publication follows these accepted gates; the P7 installed binary is not yet R1 evidence.

### Technical reasoning

Shared root options reuse existing one-shot machinery. Canonical `/prompt` registers before extension Setup; collisions preserve the extension under its numeric alias. Expansion is pure text, with an incremental, lazy 1 MiB local budget and no recursive shell, environment or file evaluation.

### Impact assessment

Prompt content is runtime-consumed. Agent definitions and core SDK host backing remain later phases. No SDK signature, session format, provider driver, dependency or non-TUI trust-policy change.

### Validation steps

Artifact `R1-f0c7ee0098fe`, SHA-256 `f0c7ee0098fea45dfcb3692150b57ad812e58f0cc4d29ba568a2df1c465c008c`, 14 files excluding traces. Quality and focused security both PASS. Leader reran formatting/vet/build, uncached upstream CLI/content tests, full affected race, real PTY no-TUI/collision tests and four static builds. An actual temporary binary verified expanded wire and persisted text, tool/stream output and no alternate-screen entry. Full-suite failures remain alpha-reproduced MCP/session flakes.

| R1 file | Changed instrumented lines | Overlapping-block statements |
| --- | --- | --- |
| `internal/cli/chat.go` | 90.0% | 96.7% |
| `internal/cli/prompt_command.go` | 98.0% | 98.7% |
| `internal/cli/root.go` | 100.0% | 100.0% |
| `internal/cli/run_command.go` | 100.0% | 100.0% |
| `internal/content/prompt.go` | 100.0% | 100.0% |

## Update 2026-10-07: R2a host backing accepted

### Summary of changes

Composed CLI/TUI hosts now provide real context snapshots, session metadata writes, active-tool advertisement and execution control, direct process execution, invocation-bound abort, run shutdown and manual verbatim compaction. Bare APIs and default contexts retain their original unavailable/empty contracts. Mailbox, registration/model controls and agent execution remain separate later phases.

### Technical reasoning

Session and lifecycle operations use generation guards and owned cancellation. Visible updates are generation-tagged jobs on the serial lifecycle dispatcher. Read getters clone nested content, argument bytes, usage and model data so co-handlers cannot mutate shared state. Compaction captures request scope and rechecks cancellation at the disk boundary. Callbacks run outside host locks, panic safely and are joined at teardown. Direct exec uses argv, existing environment/output hygiene, process-group cancellation and a bounded pipe-drain delay; it is not a sandbox.

### Impact assessment

Five previously declared SDK actions and real handler-context state are backed for CLI/TUI composition. Gateway composition remains outside this slice. Custom compaction instructions fail precisely through OnError rather than being silently ignored. Contained callback panics are counted, not falsely described as visible notices. No SDK signature, schema, provider driver or dependency change.

### Validation steps

Artifact `R2a-6785c984336c`, SHA-256 `6785c984336cf44fbbdaf3290e00c27f47e50157f410b1b977df75d8cfd2aa4f`, 30 files excluding traces, received repeated quality and focused security PASS after ownership corrections. Leader reran formatting, vet/build, fresh upstream tests for five affected packages, all affected sequential race tests, 10 repeated corrected regressions, real PTY host abort/shutdown/idle-compaction quit tests and four static release builds. Fifteen executable delta files reach 84.8-100% changed instrumented-line and 88.7-100% overlapping-block statement coverage (`/tmp/smidja-r2a-gated.cover`). Original tests, frozen SDK and protected subsystems remain unchanged; no go.sum. Known alpha MCP/session flakes remain reported; a newly observed unchanged MCP crash-test failure is not falsely claimed independently reproduced.

## Update 2026-10-07: R2b scheduler race and delivery accepted

### Summary of changes

The composed host now backs `SendMessage` and `SendUserMessage` with a per-session owned mailbox and actual model-loop boundaries. Editor follow-ups are delivered, not merely counted. Registration/model/thinking controls and agent execution remain later work.

### Technical reasoning

Two isolated candidates received identical contracts at `bb86387`. Both passed independent build/test/race checks; A was rejected for four conformance gaps. B preserved context/signal binding, one-item queue ordering, live Display delivery and authoritative session projection. Its late stop-boundary token-zero defect was corrected before acceptance. No losing source was grafted.

### Impact assessment

Custom messages preserve model context regardless of Display, persist exactly once before visibility and resume with valid IDs/tool pairs. Next-turn messages remain memory-only until injection. Sends never synchronously wait on their own FIFO worker; generation/cancellation/token guards prevent cross-session and ghost turns. Shared bool and idle-user semantics are documented explicitly.

### Validation steps

Artifact `R2b-65d07e6ef500`, SHA-256 `65d07e6ef50055b993b06dc760f0947465f9c4ae0875694a22ec9c465888aac2`, 17 files, passed quality and focused security review. The first security member failed required absolute-manifest verification; its provenance was rejected and the sequential fallback verified all 17 exact hashes before PASS. Leader fresh upstream tests, affected sequential race, 10 repeated continuation/send/editor regressions and four static builds pass. Ten production files reach 81.8-100% changed instrumented-line and 87-100% overlapping-block statement coverage. An additional unchanged MCP mid-flight test passed 100 baseline repetitions and is not labeled proven pre-existing. Only recorded new R2b race paths are eligible for cleanup after accepted integration. Documentation-only exactness corrections preserve executable hashes.

## Update 2026-10-07: R3 host controls and registries accepted

### Summary of changes

The composed CLI/TUI host now backs all 14 assigned SDK actions. R3 supplies transactional model/thinking controls, in-memory completion providers, declared extension flags and custom-event subscriptions. Agent content execution and final installation remain separate work.

### Technical reasoning

One declaration-only Setup pass precedes final parsing; run-only actions require validated/trusted readiness. Failed Setup rolls back all catalogs, active tools and UI plus flags/providers/subscriptions. Models are built outside locks and persisted/adopted in one generation transaction; TUI selection uses the same path. OpenRouter reasoning uses endpoint-scoped cloned request decoration and verified capability metadata without changing protected drivers. Unsupported transports/intents are explicit errors. Event Close is logical and nonblocking; bounded Wait occurs only at actual teardown, with no goroutine-ID mechanism.

### Impact assessment

ThinkingDefault accurately represents provider control. Explicit request reasoning is process-local; resumed historic entries do not silently restore unsupported settings. Provider API keys remain private and URL validation errors redact userinfo/query credentials. Old SDK signatures and bare/gateway availability contracts remain unchanged. The 102-row matrix reports 49 core plus 16 print-mode capabilities implemented and 37 future capabilities deferred.

### Validation steps

Artifact `R3-6fcb8f8f49d3`, SHA-256 `6fcb8f8f49d351745709b140131f22c444b174fd05653c4a35550f10c2fbfe4a`, 45 files excluding traces, passed exact quality and focused security PASS with all file hashes verified. Leader reran formatting, vet/build, fresh six-package upstream tests, complete affected sequential race, 10 repeated model/mandatory/resume/event-close/flood/Setup-rollback regressions and four static builds. Twenty-six production files reach 83.3-100% changed instrumented-line and 88.9-100% overlapping-block statement coverage (`/tmp/smidja-r3-final.cover`). The latest full-suite failure is only the alpha-reproduced MCP restart flake. No original test was weakened, no ordinary code comments, no protected driver/schema or dependency change; no go.sum.

## Update 2026-10-07: R4 agent runtime accepted

### Summary of changes

Resolved agent Markdown content is executable through `/agent` and the model-callable subagent tool. Child sessions, history, preparers, reasoning and recursion accounting are isolated; tools can only decrease relative to the immediate parent's live active view.

### Technical reasoning

Reuse existing content/trust, model wire/factory, loop, context manager and session primitives. Create and strictly open locked child files directly in the controlled subagent-sessions subtree, never transiently in the parent browser directory. Child loops do not reuse the parent extension dispatcher/API; internal progress remains available. Owned command turns, generation cancellation and delivery jobs prevent stale or canceled success. A security-blocking reactivation fallback was fixed by always substituting or denying the builtin delegation tool rather than resetting ancestry/depth through the root tool.

### Impact assessment

Agent packages are runtime-consumed; no new provider dialect, schema, required SDK signature or global turn/token cap. Native wire IDs and nested reasoning reach actual requests. Results are bounded and full artifacts private. Compiled extensions retain parent hooks and trusted overrides; independent child extension hosts are not claimed. Final installation and audit remain R5 work, and external/live acceptance remains explicit.

### Validation steps

Artifact R4-73c629342d64, SHA-256 `73c629342d6410a2ff184cb403e66487caf6bd8bf8fdf522514e5dfcf0867436`, 27 files excluding traces, passed repeated exact quality and focused security PASS. All hashes/membership verified. Leader reran formatting, vet/build, fresh affected/upstream tests, complete affected sequential race, ten repeated reactivation/nested/cancel/transport/generation cases, real PTY abort/cleanup and four static builds. Old tests were not weakened/deleted/skipped, stdlib-only dependencies unchanged, no go.sum. The latest full-suite failure is only the alpha-reproduced MCP restart flake.

| R4 file | Changed instrumented lines | Overlapping-block statements |
| --- | --- | --- |
| internal/agents/catalog.go | 96.8% | 97.9% |
| internal/agents/child_session.go | 83.8% | 88.1% |
| internal/agents/definition.go | 87.2% | 89.8% |
| internal/agents/executor.go | 89.2% | 91.4% |
| internal/agents/tool.go | 93.5% | 95.1% |
| internal/cli/agent_command.go | 86.5% | 89.4% |
| internal/cli/agent_executor.go | 92.6% | 93.5% |
| internal/cli/bootstrap.go | 100.0% | 100.0% |
| internal/cli/chat.go | 92.0% | 87.5% |
| internal/cli/host_model.go | 100.0% | 100.0% |
| internal/cli/host_runtime.go | 87.3% | 92.0% |

## Update 2026-10-07: R5 technical closure and installed runtime

### Summary of changes

All assigned P0-P7 and R1-R5 technical work is published and installed on feat/tui; PR 1 remains open against alpha, unmerged. R5 corrects legacy package-inspect agent labels, stale availability docs and the initial provider-default thinking display. The original broad harness ecosystem still needs genuine external-creator acceptance, and live-provider acceptance still needs configured credentials.

### Technical reasoning

Source acceptance and installed identity are separate evidence. The actual binary uses release-shape flags, static CGO zero, trimpath and a clean VCS revision. Two separately compiled public SDK consumer bundles prove public composition without falsely attributing extensions to the core installed binary. Test credentials are synthetic and traffic stays on isolated local fixtures, not another harness's credentials or a real provider.

### Impact assessment

Current source checkpoint is 3811f638e61326bba30d7e91c3d75b3120fa7010, after the final truthful-display fix. This trace-only closure is followed by a metadata-only reinstall to its final HEAD and PR/report readback; runtime source is unchanged. All 14 assigned hosted SDK actions are real, prompt/agent content is consumed, and the parity matrix explicitly keeps 37 future capabilities deferred (49 core +16 print-mode implemented out of 102). Bare/gateway and unsupported platform/transport contracts remain honest. No schema, provider-driver, root-package-system or dependency rewrite belongs to these slices. `smidja update` replaces the local prerelease with the public release; wait for user testing.

### Validation steps

- Leader independently read generated installer and acceptance scripts completely, then ran the full installed battery with automatic retries disabled.
- Corrected installed source: version v0.3.0-tui.1, origin github.com/digitalygo/smidja, commit 3811f638e61326bba30d7e91c3d75b3120fa7010, SHA-256 655674c5c5fa2365414f083f9662ccbb9255b35f73087c1530dace58c4ab6a9b. Go metadata proves static CGO zero, trimpath and vcs.modified=false.
- Report /tmp/smidja-runtime-independent-corrected-installed-report.json, SHA-256 29d14f61f8667bb7a9bc6f88d8fb14aec97ba69358010801506c831a85700e6e: 197 checks, nine scenarios, zero failures/retries, all first pass. Seven scenarios exercise the installed binary, two exercise distinct public SDK consumers. Counts are regular 17, fullscreen core 9, model selector 14, sessions 28, fullscreen UI 26, UI SDK consumer 22, run/prompt 17, agents 26 and runtime SDK consumer 38.
- Real flows prove multiline streaming/read tools, model-wire changes, replay/fork/new-session chains, colors/dialogs/search/Markdown/image fallback, pure-text template expansion and no TUI in one-shot mode, direct and nested agent child files, live capability restriction, SDK flags/events/snapshots/exec/session writes/delivery/reasoning max/off/default and private custom-provider routing.
- Last executable artifact R5-thinking-f892aad7b7bb, manifest SHA-256 f892aad7b7bbe0ee83c64269ed2247357628dd13273e61911a9e81a88a96706c, four source/test files: quality PASS, fresh CLI/UI/TUI normal and sequential race, real initial-label PTY PASS. Changed display blocks are 100% covered. R5 package label blocks are also 100% covered, with exact JSON and active/inactive filename tests; artifact R5-label-5e0b39a757c4 has SHA-256 5e0b39a757c45906dd69eac75b078b0e6550772b223237ae183abb43cafa1334 and quality PASS. Security N/A for these UI/copy-only slices.
- Final aggregate affected-package race, formatting, vet/build and all four static release builds/checksums pass. Every executable phase has at least 80% per-file delta coverage; docs and declarations N/A. The latest uncached complete suite fails only baseline-proven internal/mcp.TestListToolsRetryOnceAfterRestart and internal/session.TestListNewestFirst. These are not hidden, skipped or fixed in unrelated work.
- No owned test process, fixture server, descriptor, sandbox or consumer tree remains. Main/origin/PR/install equality is verified at the source checkpoint and refreshed after doc-only publication. Protected session/provider/gateway/package/updater paths and go.mod are unchanged; no go.sum. The `.github/.ai-telemetry-ignore` difference from alpha predates this completion session.

### Rendered frame excerpt

This sanitized text comes from the actual corrected installed agent scenario, not a mocked component:

```text
[subagent outer tier=workspace origin=<workspace>/.smidja/agents depth=1
model=fixture/model-one]
fixture outer answer 81
[subagent session: <session-dir>/subagent-sessions/<parent-id>/<cwd-shard>/<child>.jsonl]
fixture root followup answer 83
fixture/model-one • default
ctrl+o tools · shift+tab thinking · ctrl+l model
```

### Explicit acceptance limits

Fixture coverage is not a live-provider pass or an external clean-room creator. The live installed nested chain reaches depth two; the absolute depth-four and cycle refusal boundaries are covered by R4 tests, not claimed as separate five-level installed scenarios. All required hosted methods have composed tests; this public consumer battery does not exhaust every API/mode combination. Linux is executed; Darwin/arm64 targets are cross-compiled, not run on this workstation. Historical P5 alternative worktrees remain preserved and are not active unfinished product phases.
