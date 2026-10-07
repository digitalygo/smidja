package cli

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/models"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/internal/tools"
	"github.com/digitalygo/smidja/sdk"
)

const hostExecMaxLines = 2000

var (
	errHostClosed              = errors.New("extensions: the host session is not available")
	errHostStaleSession        = errors.New("extensions: the session context is no longer active")
	errHostCustomType          = errors.New("extensions: AppendEntry requires a non-empty custom type")
	errHostSessionName         = errors.New("extensions: SetSessionName requires a non-empty name")
	errHostEntryID             = errors.New("extensions: LabelEntry requires a non-empty entry id")
	errHostCompactPending      = errors.New("extensions: a compaction request is already pending")
	errHostCompactInstructions = errors.New("extensions: compact custom instructions are not supported by this context manager")
	errHostNothingToCompact    = errors.New("extensions: nothing to compact in the active context")
	errHostCompactCanceled     = errors.New("extensions: compaction canceled because the session changed")
	errHostCompactSettled      = errors.New("extensions: the compaction job is already settled")
)

type hostTurnCancelKey struct{}

func withHostTurnCancel(parent context.Context, cancel context.CancelFunc) context.Context {
	if parent == nil {
		parent = context.Background()
	}
	if cancel == nil {
		return parent
	}
	return context.WithValue(parent, hostTurnCancelKey{}, cancel)
}

func hostTurnCancel(signal context.Context) context.CancelFunc {
	if signal == nil {
		return nil
	}
	cancel, _ := signal.Value(hostTurnCancelKey{}).(context.CancelFunc)
	return cancel
}

