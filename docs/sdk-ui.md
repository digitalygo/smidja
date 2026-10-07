# Smidja extension UI SDK

The extension-facing UI surface lives in `github.com/digitalygo/smidja/sdk`. Every pre-P7 signature is unchanged. Two optional interfaces carry the surface:

- `sdk.ExtendedUI` extends `sdk.UI` with host UI operations: modals, header, footer, widgets, working indicator, editor access, autocomplete, terminal input hooks, themes, and tools-expanded state.
- `sdk.UIRegistrationAPI` extends `sdk.RendererRegistry` with component, widget, and terminal input hook registration.

Both are reached through type assertions, so a UI value that only satisfies `sdk.UI` keeps compiling and behaving as before. The compile-time contract checks live in `sdk/p7_contract_test.go`. The new declarations add no imports, so the public SDK package stays stdlib-only with zero external dependencies.

P7's source and adapters are implemented and independently gated on `feat/tui`. The installed binary and final runtime acceptance are tracked separately in the living TUI plan. The [SDK parity matrix](sdk-parity-matrix.md) tracks what is runtime-backed, what is print-mode only, and what stays deferred. The composed host contexts and the backed session, tool, exec, and compaction actions are documented in [SDK runtime](sdk-runtime.md).

## Getting the optional interfaces

An extension receives `sdk.API` in its `Setup` method. Registration methods are not part of `sdk.API`, so assert to `sdk.UIRegistrationAPI` first.

```go
func registerNoteRenderer(api sdk.API) error {
	registration, ok := api.(sdk.UIRegistrationAPI)
	if !ok {
		return nil
	}
	return registration.RegisterMessageRenderer("note", renderNote)
}

type noteComponent struct {
	body string
}

func (c noteComponent) Render(width int) []string {
	return []string{c.body}
}

func (c noteComponent) Invalidate() {}

func renderNote(ctx sdk.RenderContext, message sdk.CustomMessage) sdk.Component {
	return noteComponent{body: message.Content}
}
```

Handler code receives `sdk.HandlerContext`. Guard with `HasUI()` and assert to `sdk.ExtendedUI`.

```go
func showConfirm(ctx sdk.HandlerContext, prompt string) (sdk.ModalResult, error) {
	if !ctx.HasUI() {
		return sdk.ModalResult{}, sdk.ErrModeUnsupported
	}
	extended, ok := ctx.UI().(sdk.ExtendedUI)
	if !ok {
		return sdk.ModalResult{}, sdk.ErrModeUnsupported
	}
	return extended.ShowModal(func(done func(sdk.ModalResult)) sdk.Component {
		return confirmComponent{done: done, prompt: prompt}
	})
}

type confirmComponent struct {
	done   func(sdk.ModalResult)
	prompt string
}

func (c confirmComponent) Render(width int) []string {
	return []string{c.prompt}
}

func (c confirmComponent) Invalidate() {}

func (c confirmComponent) HandleInput(data string) {
	c.done(sdk.ModalResult{Value: true})
}
```

`internal/ui.LineUI`, the line interface used by print mode and non-TTY sessions, implements `sdk.UI` only. Never assume that every `sdk.UI` value implements `sdk.ExtendedUI`. In print mode the default handler context returns a no-op UI that does implement `sdk.ExtendedUI`, so assertions succeed there and every operation is unsupported or inert.

## Components

A component is any value with:

- `Render(width int) []string`
- `Invalidate()`

Optional capabilities are discovered by type assertion:

- `sdk.InputHandler` with `HandleInput(data string)` receives terminal input while the component is focused, for example inside a modal.
- `sdk.Disposable` with `Dispose()` gets called once by the host when the host releases the component.
- `sdk.ModalComponent` with `SetModalDone(done func(sdk.ModalResult))` is used for components opened by key through `ShowComponent`.

Host rules:

- A component factory that panics or returns nil produces an error. Registry-driven factory failures also add a warning notice, and the previous component stays installed where one exists.
- The host owns disposal. Replacement, clearing, modal close, transcript replacement, and runner stop dispose the old component exactly once. Disposing the same component twice is safe on the host side.
- The component's own `Render`, `HandleInput`, and `Dispose` may call back into the UI. Extension code runs outside host locks; frames are prepared before the surface lock is taken. Rendering and input can run concurrently, so components must protect their own mutable state. The host retires a component immediately but defers its raw `Dispose` until all in-flight callbacks have returned.
- Component callbacks are panic-contained. A recovered panic is reported once per kind and value as `extensions: <kind> callback panic: <value>` in the transcript. A render panic marks the component failed and shows `[extension component error]` until the host replaces it.
- Frames are sanitized before display. Allowed escapes are SGR sequences (`CSI ... m`) with at most 32 parameter bytes and OSC 8 hyperlinks whose cleaned target is empty or starts with `http://`, `https://`, or `mailto:`. OSC 8 parameters are colon-separated `key=value` pairs, at most 64 bytes in total, with keys and values restricted to letters, digits, `_`, `.`, and `-`. Malformed or oversized parameters drop the whole sequence. Accepted links are reserialized rather than echoed from input. Every other terminal control, including clipboard OSC and DCS, is removed. Tabs become three spaces, C0 and C1 controls are dropped.

