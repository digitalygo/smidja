# Smidja settings

Smidja reads optional settings files and merges them into its configuration. There is no workspace settings file: settings come only from the trusted user file under `~/.smidja` and from the bundle baked into the binary.

## Where settings live

The trusted user settings file is `~/.smidja/settings.json`. You own and edit this file directly.

Bundles can ship their own `settings.json` inside the binary at build time. The bundle locations are checked in order and the first one present wins:

1. `settings.json` at the bundle root.
2. `content/settings.json`, the legacy location, still read for compatibility.

Both files use the same schema and both are optional; a missing file changes nothing. Smidja never reads a settings file from the repository workspace.

## Supported fields

| Field | Type | Controls |
|---|---|---|
| `defaultProvider` | string | Default provider id. |
| `defaultModel` | string | Default model id. |
| `sessionDir` | string | Session store directory, used as given with no tilde expansion. |
| `modelsCatalogUrl` | string | Models catalog endpoint. |
| `retry.enabled` | boolean | Turns request retry on or off. |
| `retry.maxRetries` | integer | Retry attempts, 0 or more. |
| `retry.baseDelayMs` | integer | Base retry backoff in milliseconds, 0 or more. |
| `compaction.enabled` | boolean | Turns smart context management (prune and compact) on or off. |

Unknown fields are ignored, so a file may carry extra keys. A supported field that is present must be well formed: strings must be JSON strings, booleans JSON booleans, the two retry numbers nonnegative JSON integers, and `retry` and `compaction` JSON objects. `null` is rejected for every supported field. A malformed value fails with an error that names the file and the field, and smidja refuses to start until the file is fixed. User settings and bundle settings behave identically here.

## Example

```json
{
  "defaultProvider": "openrouter-oauth",
  "defaultModel": "anthropic/claude-sonnet-4.5",
  "sessionDir": "/home/user/.smidja/sessions",
  "modelsCatalogUrl": "https://pi.dev/api/models",
  "retry": {
    "enabled": true,
    "maxRetries": 10,
    "baseDelayMs": 2000
  },
  "compaction": {
    "enabled": true
  }
}
```

## Configuration precedence

From highest to lowest, smidja uses the first source that defines a key:

1. CLI flags, for example `-provider` and `-model`.
2. Environment variables.
3. The `.env` file in the current working directory.
4. The bundle tier: when both are present, bundle ConfigDefaults win over the bundle `settings.json` for the same key.
5. User settings, `~/.smidja/settings.json`.
6. Active-package defaults from the `config/defaults.env` of each activated package; when several packages are active, the later one wins.
7. Core defaults compiled into the binary.

A settings value behaves exactly like the corresponding environment variable, just evaluated at its own tier, so the settings chain slots between the bundle tier and the package defaults.

## Environment variables

Every settings field maps to one environment variable:

| Setting | Environment variable |
|---|---|
| `defaultProvider` | `SMIDJA_PROVIDER` |
| `defaultModel` | `SMIDJA_MODEL` |
| `sessionDir` | `SMIDJA_SESSION_DIR` |
| `modelsCatalogUrl` | `SMIDJA_MODELS_CATALOG_URL` |
| `retry.enabled` | `SMIDJA_RETRY` |
| `retry.maxRetries` | `SMIDJA_RETRY_MAX_RETRIES` |
| `retry.baseDelayMs` | `SMIDJA_RETRY_BASE_DELAY_MS` |
| `compaction.enabled` | `SMIDJA_CONTEXT` |

Because settings translate into these variables, the variable names work the same way in the shell, in `.env` files, and in package `config/defaults.env` files. Booleans accept `0`, `false`, `no`, and `off` (case-insensitive) as false and any other nonempty value as true. Integers must be nonnegative; an unparsable or negative value falls back to the core default.

## Models catalog

smidja fetches its model catalog from `https://pi.dev/api/models` unless `modelsCatalogUrl` (or `SMIDJA_MODELS_CATALOG_URL`) points elsewhere. The fetched catalog is cached in `~/.smidja/models-store.json`, next to the session store, and refreshed with conditional requests so an unchanged catalog does not redownload.

A failed catalog refresh is not fatal: smidja keeps using the cached catalog, and with no cache at all it runs without model metadata. Set `SMIDJA_OFFLINE` to a truthy value such as `1` or `yes` to skip the network fetch entirely.

## Security

Settings cannot supply credentials. No supported field carries an API key, a token, or any other secret, and no field can inject arbitrary environment variables. Provider credentials resolve only from the environment and from `~/.smidja/auth.json`, as described in the [auth documentation](auth.md). A settings file can only choose defaults such as provider id, model, paths, retry, and context behavior.
