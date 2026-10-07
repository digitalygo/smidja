package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/internal/tui"
	"github.com/digitalygo/smidja/internal/tui/interactive"
	"github.com/digitalygo/smidja/internal/ui"
	"github.com/digitalygo/smidja/sdk"
)

type switchWriter struct {
	mu     sync.Mutex
	target io.Writer
}

func (w *switchWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.target.Write(p)
}

func (w *switchWriter) Set(target io.Writer) {
	w.mu.Lock()
	w.target = target
	w.mu.Unlock()
}

type tuiCaptureWriter struct {
	mu        sync.Mutex
	capturing bool
	buffer    bytes.Buffer
	fallback  io.Writer
}

func (w *tuiCaptureWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.capturing {
		return w.buffer.Write(p)
	}
	return w.fallback.Write(p)
}

func (w *tuiCaptureWriter) Begin() {
	w.mu.Lock()
	w.capturing = true
	w.buffer.Reset()
	w.mu.Unlock()
}

func (w *tuiCaptureWriter) End() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.capturing = false
	text := strings.TrimSpace(w.buffer.String())
	w.buffer.Reset()
	return text
}

type tuiNoticeWriter struct {
	runner *ui.Runner
}

func (w *tuiNoticeWriter) Write(p []byte) (int, error) {
	text := strings.TrimSpace(string(p))
	if text != "" && w.runner != nil {
		w.runner.Notify(text, sdk.NotifyWarning)
	}
	return len(p), nil
}

type tuiTurnHandle struct {
	once   sync.Once
	cancel context.CancelFunc
}

func (h *tuiTurnHandle) cancelOnce() {
	h.once.Do(h.cancel)
}

const workerPanicNotice = "internal error: the last request failed unexpectedly"

type tuiLifecycle struct {
	mu             sync.Mutex
	queued         *sync.Cond
	stopping       bool
	pending        []func()
	sessionChanges int
	active         *tuiTurnHandle
	workerDone     chan struct{}
	onPanic        func()

	stopCh chan struct{}
	gate   chan struct{}
}

func newTuiLifecycle() *tuiLifecycle {
	l := &tuiLifecycle{workerDone: make(chan struct{}), stopCh: make(chan struct{})}
	l.queued = sync.NewCond(&l.mu)
	go l.work()
	return l
}

func (l *tuiLifecycle) work() {
	defer close(l.workerDone)
	for {
		job, ok := l.next()
		if !ok {
			return
		}
		if !l.awaitAdmission() {
			return
		}
		l.runJob(job)
	}
}

func (l *tuiLifecycle) holdAdmission() {
	l.mu.Lock()
	if !l.stopping && l.gate == nil {
		l.gate = make(chan struct{})
	}
	l.mu.Unlock()
}

func (l *tuiLifecycle) openAdmission() {
	l.mu.Lock()
	if l.gate != nil {
		close(l.gate)
		l.gate = nil
	}
	l.mu.Unlock()
}

func (l *tuiLifecycle) awaitAdmission() bool {
	l.mu.Lock()
	gate := l.gate
	stopping := l.stopping
	l.mu.Unlock()
	if stopping {
		return false
	}
	if gate == nil {
		return true
	}
	select {
	case <-gate:
		return true
	case <-l.stopCh:
		return false
	}
}

func (l *tuiLifecycle) runJob(job func()) {
	defer func() {
		if recovered := recover(); recovered != nil {
			l.beginShutdown()
			if l.onPanic != nil {
				l.onPanic()
			}
		}
	}()
	job()
}

func (l *tuiLifecycle) next() (func(), bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for len(l.pending) == 0 && !l.stopping {
		l.queued.Wait()
	}
	if len(l.pending) == 0 {
		return nil, false
	}
	job := l.pending[0]
	l.pending = l.pending[1:]
	return job, true
}

func (l *tuiLifecycle) enqueue(job func()) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.stopping {
		return false
	}
	l.pending = append(l.pending, job)
	l.queued.Signal()
	return true
}

func (l *tuiLifecycle) enqueueSessionChange(job func()) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.stopping {
		return false
	}
	l.sessionChanges++
	l.pending = append(l.pending, job)
	l.queued.Signal()
	return true
}

