# Smidja themes

The TUI renders with a theme: a named JSON document with a `colors` map. The built-in `dark` and `light` themes ship inside the binary, and custom themes come from `~/.smidja/themes`. A custom theme with the same name as a built-in replaces it.

## Where themes live

Smidja discovers themes in this order, and a later source wins when two themes share a name:

1. The built-in `dark` and `light` themes.
2. JSON files in `~/.smidja/themes`.

A theme file is any `*.json` file in the directory whose document declares a non-empty `name` without a `/`. The file name does not matter; the `name` field is the theme name. Files that fail to parse, or that lack a valid name, are ignored during discovery, so they cannot be selected.

The theme registry can also read a package theme directory between the built-ins and the user directory, and the precedence is covered by tests, but the CLI runner does not wire a package directory today. Treat `~/.smidja/themes` as the custom theme location.

## Theme file format

A theme document has three fields:

| Field | Required | Meaning |
|---|---|---|
| `name` | yes | The theme name. It cannot contain `/` or path separators. |
| `vars` | no | Named color values that `colors` entries can reference. |
| `colors` | yes | The token map. Every required token must be present. |

The `colors` map is validated every time the theme loads. Missing required tokens fail the load with a message that lists them.

Example theme:

```json
{
  "name": "example",
  "vars": {
    "bg": "#101418",
    "panel": "#1b222c",
    "panelAlt": "#232c38",
    "text": "#d8dee9",
    "muted": "#6c7a89",
    "accent": "#88c0d0",
    "success": "#a3be8c",
    "error": "#bf616a",
    "warning": "#ebcb8b"
  },
  "colors": {
    "accent": "accent",
    "border": "muted",
    "borderAccent": "accent",
    "borderMuted": "muted",
    "success": "success",
    "error": "error",
    "warning": "warning",
    "muted": "muted",
    "dim": "muted",
    "text": "text",
    "thinkingText": "muted",
    "userMessageText": "text",
    "customMessageText": "text",
    "customMessageLabel": "accent",
    "toolTitle": "text",
    "toolOutput": "muted",
    "mdHeading": "accent",
    "mdLink": "accent",
    "mdLinkUrl": "muted",
    "mdCode": "success",
    "mdCodeBlock": "text",
    "mdCodeBlockBorder": "muted",
    "mdQuote": "muted",
    "mdQuoteBorder": "muted",
    "mdHr": "muted",
    "mdListBullet": "accent",
    "toolDiffAdded": "success",
    "toolDiffRemoved": "error",
    "toolDiffContext": "muted",
    "syntaxComment": "muted",
    "syntaxKeyword": "accent",
    "syntaxFunction": "accent",
    "syntaxVariable": "text",
    "syntaxString": "success",
    "syntaxNumber": "warning",
    "syntaxType": "accent",
    "syntaxOperator": "text",
    "syntaxPunctuation": "text",
    "thinkingOff": "muted",
    "thinkingMinimal": "muted",
    "thinkingLow": "accent",
    "thinkingMedium": "accent",
    "thinkingHigh": "accent",
    "thinkingXhigh": "accent",
    "bashMode": "warning",
    "selectedBg": "panelAlt",
    "userMessageBg": "panel",
    "customMessageBg": "panel",
    "toolPendingBg": "panel",
    "toolSuccessBg": "panelAlt",
    "toolErrorBg": "panelAlt"
  }
}
```

## Color values

Every entry in `vars` and `colors` accepts one of three forms:

- A hex color string such as `"#88c0d0"`.
- An empty string, which means the terminal default for that foreground or background.
- An integer from 0 to 255, which selects a 256-color palette index.

A non-empty string that does not start with `#` is a reference to a name in `vars`. References can chain, and a missing or circular reference fails the load. Unknown token names in `colors` are allowed and ignored by the renderer.

## Required tokens

All 51 tokens below must be present. The grouping is for readability only.

| Area | Tokens |
|---|---|
| Core | `accent`, `border`, `borderAccent`, `borderMuted`, `success`, `error`, `warning`, `muted`, `dim`, `text`, `thinkingText` |
| Messages | `userMessageText`, `customMessageText`, `customMessageLabel` |
| Tools and backgrounds | `toolTitle`, `toolOutput`, `toolDiffAdded`, `toolDiffRemoved`, `toolDiffContext`, `selectedBg`, `userMessageBg`, `customMessageBg`, `toolPendingBg`, `toolSuccessBg`, `toolErrorBg` |
| Markdown | `mdHeading`, `mdLink`, `mdLinkUrl`, `mdCode`, `mdCodeBlock`, `mdCodeBlockBorder`, `mdQuote`, `mdQuoteBorder`, `mdHr`, `mdListBullet` |
| Syntax | `syntaxComment`, `syntaxKeyword`, `syntaxFunction`, `syntaxVariable`, `syntaxString`, `syntaxNumber`, `syntaxType`, `syntaxOperator`, `syntaxPunctuation` |
| Thinking and bash | `thinkingOff`, `thinkingMinimal`, `thinkingLow`, `thinkingMedium`, `thinkingHigh`, `thinkingXhigh`, `bashMode` |

## Optional tokens and fallbacks

Five tokens are optional. When a theme omits one, the renderer resolves it from the token named in the fallback column. A theme may also set them explicitly.

| Token | Fallback when omitted |
|---|---|
| `scrollbarTrack` | `muted` |
| `scrollbarThumb` | `text` |
| `thinkingMax` | `thinkingXhigh` |
| `searchMatchBg` | `selectedBg` |
| `searchMatchText` | `text` |

## Validation

Theme loading fails with an actionable error when:

- The document is not valid JSON or lacks a `colors` object.
- The `name` contains `/`, which is reserved for the `lightTheme/darkTheme` pair syntax.
- A required token is missing. The error lists every missing token.
- A color is not a hex string, an empty string, a 0 to 255 integer, or a resolvable `vars` reference.
- A `vars` reference is missing or circular.

Selecting a theme through a flag, environment variable, or setting accepts one name or a `lightTheme/darkTheme` pair. A single name cannot contain a path separator or a dot segment.

## Color mode

Smidja uses truecolor when `COLORTERM` contains `truecolor` or `24bit`, and the 256-color palette otherwise. Hex colors are converted to the nearest palette index in 256-color mode, preferring a grayscale entry for near-neutral colors.

## Selecting a theme

The first source that defines a value wins:

1. `--use-theme name` or `--use-theme lightTheme/darkTheme`.
2. `SMIDJA_THEME`, then the workspace `.env`, the bundle tier, and `~/.smidja/settings.json` `theme`, in the order described in the [settings documentation](settings.md).
3. With no value set, the built-in appearance pair: `light` on a light background and `dark` otherwise.

A single name is used as given. A pair follows the terminal background: smidja asks the terminal for its background color with a bounded 100 ms query and picks the light or dark entry. When the terminal does not answer, the dark entry stays active.

The `/theme` selector applies a theme immediately for the session, and a theme that is syntactically valid but missing leaves the current theme in place with a notice. Neither path writes the settings file. See the [settings documentation](settings.md) for the full configuration precedence.

## Hot reload

Smidja watches the file of the active custom theme and reloads it when its modification time changes. The default polling interval is two seconds. Invalid updates are ignored and the previous theme stays active, and the watcher stops when the TUI stops. Built-in themes are not watched.
