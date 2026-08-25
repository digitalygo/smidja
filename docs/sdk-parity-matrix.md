# Smidja SDK parity matrix

Method-by-method parity target between Pi's extension surface and the
smidja v0 SDK, frozen on the installed Pi 0.84.2
(`@earendil-works/pi-coding-agent`). Every Pi capability maps to one
disposition: implemented now, implemented now with print-mode semantics,
or deferred to a later phase. The smidja side of the matrix is the public
`github.com/digitalygo/smidja/sdk` package plus the internal ports in
`internal/agent/ports.go`.

## Disposition legend

- **implement now**: the capability is part of the phase 1 SDK contract and
  backed by a real v0 implementation.
- **implement now, print-mode**: the capability is in the contract and
  works in interactive mode; in print mode (`-p`) the blocking UI dialogs
  return `sdk.ErrModeUnsupported` and the fire-and-forget UI methods are
  no-ops, mirroring Pi's "extensions run but can't prompt" mode behavior.
- **deferred**: the capability is either in the contract with its
  signature frozen but its backing landing in a later wave, or entirely
  outside the v0 contract (TUI, gateway, sessions-tree, provider waves).

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

## Extension API surface (`pi.*`)

From `ExtensionAPI` in `dist/core/extensions/types.d.ts`. The smidja
contract is the `sdk.API` interface in `sdk/context.go`.

