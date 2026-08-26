package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/digitalygo/smidja/internal/update"
)

func runUpdate(args []string, d *Deps) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	var (
		check   bool
		version string
	)
	fs.BoolVar(&check, "check", false, "check for an available update without applying it")
	fs.StringVar(&version, "version", "", "update to a specific release version (default: latest)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printUpdateUsage(d.Stderr)
			return nil
		}
		return fail(d, err)
	}

	client := newUpdateClient(d)
	ctx := context.Background()

	if check {
		latest, err := client.Check(ctx)
		if err != nil {
			return fail(d, updateFailure(err))
		}
		if !latest.Available {
			fmt.Fprintf(d.Stdout, "smidja %s is up to date\n", versionFor(d))
			return nil
		}
		fmt.Fprintf(d.Stdout, "update available: %s\n", latest.Version)
		fmt.Fprintf(d.Stdout, "  %s\n", latest.URL)
		return nil
	}

	target := version
	if target == "" {
		latest, err := client.Check(ctx)
		if err != nil {
			return fail(d, updateFailure(err))
		}
		target = latest.Version
		fmt.Fprintf(d.Stdout, "update available: %s\n", target)
	}
	fmt.Fprintf(d.Stdout, "downloading %s...\n", target)
	if err := client.Apply(ctx, target); err != nil {
		return fail(d, updateFailure(err))
	}
	fmt.Fprintf(d.Stdout, "installed %s\n", target)
	return nil
}

func newUpdateClient(d *Deps) *update.Client {
	if d != nil && d.NewUpdateClient != nil {
		return d.NewUpdateClient()
	}
	return &update.Client{Origin: buildIdentity(d)}
}

func updateFailure(err error) error {
	switch {
	case errors.Is(err, update.ErrChecksumMismatch):
		return fmt.Errorf("update: the downloaded binary failed its checksum and was not installed; the current binary is untouched")
	case errors.Is(err, update.ErrLocked):
		return fmt.Errorf("update: another update is in progress; retry once it finishes")
	default:
		return err
	}
}
