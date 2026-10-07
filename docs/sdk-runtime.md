# Smidja SDK runtime

The extension SDK runtime composes a per-session host view behind the frozen `sdk` contracts. On a composed CLI or TUI host, fourteen `sdk.API` methods run with real behavior: `SetActiveTools`, `AppendEntry`, `SetSessionName`, `LabelEntry`, `Exec`, `SendMessage`, `SendUserMessage`, `SetModel`, `SetThinkingLevel`, `RegisterProvider`, `RemoveProvider`, `RegisterFlag`, `Flags`, and `EmitCustomEvent`. A host binding adds the behavior; a bare API keeps the defaults: the error-returning methods above return `extensions.ErrUnavailable` (`extensions: API method not available in this release: <name>`), `Flags` returns an empty map placeholder, and the fallback handler context returns empty values. Custom event subscriptions are a separate optional interface, not part of the frozen `sdk.API`.

R1 to R4 are implemented and published on `feat/tui` through `fc5b7dc`. The installed binary remains the P7 baseline and predates these slices; final installation and acceptance are pending. The [SDK parity matrix](sdk-parity-matrix.md) holds the row-level dispositions with the counts unchanged: 49 core and 16 print-mode rows implemented, 37 deferred. The agents runtime is behavior, not an `sdk.API` method, so it adds no parity row. The [extension UI SDK](sdk-ui.md) covers the UI surface.

## Composed contexts

`internal/extensions.Runtime` resolves the handler context on every dispatch and binds it to the dispatch signal. Three shapes exist:

| Shape | `Mode` | `HasUI` | UI | State |
| --- | --- | --- | --- | --- |
| Interactive TUI | `sdk.ModeInteractive` | true | runner-bound UI | live session |
| Print, line, and non-TTY sessions | `sdk.ModePrint` | false | no-op UI | live session |
| Bare fallback, for example the gateway | `sdk.ModePrint` | false | no-op UI | empty |

The live shapes expose the bound session through `Cwd`, `SessionManager`, `ModelRegistry`, `Model`, `ThinkingLevel`, `SystemPrompt`, and `ContextUsage`. `ContextUsage` reports the last assistant input tokens, the context window, and the percent of the window. `ThinkingLevel` reports the active level, `default` when a model is active and no explicit level is set, and `off` before a model is bound. The TUI decorates the host context through the runner and reports `ModeInteractive` with the bound UI; print and line sessions report `ModePrint`, so UI dialogs return `sdk.ErrModeUnsupported`.

The gateway builds the extension runtime without a host context or a host API binding. It keeps the bare fallback: nil session view, model, and registry; empty cwd and system prompt; and unavailable host-backed methods. Binding the gateway host is later work.

## Dispatch signals and snapshots

Every dispatch binds its signal to the handler context and captures a fresh snapshot of the session state. The composed host context receives the signal through `WithSignal`, so each handler sees the signal for its own event. The snapshot holds the session handle with its generation, the messages, the current model, the registry view, the system prompt, the usage values, and the thinking level.

Snapshots are isolated. The messages are cloned down to content blocks and tool-call argument bytes, and the model, the available-model list, and the usage pointers are copies. Two extensions handling the same event cannot mutate each other's view through the handler context.

Writes are generation-bound. A handle captured from an earlier session fails with `extensions: the session context is no longer active` after the active session changes, and every mutation fails with `extensions: the host session is not available` after shutdown. A stale write never reaches the session file, and a stale UI delivery is dropped at the serial boundary instead of landing on the replacement session.

## Setup and readiness

Extension Setup runs exactly once per process, ahead of the final flag parse in the root and `run` paths. `bootstrapExtensions` builds the catalogs, the UI registry, the flag and provider registries, the custom event bus, the host, and the API, then `Runtime.Start` runs `Registry.Setup`. A second run with the same runtime fails with `extensions: setup already run`; other subcommands such as `version` never run Setup.

Setup is a declaration phase. Registrations take effect, and `EmitCustomEvent` works because the bus is ready, while run-only host actions fail closed because the host is not ready yet:

- `Exec` returns `extensions: the host session is not available`.
- `SetModel` and `SetThinkingLevel` return the same closed-host error because no session handle is bound.

