package main

import (
	"os"

	"github.com/digitalygo/smidja/internal/cli"
)

var version = "dev"

func main() {
	cli.Version = version
	if err := cli.Run(os.Args[1:]); err != nil {
		os.Exit(1)
	}
}
