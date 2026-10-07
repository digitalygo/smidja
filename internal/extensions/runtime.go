package extensions

import (
	"context"
	"errors"
	"sync"

	"github.com/digitalygo/smidja/internal/extensionui"
	"github.com/digitalygo/smidja/sdk"
)

type HostAPI func() sdk.API

type HostContext func() sdk.HandlerContext

type ContextDecorator func(signal context.Context, base sdk.HandlerContext) sdk.HandlerContext

type SignalBoundContext interface {
	sdk.HandlerContext

	WithSignal(signal context.Context) sdk.HandlerContext
}

type Runtime struct {
	registry *Registry
	api      HostAPI
	ctx      HostContext
	logger   Logger

	mu         sync.RWMutex
	decorator  ContextDecorator
	uiRegistry *extensionui.Registry
}

func NewRuntime(reg *Registry) *Runtime {
	return &Runtime{registry: reg, logger: DefaultLogger()}
}

func (r *Runtime) SetAPI(api HostAPI) *Runtime {
	r.api = api
	return r
}

func (r *Runtime) SetContext(hc HostContext) *Runtime {
	r.ctx = hc
	return r
}

func (r *Runtime) SetLogger(l Logger) *Runtime {
	r.logger = l
	return r
}

func (r *Runtime) SetUIRegistry(registry *extensionui.Registry) *Runtime {
	r.mu.Lock()
	r.uiRegistry = registry
	r.mu.Unlock()
	return r
}

func (r *Runtime) UIRegistry() *extensionui.Registry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.uiRegistry
}

func (r *Runtime) SetContextDecorator(decorator ContextDecorator) {
	r.mu.Lock()
	r.decorator = decorator
	r.mu.Unlock()
}

func (r *Runtime) Start() error {
	if r.registry == nil {
		return errors.New("extensions: runtime has no registry")
	}
	return r.registry.Setup(r.apiOr(), r.loggerOr())
}

func (r *Runtime) Dispatcher() *Dispatcher {
	return &Dispatcher{rt: r}
}

func (r *Runtime) HandlerContext(signal context.Context) sdk.HandlerContext {
	if r == nil {
		return &defaultContext{}
	}
	return r.handlerContext(signal)
}

func (r *Runtime) handlerContext(signal context.Context) sdk.HandlerContext {
	base := sdk.HandlerContext(&defaultContext{API: r.apiOr(), signal: signal})
	if r.ctx != nil {
		if provided := r.ctx(); provided != nil {
			if bound, ok := provided.(SignalBoundContext); ok {
				base = bound.WithSignal(signal)
			} else {
				base = provided
			}
		}
	}
	r.mu.RLock()
	decorator := r.decorator
	r.mu.RUnlock()
	if decorator != nil {
		return decorator(signal, base)
	}
	return base
}

func (r *Runtime) apiOr() sdk.API {
	if r.api == nil {
		return nil
	}
	return r.api()
}

func (r *Runtime) loggerOr() Logger {
	if r == nil || r.logger == nil {
		return DefaultLogger()
	}
	return r.logger
}
