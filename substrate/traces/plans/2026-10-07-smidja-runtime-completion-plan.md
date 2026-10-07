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

- **Status:** In progress. R1 code and documentation are independently accepted; publication is next, followed by R2 host backing.
- **Baseline identity:** `2026-10-07-smidja-runtime-completion-plan`, version 1, authored by the orchestrator after read-only repository analysis.
- **Execution baseline:** Approved branch `feat/tui` at `80f29353f48116a8c4191709064f19d4402ef718`, matching origin and open PR 1 against `alpha`. Main worktree was clean before these trace updates.
- **Active phase:** R1 publication, then R2 composed host contexts, session actions and delivery.
- **Last verified checkpoint:** 2026-10-07T03:42:04+02:00, R1 independent deterministic and review gates accepted.
- **Last successful checks:** R1 formatting, vet, build, uncached upstream tests, affected race tests, real PTY collision/no-TUI checks, four static builds and actual temporary-binary wire/persistence checks pass. Five executable delta files reach 90-100% changed-line and 96.7-100% overlapping-block statement coverage. Quality and focused security verdicts are PASS.
- **Open blockers:** No R1 implementation blocker. Live configured-provider acceptance has not been performed: Smidja settings/auth files and the default OpenRouter environment key were absent when inspected. External creator acceptance requires a genuinely external actor.
- **Required approvals and gates:** Preserve the approved branch and PR, no merge/tag/release/package publication, no other repository modifications. Every behavioral slice needs independent delta tests, upstream coverage at least 80%, delegated quality judgment, focused security review when applicable, commit/push and exact remote/PR readback. Installation is only the authorized local test binary; tests use temporary homes and isolated fixtures.
- **Next action:** Publish `feat(cli): execute prompt templates and run subcommand`, verify exact remote/PR equality, then implement bounded R2 host contexts and action bindings while preserving unbound contracts.

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

No checkpoints yet.

### R3 execution checkpoints

No checkpoints yet.

### R4 execution checkpoints

No checkpoints yet.

### R5 execution checkpoints

No checkpoints yet.

## Plan-variation ledger

No variations yet.

## Closure evidence

### Final outcome

Not complete.

### Quality and security evidence

Not started for runtime-completion slices. P7 accepted baseline evidence is in the separate TUI plan.

### Operation record

The existing TUI operation record owns completed P5-P7 slices. Runtime completion will append its verified outcomes or use a separate record only if combining history would confuse scope.