The host becomes ready only after `runChat` binds the session, which happens after TUI startup and the workspace trust decision. From then on handler-context `Exec`, `SetModel`, and `SetThinkingLevel` run against the live session. Help, root `-version`, and malformed-flag exits still tear the bootstrap down through the deferred close: the host shuts down, the event bus closes, and no credential store is written. A malformed user settings file is captured during bootstrap and only fails the chat path, so help and version keep working.

A failed Setup is rolled back per extension. Before each extension's `Setup`, the API snapshots its tool catalog, command catalog, UI registry, flag declarations, provider registry, and custom event subscriptions. On failure the snapshot is restored, so a partially declared extension leaves nothing behind while successful earlier extensions keep their contributions. The failed extension is disabled and logged by id and error; provider keys never reach the log.

## Session actions

`AppendEntry`, `SetSessionName`, and `LabelEntry` write real session entries through the active recorder. `AppendEntry` and `SetSessionName` also deliver a UI update on the lifecycle dispatcher; labels are read back from the session by the tree projection. On the handler context the methods act on the captured handle; on the API they act on the current handle.

### AppendEntry

`AppendEntry(customType, data)` requires a non-empty custom type and JSON-marshalable data. It appends a custom session entry with the marshaled bytes. The TUI delivers the entry to the entry renderer path, and replay reconstructs it from the session.

Failures:

- empty custom type: `extensions: AppendEntry requires a non-empty custom type`
- marshal failure: wrapped as `extensions: AppendEntry "<type>": <marshal error>`
- stale handle: `extensions: the session context is no longer active`
- closed host: `extensions: the host session is not available`; a write error from the session itself is returned unchanged

### SetSessionName

`SetSessionName(name)` trims the name and requires a non-empty result. It appends a session-info entry, updates the live handle so later dispatches read the new name through `SessionView.Name`, and updates the TUI footer. An empty name fails with `extensions: SetSessionName requires a non-empty name`.

### LabelEntry

`LabelEntry(entryID, label)` requires a non-empty entry id. It appends a label entry that the session tree projection reads back as a node label. An empty id fails with `extensions: LabelEntry requires a non-empty entry id`.

## Active tools

`SetActiveTools(names)` is backed by the extension tool catalog and gates both advertisement and execution:

- `ActiveTools`, `Tools`, and `Names` report only enabled tools, in registration order.
- The loop resolves tool calls through `GetActive`, so a disabled tool cannot run even when the model emits a call for it.
- `AllTools` and `Get` keep every registered tool, enabled or not.
- Unknown names are ignored, `nil` resets the active set to every registered tool, and an empty non-nil list disables every tool.
- A replacement tool inherits the active state of the name it replaces.

The active view feeds the toolset fingerprint used by the session runtime profile.

## Direct exec

`Exec(command, args, opts)` runs argv directly, without a shell, in the configured workspace root and with a sanitized environment. `ctx.Exec` runs under the per-dispatch signal; `api.Exec` has no dispatch signal and is still owned by the host run context, so shutdown cancels it too. Exec is gated on host readiness, so calls from Setup fail closed and calls from later dispatches run.

Timeouts:

- a positive `opts.Timeout` overrides the configured `ExecTimeoutSecs`
- otherwise the configured value applies, with a 30 second fallback
- the effective timeout is capped at 120 seconds

Process and output rules:

- the process starts in its own process group; timeout, cancellation, and host shutdown kill the whole group
- a canceled context never starts the process
- a run killed by timeout or cancellation reports `Killed` true
- a normal nonzero exit is a result, not an error: `Code` carries the exit status and `Killed` stays false
- a start failure returns an error and no result
- `Stdout` and `Stderr` stay separate
- each stream is bounded to 2000 lines and the configured byte cap, 50 KiB by default; a truncated stream names a full-output artifact under the OS temp directory (`smidja-exec-*.log`)
- the environment drops `OPENROUTER_API_KEY`, every `SMIDJA_*` variable, and `PI_CODING_AGENT_DIR`
- a descendant that keeps the pipes open cannot hold the call past a 2 second wait delay after the main process exits

