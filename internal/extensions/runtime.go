package extensions

import (
	"context"
	"errors"

	"github.com/digitalygo/smidja/sdk"
)

// HostAPI is the seam through which the harness hands the extension API
// to the runtime. The API implementation itself is another wave's job;
// the host supplies it as a function so it can be constructed lazily or
// swapped in tests. The function must be safe to call concurrently: the
// runtime calls it once per dispatch to build the default handler context
// and once at Start for the setup phase.
type HostAPI func() sdk.API

// HostContext optionally supplies the full handler context of a dispatch,
// bypassing the default context the runtime builds around HostAPI. Set it
// when the harness backs sdk.HandlerContext directly (UI, mode, session,
// model registry, ...); the default context exposes only the API and
// zero-value run state. The function must be safe to call concurrently
// and is called once per dispatch.
type HostContext func() sdk.HandlerContext

// Runtime wires the extension registry to the harness: it owns the
// dispatch logger and the seams through which the host provides the
// extension API (SetAPI) and the per-dispatch handler context
// (SetContext). Start runs the registry setup phase once; Dispatcher
// returns the dispatcher the loop and the CLI call for the phase 1
// events.
//
// The zero value has no registry and logs to standard error; dispatchers
// built from it behave as dispatchers with no handlers.
type Runtime struct {
	registry *Registry
	api      HostAPI
	ctx      HostContext
	logger   Logger
}

// NewRuntime returns a runtime over the given registry, with the default
// stderr logger and no host seams yet. Call SetAPI (and optionally
// SetContext and SetLogger) before Start.
func NewRuntime(reg *Registry) *Runtime {
	return &Runtime{registry: reg, logger: DefaultLogger()}
}

// SetAPI installs the host API seam used for the setup phase and for the
// default handler context. It returns the runtime for chaining.
func (r *Runtime) SetAPI(api HostAPI) *Runtime {
	r.api = api
	return r
}

// SetContext installs the host handler-context seam. When set, every
// handler receives the context this function returns, and HostAPI is only
// used for the setup phase. It returns the runtime for chaining.
func (r *Runtime) SetContext(hc HostContext) *Runtime {
	r.ctx = hc
	return r
}

// SetLogger installs the logger used for setup and dispatch diagnostics.
// The default logger writes to standard error; tests inject a recorder. It
// returns the runtime for chaining.
func (r *Runtime) SetLogger(l Logger) *Runtime {
	r.logger = l
	return r
}

// Start runs the setup phase: every registered extension implementing
// sdk.SetupHook receives Setup(api) once, in registration order, with the
// API from the host seam. Per-extension failures are logged and the
// extension's hooks are disabled; the remaining extensions still run.
// Calling Start twice returns ErrSetupAlreadyRun, as does calling Setup
// on the registry directly a second time.
func (r *Runtime) Start() error {
	if r.registry == nil {
		return errors.New("extensions: runtime has no registry")
	}
	return r.registry.Setup(r.apiOr(), r.loggerOr())
}

// Dispatcher returns the dispatcher over the runtime's registry. It is
// safe to call Dispatcher and dispatch from multiple goroutines.
func (r *Runtime) Dispatcher() *Dispatcher {
	return &Dispatcher{rt: r}
}

// handlerContext builds the handler context of one dispatch: the
// host-provided context when one is set, otherwise the default context
// wrapping the host API, with the dispatch signal set to the request
// context.
func (r *Runtime) handlerContext(signal context.Context) sdk.HandlerContext {
	if r.ctx != nil {
		return r.ctx()
	}
	return &defaultContext{API: r.apiOr(), signal: signal}
}

// apiOr returns the host API, or nil when no seam is installed.
func (r *Runtime) apiOr() sdk.API {
	if r.api == nil {
		return nil
	}
	return r.api()
}

// loggerOr returns the runtime's logger, or the default one when none is
// installed.
func (r *Runtime) loggerOr() Logger {
	if r == nil || r.logger == nil {
		return DefaultLogger()
	}
	return r.logger
}
