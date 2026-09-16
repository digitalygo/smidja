package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/digitalygo/smidja/internal/agent"
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
	mu         sync.Mutex
	queued     *sync.Cond
	stopping   bool
	pending    []func()
	active     *tuiTurnHandle
	workerDone chan struct{}
	onPanic    func()
}

func newTuiLifecycle() *tuiLifecycle {
	l := &tuiLifecycle{workerDone: make(chan struct{})}
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
		l.runJob(job)
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

func (l *tuiLifecycle) beginShutdown() {
	l.mu.Lock()
	l.stopping = true
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
	}
	bridge.lifecycle.onPanic = func() {
		bridge.runner.Surface().AddNotice(interactive.NoticeError, workerPanicNotice)
		bridge.runner.RequestExit()
	}
	return bridge
}

type tuiCommandContext struct {
	sdk.HandlerContext
	bridge *tuiBridge
}

var _ sdk.CommandContext = (*tuiCommandContext)(nil)

func (c *tuiCommandContext) WaitForIdle() error {
	return sdk.ErrModeUnsupported
}

func (c *tuiCommandContext) NewSession(opts sdk.NewSessionOptions) (*sdk.SessionSwitchResult, error) {
	return nil, sdk.ErrModeUnsupported
}

func (c *tuiCommandContext) Fork(entryID string, opts sdk.ForkOptions) (*sdk.SessionSwitchResult, error) {
	return nil, sdk.ErrModeUnsupported
}

func (c *tuiCommandContext) NavigateTree(targetID string, opts sdk.TreeOptions) (*sdk.SessionSwitchResult, error) {
	return nil, sdk.ErrModeUnsupported
}

func (c *tuiCommandContext) SwitchSession(path string, opts sdk.SwitchOptions) (*sdk.SessionSwitchResult, error) {
	return nil, sdk.ErrModeUnsupported
}

func (c *tuiCommandContext) Reload() error {
	return sdk.ErrModeUnsupported
}

func (c *tuiCommandContext) runInput(input string) error {
	c.bridge.runUserTurn(input)
	return nil
}

func runTUI(ctx context.Context, d *Deps, rd *runDeps, lineUI *ui.LineUI, mode ui.TUIMode, workspace, projectPath string, skillOut *switchWriter, newTerminal func(io.Reader, io.Writer) tui.Terminal) error {
	capture := &tuiCaptureWriter{fallback: d.Stdout}
	runner := ui.NewRunner(ui.RunnerOptions{
		Stdin:         d.Stdin,
		Stdout:        d.Stdout,
		Mode:          mode,
		Title:         "smidja",
		Home:          d.Home(),
		WorkspaceRoot: workspace,
		ProjectPath:   projectPath,
		NewTerminal:   newTerminal,
	})
	rdTUI := *rd
	rdTUI.stderr = &tuiNoticeWriter{runner: runner}
	workCtx, cancelWork := context.WithCancel(ctx)
	defer cancelWork()
	bridge := newTuiBridge(workCtx, cancelWork, &rdTUI, runner, capture)
	runner.SetOnSubmit(bridge.submit)
	runner.SetOnInterrupt(bridge.interrupt)
	if err := runner.Start(); err != nil {
		fmt.Fprintf(d.Stderr, "smidja: tui unavailable (%v), using line mode\n", err)
		bridge.shutdown()
		bridge.wait()
		return repl(ctx, lineUI, rd)
	}
	if skillOut != nil {
		skillOut.Set(capture)
	}
	surface := runner.Surface()
	surface.SetModel(rd.model)
	surface.SetWorkspace(workspace)
	surface.SetSessionName(rd.sessionPath)
	var runErr error
	select {
	case <-runner.Done():
	case <-ctx.Done():
		runErr = ctx.Err()
	}
	bridge.shutdown()
	bridge.wait()
	runner.Stop()
	return runErr
}

func (b *tuiBridge) shutdown() {
	b.lifecycle.beginShutdown()
	if b.cancelWork != nil {
		b.cancelWork()
	}
}

func (b *tuiBridge) wait() {
	b.lifecycle.wait()
}

func (b *tuiBridge) submit(input string) {
	b.lifecycle.enqueue(func() {
		b.handle(input)
		if b.afterTurn != nil {
			b.afterTurn()
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
	if input == "/quit" || input == "/exit" {
		b.runner.RequestExit()
		return
	}
	name, args := splitCommandInput(input)
	if name == "help" {
		var help bytes.Buffer
		printCommandHelp(&help, b.rd.commands)
		if text := strings.TrimSpace(help.String()); text != "" {
			b.runner.Surface().AddNotice(interactive.NoticeInfo, text)
		}
		return
	}
	cmd, ok := b.rd.commands.Get(name)
	if !ok {
		b.runner.Surface().AddNotice(interactive.NoticeWarning, "unknown command /"+name)
		return
	}
	hctx := &tuiCommandContext{HandlerContext: b.rd.handlerContext(b.ctx), bridge: b}
	b.capture.Begin()
	err := cmd.Handler(hctx, args)
	captured := b.capture.End()
	if captured != "" {
		b.runner.Surface().AddNotice(interactive.NoticeInfo, captured)
	}
	if err != nil {
		b.runner.Surface().AddNotice(interactive.NoticeWarning, "/"+name+": "+err.Error())
	}
}

func (b *tuiBridge) runUserTurn(input string) {
	surface := b.runner.Surface()
	surface.AddUserMessage(input)
	turnCtx, cancel := context.WithCancel(b.ctx)
	handle := &tuiTurnHandle{cancel: cancel}
	b.lifecycle.trackTurn(handle)
	defer b.lifecycle.completeTurn(handle)
	b.runner.SetWorking(true)
	defer b.runner.SetWorking(false)
	scope := ui.NewTurnScope(surface, b.runner.Active)
	decorator := ui.NewHookDecorator(b.rd.hooks, scope)
	boundary := len(b.history)
	history, err := runTurn(turnCtx, b.rd, b.loopDeps(scope, decorator), b.history, input)
	b.history = history
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

func (b *tuiBridge) loopDeps(scope *ui.TurnScope, hooks agent.HookDispatcher) *agent.LoopDeps {
	d := b.rd
	var catalog agent.ToolCatalog
	if d.catalog != nil {
		catalog = d.catalog
	}
	var preparer agent.ContextPreparer
	if d.preparer != nil {
		preparer = d.preparer
	}
	return &agent.LoopDeps{
		Client:            d.client,
		Tools:             d.tools,
		Catalog:           catalog,
		Recorder:          d.recorder,
		Stdout:            scope.TextWriter(),
		OnThinking:        scope.ThinkingCallback(),
		Preparer:          preparer,
		Hooks:             hooks,
		Retry:             d.retry,
		IsContextOverflow: d.isOverflow,
		Detector:          d.detector,
		RetryPolicy:       d.retryPolicy,
		RetryPolicySet:    d.retryPolicySet,
	}
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