func mergeContext(signal, owned context.Context) (context.Context, context.CancelFunc) {
	if signal == nil {
		signal = context.Background()
	}
	ctx, cancel := context.WithCancel(signal)
	if owned == nil || owned == signal {
		return ctx, cancel
	}
	if owned.Err() != nil {
		cancel()
		return ctx, cancel
	}
	stop := context.AfterFunc(owned, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}

type hostSessionHandle struct {
	generation uint64
	sess       *session.Session
	recorder   agent.Recorder
	id         string
	path       string
	cwd        string
	name       string
}

type hostLifecycle struct {
	shutdown     func()
	dispatch     func(func()) bool
	runScheduled func(hostScheduledTurn)
	entry        func(customType string, raw json.RawMessage)
	message      func(*session.CustomMessageEntry)
	userMessage  func(text string)
	name         func(name string)
}

type hostRuntime struct {
	baseCtx    context.Context
	cwd        string
	controller *sessionController
	catalog    *extensions.ToolCatalog
	api        sdk.API

	mu           sync.Mutex
	generation   uint64
	handle       *hostSessionHandle
	messages     []*agent.Message
	entryIDs     []string
	modelReg     *models.Registry
	modelID      string
	wireModel    string
	provider     string
	system       string
	window       int64
	execCwd      string
	execTimeout  time.Duration
	execMaxBytes int64

	sessionMu  sync.Mutex
	deliveryMu sync.Mutex
	closed     atomic.Bool

	turnMu     sync.Mutex
	turnActive bool
	loopMu     sync.Mutex

	mailboxMu sync.Mutex
	mailbox   hostMailbox
	expander  func(string) (string, error)

	compactMu     sync.Mutex
	compactJobs   map[*hostCompactJob]struct{}
	compactWG     sync.WaitGroup
	compactClosed bool

	shutdownOnce sync.Once

	lifecycleMu sync.Mutex
	lifecycle   hostLifecycle
	runCtx      context.Context
	cancelRun   context.CancelFunc

	adapterMu sync.Mutex
	adapter   *contextPreparerAdapter

	callbackMu      sync.Mutex
	callbackClosing bool
	callbackWG      sync.WaitGroup

	callbackPanics atomic.Int64
}

func newHostRuntime(ctx context.Context, cwd string, controller *sessionController, catalog *extensions.ToolCatalog) *hostRuntime {
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancelRun := context.WithCancel(ctx)
	return &hostRuntime{
		baseCtx:     ctx,
		cwd:         cwd,
		execCwd:     cwd,
		controller:  controller,
		catalog:     catalog,
		compactJobs: map[*hostCompactJob]struct{}{},
		runCtx:      runCtx,
		cancelRun:   cancelRun,
	}
}

func (h *hostRuntime) bindAPI(api sdk.API) {
	h.api = api
}

func (h *hostRuntime) bindRunContext(ctx context.Context, cancel context.CancelFunc) {
	if ctx == nil {
		return
	}
	h.lifecycleMu.Lock()
	previous := h.cancelRun
	h.runCtx = ctx
	h.cancelRun = cancel
	h.lifecycleMu.Unlock()
	if previous != nil {
		previous()
	}
}

func (h *hostRuntime) runContext() context.Context {
	h.lifecycleMu.Lock()
	defer h.lifecycleMu.Unlock()
	if h.runCtx != nil {
		return h.runCtx
	}
	return h.baseCtx
}

func (h *hostRuntime) hostOptions() *extensions.Host {
	return &extensions.Host{
		SetActiveTools: func(names []string) error {
			if h.catalog == nil {
				return extensions.ErrUnavailable
			}
			return h.catalog.SetActive(names)
		},
		AppendEntry: func(customType string, data any) error {
			return h.appendEntry(h.snapshot(), customType, data)
		},
		SetSessionName: func(name string) error {
			return h.setSessionName(h.snapshot(), name)
		},
		LabelEntry: func(entryID, label string) error {
			return h.labelEntry(h.snapshot(), entryID, label)
		},
		SendMessage: func(msg sdk.CustomMessage, opts sdk.SendOptions) error {
			return h.sendMessage(nil, nil, msg, opts)
		},
		SendUserMessage: func(text string, opts sdk.SendOptions) error {
			return h.sendUserMessage(nil, nil, text, opts)
		},
		Exec: func(ctx context.Context, command string, args []string, opts sdk.ExecOptions) (*sdk.ExecResult, error) {
			return h.exec(ctx, command, args, opts)
		},
	}
}

func (h *hostRuntime) context() sdk.HandlerContext {
	return &hostHandlerContext{API: h.api, host: h, state: h.captureContextState()}
}

func (h *hostRuntime) bindSession(sess *session.Session, recorder agent.Recorder, id, path, cwd, name string) {
	if sess == nil {
		return
	}
	var failures []compactFailure
	h.sessionMu.Lock()
	h.compactMu.Lock()
	h.mu.Lock()
	if !h.closed.Load() {
		h.generation++
		h.handle = &hostSessionHandle{
			generation: h.generation,
			sess:       sess,
			recorder:   recorder,
			id:         id,
			path:       path,
			cwd:        cwd,
			name:       name,
		}
	}
	generation := h.generation
	h.mu.Unlock()
	h.mailboxMu.Lock()
	h.mailbox = hostMailbox{generation: generation}
	h.mailboxMu.Unlock()
	if !h.closed.Load() {
		failures = h.settleJobsLocked(func(job *hostCompactJob) bool {
			return job.handle == nil || job.handle.generation != generation
		}, errHostStaleSession)
	}
	h.compactMu.Unlock()
	h.sessionMu.Unlock()
	if len(failures) > 0 {
		h.deferCompactFailures(failures)
	}
}

func (h *hostRuntime) setMessages(history []*agent.Message) {
	h.mu.Lock()
	h.messages = append([]*agent.Message(nil), history...)
	h.mu.Unlock()
}

func (h *hostRuntime) setEntryIDs(entryIDs []string) {
	h.mu.Lock()
	h.entryIDs = append([]string(nil), entryIDs...)
	h.mu.Unlock()
}

func (h *hostRuntime) modelHistorySnapshot() ([]*agent.Message, []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]*agent.Message(nil), h.messages...), append([]string(nil), h.entryIDs...)
}

func (h *hostRuntime) setModel(reg *models.Registry, modelID, wireModel, provider string) {
	h.mu.Lock()
	h.modelReg = reg
	h.modelID = modelID
	h.wireModel = wireModel
	h.provider = provider
	h.mu.Unlock()
}

func (h *hostRuntime) setSystem(system string) {
	h.mu.Lock()
	h.system = system
	h.mu.Unlock()
}

func (h *hostRuntime) setWindow(window int64) {
	h.mu.Lock()
	h.window = window
	h.mu.Unlock()
}

func (h *hostRuntime) setExecLimits(timeout time.Duration, maxBytes int64) {
	h.mu.Lock()
	h.execTimeout = timeout
	h.execMaxBytes = maxBytes
	h.mu.Unlock()
}

func (h *hostRuntime) setExecCwd(cwd string) {
	if strings.TrimSpace(cwd) == "" {
		return
	}
	h.mu.Lock()
	h.execCwd = cwd
	h.mu.Unlock()
}