## Registration through UIRegistrationAPI

`UIRegistrationAPI` embeds `sdk.RendererRegistry`, so one assertion gives all registration methods:

| Method | Backing |
| --- | --- |
| `RegisterComponent`, `UnregisterComponent` | Keyed modal components opened by `ShowComponent`. |
| `RegisterWidget`, `UnregisterWidget` | Keyed widget component factories. |
| `RegisterTerminalInputHook`, `UnregisterTerminalInputHook` | Keyed terminal input hooks. |
| `RegisterMessageRenderer`, `UnregisterMessageRenderer` | Custom message renderers, separate from entry renderers. |
| `RegisterEntryRenderer`, `UnregisterEntryRenderer` | Custom entry renderers, separate from message renderers. |
| `RegisterMarkdownTransformer`, `UnregisterMarkdownTransformer` | Markdown transformers composed in registration order. |

Registry rules:

- Registration order is deterministic. The first registration of a key sets its position, a replacement keeps the position, and an unregister removes the key.
- Empty keys and nil values are rejected. Unregistering a missing key returns an error.
- A runner attaches one registry at startup. Registrations already present are applied at attach, and later changes are pushed to the attached runner. A second attach to the same runner fails.
- Registries are isolated per runner. A widget or renderer registered for one runner never renders in another.
- Registration itself succeeds in every mode. Without an attached TUI surface the registry is inert, which is what happens in print mode and in non-TTY sessions.

## Message and entry renderers

`MessageRenderer` receives `(sdk.RenderContext, sdk.CustomMessage)` and returns `sdk.Component`. `sdk.CustomMessage` carries `Type`, `Content`, `Display`, and `Details`. `EntryRenderer` receives `(sdk.RenderContext, sdk.Entry)` with `CustomType` and `Data`.

The render context carries the last frame `Width`, whether content is `Streaming`, a `Theme` handle, and host metadata: `Cwd`, `SessionID`, `Model`, and `ThinkingLevel`.

Both live and replayed content dispatch to renderers:

- Live custom messages arrive through the host delivery seam used by runner wiring (`DeliverCustomMessage`, `DeliverCustomEntry`); this is not the extension-facing `API.SendMessage`, which stays unavailable.
- Replay reconstructs renderers from session entries, and the session projection distinguishes entry (`CustomKindEntry`) from message (`CustomKindMessage`) so each uses its own map.

When no renderer matches, when the renderer returns nil, or when it panics, the host renders a fallback custom block. The label is the entry label or the custom type. The body is the message content, or for entry renderers the text with the string form of `Data` as the fallback when text is empty. Replacing or unregistering a renderer re-renders existing transcript rows and disposes the old components once.

## Markdown transformers

Transformers run in registration order, each receiving the previous output, and apply to live and replayed Markdown:

- User messages use kind `user`.
- Assistant text uses kind `assistant`, thinking segments use `assistant-thinking`.
- Skill and custom blocks use kind `custom`.

`Width` is the current frame width, and `Streaming` is true while an assistant turn is still open. Transformer output is sanitized to display text before rendering, and a panicking transformer leaves its input unchanged so the next transformer still runs.

## Modals

`ShowModal(factory)` builds a component from a factory that receives a `done` function. `ShowComponent(key)` builds a registered component and calls `SetModalDone` when the component implements `sdk.ModalComponent`.

Modal rules:

- The first `done` call wins. Later calls are ignored, and a `done` call made before the modal is shown is still honored.
- Cancellation is reported as `sdk.ModalResult{Canceled: true}` when the bound context ends, the runner stops, or terminal EOF arrives.
- Modals queue. A second request waits until the first session is released.
- The modal session is released before the component is disposed, so a component's `Dispose` may open another modal.
- Modal factories run outside host locks and panics or nil results return errors. A modal renders synchronously so it can respond while input is being handled.

## Header, footer, and widgets

- `SetHeader(factory)` installs a component above the transcript. A nil factory restores the empty header.
- `SetFooter(factory)` replaces the built-in footer inside the dock. A nil factory restores the built-in footer.
- `SetWidgetComponent(key, factory)` installs a keyed component widget below the status region. A nil factory clears the key. Empty keys are rejected.
- Widgets render in registration order. String widgets set through `sdk.UI.SetWidget` and component widgets coexist.
- Replacing a widget disposes the old component exactly once, unless the factory returns the same component instance, in which case the instance is kept.
- Factories run outside host locks. Unchanged widgets are not rebuilt when unrelated registrations change.
- Runner stop clears every slot, restores the default editor and built-in footer, disposes owned components once, detaches the registry, and rejects later calls.

## Editor

`SetEditorComponent(factory)` replaces the active editor. The factory receives `sdk.EditorContext`:

- `Theme`, a handle with `Name`, `Fg`, and `Bg`.
- `Keybindings`, a read-only lookup of action IDs to keys.
- `TerminalRows` and `Width` for layout decisions.

The factory runs outside host locks. A panicking factory or a nil result returns an error and keeps the current editor. The current editor text is copied into the new component before it is installed, and the surface routes focus and submit/change events to it.