func (l *tuiLifecycle) finishSessionChange() {
	l.mu.Lock()
	if l.sessionChanges > 0 {
		l.sessionChanges--
	}
	l.mu.Unlock()
}

func (l *tuiLifecycle) sessionChangePending() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sessionChanges > 0
}

func (l *tuiLifecycle) beginShutdown() {
	l.mu.Lock()
	if !l.stopping {
		l.stopping = true
		close(l.stopCh)
	}
	l.pending = nil
	l.queued.Broadcast()
	l.mu.Unlock()
	l.cancelTurn()
}

func (l *tuiLifecycle) wait() {
	<-l.workerDone
}

func (l *tuiLifecycle) trackTurn(handle *tuiTurnHandle) {
	l.mu.Lock()
	l.active = handle
	l.mu.Unlock()
}

func (l *tuiLifecycle) completeTurn(handle *tuiTurnHandle) {
	l.mu.Lock()
	owned := l.active == handle
	if owned {
		l.active = nil
	}
	l.mu.Unlock()
	if owned {
		handle.cancelOnce()
	}
}

func (l *tuiLifecycle) cancelTurn() {
	l.mu.Lock()
	handle := l.active
	l.active = nil
	l.mu.Unlock()
	if handle != nil {
		handle.cancelOnce()
	}
}

type tuiBridge struct {
	ctx        context.Context
	cancelWork context.CancelFunc
	rd         *runDeps
	runner     *ui.Runner
	capture    *tuiCaptureWriter

	lifecycle *tuiLifecycle
	history   []*agent.Message
	entryIDs  []string
	sessions  *sessionController

	projectionStale bool
	pendingNotice   string
	applyStep       func(next *activeSession) error

	afterTurn func()
}

func newTuiBridge(ctx context.Context, cancelWork context.CancelFunc, rd *runDeps, runner *ui.Runner, capture *tuiCaptureWriter) *tuiBridge {
	bridge := &tuiBridge{
		ctx:        ctx,
		cancelWork: cancelWork,
		rd:         rd,
		runner:     runner,
		capture:    capture,
		lifecycle:  newTuiLifecycle(),
		sessions:   rd.controller,
	}
	bridge.lifecycle.onPanic = func() {
		bridge.runner.Surface().AddNotice(interactive.NoticeError, workerPanicNotice)
		bridge.runner.RequestExit()
	}
	return bridge
}

type tuiCommandContext struct {
	sdk.HandlerContext
	bridge     *tuiBridge
	invocation *commandInvocation
}

var _ sdk.CommandContext = (*tuiCommandContext)(nil)

type commandInvocation struct {
	signal   context.Context
	active   atomic.Bool
	mu       sync.Mutex
	opCtx    context.Context
	opCancel context.CancelFunc
	wg       sync.WaitGroup
	mutCh    chan struct{}
}

func newCommandInvocation(signal context.Context) *commandInvocation {
	parent := signal
	if parent == nil {
		parent = context.Background()
	}
	opCtx, cancel := context.WithCancel(parent)
	mutCh := make(chan struct{}, 1)
	mutCh <- struct{}{}
	invocation := &commandInvocation{signal: signal, opCtx: opCtx, opCancel: cancel, mutCh: mutCh}
	invocation.active.Store(true)
	return invocation
}

func (i *commandInvocation) invalidate() {
	if i == nil {
		return
	}
	i.mu.Lock()
	i.active.Store(false)
	cancel := i.opCancel
	i.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	i.wg.Wait()
}

func (i *commandInvocation) check(signal context.Context) error {
	if i == nil || !i.active.Load() {
		return sdk.ErrModeUnsupported
	}
	if signal != nil && signal.Err() != nil {
		return signal.Err()
	}
	if i.signal != nil && i.signal.Err() != nil {
		return i.signal.Err()
	}
	return nil
}

