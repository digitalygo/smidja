# Smidja SDK parity matrix

Method-by-method parity target between Pi's extension surface and the
smidja v0 SDK, frozen on the installed Pi 0.84.2
(`@earendil-works/pi-coding-agent`). Every Pi capability maps to one
disposition: implemented now, implemented now with print-mode semantics,
or deferred to a later phase. The smidja side of the matrix is the public
`github.com/digitalygo/smidja/sdk` package plus the internal ports in
`internal/agent/ports.go`.

As of the P7 workstream (2026-10), the extension-facing UI surface is
implemented on `feat/tui`: custom components, message, entry, and
Markdown renderers, an editor component factory, autocomplete providers,
terminal input hooks, header, footer, and component widgets, editor
accessors, theme enumeration, and tools-expanded state. P7 source and
adapters passed independent code and review gates; installation and
final runtime acceptance are tracked separately in the living TUI plan. Existing
`sdk.UI` signatures are unchanged; the new surface is optional through
`sdk.ExtendedUI` and `sdk.UIRegistrationAPI`, documented in
[extension UI SDK](sdk-ui.md).

Nine Extension API methods keep frozen signatures but are not
runtime-backed yet. Their rows are counted as deferred, not as runtime:
`SendMessage`, `SendUserMessage`, `SetModel`, `SetThinkingLevel`,
`RegisterProvider`, `RemoveProvider`, `RegisterFlag`, `Flags`, and
`EmitCustomEvent`. Eight of them return an error naming the method
(`extensions: API method not available in this release: <name>`), and
`Flags` returns an empty map placeholder. Messaging lands with the R2b
mailbox slice; the model, thinking, provider, and flag methods stay in
later waves.

The R2a runtime slice backs five methods when a host is bound:
`SetActiveTools`, `AppendEntry`, `SetSessionName`, `LabelEntry`, and
`Exec`. The bindings are documented in [SDK runtime](sdk-runtime.md).
The bare API keeps returning the unavailable error for all five, and the
fallback context keeps its empty values. R2a source is in the worktree;
publication and installation are tracked separately, and the installed
binary remains P7.

## Disposition legend

- **implement now**: the capability is backed by a real v0
  implementation, whether it is part of the frozen `sdk.API` contract or
  the optional P7 interfaces.
- **implement now, print-mode**: the capability is in the contract and
  works in interactive mode; in print mode (`-p`) the blocking UI dialogs
  return `sdk.ErrModeUnsupported`, interactive-only setters return
  `sdk.ErrModeUnsupported` or are no-ops, and getters return zero values,
  mirroring Pi's "extensions run but can't prompt" mode behavior.
- **deferred**: the capability is either in the contract with its
  signature frozen but its backing landing in a later wave (the method
  returns the unavailable error for its own name), or entirely outside
  the v0 contract, for example extension keybinding registration and
  most Pi events.

## Inspected sources

All paths are under the installed Pi 0.84.2 package:

- `docs/extensions.md`: event reference, ExtensionContext and
  ExtensionCommandContext docs, ExtensionAPI method docs, mode behavior.
- `dist/core/extensions/types.d.ts`: ExtensionAPI, ExtensionContext,
  ExtensionCommandContext, ExtensionUIContext, all event and result types,
  provider config types.
- `dist/core/extensions/runner.d.ts` and `runner.js`: context creation,
  action wiring, emit ordering (extension order, then registration order),
  per-handler error isolation.
- `dist/core/extensions/index.d.ts`: package export surface.
- `dist/core/session-manager.d.ts`: compaction entry fields (`summary`,
  `firstKeptEntryId`, `tokensBefore`, `details?`, `usage?`, `fromHook?`)
  and the `ReadonlySessionManager` surface.
- `dist/core/agent-session.d.ts` and `agent-session.js`: `auto_retry_start`
  and `auto_retry_end` event shapes and the exponential retry backoff.
- `dist/core/compaction/compaction.d.ts`: `CompactionResult` shape.
- `dist/core/event-bus.d.ts`: the inter-extension event bus.

The P7 rows were verified against the smidja worktree: `sdk/ui_component.go`,
`sdk/ui_renderers.go`, `internal/extensionui/registry.go`,
`internal/ui/extension_runtime.go`, `extension_components.go`,
`extension_editor.go`, `extension_modal.go`, `extension_bound_ui.go`,
`internal/extensions/api_ui.go` and `context_ui.go`,
`internal/tui/editor_extensions.go`, `internal/tui/interactive/extensions.go`
and `frame_sanitize.go`, plus the P7 test files listed in
[extension UI SDK](sdk-ui.md).

