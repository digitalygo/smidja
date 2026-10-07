package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/agents"
	"github.com/digitalygo/smidja/internal/config"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/loopdetector"
	"github.com/digitalygo/smidja/internal/models"
	"github.com/digitalygo/smidja/internal/openrouter"
	"github.com/digitalygo/smidja/internal/retry"
	"github.com/digitalygo/smidja/internal/subagent"
	"github.com/digitalygo/smidja/sdk"
)

type childPreparerAdapter struct {
	*contextPreparerAdapter
}

func (a childPreparerAdapter) ForceSafety() {
	a.contextPreparerAdapter.forceSafety()
}

func (a childPreparerAdapter) DrainCompactions() []*agent.CompactionEntry {
	return a.contextPreparerAdapter.drain()
}

func childPreparerFactory(cfg *config.Config, modelReg *models.Registry) func(model, wire string, client agent.Client) (agents.Preparer, error) {
	return func(model, wire string, client agent.Client) (agents.Preparer, error) {
		if cfg == nil {
			return nil, errors.New("no configuration is available for child context management")
		}
		adapter, err := newModelPreparer(*cfg, modelReg, model, wire, subagent.NewOpenRouterSelector(client))
		if err != nil {
			return nil, err
		}
		return childPreparerAdapter{adapter}, nil
	}
}

func childClientBuilder(d *Deps, host *hostRuntime, modelReg *models.Registry, providers *extensions.ProviderRegistry, base agent.Client, baseSeam bool, rootProvider string) func(agents.Definition, agents.Parent) (agents.Client, error) {
	return func(definition agents.Definition, parent agents.Parent) (agents.Client, error) {
		return childClientFor(d, host, modelReg, providers, base, baseSeam, rootProvider, definition, parent)
	}
}

func childClientFor(d *Deps, host *hostRuntime, modelReg *models.Registry, providers *extensions.ProviderRegistry, base agent.Client, baseSeam bool, rootProvider string, definition agents.Definition, parent agents.Parent) (agents.Client, error) {
	override := strings.TrimSpace(definition.Model) != ""
	model := strings.TrimSpace(definition.Model)
	if model == "" {
		model = strings.TrimSpace(parent.Model)
	}
	if model == "" {
		return agents.Client{}, errors.New("no model is selected for the parent session")
	}
	if override {
		if _, known := hostModelInfo(modelReg, providers, model); !known {
			return agents.Client{}, fmt.Errorf("unknown model %q", clampText(model))
		}
	}
	provider := strings.TrimSpace(parent.Provider)
	if provider == "" {
		provider = rootProvider
	}
	client := base
	seam := baseSeam
	if host != nil {
		seam = parent.ReasoningSeam
	}
	wire := ""
	fromProvider := false
	if providers != nil {
		if entry, ok := providers.FindModel(model); ok {
			provider = entry.Name
			wire = model
			providerClient, ok := providerEntryClient(d, host, providers, provider)
			if !ok {
				return agents.Client{}, fmt.Errorf("provider %q is no longer registered for model %q", clampText(provider), clampText(model))
			}
			client = providerClient
			seam = false
			fromProvider = true
		}
	}
	if !fromProvider {
		snapshotWire := ""
		if !override && model == strings.TrimSpace(parent.Model) {
			snapshotWire = strings.TrimSpace(parent.WireModel)
		}
		resolved, ok := resolveWireModel(provider, model)
		switch {
		case ok:
			wire = resolved
		case snapshotWire != "":
			if _, fixed := fixedDeploymentTransports[provider]; !fixed {
				return agents.Client{}, fmt.Errorf("model %q has no verified wire model for the active transport %q", clampText(model), clampText(provider))
			}
			wire = snapshotWire
		default:
			return agents.Client{}, fmt.Errorf("model %q has no verified wire model for the active transport %q", clampText(model), clampText(provider))
		}
	}
	if client == nil {
		return agents.Client{}, errors.New("no parent client is available for the child")
	}
	thinking, thinkingSet := definition.Thinking, definition.ThinkingSet
	explicit := thinkingSet
	if !thinkingSet {
		thinking, thinkingSet = parent.Thinking, parent.ThinkingSet
	}
	applied, appliedSet, err := resolveChildThinking(modelReg, providers, model, thinking, thinkingSet, explicit, seam)
	if err != nil {
		return agents.Client{}, err
	}
	if seam && appliedSet {
		if directive, ok := childReasoningDirective(modelReg, providers, model, applied); ok {
			client = newFixedReasoningClient(client, directive)
		}
	}
	return agents.Client{Client: client, Model: model, Wire: wire, Provider: provider, Thinking: applied, ThinkingSet: appliedSet, ReasoningSeam: seam}, nil
}

