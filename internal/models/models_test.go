package models

import (
	"fmt"
	"sync"
	"testing"
)

func TestNewRegistrySeedsFallback(t *testing.T) {
	r := NewRegistry()

	d := r.Default()
	if d.ID != DefaultModelID {
		t.Errorf("Default().ID = %q, want %q", d.ID, DefaultModelID)
	}
	if d.ContextWindow != DefaultModelContextWindow {
		t.Errorf("Default().ContextWindow = %d, want %d", d.ContextWindow, DefaultModelContextWindow)
	}
	if d.Provider != "anthropic" {
		t.Errorf("Default().Provider = %q, want %q", d.Provider, "anthropic")
	}

	checks := []struct {
		id       string
		window   int64
		provider string
	}{
		{"anthropic/claude-opus-4.1", 200_000, "anthropic"},
		{"anthropic/claude-opus-5", 1_000_000, "anthropic"},
		{"anthropic/claude-hx-1.5", 1_000_000, "anthropic"},
		{"openai/gpt-5", 400_000, "openai"},
		{"openai/gpt-5.4-pro", 1_050_000, "openai"},
		{"google/gemini-2.5-pro", 1_048_576, "google"},
		{"deepseek/deepseek-chat", 163_840, "deepseek"},
		{"qwen/qwen3-max", 128_000, "qwen"},
	}
	for _, c := range checks {
		m, ok := r.Get(c.id)
		if !ok {
			t.Errorf("Get(%q) not found, want fallback entry", c.id)
			continue
		}
		if m.ContextWindow != c.window || m.Provider != c.provider {
			t.Errorf("Get(%q) = window %d provider %q, want window %d provider %q",
				c.id, m.ContextWindow, m.Provider, c.window, c.provider)
		}
	}

	if _, ok := r.Get("vendor/unknown-model"); ok {
		t.Error("Get on an unknown id returned ok=true")
	}
}

func TestRegisterOverridesDefault(t *testing.T) {
	r := NewRegistry()

	r.Register(DefaultModelID, ModelInfo{ContextWindow: 42, Provider: "custom"})
	d := r.Default()
	if d.ContextWindow != 42 || d.Provider != "custom" {
		t.Errorf("Default() = window %d provider %q, want window 42 provider custom", d.ContextWindow, d.Provider)
	}

	r.Register("vendor/new-model", ModelInfo{ContextWindow: 777, Provider: "vendor"})
	m, ok := r.Get("vendor/new-model")
	if !ok {
		t.Fatal("Get on a newly registered id returned ok=false")
	}
	if m.ID != "vendor/new-model" || m.ContextWindow != 777 || m.Provider != "vendor" {
		t.Errorf("registered entry = %+v, want id vendor/new-model window 777 provider vendor", m)
	}

	r.Register("vendor/aliased", ModelInfo{ID: "ignored", ContextWindow: 5, Provider: "vendor"})
	m, ok = r.Get("vendor/aliased")
	if !ok || m.ID != "vendor/aliased" {
		t.Errorf("aliased entry = %+v, want id vendor/aliased", m)
	}
}

func TestRegisterEmptyIDIgnored(t *testing.T) {
	r := NewRegistry()
	r.Register("", ModelInfo{ContextWindow: 9, Provider: "x"})
	if _, ok := r.Get(""); ok {
		t.Error("empty id was stored")
	}
}

func TestMerge(t *testing.T) {
	r := NewRegistry()
	r.Merge([]ModelInfo{
		{ID: DefaultModelID, ContextWindow: 999_000, Provider: "merged"},
		{ID: "vendor/new-model", ContextWindow: 12_345, Provider: "vendor"},
		{ID: "unknown/window", ContextWindow: 0, Provider: "vendor"},
		{ID: "", ContextWindow: 5, Provider: "vendor"},
		{ID: "known/kept", ContextWindow: -3, Provider: "vendor"},
	})

	m, ok := r.Get(DefaultModelID)
	if !ok || m.ContextWindow != 999_000 || m.Provider != "merged" {
		t.Errorf("merged default = %+v ok=%v, want window 999000 provider merged", m, ok)
	}
	m, ok = r.Get("vendor/new-model")
	if !ok || m.ContextWindow != 12_345 {
		t.Errorf("added entry = %+v ok=%v, want window 12345", m, ok)
	}
	if _, ok := r.Get("unknown/window"); ok {
		t.Error("zero-window entry was merged")
	}
	if _, ok := r.Get("known/kept"); ok {
		t.Error("negative-window entry was merged")
	}
	if _, ok := r.Get(""); ok {
		t.Error("empty-id entry was merged")
	}

	m, ok = r.Get("openai/gpt-5")
	if !ok || m.ContextWindow != 400_000 {
		t.Errorf("fallback entry disturbed by merge: %+v ok=%v", m, ok)
	}
}

