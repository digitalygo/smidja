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
	"github.com/digitalygo/smidja/internal/packages"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/internal/tools"
	"github.com/digitalygo/smidja/internal/workspace"
	"github.com/digitalygo/smidja/sdk"
)

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

func validateBundle(b sdk.Bundle) error {
	if b.ID == "" && b.Origin == "" {
		return nil
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

	pkgStore, err := packages.Open(packages.DefaultRoot())
	if err != nil {
		return nil, err
	}
	pkgDefaults, err := pkgStore.ActiveConfigDefaults()
	if err != nil {
		return nil, err
	}
	bundleSettings, err := config.ReadBundleSettings(bundle.FS)
	if err != nil {
		return nil, err
	}

	cfg, err := config.LoadWithSources(env, getwd, home, config.DefaultsFromAny(bundle.ConfigDefaults), bundleSettings, pkgDefaults)
	if err != nil {
		return nil, err
	}

	ws, err := workspace.New(cfg.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	toolSet := tools.All(tools.Deps{
		Workspace:      ws,
		ExecTimeoutSec: cfg.ExecTimeoutSecs,
		MaxOutputBytes: cfg.MaxOutputBytes,
	})

	store, err := session.NewStore(cfg.SessionDir)
	if err != nil {
		return nil, err
	}
	client := openrouter.New(cfg.OpenRouterURL, cfg.APIKey, nil)

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
		ModelsCatalog:    &models.CatalogSource{BaseURL: cfg.ModelsCatalogURL},
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
