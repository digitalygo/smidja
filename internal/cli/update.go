package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/digitalygo/smidja/internal/buildinfo"
	"github.com/digitalygo/smidja/internal/update"
)

// runUpdate implements "smidja update [--check] [--version <version>]"
// over internal/update.Client with the running build's identity.
// --check prints availability only; the apply path prints coarse progress
// lines around the atomic download/verify/rename. Non-linux platforms
// fail with update.ErrUnsupportedPlatform before any network access.
func runUpdate(args []string, d *cliDeps) error {
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
			printUpdateUsage(d.stderr)
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
			fmt.Fprintf(d.stdout, "smidja %s is up to date\n", buildinfo.Current().Version)
			return nil
		}
		fmt.Fprintf(d.stdout, "update available: %s\n", latest.Version)
		fmt.Fprintf(d.stdout, "  %s\n", latest.URL)
		return nil
	}

	target := version
	if target == "" {
		// Resolve the latest release first so the progress lines and the
		// pinned apply target name the same version.
		latest, err := client.Check(ctx)
		if err != nil {
			return fail(d, updateFailure(err))
		}
		target = latest.Version
		fmt.Fprintf(d.stdout, "update available: %s\n", target)
	}
	fmt.Fprintf(d.stdout, "downloading %s...\n", target)
	if err := client.Apply(ctx, target); err != nil {
		return fail(d, updateFailure(err))
	}
	fmt.Fprintf(d.stdout, "installed %s\n", target)
	return nil
}

// newUpdateClient builds the update client for this invocation. The
// default uses the real GitHub API base and the running binary's path;
// tests substitute a client pointed at an httptest server.
func newUpdateClient(d *cliDeps) *update.Client {
	if d != nil && d.newUpdateClient != nil {
		return d.newUpdateClient()
	}
	return &update.Client{Origin: buildinfo.Current()}
}

// updateFailure maps updater sentinel errors to clear user-facing
// messages. Sentinels that already read clearly pass through.
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