The R2a rows follow the composed host bindings in the smidja worktree:
`internal/cli/host_runtime.go`, `host_context.go`, `host_compaction.go`,
`host_session_actions.go`, and `context_preparer.go`;
`internal/extensions/api.go` and `runtime.go`; `internal/agent/loop.go`
and `ports.go`; `internal/tools/exec_direct.go`; and
`internal/contextmanager/manager.go`.

## Extension API surface (`pi.*`)

From `ExtensionAPI` in `dist/core/extensions/types.d.ts`. The smidja
contract is the `sdk.API` interface in `sdk/context.go`; the P7
registration methods are reached through the optional
`sdk.UIRegistrationAPI` interface in `sdk/ui_renderers.go`.

| Pi capability | Disposition | Smidja v0 mapping |
| --- | --- | --- |
| `on(event, handler)` | implement now (8 of the events) | typed registries: `LLMHookRegistry`, `ToolHookRegistry`, `SessionHookRegistry`; the full event disposition is in the events table below |
| `registerTool` | implement now | `API.RegisterTool`; registering an existing name replaces it (Pi tool override) |
| `registerCommand` | implement now | `API.RegisterCommand`; duplicate names get numeric invocation suffixes |
| `registerShortcut` | deferred | no SDK method exists; extension keybinding registration is not in the v0 contract and the TUI keybinding registry is host-only |
| `registerFlag` | deferred | `API.RegisterFlag` is frozen but returns the unavailable error naming the method; flag registration is next-slice work |
| `getFlag` | deferred | `API.Flags` returns an empty map placeholder; no flag values are populated |
| `registerMessageRenderer` | implement now | `UIRegistrationAPI.RegisterMessageRenderer`; backed by `internal/extensionui.Registry` and the runner surface for live and replayed messages |
| `registerMarkdownTransformer` | implement now | `UIRegistrationAPI.RegisterMarkdownTransformer`; composed in registration order and applied to live and replayed Markdown |
| `registerEntryRenderer` | implement now | `UIRegistrationAPI.RegisterEntryRenderer`; separate map from message renderers |
| `sendMessage` | deferred | `API.SendMessage` returns the unavailable error. The host has a delivery seam (`Runner.DeliverCustomMessage`) used by wiring and replay, and renderers draw host-delivered messages, but an extension cannot enqueue a custom message yet; delivery modes and queue ordering land with the next slice |
| `sendUserMessage` | deferred | `API.SendUserMessage` returns the unavailable error; text and image content both unwired |
| `appendEntry` | implement now (composed host) | `API.AppendEntry` persists a custom session entry through the active recorder and delivers it to the TUI entry renderer; a non-empty custom type and JSON-marshalable data are required. The bare API keeps returning the unavailable error |
| `setSessionName` | implement now (composed host) | `API.SetSessionName` persists a session-info entry, updates the live handle, and refreshes the TUI footer; a non-empty name is required and `SessionView.Name` reads the updated value. The bare API keeps returning the unavailable error |
| `getSessionName` | implement now | read side via `HandlerContext.SessionManager().Name()` |
| `setLabel` | implement now (composed host) | `API.LabelEntry` persists a label entry that the session tree projection reads back; a non-empty entry id is required. The bare API keeps returning the unavailable error |
| `exec` | implement now (composed host) | `API.Exec` runs argv directly with bounded output, workspace cwd, sanitized environment, and process-group cancellation on timeout, cancel, and shutdown. The bare API keeps returning the unavailable error |
| `getActiveTools` | implement now | `API.ActiveTools` |
| `getAllTools` | implement now | `API.AllTools` (`ToolInfo` with name, description, schema, source) |
| `setActiveTools` | implement now (composed host) | `API.SetActiveTools` gates advertisement and execution: unknown names are ignored, `nil` resets to all registered tools, an empty list disables all, and `AllTools` keeps the full registry. The bare API keeps returning the unavailable error |
| `getCommands` | implement now | `API.Commands` (`CommandInfo` without Pi's `sourceInfo` provenance) |
| `setModel` | deferred | `API.SetModel` returns the unavailable error |
| `getThinkingLevel` | implement now | read side via `HandlerContext.ThinkingLevel()` |
| `setThinkingLevel` | deferred | `API.SetThinkingLevel` returns the unavailable error; model-capability clamping is unwired |
| `registerProvider` | deferred | `API.RegisterProvider` returns the unavailable error; the OpenRouter-completions dialect and other dialects are unwired |
| `unregisterProvider` | deferred | `API.RemoveProvider` returns the unavailable error |
| `events` bus | deferred | `API.EmitCustomEvent` returns the unavailable error; the subscribe side is not in the v0 contract |

## Handler context surface (`ctx.*`)

From `ExtensionContext` in `dist/core/extensions/types.d.ts`. The smidja
contract is `sdk.HandlerContext` in `sdk/context.go`.

| Pi capability | Disposition | Smidja v0 mapping |
| --- | --- | --- |
| `ctx.ui` | implement now, print-mode | `HandlerContext.UI()`; the interactive TUI returns the bound runner UI, which implements `sdk.ExtendedUI`; print mode returns the no-op UI, which also implements `sdk.ExtendedUI` with unsupported, error, or no-op semantics. `LineUI` implements only `sdk.UI`, so callers assert and guard with `HasUI()` |
| `ctx.mode` | implement now | `Mode` with `ModeInteractive` and `ModePrint`; Pi's `rpc` and `json` modes deferred to the gateway phase |
| `ctx.hasUI` | implement now | `HandlerContext.HasUI()` |
| `ctx.cwd` | implement now | `HandlerContext.Cwd()` |
| `ctx.sessionManager` | implement now (subset) | `SessionView`: `ID`, `Path`, `Cwd`, `Name`, `Messages`; entry and tree access is not in the v0 session view |
| `ctx.modelRegistry` | implement now (subset) | `ModelRegistry`: `Model`, `Available`, `Find`; provider auth resolution deferred |
| `ctx.model` | implement now | `HandlerContext.Model()` (`sdk.Model` with `ID`, `Name`, `Provider`) |
| `ctx.scopedModels` | deferred | model scoping feature not in v0 |
| `ctx.thinkingLevel` | implement now | `HandlerContext.ThinkingLevel()` |
| `ctx.isIdle()` | deferred | delivery-queue wave; `CommandContext.WaitForIdle` returns `sdk.ErrModeUnsupported` in the current contexts |
| `ctx.isProjectTrusted()` | deferred | the interactive TUI asks for workspace trust at startup and does not persist it; extensions cannot query trust in the v0 contract |
| `ctx.signal` | implement now | `HandlerContext.Signal()` returns a `context.Context`, nil when idle (Pi returns `undefined`) |
| `ctx.abort()` | implement now | `HandlerContext.Abort()` |
| `ctx.hasPendingMessages()` | deferred | delivery-queue wave |
| `ctx.shutdown()` | implement now | `HandlerContext.Shutdown()` |
| `ctx.getContextUsage()` | implement now | `HandlerContext.ContextUsage()` (`sdk.ContextUsage`; tokens and percent nil when unknown) |
| `ctx.compact()` | implement now | `HandlerContext.Compact` with `CompactOptions` (`OnComplete`, `OnError`) |
| `ctx.getSystemPrompt()` | implement now | `HandlerContext.SystemPrompt()` |

## Command context extras

From `ExtensionCommandContext` and `ReplacedSessionContext` in
`dist/core/extensions/types.d.ts`. The smidja contract is
`sdk.CommandContext` in `sdk/context.go`. The deferred entries keep their
signatures frozen in the contract so later waves do not rework them.

| Pi capability | Disposition | Smidja v0 mapping |
| --- | --- | --- |
| `getSystemPromptOptions()` | deferred | system prompt builder not modeled in v0 |
| `waitForIdle()` | deferred | signature frozen in `sdk.CommandContext`; the current command contexts return `sdk.ErrModeUnsupported` |
| `newSession()` | implement now | `CommandContext.NewSession` in the interactive TUI command context; print and line modes return `sdk.ErrModeUnsupported` |
| `fork()` | implement now | `CommandContext.Fork` in the interactive TUI command context; a fork activates a separate session file; print and line modes return `sdk.ErrModeUnsupported` |
| `navigateTree()` | deferred | `CommandContext.NavigateTree` returns `sdk.ErrModeUnsupported`; the TUI `/tree` browser is read-only |
| `switchSession()` | implement now | `CommandContext.SwitchSession` in the interactive TUI command context; print and line modes return `sdk.ErrModeUnsupported` |
| `reload()` | deferred | hot-reload wave |
| `ReplacedSessionContext` (`sendMessage`, `sendUserMessage` on the replacement session) | deferred | with the session-replacement flow |

## UI surface (`ctx.ui.*`)

From `ExtensionUIContext` in `dist/core/extensions/types.d.ts`. The smidja
contract is `sdk.UI` in `sdk/ui.go` for the base methods and the optional
`sdk.ExtendedUI` interface in `sdk/ui_component.go` for the P7 methods.

| Pi capability | Disposition | Smidja v0 mapping |
| --- | --- | --- |
| `select()` | implement now, print-mode | `UI.Select`; returns `ErrModeUnsupported` in print mode |
| `confirm()` | implement now, print-mode | `UI.Confirm`; returns `ErrModeUnsupported` in print mode |
| `input()` | implement now, print-mode | `UI.Input`; returns `ErrModeUnsupported` in print mode |
| `editor()` | implement now, print-mode | `UI.Editor`; returns `ErrModeUnsupported` in print mode |
| `notify()` | implement now, print-mode | `UI.Notify`; no-op in print mode |
| `setStatus()` | implement now, print-mode | `UI.SetStatus`; no-op in print mode |
| `setWidget()` | implement now, print-mode | `UI.SetWidget` for string lists and `ExtendedUI.SetWidgetComponent` for keyed component widgets; no-op in print mode |
| `setWorkingMessage()` | implement now, print-mode | `UI.SetWorkingMessage`; no-op in print mode |
| `setTitle()` | implement now, print-mode | `UI.SetTitle`; no-op in print mode |
| `onTerminalInput()` | implement now, print-mode | `ExtendedUI.OnTerminalInput`; returns an idempotent unsubscribe function; hooks run after protocol filtering and before application keybindings, and host modals that capture input bypass them; returns `ErrModeUnsupported` in print mode |
| `setWorkingVisible()`, `setWorkingIndicator()`, `setHiddenThinkingLabel()` | implement now, print-mode | `ExtendedUI.SetWorkingVisible`, `SetWorkingIndicator`, `SetHiddenThinkingLabel`; a nil indicator restores defaults, an explicitly empty frame list hides, intervals are milliseconds, and an empty label restores the default; setters are no-ops in print mode |
| `setFooter()`, `setHeader()` | implement now, print-mode | `ExtendedUI.SetFooter` and `SetHeader`; a nil factory restores the built-in footer or the empty header and disposes the replaced component; no-ops in print mode |
| `custom()` components | implement now, print-mode | `ExtendedUI.ShowModal` for factory-built modals and `ExtendedUI.ShowComponent` for registered components; placement options are not modeled; factories run outside host locks; returns `ErrModeUnsupported` in print mode |
| `pasteToEditor()`, `setEditorText()`, `getEditorText()`, `addAutocompleteProvider()`, `setEditorComponent()`, `getEditorComponent()` | implement now, print-mode | `ExtendedUI.PasteToEditor`, `SetEditorText`, `GetEditorText`, `AddAutocompleteProvider`, `SetEditorComponent`, `GetEditorComponent`; the editor factory receives `sdk.EditorContext`, a nil factory restores the built-in editor, and autocomplete returns an idempotent unsubscribe; `SetEditorComponent` and `AddAutocompleteProvider` return `ErrModeUnsupported` in print mode and the getters return zero values |
| `theme`, `getAllThemes()`, `getTheme()`, `setTheme()`, `getToolsExpanded()`, `setToolsExpanded()` | implement now, print-mode | `ExtendedUI.AllThemes`, `GetTheme`, `ActiveTheme`, `SetTheme`, `ToolsExpanded`, `SetToolsExpanded`; reads do not activate a theme, `SetTheme` applies immediately and retimes the custom-theme watcher without persisting; print mode returns no themes and `ErrModeUnsupported` for `SetTheme` |

## Events

From `ExtensionEvent` and the agent-session event set in
`dist/core/extensions/types.d.ts` and `dist/core/agent-session.d.ts`. The
smidja contract is the handler func types in `sdk/hooks.go` and the event
structs in `sdk/events.go`.

P7 adds no events. The 27 deferred events below stay outside P7 on
purpose, one per later runtime wave, and the matrix never counts a typed
handler signature as runtime dispatch.

| Pi event | Disposition | Smidja v0 mapping |
| --- | --- | --- |
| `context` | implement now | `ContextHandler`; deep-copied messages, replacement via `ContextEventResult` |
| `message_end` | implement now | `MessageEndHandler`; replacement must keep the original role |
| `auto_retry_start` | implement now | `AutoRetryStartHandler`; an agent-session event in Pi, a first-class extension hook in smidja |
| `auto_retry_end` | implement now | `AutoRetryEndHandler`; same note as above |
| `tool_call` | implement now | `ToolCallHandler`; `ToolCallDecision{Block, Reason}`; handler errors are logged and the call is allowed (Pi fail-safe) |
| `tool_result` | implement now | `ToolResultHandler`; partial patches via `ToolResultEventResult` |
| `session_start` | implement now | `SessionStartHandler` with `SessionStartReason` |
| `session_shutdown` | implement now | `SessionShutdownHandler` with `SessionShutdownReason` |
| `before_agent_start` | deferred | turn-setup wave |
| `agent_start`, `agent_end`, `agent_settled` | deferred | agent lifecycle wave |
| `turn_start`, `turn_end` | deferred | loop-detector wave |
| `message_start`, `message_update` | deferred | streaming wave |
| `tool_execution_start`, `tool_execution_update`, `tool_execution_end` | deferred | streaming and parallel-tools wave |
| `model_select`, `thinking_level_select` | deferred | model-registry wave |
| `user_bash` | deferred | interactive-commands wave |
| `input` | deferred | input-pipeline wave |
| `resources_discover` | deferred | resources wave |
| `session_info_changed` | deferred | session metadata events are not dispatched in v0; the TUI reads its own session state |
| `session_before_switch`, `session_before_fork` | deferred | session transition events are not dispatched in v0; the TUI handles switches internally |
| `session_before_compact`, `session_compact` | deferred | compaction wave |
| `session_before_tree`, `session_tree` | deferred | session tree events are not dispatched in v0; the TUI tree browser is read-only and internal |
| `project_trust` | deferred | trust wave |
| `before_provider_request`, `before_provider_headers`, `after_provider_response` | deferred | provider wave |

## Session and compaction data model

Verified from `dist/core/session-manager.d.ts` and
`dist/core/compaction/compaction.d.ts`. The compaction entry fields are
modeled in `sdk.CompactionResult`:

| Pi field | Smidja v0 field |
| --- | --- |
| `summary` | `CompactionResult.Summary` |
| `firstKeptEntryId` | `CompactionResult.FirstKeptEntryID` |
| `tokensBefore` | `CompactionResult.TokensBefore` |
| `estimatedTokensAfter?` | `CompactionResult.EstimatedTokensAfter` |
| `details?` | `CompactionResult.Details` |
| `usage?` | `CompactionResult.Usage` |
| `fromHook?` | `CompactionResult.FromHook` |

## Disposition counts

The row inventory is fixed at 102 capabilities. Print-mode rows are
counted in their own column and are not merged into the core count.

| Surface | Implement now | Implement now, print-mode | Deferred | Total |
| --- | --- | --- | --- | --- |
| Extension API (`pi.*`) | 16 | 0 | 10 | 26 |
| Handler context (`ctx.*`) | 13 | 1 | 4 | 18 |
| Command context | 3 | 0 | 5 | 8 |
| UI (`ctx.ui.*`) | 0 | 15 | 0 | 15 |
| Events | 8 | 0 | 27 | 35 |
| Total | 40 | 16 | 46 | 102 |

56 capabilities are implemented: 40 core plus 16 with print-mode
semantics. The UI table moved from 9 to 15 print-mode rows, because P7
closes its 6 deferred rows. The Extension API table moved from 11 to 16
implemented rows, because R2a backs its five session, tool, and process
methods. The Extension API table carries 10 deferred rows: the 9
signature-frozen methods listed at the top of this page plus
`registerShortcut`, which has no method at all.

In print mode the `ctx.ui` surface has 4 blocking dialogs that return
`sdk.ErrModeUnsupported` and 5 fire-and-forget methods that are no-ops.
The P7 surface follows the same rule: interactive-only operations return
`sdk.ErrModeUnsupported`, setters are no-ops, and getters return zero
values. The interactive TUI command context also implements `newSession`,
`fork`, and `switchSession`; print and line modes return
`sdk.ErrModeUnsupported` for those three.

## Deviations from Pi

- **Retry events are extension hooks.** In Pi, `auto_retry_start` and
  `auto_retry_end` are agent-session events, not extension events. Smidja
  exposes them as first-class extension hooks because the retry policy is
  core (plan variation V-005) and the loop wires the events directly. The
  event shapes (attempt, maxAttempts, delayMs, errorMessage for start;
  success, attempt, finalError for end) match Pi's exactly.
- **Dialogs return `ErrModeUnsupported` in print mode.** Pi's dialogs
  return `undefined`/`false` in modes without UI. Smidja returns the
  sentinel so extensions can distinguish "no UI" from "user cancelled";
  the recommended pattern is to check `HasUI()` before prompting.
- **`ToolCallDecision` is a pointer.** Handlers return `nil` to allow the
  call and `&ToolCallDecision{Block: true, Reason: ...}` to deny it,
  mirroring Pi's "return nothing vs return `{block: true}`".
- **Partial patches use pointer fields.** `ToolResultEventResult.IsError`
  is `*bool` and `Usage` is `*Usage` so "field omitted" is distinct from
  "set to zero", matching Pi's per-field `!== undefined` checks.
- **`SetModel` returns an error** where Pi's `setModel` returns
  `Promise<boolean>`.
- **`UnregisterTool` is smidja-only.** Pi has no tool removal; smidja adds
  the symmetric registry operation.
- **`sendUserMessage` takes a string.** Pi accepts text and image content
  arrays; images are deferred.
- **`getCommands` returns `CommandInfo` without `sourceInfo`** provenance;
  the richer shape lands with the command-resolver wave.
- **Modes cover interactive and print only.** Pi's `rpc` and `json` modes
  land with the gateway phase.
- **Read-side aliases.** `getSessionName` and `getThinkingLevel` live on
  `SessionView`/`HandlerContext` rather than on the API, because handler
  contexts are the primary access path in smidja; the API still carries
  the setters, keeping the full Pi method surface reachable.
- **Event constants keep Pi's strings.** The `Event*` constants in
  `sdk/events.go` equal Pi's event type names (`context`, `message_end`,
  `tool_call`, ...); the typed registries are the idiomatic Go
  registration path.
