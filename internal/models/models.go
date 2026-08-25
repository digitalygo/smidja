// Package models implements the model catalogue of the smidja harness:
// OpenRouter model metadata and the registry that serves it.
//
// The registry is seeded with a curated offline fallback table of common
// models and their known context windows, so the harness works without
// network access. When the network is available, FetchOpenRouterModels
// retrieves the live OpenRouter catalogue and Merge applies it on top of
// the fallback: a live entry with a positive context window replaces or
// extends the table, while an unknown window never clobbers a known
// value. The ContextWindow values feed the context manager occupancy
// math.
package models

import "sync"

// ModelInfo is the registry entry for one model.
type ModelInfo struct {
	// ID is the provider model identifier used in requests, for example
	// "anthropic/claude-sonnet-4.5".
	ID string

	// ContextWindow is the model context window in tokens; the value
	// that feeds the context manager occupancy math. Zero means unknown.
	ContextWindow int64

	// Provider is the provider identifier, for example "anthropic".
	Provider string
}

// DefaultModelID is the built-in default model identifier, returned by
// Default unless a caller overrode the entry.
const DefaultModelID = "anthropic/claude-sonnet-4.5"

// DefaultModelContextWindow is the built-in default model context window
// in tokens. Claude Sonnet 4.5 ships with a 200K-token window; the 1M
// beta extension is not the v0 baseline.
const DefaultModelContextWindow int64 = 200_000

// fallbackTable is the curated offline catalogue. Values follow the
// providers' published context windows and were cross-checked against
// the live OpenRouter catalogue on 2026-08-25; they prefer the
// provider-canonical window over beta extensions. Fetch merges live
// values on top when the network is available, so drift self-heals.
var fallbackTable = []ModelInfo{
	// Anthropic Claude Sonnet family.
	{ID: "anthropic/claude-sonnet-4.5", ContextWindow: 200_000, Provider: "anthropic"},
	{ID: "anthropic/claude-sonnet-4", ContextWindow: 200_000, Provider: "anthropic"},
	{ID: "anthropic/claude-sonnet-4.6", ContextWindow: 1_000_000, Provider: "anthropic"},
	{ID: "anthropic/claude-sonnet-5", ContextWindow: 1_000_000, Provider: "anthropic"},
	// Anthropic Claude Opus family.
	{ID: "anthropic/claude-opus-4", ContextWindow: 200_000, Provider: "anthropic"},
	{ID: "anthropic/claude-opus-4.1", ContextWindow: 200_000, Provider: "anthropic"},
	{ID: "anthropic/claude-opus-4.5", ContextWindow: 200_000, Provider: "anthropic"},
	{ID: "anthropic/claude-opus-4.6", ContextWindow: 1_000_000, Provider: "anthropic"},
	{ID: "anthropic/claude-opus-4.7", ContextWindow: 1_000_000, Provider: "anthropic"},
	{ID: "anthropic/claude-opus-4.8", ContextWindow: 1_000_000, Provider: "anthropic"},
	{ID: "anthropic/claude-opus-5", ContextWindow: 1_000_000, Provider: "anthropic"},
	// Anthropic Claude Haiku and legacy families.
	{ID: "anthropic/claude-haiku-4.5", ContextWindow: 200_000, Provider: "anthropic"},
	{ID: "anthropic/claude-3.7-sonnet", ContextWindow: 200_000, Provider: "anthropic"},
	{ID: "anthropic/claude-3.5-sonnet", ContextWindow: 200_000, Provider: "anthropic"},
	{ID: "anthropic/claude-3.5-haiku", ContextWindow: 200_000, Provider: "anthropic"},
	{ID: "anthropic/claude-3-haiku", ContextWindow: 200_000, Provider: "anthropic"},
	{ID: "anthropic/claude-3-opus", ContextWindow: 200_000, Provider: "anthropic"},
	// Anthropic Claude Hx experimental family, 1M context per the
	// Anthropic Hx announcement.
	{ID: "anthropic/claude-hx-1", ContextWindow: 1_000_000, Provider: "anthropic"},
	{ID: "anthropic/claude-hx-1.5", ContextWindow: 1_000_000, Provider: "anthropic"},
	// OpenAI GPT-5 class.
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
	// OpenAI GPT-4 class.
	{ID: "openai/gpt-4.1", ContextWindow: 1_047_576, Provider: "openai"},
	{ID: "openai/gpt-4o", ContextWindow: 128_000, Provider: "openai"},
	{ID: "openai/gpt-4o-mini", ContextWindow: 128_000, Provider: "openai"},
	// Google Gemini.
	{ID: "google/gemini-2.5-pro", ContextWindow: 1_048_576, Provider: "google"},
	{ID: "google/gemini-2.5-flash", ContextWindow: 1_048_576, Provider: "google"},
	{ID: "google/gemini-2.5-flash-lite", ContextWindow: 1_048_576, Provider: "google"},
	{ID: "google/gemini-3-pro", ContextWindow: 1_048_576, Provider: "google"},
	{ID: "google/gemini-3-pro-preview", ContextWindow: 1_048_576, Provider: "google"},
	// DeepSeek.
	{ID: "deepseek/deepseek-chat", ContextWindow: 163_840, Provider: "deepseek"},
	{ID: "deepseek/deepseek-r1", ContextWindow: 64_000, Provider: "deepseek"},
	{ID: "deepseek/deepseek-v3.2", ContextWindow: 163_840, Provider: "deepseek"},
	// Qwen.
	{ID: "qwen/qwen3-max", ContextWindow: 128_000, Provider: "qwen"},
	{ID: "qwen/qwen3-235b-a22b", ContextWindow: 128_000, Provider: "qwen"},
	{ID: "qwen/qwen2.5-coder-32b-instruct", ContextWindow: 128_000, Provider: "qwen"},
}

// Registry is the concurrency-safe model catalogue. It starts seeded
// with the curated fallback table; Register and Merge layer explicit and
// network values on top.
type Registry struct {
	mu     sync.RWMutex
	models map[string]ModelInfo
}

// NewRegistry returns a registry seeded with the curated fallback table,
// including the built-in default anthropic/claude-sonnet-4.5 with its
// 200K context window.
func NewRegistry() *Registry {
	r := &Registry{models: make(map[string]ModelInfo, len(fallbackTable))}
	for _, m := range fallbackTable {
		r.models[m.ID] = m
	}
	return r
}

// Register inserts or replaces the entry for id. An empty id is ignored.
// Register is the highest-precedence writer: it always replaces the
// current entry, be it fallback, merge, or a prior registration.
func (r *Registry) Register(id string, m ModelInfo) {
	if id == "" {
		return
	}
	m.ID = id
	r.mu.Lock()
	r.models[id] = m
	r.mu.Unlock()
}

// Get returns the entry for id. ok is false when the id is unknown.
func (r *Registry) Get(id string) (ModelInfo, bool) {
	r.mu.RLock()
	m, ok := r.models[id]
	r.mu.RUnlock()
	return m, ok
}

// Default returns the built-in default model, anthropic/claude-sonnet-4.5
// with a 200K context window, unless a caller overrode that entry through
// Register or Merge.
func (r *Registry) Default() ModelInfo {
	m, _ := r.Get(DefaultModelID)
	return m
}

// Merge applies a batch of catalogue entries on top of the registry,
// replacing or adding each incoming entry with a positive context window.
// Entries with an empty id or a zero or negative window are skipped, so
// unknown network data never clobbers a known fallback value.
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
