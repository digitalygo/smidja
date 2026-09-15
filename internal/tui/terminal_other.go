//go:build !linux && !darwin

package tui

import (
	"errors"
	"os"
)

type fallbackTerminalOps struct{}

func (fallbackTerminalOps) name() string { return "unsupported" }

var errUnsupportedPlatform = errors.New("tui: raw terminal mode is not supported on this platform")

func (fallbackTerminalOps) isTerminal(*os.File) bool { return false }

func (fallbackTerminalOps) makeRaw(*os.File) (*rawState, error) {
	return nil, errUnsupportedPlatform
}

func (fallbackTerminalOps) restoreState(*os.File, *rawState) error {
	return errUnsupportedPlatform
}

func (fallbackTerminalOps) windowSize(*os.File) (int, int, error) {
	return 0, 0, errUnsupportedPlatform
}

func init() {
	setDefaultTerminalOps(fallbackTerminalOps{})
}

func resizeSignalChannel() (chan os.Signal, func(), bool) {
	return nil, func() {}, false
}

func refreshTerminalDimensions() {}

func stopResizeWatcher() {}