There is no sandbox. The command runs with the user's privileges, the workspace root is only the starting directory, and the process can read, write, or connect anywhere the user can. The environment filter keeps provider credentials out of child processes; it is not isolation.

## Message delivery

`SendMessage` and `SendUserMessage` are backed when a host is bound. A handler context delivers against the session handle and dispatch signal it captured; the API delivers against the current handle and the owned run context. A send from a replaced handle fails with the stale-session error, a send after shutdown fails with the closed-host error, and a canceled signal or run fails with `extensions: message delivery canceled: <cause>`. No send runs a model turn inline: it persists the delivery or enqueues a turn, and the host dispatches that turn through its callback scheduler, the TUI lifecycle worker or a tracked goroutine in print and line mode.

`SendMessage` requires a non-empty custom type, and `Details` must be JSON-marshalable. `SendUserMessage` requires non-empty text. Both honor `ExpandPromptTemplates`, which defaults to false: with the zero value the content is delivered as written, and with true it is expanded through the same expander as the `/prompt` path. A host without an expander fails with `extensions: prompt template expansion is unavailable`.

Validation failures:

- empty custom type: `extensions: SendMessage requires a non-empty custom message type`
- empty user text: `extensions: SendUserMessage requires non-empty text`
- unknown delivery mode: `extensions: unknown delivery mode "<mode>"`
- user send during an active turn without a mode: `extensions: SendUserMessage during an active turn requires an explicit steer, followUp, or nextTurn delivery mode`
- non-JSON details: `extensions: SendMessage details must be valid JSON: <cause>`

### Delivery modes

`SendOptions.TriggerTurn` is shared by both methods. It is inert for `SendUserMessage`: an idle user send always starts a turn, and an active user send is governed by `DeliverAs` alone. It is also ignored by `DeliverAs: nextTurn`, which always buffers.

| Host state | Options | Result |
| --- | --- | --- |
| idle | `SendUserMessage`, any options including `nextTurn` | External user turn starts |
| idle | `SendMessage` with `DeliverAs: nextTurn` | Buffered in memory, `TriggerTurn` ignored |
| idle | `SendMessage`, other modes, `TriggerTurn` false | Persisted, no turn |
| idle | `SendMessage`, other modes, `TriggerTurn` true | Continuation turn starts with the message |
| active | `SendUserMessage` without `DeliverAs` | Error, explicit mode required |
| active | `DeliverAs: nextTurn` | Buffered in memory until the next external turn, `TriggerTurn` ignored |
| active | `DeliverAs: followUp` | Queued; delivered at the stop boundary |
| active | `DeliverAs: steer` (the custom default) or empty custom, `TriggerTurn` true, or any user steer | Delivered before the next model request, after tool results |
| active | `DeliverAs: steer` or empty custom, `TriggerTurn` false | Deferred to turn end, no continuation |

Delivery order is FIFO within each queue. Steering is polled before every model request, and tool results are already recorded by then, so a steer sent from a tool-result handler arrives after its tool result. At the stop boundary the host checks steer first, then follow-up, then deferred deliveries. Follow-ups are polled at the stop boundary one at a time, and each accepted follow-up queues the next continuation. After a stop-boundary delivery that continues the turn, including a follow-up, the host skips one steer-poll slot at the next request boundary. A steer arriving during that window waits one boundary; it is not lost or duplicated.

The stop boundary and the turn end are separate. Deferred deliveries are persisted after the turn finishes and never observe the model request that was already in flight. A continuation carries a generation-scoped token; only the newest owned continuation runs, and a message that arrives while the loop is in its final stop poll claims a fresh continuation, so exactly one continuation follows it and stale scheduled jobs are skipped.

### Persistence and projection

An injected delivery writes exactly one existing session entry: a custom message entry for `SendMessage` and a user message entry for `SendUserMessage`. Next-turn deliveries stay in memory until the next external turn injects them, and injection persists them the same way. The host then reloads the session and rebuilds the model history from the file, so a resumed session shows each message once, entry ids stay aligned, and tool call and result pairs remain valid.

`Display` controls display only. A visible custom message reaches the TUI renderer path; `Display: false` messages are still part of the model history and are skipped by TUI replay. Model history renders custom messages as `[custom <type> <id>] <content>`.

### Delivery lifecycle

