package extensions

import (
	"context"
	"errors"

	"github.com/digitalygo/smidja/sdk"
)

type HostAPI func() sdk.API

type HostContext func() sdk.HandlerContext

type Runtime struct {
	registry *Registry
	api      HostAPI
	ctx      HostContext
	logger   Logger
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
	if r.ctx != nil {
		return r.ctx()
	}
	return &defaultContext{API: r.apiOr(), signal: signal}
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
