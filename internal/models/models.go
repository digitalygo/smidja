package models

import (
	"sort"
	"sync"
)

type ModelInfo struct {
	ID string

	ContextWindow int64

	Provider string
}

const DefaultModelID = "anthropic/claude-sonnet-4.5"

const DefaultModelContextWindow int64 = 200_000

var fallbackTable = []ModelInfo{
	{ID: "anthropic/claude-sonnet-4.5", ContextWindow: 200_000, Provider: "anthropic"},
	{ID: "anthropic/claude-sonnet-4", ContextWindow: 200_000, Provider: "anthropic"},
	{ID: "anthropic/claude-sonnet-4.6", ContextWindow: 1_000_000, Provider: "anthropic"},
	{ID: "anthropic/claude-sonnet-5", ContextWindow: 1_000_000, Provider: "anthropic"},
	{ID: "anthropic/claude-opus-4", ContextWindow: 200_000, Provider: "anthropic"},
	{ID: "anthropic/claude-opus-4.1", ContextWindow: 200_000, Provider: "anthropic"},
	{ID: "anthropic/claude-opus-4.5", ContextWindow: 200_000, Provider: "anthropic"},
	{ID: "anthropic/claude-opus-4.6", ContextWindow: 1_000_000, Provider: "anthropic"},
	{ID: "anthropic/claude-opus-4.7", ContextWindow: 1_000_000, Provider: "anthropic"},
	{ID: "anthropic/claude-opus-4.8", ContextWindow: 1_000_000, Provider: "anthropic"},
	{ID: "anthropic/claude-opus-5", ContextWindow: 1_000_000, Provider: "anthropic"},
	{ID: "anthropic/claude-haiku-4.5", ContextWindow: 200_000, Provider: "anthropic"},
	{ID: "anthropic/claude-3.7-sonnet", ContextWindow: 200_000, Provider: "anthropic"},
	{ID: "anthropic/claude-3.5-sonnet", ContextWindow: 200_000, Provider: "anthropic"},
	{ID: "anthropic/claude-3.5-haiku", ContextWindow: 200_000, Provider: "anthropic"},
	{ID: "anthropic/claude-3-haiku", ContextWindow: 200_000, Provider: "anthropic"},
	{ID: "anthropic/claude-3-opus", ContextWindow: 200_000, Provider: "anthropic"},
	{ID: "anthropic/claude-hx-1", ContextWindow: 1_000_000, Provider: "anthropic"},
	{ID: "anthropic/claude-hx-1.5", ContextWindow: 1_000_000, Provider: "anthropic"},
	{ID: "openai/gpt-5", ContextWindow: 400_000, Provider: "openai"},
	{ID: "openai/gpt-5-mini", ContextWindow: 400_000, Provider: "openai"},
	{ID: "openai/gpt-5-nano", ContextWindow: 400_000, Provider: "openai"},
	{ID: "openai/gpt-5-pro", ContextWindow: 400_000, Provider: "openai"},
	{ID: "openai/gpt-5.1", ContextWindow: 400_000, Provider: "openai"},
	{ID: "openai/gpt-5.2", ContextWindow: 400_000, Provider: "openai"},
	{ID: "openai/gpt-5.2-pro", ContextWindow: 400_000, Provider: "openai"},
	{ID: "openai/gpt-5.4", ContextWindow: 1_050_000, Provider: "openai"},
	{ID: "openai/gpt-5.4-mini", ContextWindow: 400_000, Provider: "openai"},
	{ID: "openai/gpt-5.4-pro", ContextWindow: 1_050_000, Provider: "openai"},
	{ID: "openai/gpt-4.1", ContextWindow: 1_047_576, Provider: "openai"},
	{ID: "openai/gpt-4o", ContextWindow: 128_000, Provider: "openai"},
	{ID: "openai/gpt-4o-mini", ContextWindow: 128_000, Provider: "openai"},
	{ID: "google/gemini-2.5-pro", ContextWindow: 1_048_576, Provider: "google"},
	{ID: "google/gemini-2.5-flash", ContextWindow: 1_048_576, Provider: "google"},
	{ID: "google/gemini-2.5-flash-lite", ContextWindow: 1_048_576, Provider: "google"},
	{ID: "google/gemini-3-pro", ContextWindow: 1_048_576, Provider: "google"},
	{ID: "google/gemini-3-pro-preview", ContextWindow: 1_048_576, Provider: "google"},
	{ID: "deepseek/deepseek-chat", ContextWindow: 163_840, Provider: "deepseek"},
	{ID: "deepseek/deepseek-r1", ContextWindow: 64_000, Provider: "deepseek"},
	{ID: "deepseek/deepseek-v3.2", ContextWindow: 163_840, Provider: "deepseek"},
	{ID: "qwen/qwen3-max", ContextWindow: 128_000, Provider: "qwen"},
	{ID: "qwen/qwen3-235b-a22b", ContextWindow: 128_000, Provider: "qwen"},
	{ID: "qwen/qwen2.5-coder-32b-instruct", ContextWindow: 128_000, Provider: "qwen"},
}

type Registry struct {
	mu     sync.RWMutex
	models map[string]ModelInfo
}

func NewRegistry() *Registry {
	r := &Registry{models: make(map[string]ModelInfo, len(fallbackTable))}
	for _, m := range fallbackTable {
		r.models[m.ID] = m
	}
	return r
}

func (r *Registry) Register(id string, m ModelInfo) {
	if id == "" {
		return
	}
	m.ID = id
	r.mu.Lock()
	r.models[id] = m
	r.mu.Unlock()
}

func (r *Registry) Get(id string) (ModelInfo, bool) {
	r.mu.RLock()
	m, ok := r.models[id]
	r.mu.RUnlock()
	return m, ok
}

func (r *Registry) Default() ModelInfo {
	m, _ := r.Get(DefaultModelID)
	return m
}

func (r *Registry) Merge(infos []ModelInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, m := range infos {
		if m.ID == "" || m.ContextWindow <= 0 {
			continue
		}
		r.models[m.ID] = m
	}
}

func (r *Registry) RegisterProviderCatalog(provider string, infos []ModelInfo) {
	if provider == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, m := range r.models {
		if m.Provider == provider {
			delete(r.models, id)
		}
	}
	for _, m := range infos {
		if m.ID == "" {
			continue
		}
		m.Provider = provider
		r.models[m.ID] = m
	}
}

func (r *Registry) ByProvider(provider string) []ModelInfo {
	r.mu.RLock()
	out := make([]ModelInfo, 0)
	for _, m := range r.models {
		if m.Provider == provider {
			out = append(out, m)
		}
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (r *Registry) Lookup(provider, id string) (ModelInfo, bool) {
	if provider == "" || id == "" {
		return ModelInfo{}, false
	}
	for _, candidate := range []string{id, provider + "/" + id} {
		if m, ok := r.Get(candidate); ok && m.Provider == provider {
			return m, true
		}
	}
	return ModelInfo{}, false
}
