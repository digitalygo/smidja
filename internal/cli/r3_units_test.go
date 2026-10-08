package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/models"
	"github.com/digitalygo/smidja/sdk"
)

func TestHostContextGuardsWithoutHost(t *testing.T) {
	ctx := &hostHandlerContext{}
	if ctx.Model() != nil {
		t.Fatal("model without a host must be nil")
	}
	if ctx.ThinkingLevel() != sdk.ThinkingOff {
		t.Fatalf("thinking = %q, want off", ctx.ThinkingLevel())
	}
	if ctx.sessionHandle() != nil {
		t.Fatal("session handle without state must be nil")
	}
	if err := ctx.SetModel(sdk.Model{ID: "model"}); err != errHostClosed {
		t.Fatalf("SetModel error = %v", err)
	}
	if err := ctx.SetThinkingLevel(sdk.ThinkingOff); err != errHostClosed {
		t.Fatalf("SetThinkingLevel error = %v", err)
	}
}

func TestHostRuntimeProviderAccessorsAndOptions(t *testing.T) {
	host := newHostRuntime(nil, "", nil, nil)
	if host.providerModels() != nil {
		t.Fatal("provider models without a registry must be nil")
	}
	if host.currentThinking() != sdk.ThinkingOff {
		t.Fatalf("thinking = %q, want off", host.currentThinking())
	}
	host.mu.Lock()
	host.thinking = sdk.ThinkingXHigh
	host.mu.Unlock()
	if host.currentThinking() != sdk.ThinkingXHigh {
		t.Fatalf("thinking = %q, want xhigh", host.currentThinking())
	}
	if err := host.hostOptions().SetModel(sdk.Model{ID: "model"}); err != errHostClosed {
		t.Fatalf("host SetModel error = %v", err)
	}
	if err := host.hostOptions().SetThinkingLevel(sdk.ThinkingOff); err != errHostClosed {
		t.Fatalf("host SetThinkingLevel error = %v", err)
	}
	registry := extensions.NewProviderRegistry()
	if err := registry.Register("proxy", sdk.ProviderConfig{BaseURL: "https://example.com", Models: []sdk.Model{{ID: "proxy/model"}}}); err != nil {
		t.Fatal(err)
	}
	host.setProviders(registry)
	models := host.providerModels()
	if len(models) != 1 || models[0].ID != "proxy/model" {
		t.Fatalf("provider models = %+v", models)
	}
}

func TestPromptCommandSlotGuards(t *testing.T) {
	var nilSlot *promptCommandSlot
	if registered := nilSlot.bind(extensions.NewCommandCatalog()); registered != "" {
		t.Fatalf("nil slot bind = %q", registered)
	}
	nilSlot.set(func(sdk.CommandContext, string) error { return nil })
	slot := newPromptCommandSlot()
	if registered := slot.bind(nil); registered != "" {
		t.Fatalf("nil commands bind = %q", registered)
	}
	if err := slot.dispatch(nil, ""); err == nil || !strings.Contains(err.Error(), "not available yet") {
		t.Fatalf("dispatch before bind = %v", err)
	}
	commands := extensions.NewCommandCatalog()
	if registered := slot.bind(commands); registered != promptCommandName {
		t.Fatalf("registered = %q", registered)
	}
	if registered := slot.bind(commands); registered != promptCommandName {
		t.Fatalf("second bind = %q", registered)
	}
	slot.set(func(sdk.CommandContext, string) error { return errors.New("bound handler") })
	if err := slot.dispatch(nil, ""); err == nil || err.Error() != "bound handler" {
		t.Fatalf("dispatch = %v", err)
	}
	slot.set(nil)
	if err := slot.dispatch(nil, ""); err == nil || !strings.Contains(err.Error(), "not available yet") {
		t.Fatalf("dispatch after unbind = %v", err)
	}
}

func TestHostModelRegistryAvailableDedupesSources(t *testing.T) {
	reg := models.NewRegistry()
	reg.Register("vendor/alpha", models.ModelInfo{ID: "vendor/alpha", Provider: "vendor", ContextWindow: 4096})
	reg.Register("alpha", models.ModelInfo{ID: "alpha", Provider: "vendor", ContextWindow: 4096})
	providers := extensions.NewProviderRegistry()
	if err := providers.Register("proxy", sdk.ProviderConfig{BaseURL: "https://example.com", Models: []sdk.Model{{ID: "proxy/model"}}}); err != nil {
		t.Fatal(err)
	}
	if err := providers.Register("proxy2", sdk.ProviderConfig{BaseURL: "https://example.com", Models: []sdk.Model{{ID: "proxy/model"}}}); err != nil {
		t.Fatal(err)
	}
	registry := &hostModelRegistry{reg: reg, modelID: "vendor/alpha", wireModel: "vendor/alpha", provider: "vendor", extra: providers.Models()}
	available := registry.Available()
	seen := map[string]int{}
	for _, model := range available {
		seen[model.ID]++
	}
	if seen["vendor/alpha"] != 1 || seen["proxy/model"] != 1 {
		t.Fatalf("available = %+v", available)
	}
	if found, ok := registry.Find("proxy", "proxy/model"); !ok || found.ID != "proxy/model" {
		t.Fatalf("Find = %+v ok=%v", found, ok)
	}
	if _, ok := registry.Find("proxy", "missing"); ok {
		t.Fatal("unknown model must not resolve")
	}
}
