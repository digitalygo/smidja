#!/usr/bin/env bash
# build.sh: build the smidja static stripped binary into bin/smidja.
#
# Produces a CGO-free, statically linked, stripped binary with the short
# git revision baked into main.version (printed by `smidja --version`)
# and into internal/buildinfo, the link-time identity the self-updater
# uses for version comparison and self-check. Prints the resulting binary
# size in bytes as its only stdout line.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

VERSION="$(git rev-parse --short HEAD 2>/dev/null || echo dev)"
mkdir -p bin

CGO_ENABLED=0 go build \
  -trimpath \
  -ldflags "-s -w \
    -X main.version=${VERSION} \
    -X github.com/digitalygo/smidja/internal/buildinfo.smidjaVersion=${VERSION} \
    -X github.com/digitalygo/smidja/internal/buildinfo.smidjaCommit=${VERSION}" \
  -o bin/smidja \
  ./cmd/smidja

wc -c < bin/smidja
