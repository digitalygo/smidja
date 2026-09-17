package ui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/digitalygo/smidja/internal/tui/interactive"
	"github.com/digitalygo/smidja/sdk"
)

type LoginState string

const (
	LoginStarting  LoginState = "starting"
	LoginAwaiting  LoginState = "awaiting authorization"
	LoginManual    LoginState = "awaiting manual code"
	LoginSucceeded LoginState = "signed in"
	LoginFailed    LoginState = "failed"
	LoginCanceled  LoginState = "canceled"
	LoginTimedOut  LoginState = "timed out"
)

var (
	errLoginSettled   = errors.New("ui: login already settled")
	errLoginCanceled  = errors.New("ui: login canceled")
	errManualCanceled = errors.New("ui: manual code entry canceled")
)

type LoginRequest struct {
	Provider        string
	Title           string
	VerificationURL string
	UserCode        string
	Status          string
	Deadline        time.Time
	ContinueOnEnter bool
}

type LoginUpdate struct {
	VerificationURL string
	UserCode        string
	Status          string
	Deadline        time.Time
}

type LoginResult struct {
	State LoginState
	Err   error
}

type manualResult struct {
	code string
	err  error
}

type manualPrompt struct {
	result chan manualResult
	once   sync.Once
}

func newManualPrompt() *manualPrompt {
	return &manualPrompt{result: make(chan manualResult, 1)}
}

func (p *manualPrompt) resolve(code string, err error) {
	p.once.Do(func() { p.result <- manualResult{code: code, err: err} })
}

type LoginOperation struct {
	runner  *Runner
	session *modalSession
	dialog  *interactive.LoginDialog
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}

	settleOnce sync.Once

	mu               sync.Mutex
	state            LoginState
	err              error
	view             interactive.LoginDialogState
	deadline         time.Time
	timer            *time.Timer
	prompt           *manualPrompt
	promptGeneration uint64
	manualInstall    func(generation uint64, installed bool)
	settled          bool
}

func (r *Runner) StartLogin(ctx context.Context, req LoginRequest) (*LoginOperation, error) {
	if !r.Active() {
		return nil, sdk.ErrModeUnsupported
	}
	flowCtx, flowCancel := context.WithCancel(r.dialogContext(ctx))
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = "Sign in"
	}
	op := &LoginOperation{
		runner: r,
		ctx:    flowCtx,
		cancel: flowCancel,
		done:   make(chan struct{}),
		state:  LoginStarting,
		view: interactive.LoginDialogState{
			Provider:        req.Provider,
			State:           string(LoginStarting),
			Status:          req.Status,
			VerificationURL: req.VerificationURL,
			UserCode:        req.UserCode,
			Deadline:        req.Deadline,
		},
	}
	op.dialog = interactive.NewLoginDialog(title, interactive.NewDialogTheme(r.surface.Theme()), func() { op.Cancel() })
	op.dialog.SetState(op.view)
	if req.ContinueOnEnter {
		op.dialog.SetContinue(func() { op.Succeed() })
	}

	session, err := r.dialogs.acquire(flowCtx)
	if err != nil {
		flowCancel()
		return nil, err
	}
	op.session = session
	if hook := r.loginStartHook; hook != nil {
		hook(op)
	}
	switch _, outcome := r.dialogs.show(session, op.dialog); outcome {
	case publishInstalled:
	case publishReleased:
		return op, nil
	default:
		session.release()
		flowCancel()
		return nil, errDialogsClosed
	}
	op.armDeadline(req.Deadline)
	go op.watch()
	return op, nil
}

func (op *LoginOperation) Context() context.Context { return op.ctx }

func (op *LoginOperation) Done() <-chan struct{} { return op.done }

func (op *LoginOperation) State() LoginState {
	op.mu.Lock()
	defer op.mu.Unlock()
	return op.state
}

func (op *LoginOperation) Err() error {
	op.mu.Lock()
	defer op.mu.Unlock()
	return op.err
}

func (op *LoginOperation) Wait() LoginResult {
	<-op.done
	op.mu.Lock()
	defer op.mu.Unlock()
	return LoginResult{State: op.state, Err: op.err}
}

func (op *LoginOperation) Update(update LoginUpdate) {
	op.mu.Lock()
	if op.settled {
		op.mu.Unlock()
		return
	}
	if update.VerificationURL != "" {
		op.view.VerificationURL = update.VerificationURL
	}
	if update.UserCode != "" {
		op.view.UserCode = update.UserCode
	}
	if update.Status != "" {
		op.view.Status = update.Status
	}
	if !update.Deadline.IsZero() {
		op.view.Deadline = update.Deadline
	}
	if op.prompt == nil {
		op.state = LoginAwaiting
		op.view.State = string(LoginAwaiting)
	}
	view := op.view
	op.mu.Unlock()

	op.dialog.SetState(view)
	op.runner.RequestRender(false)
	if !update.Deadline.IsZero() {
		op.armDeadline(update.Deadline)
	}
}

func (op *LoginOperation) setManualInstallHook(hook func(generation uint64, installed bool)) {
	op.mu.Lock()
	op.manualInstall = hook
	op.mu.Unlock()
}

func (op *LoginOperation) manualInstallHook() func(uint64, bool) {
	op.mu.Lock()
	defer op.mu.Unlock()
	return op.manualInstall
}

