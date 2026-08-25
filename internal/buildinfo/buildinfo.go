// Package buildinfo exposes the build-time identity of the smidja binary:
// the upstream repository origin, the release version, and the exact
// source commit it was compiled from.
//
// The three values live in package-level variables so they can be injected
// at link time with the standard -ldflags -X mechanism. The canonical
// wiring is:
//
//	go build -ldflags "\
//	  -X github.com/digitalygo/smidja/internal/buildinfo.smidjaOrigin=github.com/digitalygo/smidja \
//	  -X github.com/digitalygo/smidja/internal/buildinfo.smidjaVersion=v1.2.3 \
//	  -X github.com/digitalygo/smidja/internal/buildinfo.smidjaCommit=abc1234" \
//	  ./cmd/smidja
//
// Variables that are not injected keep their defaults, so a plain
// `go build` still produces a coherent identity: origin
// "github.com/digitalygo/smidja", version "dev", commit "none".
package buildinfo

import (
	"encoding/json"
	"fmt"
)

// smidjaOrigin, smidjaVersion, and smidjaCommit are the link-time
// variables. Nothing reads them except Current; the lower-case names keep
// the ldflags wiring stable while signaling that the read API is Current.
var (
	smidjaOrigin  = "github.com/digitalygo/smidja"
	smidjaVersion = "dev"
	smidjaCommit  = "none"
)

// Info is the immutable build identity of the running binary.
type Info struct {
	Origin  string // upstream repository, "github.com/owner/repo"
	Version string // release version, e.g. "v1.2.3"; "dev" when unset
	Commit  string // source commit, e.g. "abc1234"; "none" when unset
}

// Current returns the build identity of the running binary as injected at
// link time.
func Current() Info {
	return Info{Origin: smidjaOrigin, Version: smidjaVersion, Commit: smidjaCommit}
}

// JSON renders the identity as a compact JSON object with its keys in
// sorted order: {"commit":...,"origin":...,"version":...}. The
// self-updater's self-check parses it back with encoding/json.
func (i Info) JSON() string {
	b, err := json.Marshal(map[string]string{
		"origin":  i.Origin,
		"version": i.Version,
		"commit":  i.Commit,
	})
	if err != nil {
		// Info holds only strings, so Marshal cannot fail; the fallback
		// keeps the sorted-key contract even if that assumption breaks.
		return fmt.Sprintf(`{"commit":%q,"origin":%q,"version":%q}`, i.Commit, i.Origin, i.Version)
	}
	return string(b)
}