func (i *commandInvocation) beginMutation(signal context.Context) (context.Context, func(), error) {
	if i == nil {
		return nil, nil, sdk.ErrModeUnsupported
	}
	if signal != nil && signal.Err() != nil {
		return nil, nil, signal.Err()
	}
	i.mu.Lock()
	if !i.active.Load() {
		i.mu.Unlock()
		return nil, nil, sdk.ErrModeUnsupported
	}
	if i.signal != nil && i.signal.Err() != nil {
		err := i.signal.Err()
		i.mu.Unlock()
		return nil, nil, err
	}
	op := i.opCtx
	mutCh := i.mutCh
	i.mu.Unlock()
	if mutCh == nil {
		i.mu.Lock()
		defer i.mu.Unlock()
		if op == nil {
			op = context.Background()
		}
		i.wg.Add(1)
		return op, func() { i.wg.Done() }, nil
	}
	var signalDone <-chan struct{}
	if signal != nil {
		signalDone = signal.Done()
	}
	var opDone <-chan struct{}
	if op != nil {
		opDone = op.Done()
	}
	select {
	case <-mutCh:
	case <-opDone:
		if op != nil {
			return nil, nil, op.Err()
		}
		return nil, nil, context.Canceled
	case <-signalDone:
		if signal != nil {
			return nil, nil, signal.Err()
		}
		return nil, nil, context.Canceled
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if !i.active.Load() {
		select {
		case mutCh <- struct{}{}:
		default:
		}
		return nil, nil, sdk.ErrModeUnsupported
	}
	if signal != nil && signal.Err() != nil {
		select {
		case mutCh <- struct{}{}:
		default:
		}
		return nil, nil, signal.Err()
	}
	if op != nil && op.Err() != nil {
		select {
		case mutCh <- struct{}{}:
		default:
		}
		return nil, nil, op.Err()
	}
	if op == nil {
		op = context.Background()
	}
	i.wg.Add(1)
	done := func() {
		select {
		case mutCh <- struct{}{}:
		default:
		}
		i.wg.Done()
	}
	return op, done, nil
}

func (c *tuiCommandContext) sessionControlError() error {
	if c.bridge == nil || c.bridge.sessions == nil {
		return errors.New("session: session control is unavailable")
	}
	return nil
}

func (c *tuiCommandContext) mutationError() error {
	var signal context.Context
	if c.HandlerContext != nil {
		signal = c.HandlerContext.Signal()
	}
	return c.invocation.check(signal)
}

func (c *tuiCommandContext) WaitForIdle() error {
	return sdk.ErrModeUnsupported
}

func (c *tuiCommandContext) NewSession(opts sdk.NewSessionOptions) (*sdk.SessionSwitchResult, error) {
	if opts.Setup != nil || opts.WithSession != nil || strings.TrimSpace(opts.ParentSession) != "" {
		return nil, fmt.Errorf("new session: unsupported option for the interactive session controller")
	}
	if err := c.sessionControlError(); err != nil {
		return nil, err
	}
	opCtx, done, err := c.invocation.beginMutation(c.commandSignal())
	if err != nil {
		return nil, err
	}
	defer done()
	if err := c.bridge.newSessionWithSignal(opCtx, ""); err != nil {
		return nil, err
	}
	return &sdk.SessionSwitchResult{}, nil
}

func (c *tuiCommandContext) Fork(entryID string, opts sdk.ForkOptions) (*sdk.SessionSwitchResult, error) {
	if opts.WithSession != nil || (opts.Position != "" && opts.Position != "end") {
		return nil, fmt.Errorf("fork: unsupported option for the interactive session controller")
	}
	if err := c.sessionControlError(); err != nil {
		return nil, err
	}
	opCtx, done, err := c.invocation.beginMutation(c.commandSignal())
	if err != nil {
		return nil, err
	}
	defer done()
	if err := c.bridge.forkSessionWithSignal(opCtx, entryID); err != nil {
		return nil, err
	}
	return &sdk.SessionSwitchResult{}, nil
}

func (c *tuiCommandContext) NavigateTree(targetID string, opts sdk.TreeOptions) (*sdk.SessionSwitchResult, error) {
	return nil, sdk.ErrModeUnsupported
}

func (c *tuiCommandContext) SwitchSession(path string, opts sdk.SwitchOptions) (*sdk.SessionSwitchResult, error) {
	if opts.WithSession != nil {
		return nil, fmt.Errorf("switch session: unsupported option for the interactive session controller")
	}
	if err := c.sessionControlError(); err != nil {
		return nil, err
	}
	opCtx, done, err := c.invocation.beginMutation(c.commandSignal())
	if err != nil {
		return nil, err
	}
	defer done()
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("switch session: a session path is required")
	}
	if err := c.bridge.resumeSessionWithSignal(opCtx, path); err != nil {
		return nil, err
	}
	return &sdk.SessionSwitchResult{}, nil
}

