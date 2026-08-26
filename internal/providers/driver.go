// Package providers defines the provider driver seam of smidja: the
// Driver interface the agent loop calls, the canonical Ref identity, and
// the Registry that routes a provider ID to its driver. OpenAICompletions
// is the reference driver for every OpenAI chat completions dialect; the
// legacy internal/openrouter package is a compatibility facade over it.
package providers

import (
	"context"
	"sort"
	"sync"

	"github.com/digitalygo/smidja/internal/agent"
)

// Driver streams assistant turns from one provider. It satisfies
// agent.Client; the agent loop, CLI, and session packages depend on that
// shape, so new protocol drivers (anthropic-messages, gemini, ...) must
// implement exactly this seam.
type Driver interface {
	// StreamTurn performs one assistant turn against the provider.
	// See agent.Client.StreamTurn for the full contract.
	StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error)
}

// Compile-time assertion that OpenAICompletions satisfies Driver.
var _ Driver = (*OpenAICompletions)(nil)

// Ref is the canonical identity of a provider model: the provider
// identifier, the model identifier, and the API dialect. It is the
// identity the harness uses to route a turn to a driver, look up
// catalogue metadata, and record provenance on assistant messages.
type Ref struct {
	// ProviderID is the canonical provider identifier, for example
	// "deepseek".
	ProviderID string

	// ModelID is the provider model identifier, for example
	// "deepseek-chat" for a native endpoint or "deepseek/deepseek-chat"
	// behind OpenRouter.
	ModelID string

	// Dialect is the API dialect identifier, for example
	// "openai-completions".
	Dialect string
}

// Registry is the concurrency-safe map from provider ID to Driver.
type Registry struct {
	mu      sync.RWMutex
	drivers map[string]Driver
}

// NewRegistry returns an empty provider registry.
func NewRegistry() *Registry {
	return &Registry{drivers: make(map[string]Driver)}
}

// Register installs the driver for providerID, replacing any previous
// driver. An empty providerID is ignored.
func (r *Registry) Register(providerID string, d Driver) {
	if providerID == "" || d == nil {
		return
	}
	r.mu.Lock()
	r.drivers[providerID] = d
	r.mu.Unlock()
}

// Get returns the driver registered for providerID. ok is false when no
// driver is registered for that provider.
func (r *Registry) Get(providerID string) (Driver, bool) {
	r.mu.RLock()
	d, ok := r.drivers[providerID]
	r.mu.RUnlock()
	return d, ok
}

// List returns the registered provider IDs sorted lexically.
func (r *Registry) List() []string {
	r.mu.RLock()
	ids := make([]string, 0, len(r.drivers))
	for id := range r.drivers {
		ids = append(ids, id)
	}
	r.mu.RUnlock()
	sort.Strings(ids)
	return ids
}
