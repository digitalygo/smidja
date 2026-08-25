package extensions

import (
	"fmt"
	"os"
)

// Logger receives diagnostics about extension failures during setup and
// dispatch. Implementations must be safe for concurrent use: dispatch can
// run in parallel across events, and the registry can be registered to
// while a dispatch is in flight.
type Logger interface {
	// Logf writes one formatted diagnostic line.
	Logf(format string, args ...any)
}

// DefaultLogger returns the logger used when none is injected: formatted
// lines to standard error. Tests inject a recorder through
// Runtime.SetLogger instead.
func DefaultLogger() Logger {
	return stderrLogger{}
}

// stderrLogger is the default Logger implementation.
type stderrLogger struct{}

// Logf writes one formatted line to standard error.
func (stderrLogger) Logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "extensions: "+format+"\n", args...)
}
