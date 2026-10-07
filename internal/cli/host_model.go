package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/config"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/models"
	"github.com/digitalygo/smidja/internal/openrouter"
	"github.com/digitalygo/smidja/internal/providers"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/sdk"
)

var (
	errHostModelEmpty        = errors.New("extensions: SetModel requires a model id")
	errHostModelUnknown      = errors.New("extensions: unknown model")
	errHostModelUnavailable  = errors.New("extensions: model changes are not available in this host")
	errHostModelWire         = errors.New("extensions: model has no verified wire model for the active transport")
	errHostModelEffort       = errors.New("extensions: the active thinking level is not supported by the requested model")
	errHostThinkingLevel     = errors.New("extensions: unknown thinking level")
	errHostThinkingMandatory = errors.New("extensions: the active model requires reasoning")
	errHostThinkingEffort    = errors.New("extensions: the active model does not allow this reasoning effort")
	errHostProviderActive    = errors.New("extensions: the provider is active; switch models before removing it")
)

func (h *hostRuntime) requestModel(m sdk.Model, expected *hostSessionHandle) error {
	modelID := strings.TrimSpace(m.ID)
	if modelID == "" {
		return errHostModelEmpty
	}
	if h.closed.Load() {
		return errHostClosed
	}
	handle := h.snapshot()
	if handle == nil {
		return errHostClosed
	}
	if expected != nil && (expected.generation != handle.generation || expected.sess != handle.sess) {
		return errHostStaleSession
	}
	h.mu.Lock()
	bindings := h.modelBindings
	reg := h.modelReg
	providers := h.providers
	thinking := h.thinking
	thinkingSet := h.thinkingSet
	h.mu.Unlock()
	if bindings.resolveWire == nil || bindings.buildPreparer == nil || bindings.persist == nil {
		return errHostModelUnavailable
	}
	info, known := hostModelInfo(reg, providers, modelID)
	if !known {
		return fmt.Errorf("%w: %q", errHostModelUnknown, modelID)
	}
	wire, provider, ok := bindings.resolveWire(modelID)
	if !ok {
		return fmt.Errorf("%w: %q", errHostModelWire, modelID)
	}
	resetThinking := false
	if thinkingSet {
		switch {
		case thinking == sdk.ThinkingOff:
			resetThinking = info.Reasoning.Known && info.Reasoning.Supported && info.Reasoning.Mandatory
		case !info.Reasoning.AllowsEffort(string(thinking)):
			return fmt.Errorf("%w: %q does not support thinking level %q", errHostModelEffort, modelID, thinking)
		}
	}
	preparer, err := bindings.buildPreparer(modelID, wire)
	if err != nil {
		return err
	}
	window := int64(0)
	if preparer != nil {
		window = preparer.contextWindow
	}
	var client agent.Client
	seam := h.reasoningSeamValue()
	if bindings.buildClient != nil {
		built, builtSeam, buildErr := bindings.buildClient(provider)
		if buildErr != nil {
			return buildErr
		}
		if built != nil {
			client = built
			seam = builtSeam
		}
	}
	intent := &hostModelIntent{
		model:         modelID,
		wire:          wire,
		provider:      provider,
		preparer:      preparer,
		client:        client,
		seam:          seam,
		window:        window,
		resetThinking: resetThinking,
	}
	adopt := func(*hostSessionHandle) {
		h.mu.Lock()
		h.pendingModel = intent
		h.mu.Unlock()
	}
	if err := bindings.persist(handle, intent, adopt); err != nil {
		return err
	}
	h.deliverCurrent(handle, func() {
		h.lifecycleMu.Lock()
		modelFn := h.lifecycle.model
		thinkingFn := h.lifecycle.thinking
		h.lifecycleMu.Unlock()
		if modelFn != nil {
			modelFn(modelID)
		}
		if resetThinking && thinkingFn != nil {
			thinkingFn(string(sdk.ThinkingDefault))
		}
	})
	return nil
}

