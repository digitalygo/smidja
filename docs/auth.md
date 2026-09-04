# Smidja auth

Smidja stores provider credentials in `~/.smidja/auth.json` and resolves them per request with a simple precedence: the environment variable wins when set, otherwise the stored credential is used. This mirrors how Pi resolves credentials and keeps the same file working across both tools.

## Precedence

For every provider the resolution order is:

1. The provider's environment variable, for example `OPENROUTER_API_KEY`.
2. The stored credential in `~/.smidja/auth.json` under the provider id.
3. An error at request time when neither source carries a credential.

A stored credential therefore never overrides an environment variable: set the env var when you want to temporarily switch keys without touching the store.

## Where tokens live

The store is a single JSON object at `~/.smidja/auth.json`, one entry per provider id. The directory is created with mode `0700` and the file with mode `0600`, so only your user can read the credentials.

Every write is a serialized read-modify-write: smidja takes an exclusive file lock on a `.lock` sidecar next to the store, re-reads the store from disk after acquiring the lock, applies the change, and writes the result atomically through a temp file in the same directory (mode `0600`) renamed over the target. Concurrent `smidja auth` processes therefore never overwrite each other's entries, and a crash never leaves a half-written credential file.

Two entry shapes exist:

```json
{
  "deepseek": {
    "type": "api_key",
    "key": "sk-ds-...",
    "env": {"DEEPSEEK_API_KEY": "sk-ds-..."}
  },
  "anthropic-oauth": {
    "type": "oauth",
    "access": "sk-ant-oat...",
    "refresh": "sk-ant-oat...",
    "expires": 1780000000000,
    "scope": "read write"
  }
}
```

Unknown fields per entry are preserved verbatim across rewrites, so credentials written by other tools survive `smidja auth` operations. The one exception to the oauth shape is `openrouter-oauth`: OpenRouter mints non-expiring API keys through its OAuth flow, so that entry loads with an empty `refresh` and an `expires` far in the future. Other providers still require `access` and `refresh`.

## Telegram and web tokens

The gateway credentials follow the same env-over-store rule as provider credentials:

| Credential | Environment variable | Store key |
|---|---|---|
| Telegram bot token | `TELEGRAM_BOT_TOKEN` | `telegram` |
| Web server token | `SMIDJA_WEB_TOKEN` | `web` |

`smidja auth login telegram` stores the bot token and `smidja auth login web` stores the web token; with `--api-key` the command reads the environment variable when it is set and prompts on stdin only when it is not. `smidja auth status` lists both credentials. The gateway resolves each token from the environment first and falls back to the stored entry.

## Commands

### Login

```bash
smidja auth login openrouter
```

Runs the provider's OAuth flow: it opens the browser, waits for the callback on the local redirect server, exchanges the code for a token, and stores the entry. If the browser callback does not arrive, the prompt asks you to paste the authorization code or the redirect URL.

Available OAuth providers and their store keys:

| Provider | Command | Store key |
|---|---|---|
| OpenRouter | `smidja auth login openrouter` | `openrouter-oauth` |
| Anthropic | `smidja auth login anthropic` | `anthropic-oauth` |
| OpenAI Codex | `smidja auth login codex` | `codex` |
| xAI | `smidja auth login xai` | `xai-subscription` |
| Kimi | `smidja auth login kimi` | `kimi-coding-oauth` |

The device-code flows (xai, kimi) print the verification URL and code instead of opening a browser callback page.

### API-key mode

Any API-key provider of the [providers manifest](providers-manifest.md) can store its key with `--api-key`:

```bash
smidja auth login deepseek --api-key
```

The key is read from the provider's environment variable when set (`DEEPSEEK_API_KEY` in the example), otherwise the command prompts on stdin and stores what you paste:

```bash
export DEEPSEEK_API_KEY=sk-ds-...
smidja auth login deepseek --api-key
```

### Logout

```bash
smidja auth logout openrouter
```

Removes the stored entry for the provider. The friendly names map to the same store keys as login, and the store keys themselves are accepted too, so `smidja auth logout openrouter-oauth` does the same thing. Removing an entry that does not exist is a no-op.

### Status

```bash
smidja auth status
```

Prints a table of every manifest provider and every OAuth provider with its configuration state, without printing any secret:

```text
provider                 type      status
anthropic                api_key   not configured
anthropic-oauth          oauth     not configured
deepseek                 api_key   configured (store)
openrouter               api_key   configured (env)
openrouter-oauth         oauth     configured (store)
```

The status values are `configured (env)`, `configured (store)`, `configured (env + store)`, and `not configured`. The `env` part reflects the provider environment variable, the `store` part the entry in `~/.smidja/auth.json`.

## Selecting a provider at runtime

The chat commands accept a provider override:

```bash
smidja -provider anthropic-oauth -p "explain the diff"
```

Without `-provider`, smidja uses the default OpenRouter client built from the config. With `-provider`, the driver comes from the OpenRouter compatibility client, the manifest (`manifest.Build`), or the OAuth-backed drivers:

- The exact id `openrouter` selects the OpenRouter compatibility client configured by `OPENROUTER_API_KEY` and `SMIDJA_OPENROUTER_URL`; it never reads the stored OAuth credential. Use `openrouter-oauth` for the stored OAuth credential.
- Manifest ids such as `deepseek`, `openai`, or `kimi-coding` resolve their credential through the env-then-store precedence.
- OAuth ids such as `openrouter-oauth`, `anthropic-oauth`, `codex`, `xai-subscription`, and `kimi-coding-oauth` use the stored oauth entry. The friendly names `anthropic`, `codex`, `xai`, and `kimi` are accepted as well and prefer the OAuth entry when one is stored.
- OAuth access tokens are refreshed lazily inside the retry loop's produce path: each request resolves the token from the store, refreshes it before expiry through the provider's refresh endpoint, and persists the refreshed entry back.

When `-provider` is given and neither `-model` nor `SMIDJA_MODEL` is set, the model defaults to the provider's default model. Set `-model` explicitly to override it.

## Tokens never leave the machine

Login exchanges happen over HTTPS directly from your machine to the provider. The store never transmits credentials anywhere, and `smidja auth status` never prints them. Treat `~/.smidja/auth.json` like a private key file: do not commit it, do not copy it between machines, and back it up with the same care as the credentials it holds.