func (c *tuiCommandContext) Reload() error {
	return sdk.ErrModeUnsupported
}

func (c *tuiCommandContext) commandSignal() context.Context {
	if c.HandlerContext == nil {
		return nil
	}
	return c.HandlerContext.Signal()
}

func (c *tuiCommandContext) runInput(input string) error {
	c.bridge.runUserTurn(input)
	return nil
}

func runTUI(ctx context.Context, d *Deps, rd *runDeps, lineUI *ui.LineUI, mode ui.TUIMode, workspace, projectPath string, skillOut *switchWriter, newTerminal func(io.Reader, io.Writer) tui.Terminal, runtime *extensions.Runtime, startup *tuiStartup) error {
	capture := &tuiCaptureWriter{fallback: d.Stdout}
	var runner *ui.Runner
	if startup != nil && startup.runner != nil {
		runner = startup.runner
	} else {
		runner = ui.NewRunner(ui.RunnerOptions{
			Stdin:         d.Stdin,
			Stdout:        d.Stdout,
			Mode:          mode,
			Title:         "smidja",
			Home:          d.Home(),
			WorkspaceRoot: workspace,
			ProjectPath:   projectPath,
			NewTerminal:   newTerminal,
			ImagesEnabled: true,
		})
	}
	rdTUI := *rd
	rdTUI.stderr = &tuiNoticeWriter{runner: runner}
	workCtx, cancelWork := context.WithCancel(ctx)
	defer cancelWork()
	bridge := newTuiBridge(workCtx, cancelWork, &rdTUI, runner, capture)
	if rd.host != nil {
		rd.host.bindRunContext(workCtx, cancelWork)
	}
	bridge.holdAdmission()
	var initial *activeSession
	if rd.controller != nil && rd.env != nil && rd.sess != nil {
		rd.controller.SetPreparer(func(candidate *session.Session, mode sessionLoadMode) (*activeSession, error) {
			return buildActiveSession(rd.env, bridge.rd, candidate, mode)
		})
		loadMode := sessionModeNew
		if rd.resumedSession {
			loadMode = sessionModeResume
		}
		prepared, err := rd.controller.Adopt(rd.sess, loadMode)
		if err != nil {
			bridge.shutdown()
			bridge.wait()
			return err
		}
		initial = prepared
		bridge.history = prepared.history
		bridge.entryIDs = prepared.entryIDs
		bridge.syncHostSession()
		if rd.host != nil {
			rd.host.bindSession(prepared.sess, prepared.recorder, prepared.sess.ID(), prepared.path, rd.cwd, prepared.name)
			rd.host.attachPreparer(prepared.preparer)
		}
	}
	runner.SetOnSubmit(bridge.submit)
	runner.SetOnInterrupt(bridge.interrupt)
	if startup == nil {
		if err := runner.Start(); err != nil {
			fmt.Fprintf(d.Stderr, "smidja: tui unavailable (%v), using line mode\n", err)
			_ = rd.hooks.SessionStart(ctx, string(sdk.SessionStartStartup))
			defer rd.hooks.SessionShutdown(ctx, string(sdk.SessionShutdownQuit))
			bridge.shutdown()
			bridge.wait()
			if rd.host != nil {
				rd.host.setLifecycle(hostLifecycle{shutdown: cancelWork})
			}
			return repl(ctx, lineUI, rd)
		}
	}
	uiRegistry := rd.uiRegistry
	if uiRegistry == nil && runtime != nil {
		uiRegistry = runtime.UIRegistry()
	}
	if uiRegistry != nil {
		if err := runner.AttachExtensionUI(uiRegistry); err != nil {
			runner.Surface().AddNotice(interactive.NoticeWarning, "extensions: "+err.Error())
		}
	}
	if runtime != nil {
		runtime.SetContextDecorator(func(signal context.Context, base sdk.HandlerContext) sdk.HandlerContext {
			return runner.InteractiveHandlerContext(signal, base)
		})
		defer runtime.SetContextDecorator(nil)
	}
	if rd.host != nil {
		rd.host.setLifecycle(bridge.tuiHostLifecycle(runner))
		defer rd.host.setLifecycle(hostLifecycle{})
	}
	if skillOut != nil {
		skillOut.Set(capture)
	}
	surface := runner.Surface()
	surface.SetModel(rd.model)
	surface.SetWorkspace(workspace)
	surface.SetSessionName(rd.sessionPath)
	surface.SetRenderMetadata(interactive.RenderMetadata{Cwd: workspace, SessionID: rd.sessionPath, Model: rd.model})
	if initial != nil {
		bridge.replaySession(initial)
	}
	bridge.syncCommandInventory()
	startupCtx, cancelStartup := context.WithCancel(ctx)
	defer cancelStartup()
	startupDone := make(chan error, 1)
	go func() {
		startupDone <- rd.hooks.SessionStart(startupCtx, string(sdk.SessionStartStartup))
	}()
	stopStartup := func() {
		cancelStartup()
		runner.CancelDialogs()
		<-startupDone
	}
	var runErr error
	select {
	case <-runner.Done():
		stopStartup()
	case <-ctx.Done():
		runErr = ctx.Err()
		stopStartup()
	case err := <-startupDone:
		if err == nil {
			bridge.syncCommandInventory()
			bridge.openAdmission()
		}
		select {
		case <-runner.Done():
		case <-ctx.Done():
			runErr = ctx.Err()
		}
	}
	runner.CancelDialogs()
	bridge.shutdown()
	bridge.wait()
	_ = rd.hooks.SessionShutdown(ctx, string(sdk.SessionShutdownQuit))
	runner.Stop()
	return runErr
}

