# Creating compiled bundles

A compiled bundle is a full build of the smidja harness with your content and Go extensions baked in. Installing a bundle means running your binary; there is no plugin system to install into an existing binary. If you only want to share skills, prompts, agents, or config defaults without shipping code, write a [content-only package](creating-content-packages.md) instead.

Start from the public [smidja-bundle-template](https://github.com/digitalygo/smidja-bundle-template) repository and use its `Use this template` button. The template is the canonical example of everything described here, and this guide follows its files.

## Prerequisites

- Go 1.26 or newer.
- Git.
- A public or private GitHub repository you control, since the build identity and release flow are tied to it. The template builds in either, but the built-in self-updater uses unauthenticated GitHub release requests, so `smidja update` requires a public origin. Private distribution needs an external authenticated download and update process.

## Start from the template

1. Open [smidja-bundle-template](https://github.com/digitalygo/smidja-bundle-template) and create your repository with `Use this template`.
2. Clone your new repository.
3. Run the template's customization script with your origin:

   ```bash
   scripts/customize.sh github.com/<owner>/<repo>
   ```

The script rewrites the Go module path, the `bundleOrigin` default in `bundle.go`, and every Go import that references the template origin, then builds to verify. A repository generated with `Use this template` fails CI until the script runs, because CI checks that the origin matches the repository publishing the build. Do not rename the module or the identity variables by hand.

The origin you choose is permanent for a given install base: the binary validates it, prints it, and aims its self-updater at it. Pick `github.com/<you>/<your-repo>` matching the repository you will release from.

## What a bundle contains

```text
your-bundle/
  go.mod                    require on github.com/digitalygo/smidja, your harness pin
  main.go                   the single call into smidja.Run
  bundle.go                 bundle identity, your sdk.Bundle composition, and BuildInfo()
  content/                  embedded content, carried inside the binary
    skills/                 skill markdown files
    prompts/                prompt markdown files
    agents/                 agent markdown files
    config/defaults.env     key=value defaults parsed into ConfigDefaults
  extensions/               your compiled Go extension packages
  scripts/customize.sh      renames module, origin, and imports to your origin
  scripts/build-release.sh  cross-compiles the four release binaries plus checksums
  .github/workflows/        release workflow triggered by v* tags
```

`main.go` is the single call into the harness:

```go
os.Exit(smidja.Run(context.Background(), Bundle(), BuildInfo(), os.Args[1:]))
```

`bundle.go` carries the build identity and the composition. It declares the three ldflags variables `bundleOrigin`, `bundleVersion`, and `bundleCommit`, derives the bundle `ID` from the origin, and exposes `BuildInfo()`:

```go
var (
    bundleOrigin  = "github.com/digitalygo/smidja-bundle-template"
    bundleVersion = "dev"
    bundleCommit  = "none"
)

func BuildInfo() sdk.BuildInfo {
    return sdk.BuildInfo{
        Origin:  bundleOrigin,
        Version: bundleVersion,
        Commit:  bundleCommit,
    }
}
```

Embed the content tree with `go:embed` and root it where the harness expects it:

```go
//go:embed content
var contentFS embed.FS

var content fs.FS = mustSub(contentFS, "content")
```

With the filesystem rooted at `content/`, the harness finds `skills/`, `agents/`, and `prompts/` directly. It also looks for bundle settings at `settings.json` or `content/settings.json` in that filesystem; see [smidja settings](settings.md) for the schema.

## The sdk.Bundle contract

| Field | Type | Meaning |
|---|---|---|
| `ID` | `string` | The bundle name. The template derives it from the origin with `path.Base(bundleOrigin)`, so it is your repository name. It labels content that comes from the bundle tier. |
| `Origin` | `string` | The release repository, `github.com/owner/repo`. |
| `FS` | `fs.FS` | The embedded content tree. |
| `Extensions` | `[]sdk.Extension` | Compiled extensions, registered in the order you list them. |
| `ConfigDefaults` | `map[string]any` | Config defaults. Values are rendered with `fmt.Sprint`, so plain strings are the norm. |
| `MinimumHarness` | `string` | The minimum harness version you built against, as a canonical `vMAJOR.MINOR.PATCH` string. |

Startup validation is strict about identity: if `ID` or `Origin` is set, both must be set and `Origin` must parse as `github.com/owner/repo`, otherwise the binary exits with an error before anything else runs.

`MinimumHarness` is currently declarative. The harness never reads this field at runtime, so nothing enforces it. Keep it honest as documentation of the harness you built against, and verify compatibility yourself whenever you move the pin.

## Compiled Go extensions only

Extensions are Go packages that implement the sdk interfaces and are registered into your build. There is deliberately no plugin, subprocess, or WASM mechanism, and content packages cannot carry extensions. Adding or updating an extension always means cutting a new bundle release.

| Interface | Method | Purpose |
|---|---|---|
| `sdk.Extension` | `ID() string` | Required. Stable, unique identifier. |
| `sdk.SetupHook` | `Setup(api sdk.API) error` | Runs in the setup phase with the runtime API. |
| `sdk.LLMHook` | `RegisterLLMHooks(r sdk.LLMHookRegistry)` | Hooks around LLM calls. |
| `sdk.ToolHook` | `RegisterToolHooks(r sdk.ToolHookRegistry)` | Hooks around tool calls, including deny. |
| `sdk.SessionHook` | `RegisterSessionHooks(r sdk.SessionHookRegistry)` | Session lifecycle hooks. |

Implement only what you need. Registration fails for a nil extension, an empty `ID`, or a duplicate `ID`; the harness prints the failure to stderr and continues without that extension. Watch the startup output: a duplicate id means one of your extensions is not in the build.

## The immutable harness pin

Your `go.mod` requires one exact version of `github.com/digitalygo/smidja`. That pin is compiled in and immutable for the life of the release: nothing fetches harness code at runtime. Upgrade deliberately:

```bash
go get github.com/digitalygo/smidja@v0.2.0
go mod tidy
go test ./...
```

Then release a new bundle version. Users get the new harness only by installing your new release.

## Build identity

Three variables in `bundle.go` carry the build identity and are injected at link time: `bundleOrigin`, `bundleVersion`, and `bundleCommit`. The customization script already set the origin default to your module path, and its `dev` and `none` defaults keep plain local builds coherent. A release build injects the version and the commit:

```bash
go build -trimpath \
  -ldflags "-X main.bundleVersion=v1.2.3 \
            -X main.bundleCommit=$(git rev-parse HEAD)" \
  -o your-bundle .
```

`scripts/build-release.sh` does this for every release binary, injecting the origin from `go list -m`, the tag as the version, and the commit from git.

- The origin must be `github.com/owner/repo` or the binary refuses to start. The template also requires it to match the go.mod module path and the GitHub repository the build is released from, and CI fails when it does not.
- `bundleVersion` should equal the release tag you ship, because `smidja update --check` compares release tags against it.
- `bundleCommit` records the exact source revision.

Verify a build before shipping it:

```bash
./your-bundle version --json
```

```json
{"commit":"<full commit sha>","origin":"github.com/you/your-repo","version":"v1.2.3"}
```

## Local checks

Run this loop before tagging:

```bash
go build ./...
go vet ./...
go test ./...
go build -trimpath -ldflags "-X main.bundleVersion=v0.0.0 -X main.bundleCommit=$(git rev-parse HEAD)" -o your-bundle .
./your-bundle version --json
```

The built binary smoke (`version --json`) makes no provider calls and needs no provider credentials. Dependency resolution is the one step that can touch the network: on first use, Go downloads the immutable pinned harness module if it is not already in the module cache. With the cache warm, nothing here needs the network. The prompt run is an optional live smoke of content resolution and extension setup inside the real binary, and unlike everything above it needs configured provider credentials (`smidja auth login` or the provider's API key in the environment):

```bash
./your-bundle -p "list the skills you can see"
```

## Release assets

Pushing a `v*` tag triggers the release workflow. It runs `scripts/build-release.sh`, which cross-compiles `CGO_ENABLED=0` static binaries with `-trimpath` and the build identity above, writes `checksums.txt` in the standard `<64-hex>  <name>` layout sorted by asset name, and verifies it with `sha256sum -c` before publishing.

Exactly five assets land on the release, named after the base harness so the self-updater can find them:

- `smidja-linux-amd64`
- `smidja-linux-arm64`
- `smidja-darwin-amd64`
- `smidja-darwin-arm64`
- `checksums.txt` covering all four binaries.

Publish no other binaries. The updater selects the asset named exactly `smidja-<goos>-<goarch>` from your origin repository, so these are the names your users' `smidja update` downloads.

## Installing a bundle

Distribution is manual by design; there is no installer that merges files into an existing harness.

```bash
curl -LO https://github.com/<you>/<your-repo>/releases/latest/download/smidja-linux-amd64
curl -LO https://github.com/<you>/<your-repo>/releases/latest/download/checksums.txt
grep "  smidja-linux-amd64$" checksums.txt | sha256sum -c -
chmod +x smidja-linux-amd64
mv smidja-linux-amd64 ~/.local/bin/<your-bundle-name>
```

A failed checksum must stop the install; the updater applies the same rule and leaves the current binary untouched when a download does not match.

## Self-update behavior

`smidja update` reads the origin baked into the running binary and queries the releases of that repository.

- It is Linux only. On any other GOOS it fails with an unsupported-platform error.
- It fetches `releases/latest`, or `releases/tags/<version>` with `--version`, and selects the asset named exactly `smidja-<goos>-<goarch>`, for example `smidja-linux-amd64`.
- It requires a `checksums.txt` asset with exactly one matching entry, verifies the download against it, then replaces the executable atomically.
- `--check` compares the latest tag against the baked version numerically, ignoring any suffix after the patch field.
- It sends unauthenticated requests to the GitHub API, so the origin repository must be public. A private origin is indistinguishable from a missing release: the API answers 404 and the updater reports it.

The template can build and release from a private repository just as well, but its releases stay invisible to the unauthenticated updater. Distributing a private bundle means an external, authenticated download and update process instead of `smidja update`.

The updater has an optional self-check that would run `version --json` on the downloaded binary and compare its origin with the baked origin and its version with the release tag before replacing. The `smidja update` command does not enable it: a download only has to pass the checksum check to be installed.

Because the template publishes exactly the asset names above, a bundle built from it supports `smidja update` out of the box: updates arrive through the same self-updater as the base harness, pointed at your origin repository. If you publish differently named assets, the updater finds no matching asset and changes nothing.

## Versioning

Tag releases `vMAJOR.MINOR.PATCH`, for example `v1.2.3`. The build script accepts an optional prerelease suffix such as `v1.2.3-beta.1`, while the update comparison ignores the suffix when ranking versions. Keep the baked `bundleVersion` identical to the tag so `smidja version` and the update check agree.

## Trust model

Everything is MIT and there is no central build authority: whoever forks and builds owns the result. Publishing a bundle means asking users to run your compiled code with full workspace access. `checksums.txt` provides integrity for a download, not authenticity of the publisher; the identity inside the binary is whatever you inject at link time. Be the kind of publisher whose tags never move.
