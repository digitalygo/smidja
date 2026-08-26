// Package smidja is the public composition seam of the harness: it turns
// a Bundle (the baked-in contents one smidja package ships) and a
// BuildInfo (the binary's build identity) into a running CLI. A package
// main imports this package, embeds its resources, and calls Run; the
// bare harness (cmd/smidja) calls the CLI directly with an empty bundle.
//
// Run wires the whole harness: it validates the bundle, loads the config
// with the bundle's ConfigDefaults applied, builds the workspace,
// OpenRouter client, built-in tools, session store, model registry, and
// the extension runtime with the bundle's extensions registered, and
// delegates the invocation to the CLI, which receives the composed
// components as an injected environment instead of building its own.
// The bundle's FS is carried through to the CLI for future content
// resolution; nothing consumes it yet.
package smidja

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/digitalygo/smidja/internal/cli"
	"github.com/digitalygo/smidja/internal/config"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/models"
	"github.com/digitalygo/smidja/internal/openrouter"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/internal/tools"
	"github.com/digitalygo/smidja/internal/workspace"
	"github.com/digitalygo/smidja/sdk"
)

// Run composes the full harness from a bundle and executes the CLI.
// It returns the process exit code: 0 on a clean exit, 1 on any failure
// (an invalid bundle, a wiring error, or a CLI error). Diagnostics are
// printed to stderr; the CLI prints its own "smidja: <err>" lines, so
// Run only maps a non-nil CLI error to exit status 1.
//
// An empty Bundle (zero ID and Origin) is the bare harness and skips
// validation. A bundle with a partial identity, or an Origin that is not
// "github.com/owner/repo", is rejected before anything runs.
func Run(ctx context.Context, bundle sdk.Bundle, info sdk.BuildInfo, args []string) int {
	if err := validateBundle(bundle); err != nil {
		fmt.Fprintf(os.Stderr, "smidja: %v\n", err)
		return 1
	}
	deps, err := compose(ctx, bundle, info)
	if err != nil {
		fmt.Fprintf(os.Stderr, "smidja: %v\n", err)
		return 1
	}
	if err := cli.RunWithDeps(args, deps); err != nil {
		return 1
	}
	return 0
}

// validateBundle checks the identity contract of a packaged bundle: a
// non-empty ID and an Origin in "github.com/owner/repo" form (no scheme,
// no trailing slash), the same format the self-updater parses. The bare
// harness ships an empty bundle (both fields empty) and is always valid;
// a bundle with exactly one of the two set is malformed. MinimumHarness
// version gating lands with the release-version block.
func validateBundle(b sdk.Bundle) error {
	if b.ID == "" && b.Origin == "" {
		return nil // bare harness
	}
	if b.ID == "" {
		return fmt.Errorf("bundle: empty ID")
	}
	if b.Origin == "" {
		return fmt.Errorf("bundle: empty Origin")
	}
	parts := strings.Split(b.Origin, "/")
	if len(parts) != 3 || parts[0] != "github.com" || parts[1] == "" || parts[2] == "" {
		return fmt.Errorf("bundle: Origin %q must be github.com/owner/repo", b.Origin)
	}
	return nil
}

// compose builds the CLI environment of one invocation from the bundle
// and build info: the process seams, the bundle itself, and the pre-built
// components the CLI consumes instead of building its own.
func compose(ctx context.Context, bundle sdk.Bundle, info sdk.BuildInfo) (*cli.Deps, error) {
	env := os.Getenv
	getwd := os.Getwd
	home := func() string {
		h, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		return h
	}

	// Configuration: the bundle's ConfigDefaults sit between the .env
	// values and the compiled-in defaults, so a package can steer the
	// harness without overriding the user's environment.
	cfg, err := config.LoadWithDefaults(env, getwd, home, bundleDefaults(bundle.ConfigDefaults))
	if err != nil {
		return nil, err
	}

	// Workspace and the built-in tools over it.
	ws, err := workspace.New(cfg.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	toolSet := tools.All(tools.Deps{
		Workspace:      ws,
		ExecTimeoutSec: cfg.ExecTimeoutSecs,
		MaxOutputBytes: cfg.MaxOutputBytes,
	})

	// Session store and provider client.
	store, err := session.NewStore(cfg.SessionDir)
	if err != nil {
		return nil, err
	}
	client := openrouter.New(cfg.OpenRouterURL, cfg.APIKey, nil)

	// Extension runtime with the bundle's extensions registered, in
	// bundle order. A bad extension is logged and skipped (per-extension
	// error isolation); the runtime is started by the CLI, exactly once
	// per invocation, because the setup phase is session wiring.
	registry := extensions.NewRegistry()
	for _, ext := range bundle.Extensions {
		if err := registry.Register(ext); err != nil {
			fmt.Fprintf(os.Stderr, "smidja: extension: %v\n", err)
		}
	}
	runtime := extensions.NewRuntime(registry)

	return &cli.Deps{
		Context: ctx,
		Env:     env,
		Getwd:   getwd,
		Home:    home,
		Stdin:   os.Stdin,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		FetchModels: func(ctx context.Context) ([]models.ModelInfo, error) {
			return models.FetchOpenRouterModels(ctx, nil)
		},
		Bundle:           bundle,
		BuildInfo:        info,
		Config:           cfg,
		Client:           client,
		Tools:            toolSet,
		Store:            store,
		ModelRegistry:    models.NewRegistry(),
		ExtensionRuntime: runtime,
	}, nil
}

// bundleDefaults normalizes the bundle's ConfigDefaults (keyed by
// configuration name, values of any type) into the string map the config
// package consumes, keyed by environment variable name. Non-string
// values are rendered with fmt.Sprint, so numbers and booleans survive
// the round trip.
func bundleDefaults(m map[string]any) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = fmt.Sprint(v)
	}
	return out
}
