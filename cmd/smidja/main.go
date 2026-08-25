// Command smidja is the entry point of the smidja agentic coding harness.
//
// It forwards the raw arguments to the CLI package and exits non-zero when
// the CLI reports an error. The CLI itself prints "smidja: <err>" to
// stderr, so main only maps a non-nil result to exit status 1.
package main

import (
	"os"

	"github.com/digitalygo/smidja/internal/cli"
)

// version is the build version, injected at link time with
// -ldflags "-X main.version=<version>". The default is "dev".
var version = "dev"

func main() {
	cli.Version = version
	if err := cli.Run(os.Args[1:]); err != nil {
		os.Exit(1)
	}
}
