# Prompts

A prompt template is a markdown file that smidja resolves from the same content tiers as skills and agents. Invoking a template substitutes its arguments into the text and sends the result as a normal user turn. Expansion is text substitution only: smidja never executes a template, and it never evaluates shell commands, environment variables, or files.

## Status

Prompt runtime consumption is implemented in the worktree and awaits its final validation gates and publication; the installed binary predates it. Agent definitions are still carried as deferred content, and this surface does not back the extension SDK host actions that remain unavailable.

## Where templates come from

Templates resolve with the other content kinds, highest tier first:

| Tier | Location |
|---|---|
| Bundle | `prompts` or `content/prompts` inside the bundle filesystem |
| Trusted workspace | `<workspace>/.smidja/prompts` |
| User | `~/.smidja/prompts` |
| Active packages | the root declared for `prompts` in `smidja.json`, `prompts` by default |
| Core | none |

- A higher tier wins when two tiers define the same name.
- The name is the path under the root minus `.md`, so `role/core.md` is the template `role/core`.
- Files follow the shared content rules: only `.md` files are read, each file is at most 100 KiB, content must be valid UTF-8, symlinks are rejected, and no path segment may be empty, `.`, `..`, or start with a dot. [Creating content packages](creating-content-packages.md) documents the package manifest and validation.
- The workspace tier uses the existing workspace trust decision and the same snapshot as skills and agents. The TUI asks for trust per run; the line, print, and `smidja run` paths do not show the dialog and load workspace content. Prompts add no separate trust store or ingestion path.

### Catalog safety

- Names containing whitespace, control characters, Unicode format characters, line or paragraph separators, or invalid UTF-8 are excluded from the prompt catalog, so they cannot be listed or invoked.
- A shorthand description comes from the first non-blank line of the template: surrounding whitespace is removed, the result is cut to 80 runes, and control and format characters are stripped. A template with no usable line shows the fallback description `prompt template`.

## Invoking a template

In the line interface and the TUI:

- `/prompt` lists the resolved names, sorted and one per line. A missing or blank name lists instead of running.
- `/prompt <name> [arguments]` runs the template.
- A template name that collides with no existing command also works as a shorthand: `/name [arguments]`.
- An unknown name fails with `no prompt named "<name>"`.

Reserved names stay free for built-in commands: `new`, `tree`, `fork`, `resume`, `sessions`, `help`, `model`, `theme`, `settings`, `quit`, `exit`, and `agent`. `agent` is reserved for a future built-in `/agent` command. A template with a reserved or colliding name is still reachable through explicit `/prompt <name>`.

The canonical `/prompt` command is registered before extensions register their commands. When an extension registers `prompt`, the catalog keeps the canonical command and gives the extension the next free numeric suffix, so the extension is reachable as `/prompt2` (then `/prompt3`, and so on).

The TUI routes the listing to a transcript notice instead of terminal stdout, and `/prompt` and its shorthands appear in `/help`, the command inventory, and editor autocomplete. [TUI](tui.md) documents the interactive surface. In the line interface, failures print as `smidja: /prompt: <error>`, and the TUI shows them as warning notices.

## Arguments

Arguments after the template name are split with these rules:

