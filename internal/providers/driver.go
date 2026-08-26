package providers

import (
	"context"
	"sort"
	"sync"

	"github.com/digitalygo/smidja/internal/agent"
)

type Driver interface {
	StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error)
}

var _ Driver = (*OpenAICompletions)(nil)

type Ref struct {
	ProviderID string

	ModelID string

	Dialect string
}

type Registry struct {
	mu      sync.RWMutex
	drivers map[string]Driver
}

func NewRegistry() *Registry {
	return &Registry{drivers: make(map[string]Driver)}
}

func (r *Registry) Register(providerID string, d Driver) {
	if providerID == "" || d == nil {
		return
	}
	r.mu.Lock()
	r.drivers[providerID] = d
	r.mu.Unlock()
}

func (r *Registry) Get(providerID string) (Driver, bool) {
	r.mu.RLock()
	d, ok := r.drivers[providerID]
	r.mu.RUnlock()
	return d, ok
}

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
