package ui

import (
	"context"
	"errors"
	"sync"

	"github.com/digitalygo/smidja/internal/tui"
	"github.com/digitalygo/smidja/internal/tui/interactive"
	"github.com/digitalygo/smidja/sdk"
)

var (
	errDialogsClosed = errors.New("ui: dialogs are closed")
	errNilDialog     = errors.New("ui: dialog component is nil")
)

type DialogResult struct {
	Value string
	OK    bool
}

type modalWaiter struct {
	ready    chan struct{}
	admitted bool
	canceled bool
}

type sessionInstallState int

const (
	sessionIdle sessionInstallState = iota
	sessionPublishing
	sessionAttached
)

type modalSession struct {
	svc          *modalService
	handle       tui.OverlayHandle
	component    tui.Component
	installState sessionInstallState
	released     bool
}

type publishOutcome int

const (
	publishInstalled publishOutcome = iota
	publishReleased
	publishClosed
)

func (s *modalSession) release() {
	s.svc.releaseSession(s)
}

type modalService struct {
	view screen

	done      chan struct{}
	closeOnce sync.Once

	mu       sync.Mutex
	closed   bool
	owner    bool
	waiting  []*modalWaiter
	active   tui.Component
	sessions int
	zero     chan struct{}

	installHook func()
	publishHook func()
}

func newModalService(view screen) *modalService {
	return &modalService{
		view: view,
		done: make(chan struct{}),
		zero: make(chan struct{}),
	}
}

func (m *modalService) shutdown() {
	m.closeOnce.Do(func() {
		m.mu.Lock()
		m.closed = true
		close(m.done)
		m.syncCaptureLocked()
		m.mu.Unlock()
	})
	m.waitDrained()
}

func (m *modalService) waitDrained() {
	m.mu.Lock()
	for m.sessions != 0 {
		zero := m.zero
		m.mu.Unlock()
		<-zero
		m.mu.Lock()
	}
	m.mu.Unlock()
}

func (m *modalService) beginSessionLocked() {
	if m.sessions == 0 {
		m.zero = make(chan struct{})
	}
	m.sessions++
}

func (m *modalService) endSessionLocked() {
	m.sessions--
	if m.sessions == 0 {
		close(m.zero)
	}
}

func (m *modalService) syncCaptureLocked() {
	enabled := m.owner || (!m.closed && len(m.waiting) > 0)
	m.view.SetModalCapture(enabled)
}

func (m *modalService) acquire(ctx context.Context) (*modalSession, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, errDialogsClosed
	}
	if !m.owner {
		m.owner = true
		m.beginSessionLocked()
		m.syncCaptureLocked()
		m.mu.Unlock()
		return &modalSession{svc: m}, nil
	}
	waiter := &modalWaiter{ready: make(chan struct{})}
	m.waiting = append(m.waiting, waiter)
	m.syncCaptureLocked()
	m.mu.Unlock()

	select {
	case <-waiter.ready:
		session := &modalSession{svc: m}
		m.mu.Lock()
		closed := m.closed
		m.mu.Unlock()
		if closed {
			session.release()
			return nil, errDialogsClosed
		}
		return session, nil
	case <-ctx.Done():
		if m.abandon(waiter) {
			session := &modalSession{svc: m}
			session.release()
		}
		return nil, ctx.Err()
	case <-m.done:
		if m.abandon(waiter) {
			session := &modalSession{svc: m}
			session.release()
		}
		return nil, errDialogsClosed
	}
}

func (m *modalService) abandon(waiter *modalWaiter) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if waiter.admitted {
		return true
	}
	if waiter.canceled {
		return false
	}
	waiter.canceled = true
	for index, candidate := range m.waiting {
		if candidate == waiter {
			m.waiting = append(m.waiting[:index], m.waiting[index+1:]...)
			break
		}
	}
	m.syncCaptureLocked()
	return false
}

func (m *modalService) releaseSession(session *modalSession) {
	m.mu.Lock()
	if session.released {
		m.mu.Unlock()
		return
	}
	session.released = true
	if session.installState == sessionPublishing {
		m.mu.Unlock()
		return
	}
	m.finishReleaseLocked(session)
}

func (m *modalService) finishReleaseLocked(session *modalSession) {
	handle := session.handle
	component := session.component
	session.handle = nil
	session.component = nil
	session.installState = sessionIdle
	if component != nil && m.active == component {
		m.active = nil
	}
	if handle != nil {
		handle.Hide()
	}
	if !m.closed && len(m.waiting) > 0 {
		next := m.waiting[0]
		m.waiting = m.waiting[1:]
		next.admitted = true
		m.owner = true
		close(next.ready)
	} else {
		m.owner = false
		m.endSessionLocked()
	}
	m.syncCaptureLocked()
	m.mu.Unlock()
	m.view.RequestRender(true)
}

