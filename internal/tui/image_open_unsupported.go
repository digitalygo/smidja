//go:build !linux && !darwin

package tui

import "os"

func anchoredImageOpen(workspaceRoot, relative string, override func(string) (*os.File, error), hook imageOpenHook) (*os.File, error) {
	return nil, errImageOpenUnsupported
}
