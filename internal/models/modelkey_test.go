package models

import (
	"testing"
)

func TestModelKeyGetByKey(t *testing.T) {
	r := NewRegistry()
	r.Register("anthropic/claude-sonnet-4.5", ModelInfo{ContextWindow: 200_000, Provider: "anthropic"})
	r.Register("vendor/bare", ModelInfo{ContextWindow: 1000, Provider: "vendor"})

	m, ok := r.GetByKey(ModelKey{ProviderID: "anthropic", ModelID: "claude-sonnet-4.5"})
	if !ok || m.ContextWindow != 200_000 {
		t.Errorf("GetByKey = %+v, %v", m, ok)
	}
	m, ok = r.GetByKey(ModelKey{ProviderID: "vendor", ModelID: "bare"})
	if !ok || m.Provider != "vendor" {
		t.Errorf("GetByKey bare = %+v, %v", m, ok)
	}
	if _, ok := r.GetByKey(ModelKey{ProviderID: "openai", ModelID: "claude-sonnet-4.5"}); ok {
		t.Error("GetByKey crossed provider boundaries")
	}
	if _, ok := r.GetByKey(ModelKey{ProviderID: "", ModelID: "claude-sonnet-4.5"}); ok {
		t.Error("GetByKey without a provider resolved a namespaced id")
	}
	if _, ok := r.GetByKey(ModelKey{ProviderID: "vendor", ModelID: ""}); ok {
		t.Error("GetByKey with an empty model id resolved")
	}
}

func TestModelKeyFullIDAlreadyPrefixed(t *testing.T) {
	r := NewRegistry()
	r.Register("openai/gpt-5", ModelInfo{ContextWindow: 400_000, Provider: "openai"})
	m, ok := r.GetByKey(ModelKey{ProviderID: "openai", ModelID: "openai/gpt-5"})
	if !ok || m.ContextWindow != 400_000 {
		t.Errorf("prefixed GetByKey = %+v, %v", m, ok)
	}
}

func TestModelKeyRegisterKey(t *testing.T) {
	r := NewRegistry()
	r.RegisterKey(ModelKey{ProviderID: "vendor", ModelID: "new"}, ModelInfo{ContextWindow: 42, Provider: "vendor"})
	m, ok := r.Get("vendor/new")
	if !ok || m.ContextWindow != 42 {
		t.Errorf("RegisterKey stored %+v ok=%v", m, ok)
	}
	r.RegisterKey(ModelKey{ProviderID: "vendor", ModelID: "prefixed/id"}, ModelInfo{ContextWindow: 7, Provider: "vendor"})
	if _, ok := r.Get("prefixed/id"); !ok {
		t.Error("RegisterKey with a prefixed id must not double-prefix")
	}
	r.RegisterKey(ModelKey{ProviderID: "vendor", ModelID: ""}, ModelInfo{ContextWindow: 1})
	if _, ok := r.Get("vendor/"); ok {
		t.Error("RegisterKey accepted an empty model id")
	}
}

func TestModelKeyKeys(t *testing.T) {
	r := NewRegistry()
	r.RegisterProviderCatalog("deepseek", []ModelInfo{
		{ID: "deepseek-chat", ContextWindow: 163_840},
		{ID: "deepseek-r1", ContextWindow: 64_000},
	})
	keys := r.Keys()
	if len(keys) < 2 {
		t.Fatalf("Keys = %v, want at least the deepseek pair", keys)
	}
	found := map[string]bool{}
	for _, k := range keys {
		if k.ProviderID == "deepseek" {
			found[k.ModelID] = true
		}
	}
	if !found["deepseek-chat"] || !found["deepseek-r1"] {
		t.Errorf("Keys = %v, want deepseek-chat and deepseek-r1 entries", keys)
	}
	for i := 1; i < len(keys); i++ {
		prev, cur := keys[i-1], keys[i]
		if prev.ProviderID > cur.ProviderID || (prev.ProviderID == cur.ProviderID && prev.ModelID > cur.ModelID) {
			t.Errorf("Keys unsorted at %v before %v", prev, cur)
		}
	}
}

func TestModelKeyRoundTripWithRegistry(t *testing.T) {
	r := NewRegistry()
	r.RegisterProviderCatalog("openai", []ModelInfo{
		{ID: "gpt-5", ContextWindow: 400_000},
	})
	for _, k := range r.Keys() {
		if k.ProviderID != "openai" {
			continue
		}
		m, ok := r.GetByKey(k)
		if !ok || m.ContextWindow != 400_000 || m.Provider != "openai" {
			t.Errorf("round trip for %+v = %+v, %v", k, m, ok)
		}
	}
}
