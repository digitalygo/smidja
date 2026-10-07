# Smidja SDK runtime

The extension SDK runtime composes a per-session host view behind the frozen `sdk` contracts. A host binding adds behavior; callers without one keep the defaults: `SetActiveTools`, `AppendEntry`, `SetSessionName`, `LabelEntry`, and `Exec` return `extensions.ErrUnavailable` (`extensions: API method not available in this release: <name>`), and the fallback handler context returns empty values.

R2a is the current runtime slice. Its source is in the worktree; publication and installation are tracked separately, and the installed binary remains P7. The [SDK parity matrix](sdk-parity-matrix.md) holds the row-level dispositions, and the [extension UI SDK](sdk-ui.md) covers the UI surface.

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

## Abort and shutdown

`Abort()` cancels the turn that owns the dispatch signal. A context captured during an earlier turn carries that turn's cancel function and cannot abort a newer one. A context created outside a turn carries no cancel, so `Abort` does nothing.

`Shutdown()` owns the run and is idempotent:

- it marks the host closed, cancels the owned run context, and joins running exec processes through that context
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

R2a backs the five methods above. The rest of the frozen API keeps its unavailable behavior: `SendMessage` and `SendUserMessage` stay for the R2b mailbox slice, and `SetModel`, `SetThinkingLevel`, `RegisterProvider`, `RemoveProvider`, `RegisterFlag`, `Flags`, and `EmitCustomEvent` stay for later waves. The command-extra and event tables are unchanged from P7.
