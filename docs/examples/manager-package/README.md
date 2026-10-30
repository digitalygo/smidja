# Manager/client packages, schema 1

This is distinct from the legacy `internal/packages` schema 0. There is no migration.

A release URL returns one JSON object:

```json
{
  "id": "example.coding",
  "version": "1.0.0",
  "archive_url": "https://publisher.example/coding-1.0.0.zip",
  "sha256": "<lowercase SHA-256 of the complete ZIP>"
}
```

Configure its source as `{ "url": "https://publisher.example/release.json", "token_env": "PACKAGE_TOKEN" }`. Omit `token_env` for public sources. Secrets are resolved locally, not saved in receipts. URLs must be HTTPS without embedded credentials, query strings or fragments. Downloads follow at most five HTTPS redirects. Signed query parameters are permitted only on redirect targets, never on the configured source URL. Authorization is initially sent only to the release URL's exact host. On any cross-origin redirect, Authorization and cookies are stripped permanently for that download, even if a later redirect returns to the original host. Redirect errors never expose signed URLs or credentials.

The ZIP has `smidja.json` at its root and regular relative files only. Directory entries, links, duplicate paths, unlisted files, traversal paths and archives expanding beyond 64 MiB are rejected. The ZIP and every payload file must match their recorded digests. A manifest looks like:

```json
{
  "schema_version": 1,
  "id": "example.coding",
  "version": "1.0.0",
  "publisher": "Example",
  "requires_smidja": ">=1.0.0",
  "scopes": ["manager", "client"],
  "targets": ["linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64"],
  "teams": ["teams/reviewer.json"],
  "workflows": [],
  "tools": [],
  "skills": ["skills/review.md"],
  "files": [
    {"path": "teams/reviewer.json", "sha256": "<file digest>"},
    {"path": "skills/review.md", "sha256": "<file digest>"}
  ],
  "dependencies": [],
  "configuration": {"GITHUB_TOKEN": {"secret": true}}
}
```

Versions currently support stable `x.y.z`, and `requires_smidja` supports exactly `>=x.y.z`; unsupported constraints are rejected rather than guessed. Team, workflow and tool JSON documents must each have a unique ID beginning with `example.coding/`. Their remaining payload is consumed by the corresponding runtime, not interpreted as an installation hook. Skills are verbatim files. Executable helpers explicitly set `"executable": true` on their file entry. Installation never executes helpers or install scripts. Do not publish credentials inside any payload.

`managerpackages.Open` uses its own exclusive file lock. `Install` reads a release URL, stages and verifies its archive and atomically switches `active.json`. Payload files are read-only, helper files executable/read-only, and revision directories read-only. Every activation records source, exact version, manifest and archive digest. Changing the publisher source, downgrading or publishing different bytes under an active version is rejected. Existing revision directories remain available to pinned tasks. A failed update leaves the previous active revision unchanged. A verified inactive revision left by interrupted activation can be activated on retry.

`RunUpdates` checks at startup and every 15 minutes plus up to 10% jitter by default; callers may select another interval. `CheckUpdates` and `Update` support manual checks. The caller owns cancellation and reports per-package errors. These APIs do not add generic approvals for publisher updates. Agent-created private extensions remain a separate approval flow.

Dependencies are exact `{ "id", "version", "digest" }` pins. They are not automatically downloaded. Install remains possible with missing dependencies; before exposing a capability, consumers must call `Resolve(revision)`, which returns dependency-first pinned revisions or a specific missing/cyclic dependency error. It never substitutes another installed version. Missing runtime credentials likewise are not an installation failure: the capability runtime must report the missing configuration when invoked.

`ReadFile(revision, path)` checks file membership and digest every time. Startup verifies active receipts and payload integrity. External edits are integrity failures, not new published revisions. Managed APIs provide no in-place editing. Read-only permissions are not a security boundary against the owning OS user, who can change their own permissions.

The store is an implementation library: registration of package-defined Envai teams, workflow schema validation, helper process execution and exposing installed package inventories must be wired by the manager/client runtime. Installing a package alone does not claim that those integrations or remote capabilities are available.