func (b *tuiBridge) tuiHostLifecycle(runner *ui.Runner) hostLifecycle {
	return hostLifecycle{
		shutdown: runner.RequestExit,
		dispatch: func(job func()) bool { return b.lifecycle.enqueue(job) },
		runScheduled: func(turn hostScheduledTurn) {
			if turn.external {
				b.runUserTurn(turn.text)
				return
			}
			b.runScheduledContinuation(turn)
		},
		entry: func(customType string, raw json.RawMessage) {
			runner.DeliverCustomEntry(interactive.CustomEntryView{CustomType: customType, Data: raw, Text: string(raw)})
		},
		message: func(entry *session.CustomMessageEntry) {
			runner.DeliverCustomMessage(interactive.CustomEntryView{
				CustomType: entry.CustomType,
				Text:       rawContentText(entry.Content),
				Data:       append(json.RawMessage(nil), entry.Details...),
			})
		},
		userMessage: func(text string) {
			if surface := runner.Surface(); surface != nil {
				surface.AddUserMessage(text)
			}
		},
		name: func(name string) {
			if surface := runner.Surface(); surface != nil {
				surface.SetSessionName(name)
				surface.RequestRender()
			}
		},
		model: func(model string) {
			if surface := runner.Surface(); surface != nil {
				surface.SetModel(model)
			}
		},
		thinking: func(level string) {
			if surface := runner.Surface(); surface != nil {
				surface.SetThinkingLevel(level)
			}
		},
	}
}

func (b *tuiBridge) holdAdmission() {
	b.lifecycle.holdAdmission()
}

func (b *tuiBridge) openAdmission() {
	b.lifecycle.openAdmission()
}

func (b *tuiBridge) shutdown() {
	if b.rd != nil && b.rd.host != nil {
		b.rd.host.shutdown()
	}
	b.lifecycle.beginShutdown()
	if b.cancelWork != nil {
		b.cancelWork()
	}
}

func (b *tuiBridge) wait() {
	b.lifecycle.wait()
	if b.rd != nil && b.rd.host != nil {
		b.rd.host.waitCallbacks()
	}
}

func (b *tuiBridge) submit(input string) {
	_, sessionChange := queuedSessionChange(input)
	job := func() {
		b.handle(input)
		if sessionChange {
			b.lifecycle.finishSessionChange()
		}
		b.scheduleEditorFollowUps()
		if b.afterTurn != nil {
			b.afterTurn()
		}
	}
	if sessionChange {
		b.lifecycle.enqueueSessionChange(job)
		return
	}
	b.lifecycle.enqueue(job)
}