func TestMergeUnknownWindowKeepsFallback(t *testing.T) {
	r := NewRegistry()
	r.Merge([]ModelInfo{{ID: DefaultModelID, ContextWindow: 0, Provider: "x"}})
	d := r.Default()
	if d.ContextWindow != DefaultModelContextWindow {
		t.Errorf("Default() window = %d after zero-window merge, want %d",
			d.ContextWindow, DefaultModelContextWindow)
	}
}

func TestRegistryConcurrentAccess(t *testing.T) {
	r := NewRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("test/model-%d", i)
			r.Register(id, ModelInfo{ContextWindow: int64(1000 + i), Provider: "test"})
			for j := 0; j < 50; j++ {
				r.Get(id)
				r.Default()
				r.Merge([]ModelInfo{{ID: id, ContextWindow: int64(2000 + j), Provider: "test"}})
			}
		}(i)
	}
	wg.Wait()

	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("test/model-%d", i)
		m, ok := r.Get(id)
		if !ok {
			t.Errorf("Get(%q) not found after concurrent writes", id)
			continue
		}
		if m.ContextWindow < 2000 {
			t.Errorf("Get(%q) window = %d, want a merged value >= 2000", id, m.ContextWindow)
		}
	}
	if d := r.Default(); d.ContextWindow != DefaultModelContextWindow {
		t.Errorf("Default() window = %d after concurrent access, want %d",
			d.ContextWindow, DefaultModelContextWindow)
	}
}

func TestRegisterProviderCatalog(t *testing.T) {
	r := NewRegistry()
	r.RegisterProviderCatalog("deepseek", []ModelInfo{
		{ID: "deepseek-chat", ContextWindow: 163_840},
		{ID: "deepseek-r1", ContextWindow: 64_000},
	})
	if got := r.ByProvider("deepseek"); len(got) != 2 {
		t.Fatalf("ByProvider(deepseek) = %d entries, want 2", len(got))
	}
	m, ok := r.Get("deepseek-chat")
	if !ok || m.Provider != "deepseek" {
		t.Errorf("Get(deepseek-chat) = %+v, %v; want provider forced to deepseek", m, ok)
	}
	r.RegisterProviderCatalog("deepseek", []ModelInfo{
		{ID: "deepseek-chat", ContextWindow: 200_000},
		{ID: "deepseek-v3.2", ContextWindow: 163_840},
	})
	if got := r.ByProvider("deepseek"); len(got) != 2 {
		t.Fatalf("ByProvider(deepseek) after replace = %d entries, want 2", len(got))
	}
	if _, ok := r.Get("deepseek-r1"); ok {
		t.Error("stale deepseek-r1 survived a provider catalog replacement")
	}
	if _, ok := r.Get("anthropic/claude-sonnet-4.5"); !ok {
		t.Error("anthropic fallback entry lost after deepseek catalog registration")
	}
}

func TestByProviderOrder(t *testing.T) {
	r := NewRegistry()
	r.RegisterProviderCatalog("openai", []ModelInfo{
		{ID: "gpt-5-pro", ContextWindow: 400_000},
		{ID: "gpt-5", ContextWindow: 400_000},
		{ID: "gpt-5-nano", ContextWindow: 400_000},
	})
	got := r.ByProvider("openai")
	if len(got) != 3 || got[0].ID != "gpt-5" || got[1].ID != "gpt-5-nano" || got[2].ID != "gpt-5-pro" {
		t.Errorf("ByProvider(openai) = %v, want sorted gpt-5, gpt-5-nano, gpt-5-pro", got)
	}
	if got := r.ByProvider("unknown"); len(got) != 0 {
		t.Errorf("ByProvider(unknown) = %v, want empty", got)
	}
}

func TestLookupProviderAware(t *testing.T) {
	r := NewRegistry()
	r.RegisterProviderCatalog("deepseek", []ModelInfo{
		{ID: "deepseek-chat", ContextWindow: 163_840},
	})
	m, ok := r.Lookup("deepseek", "deepseek-chat")
	if !ok || m.ContextWindow != 163_840 {
		t.Errorf("Lookup(deepseek, deepseek-chat) = %+v, %v", m, ok)
	}
	m, ok = r.Lookup("anthropic", "claude-sonnet-4.5")
	if !ok || m.ContextWindow != DefaultModelContextWindow {
		t.Errorf("Lookup(anthropic, claude-sonnet-4.5) = %+v, %v", m, ok)
	}
	if _, ok := r.Lookup("openai", "claude-sonnet-4.5"); ok {
		t.Error("Lookup matched a model of another provider")
	}
	if _, ok := r.Lookup("deepseek", ""); ok {
		t.Error("Lookup with empty id resolved")
	}
	if _, ok := r.Lookup("", "deepseek-chat"); ok {
		t.Error("Lookup with empty provider resolved")
	}
}