func (op *LoginOperation) RequestManualCode(ctx context.Context) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	prompt := newManualPrompt()
	op.mu.Lock()
	if op.settled {
		err := op.err
		op.mu.Unlock()
		if err != nil {
			return "", err
		}
		return "", errLoginSettled
	}
	op.promptGeneration++
	generation := op.promptGeneration
	previous := op.prompt
	op.prompt = prompt
	op.state = LoginManual
	op.view.State = string(LoginManual)
	provider := op.view.Provider
	op.mu.Unlock()

	if previous != nil {
		previous.resolve("", errManualCanceled)
	}
	if hook := op.manualInstallHook(); hook != nil {
		hook(generation, false)
	}
	if op.dialog.PromptManual(generation, loginManualTitle(provider), func(code string) {
		op.deliverManual(generation, prompt, code)
	}, func() {
		op.cancelManual(generation, prompt)
	}) {
		if hook := op.manualInstallHook(); hook != nil {
			hook(generation, true)
		}
	}
	op.runner.RequestRender(false)

	select {
	case res := <-prompt.result:
		op.clearPrompt(prompt)
		if res.err != nil {
			return "", res.err
		}
		return res.code, nil
	case <-ctx.Done():
		op.clearPrompt(prompt)
		if view, cleared := op.releaseManualDisplay(generation, prompt); cleared {
			op.dialog.ClearManual(generation)
			op.dialog.SetState(view)
			op.runner.RequestRender(false)
		}
		return "", ctx.Err()
	case <-op.done:
		op.clearPrompt(prompt)
		if err := op.Err(); err != nil {
			return "", err
		}
		return "", errLoginSettled
	}
}

func (op *LoginOperation) Succeed() { op.settle(LoginSucceeded, nil) }

func (op *LoginOperation) Fail(err error) { op.settle(LoginFailed, err) }

func (op *LoginOperation) Cancel() { op.settle(LoginCanceled, errLoginCanceled) }

func (op *LoginOperation) watch() {
	select {
	case <-op.ctx.Done():
		op.settle(loginStateForError(op.ctx.Err()), loginError(op.ctx.Err()))
	case <-op.runner.dialogs.done:
		op.settle(LoginCanceled, errDialogsClosed)
	}
}

func (op *LoginOperation) deliverManual(generation uint64, prompt *manualPrompt, code string) {
	op.mu.Lock()
	if op.settled || op.prompt != prompt || op.promptGeneration != generation {
		op.mu.Unlock()
		return
	}
	op.prompt = nil
	op.state = LoginAwaiting
	op.view.State = string(LoginAwaiting)
	view := op.view
	op.mu.Unlock()

	op.dialog.SetState(view)
	op.runner.RequestRender(false)
	prompt.resolve(code, nil)
}

func (op *LoginOperation) cancelManual(generation uint64, prompt *manualPrompt) {
	op.mu.Lock()
	stale := op.settled || op.prompt != prompt || op.promptGeneration != generation
	op.mu.Unlock()
	if stale {
		return
	}
	op.Cancel()
}

func (op *LoginOperation) clearPrompt(prompt *manualPrompt) {
	op.mu.Lock()
	if op.prompt == prompt {
		op.prompt = nil
	}
	op.mu.Unlock()
}

func (op *LoginOperation) releaseManualDisplay(generation uint64, prompt *manualPrompt) (interactive.LoginDialogState, bool) {
	op.mu.Lock()
	defer op.mu.Unlock()
	if op.settled || op.prompt != nil || op.promptGeneration != generation {
		return interactive.LoginDialogState{}, false
	}
	op.state = LoginAwaiting
	op.view.State = string(LoginAwaiting)
	return op.view, true
}

func (op *LoginOperation) armDeadline(deadline time.Time) {
	op.mu.Lock()
	defer op.mu.Unlock()
	if op.settled {
		return
	}
	if op.timer != nil {
		op.timer.Stop()
		op.timer = nil
	}
	op.deadline = deadline
	if deadline.IsZero() {
		return
	}
	delay := time.Until(deadline)
	if delay < 0 {
		delay = 0
	}
	op.timer = time.AfterFunc(delay, func() { op.settle(LoginTimedOut, context.DeadlineExceeded) })
}

func (op *LoginOperation) settle(state LoginState, err error) {
	op.settleOnce.Do(func() {
		op.mu.Lock()
		op.settled = true
		op.state = state
		op.err = err
		if op.timer != nil {
			op.timer.Stop()
			op.timer = nil
		}
		prompt := op.prompt
		op.prompt = nil
		if state == LoginFailed && err != nil {
			op.view.Status = err.Error()
		}
		op.view.State = string(state)
		view := op.view
		op.mu.Unlock()

		if prompt != nil {
			prompt.resolve("", loginPromptError(err))
		}
		if op.dialog != nil {
			op.dialog.SetState(view)
			op.dialog.Settle()
		}
		if op.session != nil {
			op.session.release()
		}
		op.cancel()
		close(op.done)
	})
}

func loginManualTitle(provider string) string {
	trimmed := strings.TrimSpace(provider)
	if trimmed == "" {
		return "Paste the authorization code"
	}
	return "Sign in to " + trimmed + ": paste the authorization code"
}

func loginPromptError(err error) error {
	if err != nil {
		return err
	}
	return errManualCanceled
}

func loginStateForError(err error) LoginState {
	if errors.Is(err, context.DeadlineExceeded) {
		return LoginTimedOut
	}
	return LoginCanceled
}

func loginError(err error) error {
	if err == nil {
		return context.Canceled
	}
	return err
}
