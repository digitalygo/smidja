---
document_type: mycelium-plan
plan_id: 2026-10-07-smidja-runtime-completion-plan
status: in-progress
created_at: 2026-10-07
planner: orchestrator
baseline_version: 1
execution_owner: orchestrator
execution_started_at: 2026-10-07T02:51:14+02:00
last_updated_at: 2026-10-07
---

# Smidja runtime completion plan

## Current execution snapshot

- **Status:** In progress. R1-R5 source is published and installed acceptance passes; the final truthful initial-thinking display correction is accepted and awaits publication/reinstall.
- **Baseline identity:** `2026-10-07-smidja-runtime-completion-plan`, version 1, authored by the orchestrator after read-only repository analysis.
- **Execution baseline:** Approved branch `feat/tui` at `80f29353f48116a8c4191709064f19d4402ef718`, matching origin and open PR 1 against `alpha`. Main worktree was clean before these trace updates.
- **Active phase:** R5: final audit, source publication, exact installed identity and actual-process acceptance.
- **Last verified checkpoint:** 2026-10-07T21:31:42+02:00, first installed 197-check acceptance and final thinking-display correction independently verified.
- **Last successful checks:** R4 formatting, diff checks, vet/build, fresh affected/upstream tests, full affected race, ten reactivation/nested/cancellation/transport/generation regressions, real PTY abort/cleanup and four static builds pass. Eleven executable delta files reach 83.8-100% changed instrumented-line and 87.5-100% overlapping-block statement coverage. Repeated quality and focused security verdicts are PASS.
- **Open blockers:** No R4 code-review blocker remains. Final audit/install/acceptance is pending; live-provider credentials and genuine external creator acceptance remain external limitations.
- **Required approvals and gates:** Preserve the approved branch and PR, no merge/tag/release/package publication, no other repository modifications. Every behavioral slice needs independent delta tests, upstream coverage at least 80%, delegated quality judgment, focused security review when applicable, commit/push and exact remote/PR readback. Installation is only the authorized local test binary; tests use temporary homes and isolated fixtures.
- **Next action:** Publish the accepted initial-thinking display correction and closure docs, install the new exact pushed head, and repeat all 197 installed-process checks before the trace-only final closure.

## Planner baseline

### Problem statement

- **Planner prediction:** Complete actual suspended features: `smidja run`, resolved prompt and agent content consumption, 14 declared but unbacked SDK actions, and real handler-context host state. Do not mistake 27 future Pi event waves, non-TUI command extras or platform-specific intentional errors for this suspended work.
- **Planner prediction:** Keep the TUI baseline immutable. This supplementary plan owns a distinct runtime-completion scope rather than a second execution ledger for P0-P7.
- **Planner prediction:** Preserve existing SDK method signatures and old bare-API/default-context contracts. A standalone unbound API remains explicitly unavailable; a composed CLI/TUI host gains actual implementations, never placeholder success.
- **Planner prediction:** Keep Go stdlib-only and static Linux/Darwin amd64/arm64 builds. Do not rewrite gateway, package installation, provider drivers, updater or session codec/store semantics. New adapters may use existing session entry types and provider factories.

### Research and evidence

