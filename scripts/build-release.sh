#!/usr/bin/env bash
# build-release.sh: cross-compile the smidja release binaries and their
# checksums into outdir (default dist/).
#
# Produces CGO_ENABLED=0 static binaries for linux/amd64, linux/arm64,
# darwin/amd64, and darwin/arm64, named smidja-<goos>-<goarch> exactly as
# the self-updater (internal/update) selects them. Each binary is built
# with -trimpath and the full build identity injected via -ldflags:
# main.version (printed by `smidja -version`), buildinfo origin, version,
# and the exact source commit.
#
# checksums.txt is written in the standard "<64-hex>  <name>" layout,
# sorted by asset name, so both `sha256sum -c` and internal/update's
# findChecksum accept it.
#
# Usage: scripts/build-release.sh <version> [outdir]
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

# --- arguments -----------------------------------------------------------
if [[ $# -lt 1 ]]; then
  echo "usage: $0 <version> [outdir]" >&2
  exit 2
fi
version="$1"
outdir="${2:-dist}"

# Release tags are v-prefixed (the workflow triggers on v*) and
# CompareVersions in internal/update understands the prefix, so the
# version is baked in verbatim. A prerelease suffix after the patch
# number is allowed, e.g. v1.2.3-beta.1.
if [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+ ]]; then
  echo "build-release: version must look like v1.2.3, got '$version'" >&2
  exit 2
fi

commit="$(git rev-parse HEAD)"

# --- targets -------------------------------------------------------------
# (goos goarch) pairs; the asset name is smidja-<goos>-<goarch>.
targets=(
  linux amd64
  linux arm64
  darwin amd64
  darwin arm64
)

mkdir -p "$outdir"
# Drop stale artifacts from an earlier run so a release never carries
# outdated binaries or checksums.
rm -f "$outdir"/smidja-* "$outdir/checksums.txt"

ldflags="-s -w \
  -X main.version=${version} \
  -X github.com/digitalygo/smidja/internal/buildinfo.smidjaOrigin=github.com/digitalygo/smidja \
  -X github.com/digitalygo/smidja/internal/buildinfo.smidjaVersion=${version} \
  -X github.com/digitalygo/smidja/internal/buildinfo.smidjaCommit=${commit}"

for ((i = 0; i < ${#targets[@]}; i += 2)); do
  goos="${targets[i]}"
  goarch="${targets[i + 1]}"
  name="smidja-${goos}-${goarch}"
  echo "building ${name}"
  GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags "$ldflags" \
    -o "$outdir/$name" \
    ./cmd/smidja
done

# --- checksums -----------------------------------------------------------
# "<64-hex>  <name>" lines sorted by asset name. sha256sum is the norm;
# shasum -a 256 covers macOS hosts that lack the GNU utility (both emit
# the same two-space layout).
if command -v sha256sum >/dev/null 2>&1; then
  sumcmd=(sha256sum)
else
  sumcmd=(shasum -a 256)
fi
(
  cd "$outdir"
  "${sumcmd[@]}" smidja-* | sort -k2 > checksums.txt
  "${sumcmd[@]}" -c checksums.txt
)

echo "release artifacts in $outdir:"
ls -la "$outdir"