func (h *hostRuntime) attachPreparer(adapter *contextPreparerAdapter) {
	if adapter == nil {
		return
	}
	adapter.setExplicit(h.runExplicitCompact)
	h.adapterMu.Lock()
	h.adapter = adapter
	h.adapterMu.Unlock()
}

func (h *hostRuntime) currentPreparer() *contextPreparerAdapter {
	h.adapterMu.Lock()
	defer h.adapterMu.Unlock()
	return h.adapter
}

func (h *hostRuntime) setLifecycle(lifecycle hostLifecycle) {
	h.lifecycleMu.Lock()
	h.lifecycle = lifecycle
	h.lifecycleMu.Unlock()
	h.scheduleMailbox()
}

func copyHostHandle(handle *hostSessionHandle) *hostSessionHandle {
	if handle == nil {
		return nil
	}
	clone := *handle
	return &clone
}

func (h *hostRuntime) snapshot() *hostSessionHandle {
	h.mu.Lock()
	defer h.mu.Unlock()
	return copyHostHandle(h.handle)
}

func (h *hostRuntime) beginTurn() {
	h.turnMu.Lock()
	h.turnActive = true
	h.turnMu.Unlock()
}

func (h *hostRuntime) endTurn(canceled ...bool) error {
	wasCanceled := len(canceled) > 0 && canceled[0]
	var (
		adapter *contextPreparerAdapter
		opts    sdk.CompactOptions
		compact bool
	)
	h.turnMu.Lock()
	adapter = h.currentPreparer()
	if adapter != nil {
		opts, compact = adapter.takeCompact()
	}
	var deliveryErr error
	h.mailboxMu.Lock()
	h.turnActive = false
	if wasCanceled {
		h.mailbox.steer = nil
		h.mailbox.followUp = nil
		h.mailbox.deferred = nil
		h.mailbox.continuationQueued = false
		h.mailbox.skipSteerOnce = false
		h.invalidateContinuationLocked()
	} else if len(h.mailbox.steer) > 0 || len(h.mailbox.followUp) > 0 {
		h.queueContinuationLocked()
	}
	h.mailboxMu.Unlock()
	h.turnMu.Unlock()
	if !wasCanceled {
		for {
			h.mailboxMu.Lock()
			delivery, remaining, found := popHostDelivery(h.mailbox.deferred)
			h.mailbox.deferred = remaining
			h.mailboxMu.Unlock()
			if !found {
				break
			}
			if _, err := h.persistDelivery(delivery, false); err != nil {
				deliveryErr = err
				break
			}
		}
	}
	h.scheduleMailbox()
	if compact {
		if h.closed.Load() {
			h.reportCompactFailures([]compactFailure{{opts: opts, err: errHostClosed}})
		} else {
			h.dispatchCompactJob(h.captureCompactRequest(nil, opts), func(job *hostCompactJob) {
				h.runIdleCompact(nil, adapter, job)
			})
		}
	}
	return deliveryErr
}

func (h *hostRuntime) abort(signal context.Context) {
	if cancel := hostTurnCancel(signal); cancel != nil {
		cancel()
	}
}

func (h *hostRuntime) exec(signal context.Context, command string, args []string, opts sdk.ExecOptions) (*sdk.ExecResult, error) {
	if strings.TrimSpace(command) == "" {
		return nil, errors.New("exec: command must not be empty")
	}
	h.mu.Lock()
	cwd := h.execCwd
	storedTimeout := h.execTimeout
	maxBytes := h.execMaxBytes
	h.mu.Unlock()
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = storedTimeout
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := mergeContext(signal, h.runContext())
	defer cancel()
	res, err := tools.ExecDirect(ctx, command, args, cwd, tools.DirectExecOptions{
		Timeout:  timeout,
		MaxLines: hostExecMaxLines,
		MaxBytes: maxBytes,
	})
	if err != nil {
		return nil, err
	}
	return &sdk.ExecResult{Stdout: res.Stdout, Stderr: res.Stderr, Code: res.Code, Killed: res.Killed}, nil
}

func (h *hostRuntime) appendMessage(message *agent.Message) {
	if message == nil {
		return
	}
	h.mu.Lock()
	next := make([]*agent.Message, len(h.messages), len(h.messages)+1)
	copy(next, h.messages)
	h.messages = append(next, message)
	h.mu.Unlock()
}
