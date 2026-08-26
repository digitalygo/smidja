package sdk

import (
	"context"
	"io/fs"
)

// Bundle is the complete set of baked-in contents one smidja package
// ships: the embedded resources, the extensions, and the configuration
// defaults. A package main builds the harness binary with its bundle; a
// bare harness ships an empty Bundle.
type Bundle struct {
	// ID is the stable package identifier, for example "digitalygo" or
	// "example/smidja-tooling".
	ID string

	// Origin is the source repository of the package in
	// "github.com/owner/repo" form: no scheme, no trailing slash, for
	// example "github.com/digitalygo/smidja". It is used by the
	// self-update flow, and the harness validates the format before
	// running a packaged build.
	Origin string

	// FS is the embedded filesystem with the package contents (skills,
	// agents, prompts, default config). It may be nil for a harness
	// without baked-in resources. The harness carries the bundle through
	// to the CLI, which will resolve content from FS in a later phase;
	// nothing consumes it yet.
	FS fs.FS

	// Extensions lists the extensions baked into the package, in load
	// order.
	Extensions []Extension

	// ConfigDefaults carries the package's configuration defaults keyed
	// by configuration name, applied below user configuration.
	ConfigDefaults map[string]any

	// MinimumHarness is the minimum smidja harness version this package
	// requires, in semantic version form. The harness refuses to load
	// the bundle when the running version is lower.
	MinimumHarness string
}

// BuildInfo identifies one build of the harness binary.
type BuildInfo struct {
	// Origin is the source repository of the harness binary in
	// "github.com/owner/repo" form (no scheme), used by the
	// self-update flow.
	Origin string

	// Version is the semantic version of the build, injected at link
	// time.
	Version string

	// Commit is the source revision the build was produced from,
	// injected at link time.
	Commit string
}

// RunFunc is the signature of the smidja application entry point. A
// package main wires it to run the harness with its bundle baked in; the
// return value is the process exit code.
type RunFunc func(ctx context.Context, bundle Bundle, build BuildInfo, args []string) int

// Run is the application entry point of the current build. The binary
// (cmd/smidja or a package main) assigns it; a nil Run means the binary
// was not wired to a runner and any call will panic. Package builds
// override it to embed their bundle before invoking the harness.
var Run RunFunc