func (m *modalService) Active() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active != nil
}

func (m *modalService) overlayFocused() bool {
	m.mu.Lock()
	active := m.active
	m.mu.Unlock()
	if active == nil {
		return false
	}
	return m.view.HasOverlay() && m.view.FocusedComponent() == active
}

func (m *modalService) setInstallHook(hook func()) {
	m.mu.Lock()
	m.installHook = hook
	m.mu.Unlock()
}

func (m *modalService) setPublishHook(hook func()) {
	m.mu.Lock()
	m.publishHook = hook
	m.mu.Unlock()
}

func (m *modalService) waitingCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.waiting)
}

func (m *modalService) isClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

func (m *modalService) show(session *modalSession, component tui.Component) (tui.OverlayHandle, publishOutcome) {
	m.mu.Lock()
	if session.released {
		m.mu.Unlock()
		return nil, publishReleased
	}
	if m.closed {
		m.mu.Unlock()
		return nil, publishClosed
	}
	m.active = component
	session.installState = sessionPublishing
	hook := m.installHook
	m.mu.Unlock()

	if hook != nil {
		hook()
	}

	handle := m.view.ShowOverlay(component, tui.OverlayOptions{
		Anchor:    tui.AnchorCenter,
		Width:     "70%",
		MinWidth:  24,
		MaxHeight: "80%",
	})

	m.mu.Lock()
	publishHook := m.publishHook
	m.mu.Unlock()
	if publishHook != nil {
		publishHook()
	}

	m.mu.Lock()
	if session.released || m.closed {
		handle.Hide()
		if m.active == component {
			m.active = nil
		}
		session.installState = sessionIdle
		if session.released {
			m.finishReleaseLocked(session)
			return nil, publishReleased
		}
		m.mu.Unlock()
		return nil, publishClosed
	}
	session.handle = handle
	session.component = component
	session.installState = sessionAttached
	m.mu.Unlock()
	return handle, publishInstalled
}

func (m *modalService) run(ctx context.Context, build func(emit func(DialogResult)) tui.Component) (DialogResult, error) {
	session, err := m.acquire(ctx)
	if err != nil {
		return DialogResult{}, err
	}
	defer session.release()

	var once sync.Once
	result := make(chan DialogResult, 1)
	emit := func(value DialogResult) {
		once.Do(func() {
			result <- value
			m.view.RequestRender(false)
		})
	}
	component := build(emit)
	if component == nil {
		return DialogResult{}, errNilDialog
	}
	if _, outcome := m.show(session, component); outcome != publishInstalled {
		return DialogResult{}, errDialogsClosed
	}

	var outcome DialogResult
	var runErr error
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case outcome = <-result:
	case <-ctx.Done():
		runErr = ctx.Err()
	case <-m.done:
		runErr = errDialogsClosed
	}
	once.Do(func() {})
	return outcome, runErr
}

func (m *modalService) SetTheme(theme *tui.Theme) {
	dialogTheme := interactive.NewDialogTheme(theme)
	m.mu.Lock()
	active := m.active
	m.mu.Unlock()
	if active != nil {
		if setter, ok := active.(interface {
			SetTheme(interactive.DialogTheme)
		}); ok {
			setter.SetTheme(dialogTheme)
		}
		active.Invalidate()
	}
	m.view.RequestRender(false)
}

func (r *Runner) dialogContext(ctx context.Context) context.Context {
	if ctx != nil {
		return ctx
	}
	if r.lifecycleCtx != nil {
		return r.lifecycleCtx
	}
	return context.Background()
}

func (r *Runner) confirm(ctx context.Context, title, message string) (bool, error) {
	if !r.Active() {
		return false, sdk.ErrModeUnsupported
	}
	title = interactive.SanitizeSingleLine(title)
	if message != "" {
		message = interactive.SanitizeDisplayText(message)
	}
	outcome, err := r.dialogs.run(r.dialogContext(ctx), func(emit func(DialogResult)) tui.Component {
		return interactive.NewConfirmDialog(title, message, interactive.NewDialogTheme(r.surface.Theme()), func(ok bool) {
			emit(DialogResult{OK: ok})
		})
	})
	return outcome.OK, err
}

