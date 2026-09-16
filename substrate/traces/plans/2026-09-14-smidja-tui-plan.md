---
document_type: mycelium-plan
plan_id: 2026-09-14-smidja-tui-plan
status: in-progress
created_at: 2026-09-14
planner: planner
baseline_version: 1
execution_owner: orchestrator
execution_started_at: 2026-09-15T00:31:24+02:00
last_updated_at: 2026-09-16
---

# Smidja TUI implementation plan

## Current execution snapshot

- **Status:** In progress.
- **Baseline identity:** `2026-09-14-smidja-tui-plan`, baseline version 1, planner handoff date 2026-09-14.
- **Execution baseline:** Resumed 2026-09-16T08:25:38+02:00 at repository commit `19be845a27d963b320e73334a30008eae7eb623c` on `feat/tui`, matching `origin/feat/tui`, with the preserved unfinished P2 work and living-plan update recorded in the ignored workspace-state snapshot.
- **Active phase:** Publish the verified post-P2 wiring commit and install the first testable local build.
- **Last verified checkpoint:** Checkpoint 2026-09-16T20:53:24+02:00, minimal end-to-end wiring independently verified with quality and focused security verdicts PASS.
- **Last successful checks:** The wiring slice passed formatting, vet, build, all delta-package tests, four Darwin and Linux amd64 and arm64 static cross-builds, dependency hygiene, sequential race tests, and repeated real-PTY smoke, panic, Ctrl-D, and Ctrl-G tests. Package coverage is 90.3% for `internal/tui`, 96.5% for `internal/tui/interactive`, 97.2% for `internal/ui`, and 86.9% for `internal/cli`. The only full-suite failures are the verified pre-existing `internal/mcp.TestListToolsRetryOnceAfterRestart` and `internal/session.TestListNewestFirst` flakes outside the delta.
- **Open blockers:** None.
- **Required approvals and gates:** Deterministic validation and delegated quality judgment after each executable phase; focused security review for foundational terminal, input, filesystem, hook, and public SDK slices; push and pull request readback after each phase; local install and smoke test after minimal P2 wiring and again in P6; no merge.
- **Next action:** Amend the wiring commit with this execution evidence, push `feat/tui`, read back pull request 1, install `v0.3.0-tui.1` at `/home/luca/.local/bin/smidja`, and verify its identity and PTY behavior before P3.

## Planner baseline

### Problem statement

- **Planner prediction:** Give smidja a real interactive TUI that reproduces the Pi interactive experience as closely as a zero-dependency Go implementation allows, wired into the existing harness, committed phase by phase on a feature branch, with a pull request open against `alpha` and a locally installed binary the user can test.
- **Planner prediction:** Work in `/home/luca/Documents/github-digitalygo/smidja`, clone of `digitalygo/smidja`, default branch `alpha`. Branch `feat/tui` is based on `origin/alpha` at commit `81740f5c2b3b9988f19852ab924d4a704cae2b38`. The pull request targets `alpha`. Nothing is merged.
- **Planner prediction:** Current interactive mode is a line UI: `internal/ui/ui.go` (`LineUI`) driven by `internal/cli/chat.go` (`repl`), reading one line per prompt from stdin and streaming the turn to `d.Stdout` through a `trailingWriter`. The agent loop renders through `agent.LoopDeps{Stdout io.Writer, OnThinking func(string)}` and emits structured tool hooks through `agent.HookDispatcher` (`ToolCall`, `ToolResult`, `MessageEnd`, `AutoRetryStart`, `AutoRetryEnd`, `SessionStart`, `SessionShutdown`).
- **Planner prediction:** `docs/sdk-parity-matrix.md` defers the whole TUI surface to the TUI phase. This plan makes that deferred surface real in phases P0 to P6, with the extension-facing remainder isolated in sacrificable phase P7.
- **Planner prediction:** `go.mod` declares `module github.com/digitalygo/smidja` with `go 1.26`, zero `require` directives, and no `go.sum`. The zero-dependency posture must hold for the entire plan.
- **Planner prediction:** Reference implementation is the public MIT Pi project, cloned outside the smidja worktree (for example `/tmp/pi-reference`), never committed, never placed inside the worktree, never covered by an ignore rule. Port behaviour and structure with original Go code. Do not translate TypeScript line by line, do not copy comments, do not port Node or native pieces.

### Research and evidence

