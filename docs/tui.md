# Smidja TUI

Smidja has two interactive frontends. The TUI renders the transcript, editor, dialogs, selectors, and terminal extras in regular or fullscreen mode. The line interface stays for print mode and for any session whose standard input or output is not a terminal.

## Status

Phases P0 to P7 of the TUI workstream are implemented on `feat/tui`. P6 at `b091d74` passed installed-binary checks in regular and fullscreen modes. P7's additive SDK adapters have passed independent tests and quality/security review. R2a and R2b are published through `52dd418`; the R3 runtime controls are uncommitted in the worktree, their gate has not run, nothing beyond `52dd418` is published, and the installed binary remains P7. Final installation and expanded runtime acceptance are tracked in the living TUI plan. The [SDK parity matrix](sdk-parity-matrix.md) tracks each row, and the [extension UI SDK documentation](sdk-ui.md) documents the surface.

## Starting the TUI

A plain `smidja` run starts the TUI only when both conditions hold:

- No prompt is given with `-p`.
- Stdin and stdout are the real terminal file descriptors.

TTY detection runs an ioctl on the actual files behind the injected `Deps.Stdin` and `Deps.Stdout`. Non-file reader and writer test doubles are never treated as terminals. Injected `*os.File` values backed by real terminals, including PTYs, can activate the TUI.

When the terminal framework cannot start, smidja prints `smidja: tui unavailable (<reason>), using line mode` to stderr and continues with the line interface.

Print mode (`-p`) and every non-TTY path use the existing `LineUI` and are unchanged. Those paths never show the trust dialog, never run the startup sign-in flow, and never enter the alternate screen.

## Renderer modes

| Mode | Behavior |
|---|---|
| `regular` | Renders on the main screen and keeps terminal scrollback. This is the default. |
| `fullscreen` | Uses the alternate screen with a fixed viewport, synchronized output, and the final document printed when the TUI stops. |

`regular` matches Pi's default. Set the mode with `--tui-mode`, the `tuiMode` setting, or `SMIDJA_TUI_MODE`. Values are case-insensitive; anything other than `regular` or `fullscreen` fails configuration loading with an error that names the field or variable.

## Flags

| Flag | Values | Effect |
|---|---|---|
| `--tui-mode` | `regular`, `fullscreen` | Selects the interactive renderer for this run. |
| `--use-theme` | `name` or `lightTheme/darkTheme` | Selects the theme for this run. A pair follows the terminal background. |

Both flags are validated even when the TUI will not start, and both are ignored by print mode and by non-TTY sessions. A value given on the command line applies to that run only: smidja never writes the `theme` or `tuiMode` settings back to disk. The [settings page](settings.md) documents the full precedence chain, and the [themes page](themes.md) documents theme files.

## Themes

The built-in `dark` and `light` themes ship inside the binary. Custom themes live in `~/.smidja/themes` as JSON files, as described in the [themes documentation](themes.md).

Startup behavior:

- With no theme value, or with a `lightTheme/darkTheme` pair, the theme follows the terminal background, which smidja asks for with a bounded 100 ms query. When the terminal does not answer, the dark theme stays active.
- A single theme name is used as given.
- A theme name that passes validation but has no file or built-in match leaves the current theme in place and adds a `theme: ...` notice to the transcript.

The `/theme` selector applies a theme immediately for the session. It does not persist the choice. Only the active custom theme is watched for updates, and only while a custom theme file is loaded: smidja reloads it within a bounded polling interval, ignores invalid updates, and stops the watcher when the runner exits.

## Keybindings

Bindings come from the defaults in the [keybindings documentation](keybindings.md) and from `~/.smidja/keybindings.json`. A file that fails to parse produces a `keybindings: ...` warning notice and the defaults stay in effect. User bindings replace the default key list for that action.

## Sessions

The TUI owns one active session at a time and can switch it from slash commands. Session files keep their existing format; the TUI does not change the session codec or store semantics.

| Command | Behavior |
|---|---|
| `/new` | Starts a new session. |
| `/resume [path or id]` | Resumes a session. Without an argument it opens the session browser. |
| `/sessions` | Browses the current project's sessions to resume, rename, or delete. |
| `/tree` | Browses the active session tree. |
| `/fork [entry id]` | Forks the active session. |
| `/help` | Shows built-in and extension command help. |
| `/quit`, `/exit` | Ends the session. |

`/model` selects the model for subsequent turns and records the choice in the session runtime profile. `/theme` selects a theme, and `/settings` changes session-only values: auto retry, tool output expansion, thinking block visibility, and inline images. None of the session settings are written to `~/.smidja/settings.json`; persisted defaults live there as described in the [settings documentation](settings.md).

Resuming reconstructs model history and replays the saved transcript into the viewport before the first new turn.

