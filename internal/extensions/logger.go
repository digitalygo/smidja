package extensions

import (
	"fmt"
	"os"
)

type Logger interface {
	Logf(format string, args ...any)
}

func DefaultLogger() Logger {
	return stderrLogger{}
}

type stderrLogger struct{}

func (stderrLogger) Logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "extensions: "+format+"\n", args...)
}