- **Planner prediction:** Source for this baseline is the user task spec at `/home/luca/artifacts/smidja-tui-spec.md`, read in full. It defines the deliverable, working directory, hard constraints, target architecture, phases P0 to P7 in strict order, validation commands, deliverable and stop rule, and explicit out of scope list. Every phase below preserves that order and those constraints.
- **Planner prediction:** Repository rules at `AGENTS.md`, read in full: delta tests for every behaviour change with 80 to 100 percent coverage on added or modified executable lines reported per file; production files normally 500 to 700 lines with cohesion judged before any split; cohesive tests may approach 1000 lines and must never be thinned; no comments in Go code; conventional commits; sentence case in docs; never an em dash.
- **Planner prediction:** Parity map at `docs/sdk-parity-matrix.md`, read in full: 102 capabilities total, 54 implemented, 48 deferred. Deferred TUI rows relevant here are `registerMessageRenderer`, `registerMarkdownTransformer`, `registerEntryRenderer`, `registerShortcut`, `onTerminalInput`, working indicator and thinking label setters, footer and header setters, custom components, editor accessors and autocomplete providers, theme enumeration and selection, plus image content in `sendUserMessage`. P0 to P6 build the interactive surface; P7 promotes only the remaining deferred UI rows listed in the spec.
- **Planner prediction:** `internal/ui/ui.go`, read in full: `LineUI` implements `sdk.UI` with `Notify`, `Confirm`, `Select`, `Input`, `Editor`, `SetStatus`, `SetWidget`, `SetWorkingMessage`, `SetTitle`. Print mode returns `sdk.ErrModeUnsupported` for blocking dialogs and is a no-op for fire-and-forget methods. `Editor` shells to `$EDITOR` and reports the built-in multiline editor as deferred to the TUI phase. `LineUI` stays for print mode, non-TTY interactive mode, and existing tests.
- **Planner prediction:** `internal/cli/chat.go`, read in full: `runChat` builds config, workspace tools, session store, extension catalogs, runtime, hooks, model registry, context preparer, then `runOnce` for `-p` prompt mode or `repl` for interactive mode. `repl` uses `lineUI.Input(">", "")`, handles `/quit` and `/exit`, dispatches slash commands, and calls `runTurn` with `loopDeps(d, out)`. Any TUI selection must slot into this function without changing print mode behaviour.
- **Planner prediction:** `sdk/ui.go`, read in full: `UI` interface with the nine methods above plus `Mode` values `interactive` and `print` and sentinel `ErrModeUnsupported`. Existing signatures must stay unchanged through P6; P7 keeps every existing method signature unchanged while adding new surface.
- **Planner prediction:** `internal/agent/loop.go` header and hook call sites, inspected: `LoopDeps` carries `Client`, `Tools`, `Catalog`, `Recorder`, `Stdout`, `OnThinking`, `Preparer`, `Hooks`, `Detector`, retry policy and callbacks. `runTurn` consults hooks for context mutation, message end replacement, tool call deny gate, tool result patching, and auto retry events. The TUI adapter consumes these existing seams; it does not redesign the loop.
- **Planner prediction:** `internal/cli/root.go` `Deps` shape, inspected: `Getwd`, `Home`, `Stdin`, `Stdout`, `Stderr`, `Bundle`, `Env`, with nil-safe defaults to `os.Getwd`, `os.UserHomeDir`, and the process stdio. TUI TTY detection must use the real file descriptors of stdin and stdout and must never treat an injected test double as a terminal.
- **Planner prediction:** `scripts/build-release.sh`, inspected: cross compiles `CGO_ENABLED=0` static binaries for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64` with `-trimpath` and `-ldflags` injecting `main.version`, `buildinfo` origin, version, and commit. P6 local install follows this ldflags shape with version `v0.3.0-tui.1` into `/home/luca/.local/bin/smidja`.
- **Planner prediction:** Session store surface (`internal/session/session.go`, `open.go`), inspected: `NewStore`, `Create`, `Open`, `AppendUser`, `AppendAssistant`, `AppendToolResult`, `AppendEntry`, `List`, `DirForCwd`. P4 builds a tree navigator over this existing store without changing codec or store semantics.
- **Planner prediction:** No `substrate/directives/` and no `substrate/expectations/` exist in this repository. Verified by listing both paths. There are no DRC or EXP files to comply with beyond shared standards.
- **Planner prediction:** Existing traces read: `substrate/traces/plans/2026-08-24-smidja-harness-plan.md` (living harness plan, used as schema reference for ledger structure) and `substrate/traces/operations/` (operation record shape referenced by P6). No prior TUI plan exists.
- **Planner prediction:** Branch and tree state verified read-only: current branch `feat/tui`, HEAD `81740f5c2b3b9988f19852ab924d4a704cae2b38`, worktree paths `internal/ui/` and `internal/cli/` present, `internal/tui/` absent. Go toolchain for execution is `go1.27.1` at `/home/linuxbrew/.linuxbrew/bin/go`, exported on PATH in every shell used.
- **Planner prediction:** Solution architect input is absent. Delegation was attempted and unavailable due to provider usage limit. This baseline therefore records planner judgment only, stays close to the spec text, and flags architectural choices as needing orchestrator review rather than presenting them as architect approved.

### Hypotheses, decisions, and rationale

- **Planner prediction:** Phase sequence P0 to P7 is strict priority order because each layer is a dependency of the next. Framework first (terminal, renderer, input, theme, width, components, layout), then editor and input, then the chat surface that composes them, then dialogs and selectors, then sessions over the existing store, then fullscreen extras that assume the surface exists, then wiring, docs, and install, with the extension-facing SDK surface last and explicitly sacrificable.
- **Planner prediction:** P7 is last and conditional on P0 to P6 being complete and green because it touches `sdk/`, which is frozen through P6. If the run cannot reach P7, the plan stops earlier and leaves those rows deferred. This protects the public contract from partial SDK churn.
- **Planner prediction:** `LineUI` is never removed because print mode and every non-TTY path must stay behaviourally identical, existing tests must stay green without weakening, skipping, or deletion, and injected `Deps` stdio must keep working.
- **Planner prediction:** Suggested package layout (`internal/tui/` framework, `internal/tui/interactive/` surface, `internal/ui/tui.go` adapter, `internal/cli/` selection) is accepted as the default. Deviations are allowed only with a reason stated in the pull request.
- **Planner prediction:** Zero dependencies, per-OS raw mode behind build tags, and Windows failing closed are treated as release blocking constraints, because the static release matrix and the stdlib only check gate every phase.
- **Planner prediction:** Commit and push after each finished phase, with the pull request opened after P0, because partial progress must always be preserved and visible while work continues. A phase is finished or reverted, never left half implemented on the branch.
- **Planner prediction:** No-edit boundaries through P6 (`sdk/`, `internal/gateway/`, `internal/packages/`, `internal/providers/`, `internal/update/`, `internal/session/` codec and store semantics, `scripts/`, `.github/`, `brew/`, `bench/`, `go.mod`, `go.sum`) protect the gateway, package system, providers, session format, updater, release pipeline, and dependency posture from incidental churn. They are readable at all times.
- **Planner prediction:** No live machine state is touched (no chezmoi, no `~/.pi`, no dotfiles repository, no brew, no PATH edits, no writes under `~/.smidja` outside what the built binary does at user runtime). Tests point home and session directories at temporary directories through existing injection (`Deps.Home`, `Deps.Getwd`, `SMIDJA_HOME` style overrides used by config and session stores).

### Planned phases

Global lifecycle and gates applying to every phase:

- **Planner prediction:** Work in strict P0 to P7 order. Finish a phase or revert it. Never leave a half-implemented phase on the branch.
- **Planner prediction:** Run the full validation block before every commit and again before the pull request, using real output in the pull request body. Required commands:
- **Planner prediction:** Per-file coverage between 80 and 100 percent on changed executable lines, or explicit N/A with justification for non-executable changes.
- **Planner prediction:** Deterministic headless frame tests plus one real PTY smoke test as defined in the validation section of the spec, in addition to the command block.
- **Planner prediction:** One conventional commit per phase, push after each phase, pull request opened after P0 against `alpha`, never committed onto `alpha`, never merged.
- **Planner prediction:** No-edit boundaries and zero-dependency posture hold for P0 to P6 and are checked by the stdlib only and `go.mod` diff commands in every gate.

```bash
export PATH="/home/linuxbrew/.linuxbrew/bin:$PATH"
cd /home/luca/Documents/github-digitalygo/smidja
gofmt -l .
go vet ./...
go build ./...
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o /tmp/smidja-darwin-arm64 ./cmd/smidja
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o /tmp/smidja-linux-arm64 ./cmd/smidja
go test ./...
go test -cover ./internal/tui/... ./internal/ui/... ./internal/cli/...
go list -deps ./... | grep -v '^github.com/digitalygo/smidja'
test ! -e go.sum && echo "no go.sum"
git diff origin/alpha --name-only
git diff origin/alpha -- go.mod | wc -l
```

#### Phase P0: framework core

- **Objective:** Build the terminal framework everything else composes: raw mode, resize, capability queries, split renderers, input decoding, keybindings, theming, width handling, components, layout, scroll view, and overlays.
- **Planner predictions:** Likely files are new files under `internal/tui/` such as `terminal_linux.go`, `terminal_darwin.go`, `ansi.go`, `width.go`, `keys.go`, `keybindings.go`, `component.go`, `render_main.go`, `render_alt.go`, `layout.go`, `scrollview.go`, `overlay.go`, `theme.go` with `theme_dark.json` and `theme_light.json`, plus one file per component group. No edits to `sdk/`, `internal/gateway/`, `internal/packages/`, `internal/providers/`, `internal/update/`, session codec or store semantics, `scripts/`, `.github/`, `brew/`, `bench/`, `go.mod`, `go.sum`. Risks are raw mode restore failures on panic or signal, capability queries hanging on unresponsive terminals, differential rendering artifacts, incomplete key sequence coverage, theme fallback gaps, and width table gaps for wide or zero-width runes. Constraints are stdlib only, per-OS raw mode and size behind build tags with Windows failing closed with a clear error, and AGENTS.md file length, coverage, and no-comment rules.
- **Proposed steps:**
  1. Implement raw mode with correct restore on exit, error, panic, and signal, plus `SIGWINCH` resize handling and a goroutine read loop that tolerates partial escape sequences and reports EOF cleanly.
  2. Implement bounded-timeout capability queries with safe defaults: primary device attributes, cursor position report, terminal background.
  3. Implement the renderer split: main-screen differential renderer preserving scrollback, alt-screen fixed viewport renderer with synchronized output, final document print on stop, per-frame line batching, throttled re-render, per-line trailing reset.
  4. Implement input decoding: legacy sequences, bracketed paste, Kitty keyboard protocol, xterm `modifyOtherKeys`, SGR mouse, focus events, OSC 52 clipboard writes, OSC 8 hyperlinks, OSC 133 prompt markers.
  5. Implement keybindings with Pi namespaced action ids verbatim (`tui.editor.*`, `tui.input.*`, `tui.select.*`, `tui.altScreen.*`, `app.*`), defaults from Pi `docs/keybindings.md`, overridden by `~/.smidja/keybindings.json`, one key or an array of keys per action.
  6. Implement theming in Pi JSON format with all 51 required tokens and documented fallbacks, hex, 256-index, variable and default values, built-in dark and light themes, `~/.smidja/themes/*.json` plus package-provided themes following existing content precedence, hot reload of the active custom theme.
  7. Implement ANSI-aware visible width, truncation that re-closes styles, wrapping that keeps styles per line, and compact wide and zero-width rune tables covering CJK, Hangul, fullwidth forms, combining marks, and ZWJ emoji without vendoring a Unicode database.
  8. Implement components (text, truncated text, spacer, border and dynamic border, loader and cancellable loader, select list, settings list, input), stacks with `basis`, `grow`, `shrink`, `minSize`, `maxSize`, and `visible` predicate, scroll view with follow, primary-region flag, and scroll chaining, and overlays with anchor, percentage, or absolute placement, margins, size clamps, `visible` predicate, and focus capture.
  9. Add headless frame tests for the framework pieces and run the full gate, then commit and push, then open the pull request against `alpha`.
- **Predicted verification:** `gofmt -l .` empty, `go vet ./...` clean, `go build ./...` succeeds, both cross builds succeed, `go test ./...` passes, `go test -cover` over the new framework plus `internal/ui` and `internal/cli`, stdlib only dependency list, no `go.sum`, `go.mod` diff empty, plus headless frame assertions covering alt-screen enter and exit at framework level and a `gh pr view --json state,files,commits` readback after opening the pull request. Coverage 80 to 100 percent on changed executable lines reported per file.
- **Completion criterion:** Framework merges as one conventional commit on `feat/tui`, pushed, pull request open against `alpha`, all gates green, terminal state restore and bounded capability defaults demonstrated by tests.

#### Phase P1: editor and input

- **Objective:** Deliver the multiline editor, prompt history, clipboard, paste, submit semantics, autocomplete, external editor, bash mode prefix, thinking-level border colours, and IME cursor marker.
- **Planner predictions:** Likely files are new or extended files under `internal/tui/` for the buffer, history store, clipboard, autocomplete, and input component, with minimal selection plumbing later consumed by `internal/cli/`. No `sdk/` changes. Risks are cursor visibility bugs across wrapped lines, history persistence location regressions, clipboard silent no-op mishandling, large-paste performance, and autocomplete matching against workspace paths outside containment. Constraints are no writes under `~/.smidja` except through existing precedence and injection in tests, and no live machine state changes.
- **Proposed steps:**
  1. Implement the multiline buffer with a cursor that stays visible across wrapping and hard crop of overlong lines.
  2. Implement movement (character, word, line start and end, home and end, page, vertical across wrapped lines, jump to character) and deletion (character forward and backward, word forward and backward, to line start, to line end).
  3. Implement undo stack, kill ring with yank and yank pop.
  4. Implement per-project prompt history persisted under `~/.smidja`, browsable with dedicated history actions and with up and down inside a multiline buffer, using temporary home directories in tests.
  5. Implement selection with OSC 52 copy where supported and silent no-op otherwise, with `app.clear` copying when a selection exists and clearing otherwise.
  6. Implement bracketed paste as one undo step with large-paste markers for very large pastes.
  7. Implement submit semantics: enter submits, shift+enter and ctrl+j insert newline, alt+enter queues a follow-up message, alt+up restores queued messages into the editor.
  8. Implement autocomplete for slash commands at buffer start plus the command catalog and `@` file paths resolved against the workspace, with fuzzy matching, tab accept, arrow navigation, escape dismiss, and inline hints.
  9. Implement external editor via `app.editor.external`, bash mode `!` prefix with its border colour, thinking-level border colours, and IME cursor marker.
  10. Add delta tests including editor wrapping frames and run the full gate, then commit and push.
- **Predicted verification:** Same command block as the global gate plus headless frame assertions for editor wrapping and autocomplete interactions. Coverage 80 to 100 percent on changed executable lines reported per file.
- **Completion criterion:** Editor behaviours above work headlessly and in the smoke path, committed as one conventional commit on `feat/tui` and pushed, all gates green.

#### Phase P2: chat surface

- **Objective:** Compose the transcript, message blocks, tool blocks, diff, footer, status, and markdown rendering, including extension-set statuses and widgets.
- **Planner predictions:** Likely files are new files under `internal/tui/interactive/` for transcript, message blocks, tool blocks, diff, footer, status, plus markdown rendering reusing the P5 lexer for code highlighting. Layout order is transcript scroll view as primary region, then editor, then status and working indicator, then footer. Risks are streaming markdown reflow, tool output truncation accounting, diff token mapping errors, and footer or status regressions when extensions set values through `sdk.UI`. Constraints are `LineUI` retention, print mode parity, and no `sdk/` signature changes.
- **Proposed steps:**
  1. Implement layout with transcript scroll view as primary region followed by editor, status and working indicator, and footer.
  2. Implement blocks: user message background, assistant message with live markdown during streaming, collapsed thinking block with thinking colours, tool execution block with pending, success, and error backgrounds plus expand and collapse via `app.tools.expand`, truncated tool output with remaining-lines counter, diff block for `edit` and `write` using diff tokens, bash execution block, subagent block through `internal/subagent`, skill invocation notice, compaction summary with usage, retry countdown, notices and errors.
  3. Implement markdown: headings, bold, italic, strikethrough, inline code, fenced code blocks with language label and P5 highlighting, ordered, unordered, and nested lists, task list checkboxes, blockquote, horizontal rule, links as OSC 8 hyperlinks using `mdLink` and `mdLinkUrl`, simple aligned tables.
  4. Implement status line and footer: model, thinking level, working indicator with elapsed time, workspace and session, token usage and cost when session data provides them, keybinding hints, queued message count, plus extension-set statuses and widgets.
  5. Add delta tests including a streaming assistant turn frame and a tool block with diff frame, run the full gate, then commit and push.
- **Predicted verification:** Same command block plus frame assertions for a streaming assistant turn and a tool block with diff. Coverage 80 to 100 percent on changed executable lines reported per file.
- **Completion criterion:** Chat surface renders the specified blocks and markdown in headless frames, committed as one conventional commit on `feat/tui` and pushed, all gates green.

#### Phase P3: dialogs and selectors

- **Objective:** Implement `sdk.UI` on the TUI for interactive TTY mode and build the selector surfaces, keeping print mode behaviour exactly as today.
- **Planner predictions:** Likely files are the adapter at `internal/ui/tui.go` plus dialog and selector components under `internal/tui/` and `internal/tui/interactive/`. Print mode keeps returning `sdk.ErrModeUnsupported` exactly as today for `Confirm`, `Select`, `Input`, and `Editor`. Risks are focus capture conflicts with overlays, masked input leaks for secrets, and OAuth device flow polling state handling. Constraints are no `sdk/` signature changes and no weakening of existing `internal/ui` tests.
- **Proposed steps:**
  1. Implement `sdk.UI` on the TUI for interactive TTY mode: `Notify`, `Confirm`, `Select`, `Input` with placeholder and masked input for secrets, `Editor`, `SetStatus`, `SetWidget`, `SetWorkingMessage`, `SetTitle`.
  2. Implement selectors: model with search showing provider and context window, thinking level, theme, settings, session resume, project trust, OAuth device flow login showing URL and code with polling state, plus command help.
  3. Add delta tests including a dialog frame assertion, run the full gate, then commit and push.
- **Predicted verification:** Same command block plus dialog frame assertion and print mode regression tests proving `ErrModeUnsupported` parity. Coverage 80 to 100 percent on changed executable lines reported per file.
- **Completion criterion:** Interactive TTY dialogs work through the TUI adapter with print mode unchanged, committed as one conventional commit on `feat/tui` and pushed, all gates green.

#### Phase P4: sessions

- **Objective:** Add the session tree navigator, session commands, and resume replay over the existing session store without changing codec or store semantics.
- **Planner predictions:** Likely files are session navigator and command handling under `internal/tui/interactive/` with read-only use of `internal/session/` store APIs. No changes to session codec or store semantics. Risks are branch fold state confusion, filter misapplication, and replay ordering bugs before the first new turn. Constraints are the session no-edit boundary and existing import compatibility tests staying green.
- **Proposed steps:**
  1. Implement the tree navigator with branches, fold and unfold, labels with optional timestamps, filters (default, no tools, user only, labelled only, all), and copy of the selected message.
  2. Implement commands `/new`, `/tree`, `/fork`, `/resume`, `/sessions`, rename, and delete.
  3. Implement resume replay of the existing session transcript into the viewport before the first new turn.
  4. Add delta tests including a session resume replay frame, run the full gate, then commit and push.
- **Predicted verification:** Same command block plus resume replay frame assertion and session store regression tests. Coverage 80 to 100 percent on changed executable lines reported per file.
- **Completion criterion:** Tree navigation, commands, and replay work against the existing store with codec untouched, committed as one conventional commit on `feat/tui` and pushed, all gates green.

#### Phase P5: fullscreen extras

- **Objective:** Add transcript search, prompt jumping, mouse handling, link clicks, drag selection, scrollbar, inline images, mermaid box art, math subset, and syntax highlighting.
- **Planner predictions:** Likely files are search, mouse, image, mermaid, math, and lexer modules under `internal/tui/` and `internal/tui/interactive/`. Risks are mouse routing errors across chained scroll regions, image protocol fallback mistakes, incorrect mermaid rendering presented as correct, and math fallback gaps. Constraints are honest degradation (unsupported diagrams keep the original code block plus a warning line; images fall back to a text placeholder with a disable setting), and stdlib only lexer for go, javascript and typescript, json, yaml, bash, python, markdown, and diff using the nine syntax tokens.
- **Proposed steps:**
  1. Implement transcript search with search token highlighting, next, previous, and close.
  2. Implement OSC 133 prompt jumping, mouse wheel routing to the region under the pointer with chaining, OSC 8 link click through the platform handler with silent failure, drag selection with edge auto-scroll and clipboard copy, and scrollbar thumb.
  3. Implement inline images for local png, jpg, gif, and webp via Kitty graphics protocol and iTerm2 inline image protocol after capability detection, with text placeholder fallback and a disable setting.
  4. Implement bounded mermaid subset (flowcharts `graph TD|TB|LR|RL|BT` with `[]`, `()`, `{}` node shapes and labelled arrows, plus minimal `sequenceDiagram`) as themed Unicode box art honouring available width, with unsupported diagrams preserved plus a warning line.
  5. Implement bounded LaTeX subset (fractions, sub and superscripts, greek letters, common operators) with raw fallback.
  6. Implement stdlib syntax highlighting for the specified languages using the nine syntax tokens.
  7. Add delta tests, run the full gate, then commit and push.
- **Predicted verification:** Same command block plus frame assertions for search highlighting and representative extras fallbacks. Coverage 80 to 100 percent on changed executable lines reported per file.
- **Completion criterion:** Extras work with honest fallbacks and bounded subsets only, committed as one conventional commit on `feat/tui` and pushed, all gates green.

#### Phase P6: wiring, docs, install

- **Objective:** Select the TUI on real TTYs, add flags and settings plumbing, update docs and the parity matrix, record the operation trace, install the local binary, and smoke test it.
- **Planner predictions:** Likely files are `internal/cli/` TTY detection and selection, flag and settings plumbing, `internal/ui/tui.go` final adapter wiring, `README.md` status section, new `docs/tui.md`, `docs/themes.md`, `docs/keybindings.md`, updated `docs/sdk-parity-matrix.md`, operation trace at `substrate/traces/operations/<YYYY-MM-DD>-smidja-tui-implementation.md`, and the installed binary at `/home/luca/.local/bin/smidja`. Risks are TTY misdetection (especially treating test doubles as terminals), flag or settings precedence mistakes, and parity matrix drift. Constraints are ioctl TTY detection on real stdin and stdout file descriptors only, no existing flag changes with usage text updated, `theme` and `tuiMode` settings with documented precedence, no-edit boundaries still holding, and no live environment changes beyond the described local binary install.
- **Proposed steps:**
  1. Implement TTY detection through ioctl on the real file descriptors of stdin and stdout so a terminal gets the TUI and anything else keeps `LineUI`.
  2. Add flags `--tui-mode regular|fullscreen` defaulting to `regular` and `--use-theme name[/name]` without changing existing flags, and update usage text.
  3. Add settings `theme` and `tuiMode` with documented precedence plus `~/.smidja/themes` and `~/.smidja/keybindings.json` handling.
  4. Write the README status section, `docs/tui.md`, `docs/themes.md`, `docs/keybindings.md`, and update `docs/sdk-parity-matrix.md` dispositions for the UI surface that is no longer deferred.
  5. Write the operation trace mirroring the structure of existing traces in `substrate/traces/operations/`, listing phases landed, validation evidence, and what remains deferred.
  6. Build with the release ldflags shape from `scripts/build-release.sh` at version `v0.3.0-tui.1` into `/home/luca/.local/bin/smidja`, confirm `smidja -version` reports the injected identity, and run the real PTY smoke test (send a prompt and an exit key sequence, assert alt-screen enter and exit order and terminal restore).
  7. Report that `smidja update` would replace this local build with the released binary and that the user must not run it before testing.
  8. Run the full gate including `git status` clean, branch pushed, and `gh pr view` readback, then commit and push.
- **Predicted verification:** Same command block plus per-file coverage, deterministic frame suite green, PTY smoke assertions on the ANSI stream and terminal restore, `git status` clean, branch pushed, pull request readback, and `smidja -version` identity confirmation. Docs and non-executable changes marked N/A with justification where applicable.
- **Completion criterion:** Real TTYs enter the TUI while all other paths keep `LineUI`, docs and matrix updated, operation trace written, local binary installed and smoke tested, committed as one conventional commit on `feat/tui` and pushed, all gates green.

#### Phase P7: extension-facing UI surface

- **Objective:** Promote the remaining deferred UI rows of `docs/sdk-parity-matrix.md` into `sdk/` only when P0 to P6 are complete and green, otherwise stop earlier and leave them deferred.
- **Planner predictions:** Likely files are `sdk/` additions plus docs and parity matrix updates and new tests. Explicitly sacrificable. Risks are public API churn and breaking existing `sdk` consumers. Constraints are every existing `sdk` method signature unchanged, new surface documented and tested, parity matrix updated, and full gate green.
- **Proposed steps:**
  1. Confirm P0 to P6 complete and green; otherwise stop and leave this phase deferred with the reason recorded.
  2. Add Go-idiomatic equivalents of Pi custom components, message, markdown, and entry renderers, an editor component factory, autocomplete providers, a terminal input hook, footer and header, paste-to-editor and editor text accessors, and theme enumeration and selection.
  3. Document the new surface, test it, update the parity matrix, run the full gate, then commit and push.
- **Predicted verification:** Same command block plus new surface tests. Coverage 80 to 100 percent on changed executable lines reported per file.
- **Completion criterion:** Either landed as one conventional commit on `feat/tui` and pushed with all gates green and signatures preserved, or explicitly deferred with the reason recorded in the pull request and operation trace.

## Execution ledger

### Ledger rules

- **Planner prediction:** Checkpoints are append-only and recorded only for material events: hypothesis validation or rejection, scope or decision changes, verified phase completions, failed checks, blockers, handoffs, resumptions, gate outcomes, and closure.
- **Planner prediction:** Every checkpoint distinguishes planner prediction, subagent claim, orchestrator finding, and independently verified fact.
- **Planner prediction:** A phase stays open until its required independent checks pass. A subagent statement, diff, or unverified command result alone cannot close a phase.
- **Planner prediction:** Variations go in the plan-variation ledger without rewriting the baseline.
- **Planner prediction:** Only the orchestrator writes the ledger after execution starts. Subagents return evidence and never update the plan.

### Phase P0 execution checkpoints

#### Checkpoint 2026-09-15T00:31:24+02:00: baseline validation and execution start

- **Event:** Execution started with P0 as the active phase.
- **Planner prediction:** The branch is `feat/tui` at the `origin/alpha` baseline, the worktree has no pre-existing tracked changes, `internal/tui/` does not exist, and P0 must finish and pass its gates before P1 begins.
- **Subagent claims:** Four locator delegations found no DRC or EXP files, located the relevant traces and code seams, and one retry completed the codebase inventory. Analyzer and pattern-finder delegations mapped the phase dependency graph, no-edit boundaries, existing test patterns, and Pi behavior. The plan author reported a schema-compliant baseline. The solution architect returned no proposal because its provider usage limit was exhausted.
- **Orchestrator finding:** Direct inspection confirmed the plan schema, repository `AGENTS.md`, `internal/ui/ui.go`, `internal/cli/chat.go`, `docs/sdk-parity-matrix.md`, the relevant agent ports, the complete upstream TUI and interactive source set at `/tmp/pi-reference`, and the existing harness plan. The new plan is the only worktree path and remains outside executable review scope.
- **Independently verified facts:** `git status --short --branch` showed `feat/tui...origin/alpha` with only the new untracked plan; `git rev-parse HEAD`, `git rev-parse origin/alpha`, and `git merge-base HEAD origin/alpha` all resolved to `81740f5c2b3b9988f19852ab924d4a704cae2b38`; ahead and behind counts were both zero; the Pi reference is outside the worktree at commit `53816d7dcc5ebe3a0eedec3cd07196c3a66d83fd`; no `go.sum` exists and `go.mod` has no `require` block.
- **Decision and impact:** The planner baseline is frozen. Execution follows the user's strict P0 to P7 sequence. The missing architect proposal is recorded as a process limitation, not substituted with an unverified claim. The quality cursor starts at commit `81740f5c2b3b9988f19852ab924d4a704cae2b38`.
- **Next action:** Create P0 race worktrees from the baseline and delegate the same complete framework task to backend developers A, B, and C.

#### Checkpoint 2026-09-15T02:06:39+02:00: P0 race verified and blocked at required adjudication

- **Event:** The P0 implementation race produced two verification survivors, but required winner adjudication could not run.
- **Planner prediction:** P0 finishes only after one complete framework implementation passes deterministic checks, independent race adjudication, quality judgment, focused security review, commit, push, and pull request creation.
- **Subagent claims:** Backend developer B reported a complete 42-file `internal/tui/` implementation with 85.5% statement coverage. Backend developers A and C returned provider errors, but both left complete-looking worktrees. The solution architect returned a provider usage-limit error for race adjudication.
- **Orchestrator finding:** Candidate A was eliminated because independently measured statement coverage was 72.3%, below the repository's 80% floor. Candidates B and C were committed locally as `31ea9d3` and `e7ce7f6`. Both remain isolated from `feat/tui`. Candidate B is 8,943 inserted lines across 42 files; candidate C is 14,749 inserted lines across 43 files. No implementation file has been merged, pushed, or presented as complete.
- **Independently verified facts:** In both surviving worktrees, `gofmt -l internal/tui` was empty; `go vet ./...`, `go build ./...`, Linux arm64 static cross-build, Darwin arm64 static cross-build, and `go test -count=1 -coverprofile=... ./internal/tui/...` passed. Candidate B coverage was 85.5%; candidate C coverage was 83.9%. Full-suite runs reached unrelated pre-existing timing failures in `internal/gateway/telegram.TestBackoffGrowthCapped` for B and `internal/session.TestListNewestFirst` for C; reruns are required before any phase gate. Both candidates changed only `internal/tui/`; `go.mod` is unchanged and `go.sum` is absent. The architect call failed with `The usage limit has been reached` on both the initial proposal and the adjudication attempt.
- **Decision and impact:** P0 is blocked before winner selection. The orchestrator did not bypass the mandated architect by choosing a candidate directly. P1 through P7, pull request creation, push, and local installation have not started. The two survivor commits remain as local race branches for resumption.
- **Next action:** Retry solution-architect race adjudication when quota is available, then continue the normal verification and correction loop.

#### Checkpoint 2026-09-15T10:15:40+02:00: execution resumed from landed P0

- **Event:** The continuation run independently reconstructed repository state and resumed with P0 debt closure as the active work.
- **Planner prediction:** Candidate selection, merge, push, and pull request creation were still blocked, while P0 debt and all later phases were not started.
- **Subagent claims:** Locator and analyzer delegations reported that candidate C is now the checked-out branch head, P0 debt items for production comments and the primary device attributes query are already satisfied, and the remaining implementation debt is the `component.go` split plus coverage evidence. A solution architect request returned immediately with a provider usage-limit error.
- **Orchestrator finding:** The continuation brief supersedes the stale blocked snapshot by recording that Hermes selected candidate C, now landed at `e7ce7f6`; the open pull request already points from `feat/tui` to `alpha`. The five apparent production comments are three `go:build` and two `go:embed` compiler directives and cannot be removed without breaking builds. `QueryDeviceAttributes` already uses the shared bounded query path and has answering and timeout tests.
- **Independently verified facts:** `git branch --show-current` returned `feat/tui`; `git rev-parse HEAD` returned `e7ce7f6f8342f89fc01151dbff048d2cced3de8e`; ahead and behind counts against `origin/feat/tui` were both zero; `gh pr view 1` returned open pull request `https://github.com/digitalygo/smidja/pull/1` against `alpha` with the same head SHA; `component.go` is 1,257 lines; repository searches found only required compiler directives, the primary device attributes implementation, and both required tests.
- **Decision and impact:** Accept the user-authorized continuation state and process variation V-001. No new race will run. Implementation remains delegated to a single available backend implementer, and unavailable architect review is recorded rather than claimed. Deterministic gates, delegated quality judgment, focused security review, phase order, zero dependencies, no-edit boundaries, and no-merge rules remain unchanged. Chezmoi was not run because the task explicitly prohibits it as live machine state.
- **Next action:** Split `component.go` by responsibility without API or behavior changes, add targeted tests for thin paths, report per-file coverage, and complete the P0 debt gate.

#### Checkpoint 2026-09-15T11:26:55+02:00: P0 debt closure verified

- **Event:** The P0 debt slice is complete and passed its required deterministic, quality, and focused security gates.
- **Planner prediction:** The debt commit would split the oversized component core without behavior or API change, remove prohibited comments, provide the missing primary device attributes query and tests, and raise and report per-file coverage.
- **Subagent claims:** The implementer split `component.go` into focused core, container/input routing, overlay, and compositing files; added meaningful edge tests; preserved required compiler directives; confirmed the device attributes implementation already existed; and reported touched production coverage above 92%. The quality reviewer returned `# PASS`; the focused security reviewer returned exact verdict `PASS` after confirming a pure move with no trust-boundary change.
- **Orchestrator finding:** Direct source and diff inspection confirmed the split preserved declarations and signatures, added no production comments, changed no query behavior, and touched only the intended framework, tests, `.gitignore`, and living plan. The ignored workspace-state record remains local and untracked. The new tests exercise ANSI helpers, Base scheduling and invalidation, container and input routing, overlay state, geometry, mouse routing, stack rendering, and device-attributes parser edges without weakening existing tests.
- **Independently verified facts:** `gofmt -l .`, `go vet ./...`, `go build ./...`, Darwin arm64 static build, Linux arm64 static build, targeted TUI/UI/CLI tests, dependency hygiene, no-`go.sum`, zero `go.mod` diff, and `git diff --check` all passed. A subsequent `go test ./...` passed every package after an initial unrelated `internal/mcp.TestListToolsRetryOnceAfterRestart` failure passed in isolation; the implementer reproduced that intermittent failure on the untouched baseline. Statement-weighted file coverage was `component.go` 94.8%, `container.go` 99.3%, `overlay.go` 96.9%, and `composite.go` 92.5%; additionally tested unchanged files were `ansi.go` 100.0%, `components_stack.go` 95.7%, and `terminal.go` 88.3%, with `QueryDeviceAttributes` and its parser at 100.0%. Package coverage was TUI 88.3%, UI 94.0%, and CLI 85.3%.
- **Decision and impact:** Advance the quality cursor from `e7ce7f6` after commit. Compiler directives remain because deleting them would break platform selection and embedded themes; the requirement is satisfied as zero ordinary production comments. The pre-existing MCP flake and test-double race are out-of-scope signals and do not block the behavior-preserving debt slice.
- **Next action:** Commit and push `refactor(tui): split component core and close P0 gaps`, read back pull request 1, then begin phase P1.

#### Checkpoint 2026-09-15T11:29:18+02:00: P0 debt commit published

- **Event:** The independently gated P0 debt closure was committed, pushed, and read back from the existing pull request.
- **Planner prediction:** The debt closure would be a separate conventional commit before phase P1 and the existing pull request would update automatically.
- **Subagent claims:** None.
- **Orchestrator finding:** The commit contains only the intended P0 debt, tests, workspace-state ignore rule, and living plan. The ignored status record was not committed.
- **Independently verified facts:** Commit `d6dba305458f238c532a153a8b3837544bc3923a` has subject `refactor(tui): split component core and close P0 gaps`; `git push origin feat/tui` succeeded; ahead and behind counts are zero; `gh pr view 1` returned open pull request `https://github.com/digitalygo/smidja/pull/1`, head `d6dba30`, two commits, and target `alpha`; the working tree is clean.
- **Decision and impact:** P0 debt is closed and phase P1 may start from the synchronized commit. The quality cursor advances to `d6dba30`.
- **Next action:** Implement and gate phase P1 editor and input behavior.

### Phase P1 execution checkpoints

#### Checkpoint 2026-09-15T12:10:09+02:00: phase P1 verified

- **Event:** Phase P1 editor and input implementation is complete and passed deterministic, quality, and focused security gates.
- **Planner prediction:** P1 would add multiline editing, complete movement and deletion, undo and kill-ring behavior, project history, selection and OSC 52 copy, bracketed and large paste handling, submit and queue semantics, slash and workspace autocomplete, external editor support, bash and thinking borders, and an IME cursor marker.
- **Subagent claims:** The implementer added focused editor, buffer, editing, input, rendering, mouse, autocomplete, history, and external-editor modules with behavior-mapped tests. The first quality review identified invisible selection, empty submit behavior, and lexical-only symlink containment; corrections were implemented and the quality reviewer returned PASS. The focused security review found terminal-control injection through malicious workspace filenames; the correction rejects C0, DEL, and C1 names from default and injected listers, and the reviewer returned PASS.
- **Orchestrator finding:** Direct inspection confirmed P1 is isolated to 10 production and 11 test files under `internal/tui/`, uses only the standard library and existing framework helpers, adds no production comments, keeps production files within the repository's contextual length guidance, and does not alter print or non-TTY paths. Tests cover wrapped cursor frames, movement, deletion, undo, kill-ring operations, persisted history, visible selection and OSC 52 copy, paste markers, submit and queue actions, autocomplete and containment, external editor injection, bash and thinking borders, IME markers, and malicious filename rejection.
- **Independently verified facts:** `gofmt -l .`, `go vet ./...`, `go build ./...`, Darwin arm64 static build, Linux arm64 static build, `go test ./...`, coverage targets, dependency hygiene, no-`go.sum`, zero `go.mod` diff, and `git diff --check` all passed. Package coverage was TUI 88.9%, UI 94.0%, CLI 85.3%. Statement-weighted P1 file coverage was `autocomplete.go` 95.8%, `editor.go` 91.2%, `editor_autocomplete.go` 82.6%, `editor_buffer.go` 95.0%, `editor_edit.go` 90.9%, `editor_input.go` 85.5%, `editor_mouse.go` 84.8%, `editor_render.go` 85.5%, `external_editor.go` 89.8%, and `history.go` 95.3%. Final quality verdict was `# PASS`; final focused security verdict was `PASS`.
- **Decision and impact:** P1 is accepted. Invalid or control-bearing workspace filenames are omitted from autocomplete rather than sanitized into unusable paths. Selection is rendered with width-neutral inverse-video spans. Empty submissions are no-ops. The quality cursor advances after the phase commit.
- **Next action:** Commit and push `feat(tui): add editor and input`, read back pull request 1, then begin phase P2.

#### Checkpoint 2026-09-15T12:11:52+02:00: phase P1 commit published

- **Event:** The independently gated P1 implementation was committed, pushed, and read back from pull request 1.
- **Planner prediction:** P1 would land as one conventional commit and the existing pull request would update before P2 began.
- **Subagent claims:** None.
- **Orchestrator finding:** The commit contains exactly the P1 production files, behavior-mapped tests, and living-plan update. The working tree was clean after commit.
- **Independently verified facts:** Commit `19be845a27d963b320e73334a30008eae7eb623c` has subject `feat(tui): add editor and input`; push succeeded; branch ahead and behind counts are zero; pull request 1 is open against `alpha`, has head `19be845`, and contains three commits.
- **Decision and impact:** The quality cursor advances to `19be845`; phase P2 may begin.
- **Next action:** Implement and gate phase P2 chat surface.

### Phase P2 execution checkpoints

#### Checkpoint 2026-09-15T17:56:31+02:00: interrupted P2 implementation resumed

- **Event:** Execution resumed on the preserved unfinished P2 worktree without switching, stashing, resetting, or discarding files.
- **Planner prediction:** P2 would add the interactive chat surface, behavior-mapped frame tests, and a per-file coverage report before one conventional commit and push.
- **Subagent claims:** Locator, analyzer, and pattern-finder delegations found that P0, the P0 debt correction, and P1 are published through `19be845`; P2 consists of 11 untracked production files and one scratch-only test file under `internal/tui/interactive/`; no DRC or EXP files exist; the surface is not wired into production; and the current tests log frames without assertions.
- **Orchestrator finding:** Direct inspection confirmed the full P2 source set, its unasserted scratch tests, the modified living plan, the absent directives and expectations directories, and the preserved zero-dependency boundaries. The P2 implementation covers most specified components but needs correctness fixes, behavior-mapped assertions, coverage work, and independent validation before it can be accepted.
- **Independently verified facts:** `feat/tui` and `origin/feat/tui` both resolve to `19be845a27d963b320e73334a30008eae7eb623c`; the branch is based on `origin/alpha` at `81740f5`; pull request 1 is OPEN against `alpha` with head `19be845` and three commits; `/tmp/pi-reference` exists outside the worktree; `go.mod` and `go.sum` have no working-tree delta.
- **Decision and impact:** Preserve and complete the existing P2 implementation with one delegated implementer. Keep minimal production wiring as the immediate post-P2 slice required by variation V-001, then return to P3 in strict order.
- **Next action:** Complete P2 correctness and tests, run deterministic gates, obtain the quality verdict, commit, push, and read back pull request 1.

#### Checkpoint 2026-09-16T08:13:29+02:00: recovery blocked by unavailable subagents

- **Event:** The recovery run rechecked the interrupted P2 work and stopped before code changes because no compliant implementation path is available.
- **Planner prediction:** P2 would be completed by a delegated backend implementer, independently inspected, deterministically validated, and submitted to a delegated quality judgment before commit and push.
- **Subagent claims:** None. The user reports that the prior Codex process was OOM-killed at 22.4 GB immediately after dispatching `backend-dev-a` and explicitly states that the subagent tool is disabled for this recovery run.
- **Orchestrator finding:** The available tool surface contains no subagent delegation capability. The active orchestrator role prohibits implementing or correcting code directly and requires delegated quality judgment, so the user's requested direct-implementation deviation cannot be performed without violating the binding role boundary.
- **Independently verified facts:** `feat/tui`, `HEAD`, and `origin/feat/tui` resolve to `19be845a27d963b320e73334a30008eae7eb623c`; pull request 1 remains OPEN against `alpha` at `https://github.com/digitalygo/smidja/pull/1` with three commits; the untracked P2 directory still contains 12 files; only the living plan is a tracked modification; `go.mod` and `go.sum` have no working-tree delta. The reported OOM event and memory value could not be independently verified in this session.
- **Decision and impact:** Preserve all unfinished P2 work and record variation V-003. Do not edit code, commit an incomplete phase, push, install a binary that still lacks real TUI wiring, or claim quality evidence that could not run. P2 through P7 remain open.
- **Next action:** Restore subagent availability, then delegate completion of the preserved P2 implementation and run the full P2 gates before any phase commit.

#### Checkpoint 2026-09-16T08:25:38+02:00: recovery baseline validated and delegation restored

- **Event:** Execution resumed from the preserved P2 worktree after delegation capability became available again.
- **Planner prediction:** Resume must preserve the 12 untracked P2 files and tracked plan update, reconstruct branch and pull request state, and restore delegated implementation and quality review before changing executable files.
- **Subagent claims:** Four locator delegations found no DRC or EXP files, identified the living plan and workspace-state records, and mapped the P2 through P7 code and test seams. The codebase locator confirmed that P2 remains unwired and its three scratch tests only log frames.
- **Orchestrator finding:** Direct inspection confirmed every uncommitted P2 file, the full living-plan diff, the synchronized `feat/tui` branch, open pull request 1, zero dependency changes, and the required no-edit boundaries. The subagent tool is available in this session, so the earlier process blocker no longer applies.
- **Independently verified facts:** `HEAD` and `origin/feat/tui` both resolve to `19be845a27d963b320e73334a30008eae7eb623c`; ahead and behind counts are zero; pull request 1 is open against `alpha` with the same remote SHA and three commits; `git status` shows only the tracked plan update and untracked `internal/tui/interactive/`; the ignored status record `substrate/traces/status/2026-09-16-smidja-tui-p2-recovery-workspace-state.md` captures this baseline.
- **Decision and impact:** Resume P2 with the existing files as the implementation base. Run analyzer and architecture passes before delegating one implementer, then verify and gate the phase before commit and push. No source is discarded or replaced wholesale.
- **Next action:** Complete analysis and architecture review, then delegate P2 correctness, behavior tests, and coverage.

#### Checkpoint 2026-09-16T08:43:10+02:00: P2 architecture and defect scope validated

- **Event:** Read-only analysis and the required solution architecture pass completed before implementation resumed.
- **Planner prediction:** P2 would remain a reusable headless chat surface, with integration deferred to the authorized immediate wiring slice.
- **Subagent claims:** The analyzer identified parser non-termination on headings, unsynchronized tree and cache mutation, one-shot animation timers, missing bash render and cancel behavior, diff and width defects, incomplete sanitization, Markdown defects, absent usage and submit seams, and unasserted tests. The solution architect proposed one serialized UI owner, runtime-owned deterministic animation and cleanup, incremental repair of the preserved P2 source, and a distinct post-P2 wiring commit using existing internal agent streams and hook decorators without changing `sdk/`.
- **Orchestrator finding:** The reported heading bug is directly visible because `parseHeading` returns without advancing `p.pos`. The recovered scratch streaming test includes a heading, so this defect can prevent the test from completing. The architecture preserves the P2 headless boundary while addressing the concurrency hazards that would become active immediately after wiring.
- **Independently verified facts:** All analyzed P2 files remain unmodified from the recorded recovery baseline; no production importer of `internal/tui/interactive` exists; `sdk/`, protected packages, `go.mod`, and `go.sum` remain unchanged.
- **Decision and impact:** Adopt the architecture proposal. Complete P2 incrementally rather than replacing it. Add the smallest framework scheduling and callback changes needed for one serialized UI owner, deterministic animation, and safe shutdown. Keep real CLI, agent, and terminal integration in the separate wiring slice already authorized by V-001. A single implementer will work in the canonical dirty worktree because the unfinished files are not committed and must remain the preserved base.
- **Next action:** Delegate P2 completion, then inspect every changed file and run the P2 deterministic and review gates.

#### Checkpoint 2026-09-16T09:31:49+02:00: P2 chat surface completed and gated

- **Event:** P2 implementation, correction loop, deterministic verification, quality judgment, and focused security review completed successfully.
- **Planner prediction:** The phase would add the chat surface under `internal/tui/interactive/`, asserted streaming and diff frames, zero dependencies, and no CLI wiring or public SDK changes.
- **Subagent claims:** The implementer repaired the preserved source incrementally, added a serialized runtime, deterministic animation and close handling, corrected Markdown, diff, width, sanitization, footer, status, widget, tool, bash, subagent, skill, compaction, submit, usage, cancel, and expansion behavior, and replaced log-only scratch checks with behavior assertions. The first quality and security passes returned PASS with hyperlink and status advisories; the focused correction closed hyperlinks, fixed overlapping status animation, sanitized custom frames, and made key clearing symmetric. The repeated quality and focused security reviews returned PASS.
- **Orchestrator finding:** Direct source and diff inspection confirmed the promised implementation, tests, protected-path compliance, explicit OSC 8 closure, visible labelled-link URLs, parser progress, bounded frames, runtime-owned animation, and no production import of the interactive package. The original scratch scenarios remain as asserted surface tests rather than log-only checks.
- **Independently verified facts:** Review artifact `P2-cb0c0bcf0391` is the sorted file-content manifest with SHA-256 `cb0c0bcf0391bfe616f3b3d21a729845badf1dbe63d5524d0753dbfc28ecb8bb`. `gofmt -l .`, `go vet ./...`, `go build ./...`, `go test ./...`, four static builds for Darwin and Linux on amd64 and arm64, `go test -race ./internal/tui/interactive`, focused framework race tests, dependency checks, `git diff --check`, and the protected-path checks passed. Package coverage is 96.3% for `internal/tui/interactive` and 88.9% for `internal/tui`; changed production-file coverage ranges from 80.9% to 100.0%, and changed `TruncateToWidth` is 90.8%. The first targeted TUI run hit the known asynchronous `TestAltScreenMouseEventDispatch` signal-order failure; the test then passed 20 of 20 isolated runs and the full targeted suite passed on rerun. Full framework race testing still exposes pre-existing P0 test-local races outside this delta.
- **Decision and impact:** Mark P2 complete and advance the quality cursor after publication. Accept the delegated quality verdict `PASS` and focused security verdict `PASS` on the same artifact. Keep CLI and agent integration in the separately gated wiring slice. Residual non-blocking risks are unbounded long-session transcript storage and weaker control-rune filtering in the framework loader, neither of which has a current untrusted P2 production caller.
- **Next action:** Create the conventional P2 commit, push `feat/tui`, verify pull request 1, then begin minimal end-to-end wiring and the first local install.

#### Checkpoint 2026-09-16T09:33:07+02:00: P2 published

- **Event:** The complete P2 phase was committed and pushed to the existing feature branch and pull request.
- **Planner prediction:** Each completed phase receives one conventional commit, is pushed immediately, and is verified through pull request readback without merge.
- **Subagent claims:** None.
- **Orchestrator finding:** The P2 commit contains the verified chat surface, framework runtime and corrections, tests, and living-plan evidence only. Protected paths and public SDK files are absent from the commit.
- **Independently verified facts:** Commit `78de304a9f2556d191c38895d178cf856e713efa` has subject `feat(tui): add interactive chat surface`. Local `HEAD`, `origin/feat/tui`, and pull request 1 head all resolve to that SHA; ahead and behind counts are zero. Pull request 1 remains open from `feat/tui` to `alpha` with four commits. The worktree was clean immediately after publication.
- **Decision and impact:** Advance the quality cursor to `78de304a9f2556d191c38895d178cf856e713efa` and begin the distinct wiring slice authorized by V-001 and V-004. No second pull request and no merge are needed.
- **Next action:** Implement, gate, publish, and locally install the minimal end-to-end TUI wiring before P3.

#### Checkpoint 2026-09-16T20:53:24+02:00: minimal end-to-end wiring completed and gated

- **Event:** The authorized post-P2 wiring slice completed after a three-candidate backend race, correction passes, final deterministic verification, delegated quality judgment, and focused security review.
- **Planner prediction:** A distinct wiring commit would activate the P2 surface only for real interactive terminals, preserve `LineUI` elsewhere, add `--tui-mode regular|fullscreen`, produce a real PTY smoke test, and support the first local install without public SDK or protected-path changes.
- **Subagent claims:** Candidate B won the race on correctness after candidate C was eliminated by a turn-state panic and candidate A lost on message boundaries and lifecycle behavior. The solution architect identified and rechecked terminal ownership, worker ordering, assistant finalization, external editor, input-delivery, render, navigation, PTY, cross-platform, panic, and persistence defects through iterative correction. Its final verdict was `WINNER READY`. The delegated quality verdict is `PASS`; the focused security verdict is `PASS` with only non-blocking residual advisories.
- **Orchestrator finding:** Direct source and diff inspection confirmed real-file ioctl TTY selection, exact non-TTY and print fallback, one FIFO worker, hook forwarding with visual-only tool deduplication, authoritative message and usage reconciliation, orderly panic/cancel/signal/editor shutdown, generation-bound input delivery, frame-bound fullscreen navigation, final document flushing, and no ordinary code comments or protected-path changes. The winner was amended into one conventional wiring commit and fast-forwarded into `feat/tui`; all three race worktrees and branches were removed afterward.
- **Independently verified facts:** Review artifact `P2W-aab6a3100bd7` is the sorted 80-file manifest with SHA-256 `aab6a3100bd704428de1751d1b85c99f68d9fa9b186436972f4f9c2a4dfd522c`. `gofmt -l .`, `go vet ./...`, `go build ./...`, `git diff --check`, zero dependency checks, no `go.sum`, zero `go.mod` change, and four static builds for Darwin and Linux on amd64 and arm64 passed. `go test -race -p 1 ./internal/tui/... ./internal/ui ./internal/cli` passed. Repeated real PTY smoke, panic restoration, Ctrl-D, and Ctrl-G tests passed under race. Coverage is 90.3% for TUI, 96.5% for interactive, 97.2% for UI, and 86.9% for CLI. Repeated `go test ./...` runs reached only the known unrelated MCP restart and session timestamp-order flakes; all delta packages pass. The quality and security reviewers both returned PASS on this artifact.
- **Decision and impact:** Accept the race winner and complete the wiring slice. The existing `sdk/` contract remains unchanged, P3 dialogs remain unsupported rather than simulated, and every non-TTY path still uses `LineUI`. Advance the quality cursor after publication. Residual non-blocking items are long terminal ownership files, environment-gated PTY skips, pre-existing OSC background-color parsing risk in an unused path, and user-invoked extension commands that do not yet receive the P3 TUI context.
- **Next action:** Publish the single wiring commit, verify the existing pull request and remote SHA, then build and install the committed `v0.3.0-tui.1` binary and run the installed-binary smoke check.

### Phase P3 execution checkpoints

No checkpoints yet.

### Phase P4 execution checkpoints

No checkpoints yet.

### Phase P5 execution checkpoints

No checkpoints yet.

### Phase P6 execution checkpoints

No checkpoints yet.

### Phase P7 execution checkpoints

No checkpoints yet.

## Plan-variation ledger

### Variation V-001: resume from Hermes-selected P0 without another race

- **Baseline reference:** The P0 checkpoint required solution architect race adjudication before winner selection and left candidate commits `31ea9d3` and `e7ce7f6` isolated.
- **Discovered evidence:** The continuation brief states that Hermes independently selected candidate C, P0 landed on `feat/tui` at `e7ce7f6`, and pull request 1 is open. Direct repository and pull request inspection confirmed the branch, remote, commit, and pull request state. A new solution architect proposal attempt returned `The usage limit has been reached`.
- **Decision:** Accept `e7ce7f6` as the immutable P0 base. Do not start another candidate race and do not wait for unavailable quota. Use one delegated implementer per coherent phase and preserve every deterministic and review gate that is available.
- **Scope and downstream impact:** P0 debt is completed first, then P1 through P7 remain in strict order. Minimal end-to-end wiring and a truthful local install move immediately after P2 for testability; complete wiring, documentation, and final installation remain in P6. The missing architect review is a recorded process limitation, not review evidence.
- **Approval:** Explicit user approval in `/home/luca/artifacts/smidja-tui-continue.md` and the continuation request received 2026-09-15.
- **Resolution:** Checkpoint 2026-09-15T10:15:40+02:00 resumed execution with P0 debt closure active.

### Variation V-002: mandatory harness sync despite task-local machine-state constraint

- **Baseline reference:** The task specification prohibited running chezmoi or changing dotfile state during this implementation.
- **Discovered evidence:** The active orchestrator role requires `chezmoi update --force` before repository work and treats that workflow requirement as non-overridable. The command completed successfully before code delegation and reported the source repository already up to date, followed by local package installation output.
- **Decision:** Record the conflict and the command outcome rather than conceal it. Make no further dotfile, Homebrew, PATH, or live configuration changes; retain the task-authorized local binary installation as the only later machine-state write.
- **Scope and downstream impact:** Repository implementation scope is unchanged. The sync touched harness-managed machine state outside the repository before the implementation resumed.
- **Approval:** No user approval was obtained for this conflict; execution was required by the higher-priority active role workflow.
- **Resolution:** The command exited zero at session start. No repair or follow-up state mutation was needed.

### Variation V-003: recovery run without subagent capability

- **Baseline reference:** Variation V-001 and the latest P2 checkpoint require one delegated implementer per phase, deterministic verification, and delegated quality judgment before a phase can be accepted.
- **Discovered evidence:** The user reports that the preceding Codex run was OOM-killed at 22.4 GB immediately after dispatching `backend-dev-a` and disabled subagents for this recovery. Independent inspection confirms that this session exposes no subagent delegation tool.
- **Decision:** Reject direct code implementation because the active orchestrator role makes delegation a binding boundary even when a user requests otherwise. Record the process deviation and block rather than modify P2 without a compliant implementer or claim an unavailable review.
- **Scope and downstream impact:** No executable file is changed by this recovery run. P2 remains unfinished, minimal end-to-end wiring and local installation do not occur, and P3 through P7 remain deferred until P2 is complete and green.
- **Approval:** The user explicitly approved a no-subagent recovery method, but that approval cannot override the active orchestrator role boundary.
- **Resolution:** Checkpoint 2026-09-16T08:13:29+02:00 records the preserved state and exact restart action.

### Variation V-004: P2 serialized ownership and separate wiring slice

- **Baseline reference:** The P2 baseline predicted changes under `internal/tui/interactive/` only, followed by P3. Variation V-001 authorized minimal end-to-end wiring and a local install immediately after P2.
- **Discovered evidence:** Read-only analysis found that the existing render scheduler can read component trees while P2 producers mutate them, P2 cache invalidation is not consistently synchronized, animation timers fire once without teardown, and the heading parser does not advance. The solution architect confirmed that these faults must be fixed before wiring and proposed one serialized UI owner plus a distinct P2 wiring slice.
- **Decision:** Permit the smallest necessary P2 framework scheduling and callback edits under `internal/tui/` so component mutation, invalidation, input callbacks, animation, rendering, and shutdown have one owner. Keep CLI, agent-hook, and process-terminal integration in a separate complete wiring commit after P2 and before P3.
- **Scope and downstream impact:** P2 may touch `internal/tui/` in addition to the preserved interactive package and tests. The wiring slice may touch `internal/cli/` and `internal/ui/`, must preserve `LineUI` and every non-TTY path, and must produce the first testable local binary. No protected path or public SDK surface changes before P7.
- **Approval:** No new product behavior or requirement is introduced. The framework correction is necessary to meet P2 safety and the separate wiring slice was explicitly approved in V-001.
- **Resolution:** Checkpoint 2026-09-16T08:43:10+02:00 adopts the architecture before implementation.

## Closure evidence

### Final outcome

Not started.

### Quality and security evidence

Not started.

### Operation record

Not started.