The tree browser is read-only. It shows branches, fold and unfold, labels, and the default, no-tools, user-only, labeled-only, and all filters, and it can append a label to the selected entry. Selecting an entry does not rewind history: in-file leaf rewind is unsupported because it would make persistence and model history disagree.

`/fork` creates and activates a separate session file containing a validated, remapped prefix of the current branch. Entries with unsupported or untranslatable references are rejected rather than silently dropped.

Deleting from `/sessions` works only for inactive sessions in the current project's session directory, only for regular files that pass the symlink and identity checks, and only after an explicit confirmation. On platforms without the anchored delete transaction the operation reports an error and deletes nothing.

## Queued follow-ups

`alt+enter` queues the editor text as a follow-up instead of sending it. The footer shows the queued count, and the pending list previews each queued message above the editor.

At the end of the running turn, the TUI hands the queued messages to the host mailbox as follow-ups; the mailbox delivers them one at a time. Each delivered follow-up is persisted once and runs its own continuation turn, so queued text is accepted while a turn is still generating and never lands in the middle of a tool sequence. The dequeue binding, `alt+up` by default, restores every queued message into the editor for editing before it is delivered.

The queue belongs to the active session. Starting, resuming, forking, or switching sessions drops queued follow-ups and adds a `dropped N queued follow-up message(s) on session switch` warning. Follow-ups are not delivered after the runner shuts down; the closed host reports the error instead. Key IDs are listed in the [keybindings documentation](keybindings.md).

## Prompt templates

Resolved prompt templates join the slash-command surface. `/prompt` lists the available names as a transcript notice, `/prompt <name> [arguments]` runs a template, and every template name that does not collide with a host or extension command also works as a shorthand.

- `/prompt` and its shorthands appear in `/help`, the command inventory, and editor autocomplete.
- The canonical `/prompt` command is registered before extensions register theirs, so an extension command named `prompt` is reachable as `/prompt2`.
- Names reserved for built-in commands, including the future `/agent`, never become shorthands; explicit `/prompt <name>` still runs them.
- Workspace `.smidja/prompts` files join the workspace trust decision, the same as workspace skills and instructions.

Argument parsing, expansion rules, and the expansion budget are documented in [prompt templates](prompts.md).

## Transcript search

Search opens in fullscreen mode with the `tui.altScreen.search` binding, `ctrl+shift+f` by default, and indexes the visible rendered transcript rather than live scroll state.

- The query and bracketed paste content share a 256-byte budget. Pasted bytes beyond the budget are dropped at a rune boundary, and the paste end marker is detected even when it arrives split across reads. Closing search, replacing the session, and reopening search all reset the paste state.
- The index holds at most 500 matches over at most 1 MiB of rendered source. The status line reports when a limit was reached.
- Adjacent visual lines are joined with one space so a query can match across a word wrap. A match that spans a hard wrap inside a word is not guaranteed to match, and empty lines do not join unrelated text.

The full details and the wrap-boundary limitation are in [transcript search](transcript-search.md).

## Inline images

Inline images are enabled by default when the TUI starts, and the `/settings` dialog can turn them off for the session. Image rendering needs a graphics-capable terminal; detection looks at the environment and, for Kitty, sends a bounded capability probe.

Images are loaded only from relative paths inside the workspace. Absolute paths, URI schemes, `..` segments, symlinks, and non-regular files are rejected, and the file is not read at all while images are disabled. Loading is bounded before decode: 4 MiB per file, 4096 pixels per dimension, 4,000,000 pixels total, and a 32 MiB in-memory cache.

Render support follows the accepted P5 degradation matrix:

| Terminal | Supported | Fallback |
|---|---|---|
| Kitty | PNG, JPEG, GIF. JPEG and GIF are decoded with the standard library and sent as PNG. | WebP, or any failed validation, renders the text placeholder. |
| iTerm2, regular mode | PNG, JPEG, GIF, and bounded WebP passthrough for the terminal to decode. | Unsupported formats render the text placeholder. |
| iTerm2, fullscreen mode | None for inline placement. | Text placeholder. |
| Other terminals | None. | Text placeholder. |

Placements are owned by the renderer and are not embedded in wrapped text. When an image resolves, the renderer reserves rows for the placement and uses a sanitized `[image: alt]` label. When it cannot resolve, the Markdown renderer shows `[image: alt] (source)`, or `[image: source]` when the alt text is empty, with every part sanitized to one line.

## Mermaid and math

Fenced `mermaid` blocks render as themed Unicode box art for a bounded subset: flowcharts with `graph TD|TB|LR|RL|BT`, `[]`, `()`, and `{}` node shapes plus labelled arrows, and a minimal `sequenceDiagram`. Acyclic diagrams only. Budgets are 8192 source bytes, 24 nodes, 40 edges, 12 participants, 40 messages, and 40-rune labels.