func (h *hostRuntime) requestThinking(level sdk.ThinkingLevel, expected *hostSessionHandle) error {
	normalized, err := normalizeThinkingLevel(level)
	if err != nil {
		return err
	}
	if h.closed.Load() {
		return errHostClosed
	}
	handle := h.snapshot()
	if handle == nil {
		return errHostClosed
	}
	if expected != nil && (expected.generation != handle.generation || expected.sess != handle.sess) {
		return errHostStaleSession
	}
	h.mu.Lock()
	seam := h.reasoningSeam
	reg := h.modelReg
	providers := h.providers
	modelID := h.modelID
	h.mu.Unlock()
	if !seam {
		return fmt.Errorf("%w: the active transport has no request-side reasoning seam", sdk.ErrUnsupported)
	}
	info, known := hostModelInfo(reg, providers, modelID)
	switch normalized {
	case sdk.ThinkingDefault:
	case sdk.ThinkingOff:
		if known && info.Reasoning.Known && info.Reasoning.Supported && info.Reasoning.Mandatory {
			return fmt.Errorf("%w: %q", errHostThinkingMandatory, modelID)
		}
	default:
		if !known || !info.Reasoning.Known || !info.Reasoning.Supported {
			return fmt.Errorf("%w: model %q does not support reasoning effort", sdk.ErrUnsupported, modelID)
		}
		if !info.Reasoning.EffortSelection {
			return fmt.Errorf("%w: model %q does not expose reasoning effort selection", sdk.ErrUnsupported, modelID)
		}
		if !info.Reasoning.AllowsEffort(string(normalized)) {
			return fmt.Errorf("%w: model %q does not allow effort %q", errHostThinkingEffort, modelID, normalized)
		}
	}
	if err := h.commitSession(handle, func(sess *session.Session) error {
		return sess.AppendEntry(&session.ThinkingLevelChangeEntry{ThinkingLevel: string(normalized)})
	}, func(*hostSessionHandle) {
		h.mu.Lock()
		h.thinking = normalized
		h.thinkingSet = normalized != sdk.ThinkingDefault
		h.mu.Unlock()
	}); err != nil {
		return err
	}
	h.deliverCurrent(handle, func() {
		h.lifecycleMu.Lock()
		fn := h.lifecycle.thinking
		h.lifecycleMu.Unlock()
		if fn != nil {
			fn(string(normalized))
		}
	})
	return nil
}

func (h *hostRuntime) removeProvider(name string) error {
	name = strings.TrimSpace(name)
	h.mu.Lock()
	active := h.provider == name
	if h.pendingModel != nil {
		active = h.pendingModel.provider == name
	}
	registry := h.providers
	h.mu.Unlock()
	if registry == nil {
		return extensions.ErrUnavailable
	}
	if active {
		return fmt.Errorf("%w: %s", errHostProviderActive, name)
	}
	return registry.Remove(name)
}

func (h *hostRuntime) reasoningSeamValue() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.reasoningSeam
}

func (h *hostRuntime) reasoningDirective() (openrouter.ReasoningDirective, bool) {
	h.mu.Lock()
	seam := h.reasoningSeam
	thinking := h.thinking
	set := h.thinkingSet
	reg := h.modelReg
	providers := h.providers
	modelID := h.modelID
	h.mu.Unlock()
	if !seam || !set {
		return openrouter.ReasoningDirective{}, false
	}
	info, known := hostModelInfo(reg, providers, modelID)
	if thinking == sdk.ThinkingOff {
		if !known || !info.Reasoning.Known || !info.Reasoning.Supported {
			return openrouter.ReasoningDirective{}, false
		}
		return openrouter.ReasoningDirective{Disable: true}, true
	}
	if !known || !info.Reasoning.AllowsEffort(string(thinking)) {
		return openrouter.ReasoningDirective{}, false
	}
	return openrouter.ReasoningDirective{Effort: string(thinking)}, true
}

