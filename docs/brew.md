# Brew tap

Smidja distributes a single static binary with no runtime dependencies. On macOS the install path is Homebrew: a tap repository at `github.com/digitalygo/homebrew-smidja` hosts the formula, so `brew install digitalygo/tap/smidja` works like any other tap.

## Current status

The tap repository does not exist yet. What exists in this repository is the formula template at `brew/smidja.rb`: a source-based formula that Homebrew can audit, install, and test as soon as the tap repo is created and the first release is tagged.

## How the formula works

`brew/smidja.rb` is source-based, not bottle-based:

- `url` points at the GitHub release source archive, for example `https://github.com/digitalygo/smidja/archive/refs/tags/v0.1.0.tar.gz`.
- The build runs `go build` with the same `-ldflags` identity injection as the [release pipeline](https://github.com/digitalygo/smidja/blob/main/scripts/build-release.sh): `main.version` and the buildinfo origin and version, so `smidja -version` and `smidja version --json` report the installed release.
- The `test` block runs `smidja -version` and asserts the output carries the formula version, so `brew test digitalygo/tap/smidja` verifies the installed binary.
- The `livecheck` block points at `https://github.com/digitalygo/smidja/releases` with the standard `v`-prefixed version regex, so `brew update` and `brew upgrade` detect new releases automatically.

## Installing from the tap

Once the tap exists, installation works in the usual way:

```bash
brew tap digitalygo/homebrew-smidja
brew install smidja
```

Upgrades come through the same channel:

```bash
brew update
brew upgrade smidja
```

## Creating the tap

Creating the tap repository is a one-time release-task step, not part of this codebase:

1. Create the repository `github.com/digitalygo/homebrew-smidja`.
2. Copy `brew/smidja.rb` from this repository into `Formula/smidja.rb` in the tap.
3. For every tagged release, bump the `url` version and `sha256` in the tap's formula from the release source archive.

The formula in this repository stays the template: any structural change (new build flags, changed test, different livecheck URL) lands here first and is mirrored into the tap at the next release.

## Notes for tap maintainers

- Cellar installs must not self-update: the binary installed by Homebrew belongs to the brew lifecycle, so `smidja update` is intentionally not offered on macOS builds installed this way.
- The `sha256` placeholder in the template is the all-zeros digest; it must be replaced with the real source archive digest before the formula can be audited.
- The commit part of the build identity is not injected from a source archive, since GitHub archives carry no git history; the binary reports `none` for the commit until a bottle-based path exists.