| Input | Result |
|---|---|
| `one two` | two arguments |
| `"two words"` or `'two words'` | one argument, whitespace kept |
| `one\ two` | one argument with a literal space |
| `a\\b` | one argument, `a\b` |
| `a\` | one argument, the trailing backslash stays literal |
| `"say \"hi\""` | one argument, `say "hi"` |
| `'a\b'` | one argument, `a\b`, because single quotes are literal |
| `"a"'b'c` | one argument, `abc`, because adjacent segments join |
| `""` | one empty argument |

- Whitespace outside quotes (space, tab, newline, carriage return, vertical tab, form feed) separates arguments.
- Outside quotes, a backslash escapes the next character, including whitespace and quotes.
- Inside double quotes, a backslash escapes the next character. Inside single quotes, everything is literal, including backslashes.
- An unmatched quote fails with `prompt arguments: unmatched single quote` or `prompt arguments: unmatched double quote`, wrapped with the template name.

Nothing is executed while parsing. `$HOME`, `$(command)`, backticks, `${name}`, `;`, `|`, and `&&` are ordinary text.

## Expansion

Placeholders in the template body:

| Placeholder | Value |
|---|---|
| `$1` to `$N` | the Nth argument; a missing argument becomes empty |
| `$@` | every argument joined with single spaces |
| `$ARGUMENTS` | the same as `$@`; recognized only when not followed by a letter, a digit, or an underscore |

A template containing `hello $1 from $ARGUMENTS` invoked as `/prompt greet Bob Smith` expands to `hello Bob from Bob Smith`.

- `$0` is literal `$0`. A leading zero on a nonzero index is ignored, so `$01` is the first argument.
- An index larger than the number of arguments expands to nothing. A number too large to parse stays literal.
- Unknown placeholders stay literal as written: `$FOO`, `${BAR}`, `$x`, `$ARGUMENTSX`, `$%`, and a trailing `$`.
- Substitution is not recursive. If `$1` holds `$2`, the output contains `$2`, not the second argument.
- A placeholder repeats as many times as it appears.

Expansion is text only. Templates cannot call tools, run shell commands, read environment variables, or read files.

## Expansion budget

One invocation may expand to at most 1048576 bytes (1 MiB). The limit is enforced while the output is written:

- The literal template body counts toward the limit, and the exact limit is allowed.
- Arguments the template never references are not counted and are not expanded.
- Going over the limit fails the invocation with `prompt: expanded content exceeds the 1048576 byte limit`, wrapped with the template name. Nothing is truncated silently.

The guard is local to one expanded prompt. It is not the model context window and not a global turn or token cap; the model side keeps its own context handling.

## Print mode and one-shot runs

A prompt passed to the root `-p` flag or to `smidja run` is checked only when its trimmed text begins with `/`:

- `/prompt <name> [arguments]` expands the template before the turn.
- `/name [arguments]` expands when the name is a registered shorthand.
- Any other text beginning with `/` is sent to the model unchanged.
- Plain text is never scanned for placeholders, so `$1` in ordinary input stays `$1`.

The expanded text is what reaches the model and what the session stores; the raw invocation is not persisted. In these one-shot paths, an expansion error stops the run before a request is sent, and `/prompt` without a name fails with `prompt: a template name is required` because there is no listing surface for a single prompt string.

## The `smidja run` subcommand

`smidja run` runs one turn and exits. It takes the prompt either as one positional argument or with `-p`, never both, and it shares the root `-p` execution path, prompt expansion, and exit behavior.

```bash
smidja run "explain this repository"
smidja run -p "explain this repository"
smidja run -model openai/gpt-5 "explain this repository"
smidja run "explain this repository" -model openai/gpt-5
smidja run -- "-prompt that starts with a dash"
smidja run -continue <path-or-id> "next question"
```

- The root flags (`-model`, `-system`, `-provider`, `-continue`, `-tui-mode`, `-use-theme`, `-version`, `-allow-workspace-mcp`) work before or after the positional prompt. `--` ends flag parsing.
- `-h` and `--help` print the run usage without starting a turn.
- An unknown flag, a flag missing its value, more than one positional prompt, no prompt, or an empty or whitespace-only prompt fails with the run usage and starts nothing. A rejected invocation creates no session file.
- `run` never starts the TUI, even when stdin and stdout are terminals. It stays on the one-shot path: no alternate screen and no terminal attribute changes.
- The exit status matches the root `-p` path: 0 for a completed run, 1 when validation, startup, or the turn fails.
- `-continue <path-or-id>` resumes an existing session instead of creating one, keeps its transcript and runtime profile continuity, and writes the new turn into that session file.

Common failure messages:

| Failure | Message |
|---|---|
| Both forms | `run: use either -p <prompt> or one positional prompt, not both` |
| No prompt | `run: a prompt is required, use smidja run <prompt> or smidja run -p <prompt>` |
| More than one positional | `run: expected exactly one prompt, got <n>` |
| Empty prompt | `run: the prompt must not be empty` |
