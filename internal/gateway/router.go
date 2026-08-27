package gateway

import (
	"context"
	"sync"
)

type Router struct {
	mu      sync.Mutex
	ctx     context.Context
	resolve Resolver
	factory func(key, workspace, sessionHint string) *Actor
	actors  map[string]*Actor
}

func NewRouter(resolve Resolver, factory func(key, workspace, sessionHint string) *Actor) *Router {
	if resolve == nil {
		resolve = func(string) (string, string) { return "", "" }
	}
	return &Router{
		resolve: resolve,
		factory: factory,
		actors:  make(map[string]*Actor),
	}
}

func (r *Router) Start(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ctx = ctx
}

func (r *Router) Actor(key string) (*Actor, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if a, ok := r.actors[key]; ok {
		return a, nil
	}
	if r.ctx == nil {
		return nil, ErrNotStarted
	}
	workspace, hint := r.resolve(key)
	a := r.factory(key, workspace, hint)
	a.start(r.ctx)
	r.actors[key] = a
	return a, nil
}

func (r *Router) Lookup(key string) (*Actor, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.actors[key]
	return a, ok
}

func (r *Router) Shutdown() {
	r.mu.Lock()
	actors := make([]*Actor, 0, len(r.actors))
	for _, a := range r.actors {
		actors = append(actors, a)
	}
	r.mu.Unlock()
	for _, a := range actors {
		a.stop()
	}
}

func (r *Router) Keys() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	keys := make([]string, 0, len(r.actors))
	for k := range r.actors {
		keys = append(keys, k)
	}
	return keys
}

func (r *Router) Size() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.actors)
}