func resolveChildThinking(modelReg *models.Registry, providers *extensions.ProviderRegistry, model string, level sdk.ThinkingLevel, set bool, explicit bool, seam bool) (sdk.ThinkingLevel, bool, error) {
	if !set || level == "" || level == sdk.ThinkingDefault {
		return sdk.ThinkingDefault, false, nil
	}
	if !seam {
		if explicit {
			return "", false, fmt.Errorf("%w: the active transport has no request-side reasoning seam", sdk.ErrUnsupported)
		}
		return sdk.ThinkingDefault, false, nil
	}
	info, known := hostModelInfo(modelReg, providers, model)
	switch level {
	case sdk.ThinkingOff:
		if !known || !info.Reasoning.Known || !info.Reasoning.Supported {
			return sdk.ThinkingDefault, false, nil
		}
		if info.Reasoning.Mandatory {
			if explicit {
				return "", false, fmt.Errorf("%w: model %q requires reasoning", sdk.ErrUnsupported, clampText(model))
			}
			return sdk.ThinkingDefault, false, nil
		}
		return sdk.ThinkingOff, true, nil
	default:
		if !known || !info.Reasoning.Known || !info.Reasoning.Supported {
			if explicit {
				return "", false, fmt.Errorf("%w: model %q does not support reasoning effort", sdk.ErrUnsupported, clampText(model))
			}
			return sdk.ThinkingDefault, false, nil
		}
		if !info.Reasoning.EffortSelection {
			if explicit {
				return "", false, fmt.Errorf("%w: model %q does not expose reasoning effort selection", sdk.ErrUnsupported, clampText(model))
			}
			return sdk.ThinkingDefault, false, nil
		}
		if !info.Reasoning.AllowsEffort(string(level)) {
			if explicit {
				return "", false, fmt.Errorf("model %q does not allow effort %q", clampText(model), level)
			}
			return sdk.ThinkingDefault, false, nil
		}
		return level, true, nil
	}
}

func childReasoningDirective(modelReg *models.Registry, providers *extensions.ProviderRegistry, model string, level sdk.ThinkingLevel) (openrouter.ReasoningDirective, bool) {
	info, known := hostModelInfo(modelReg, providers, model)
	if level == sdk.ThinkingOff {
		if !known || !info.Reasoning.Known || !info.Reasoning.Supported {
			return openrouter.ReasoningDirective{}, false
		}
		return openrouter.ReasoningDirective{Disable: true}, true
	}
	if !known || !info.Reasoning.AllowsEffort(string(level)) {
		return openrouter.ReasoningDirective{}, false
	}
	return openrouter.ReasoningDirective{Effort: string(level)}, true
}

type fixedReasoningClient struct {
	base      agent.Client
	directive openrouter.ReasoningDirective
}

var _ agent.Client = (*fixedReasoningClient)(nil)

func newFixedReasoningClient(base agent.Client, directive openrouter.ReasoningDirective) agent.Client {
	if base == nil {
		return nil
	}
	return &fixedReasoningClient{base: base, directive: directive}
}

func (c *fixedReasoningClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	return c.base.StreamTurn(openrouter.WithReasoning(ctx, c.directive), req, onText, onThinking)
}

type childExecutorConfig struct {
	catalog       agents.Catalog
	parentCatalog *extensions.ToolCatalog
	sessionsRoot  string
	cwd           string
	cfg           *config.Config
	modelReg      *models.Registry
	providers     *extensions.ProviderRegistry
	baseClient    agent.Client
	baseSeam      bool
	rootProvider  string
	d             *Deps
	host          *hostRuntime
	retryPolicy   agent.RetryPolicy
}

func newChildExecutor(cfg childExecutorConfig) (*agents.Executor, error) {
	return agents.NewExecutor(agents.Dependencies{
		Catalog:     func() agents.Catalog { return cfg.catalog },
		ParentTools: func() agent.ToolCatalog { return cfg.parentCatalog },
		Client:      childClientBuilder(cfg.d, cfg.host, cfg.modelReg, cfg.providers, cfg.baseClient, cfg.baseSeam, cfg.rootProvider),
		Preparer:    childPreparerFactory(cfg.cfg, cfg.modelReg),
		Detector: func() agent.LoopDetector {
			return newLoopDetectorAdapter(loopdetector.New(loopdetector.DefaultConfig()))
		},
		Retry:             retryAdapter,
		RetryPolicy:       cfg.retryPolicy,
		IsContextOverflow: retry.IsContextOverflow,
		SessionsRoot:      cfg.sessionsRoot,
		Cwd:               cfg.cwd,
		DepthLimit:        agents.DefaultDepthLimit,
	})
}
