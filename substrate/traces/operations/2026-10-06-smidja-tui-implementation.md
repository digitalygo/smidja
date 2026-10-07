---
status: completed
created_at: 2026-10-06
files_edited: [sdk/, internal/extensions/, internal/extensionui/, internal/tui/, internal/tui/interactive/, internal/ui/, internal/cli/, internal/config/, internal/content/, README.md, docs/sdk-ui.md, docs/tui.md, docs/themes.md, docs/keybindings.md, docs/settings.md, docs/auth.md, docs/sdk-parity-matrix.md, docs/tui-handoff.md, docs/transcript-search.md]
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
