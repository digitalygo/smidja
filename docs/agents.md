# Agents

An agent definition is a markdown file that gives a child assistant its own identity, instructions, model, tools, and thinking level. The R4 runtime makes definitions executable in two ways: the `/agent` command runs one on demand in an interactive session, and the model-callable `subagent` tool delegates to one from any turn, including print mode. Every run gets an isolated child session, an independent history and context manager, and a tool set that can only shrink relative to the parent.

## Status

Agent execution is implemented on the `feat/tui` branch and included in the installed local test binary: the `/agent` command and the model-callable `subagent` tool run definitions in isolated child sessions. The branch is not merged, and [pull request #1](https://github.com/digitalygo/smidja/pull/1) is open. The runtime adds behavior, not an `sdk.API` method, so the SDK parity counts stay at 49 core and 16 print-mode rows implemented with 37 deferred. Acceptance ran against local fixtures only; live-provider credentials and external creator acceptance are still pending, and this page claims no live-provider result.

## Where definitions come from

Definitions resolve with the other content kinds, highest tier first:

| Tier | Location |
| --- | --- |
| Bundle | `agents` or `content/agents` inside the bundle filesystem |
| Trusted workspace | `<workspace>/.smidja/agents` |
| User | `~/.smidja/agents` |
| Active packages | the root declared for `agents` in `smidja.json`, `agents` by default |
| Core | none |

- A higher tier wins when two tiers define the same name.
- Among active packages, a package activated later wins over an earlier one with the same name.
- The name is the path under the root minus `.md`, so `team/reader.md` is the agent `team/reader`.
- Files follow the shared content rules: only `.md` files are read, each file is at most 100 KiB, content must be valid UTF-8, symlinks are rejected, and no path segment may be empty, `.`, `..`, or start with a dot.
- The workspace tier uses the same trust decision as skills and prompts. The TUI asks per run; the line, print, and `smidja run` paths load workspace content without the dialog.
- An unknown or malformed definition is not executable and not listed. `/agent <name>` reports the parse error instead of running a partial definition.

The [creating content packages](creating-content-packages.md) guide documents the package manifest and its validation rules.

## Definition format

Frontmatter is optional. When the first line is `---`, the block runs to the next `---` line and is parsed as the metadata subset below. When the first line is anything else, the whole file is the body.

```markdown
---
name: Reader
description: Reads files and reports findings
model: anthropic/claude-sonnet-4.5
tools: [read, grep]
thinking: high
---

You are the reader. Read the files the task names and report what matters.
```

### Frontmatter fields

| Field | Type | Behavior |
| --- | --- | --- |
| `name` | string | Display name in listings and progress. Falls back to the path-derived name. Cut to 120 runes. |
| `description` | string | One-line summary for `/agent`. Falls back to the first body line. Cut to 80 runes with control and format characters stripped. |
| `model` | string | Model id for the child. The id must resolve and have a verified wire model for the active transport. The default inherits the parent model. |
| `tools` | list | Parent-active tool allowlist. Comma form (`read, exec`), inline form (`[read, exec]`), or block list. Quoted items are accepted. Unset inherits every parent-active tool. An empty list gives the child no tools. Duplicate and empty items are dropped. |
| `thinking` | string | `off`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max`, or `default`, case-insensitive. An unknown value fails the parse. `default` behaves like unset. |

Parsing rules for the metadata block:

- Lines are `key: value`. Keys use letters, digits, `_`, and `-`. Indented or nested lines are rejected.
- A duplicate key, including a duplicate unknown key, fails the parse.
- Scalar values reject JSON object or list syntax, control characters, and Unicode format characters. Values may be quoted with single or double quotes.
- The body is trimmed and must be non-empty. It becomes the child system prompt, with the parent system prompt appended after it. When the parent has no system prompt, the body stands alone.
- Error text is sanitized and bounded, so a malformed file cannot flood the terminal.

### Unknown metadata

Keys outside the table are tolerated for compatibility with other harness formats. An unknown key with a scalar value or an inline list (`color: blue`, `tags: [a, b]`) is ignored. An unknown key with a mapping (`meta: {a: b}`) or a block list fails with an unsupported type error, and an unknown key without a value fails when a list follows. Unknown values are never read, so they cannot influence a run.

## Running an agent

### The /agent command

In the line interface and the TUI:

- `/agent` lists the resolved names sorted, one per line as `name<TAB>description`, or just the name when the definition has no usable description.
- `/agent <name> <task>` runs the definition. The first token is the name; the rest is the task.
- An unknown name fails with `no agent named "<name>"`, a malformed definition reports its parse error, and a missing task fails with `a task is required`.
- The task is plain text. It is never interpreted as a shell command, never expanded as a prompt template, and never evaluated for environment variables.

The canonical `/agent` command is registered before extensions run `Setup`, the same as `/prompt`. An extension that registers `agent` gets the next free numeric suffix, so its command is reachable as `/agent2` (`/agent3` and so on after that). Prompt templates named `agent` never become shorthand commands because `agent` is a reserved built-in name; explicit `/prompt agent ...` still runs the template.

Print mode (`-p`) and `smidja run` do not dispatch slash commands. `/agent ...` there is literal prompt text sent to the model, exactly like any other text; only `/prompt` invocations expand before the turn.

### The subagent tool

The parent model can call the `subagent` tool from any turn when it is active in the parent tool catalog, with JSON arguments:

```json
{"name": "reader", "task": "read note.txt and summarize it"}
```

Both fields are required and must be non-empty; a call without them returns a precise argument error. The result reaches the parent through the normal tool result path with a header that names the agent, tier, origin, depth, and model, then the answer and the child session path:

```text
[subagent reader tier=bundle origin=bundle:agents depth=1 model=anthropic/claude-sonnet-4.5]
<answer>
[subagent session: /home/you/.smidja/sessions/subagent-sessions/<parent>/<cwd shard>/<session>.jsonl]
```

The host registers the tool only when no extension or built-in tool named `subagent` is already registered. An override wins, so an extension tool keeps its behavior and the host executor stays out of the way.

Unlike `/agent`, the tool works in print mode and `smidja run`, because the model, not the command surface, triggers it.

### Direct results

A direct `/agent` run does not append user or assistant messages to the parent session. It records the outcome as one custom entry of type `smidja.subagent.result` in the parent session:

| Field | Content |
| --- | --- |
| `agent` | Path-derived definition name |
| `displayName` | Frontmatter display name, when set |
| `tier` | Resolving tier |
| `origin` | Resolving source |
| `path` | Definition path inside its root |
| `model` | Display model id used for the child |
| `depth` | Child depth, starting at 1 |
| `session` | Child session file path |
| `sessionId` | Child session id |
| `status` | `ok` or `error` |
| `result` | Bounded answer, or the bounded error text when the run failed |
| `fullOutput` | Full-output artifact path when the answer was truncated |
| `truncated` | Whether the answer was truncated |

A canceled run writes no entry. Credentials, provider keys, and the task text are not stored in the entry.

## Isolation and limits

### Child sessions

Each run opens one fresh child session under the parent's session root:

```text
<sessions root>/subagent-sessions/<parent session id>/<cwd shard>/<timestamp>_<child id>.jsonl
```

- Directories are created `0700` and the session file is created `0600`.
- The file is opened strict and locked for the duration of the child run; the lock is released when the run ends, including cancellation.
- The first entry is a `smidja.subagent` marker carrying the agent name, tier, depth, and parent session id.
- The child session never appears in the parent's session browser (`/sessions` or `Store.List`) because it lives under `subagent-sessions`.
- The child has its own recorder, history projection, context preparer and compaction, loop detector, and retry policy. Parent history never receives child messages, tool calls, or tool results.

### Tool capabilities

- A definition without `tools` inherits every tool that is active in the parent. With `tools`, the child sees only the named subset.
- Every allowlist entry must be active in the parent when the run starts, otherwise the run fails before any child session or model call with `tool "<name>" is not active for the parent`.
- Tools are revalidated against the live parent-active catalog at call time. A tool disabled while the child is running fails as an unknown tool instead of executing.
- Nested children use the immediate parent's restricted catalog and revalidate against the same root active gate, so capabilities can only decrease down the chain.

### Nesting depth and cycles

- Delegation depth is limited to 4. A request whose parent is already at the limit fails with `maximum nesting depth 4 reached`.
- This is a local recursion guard for delegation. It is not a global turn, token, or cost cap.
- Each child keeps its own delegation wrapper, built from its own depth and ancestry, so re-enabling the builtin `subagent` tool in the parent catalog cannot fall through to a root wrapper.
- A definition already present in the delegation ancestry fails with `delegation cycle detected` and the chain, so `a -> b -> a` cannot loop.

### Model, provider, and reasoning

- The child inherits the parent model unless the definition sets `model`.
- An override must resolve in the model registry or a registered custom provider, otherwise the run fails with `unknown model`.
- A model with no verified wire model for the active transport fails with `no verified wire model for the active transport`. The request uses the native wire id; results and progress report the display id.
- Custom provider models route through the provider's own client and private credential (`Authorization: Bearer <provider key>`). The child never borrows the root provider key for another transport.
- `thinking` on the definition wins over the parent level. An explicit level that the model cannot honor fails with the SDK unsupported error; an inherited level that the model cannot honor is dropped to the provider default instead of failing.
- The request-side reasoning seam is OpenRouter-only. On a transport without it, explicit thinking fails and inherited thinking is dropped.
- Reasoning is resolved per child invocation and is not persisted, so there is no claim that a later process restores a child's level.

### Bounded answers

- A child answer over 2000 lines or 50 KiB is truncated at the budget, clamped to a valid UTF-8 boundary, and returned with the truncation note.
- The complete text is written to a `0600` temporary artifact named `smidja-subagent-*.log` under the OS temporary directory, and the note names its path.
- When the artifact cannot be created, written, or closed, the note says the full output could not be saved and no path is claimed.
- Model errors use the same budget, so a provider error cannot flood the parent context. Failed runs are marked as errors.

### Cancellation and turn ownership

A direct `/agent` run owns the host turn for its whole duration: it holds the turn lock, marks the turn active, merges the caller signal with the host run context, and watches the session generation.

- Esc in the TUI, `Abort` in a host context, a session switch or rebind, and shutdown cancel the child model stream.
- A canceled run persists no success entry and presents a failure only on the viewport that still owns the session generation.
- A pre-canceled request runs no model call, creates no child session artifacts, and writes no entry.
- The child session lock is released on every path, so a canceled child file can be reopened afterward.
- The depth limit and cancellation apply to direct commands and model-callable delegation the same way.

## Extension hooks

Parent extension hooks run on the parent loop only:

- Tool call and tool result hooks fire for the parent's own `subagent` call and result. Other parent hooks keep their existing parent-loop behavior.
- The child loop does not dispatch the parent's compiled hook dispatcher, so a child turn cannot mutate extension state or the parent host API through parent hooks. There is no fabricated child host context or child SDK surface.
- Internal progress reporting still works: the CLI and TUI receive the child start event, tool call progress, and the final done event. The line writer prints `agent <name>: <identity>`, a line per tool call, and the final answer or failure; the TUI renders a subagent block with bounded lines and a success or failure status.
- The run does not add an `sdk.API` method, so the SDK parity counts and the extension contract are unchanged.

## Security boundaries

- Definitions are data. The parser reads only the documented metadata, and nothing in a definition is executed as a shell command, a program, or an environment lookup.
- There is no filesystem sandbox. Child tools run with the same user privileges as the parent process, and the workspace root is not a jail. A child can read, write, or connect anywhere the user can, subject to the tools it is allowed to use.
- The child can only reduce the parent's active tool set, never add to it. The parent authorizes a child run by invoking `/agent` or by allowing the model's `subagent` call.
- Credentials stay with provider clients. Definition metadata cannot carry a credential, custom provider keys are sent only to that provider, and result entries, session files, prompts, and logs do not contain keys.

## Tests

The R4 behavior is covered by these test files, verified by the independent gate:

- `internal/agents/definition_test.go` and `internal/agents/catalog_test.go`: the metadata subset, tier and trust resolution, bounds, and parse errors.
- `internal/agents/executor_test.go` and `internal/agents/executor_extra_test.go`: isolation, tool allowlists and revalidation, nested depth and cycles, output budgets, cancellation, compaction, retry, and child storage.
- `internal/agents/tool_test.go`: tool argument validation, rebinding, and result formatting.
- `internal/cli/agent_wiring_test.go` and `internal/cli/agent_command_test.go`: literal print-mode input, direct and tool paths, canonical command collisions, tool overrides, hook isolation, and stale-generation persistence.
- `internal/cli/agent_executor_test.go`: wire ids, provider credentials and transport switches, thinking inheritance and rejections, nested reasoning seams, and overflow continuation.
- `internal/cli/agent_ownership_test.go`, `internal/cli/agent_pty_test.go`, and `internal/cli/agent_pty_abort_test.go`: turn ownership, pre-canceled runs, abort, rebind, shutdown, and real-PTY cleanup.

The independent gate covered the whole 27-file R4 artifact (review manifest SHA-256 `73c629342d6410a2ff184cb403e66487caf6bd8bf8fdf522514e5dfcf0867436`). Eleven production files measured 83.8% to 100% changed instrumented-line and 87.5% to 100% overlapping-block statement coverage, and the quality and security verdicts are PASS on that exact artifact.

## Limits

- The `/agent` command is not available in print mode or `smidja run`; those paths treat the input as literal prompt text.
- `smidja pkg inspect` lists a package's agent files as plain relative filenames, whether the package is active or only installed. The listing is not validation and does not promise the definitions run; the subagent runtime consumes agent content only while the package is active.
- External provider credentials and external creator acceptance are pending, so no live-provider run is claimed here.
