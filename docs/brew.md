# Brew tap

Smidja distributes a single static binary with no runtime dependencies. On macOS the install path is Homebrew: the public tap at [digitalygo/homebrew-smidja](https://github.com/digitalygo/homebrew-smidja) hosts the authoritative source formula at `Formula/smidja.rb`, so install and upgrade work like any other tap.

## Install and upgrade

Installation needs no manual tap step; brew resolves the `digitalygo/smidja/smidja` formula name automatically:

```bash
brew install digitalygo/smidja/smidja
```

The explicit tap form is equivalent:

```bash
brew tap digitalygo/smidja
brew install digitalygo/smidja/smidja
```

Upgrades come through the same channel:

```bash
brew update && brew upgrade smidja
```

The formula's `test` block runs `smidja -version` and asserts the output carries the formula version, so `brew test digitalygo/smidja/smidja` verifies the installed binary.

## What the formula builds

The tap formula is source-based, not bottle-based:

- `url` points at the tagged GitHub source archive, for example `https://github.com/digitalygo/smidja/archive/refs/tags/v0.3.0.tar.gz`.
- The build compiles with Go and injects the build identity through the same `-ldflags` flags as [build-release.sh](../scripts/build-release.sh): the buildinfo origin and version, so `smidja -version` and `smidja version --json` report the installed release.
- GitHub source archives carry no git history, so the JSON build commit is `none`. Release binaries built from a git checkout carry the exact commit.
- The formula compiles from the source archive. The downloadable release binaries published by the [release workflow](../.github/workflows/release.yml) are prebuilt artifacts for direct installs and are not what brew uses.

## In-repo formula reference

The formula at [brew/smidja.rb](../brew/smidja.rb) in this repository is a structural reference only: its `sha256` is the all-zero digest placeholder, so it is not installable and is not the formula users install. The authoritative formula lives at `Formula/smidja.rb` in the tap repository. Any structural change (new build flags, changed test block, different livecheck URL) lands in the in-repo reference first and is mirrored into the tap.

## Releasing a new version

Every release requires two metadata updates in the tap's `Formula/smidja.rb`:

1. Point `url` at the new tagged source archive.
2. Replace `sha256` with the digest of that archive.

The `livecheck` block points at the [GitHub releases page](https://github.com/digitalygo/smidja/releases) with the standard `v`-prefixed version regex. It detects newer releases and reports the formula as outdated, but it does not change formula metadata automatically; a maintainer still commits the `url` and `sha256` bump.

## Updater boundary

Homebrew owns Cellar upgrades for brew-managed installs. The built-in updater behind `smidja update` is Linux-only and should not be used on a Homebrew-managed macOS install; upgrade with brew instead.
