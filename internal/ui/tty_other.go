//go:build !linux && !darwin

package ui

import (
	"os"
)

func isTerminalFile(file *os.File) bool {
	return false
}
