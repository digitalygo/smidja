package cli

import (
	"context"
	"strings"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/config"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/extensionui"
	"github.com/digitalygo/smidja/internal/models"
	"github.com/digitalygo/smidja/internal/providers/manifest"
	"github.com/digitalygo/smidja/internal/tools"
	"github.com/digitalygo/smidja/internal/workspace"
	"github.com/digitalygo/smidja/sdk"
)

type extensionBootstrap struct {
	cfg        *config.Config
	configErr  error
	modelReg   *models.Registry
	toolSet    []agent.Tool
	catalog    *extensions.ToolCatalog
	commands   *extensions.CommandCatalog
	uiRegistry *extensionui.Registry
	flags      *extensions.FlagRegistry
	providers  *extensions.ProviderRegistry
	events     *extensions.CustomEventBus
	runtime    *extensions.Runtime
	host       *hostRuntime
	api        sdk.API

	toolsRegistered bool
	promptSlot      *promptCommandSlot
	promptCommand   string
}

const customEventDrainJoinTimeout = 5 * time.Second

var coreExtensionFlagNames = []string{
	"p",
	"model",
	"system",
	"provider",
	"continue",
	"tui-mode",
	"use-theme",
	"version",
	"allow-workspace-mcp",
	"h",
	"help",
}

func bootstrapExtensions(d *Deps) (*extensionBootstrap, error) {
	cfg := d.Config
	configErr := error(nil)
	if cfg == nil {
		loaded, err := loadChatConfig(d)
		if err != nil {
			configErr = err
			cfg = fallbackChatConfig(d)
		} else {
			cfg = loaded
		}
	}
	modelReg := d.ModelRegistry
	if modelReg == nil {
		modelReg = models.NewRegistry()
	}
	toolSet := d.Tools
	if len(toolSet) == 0 {
		if ws, err := workspace.New(cfg.WorkspaceRoot); err == nil {
			toolSet = tools.All(tools.Deps{
				Workspace:      ws,
				ExecTimeoutSec: cfg.ExecTimeoutSecs,
				MaxOutputBytes: cfg.MaxOutputBytes,
			})
		}
	}
	catalog := extensions.NewToolCatalog()
	toolsRegistered := false
	for _, tool := range toolSet {
		if err := catalog.Register(tool); err != nil {
			return nil, err
		}
		toolsRegistered = true
	}
	commands := extensions.NewCommandCatalog()
	uiRegistry := extensionui.NewRegistry()
	flags := extensions.NewFlagRegistry(coreExtensionFlagNames...)
	providers := extensions.NewProviderRegistry(reservedProviderNames()...)
	events := extensions.NewCustomEventBus()
	promptSlot := newPromptCommandSlot()
	promptCommand := promptSlot.bind(commands)
	ctx := d.Context
	if ctx == nil {
		ctx = context.Background()
	}
	host := newHostRuntime(ctx, "", nil, catalog)
	host.setReady(false)
	host.setModelRegistry(modelReg)
	host.setProviders(providers)
	host.setEventBus(events)
	api := extensions.NewAPI(extensions.APIOptions{
		Catalog:       catalog,
		Commands:      commands,
		ResolveConfig: cfg.Default,
		UI:            uiRegistry,
		Host:          host.hostOptions(),
		Flags:         flags,
		Providers:     providers,
		Events:        events,
	})
	host.bindAPI(api)
	runtime := d.ExtensionRuntime
	if runtime == nil {
		runtime = extensions.NewRuntime(extensions.NewRegistry())
	}
	runtime.SetAPI(func() sdk.API { return api })
	runtime.SetUIRegistry(uiRegistry)
	runtime.SetContext(func() sdk.HandlerContext { return host.context() })
	if err := runtime.Start(); err != nil {
		events.Close()
		return nil, err
	}
	return &extensionBootstrap{
		cfg:             cfg,
		configErr:       configErr,
		modelReg:        modelReg,
		toolSet:         toolSet,
		catalog:         catalog,
		commands:        commands,
		uiRegistry:      uiRegistry,
		flags:           flags,
		providers:       providers,
		events:          events,
		runtime:         runtime,
		host:            host,
		api:             api,
		toolsRegistered: toolsRegistered,
		promptSlot:      promptSlot,
		promptCommand:   promptCommand,
	}, nil
}

func (b *extensionBootstrap) close() {
	if b == nil || b.host == nil {
		return
	}
	b.host.shutdown()
	if b.events != nil {
		b.events.Wait(customEventDrainJoinTimeout)
	}
	b.host.waitCompacts()
	b.host.waitCallbacks()
}

func fallbackChatConfig(d *Deps) *config.Config {
	cwd := ""
	if d != nil && d.Getwd != nil {
		if resolved, err := d.Getwd(); err == nil {
			cwd = resolved
		}
	}
	if strings.TrimSpace(cwd) == "" {
		cwd = "."
	}
	return &config.Config{
		Model:           models.DefaultModelID,
		WorkspaceRoot:   cwd,
		ExecTimeoutSecs: 30,
		MaxOutputBytes:  1 << 20,
	}
}

func reservedProviderNames() []string {
	out := make([]string, 0, len(manifest.All)+len(oauthProviders)+len(transportModelProviderAliases)+2)
	for _, spec := range manifest.All {
		out = append(out, spec.ID)
	}
	for _, provider := range oauthProviders {
		out = append(out, provider.id, provider.name)
	}
	for alias, canonical := range transportModelProviderAliases {
		out = append(out, alias, canonical)
	}
	out = append(out, openrouterProviderName)
	return out
}
