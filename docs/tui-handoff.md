# TUI implementation handoff

## Purpose

This document is the restart point for the unfinished Smidja TUI work. It records the published state, the preserved P5 work, the remaining phases, the validation history, and the decisions that should not be rediscovered in a later session.

The implementation is paused because the Codex subscription quota was exhausted. No Smidja Pi process is running. The hourly monitor is paused. Resume only when a working Pi provider is available and Luca asks to continue.

## Authoritative state

- Repository: `digitalygo/smidja`
- Local clone: `/home/luca/Documents/github-digitalygo/smidja`
- Base branch: `alpha`
- Working branch: `feat/tui`
- Pull request: [digitalygo/smidja#1](https://github.com/digitalygo/smidja/pull/1)
- Published head: `44ec39ffc645b30e417551d98b4ea2f31da2e703`
- Local `feat/tui`, `origin/feat/tui`, and the pull request head match that commit.
- The pull request is open and mergeable, with merge state `BLOCKED`. It has no published CI checks.
- Published delta from `origin/alpha`: 251 files, 76,342 additions, 64 deletions.
- `go.mod` still contains no requirements. `go.sum` does not exist.
- Nothing has been merged, tagged, released, or published as a package.

The canonical living plan is [`substrate/traces/plans/2026-09-14-smidja-tui-plan.md`](../substrate/traces/plans/2026-09-14-smidja-tui-plan.md). It contains the original P0-P7 baseline, execution checkpoints through P5 architecture, and variations V-001 through V-007. The earlier recovery brief at `/home/luca/artifacts/smidja-tui-recovery-2026-09-17-1921.md` is machine-local. This document carries the information needed to resume without depending on that file.

## Published work

### P0: framework core

Commit `e7ce7f6f8342f89fc01151dbff048d2cced3de8e`, `feat(tui): implement P0 framework core`.

P0 added the stdlib-only terminal framework under `internal/tui/`:

- Linux and Darwin raw terminal handling, terminal size, resize, bounded capability queries, cleanup, and unsupported-platform fallback
- main-screen differential rendering and alternate-screen rendering with synchronized output
- legacy, Kitty, `modifyOtherKeys`, bracketed-paste, mouse, focus, and terminal protocol input decoding
- keybindings, built-in dark and light themes, custom theme loading, hot reload, width handling, components, stacks, scroll views, and overlays

Three candidates were produced. Candidate C was selected because it covered `SIGWINCH`, stdin buffering, hot reload, scrollbar geometry, OSC 133 zones, and a real PTY round trip. The first mandatory adjudicator had no quota, so Hermes performed the independent selection and recorded that process deviation.

### P0 debt correction

Commit `d6dba305458f238c532a153a8b3837544bc3923a`, `refactor(tui): split component core and close P0 gaps`.

This commit split the original 1,257-line component core, filled thin test paths, and completed the bounded primary device attributes query. Apparent source comments were required `go:build` and `go:embed` directives, not ordinary code comments.

Verified package coverage at this checkpoint was 88.3% for TUI, 94.0% for UI, and 85.3% for CLI. Statement-weighted coverage for the split component files was between 92.5% and 99.3%.

### P1: editor and input

Commit `19be845a27d963b320e73334a30008eae7eb623c`, `feat(tui): add editor and input`.

P1 added:

- multiline editing, wrapped-cursor movement, deletion, undo, kill ring, yank and yank-pop
- project prompt history, visible selection, OSC 52 copy, bracketed paste, queued follow-ups, and external editor support
- slash-command and workspace path autocomplete with containment and control-character checks
- bash mode, thinking-level borders, and IME cursor positioning

The final quality and focused security verdicts were PASS. Package coverage was 88.9% for TUI, 94.0% for UI, and 85.3% for CLI. Changed production files ranged from 82.6% to 95.8%.

### P2: interactive chat surface

Commit `78de304a9f2556d191c38895d178cf856e713efa`, `feat(tui): add interactive chat surface`.

P2 added the headless coding-agent surface under `internal/tui/interactive/`:

- transcript, user, assistant, thinking, tool, bash, subagent, skill, retry, compaction, notice, error, diff, status, footer, and widget rendering
- streaming Markdown, lists, links, tables, code blocks, truncation, animation, expansion, sanitization, and deterministic frame tests
- serialized UI ownership and shutdown behavior needed before the surface could be connected to a real terminal

Quality and focused security reviews returned PASS. Package coverage was 96.3% for `internal/tui/interactive` and 88.9% for `internal/tui`; changed production files ranged from 80.9% to 100%.

### P2 wiring and first usable binary

Commit `b598cfb68897af9ff18dbb077a50335f8e0ba272`, `feat(tui): wire interactive chat`.

This separate wiring slice connected the TUI to the real chat loop while preserving the old line UI for print mode, non-TTY streams, and injected test doubles. It added:

- real-file TTY detection
- regular and fullscreen startup
- one FIFO worker for input and turns
- hook forwarding, tool deduplication, authoritative assistant reconciliation, error and cancellation handling
- orderly shutdown for signals, panic, EOF, Ctrl-D, Ctrl-G, and external editor suspension
- final document flush and terminal restoration

A real PTY test used a temporary deterministic OpenRouter-compatible fixture. It observed alternate-screen entry, streamed response text, alternate-screen exit, exit code 0, and exact termios restoration.

Coverage at the final wiring gate was 90.3% for TUI, 96.5% for interactive, 97.2% for UI, and 86.9% for CLI. The quality and security verdicts were PASS.

The locally installed binary still comes from this commit:

```text
path: /home/luca/.local/bin/smidja
version: v0.3.0-tui.1
origin: github.com/digitalygo/smidja
commit: b598cfb68897af9ff18dbb077a50335f8e0ba272
```

It is useful for basic TUI testing but does not include P3, P4, or the unfinished P5 work. Running `smidja update` would replace it with the public release.

### P3: dialogs and selectors

Commit `d2f4971817e699fe8ce5ffc25949ce7ce1d83c02`, `feat(tui): add dialogs and selectors`.

P3 added:

- interactive implementations of the existing `sdk.UI` dialogs while preserving `sdk.ErrModeUnsupported` in print and non-TTY modes
- modal lifecycle, focus capture, cancellation, masked host input, extension context binding, and live retheming
- model, theme, settings, session, trust, OAuth, and command-help selectors
- real model and preparer updates instead of footer-only state
- startup and transport seams needed by P4 and P6

The quality and focused security verdicts were PASS. Four static cross-builds and PTY dialog smoke tests passed. Coverage was 91.2% for TUI, 95.3% for interactive, 95.6% for UI, 87.5% for CLI, and 95.5% for extensions.

### P4: sessions

Commit `44ec39ffc645b30e417551d98b4ea2f31da2e703`, `feat(tui): add session navigation and replay`.

P4 added:

- one locked active-session owner with prepare, apply, commit, abort, and cleanup semantics
- reconstruction of model history and visible transcript replay before new input
- `/new`, `/resume`, `/sessions`, `/tree`, `/fork`, rename, labels, filters, and copy
- read-only branch browsing and independent session-file forks with supported reference remapping
- append-only metadata updates
- confirmed deletion of inactive current-project sessions through anchored no-follow Linux and Darwin operations
- entry-ID alignment, context-overflow continuation, and paired tool compaction fixes

P4 deliberately does not rewrite `internal/session/` codec or store semantics. In-file historical leaf rewind remains unsupported because it would make persistence and model history disagree. Forking creates and activates a separate session.

The full suite, race tests across the affected packages, repeated PTY resume smoke, dependency checks, and four static cross-builds passed at the P4 checkpoint. Coverage was 88.9% for CLI, 85.2% for agent, 89.5% for context manager, 90.9% for TUI, 94.2% for interactive, and 94.4% for UI. Quality and focused security verdicts were PASS.

## Preserved P5 work

P5 is not committed. Do not reset, clean, restore, stash, delete, or overwrite either worktree before comparing and consolidating them.

### Candidate A

- Path: `/home/luca/Documents/github-digitalygo/smidja-race-20260917-p5-a`
- Branch: `race/20260917-p5/a`
- Base: `44ec39f`
- State: 18 tracked files modified and 47 untracked files, 65 status entries total
- Tracked delta: 1,484 additions and 159 deletions
- Untracked source and test files: 12,458 lines
- `go.mod` unchanged; `go.sum` absent
- `gofmt -l .` and `git diff --check` were clean at handoff
- Focused packages passed at handoff:

```text
ok github.com/digitalygo/smidja/internal/tui
ok github.com/digitalygo/smidja/internal/tui/interactive
ok github.com/digitalygo/smidja/internal/ui
ok github.com/digitalygo/smidja/internal/cli
```

Candidate A is the advanced recovery source. It includes:

- bounded transcript search, match navigation, highlighting, and search overlay work
- pointer capture, mouse routing, drag selection, clipboard behavior, link activation, and scrollbar work
- Kitty and iTerm2 capability probing, image validation, loading, placement, fallback, and platform link opening
- Markdown image integration
- bounded Mermaid and math rendering with source-preserving fallback
- stdlib syntax lexing and highlighting
- theme propagation and P5-specific concurrency tests
- `docs/transcript-search.md`

The latest files were written on 2026-09-17 between 18:18 and 18:52. The final worker stopped before a full P5 gate and before commit. The last known correction cycle addressed retheme deadlocks and image geometry. The focused package suite is green, but that is not enough to accept P5.

Candidate A files at handoff:

```text
docs/transcript-search.md
internal/cli/tui_selectors.go
internal/tui/component.go
internal/tui/container.go
internal/tui/graphics.go
internal/tui/graphics_probe_test.go
internal/tui/image.go
internal/tui/image_open.go
internal/tui/image_open_unix.go
internal/tui/image_open_unix_test.go
internal/tui/image_open_unsupported.go
internal/tui/image_test.go
internal/tui/image_validate.go
internal/tui/image_validate_test.go
internal/tui/interactive/blocks.go
internal/tui/interactive/blocks_p5_test.go
internal/tui/interactive/lexer.go
internal/tui/interactive/lexer_data.go
internal/tui/interactive/lexer_script.go
internal/tui/interactive/lexer_test.go
internal/tui/interactive/markdown.go
internal/tui/interactive/markdown_image.go
internal/tui/interactive/markdown_inline.go
internal/tui/interactive/markdown_render.go
internal/tui/interactive/math.go
internal/tui/interactive/math_test.go
internal/tui/interactive/mermaid.go
internal/tui/interactive/mermaid_test.go
internal/tui/interactive/p5_image_geometry_blocker3_test.go
internal/tui/interactive/p5_retheme_blocker10_test.go
internal/tui/interactive/surface.go
internal/tui/interactive/transcript.go
internal/tui/layout.go
internal/tui/link.go
internal/tui/link_test.go
internal/tui/overlay.go
internal/tui/overlay_focus_repair_test.go
internal/tui/p5_image_geometry_blocker3_test.go
internal/tui/p5_retheme_blocker10_test.go
internal/tui/placements_p5_test.go
internal/tui/pointer_capture.go
internal/tui/pointer_capture_p5_test.go
internal/tui/render_alt.go
internal/tui/render_alt_p5_test.go
internal/tui/render_alt_transcript.go
internal/tui/render_main.go
internal/tui/render_main_p5_test.go
internal/tui/rich.go
internal/tui/scrollview.go
internal/tui/search.go
internal/tui/search_alt_p5_test.go
internal/tui/search_edge_p5_test.go
internal/tui/search_overlay.go
internal/tui/search_p5_test.go
internal/tui/search_test.go
internal/tui/selection.go
internal/tui/selection_test.go
internal/tui/terminal.go
internal/ui/graphics_probe_state_test.go
internal/ui/selectors.go
internal/ui/tui_p5_retheme_blocker10_test.go
internal/ui/tui_p5_test.go
internal/ui/tui_runner.go
internal/ui/tui_search_p5_test.go
internal/ui/ui_fake_test.go
```

### Candidate B

- Path: `/home/luca/Documents/github-digitalygo/smidja-race-20260917-p5-b`
- Branch: `race/20260917-p5/b`
- Base: `44ec39f`
- State: no tracked modifications, 8 untracked files, 3,246 lines
- `go.sum` absent

Candidate B is incomplete and was not selected as the implementation base, but it contains an independent treatment of capabilities, highlighting, images, math, Mermaid, link opening, search, and selection. Review it for ideas or missing cases before deleting it:

```text
internal/tui/capabilities.go
internal/tui/interactive/highlight.go
internal/tui/interactive/images.go
internal/tui/interactive/math.go
internal/tui/interactive/mermaid.go
internal/tui/linkopener.go
internal/tui/search.go
internal/tui/selection.go
```

## P5 design decisions

Variation V-007 in the living plan is the accepted behavior. Preserve it unless Luca explicitly changes the requirements.

- Search indexes rendered visible content with bounded literal matching. It cannot recover every hard-break versus soft-wrap distinction without a different rendering representation.
- Kitty fullscreen placements are allowed for validated PNG and stdlib-decoded JPEG or GIF data.
- Regular-mode iTerm2 inline transfer may pass bounded WebP to the terminal for decoding.
- WebP on Kitty and iTerm2 fullscreen must use a clear text fallback.
- Image loading stays inside the workspace, is bounded before allocation, and does not read image files when images are disabled.
- Renderers own image placement and deletion. Graphics payloads do not enter wrapped text lines.
- Links open through fixed platform executables, never through a shell.
- Unsupported or over-budget Mermaid and math input preserves the complete original source and adds a warning. It must not render plausible but wrong output.
- Syntax highlighting remains stdlib-only.

## What remains

### Finish and publish P5

Start from Candidate A. Candidate B is read-only comparison material.

- Review all 65 Candidate A status entries and compare the eight Candidate B files for missing behavior.
- Reconstruct the last correction state from the P5 tests and the main plan. Do not trust old worker self-reports without rerunning checks.
- Finish search lifecycle, retheme concurrency, image geometry, capability handling, resource cleanup, parser budgets, and fallback behavior.
- Run focused and race tests for TUI, interactive, UI, and CLI.
- Measure changed-file coverage. Every changed executable path needs 80-100% coverage.
- Run `gofmt`, `go vet`, build, full suite, dependency checks, protected-path checks, and all four static cross-builds.
- Use Candidate A to create one clean conventional P5 commit. Update the living plan, fast-forward `feat/tui`, fetch before push, push, then verify local, remote, and pull request SHA equality.
- Remove P5 worktrees only after the accepted commit is safely published.

### Complete P6

P6 owns final product wiring and documentation:

- finish `--tui-mode regular|fullscreen` and `--use-theme name[/name]`
- add `theme` and `tuiMode` settings with documented precedence
- finish `~/.smidja/themes` and `~/.smidja/keybindings.json` behavior
- connect startup trust and OAuth flows left by P3
- update `README.md`
- add `docs/tui.md`, `docs/themes.md`, and `docs/keybindings.md`
- update `docs/sdk-parity-matrix.md` to match the implemented surface
- write `substrate/traces/operations/<date>-smidja-tui-implementation.md`
- build and install the final `v0.3.0-tui.1` binary from the final committed head
- run real PTY smoke tests for regular and fullscreen modes, prompt submission, streaming, tool rendering, clean exit, and exact terminal restoration
- record that `smidja update` replaces this local test build with the latest public release

Some P6 work already exists in earlier commits, especially TTY detection and `--tui-mode`. Treat P6 as a gap audit against the phase contract, not an instruction to rewrite working code.

### Complete P7

P7 is the extension-facing UI surface. It starts only after P0-P6 are complete and green. Existing SDK method signatures must stay unchanged.

Implement and test Go equivalents of the remaining deferred Pi surfaces:

- custom components
- message, Markdown, and entry renderers
- editor component factory
- autocomplete providers
- terminal input hook
- custom footer and header
- paste-to-editor and editor text accessors
- theme enumeration and selection
- tools-expanded state

Update `docs/sdk-parity-matrix.md` and public SDK documentation. If P7 cannot be completed truthfully, record exactly what remains deferred instead of claiming parity.

### Final independent acceptance

The task is not complete when Pi stops. A separate verification pass must prove:

- `feat/tui`, `origin/feat/tui`, and pull request head are identical
- the worktree is clean
- `go.mod` has no dependency additions and `go.sum` is absent
- `gofmt -l .` is empty
- `go vet ./...`, `go build ./...`, and `go test ./...` pass
- relevant race tests pass
- changed executable lines meet the 80-100% coverage rule
- static `CGO_ENABLED=0` builds pass for Linux amd64, Linux arm64, Darwin amd64, and Darwin arm64
- the final installed binary reports origin, version, and final commit correctly
- regular and fullscreen PTY sessions start, render, exit, and restore terminal state
- the configured model answers a short real request
- a model can be changed through the TUI and a second short request succeeds
- a controlled session can be created, closed, resumed, and replayed
- `/sessions`, `/tree`, `/fork`, dialogs, selectors, transcript search, theme changes, Markdown, streaming, and tool blocks work through the actual installed binary

Use temporary homes, sessions, and workspaces for destructive tests. Do not print credentials. If a live acceptance test fails, preserve the exact failure and send it back through Pi for correction before closure.

## How to resume

1. Run `chezmoi update --force` because the Pi and Digitalygo workflows require a fresh harness state.
2. Confirm no other writer owns the Smidja worktrees.
3. Recheck local, remote, and pull request heads against `44ec39f` and fetch `origin`.
4. Read this handoff, `AGENTS.md`, and the living plan in full.
5. Inspect Candidate A and Candidate B without resetting or cleaning them.
6. Launch Pi from the main Smidja clone with one prompt argument and no other arguments. Tell it to read this handoff and complete P5-P7 on the existing pull request. Do not pass `--print`, `--approve`, provider, model, thinking, tool filters, `@file`, or any other option unless Luca explicitly requests that override for that run.
7. Monitor hourly. A live process is not proof of progress. Check transcript timestamps, worktree changes, CPU, child processes, remote commits, and pull request state. Kill only a demonstrably stuck leaf worker, never the main orchestrator.
8. After Pi finishes, perform the final independent acceptance above. Do not merge the pull request.

The previous hourly monitor job was `c2aff3066c5a`. It is paused. Resume or replace it only when work restarts.

## Known operational lessons

- Broad Pi runs can consume substantial memory when they launch nested workers. One earlier run reached a 22.4 GiB cgroup peak and was killed. Monitor memory and avoid duplicate writers.
- Gateway restarts killed foreground or gateway-owned Pi processes. For long work, use a durable tracked process or a monitor that can reconstruct and restart the run.
- A growing Pi transcript can still be an error loop. Inspect the latest `stopReason` and provider error, not just file size.
- Workers have stalled while holding incomplete work. A worker is considered stuck only after its transcript and worktree stop changing, CPU remains near zero, and no legitimate long command is running. Preserve the worktree and terminate only the leaf process.
- Mandatory reviewer loops found real lifecycle, terminal, security, and concurrency defects. Keep them when quota permits, but do not let an unavailable optional reviewer erase or strand a deterministically verified phase.
- The published branch is large: 76,342 inserted lines across 251 files. Most of that is behavior-mapped test coverage. Review and publication should use immutable file manifests or exact commit SHAs rather than a mutable worktree.

## Current limitations and cautions

- Candidate A passes the focused package suite, but P5 has not passed its final full, race, coverage, quality, security, publication, or installed-binary gates.
- Candidate B is incomplete and untested as a whole.
- The installed binary is older than the published branch and must not be used as evidence for P3 or P4 behavior.
- GitHub reports no CI checks for the pull request. Local validation is the only recorded executable evidence so far.
- The main worktree includes an uncommitted living-plan update describing P4 publication and P5 architecture. Preserve and commit it with the next accepted phase.
- Known historical flakes included `internal/mcp.TestListToolsRetryOnceAfterRestart` and `internal/session.TestListNewestFirst`; later phase gates did pass the complete suite. Reproduce any recurrence on `origin/alpha` before classifying it as pre-existing.
