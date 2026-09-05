# Creating content packages

A content-only package extends an installed smidja harness after the fact with skills, prompts, agents, and config defaults. It carries no code and is installed straight from a public GitHub repository with `smidja pkg install <owner/repo@version>`. Installation always remains direct and never depends on the optional public discovery registry: the direct `owner/repo@version` form is the only install path, and the [digitalygo/smidja-packages](https://github.com/digitalygo/smidja-packages) catalog is a discovery aid that never mediates installation or version resolution.

If you want to ship compiled Go extensions, you need a [compiled bundle](creating-bundles.md) instead.

## The no-code boundary

The boundary is absolute, and it is a permanent product decision rather than a temporary gap:

- A package can carry only four content kinds: `skills`, `agents`, `prompts`, and `config`.
- Skills, prompts, and agents are markdown files. Config defaults are `KEY=VALUE` text files.
- Nothing in a package is executed. There is no extension, hook, binary, script, or plugin surface in packages, and that package-extension boundary is permanent: extensions are compiled Go inside a bundle build only. The harness separately supports MCP servers configured in `mcp.json` files, but that is a configured integration of its own, not something a package can carry or enable.
- Package config defaults are data, not commands: they supply fallback values for config keys, and the environment or the user always wins over them.

## Repository layout

`smidja pkg install` downloads the GitHub source tarball of your version tag, so the tarball contents are your contract. The rules are strict:

- `smidja.json` must sit at the repository root.
- Every regular file in the tarball must be either `smidja.json` or a file declared in the manifest. Anything else fails installation with an `unexpected file` error.
- All declared files must live under a content root declared in `contents`. Files at the repository root other than `smidja.json` can never be declared, because a content root must be a non-empty nested path.
- Symlinks, hardlinks, and special files are rejected everywhere in the tree.

Layout the repository accordingly. Tracked files such as `README.md`, `LICENSE`, `.gitignore`, or `.github/` workflows appear in the tarball and break installation. Keep the repository to the manifest and content directories only, keep helper files untracked, or move documentation into a file under a declared root when it is meant to ship as content. Plan for this before the first tag, because fixing it later means retagging.

A minimal valid repository looks like this:

```text
smidja-daily-notes/
  smidja.json
  config/
    defaults.env
  skills/
    daily-notes.md
```

## The smidja.json manifest

The manifest is parsed strictly: unknown fields, duplicate JSON keys, and trailing data are rejected, and the top-level value must be an object.

```json
{
  "schemaVersion": 0,
  "id": "daily-notes",
  "version": "v0.1.0",
  "owner": "octo-labs",
  "repo": "smidja-daily-notes",
  "description": "Daily notes skill with its config defaults.",
  "contents": {
    "skills": "skills",
    "config": "config"
  },
  "minimumHarness": "v0.0.0",
  "files": [
    { "path": "config/defaults.env", "sha256": "d380d5f348e18a8bceba9e9927c108d385d2fe4106b47ed6ee70a8b5b42b9981", "size": 109 },
    { "path": "skills/daily-notes.md", "sha256": "5ef15b9167833d0b01b7c5b4d3017a0c270141b9418f4bad574347269f120190", "size": 205 }
  ]
}
```

### Field reference

| Field | Type | Required | Constraints |
|---|---|---|---|
| `schemaVersion` | number | must be `0` | Set it explicitly; `0` is the only accepted value. |
| `id` | string | yes | Matches `^[a-z0-9-]{1,64}$`. This is the package id every `pkg` command uses. |
| `version` | string | yes | Canonical `vMAJOR.MINOR.PATCH`, identical to the git tag. |
| `owner` | string | yes | GitHub owner. Must match the `owner` of the install request. |
| `repo` | string | yes | GitHub repository. Must match the `repo` of the install request. |
| `description` | string | no | At most 200 characters. |
| `contents` | object | no | Keys from `skills`, `agents`, `prompts`, `config`; values are root paths. |
| `depends` | array | no | See [dependencies](#dependencies). |
| `minimumHarness` | string | yes | Canonical `vMAJOR.MINOR.PATCH`. |
| `files` | array | when content exists | May be omitted only for a package that ships no files; otherwise see [the files array](#the-files-array). |

A canonical version is `v` followed by three numeric fields with no leading zeros and no prerelease or build suffix. `v0.1.0` is canonical; `v1.0`, `v01.0.0`, `v1.0.0-rc.1`, and `1.0.0` are not.

`minimumHarness` is currently declarative. It must be present and well formed, and `smidja pkg inspect` displays it, but nothing enforces it against the running harness version at install time or runtime.

### Content kinds and roots

Each `contents` entry maps a content kind to a directory root:

- Kind must be exactly one of `skills`, `agents`, `prompts`, `config`.
- A root must be a clean relative path: no leading slash, no backslashes, no empty, `.`, or `..` path segments.
- Two roots must not overlap; `skills` and `skills/extra` cannot both be declared.
- `config` roots hold `KEY=VALUE` files parsed by the harness. The other kinds hold markdown files.

### The files array

`files` lists every content file in the tarball, with exact metadata:

- `path` is a clean relative path under one of the declared roots. A path directly equal to a root is not enough; the file must be inside it.
- Entries must be sorted by path in strictly increasing byte order, with no duplicates.
- `sha256` is the 64 hex character digest of the file contents. Verification compares case-insensitively; publish lowercase digests.
- `size` is the exact byte size and must be positive. It is checked against the real file.

Sizes are re-verified and hashes recomputed at install time and on every `smidja pkg verify`.

### Limits

- Total package size: 32 MiB, summed over the declared file sizes.
- File count: at most 500 declared files.

### Dependencies

A `depends` entry pins one other package:

```json
{
  "id": "code-review",
  "owner": "octo-labs",
  "repo": "smidja-code-review",
  "minimumVersion": "v1.2.0"
}
```

- `id` follows the same pattern as the package id and must not equal your own id.
- `owner` and `repo` are required.
- Exactly one of `minimumVersion` or `exactVersion` must be present, and it must be a canonical version.
- Duplicate dependency ids are rejected.

Resolution works over the whole constraint graph:

- All dependers on the same id must agree on `owner` and `repo`.
- Exact constraints must all agree; if an exact and a minimum meet, the exact version must not be below the minimum.
- Otherwise the highest declared `minimumVersion` is the version that gets fetched. The resolver never queries for the newest release.
- An installed package is reused only when id, owner, repo, and version all match. A newer installed version does not satisfy a lower minimum; the requested version is installed alongside it.
- Dependency cycles are rejected.
- Dependencies install before the package that needs them, and activating a package also activates its recorded dependency closure.

## Computing file metadata

No manifest generator ships with smidja today, so produce `files` with standard tools. From the repository root, list your declared roots in place of `config skills`:

```bash
find config skills -type f | LC_ALL=C sort | while read -r p; do
  size=$(wc -c < "$p")
  printf '    { "path": "%s", "sha256": "%s", "size": %s },\n' \
    "$p" "$(sha256sum "$p" | cut -d" " -f1)" "$((size))"
done
```

Paste the output into the `files` array. Two details make this deterministic:

- `LC_ALL=C sort` reproduces the byte order the manifest validation requires.
- `sha256sum` prints lowercase digests; on macOS, substitute `shasum -a 256`.

The snippet lists whatever exists under those roots, including files you forgot about, which is exactly what you want: installation fails on anything the manifest fails to declare.

## Tagging and publishing

- Cut a git tag whose name equals the manifest `version`, for example `v0.1.0`.
- The repository must be public. Fetching uses the GitHub API without credentials, so unauthenticated rate limits apply.
- On install, smidja resolves the tag to a commit through the API, downloads the tarball for that commit, and records the commit and the manifest SHA-256 in the package receipt.
- `smidja pkg install <owner/repo@version> --pin <commit>` fails unless the resolved commit matches the pin.

Tags on GitHub are mutable by default. Protect your tags or treat a moved tag as a broken contract for everyone who pinned or installed it.

## Listing in the public registry

The optional discovery registry lives at [digitalygo/smidja-packages](https://github.com/digitalygo/smidja-packages). Its [`catalog.md`](https://github.com/digitalygo/smidja-packages/blob/main/catalog.md) lists published packages so people can find them, and its [contribution guide](https://github.com/digitalygo/smidja-packages/blob/main/.github/CONTRIBUTING.md) explains how to get a package listed.

- Listing is a publisher action: you submit your package by opening a pull request against the registry repository, following its contribution guide.
- The catalog is publisher-maintained and does not track releases. There is nothing to update per release, because the tags in your own repository remain the only authority on versions; the catalog is discovery metadata only.
- Being listed is not an endorsement and not an authenticity or safety certification. The harness marks package authenticity `unverified` regardless of any catalog entry, so review a package before installing it, listed or not.
- Admission can require evidence of package ownership, and such evidence must live outside the strict package archive: the tarball accepts only the manifest and the declared content files, so nothing else can ship inside it. Provide it in the registry pull request itself.

## Install and lifecycle

Installing the minimal example prints:

```text
installed daily-notes@v0.1.0 (commit <commit sha>)
config defaults that will change:
  SMIDJA_MODEL: (unset) -> anthropic/claude-sonnet-4.5
pkg activate: activate daily-notes@v0.1.0? [y/N] y
activated daily-notes@v0.1.0
```

The command installs, then shows which config values would change, then offers activation. Answering `n` leaves the package installed but inactive. Use `--yes` in scripts to skip the confirmation; note that the prompt cannot run without a terminal, so non-interactive runs need `--yes`.

The full lifecycle:

```bash
smidja pkg install <owner/repo@version> [--yes] [--pin <commit>]
smidja pkg list [--json]
smidja pkg inspect <id> [--version v] [--json]
smidja pkg activate <id> [--version v]
smidja pkg deactivate <id> [--version v]
smidja pkg update [<id>...] [--yes]
smidja pkg verify [<id>...] [--version v]
smidja pkg uninstall <id> [--version v] [--yes]
```

- `update` compares each installed package with the first canonical version tag in the repository's tag list and installs it when it is newer. If the updated version was active, activation moves to it and the old version deactivates.
- `verify` recomputes every declared file's hash and size against the manifest and requires the install receipt; it prints `<id>@<version> ok` per package.
- `uninstall` refuses while the package is active (it deactivates first after confirmation) or while another installed package records it as a dependency.
- Packages live under `~/.smidja/packages` by default; set `SMIDJA_PACKAGES_DIR` to relocate the store.

## Config defaults

Every file under a `config` root is parsed as a defaults file:

- `KEY=VALUE` per line; `#` starts a comment; blank lines are skipped; lines without `=` are ignored.
- Values are trimmed and surrounding single or double quotes are stripped.
- All files under the root are parsed, recursively.

Defaults sit at the bottom of the config precedence, above only the compiled core defaults. From highest to lowest: CLI flags, environment variables, the workspace `.env`, the bundle tier, user settings, active-package defaults, core defaults. When several packages are active, a later activated package wins over an earlier one for the same key. The full description lives in [smidja settings](settings.md).

## What content does today

Markdown files under `skills`, `prompts`, and `agents` share the same constraints: no path segment may start with a dot, each file is capped at 100 KiB, and content must be valid UTF-8.

- Skills are live: `skills/**/*.md` files become `/skill` entries, named by their path under the skills root minus the `.md` suffix.
- Prompts are validated and carried but have no runtime surface yet.
- Agents are validated and carried; `smidja pkg inspect` lists them as deferred until the subagent runtime consumes them.

Content is resolved per tier: bundle, then trusted workspace, then user content in `~/.smidja`, then active packages, then core defaults. A package never overrides the user's own content at the same name.

## Authenticity and integrity

- Integrity: the manifest hashes and sizes cover every file, verified at install and on every `verify`.
- Authenticity: there are no signatures. The receipt records the source repository, version, resolved commit, and manifest digest, and marks authenticity as `unverified`.
- Trust: installing `owner/repo@version` trusts that the public repository serves the content you expect at that tag. Review the tag before installing and keep your own tags immutable.

## Minimal valid example

The manifest at the top of this guide, plus these two content files, forms a complete package.

`config/defaults.env`:

```bash
# daily-notes defaults; the user env and .env win over these values
SMIDJA_MODEL=anthropic/claude-sonnet-4.5
```

`skills/daily-notes.md`:

```markdown
# Daily notes

At the end of each work session, create or update `notes/<date>.md` in the
workspace with a short summary of what was done, open questions, and the next
three actions for the following day.
```

Tag the repository `v0.1.0` and install it with the command shown in [install and lifecycle](#install-and-lifecycle). After activation, `/skill daily-notes` injects the skill into a session and `SMIDJA_MODEL` falls back to the declared default when the user set none.