- A canceled turn drops pending steer, follow-up, and deferred deliveries and invalidates queued continuations. Buffered `nextTurn` deliveries stay.
- A session change resets the mailbox with the new generation, so buffered next-turn content is dropped with everything else.
- Shutdown clears every queue, including `nextTurn`.
- Scheduled turns are asynchronous. A provider failure in a TUI continuation appears as a warning notice, and a canceled continuation settles as interrupted. In print and line mode the scheduler writes `smidja: scheduled message: <err>` to stderr. `SendMessage` and `SendUserMessage` return after persisting or enqueueing, not after the turn result.

## Model controls

`SetModel` is backed through the same validated transaction used by the TUI `/model` selector. Validation runs before anything commits:

- a non-empty model id is required
- the model must resolve in the configured registry or in a registered custom provider, otherwise the error names the unknown model
- a verified wire model must exist for the active transport, otherwise `extensions: model has no verified wire model for the active transport`
- when a thinking level is explicitly set, the target model must allow it; a level outside the allowlist fails with `extensions: the active thinking level is not supported by the requested model`
- when the explicit level is `off` and the target model requires reasoning, the transaction resets thinking to the provider default and persists a `default` entry instead of sending a rejected disable
- host bindings must be configured, otherwise `extensions: model changes are not available in this host`

The preparer and the client for the new model are built before the session commit. The commit then writes the runtime profile entry, plus the optional thinking-default reset entry, in one session transaction. Only after the write succeeds does the host adopt the intent: `pendingModel` is set, and the next turn boundary applies the new model, wire model, provider, reasoning seam, context window, preparer, and client. `ContextUsage` recomputes its percent against the new window, and later dispatches read the new model and provider through the handler context. In print and line mode the boundary is `loopDeps`; the TUI applies the pending intent right after the selector confirms it and then updates the footer.

Every failure path rolls back: an empty, unknown, unverified, or incompatible model, a host without bindings, a preparer or client build failure, a stale session handle, and a persistence failure all leave the model, thinking level, preparer, client, window, and runtime profile untouched, and no pending intent is queued. A session switch or shutdown discards a pending intent instead of applying it to the replacement session. When the active transport uses a fixed deployment, the TUI selector refuses selection before any transaction starts.

## Thinking level