func queuedSessionChange(input string) (string, bool) {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "/") {
		return "", false
	}
	name, _ := splitCommandInput(trimmed)
	switch name {
	case "new", "fork", "resume", "sessions", "tree":
		return name, true
	default:
		return name, false
	}
}

func (b *tuiBridge) scheduleEditorFollowUps() {
	if b.runner == nil || b.runner.Surface() == nil || b.runner.Surface().Editor().QueuedCount() == 0 {
		return
	}
	b.lifecycle.enqueue(func() {
		messages := b.runner.Surface().Editor().TakeQueued()
		for _, text := range messages {
			if b.rd != nil && b.rd.host != nil {
				if err := b.rd.host.sendUserMessage(b.rd.host.snapshot(), nil, text, sdk.SendOptions{DeliverAs: sdk.DeliveryFollowUp}); err != nil {
					b.runner.Surface().AddNotice(interactive.NoticeWarning, err.Error())
				}
				continue
			}
			b.handle(text)
		}
	})
}

func (b *tuiBridge) interrupt() {
	b.lifecycle.cancelTurn()
}

func (b *tuiBridge) handle(input string) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return
	}
	if strings.HasPrefix(trimmed, "/") {
		b.handleSlash(trimmed)
		return
	}
	b.runUserTurn(trimmed)
}

func (b *tuiBridge) handleSlash(input string) {
	name, args := splitCommandInput(input)
	if !b.dispatchCommand(name, args) {
		b.runner.Surface().AddNotice(interactive.NoticeWarning, "unknown command /"+name)
	}
	b.syncCommandInventory()
}

func (b *tuiBridge) runUserTurn(input string) {
	surface := b.runner.Surface()
	surface.AddUserMessage(input)
	if err := b.refreshProjection(); err != nil {
		surface.AddNotice(interactive.NoticeError, "session: cannot reload the active session: "+err.Error())
		return
	}
	turnCtx, cancel := context.WithCancel(b.ctx)
	turnCtx = withHostTurnCancel(turnCtx, cancel)
	handle := &tuiTurnHandle{cancel: cancel}
	b.lifecycle.trackTurn(handle)
	defer b.lifecycle.completeTurn(handle)
	defer b.syncCommandInventory()
	b.runner.SetWorking(true)
	defer b.runner.SetWorking(false)
	scope := ui.NewTurnScope(surface, b.runner.Active)
	decorator := ui.NewHookDecorator(b.rd.hooks, scope)
	boundary := len(b.history)
	history, err := runTurn(turnCtx, b.rd, b.loopDeps(scope, decorator), b.history, input)
	b.history = history
	if reloadErr := b.refreshProjection(); reloadErr != nil {
		surface.AddNotice(interactive.NoticeError, "session: cannot reload the active session: "+reloadErr.Error())
	}
	if err != nil {
		var persistErr *persistError
		isPersist := errors.As(err, &persistErr)
		if turnCtx.Err() != nil {
			if isPersist {
				surface.AddNotice(interactive.NoticeWarning, persistErr.Error())
			}
			scope.FinalizeAuthoritative(false, nil, "aborted", "")
			if !scope.Opened() {
				surface.AddNotice(interactive.NoticeWarning, "interrupted")
			}
			return
		}
		if asst, ok := authoritativeSince(history, boundary); ok && asst.StopReason != "toolUse" {
			reason := asst.StopReason
			if reason == "" {
				reason = "error"
			}
			message := asst.ErrorMessage
			if message == "" && reason == "error" {
				message = err.Error()
			}
			if usage, _, ok := lastAssistantUsageSince(history, boundary); ok {
				surface.SetUsage(interactive.UsageSummary{
					Input:      usage.Input,
					Output:     usage.Output,
					CacheRead:  usage.CacheRead,
					CacheWrite: usage.CacheWrite,
					Cost:       usage.Cost.Total,
				})
			}
			parts := ui.AssistantParts(&agent.Message{Assistant: asst})
			scope.FinalizeAuthoritative(true, parts, reason, message)
			if !scope.Opened() {
				notice := message
				if notice == "" {
					notice = err.Error()
				}
				surface.AddNotice(interactive.NoticeWarning, notice)
				if isPersist && err.Error() != notice {
					surface.AddNotice(interactive.NoticeWarning, err.Error())
				}
				return
			}
			if reason != "error" || isPersist {
				surface.AddNotice(interactive.NoticeWarning, err.Error())
			}
			return
		}
		if usage, _, ok := lastAssistantUsageSince(history, boundary); ok {
			surface.SetUsage(interactive.UsageSummary{
				Input:      usage.Input,
				Output:     usage.Output,
				CacheRead:  usage.CacheRead,
				CacheWrite: usage.CacheWrite,
				Cost:       usage.Cost.Total,
			})
		}
		scope.FinalizeAuthoritative(false, nil, "error", err.Error())
		if !scope.Opened() {
			surface.AddNotice(interactive.NoticeWarning, err.Error())
		}
		return
	}
	asst, ok := authoritativeSince(history, boundary)
	if !ok {
		scope.FinalizeAuthoritative(false, nil, "", "")
		return
	}
	if usage, _, ok := lastAssistantUsageSince(history, boundary); ok {
		surface.SetUsage(interactive.UsageSummary{
			Input:      usage.Input,
			Output:     usage.Output,
			CacheRead:  usage.CacheRead,
			CacheWrite: usage.CacheWrite,
			Cost:       usage.Cost.Total,
		})
	}
	parts := ui.AssistantParts(&agent.Message{Assistant: asst})
	scope.FinalizeAuthoritative(true, parts, asst.StopReason, asst.ErrorMessage)
	if !scope.Opened() && (asst.StopReason == "aborted" || asst.StopReason == "error") {
		notice := asst.ErrorMessage
		if notice == "" && asst.StopReason == "aborted" {
			notice = "interrupted"
		}
		if notice == "" {
			notice = "interrupted"
		}
		surface.AddNotice(interactive.NoticeWarning, notice)
	}
}