`GetEditorComponent` returns the custom component while one is installed, and otherwise a stable adapter over the built-in editor. `SetEditorComponent(nil)` restores the built-in editor, copies the custom text back into it, and disposes the custom component. Because the built-in editor object is retained, its queued follow-up messages survive the custom editor session and its queued count stays in the footer.

`PasteToEditor` inserts through `sdk.EditorInserter.InsertTextAtCursor` when the component implements it, and otherwise appends with `SetText`. `SetEditorText` and `GetEditorText` operate on the active editor. The external editor command pushes custom text into the built-in editor before opening `$EDITOR` and pulls the result back afterwards.

Application-level keys still belong to the runner: external editor, exit, interrupt, and host action keys are handled before input reaches the custom editor component.

## Autocomplete

`AddAutocompleteProvider(provider)` registers an `sdk.AutocompleteProvider` and returns an idempotent unsubscribe function. Nil providers are rejected.

- Providers run after the editor processes an edit, outside the editor lock, so a provider may edit the text or read state. Results are discarded when the text changed while the provider ran.
- External suggestions are sanitized, empty values are dropped, and providers that panic are skipped with a warning.
- When external and built-in suggestions both exist, external suggestions come first and duplicates by value are removed.
- Accepting a suggestion replaces the current token only, bounded by the token start and cursor positions, so surrounding text is preserved.
- Unsubscribing removes the provider. Unsubscribing twice is safe, and a provider that unsubscribes itself still completes the current call.

## Terminal input hooks

`OnTerminalInput(handler)` registers an `sdk.TerminalInputHandler` and returns an idempotent unsubscribe function. Nil handlers are rejected.

- Input hooks run before application keybindings and after terminal protocol replies and key-release events are filtered out.
- Registry hooks run before hooks registered through `OnTerminalInput`, each group in registration order. A hook returning `Consume: true` stops routing and the input is discarded. `Replace: true` replaces the data seen by later hooks and by the focused editor.
- A panicking hook is skipped and the next hook still runs. A hook may unsubscribe itself or register another hook during dispatch.
- Host modals that capture input, including masked secret prompts, bypass hooks entirely and never expose those keystrokes.
- Unsubscribe is safe after a hook has already run or been removed, and runner stop clears every hook so a later input never reaches it.

## Working indicator, themes, and tools

- `SetWorkingVisible` toggles the working row; it starts visible.
- `SetWorkingIndicator` takes frames and an interval in milliseconds. A nil indicator restores the default frames and interval. A non-nil but empty frame list hides the indicator while it is set. Frames are sanitized to a single line.
- `SetHiddenThinkingLabel` sets the label shown while thinking blocks are collapsed. An empty string restores the default.
- `AllThemes` returns `sdk.ThemeInfo` values with `Name` and `Path`, sorted by name. `GetTheme` loads a theme by name without activating it. `ActiveTheme` returns a handle for the active theme.
- `SetTheme` validates and applies a theme immediately. It retimes the custom-theme watcher, and it does not write settings. A missing theme returns an error.
- `ToolsExpanded` reads the current tool expansion state, and `SetToolsExpanded` updates existing collapsible blocks.

## Print mode and non-interactive behavior

The default handler context reports `ModePrint`, `HasUI` false, and returns a no-op `sdk.ExtendedUI`. Its behavior separates three cases:

- Blocking or interactive-only operations return `sdk.ErrModeUnsupported`: `ShowModal`, `ShowComponent`, `SetWidgetComponent`, `SetEditorComponent`, `AddAutocompleteProvider`, `OnTerminalInput`, and `SetTheme`.
- Fire-and-forget setters are no-ops: `SetHeader`, `SetFooter`, `SetWorkingVisible`, `SetWorkingIndicator`, `SetHiddenThinkingLabel`, `PasteToEditor`, `SetEditorText`, and `SetToolsExpanded`.
- Getters return zero values: `GetEditorComponent` nil, `GetEditorText` empty, `AllThemes` nil, `GetTheme` false, `ActiveTheme` nil, `ToolsExpanded` false.

`UIRegistrationAPI` registration calls also return `sdk.ErrModeUnsupported` when the API has no registry attached.

## Automated verification

The P7 contract is covered by:

- `sdk/p7_contract_test.go` for the optional-interface compile checks.
- `internal/extensionui/registry_test.go` for ordering, replacement, unregister, and notification.
- `internal/ui/extension_ui_test.go`, `extension_editor_hooks_test.go`, `extension_modal_coverage_test.go`, `extension_coverage_test.go`, and `extension_lifecycle_p7_test.go` for the runtime adapters, lifecycle, panics, modals, editor, hooks, themes, and stop cleanup.
- `internal/tui/editor_extensions_test.go` for autocomplete semantics and `internal/tui/interactive/extensions_test.go` and `reentrancy_p7_test.go` for renderer, transformer, sanitization, and reentrancy behavior.
- `internal/cli/tui_extension_attach_test.go` and `internal/cli/tui_extension_replay_test.go` for startup attach and replay rendering.