`SetThinkingLevel` accepts `off`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max`, and `default`. `default` returns control to the provider: the getter reports `default`, no effort directive is sent, and an explicit `default` persists a `default` thinking entry while clearing the explicit-set flag. Unknown levels fail with `extensions: unknown thinking level`.

Capability checks use the model metadata gathered from the catalog and the OpenRouter models endpoint:

- `reasoning: true` and `reasoning: false` set known support or known unsupported state.
- An object may carry `mandatory` plus an effort list under `supported_efforts`, `efforts`, or `values`; effort names are trimmed and lowercased.
- A null effort list means effort selection without an allowlist, so every gateway effort is accepted.
- A list means effort selection with an allowlist; a level outside it fails with `extensions: the active model does not allow this reasoning effort`.
- `supported_parameters` containing `reasoning` means supported without effort selection.
- Omitted, null, or malformed metadata stays unknown.

Unknown metadata never invents support, models without effort selection reject efforts, and mandatory models reject `off` with `extensions: the active model requires reasoning`. Rejected levels are neither clamped to a nearby effort nor persisted. A registry merge preserves known reasoning metadata over an unknown later entry.

### OpenRouter wire

The OpenRouter client wraps the HTTP client with a per-request reasoning decorator. Only POST requests to the configured endpoint are patched; the decorator clones the request, leaves the caller request untouched, and rewrites or inserts the `reasoning` field:

| Active level | `reasoning` field |
| --- | --- |
| `minimal`, `low`, `medium`, `high`, `xhigh`, `max` | `{"effort":"<level>"}` |
| `off` | `{"enabled":false,"effort":"none"}` |
| `default` or unset | omitted |

The directive travels in the request context, so it is per request and never leaks between turns. Non-POST requests, off-endpoint requests, and non-JSON bodies pass through unchanged. Transports without this seam return the typed `sdk.ErrUnsupported` (`sdk: the active transport or model does not support this operation`) instead of pretending to apply a level.

### Resume behavior

Thinking levels are ephemeral across process restarts. A resumed session keeps its persisted thinking entry in history, but the new process starts at the provider default: the persisted entry does not restore a wire directive, and a new context reports `default` until `SetThinkingLevel` is called again. Do not read this as cross-process setting continuity. The runtime profile, by contrast, does continue: it carries the provider, model, system prompt, tool schema, content, and affinity state, it is rewritten only when that profile actually changes, and it never encodes thinking state. Tests assert both sides: the old thinking entry stays untouched and the first request of each resumed run omits reasoning.

## Providers

`RegisterProvider` and `RemoveProvider` are backed by a per-run, in-memory provider registry. No provider configuration is persisted, and no credential metadata is written to sessions, logs, flags, or the system prompt. Only the `openai-completions` dialect is supported; an empty `API` field defaults to it and any other value fails with `extensions: unsupported provider completion dialect`.

Registration validation:

- the name must match `[A-Za-z0-9][A-Za-z0-9._-]*` and must not collide with a built-in transport name, otherwise `extensions: provider name collides with a built-in transport`
- the base URL must be a plain http(s) URL with a host and no user info; trailing slashes are trimmed, and a query string is allowed and preserved
- every model id must be non-empty; duplicate ids collapse, and the provider name is stamped on each model
- registering an existing name replaces it in place and keeps its position in the provider order

Runtime effects:

- registered models appear in `ModelRegistry().Available()` and resolve through `Find(provider, id)`, and the TUI model selector lists them
- selecting one routes the next model turn through the existing OpenAI completions client at `base URL + /chat/completions`, with the API key sent as `Authorization: Bearer <key>`
- a custom provider client has no reasoning seam, so thinking levels report unsupported on that route
- removing the provider that backs the active or pending model fails with `extensions: the provider is active; switch models before removing it`; after switching away, removal succeeds and the model no longer resolves
- removing an unknown provider fails with `extensions: provider is not registered`

Errors are redacted. A validation error never echoes the raw URL or the API key: user info is dropped and query strings and fragments are replaced with `redacted` while scheme, host, and path stay readable. Tests assert that keys do not reach errors, stderr, the session file, the system prompt, flag values, or the credential store.

## Flags

`RegisterFlag` and `Flags` are backed by a per-run flag registry. Setup declares flags once, before the final parse, and the parser then sees them on the root command and on `run`, including the positional prompt form.

Declarations:

- only `boolean` and `string` types are accepted; anything else fails with `extensions: unsupported flag type`
- a nil default becomes `false` for booleans and `""` for strings; a default of the wrong type fails with `extensions: flag default does not match its type`
- names must match `[A-Za-z0-9][A-Za-z0-9._-]*`
- names that collide with a core flag fail with `extensions: flag name collides with a core flag`; the reserved set is `p`, `model`, `system`, `provider`, `continue`, `tui-mode`, `use-theme`, `version`, `allow-workspace-mcp`, `h`, and `help`
- re-registering a name fails with `extensions: flag is already registered`

Parsing follows the standard library behavior. Extension flags parse at the root and after `run`, help and version still exit cleanly, unknown extension flags produce the standard `flag provided but not defined` error, and typed values use the standard conversion errors. `Values` returns a copy of the captured values, and a flag that was declared but never applied is skipped. Extensions with a failed Setup leave no ghost flag declarations behind, so their names are unknown on the command line while earlier extensions' flags keep working.

## Custom events

The custom event bus is the smidja equivalent of Pi's inter-extension event bus. Because the frozen `sdk.API` keeps its signature, subscription is offered through the optional `sdk.CustomEventSubscription` interface:

```go
type CustomEventSubscription interface {
	SubscribeCustomEvent(name string, handler CustomEventHandler) (unsubscribe func(), err error)
}
```

`sdk.CustomEvent` carries the name and the `Data` value. Data is the Go value the emitter passed, not a JSON copy, so handlers see the source object semantics. Custom event payloads are never persisted to the session and never enter model requests.

Dispatch rules:

- subscribers run in registration order; an event emitted from inside a handler is appended to the drain queue and delivered after the current event's remaining handlers, in emit order
- nesting is bounded at 1024 queued emits; beyond that `Emit` returns `extensions: too many nested custom events` and the bus stays open
- while a drain is active, concurrent emits are either accepted into the queue or rejected with the same overflow error, and every accepted event is delivered exactly once
- callbacks run outside the bus lock, so a handler may unsubscribe itself, close the bus, or emit another event
- a handler error or panic does not stop later handlers; `Emit` joins the errors, naming panics as `extensions: custom event "<name>" handler panic: <value>`, and the bus remains usable

Lifecycle:

- `SubscribeCustomEvent` rejects empty names and nil handlers, and `Emit` rejects empty names; after `Close` both fail with `extensions: the custom event bus is closed`
- `Close` is a logical admission barrier: it is nonblocking, refuses new subscriptions and emits, drops queued events, and does not interrupt a handler that is already running
- `Close` itself does not join the active drain; `Wait(timeout)` joins it, and a zero timeout is a probe. Shutdown closes the bus and the bootstrap teardown then waits up to five seconds for the drain; an uncooperative handler that outlasts the bound leaves teardown proceeding without a false claim that it joined, and the bus stays closed
- a rollback that restores a bootstrap snapshot never reopens a closed bus

The event bus is not the 27 typed Pi events. Those rows stay deferred to their own runtime waves, and the matrix does not count them as backed by this bus.

## Abort and shutdown

`Abort()` cancels the turn that owns the dispatch signal. A canceled turn drops pending steer, follow-up, and deferred deliveries and invalidates queued continuations; buffered next-turn deliveries stay until the session changes or the host shuts down. A context captured during an earlier turn carries that turn's cancel function and cannot abort a newer one. A context created outside a turn carries no cancel, so `Abort` does nothing. A direct `/agent` run owns the same turn slot, so `Abort`, a session rebind, and shutdown cancel its child stream the same way, and a canceled run persists no result.

`Shutdown()` owns the run and is idempotent:

- it marks the host closed, cancels the owned run context, joins running exec processes through that context, clears every mailbox queue, and closes the custom event bus
- it discards a pending model intent instead of applying it to a replacement session
- pending and running compaction jobs settle with a truthful error instead of committing late work
- in the TUI it requests the runner exit; teardown joins the callbacks before the host stops

## Compaction

`Compact(opts)` produces a real compaction entry through the context manager and reports the outcome exactly once. The persisted entry is the context manager's own entry, and `OnComplete` receives its summary, first kept entry id, and tokens before.

Behavior:

- during an active turn the request becomes the single pending compaction; a second request while it is pending fails with `extensions: a compaction request is already pending`
- the pending request runs at the next prepare boundary or when the turn ends
- outside a turn the request runs as an owned job against a snapshot of the handle, system prompt, messages, and entry ids
- `CustomInstructions` is not supported by the current context manager: the request fails with `extensions: compact custom instructions are not supported by this context manager` and persists nothing
- errors report through `OnError`: `context.Canceled` for a canceled signal, `extensions: nothing to compact in the active context` for an empty history, `contextmanager: context management is disabled` when context management is off, `extensions: compaction canceled because the session changed` for a pending request canceled before it runs, and the stale and closed errors above for a replaced session or a stopped host
- a canceled or stale job never commits a late entry, even when the context manager finishes after the cancellation

Callback panics are recovered and counted on the host. The host does not raise a warning notice for them; a wired UI surface reports the panics it observes through its own handling.

## Remaining surface

All fourteen declared API methods above are runtime-backed on a composed CLI or TUI host. The rest of the frozen contract keeps its existing behavior:

- the bare API and the gateway build no host binding, so all fourteen keep the unavailable behavior and `Flags` keeps returning an empty map
- `registerShortcut` has no SDK method and no current plan; extension keybinding registration stays outside the contract
- the 27 deferred Pi events stay with their runtime waves and are not dispatched
- agent content execution is part of the published runtime: `/agent` and the model-callable `subagent` tool resolve definitions and run them in isolated child sessions, without adding an `sdk.API` method or a parity row. The existing `internal/subagent` package remains the compaction selector, not a coding-agent executor. See [agents](agents.md).
- real-provider acceptance still needs user-configured credentials, and no live provider or external acceptance result is claimed here