func (b *tuiBridge) runScheduledContinuation(turn hostScheduledTurn) {
	if b.rd == nil || b.rd.host == nil || !b.rd.host.scheduledTurnCurrent(turn) {
		return
	}
	if err := b.refreshProjection(); err != nil {
		b.runner.Surface().AddNotice(interactive.NoticeError, "session: cannot reload the active session: "+err.Error())
		return
	}
	turnCtx, cancel := context.WithCancel(b.ctx)
	turnCtx = withHostTurnCancel(turnCtx, cancel)
	handle := &tuiTurnHandle{cancel: cancel}
	b.lifecycle.trackTurn(handle)
	defer b.lifecycle.completeTurn(handle)
	defer b.syncCommandInventory()
	b.runner.SetWorking(true)
	defer b.runner.SetWorking(false)
	scope := ui.NewTurnScope(b.runner.Surface(), b.runner.Active)
	decorator := ui.NewHookDecorator(b.rd.hooks, scope)
	boundary := len(b.history)
	history, err := runContinuation(turnCtx, b.rd, b.loopDeps(scope, decorator), b.history, turn)
	b.history = history
	if reloadErr := b.refreshProjection(); reloadErr != nil {
		b.runner.Surface().AddNotice(interactive.NoticeError, "session: cannot reload the active session: "+reloadErr.Error())
	}
	if turnCtx.Err() != nil {
		scope.FinalizeAuthoritative(false, nil, "aborted", "")
		if !scope.Opened() {
			b.runner.Surface().AddNotice(interactive.NoticeWarning, "interrupted")
		}
		return
	}
	asst, ok := authoritativeSince(history, boundary)
	if !ok {
		if err != nil {
			scope.FinalizeAuthoritative(false, nil, "error", err.Error())
			if !scope.Opened() {
				b.runner.Surface().AddNotice(interactive.NoticeWarning, err.Error())
			}
			return
		}
		scope.FinalizeAuthoritative(false, nil, "", "")
		return
	}
	if usage, _, ok := lastAssistantUsageSince(history, boundary); ok {
		b.runner.Surface().SetUsage(interactive.UsageSummary{
			Input:      usage.Input,
			Output:     usage.Output,
			CacheRead:  usage.CacheRead,
			CacheWrite: usage.CacheWrite,
			Cost:       usage.Cost.Total,
		})
	}
	reason := asst.StopReason
	message := asst.ErrorMessage
	if err != nil && reason == "" {
		reason = "error"
		message = err.Error()
	}
	scope.FinalizeAuthoritative(true, ui.AssistantParts(&agent.Message{Assistant: asst}), reason, message)
	if err != nil {
		b.runner.Surface().AddNotice(interactive.NoticeWarning, err.Error())
	}
}