func normalizeThinkingLevel(level sdk.ThinkingLevel) (sdk.ThinkingLevel, error) {
	switch level {
	case sdk.ThinkingOff, sdk.ThinkingMinimal, sdk.ThinkingLow, sdk.ThinkingMedium, sdk.ThinkingHigh, sdk.ThinkingXHigh, sdk.ThinkingMax, sdk.ThinkingDefault:
		return level, nil
	default:
		return "", fmt.Errorf("%w: %q", errHostThinkingLevel, level)
	}
}

func hostModelInfo(reg *models.Registry, providers *extensions.ProviderRegistry, modelID string) (models.ModelInfo, bool) {
	if reg != nil {
		if info, ok := reg.Get(modelID); ok {
			return info, true
		}
		for _, key := range reg.Keys() {
			info, ok := reg.GetByKey(key)
			if ok && info.ID == modelID {
				return info, true
			}
		}
	}
	if providers != nil {
		if entry, ok := providers.FindModel(modelID); ok {
			return models.ModelInfo{ID: modelID, Provider: entry.Name}, true
		}
	}
	return models.ModelInfo{}, false
}

func newHostModelPersister(h *hostRuntime, cfg *config.Config, systemPrompt string, catalog agent.ToolCatalog, tools []agent.Tool, affinityRoot string, contentFingerprint func() string) func(handle *hostSessionHandle, intent *hostModelIntent, adopt func(*hostSessionHandle)) error {
	return func(handle *hostSessionHandle, intent *hostModelIntent, adopt func(*hostSessionHandle)) error {
		if handle == nil || handle.sess == nil {
			return errHostClosed
		}
		if intent == nil {
			return errHostClosed
		}
		updated := *cfg
		updated.Model = intent.model
		cur := currentRuntimeProfile(&updated, intent.provider, systemPrompt, toolsetFingerprint(catalog, tools), affinityRoot)
		return h.commitSession(handle, func(sess *session.Session) error {
			if _, err := syncRuntimeProfile(sess, cur, contentFingerprint); err != nil {
				return err
			}
			if intent.resetThinking {
				return sess.AppendEntry(&session.ThinkingLevelChangeEntry{ThinkingLevel: string(sdk.ThinkingDefault)})
			}
			return nil
		}, adopt)
	}
}

func modelProviderFor(registry *extensions.ProviderRegistry, fallback, model string) string {
	if registry != nil {
		if entry, ok := registry.FindModel(model); ok {
			return entry.Name
		}
	}
	return fallback
}

func newModelPersisterWithProviders(controller *sessionController, cfg *config.Config, registry *extensions.ProviderRegistry, providerID, systemPrompt string, catalog agent.ToolCatalog, tools []agent.Tool, affinityRoot string, contentFingerprint func() string) func(string) error {
	return func(model string) error {
		provider := modelProviderFor(registry, providerID, model)
		persist := newModelPersister(controller, cfg, provider, systemPrompt, catalog, tools, affinityRoot, contentFingerprint)
		return persist(model)
	}
}

func providerEntryClient(d *Deps, host *hostRuntime, registry *extensions.ProviderRegistry, provider string) (agent.Client, bool) {
	if d == nil || registry == nil || strings.TrimSpace(provider) == "" {
		return nil, false
	}
	entry, ok := registry.Lookup(provider)
	if !ok {
		return nil, false
	}
	key, _ := registry.Credential(provider)
	driver := providers.NewOpenAICompletions(providers.Config{
		BaseURL:    customCompletionsEndpoint(entry.BaseURL),
		Auth:       func(context.Context) (string, error) { return key, nil },
		ProviderID: entry.Name,
		API:        entry.API,
	}, d.HTTPClient)
	return newHostClient(driver, host, false), true
}

func customCompletionsEndpoint(baseURL string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if strings.HasSuffix(trimmed, "/chat/completions") {
		return trimmed
	}
	return trimmed + "/chat/completions"
}
