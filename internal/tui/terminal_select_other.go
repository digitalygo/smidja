//go:build !linux && !darwin

package tui

import "time"

func stdinReadableSelect(uintptr, time.Duration) (bool, error) {
	return true, nil
}