| Pi capability | Disposition | Smidja v0 mapping |
| --- | --- | --- |
| `on(event, handler)` | implement now (8 of the events) | typed registries: `LLMHookRegistry`, `ToolHookRegistry`, `SessionHookRegistry`; the full event disposition is in the events table below |
| `registerTool` | implement now | `API.RegisterTool`; registering an existing name replaces it (Pi tool override) |
| `registerCommand` | implement now | `API.RegisterCommand`; duplicate names get numeric invocation suffixes |
| `registerShortcut` | deferred | keybindings phase (no keybinding model in v0) |
| `registerFlag` | implement now | `API.RegisterFlag` |
| `getFlag` | implement now | `API.Flags` (map of current values, bool or string) |
| `registerMessageRenderer` | deferred | TUI phase |
| `registerMarkdownTransformer` | deferred | TUI phase |
| `registerEntryRenderer` | deferred | TUI phase |
| `sendMessage` | implement now | `API.SendMessage`; delivery modes (`steer`, `followUp`, `nextTurn`) modeled, queue ordering semantics land with the loop-detector wave |
| `sendUserMessage` | implement now | `API.SendUserMessage`; text content only, image content deferred |
| `appendEntry` | implement now | `API.AppendEntry` (custom session entries, not sent to the model) |
| `setSessionName` | implement now | `API.SetSessionName` |
| `getSessionName` | implement now | read side via `HandlerContext.SessionManager().Name()` |
| `setLabel` | implement now | `API.LabelEntry`; JSONL persistence of label entries lands with the sessions wave |
| `exec` | implement now | `API.Exec` with `ExecOptions` timeout |
| `getActiveTools` | implement now | `API.ActiveTools` |
| `getAllTools` | implement now | `API.AllTools` (`ToolInfo` with name, description, schema, source) |
| `setActiveTools` | implement now | `API.SetActiveTools`; unknown names ignored, additive changes supported |
| `getCommands` | implement now | `API.Commands` (`CommandInfo` without Pi's `sourceInfo` provenance) |
| `setModel` | implement now | `API.SetModel`; returns `error` instead of Pi's `Promise<boolean>` |
| `getThinkingLevel` | implement now | read side via `HandlerContext.ThinkingLevel()` |
| `setThinkingLevel` | implement now | `API.SetThinkingLevel`; model-capability clamping deferred |
| `registerProvider` | implement now | `API.RegisterProvider`; OpenRouter-completions dialect only, other dialects deferred |
| `unregisterProvider` | implement now | `API.RemoveProvider` |
| `events` bus | implement now (emit) | `API.EmitCustomEvent`; the subscribe side is not in the v0 contract and is deferred |

## Handler context surface (`ctx.*`)

From `ExtensionContext` in `dist/core/extensions/types.d.ts`. The smidja
contract is `sdk.HandlerContext` in `sdk/context.go`.

| Pi capability | Disposition | Smidja v0 mapping |
| --- | --- | --- |
| `ctx.ui` | implement now, print-mode | `HandlerContext.UI()`; see the UI table below |
| `ctx.mode` | implement now | `Mode` with `ModeInteractive` and `ModePrint`; Pi's `rpc` and `json` modes deferred to the gateway phase |
| `ctx.hasUI` | implement now | `HandlerContext.HasUI()` |
| `ctx.cwd` | implement now | `HandlerContext.Cwd()` |
| `ctx.sessionManager` | implement now (subset) | `SessionView`: `ID`, `Path`, `Cwd`, `Name`, `Messages`; entry and tree access deferred to the sessions wave |
| `ctx.modelRegistry` | implement now (subset) | `ModelRegistry`: `Model`, `Available`, `Find`; provider auth resolution deferred |
| `ctx.model` | implement now | `HandlerContext.Model()` (`sdk.Model` with `ID`, `Name`, `Provider`) |
| `ctx.scopedModels` | deferred | model scoping feature not in v0 |
| `ctx.thinkingLevel` | implement now | `HandlerContext.ThinkingLevel()` |
| `ctx.isIdle()` | deferred | covered by `CommandContext.WaitForIdle` |
| `ctx.isProjectTrusted()` | deferred | project trust not in smidja v0 |
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
| `waitForIdle()` | implement now | `CommandContext.WaitForIdle` |
| `newSession()` | implement now | `CommandContext.NewSession` (session store create) |
| `fork()` | deferred | session tree and branching wave; signature frozen in `ForkOptions` |
| `navigateTree()` | deferred | session tree wave; signature frozen in `TreeOptions` |
| `switchSession()` | deferred | session open/resume wave; signature frozen in `SwitchOptions` |
| `reload()` | deferred | hot-reload wave |
| `ReplacedSessionContext` (`sendMessage`, `sendUserMessage` on the replacement session) | deferred | with the session-replacement flow |

## UI surface (`ctx.ui.*`)

From `ExtensionUIContext` in `dist/core/extensions/types.d.ts`. The smidja
contract is `sdk.UI` in `sdk/ui.go`.

| Pi capability | Disposition | Smidja v0 mapping |
| --- | --- | --- |
| `select()` | implement now, print-mode | `UI.Select`; returns `ErrModeUnsupported` in print mode |
| `confirm()` | implement now, print-mode | `UI.Confirm`; returns `ErrModeUnsupported` in print mode |
| `input()` | implement now, print-mode | `UI.Input`; returns `ErrModeUnsupported` in print mode |
| `editor()` | implement now, print-mode | `UI.Editor`; returns `ErrModeUnsupported` in print mode |
| `notify()` | implement now, print-mode | `UI.Notify`; no-op in print mode |
| `setStatus()` | implement now, print-mode | `UI.SetStatus`; no-op in print mode |
| `setWidget()` | implement now, print-mode | `UI.SetWidget`; string-list content only, component factories deferred to the TUI phase |
| `setWorkingMessage()` | implement now, print-mode | `UI.SetWorkingMessage`; no-op in print mode |
| `setTitle()` | implement now, print-mode | `UI.SetTitle`; no-op in print mode |
| `onTerminalInput()` | deferred | TUI phase |
| `setWorkingVisible()`, `setWorkingIndicator()`, `setHiddenThinkingLabel()` | deferred | TUI phase |
| `setFooter()`, `setHeader()` | deferred | TUI phase |
| `custom()` components | deferred | TUI phase |
| `pasteToEditor()`, `setEditorText()`, `getEditorText()`, `addAutocompleteProvider()`, `setEditorComponent()`, `getEditorComponent()` | deferred | TUI phase |
| `theme`, `getAllThemes()`, `getTheme()`, `setTheme()`, `getToolsExpanded()`, `setToolsExpanded()` | deferred | TUI phase |

## Events

From `ExtensionEvent` and the agent-session event set in
`dist/core/extensions/types.d.ts` and `dist/core/agent-session.d.ts`. The
smidja contract is the handler func types in `sdk/hooks.go` and the event
structs in `sdk/events.go`.

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
| `session_info_changed` | deferred | sessions wave |
| `session_before_switch`, `session_before_fork` | deferred | sessions wave |
| `session_before_compact`, `session_compact` | deferred | compaction wave |
| `session_before_tree`, `session_tree` | deferred | session tree wave |
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

| Surface | Implement now | Implement now, print-mode | Deferred | Total |
| --- | --- | --- | --- | --- |
| Extension API (`pi.*`) | 21 | 0 | 5 | 26 |
| Handler context (`ctx.*`) | 14 | 0 | 4 | 18 |
| Command context | 2 | 0 | 6 | 8 |
| UI (`ctx.ui.*`) | 0 | 9 | 6 | 15 |
| Events | 8 | 0 | 27 | 35 |
| Total | 45 | 9 | 48 | 102 |

Of the 54 implemented capabilities, 4 dialogs return
`sdk.ErrModeUnsupported` in print mode and 5 fire-and-forget UI methods
are no-ops in print mode.

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