- **Planner prediction:** Repository `AGENTS.md` requires mapped delta tests and 80-100% coverage on added or modified executable lines, no test weakening/deletion/skipping, contextual file-length judgment and no ordinary code comments. DRC, EXP and CONTRIBUTING domains are absent.
- **Planner prediction:** `internal/cli/root.go` registers `run` but returns `not implemented yet`. The existing `-p` path in `chat.go` already supplies one-shot execution, resume continuity and exit behavior.
- **Planner prediction:** `internal/content/content.go` already resolves skills, prompts and agents with deterministic bundle > trusted workspace > user > package > core precedence, symlink rejection, UTF-8 validation and a 100 KiB artifact budget. Only skills have a runtime consumer. P6 supplies project trust without a new persistent trust store.
- **Planner prediction:** `internal/subagent/` is a compaction selector, not a coding-agent executor. Do not falsely claim the latter exists or replace the selector.
- **Planner prediction:** `internal/extensions/api.go` has unbacked active-tool selection, message delivery, session metadata, model/thinking changes, provider/flag registration, exec and custom events. `defaultContext` remains empty and no production caller supplies `Runtime.SetContext`.
- **Planner prediction:** SDK declaration shapes are in `sdk/context.go` and `sdk/types.go`. Existing unbound-API tests deliberately expect `ErrUnavailable` and non-nil empty flags. Preserve those tests unchanged while supplying optional host bindings.
- **Planner prediction:** `sessionController`, model/preparer/persistence helpers, workspace tools, the FIFO TUI lifecycle, extension Setup registries and P7 renderer delivery seams are reusable. Same-worker synchronous enqueue-and-wait from hooks would deadlock and is forbidden.
- **Planner prediction:** Current OpenRouter reasoning wire research uses official reasoning and models documentation. `reasoning.effort` accepts `minimal`, `low`, `medium`, `high`, `xhigh`, `max`; disabling must respect mandatory-model capability metadata. Research recommendations about clamping are inference, not an accepted silent behavior change. See the [official reasoning guide](https://openrouter.ai/docs/guides/best-practices/reasoning-tokens) and [model properties reference](https://openrouter.ai/docs/api/api-reference/models/list-all-models-and-their-properties).
- **Planner prediction:** MCP restart and session timestamp-order flakes were independently reproduced on untouched `origin/alpha`; do not fix them in unrelated slices or conceal them as passes. Runtime tests must still pass for all mapped behaviors.

### Hypotheses, decisions, and rationale

- **Planner prediction:** Reuse one-shot execution for `run`, accepting either `-p` or one positional prompt, never both. Reuse all existing root options, stdout streaming and exit codes. Missing/empty prompt is an explicit error; help and malformed flags are validated before a turn starts.
- **Planner prediction:** Consume resolved prompts through `/prompt [name] [arguments]` and non-conflicting named shorthand. Existing host and extension commands win collisions; explicit `/prompt name` remains unambiguous. Positional arguments and `$ARGUMENTS`/`$@` are text substitutions only, never shell/env/file evaluation. Quoting and escaping are parsed without executing anything. Expansion has an explicit local output budget with an error, not silent truncation; this is not a global model turn/token cap.
- **Planner prediction:** Bind SDK host capabilities through optional, late-bound callbacks/providers. Construct real session/model views and invocation-specific actions without changing empty fallback contexts or old SDK interface requirements. Getters return defensive snapshots; credentials and provider API keys never enter metadata, logs, sessions or model prompts.
- **Planner prediction:** Active-tool selection must affect tools advertised to and callable by the loop, not just a displayed list. Exec uses direct argv, existing workspace/timeout/process-group/output/environment hygiene, and actual separated stdout/stderr/exit/killed results.
- **Planner prediction:** Message delivery uses a per-session mailbox with explicit steer, follow-up and next-turn semantics. Hook calls enqueue without blocking the FIFO worker. Session entries and visible custom renderers reflect real delivery; canceled/session-replaced work never leaks into another session. Prompt expansion is honored only when requested for SDK delivery.
- **Planner prediction:** Model changes reuse wire compatibility, preparer construction and runtime-profile persistence with rollback. Thinking changes must affect a proven request-side reasoning seam or return a precise unsupported error. Do not turn a footer-only change into an apparent model setting. Known unsupported effort values or mandatory reasoning-off requests fail explicitly, not silently clamp to another intent.
- **Planner prediction:** Custom providers are per-host, in-memory and limited to the already-supported OpenAI/OpenRouter completions dialect via existing client factories. No code plugin loading, package rewrite or credential persistence. Flag registration happens during Setup exactly once and parsed values remain compatible with standard root flag behavior. A real custom-event bus uses an additive subscription interface and deterministic, cancellable, panic-isolated dispatch rather than an emit no-op.
- **Planner prediction:** Coding agents consume resolved trusted Markdown definitions, with a small documented frontmatter subset for model/tool selection. Default inheritance is from the current host; tool restrictions can only reduce parent-active capabilities. Model overrides must be compatible with the existing transport. Each child has an isolated recorder/session, context, context management and cancellation; results are recorded and subject to existing tool-output truncation. Bound recursive nesting as a local runtime safety control, never restore global turn/token caps removed by the user.
- **Planner prediction:** Technical design choices above resolve unspecified invocation details using existing patterns and minimal behavior. Genuine external acceptance and missing live credentials are reported, not manufactured or silently waived.

### Planned phases

#### R1: one-shot command and prompt runtime

- **Objective:** Replace the reserved `run` error and consume already-resolved prompt content in print, line and TUI paths.
- **Planner predictions:** `internal/cli/` option/invocation/catalog helpers, a focused prompt expansion package or helper, tests and accurate content documentation. No model/provider/session-format change.
- **Proposed steps:**
  1. Reuse root option parsing and one-shot execution for `run`, with positional versus `-p` validation.
  2. Add deterministic prompt listing, lookup, text-only argument expansion and collision handling using the resolved snapshot.
  3. Connect invocation and autocomplete/inventory through existing CLI/TUI seams; preserve print and non-TTY behavior when no prompt is invoked.
  4. Add malformed flags/quotes/name/control/budget/tier/trust/collision and real wire-prompt tests.
  5. Run independent gates, write evidence and publish one conventional commit.
- **Predicted verification:** Uncached prompt/CLI/core tests, upstream coverage for modified production paths, relevant race tests, vet/build/formatting, four static builds, actual binary one-shot fixtures, zero dependencies and protected-path checks, quality plus input/expansion security review.
- **Completion criterion:** The actual composed runtime executes `run` and trusted prompt templates with correct wire text and existing session/exit behavior; all required gates and exact publication checks pass.

#### R2: host contexts, session actions and delivery

- **Objective:** Replace empty composed contexts and supply real session/tool/exec/abort/shutdown/compaction/delivery backing without deadlocks or stale session state.
- **Planner predictions:** Focused `internal/extensions/` host interfaces/adapters and `internal/cli/` state/mailbox/context wiring, bounded execution helpers, optional agent-loop delivery seam, tests. Preserve existing bare API tests and codec/store behavior.
- **Proposed steps:**
  1. Add optional host binding seams preserving unbound availability errors.
  2. Supply actual cwd, session view, model registry/model, system prompt, usage and per-invocation signal snapshots.
  3. Back append/name/label, active tools and direct exec, with safe data and process lifetimes.
  4. Connect abort/shutdown and real compaction callbacks without reentering a held context-manager or worker lock.
  5. Add per-session mailbox delivery and real custom-message renderer/persistence paths, proving hook-time delivery ordering and cancellation/session-replacement isolation.
  6. Independently gate and publish.
- **Predicted verification:** Old unbound contracts unchanged; hosted context and API integration assertions, negative/late/canceled/session-swap/tool-disable/process-child/output-cap cases, full relevant race and PTY tests, upstream delta coverage, static/dependency checks and focused quality/security reviews.
- **Completion criterion:** Every R2 action changes actual model/runtime/session behavior or reports a precise runtime error; no empty success, same-worker deadlock, credential leak or stale cross-session delivery remains.

#### R3: registration, model and thinking controls

- **Objective:** Back remaining provider/flag/event/model/thinking capabilities with true runtime effects and honest transport limitations.
- **Planner predictions:** Host-local registries and parsers, model adapters and a narrow `internal/openrouter/` request-side reasoning seam when capability metadata permits it. Existing provider drivers and manifests remain unchanged.
- **Proposed steps:**
  1. Reuse model compatibility/preparer/persistence transactions behind the public setter.
  2. Add explicit capability-aware reasoning selection and verify exact request JSON, with unsupported/malformed/mandatory-off rejection and no implicit higher-effort clamp.
  3. Make provider registration/removal affect the host registry through existing factories without persistence or secret-bearing snapshots.
  4. Register extension flags during exactly one Setup pass and preserve standard help/unknown/type/default/core-flag behavior.
  5. Add a per-host custom-event subscriber bus with deterministic order, self-unsubscribe, cancellation and panic isolation.
  6. Run all gates and publish.
- **Predicted verification:** Hosted plus unbound tests, request-body capture rather than cosmetic labels, registration/flag parsing integration, event ordering/reentrancy/cleanup, concurrency and race, coverage/static/dependency checks, quality and core API/security review.
- **Completion criterion:** All remaining declared methods have real composed-host backing; unsupported transports/modes are explicitly and accurately documented instead of stubbed or falsely implemented.

#### R4: agent content execution

- **Objective:** Consume agent definitions through explicit user invocation and model-callable delegation using isolated child state.
- **Planner predictions:** A new executor beside, not replacing, the existing compaction selector; `internal/cli/` command/tool integration; definition parser and tests; optional session metadata using existing custom entries.
- **Proposed steps:**
  1. Resolve/list agent definitions with existing content tiers and trust decisions, parsing only the documented safe metadata subset.
  2. Add `/agent name task` and a model-callable `subagent` tool, with exact lookup/errors and parent-active tool restriction.
  3. Build isolated child sessions/recorders/context management, compatible model inheritance/override and cooperative cancellation.
  4. Record bounded results and child identity, expose meaningful TUI progress without sharing parent mutable history or leaking credentials.
  5. Test unknown agents, bad metadata/tools/models, trust refusal, nested depth, cancellation, session isolation and actual parent/child wire flow; gate and publish.
- **Predicted verification:** Deterministic local provider fixtures for parent and child calls, repeated cancellation/cleanup/race tests, child JSONL validation and parent containment, coverage/static/dependency checks, quality and focused model/tool/filesystem security review.
- **Completion criterion:** Activated package/user/bundle/trusted-workspace agents are executable, not merely listed as deferred; child state and tool capabilities remain isolated and all gates pass.

#### R5: final audit, documentation and installed acceptance

- **Objective:** Reconcile all active backlog claims with code and prove the final installed head and publication state.
- **Planner predictions:** Documentation and both living-plan ledgers, an updated compact operation record, final local binary replacement, temporary acceptance harnesses and PR body evidence.
- **Proposed steps:**
  1. Audit active stubs and consumed content; distinguish intentional future scope and historical rejected worktrees from unfinished assigned features.
  2. Update README, content creator/runtime docs, SDK availability/parity and accurate capability/limitation descriptions.
  3. Independently rerun all changed/upstream tests, relevant race/coverage, formatting/vet/build/static/dependency/protected checks.
  4. Commit/push final verified slices, install the exact final head and rerun actual-binary and separate public SDK consumer acceptance with isolated fixtures.
  5. Update PR 1 with exact outcomes, per-slice coverage, frame evidence, installed identity and unresolved external/live requirements; never merge/tag/release.
- **Predicted verification:** Main/origin/PR/installed commit equality, clean worktree, no live test processes/resources, stable SDK and schema, complete accurate operation records and no unsupported completion claims.
- **Completion criterion:** Assigned technical work is independently complete and published, with real-provider and external-creator criteria either evidenced or explicitly blocked without being claimed passed.

## Execution ledger

### Ledger rules

- **Planner prediction:** The orchestrator alone updates this snapshot and appends material checkpoints. Baseline research, choices, phases and predicted verification remain immutable.
- **Planner prediction:** Every behavioral slice stays open until independent deterministic checks, quality judgment and applicable security review pass; only then commit and publish. Preserve exact failed findings and correction evidence.
- **Planner prediction:** Trace-only updates are outside executable review artifacts but retain hashes, commands, outcomes and limitations. Do not turn this ledger into a tool-call log.

### R1 execution checkpoints

#### Checkpoint 2026-10-07T02:51:14+02:00: supplemental baseline validated

- **Event:** Runtime-completion scope starts after the technical TUI source and installed acceptance slices are independently verified.
- **Planner prediction:** Reuse existing machinery and preserve SDK/default-context contracts before supplying actual host backings.
- **Subagent claims:** Read-only analysis identified actual consumer and host-binding gaps and reusable session/model/loop/tool seams. Official OpenRouter research distinguished gateway effort values, metadata capabilities and unsupported-level ambiguities from inferred client clamping policy.
- **Orchestrator finding:** Directly inspected SDK types, API stubs, default contexts, CLI commands, public composition and content resolution. Chosen invocation details are internal design decisions consistent with the broad completion request, not permission to rewrite other repositories or protected formats. Agent execution is genuinely absent; the existing subagent package is only a selector.
- **Independently verified facts:** Main, origin and PR 1 match `80f29353f48116a8c4191709064f19d4402ef718`, worktree was clean, installed static binary matches the same commit. Independent six-scenario acceptance report `/tmp/smidja-p7-independent-installed-report.json` passes 116 checks and leaves no test processes or temporary sandboxes. No Smidja settings/auth file or default OpenRouter environment key was present when inspected; no live provider acceptance is claimed.
- **Decision and impact:** Initialize the quality cursor at P7 HEAD, keep the TUI baseline separate, and execute R1-R5 as coherent independently gated slices. Do not borrow another harness's credentials, fabricate external acceptance or replace known baseline flakes with weakened tests.
- **Next action:** Delegate R1 code/tests for the run alias and prompt catalog/expansion.

#### Checkpoint 2026-10-07T03:42:04+02:00: run and prompt runtime independently accepted

- **Event:** R1 code, correction, documentation, independent tests and both applicable reviews completed.
- **Planner prediction:** Reuse single-turn execution and resolved prompt tiers with unambiguous invocation, pure text expansion and strict local budgets.
- **Subagent claims:** Shared root options implement positional or `-p` run forms, prompt catalogs consume snapshots and expand text, and 42 top-level tests plus subtests cover invocation, parsing, tiers/trust, collision and real wire/session behavior. A bounded correction registers canonical `/prompt` before extension Setup and makes literal/joined expansion budgets incremental and lazy. Documentation matches source semantics.
- **Orchestrator finding:** Direct source/diff inspection found and corrected the initial canonical `/prompt` collision, preserving the colliding extension at `/prompt2` across line/TUI/print. A future `/agent` shorthand is reserved without claiming an agent runtime. No shell, environment, file evaluation or recursive substitution occurs. The local 1 MiB expansion limit is not a global prompt/turn/token cap.
- **Independently verified facts:** Artifact `R1-f0c7ee0098fe`, manifest SHA-256 `f0c7ee0098fea45dfcb3692150b57ad812e58f0cc4d29ba568a2df1c465c008c`, covers 14 files excluding traces. Formatting, diff checks, vet, build, fresh upstream CLI/content tests, full affected race, explicit real PTY run-without-TUI and canonical collision checks, and all four static release builds passed. `/tmp/smidja-r1-final.cover` OR-merges duplicate blocks; five production files measure 90-100% instrumented changed lines and 96.7-100% overlapping-block statements. The leader built and executed an actual temporary binary against the local SSE fixture: `run /prompt greet Alice 42` sent and persisted `Hello Alice, Alice 42!`, returned tool plus streamed output and never entered the TUI. Full suite reports only the alpha-reproduced MCP/session flakes. Frozen SDK, protected subsystems and `go.mod` are unchanged; no `go.sum`, test deletion/skip/weakening or added code comments. Both delegated quality and focused security verdicts are exact PASS.
- **Decision and impact:** Accept R1 and publish one conventional commit. The only advisory is missing dedicated `run --` delta coverage for the unchanged shared terminator helper; other run paths and its prior tests pass. Installed binary remains P7 until the final committed runtime head is installed.
- **Next action:** Commit and publish R1, then initialize composed-host state and real SDK action backing for R2.

### R2 execution checkpoints

#### Checkpoint 2026-10-07T04:01:18+02:00: R1 publication verified and R2 split into coherent slices

- **Event:** R1 is published at `ed31a06d526706f5ef0c79e6b59cb2d26a237705`, `feat(cli): execute prompt templates and run subcommand`.
- **Planner prediction:** R2 supplies optional host bindings, real context views/actions and then a non-deadlocking per-session delivery mailbox.
- **Subagent claims:** None for R2 implementation yet.
- **Orchestrator finding:** Implement known data/action adapters first as R2a; isolate mailbox ordering and loop integration as R2b after that gate. This partitions existing planned steps, not scope or acceptance requirements. Real API calls must be invocation/session scoped; getters are defensive snapshots and sensitive provider configuration never enters them. External callbacks must run outside host locks, and compaction must not synchronously reenter the loop/preparer while it is active.
- **Independently verified facts:** Main, origin and OPEN PR 1 match `ed31a06`; main worktree is clean. R1 actual-binary wire/persistence and prior quality/security evidence remain accepted. The installed binary is still P7, explicitly not claimed to include R1.
- **Decision and impact:** Use one writer for R2a's constrained adapters and retain all unbound `ErrUnavailable`/empty-context tests. The higher-variance R2b scheduler is a separate implementation and review slice, with race adjudication when competing implementations are warranted.
- **Next action:** Delegate R2a source/tests only; verify all host read/write/process/lifecycle semantics before implementing queued model delivery.


#### Checkpoint 2026-10-07T05:50:27+02:00: R2a independent verification passes but quality blocks publication

- **Event:** R2a code and ownership corrections pass deterministic checks; quality returns FAIL and focused security PASS. No R2a code is committed.
- **Planner prediction:** Hosted contexts/actions must be session-scoped, defensive, cancellable and lifecycle-owned while preserving unbound contracts.
- **Subagent claims:** Optional host bindings, active-tool execution gates, direct exec and real compaction were added; corrections introduced request snapshots, generation transactions, owned cancellation and panic containment. The reviewer found five remaining quality gaps.
- **Orchestrator finding:** Preserve exact blockers: generation check and UI delivery are still check-then-act; message contents/arguments/usage and model/usage pointers are shared across co-handlers; cancellation needs a final disk-commit check; fallback callback goroutines need lifecycle ownership. The initial reported Shutdown/Once self-deadlock was refuted and is not claimed as evidence.
- **Independently verified facts:** Artifact `R2a-e7187de84de9`, manifest SHA-256 `e7187de84de9fef2e05234b3616e0dba5a79307d4e72edf184fe6b7a589e018d`, covers 26 files excluding traces. Fresh upstream tests, affected race suites, 10 repeated host stress sets, real PTY host abort/shutdown and canceled idle compaction, four static builds, formatting/vet/build passed. Fifteen production files measure 81.2-100% changed instrumented lines and 86.5-100% overlapping-block statements. Frozen SDK, protected subsystems and `go.mod` are unchanged; no `go.sum` or weakened tests. Full suite includes known alpha MCP/session flakes and a newly observed unchanged MCP crash-test failure, which is not falsely classified as independently proven pre-existing.
- **Decision and impact:** Keep the quality cursor at published R1 `ed31a06`; correct only these remaining defects and rerun all required gates before publication. Core registered methods outside R2a remain explicitly unavailable rather than no-op backed.
- **Next action:** Delegate bounded quality corrections and stronger same-event snapshot, cancellation-boundary, generation-delivery and callback-teardown tests.

#### Checkpoint 2026-10-07T06:53:59+02:00: corrected R2a accepted

- **Event:** Optional host contexts, session/tool/process/lifecycle and compaction backing pass corrected independent tests and repeated quality/security reviews, both exact PASS.
- **Planner prediction:** Keep bare API/default contexts unchanged while composed hosts gain real, scoped effects and defensive views.
- **Subagent claims:** Session/generation transactions protect disk writes; serial generation-tagged jobs protect visible delivery; getters deeply clone nested SDK messages, arguments, usage and model values. Compaction captures request scope, owns cancellation and checks it at the disk boundary. Safe callback ownership joins fallback work during teardown without self-waiting Shutdown. Direct exec is argv-based, canceled-before-start safe and bounded by process-group cancellation plus pipe drain delay.
- **Orchestrator finding:** Direct source/diff/test inspection and quality re-review confirm all five previous blockers closed. The earlier broader eight ownership/process defects and their correction evidence remain relevant context, not unverified completion claims. Compact custom instructions are accurately unsupported through OnError. Callback panics are contained and counted, not falsely advertised as visible warnings. Gateway host binding remains outside this slice.
- **Independently verified facts:** Accepted artifact `R2a-6785c984336c`, manifest SHA-256 `6785c984336cf44fbbdaf3290e00c27f47e50157f410b1b977df75d8cfd2aa4f`, covers 30 files excluding traces. Formatting, diff checks, vet, build, fresh upstream tests across five affected packages, complete affected sequential race tests, 10 repeated snapshot/delivery/cancellation/callback regressions, real PTY abort/shutdown/canceled-idle-compaction tests and four static Linux/Darwin amd64/arm64 builds passed. Profile `/tmp/smidja-r2a-gated.cover` measures 15 production files at 84.8-100% changed instrumented-line and 88.7-100% overlapping-block statement coverage. No old test was deleted, skipped or weakened; frozen SDK, protected subsystems and `go.mod` are unchanged, no `go.sum`. Exact repeated quality and focused security verdicts are PASS.
- **Decision and impact:** Accept R2a source and accurate availability documentation. Publish one coherent commit before R2b scheduler work. Remaining nine SDK action methods are still explicitly deferred; no delivery, provider, flag, event or agent execution is claimed yet. The installed binary remains P7 until final runtime installation.
- **Next action:** Commit and push `feat(sdk): bind host contexts and runtime actions`, then execute R2b with isolated scheduler candidates if its variance warrants a race.

#### Checkpoint 2026-10-07T07:10:03+02:00: R2a published and R2b semantics selected

- **Event:** R2a is published as `bb86387d7e73dd7f929dfbfe673bfee5af63e714`, `feat(sdk): bind host contexts and runtime actions`.
- **Planner prediction:** A mailbox supplies steer/follow-up/next-turn ordering without synchronous same-worker waits or stale session delivery.
- **Subagent claims:** Read-only pattern analysis verified upstream defaults, tool-boundary steering, follow-up-at-stop, idle custom append and memory-only next-turn buffering. Current Smidja projection already includes custom messages in model history regardless of Display and skips invisible custom messages in TUI replay.
- **Orchestrator finding:** Preserve frozen shared bool semantics truthfully: custom `SendMessage` with TriggerTurn false appends without a model turn when idle and defers non-follow-up active custom messages to turn end; TriggerTurn true starts an idle continuation or enables active steering. FollowUp remains ordered after current completion; NextTurn buffers until the next external user turn and ignores TriggerTurn. `SendUserMessage` behaves as a user input and always triggers an idle turn; active calls require explicit steer/followUp (with explicit NextTurn buffering supported by the existing SDK), and ExpandPromptTemplates defaults false. Display affects only UI, not model context. Persist each injected message exactly once through existing entry types before model visibility; next-turn content intentionally remains memory-only until injection.
- **Independently verified facts:** Main, origin and OPEN PR 1 match `bb86387`; worktree was clean. Upstream source and existing Smidja loop/projection/worker seams were directly located; no session-format or required SDK-interface change is needed.
- **Decision and impact:** Scheduler ordering has high variance, so race two backend members in isolated worktrees at the exact published baseline. Use byte-identical tasks and disposable fixture ports/homes. Keep main untouched except this ledger; verification precedes leader adjudication. Existing editor follow-up queues may join the mailbox at actual turn boundaries but session-switch drop behavior and its stronger tests remain intact.
- **Next action:** Create recorded R2b race paths, delegate candidates A and B, independently verify both and retain only the best accepted implementation.

#### Checkpoint 2026-10-07T10:04:27+02:00: scheduler race verified and candidate B retained for final gates

- **Event:** Identical R2b tasks ran in recorded isolated worktrees `../smidja-race-20261007-r2b-a` and `../smidja-race-20261007-r2b-b`, both based on `bb86387`.
- **Planner prediction:** Independently verify survivors, then adjudicate correctness, conventions and simplicity; never merge a failed candidate.
- **Subagent claims:** Both members reported passing affected tests/race/build/static checks and coverage. Read-only comparison identified four contract defects in A and one late-continuation token defect in B.
- **Orchestrator finding:** Direct source inspection confirms A's explicit custom steer with false TriggerTurn does not defer, stale handler contexts send through the current global API, whole queues drain as a batch, and active Display delivery lacks the direct live sink. B preserves context/signal scope, one-at-a-time boundaries and Display/model projection, so correctness ranks B first despite A's higher test coverage. B's turn-end token-zero scheduling defect was corrected before acceptance, using the common token allocator and strong late-stop/error/cancel regression tests. No losing source is grafted.
- **Independently verified facts:** Both candidates passed leader formatting, whitespace, vet, build, uncached affected agent/extensions/TUI/CLI tests and complete affected sequential race runs. Corrected B additionally passes fresh upstream-instrumented tests, complete affected race, 10 repeated late/continuation/composed-send/editor cases and all four static release builds. Protected paths, SDK signatures, schema and dependencies remain unchanged; no go.sum. A 100-repeat probe of the newly observed MCP mid-flight crash test on archived `bb86387` passed, so that specific signal is not claimed proven pre-existing.
- **Decision and impact:** Retain corrected B for complete independent coverage and quality/security gates. A is rejected on conformance, not arbitrarily discarded on diff size. Neither candidate is merged or published yet. Race cleanup will remove only the two paths recorded at creation after accepted winner integration.
- **Next action:** Verify strict B delta coverage, write source-accurate docs, gate the entire corrected candidate and consolidate only after both reviews pass.

#### Checkpoint 2026-10-07T10:04:27+02:00: retained scheduler B independently accepted

- **Event:** Corrected candidate B passes deterministic eligibility, quality PASS and focused security PASS with verified snapshot provenance.
- **Planner prediction:** Accept only independently verified code that implements the fixed mailbox/persistence/ownership semantics.
- **Subagent claims:** B's late stop-poll continuation now uses the shared token allocator; canceled turns invalidate only stale continuations. Documentation describes actual shared bool, expansion, next-turn and Display semantics and keeps R3 methods deferred.
- **Orchestrator finding:** Correctness selects B over A's four conformance gaps. Direct source and comparison verified scope-bound sends, stable one-item loop boundaries, resume projection and live display. No losing source is grafted. The first security member returned a source verdict but failed absolute-manifest verification by checking a relative path; that deliverable was rejected and the sequential fallback independently verified the exact snapshot before PASS. A functional entry-ID note was reassessed against all caller attachment paths and was not a defect.
- **Independently verified facts:** Accepted artifact `R2b-65d07e6ef500`, manifest SHA-256 `65d07e6ef50055b993b06dc760f0947465f9c4ae0875694a22ec9c465888aac2`, covers 17 files. Leader reran formatting, diff checks, vet, build, fresh upstream agent/extensions/CLI/TUI tests, complete affected sequential race, 10 repeated continuation/composed-send/editor regressions and four static release builds. All ten production files reach 81.8-100% changed instrumented-line and 87-100% overlapping-block statement coverage in `/tmp/smidja-r2b-b-independent.cover`. Both reviews inspect actual source/tests/docs, old tests are not weakened, frozen interfaces and protected subsystems remain unchanged, no dependencies/go.sum. Security fallback verifies all 17 file hashes and exact delta membership.
- **Decision and impact:** Accept B for integration and publication; preserve the failed security provenance attempt and losing-candidate reasoning. Only the two newly recorded race paths may be removed after accepted winner integration. Source/core docs receive only small exactness clarifications for custom-message versus custom-entry rendering and stop-boundary steer-poll skipping; executable hashes stay frozen.
- **Next action:** Consolidate B through Git into `feat/tui`, commit the current ledger, push/read back PR 1, remove only recorded R2b race resources and continue R3.

### R3 execution checkpoints

#### Checkpoint 2026-10-07T10:57:49+02:00: R2b publication and race cleanup verified

- **Event:** Accepted B was fast-forward integrated and published as `52dd4184a8e623b8d1d8ac91c9c4aa9bbc789a81`, `feat(sdk): deliver queued extension messages`.
- **Planner prediction:** Publish only the accepted winner after independent gates and remove only newly recorded race resources.
- **Subagent claims:** None for R3 implementation yet.
- **Orchestrator finding:** Source and docs corrections preserve the accepted executable hashes. The losing candidate was rejected on correctness; no unverified source is grafted. R3 must keep optional interfaces and plain unbound API tests, use existing model transactions and factories, and avoid hidden cosmetic reasoning changes or credential-bearing metadata.
- **Independently verified facts:** Main, origin and OPEN PR 1 match `52dd418`; main worktree is clean. Only `smidja-race-20261007-r2b-a` and `smidja-race-20261007-r2b-b`, recorded at this session's creation, were removed with their temporary branches after integration/publication. Historical P5 worktrees remain preserved. Installed binary is P7 until final runtime installation.
- **Decision and impact:** The R3 provider scope stays in-memory and uses existing completion client factories without driver/schema rewrites. Request reasoning is limited to a proven narrow OpenRouter adapter seam with capability metadata and exact JSON assertions; unsupported transports or effort intents fail explicitly. Extension flags are discovered in one Setup pass before the final standard parse; custom event subscription is additive and isolated from future typed Pi event waves.
- **Next action:** Delegate R3 source/tests with bounded host/model/registry ownership and complete real-runtime fixtures, then independent gates and documentation.


#### Checkpoint 2026-10-07T13:46:10+02:00: corrected R3 controls and registration accepted

- **Event:** All seven remaining hosted API controls/registrations pass independent eligibility and exact quality/security PASS.
- **Planner prediction:** Actual model/request effects, one declaration-only Setup pass, private in-memory providers, safe flags/events and honest unsupported transport/effort behavior, without protected driver rewrites.
- **Subagent claims:** Optional host callbacks, model intents and atomic persistence/adoption, provider registry, flag discovery and event subscriptions are implemented. Corrections closed eight initial bootstrap/rollback/model/window/thinking/event/secret defects and removed an unsupported goroutine-ID close mechanism. Ordinary Go doc comments were removed to restore repository compliance.
- **Orchestrator finding:** Direct source inspection confirms transactional model/thinking state, actual OpenRouter endpoint-scoped cloned request decoration and exact JSON parameters, coherent TUI/SDK selector behavior and preparer/window/client updates. Failed Setup rolls back all catalogs/UI/active state plus new registries. Early run actions fail closed before validated/trusted readiness, and every helper/error path closes the bootstrap. Event Close is a nonblocking logical admission barrier; bounded Wait occurs at actual teardown, not inside a handler. Explicit request thinking is process-local: resume reports ThinkingDefault and keeps historic entries as notices rather than silently restoring an unsupported value. No cosmetic reasoning effect or unsupported success is claimed.
- **Independently verified facts:** Artifact `R3-6fcb8f8f49d3`, manifest SHA-256 `6fcb8f8f49d351745709b140131f22c444b174fd05653c4a35550f10c2fbfe4a`, covers 45 files excluding traces; all hashes verify. Leader formatting, diff checks, vet, build, fresh upstream tests across six affected packages, complete sequential race, 10 repeated transaction/mandatory/resume/event-close/flood/Setup-rollback tests and four static Linux/Darwin amd64/arm64 builds pass. Twenty-six production files reach 83.3-100% instrumented changed-line and 88.9-100% overlapping-block statement coverage in `/tmp/smidja-r3-final.cover`. New SDK files and ThinkingDefault are additive declarations; old method signatures and protected driver/schema/gateway/package/build/dependency paths are unchanged, no go.sum or code comments. Full suite's only latest failure is the alpha-reproduced MCP restart flake. Quality PASS and security PASS apply to the exact artifact.
- **Decision and impact:** Accept R3 source and accurate 102-row parity documentation (49 core +16 print-mode implemented,37 deferred). All 14 assigned API actions are now hosted-backed; bare APIs/gateway retain honest availability errors. Preserve deferred typed Pi waves and shortcut scope. The uncooperative event-handler join is bounded at five seconds, not a false guarantee of killing arbitrary Go code.
- **Next action:** Publish `feat(sdk): complete model and extension registries`, verify remote/PR, then execute R4 agent content runtime and final installation.

### R4 execution checkpoints

#### Checkpoint 2026-10-07T14:23:50+02:00: R3 publication verified and agent execution boundary fixed

- **Event:** Accepted R3 is published as `24df52e6883b5a07e2be8f98cea134eedf180510`, `feat(sdk): complete model and extension registries`.
- **Planner prediction:** Agent Markdown content becomes executable through isolated child state, not the existing selector package masquerading as a runtime.
- **Subagent claims:** None for R4 implementation yet.
- **Orchestrator finding:** Reuse existing resolver tiers and trust, parent current model/client and active catalog, loop/context manager and recorder. Agent metadata is a documented bounded plain frontmatter subset; no shell/env/code evaluation or arbitrary package load. Child tools can only reduce parent-active capability, model overrides must match the existing selected transport, and recursion depth is a local safety control rather than a global round/token cap. Child sessions are separate and not normal parent session-browser entries; parent records only bounded result/child identity. `/agent` must be canonical before Setup collision handling, like `/prompt`.
- **Independently verified facts:** Main, origin and OPEN PR 1 match `24df52e`; main worktree is clean. Existing content snapshots already contain AgentRef data with UTF-8/name/symlink/size validation; `internal/subagent` remains a compaction selector. All 14 assigned hosted SDK actions are now backed, while gateway/bare contexts retain honest unavailable behavior. Installed binary remains P7 until final runtime installation.
- **Decision and impact:** One bounded executor integration writer reuses known primitives without a new provider or session schema. Explicit `/agent name task` and model-callable `subagent` use the same executor; every coding child has its own history/recorder/context management and cancellation. Unknown/malformed/incompatible/over-depth requests fail precisely and no credential is embedded in definition metadata or result/session/logs.
- **Next action:** Delegate R4 source/tests, then accurate agent docs and independent quality/security/runtime verification.


#### Checkpoint 2026-10-07T17:04:00+02:00: R4 resumption and semantic blockers verified

- **Event:** User reconfirmed continuation. Preserve the complete uncommitted R4 worker delta and leader trace; R4 remains unaccepted.
- **Planner prediction:** Isolated child model/session/context state, monotonically decreasing tool capabilities and cooperative lifetime ownership.
- **Subagent claims:** R4 implements source parsing, commands, model-callable delegation, nested depth controls and isolated recorders; prior normal/race tests pass. Read-only analysis identifies five material blind spots and a frozen-client coherence risk.
- **Orchestrator finding:** Direct source confirms display IDs are used instead of native wire IDs, nested executors reuse root capabilities rather than the immediate child's restricted catalog, storage creates a parent-visible file then renames without updating session path/lock, and child dispatch reuses parent extension contexts/API. Direct commands do not acquire host turn ownership and already-canceled requests can create session artifacts. Existing compaction display matches the parent's synthetic representation, so that suspicion is refuted rather than invented as a blocker.
- **Independently verified facts:** Main/origin/OPEN PR 1 remain at 24df52e; pull is already up to date. DRC/EXP/CONTRIBUTING are absent, status baseline is ignored, preserved files match the worker scope. Prior independent normal and race tests plus real PTY cleanup pass but do not prove these uncovered cases. Chezmoi update returned zero; no repository or credential setting was changed by this sync.
- **Decision and impact:** Correct to the existing isolation contract without new child extension APIs: child loops must not invoke the root extension dispatcher with parent mutation authority. Keep internal progress callbacks, parent extension hooks on the parent loop and external parent-active tool revalidation. Add strong tests for absent parent hook leakage rather than retain a test dependent on that bug. Create/open locked sessions directly under controlled child storage, never transiently in the parent browser directory. Resolve current compatible transport per child invocation and preserve protected schemas/drivers and zero dependencies.
- **Next action:** One bounded correction writer, then whole R4 independent tests, coverage, quality and security review.

#### Checkpoint 2026-10-07T19:04:00+02:00: R4 quality passes but delegation reactivation blocks security

- **Event:** Corrected R4 passes independent deterministic verification and quality PASS; focused security reports BLOCKED with one proven depth/ancestry bypass.
- **Planner prediction:** Nested capabilities and recursion accounting remain anchored to the immediate caller even across live tool toggles.
- **Subagent claims:** The exact 27-file snapshot is verified. When builtin subagent is inactive at child construction, no nested wrapper is created; later reactivation lets Get/GetActive/Tools fall through to the root builtin that snapshots depth zero and an empty ancestry.
- **Orchestrator finding:** Accept the finding as a correction to the existing nested safety contract, not a new requirement. Preserve extension overrides, but the executor's own host-bound delegation tool must never be passed through to a child without depth/ancestry threading.
- **Independently verified facts:** Artifact R4-392dc6e17482 has manifest SHA-256 392dc6e174825470736da300bfb27a98b9d22be79bb1797ae139743886521332, 27 files excluding traces. Formatting, vet/build, fresh upstream two-package coverage, full affected race, ten repeated nested/cancel/transport/generation cases, upstream packages, real PTY abort/cleanup and four static builds pass. Eleven production files measure 83.8-100% changed instrumented-line and 87.5-100% overlapping-block statement coverage. The latest full suite failure is only the alpha-reproduced MCP restart flake. Security verifies all hashes/membership; its prose mangles the supplied digest despite independently computing the correct one, which does not invalidate the concrete source finding.
- **Decision and impact:** Quality cursor remains at published R3. Bound or refuse the builtin delegation fallback in all catalog lookup/advertising paths across inactive-to-active transitions, test cycle/depth/system-chain integrity and preserve arbitrary extension overrides. Rerun both reviews on the unchanged R4 delta plus correction.
- **Next action:** Bounded delegation reactivation correction, then repeat independent gates and both judgments.

#### Checkpoint 2026-10-07T19:46:19+02:00: R4 reactivation correction accepted

- **Event:** Complete R4 plus bounded M1 correction passes repeated quality PASS and security PASS, with exact provenance.
- **Planner prediction:** Child wire IDs, isolated state, immediate-parent capabilities, stable recursion accounting and cooperative ownership remain real behavior.
- **Subagent claims:** One substitution helper returns only a depth-scoped builtin wrapper, constructs it lazily across inactive-to-active toggles and preserves non-builtin extension overrides. Nested catalogs cannot fall back to root. Four regression tests fail when the root fallback is restored.
- **Orchestrator finding:** Directly inspected all production paths and the targeted fix. Original semantic gaps are closed: locked direct child storage, native wire/continuations, nested live tool revalidation, truthful reasoning seam, no root dispatcher hooks, generation-scoped presentation and joined watchers. Source is organized by executor versus storage responsibility; no tests were thinned. Strong external-disable-mid-child coverage replaces the new test that depended on unsafe parent hook dispatch.
- **Independently verified facts:** Accepted artifact R4-73c629342d64, SHA-256 73c629342d6410a2ff184cb403e66487caf6bd8bf8fdf522514e5dfcf0867436, 27 files excluding traces, all hashes and exact membership verified by both reviewers. Fresh leader normal/upstream, full affected sequential race, ten repeated reactivation/nested/cancel/transport cases, real PTY abort/cleanup, formatting/vet/build and four static builds pass. Profile /tmp/smidja-r4-gated-final.cover OR-merges unique column-aware blocks; 11 production files measure 83.8-100% changed instrumented-line and 87.5-100% overlapping-block statements. Latest full-suite failure remains only the alpha-reproduced MCP restart flake. No original tracked test is changed or skipped; environment-guarded new filesystem tests have explicit platform/permission reasons and were accepted. Protected schemas/drivers/SDK/dependencies unchanged, no go.sum. Documentation-only status refresh follows accepted source, without changing executable hashes.
- **Decision and impact:** Accept and publish R4. Keep installed identity at P7 until R5; do not conflate source acceptance with installation or live-provider/creator acceptance. R5 fixes stale prompt/package-inspect dispositions and rechecks all final artifacts.
- **Next action:** Commit/push feat(agents): execute isolated coding agents, verify PR/remote equality, then final audit, accurate docs and installed runtime acceptance.

### R5 execution checkpoints

#### Checkpoint 2026-10-07T20:44:04+02:00: R4 published and final source audit accepted

- **Event:** R4 is committed and published as fc5b7dc877a690ba385c6b1b53c3f77aca84aa98. R5 source-label and documentation corrections are accepted; installation is not yet performed.
- **Planner prediction:** Close actual assigned stubs and reconcile public availability before final installation/acceptance.
- **Subagent claims:** Read-only audit finds no remaining unimplemented assigned method; remaining Pi waves, shortcuts, bare/gateway errors and unsupported platform/transport paths are intentional. Documentation refresh removes stale uncommitted/future agent and unavailable-action claims. Temporary install/acceptance tools smoke-pass nine scenarios but these are not installed-binary evidence yet.
- **Orchestrator finding:** Directly inspected current source, label changes, tests, documentation and both generated tools in full. The only behavioral R5 source delta removes the false deferred agent filename/help label; it neither changes JSON nor package validation/activation semantics. Existing label assertions change to the implemented contract and gain negative checks plus active/inactive and exact JSON tests, not test weakening. The generated installer uses exact HEAD/origin/PR validation, isolated identity probes, static release flags, unique backup and atomic replacement. The acceptance wrapper preserves prior checks, separates public SDK consumer identity and reports unsupported live/depth coverage honestly.
- **Independently verified facts:** Main/origin/OPEN PR 1 match fc5b7dc. R5 label artifact R5-label-5e0b39a757c4 has SHA-256 5e0b39a757c45906dd69eac75b078b0e6550772b223237ae183abb43cafa1334, two files, and exact quality PASS. Leader normal CLI coverage and full CLI race, formatting, vet and build pass. Two changed printf blocks are covered at 100% changed-line and overlapping-block statement coverage in /tmp/smidja-r5-label-independent.cover. Non-executable docs have tests/coverage N/A; pure presentation introduces no core security review surface. The read-only audit confirms 49 core +16 print-mode implemented and 37 intentional deferred rows, 102 total; frozen TUI baseline counts are historic, not current facts.
- **Decision and impact:** Publish coherent final source/docs corrections, then install and execute independent acceptance with no automatic P7 retry on the first full run. Any failed check remains visible and must be investigated rather than accepted from smoke self-report. The only remaining expected external constraints are configured-provider credentials and a genuinely external creator.
- **Next action:** Commit/push chore(cli): refresh agent availability labels, install exact head with /tmp/smidja-runtime-install-20261007.sh, and run /tmp/smidja-runtime-acceptance-20261007.py against the installed binary and separate SDK consumers.


#### Checkpoint 2026-10-07T21:31:42+02:00: installed runtime acceptance and truthful display correction

- **Event:** R5 source-label/docs work is published and installed at 0e83d72531c235163e335a241ca3baf6832cf5f6. The complete installed battery passes; one final display-only inconsistency discovered in its frames is corrected and gated.
- **Planner prediction:** Prove actual installed behavior, not only internal test factories, with independent identity and clean resources.
- **Subagent claims:** Installer verifies static release flags, unique backup and atomic replacement; temporary acceptance tooling adds runtime flows while preserving P7 checks and separating SDK consumers. The label correction syncs initial and activated/restored session surfaces to actual host thinking state.
- **Orchestrator finding:** Both scripts were read completely and the leader ran the full harness against the actual installed target with automatic retries disabled. Initial footer off was misleading while the host returned ThinkingDefault and the request omitted reasoning; the correction reuses the existing display setter without changing request behavior, enums or bare Footer defaults. This restores agreed truthful display rather than adding scope.
- **Independently verified facts:** First installed binary SHA-256 335660ef3f1b2e2c223e1262cf7767595fb454c101494336a839727eeea729ae, exact version v0.3.0-tui.1, origin github.com/digitalygo/smidja and source commit 0e83d72, CGO zero/static/trimpath/vcs.modified=false. Report /tmp/smidja-runtime-independent-first-installed-report.json SHA-256 738d7afd3e35105c9d4d762d4ffdcf8dd5066876836d0ceb84f9ff222500c1cf passes 197 checks across nine scenarios on the first attempt, zero retries/failures; seven are installed-binary scenarios and two distinct public SDK consumers. Owned sandboxes/consumer trees/processes are gone. Full affected aggregate race passes; unchanged release script builds all four static targets with verified checksums. Latest uncached full suite fails only alpha-reproduced MCP restart and session timestamp-order flakes. Thinking artifact R5-thinking-f892aad7b7bb, SHA-256 f892aad7b7bbe0ee83c64269ed2247357628dd13273e61911a9e81a88a96706c, four files, has quality PASS, fresh CLI/UI/TUI normal/race and actual initial-label PTY PASS; changed blocks are 100% covered. Security N/A for purely UI labels with no new foundational/API/secret behavior.
- **Decision and impact:** Keep known baseline flakes explicit and do not borrow credentials. Publish the tiny truthful display correction with completed public docs, reinstall and rerun the unchanged full acceptance battery. Do not attribute earlier installed checks to the uninstalled correction.
- **Next action:** Publish fix(tui): reflect the host thinking state, then exact-head install and full no-retry acceptance.

## Plan-variation ledger

No variations yet.

## Closure evidence

### Final outcome

Not complete.

### Quality and security evidence

Not started for runtime-completion slices. P7 accepted baseline evidence is in the separate TUI plan.

### Operation record

The existing TUI operation record owns completed P5-P7 slices. Runtime completion will append its verified outcomes or use a separate record only if combining history would confuse scope.