- **The extended UI surface is an optional interface.** Pi methods such as
  `setHeader` and `addAutocompleteProvider` do not extend `sdk.UI`.
  Smidja puts them on `sdk.ExtendedUI`, so the frozen `sdk.UI` interface
  and the line interface keep compiling. Callers must assert and guard
  with `HasUI()`.
- **Registration is an optional interface on `sdk.API`.** The renderer,
  widget, component, and input hook registrations live on
  `sdk.UIRegistrationAPI`, which `sdk.API` implementations satisfy but do
  not declare. Extensions that only know `sdk.API` keep compiling, and
  registration succeeds into an inert registry when no surface is
  attached.
- **Signature-frozen methods are deferred until runtime-backed.**
  `SendMessage`, `SendUserMessage`, `SetModel`, `SetThinkingLevel`,
  `RegisterProvider`, `RemoveProvider`, `RegisterFlag`, `Flags`, and
  `EmitCustomEvent` are counted as deferred even though their signatures
  exist, because calling them returns the unavailable error (or, for
  `Flags`, an empty map placeholder). Counting signatures as runtime
  would overstate parity.
- **Five methods are backed only through the composed host.**
  `SetActiveTools`, `AppendEntry`, `SetSessionName`, `LabelEntry`, and
  `Exec` are runtime-backed in the CLI host and unavailable on the bare
  API and in the gateway. The composed contexts, snapshots, and lifecycle
  rules are documented in [SDK runtime](sdk-runtime.md).
- **Host delivery is not `SendMessage`.** The interactive runner can
  render a custom message or entry the host delivers (`DeliverCustomMessage`,
  `DeliverCustomEntry`), which is how replay and renderer tests exercise
  the render path. An extension still cannot send a message through
  `API.SendMessage`, so the two capabilities are reported separately.
- **`registerShortcut` stays out.** No SDK method exists, and extension
  keybinding registration is not planned for the current surface.
- **P7 adds no events.** Renderer and UI registration does not dispatch
  any of the deferred Pi events; event parity remains an explicit later
  wave.