func (b *tuiBridge) refreshProjection() error {
	if b.sessions == nil {
		return nil
	}
	active, err := b.sessions.Refresh()
	if err != nil {
		b.projectionStale = true
		return err
	}
	b.history = active.history
	b.entryIDs = active.entryIDs
	b.projectionStale = false
	b.syncHostSession()
	return nil
}

func (b *tuiBridge) syncHostSession() {
	if b.rd == nil || b.rd.host == nil {
		return
	}
	b.rd.host.setMessages(b.history)
	b.rd.host.setEntryIDs(b.entryIDs)
}

func (b *tuiBridge) refreshEntryIDs(history []*agent.Message) ([]string, error) {
	if err := b.refreshProjection(); err != nil {
		return nil, err
	}
	if len(b.entryIDs) != len(history) {
		return nil, fmt.Errorf("session: %d entry ids do not align with %d context messages", len(b.entryIDs), len(history))
	}
	return append([]string(nil), b.entryIDs...), nil
}

func (b *tuiBridge) loopDeps(scope *ui.TurnScope, hooks agent.HookDispatcher) *agent.LoopDeps {
	d := b.rd
	d.applyPendingModel()
	var catalog agent.ToolCatalog
	if d.catalog != nil {
		catalog = d.catalog
	}
	var preparer agent.ContextPreparer
	if d.preparer != nil {
		preparer = d.preparer
	}
	deps := &agent.LoopDeps{
		Client:            d.client,
		Tools:             d.tools,
		Catalog:           catalog,
		Recorder:          hostRecorder(d, d.recorder),
		Stdout:            scope.TextWriter(),
		OnThinking:        scope.ThinkingCallback(),
		Preparer:          preparer,
		Hooks:             hooks,
		Retry:             d.retry,
		IsContextOverflow: d.isOverflow,
		Detector:          d.detector,
		RetryPolicy:       d.retryPolicy,
		RetryPolicySet:    d.retryPolicySet,
		SessionEntryIDs:   append([]string(nil), b.entryIDs...),
	}
	if b.sessions != nil {
		deps.RefreshSessionEntryIDs = b.refreshEntryIDs
	}
	if d.host != nil {
		deps.Mailbox = func(ctx context.Context, history []*agent.Message, stopping, external bool) (agent.MailboxResult, error) {
			if stopping && !b.lifecycle.sessionChangePending() {
				for _, text := range b.runner.Surface().Editor().TakeQueued() {
					if err := d.host.sendUserMessage(d.host.snapshot(), ctx, text, sdk.SendOptions{DeliverAs: sdk.DeliveryFollowUp}); err != nil {
						return agent.MailboxResult{}, err
					}
				}
			}
			return d.host.mailboxBoundary(ctx, history, stopping, external)
		}
	}
	return deps
}

func authoritativeSince(history []*agent.Message, boundary int) (*agent.AssistantMessage, bool) {
	if boundary < 0 {
		boundary = 0
	}
	if boundary > len(history) {
		return nil, false
	}
	for i := len(history) - 1; i >= boundary; i-- {
		message := history[i]
		if message == nil || message.Assistant == nil {
			continue
		}
		return message.Assistant, true
	}
	return nil, false
}

func lastAssistantUsageSince(history []*agent.Message, boundary int) (agent.Usage, string, bool) {
	asst, ok := authoritativeSince(history, boundary)
	if !ok {
		return agent.Usage{}, "", false
	}
	return asst.Usage, asst.StopReason, true
}

func lastAssistantUsage(history []*agent.Message) (agent.Usage, string, bool) {
	return lastAssistantUsageSince(history, 0)
}