func (r *Runner) selectValue(ctx context.Context, options interactive.SelectDialogOptions) (string, bool, error) {
	if !r.Active() {
		return "", false, sdk.ErrModeUnsupported
	}
	outcome, err := r.dialogs.run(r.dialogContext(ctx), func(emit func(DialogResult)) tui.Component {
		return interactive.NewSelectDialog(options, interactive.NewDialogTheme(r.surface.Theme()),
			func(value string) { emit(DialogResult{Value: value, OK: true}) },
			func() { emit(DialogResult{}) })
	})
	if err != nil {
		return "", false, err
	}
	return outcome.Value, outcome.OK, nil
}

func (r *Runner) selectStrings(ctx context.Context, title string, options []string) (string, error) {
	if len(options) == 0 && r.Active() && !r.dialogs.isClosed() {
		return "", nil
	}
	items := make([]tui.SelectItem, 0, len(options))
	for _, option := range options {
		items = append(items, tui.SelectItem{Value: option, Label: option})
	}
	value, accepted, err := r.selectValue(ctx, interactive.SelectDialogOptions{
		Title:      title,
		Items:      items,
		Searchable: true,
	})
	if err != nil {
		return "", err
	}
	if !accepted {
		return "", nil
	}
	return value, nil
}

func (r *Runner) Confirm(title, message string) (bool, error) {
	return r.confirm(nil, title, message)
}

func (r *Runner) Select(title string, options []string) (string, error) {
	return r.selectStrings(nil, title, options)
}

func (r *Runner) Input(title, placeholder string) (string, error) {
	return r.input(nil, title, placeholder)
}

func (r *Runner) input(ctx context.Context, title, placeholder string) (string, error) {
	if !r.Active() {
		return "", sdk.ErrModeUnsupported
	}
	outcome, err := r.dialogs.run(r.dialogContext(ctx), func(emit func(DialogResult)) tui.Component {
		return interactive.NewInputDialog(title, placeholder, interactive.NewDialogTheme(r.surface.Theme()),
			func(value string) { emit(DialogResult{Value: value, OK: true}) },
			func() { emit(DialogResult{}) })
	})
	if err != nil {
		return "", err
	}
	if !outcome.OK {
		return "", nil
	}
	return outcome.Value, nil
}

func (r *Runner) Editor(title, prefill string) (string, error) {
	return r.editor(nil, title, prefill)
}

func (r *Runner) editor(ctx context.Context, title, prefill string) (string, error) {
	if !r.Active() {
		return "", sdk.ErrModeUnsupported
	}
	outcome, err := r.dialogs.run(r.dialogContext(ctx), func(emit func(DialogResult)) tui.Component {
		return interactive.NewEditorDialog(title, prefill, interactive.NewDialogTheme(r.surface.Theme()),
			func(value string) { emit(DialogResult{Value: value, OK: true}) },
			func() { emit(DialogResult{}) })
	})
	if err != nil {
		return "", err
	}
	return outcome.Value, nil
}

func (r *Runner) PromptSecret(ctx context.Context, title string) (string, error) {
	if !r.Active() {
		return "", sdk.ErrModeUnsupported
	}
	outcome, err := r.dialogs.run(r.dialogContext(ctx), func(emit func(DialogResult)) tui.Component {
		return interactive.NewMaskedInput(title, interactive.NewDialogTheme(r.surface.Theme()),
			func(value string) { emit(DialogResult{Value: value, OK: true}) },
			func() { emit(DialogResult{}) })
	})
	if err != nil {
		return "", err
	}
	return outcome.Value, nil
}

type boundUI struct {
	runner *Runner
	signal context.Context
}

var _ sdk.UI = (*boundUI)(nil)

func (r *Runner) BoundUI(signal context.Context) sdk.UI {
	return &boundUI{runner: r, signal: signal}
}

func (u *boundUI) Notify(message string, kind sdk.NotifyKind) { u.runner.Notify(message, kind) }

func (u *boundUI) Confirm(title, message string) (bool, error) {
	return u.runner.confirm(u.signal, title, message)
}

func (u *boundUI) Select(title string, options []string) (string, error) {
	return u.runner.selectStrings(u.signal, title, options)
}

func (u *boundUI) Input(title, placeholder string) (string, error) {
	return u.runner.input(u.signal, title, placeholder)
}

func (u *boundUI) Editor(title, prefill string) (string, error) {
	return u.runner.editor(u.signal, title, prefill)
}

func (u *boundUI) SetStatus(key, text string) { u.runner.SetStatus(key, text) }

func (u *boundUI) SetWidget(key string, content []string) { u.runner.SetWidget(key, content) }

func (u *boundUI) SetWorkingMessage(message string) { u.runner.SetWorkingMessage(message) }

func (u *boundUI) SetTitle(title string) { u.runner.SetTitle(title) }