Inline `$...$` and display `$$...$$` math render a bounded LaTeX subset: fractions, sub and superscripts, greek letters, and common operators. Budgets are 4096 source bytes, nesting depth 8, and 8192 output bytes.

Anything outside a subset or over budget keeps its complete original source and adds a warning line, `mermaid: <reason>` or `math: unsupported or over-budget expression; showing source`. Smidja never renders a plausible but wrong diagram or formula.

Fenced code blocks are highlighted by a stdlib-only lexer for Go, JavaScript and TypeScript, JSON, YAML, Bash, Python, Markdown, and diff, using the nine syntax tokens.

## Workspace trust

The TUI asks for trust per run, before the first turn, and only when it matters:

- The working directory or a parent up to the workspace root has an `AGENTS.md`.
- The workspace has content under `.smidja/skills`, `.smidja/agents`, or `.smidja/prompts`.
- `--allow-workspace-mcp` is set and the workspace `.smidja/mcp.json` defines enabled servers.

The prompt asks to trust the workspace and says trusted workspaces may load workspace instructions and run extensions. Declining continues the run without workspace instructions and without workspace-defined MCP servers. A terminal EOF, process signal, or canceled context during the prompt ends startup. Smidja does not persist trust; the next run asks again when the condition still holds.

Trust does not enable workspace MCP. Workspace-defined MCP servers spawn only when `--allow-workspace-mcp` is set and trust is accepted; without the flag smidja skips them and says so on stderr. User servers in `~/.smidja/mcp.json` are not workspace-defined and are not affected by this gate.

## Startup sign-in

When the TUI starts with a provider that supports OAuth, and no credential is found in the environment or `~/.smidja/auth.json`, smidja opens a sign-in dialog before the first turn. The dialog shows the verification URL and user code for device flows, accepts a pasted authorization code through masked input, and can be canceled with `Esc` or `ctrl+c`. The login has a five-minute deadline. Success stores the credential in `~/.smidja/auth.json`; cancel, timeout, and failure store nothing and end startup. Credentials never render in the dialog frames.

The `openrouter` provider name resolves through the API-key client and does not trigger the OAuth startup flow; use `openrouter-oauth` for the stored OAuth credential. See the [auth documentation](auth.md) for provider names and credential precedence.

## Extension UI

The P7 surface attaches the extension UI registry to the runner when the TUI starts. Extensions registered in the same run can then add message, entry, and Markdown renderers, component widgets, header and footer components, a custom editor, autocomplete providers, and terminal input hooks through the optional `sdk.ExtendedUI` and `sdk.UIRegistrationAPI` interfaces.

- Component render, input, and dispose callbacks run outside the host surface lock, so they may call back into the UI. Frames are sanitized before display.
- A recovered callback panic is reported once as an `extensions: ...` warning notice, and the host keeps running with a fallback or the previous component.
- Replacing or clearing a component disposes the old one exactly once. Runner stop clears every slot, restores the built-in editor and footer, disposes owned components, and releases the registry.
- Registration succeeds in every mode, but only an attached TUI surface renders it, so print mode and non-TTY sessions accept registrations into an inert registry.
- The line interface does not implement the extended surface. Extensions check `HasUI()` and assert to `sdk.ExtendedUI` before using it.

The full contract is in the [extension UI SDK documentation](sdk-ui.md).

## Platform support

The terminal framework implements raw mode, size handling, and resize for Linux and Darwin, behind build tags. Release builds are static `CGO_ENABLED=0` binaries for Linux and Darwin on amd64 and arm64, with zero module dependencies and no `go.sum`. On other platforms the TUI fails closed: TTY detection reports false, so smidja stays on the line interface, and any direct raw-mode attempt returns a clear unsupported-platform error instead of panicking.

## What remains

P0-P7 source and isolated installed acceptance are complete. Fourteen SDK actions are backed in the composed CLI/TUI host: `SetActiveTools`, `AppendEntry`, `SetSessionName`, `LabelEntry`, `Exec`, `SendMessage`, `SendUserMessage`, `SetModel`, `SetThinkingLevel`, `RegisterProvider`, `RemoveProvider`, `RegisterFlag`, `Flags`, and `EmitCustomEvent`. Agent content execution (R4) and final installation of the broader runtime head remain. Real-provider acceptance requires user-configured Smidja credentials and is not claimed. Extension keybinding registration stays outside the contract, and the 27 deferred Pi events stay with their runtime waves. The [SDK runtime](sdk-runtime.md) page documents the composed host rules, and the [SDK parity matrix](sdk-parity-matrix.md) records each row.
