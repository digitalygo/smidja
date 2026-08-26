package sdk

import (
	"context"
	"io/fs"
)

type Bundle struct {
	ID string

	Origin string

	FS fs.FS

	Extensions []Extension

	ConfigDefaults map[string]any

	MinimumHarness string
}

type BuildInfo struct {
	Origin string

	Version string

	Commit string
}

type RunFunc func(ctx context.Context, bundle Bundle, build BuildInfo, args []string) int

var Run RunFunc
