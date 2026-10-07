# Smidja SDK runtime

The extension SDK runtime composes a per-session host view behind the frozen `sdk` contracts. A host binding adds behavior; callers without one keep the defaults: `SetActiveTools`, `AppendEntry`, `SetSessionName`, `LabelEntry`, `Exec`, `SendMessage`, and `SendUserMessage` return `extensions.ErrUnavailable` (`extensions: API method not available in this release: <name>`), and the fallback handler context returns empty values.

R2a is published at `bb86387`. The R2b delivery source is uncommitted in the retained candidate worktree; MAIN still carries R2a only, and the installed binary remains P7 without either slice. The [SDK parity matrix](sdk-parity-matrix.md) holds the row-level dispositions, and the [extension UI SDK](sdk-ui.md) covers the UI surface.

## Composed contexts

`internal/extensions.Runtime` resolves the handler context on every dispatch and binds it to the dispatch signal. Three shapes exist:

| Shape | `Mode` | `HasUI` | UI | State |
| --- | --- | --- | --- | --- |
| Interactive TUI | `sdk.ModeInteractive` | true | runner-bound UI | live session |
| Print, line, and non-TTY sessions | `sdk.ModePrint` | false | no-op UI | live session |
| Bare fallback, for example the gateway | `sdk.ModePrint` | false | no-op UI | empty |

The live shapes expose the bound session through `Cwd`, `SessionManager`, `ModelRegistry`, `Model`, `ThinkingLevel`, `SystemPrompt`, and `ContextUsage`. `ContextUsage` reports the last assistant input tokens, the context window, and the percent of the window. The TUI decorates the host context through the runner and reports `ModeInteractive` with the bound UI; print and line sessions report `ModePrint`, so UI dialogs return `sdk.ErrModeUnsupported`.

The gateway builds the extension runtime without a host context or a host API binding. It keeps the bare fallback: nil session view, model, and registry; empty cwd and system prompt; and unavailable host-backed methods. Binding the gateway host is later work.

## Dispatch signals and snapshots

Every dispatch binds its signal to the handler context and captures a fresh snapshot of the session state. The composed host context receives the signal through `WithSignal`, so each handler sees the signal for its own event. The snapshot holds the session handle with its generation, the messages, the current model, the registry view, the system prompt, and the usage values.

Snapshots are isolated. The messages are cloned down to content blocks and tool-call argument bytes, and the model, the available-model list, and the usage pointers are copies. Two extensions handling the same event cannot mutate each other's view through the handler context.

Writes are generation-bound. A handle captured from an earlier session fails with `extensions: the session context is no longer active` after the active session changes, and every mutation fails with `extensions: the host session is not available` after shutdown. A stale write never reaches the session file, and a stale UI delivery is dropped at the serial boundary instead of landing on the replacement session.

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

`Exec(command, args, opts)` runs argv directly, without a shell, in the configured workspace root and with a sanitized environment. `ctx.Exec` runs under the per-dispatch signal; `api.Exec` has no dispatch signal and is still owned by the host run context, so shutdown cancels it too.

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

## Abort and shutdown

`Abort()` cancels the turn that owns the dispatch signal. A canceled turn drops pending steer, follow-up, and deferred deliveries and invalidates queued continuations; buffered next-turn deliveries stay until the session changes or the host shuts down. A context captured during an earlier turn carries that turn's cancel function and cannot abort a newer one. A context created outside a turn carries no cancel, so `Abort` does nothing.

`Shutdown()` owns the run and is idempotent:

- it marks the host closed, cancels the owned run context, joins running exec processes through that context, and clears every mailbox queue
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

## Deferred surface

R2a backs five methods and R2b adds the two messaging methods, for seven runtime-backed methods on a composed host. The rest of the frozen API keeps its unavailable behavior: `SetModel`, `SetThinkingLevel`, `RegisterProvider`, `RemoveProvider`, `RegisterFlag`, `Flags`, and `EmitCustomEvent` stay for the R3 wave, and the gateway builds no host binding, so its extension surfaces keep the bare fallback. The command-extra and event tables are unchanged from P7.
