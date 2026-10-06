package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/digitalygo/smidja/internal/authstore"
	"github.com/digitalygo/smidja/internal/config"
	"github.com/digitalygo/smidja/internal/content"
	"github.com/digitalygo/smidja/internal/mcp"
	"github.com/digitalygo/smidja/internal/providers/manifest"
	"github.com/digitalygo/smidja/internal/providers/oauth"
	"github.com/digitalygo/smidja/internal/tui"
	"github.com/digitalygo/smidja/internal/ui"
)

var (
	errTUIStartupAborted    = errors.New("cli: interactive startup aborted")
	errStartupLoginCanceled = errors.New("smidja: sign in canceled")
	errStartupLoginEmpty    = errors.New("smidja: sign in returned an empty credential")
)

const (
	startupLoginDeadline  = 5 * time.Minute
	startupLoginJoinGrace = time.Second
)

type tuiStartup struct {
	runner *ui.Runner
}

type tuiStartupOptions struct {
	mode              ui.TUIMode
	theme             config.ThemeSetting
	provider          string
	allowWorkspaceMCP bool
	newTerminal       func(io.Reader, io.Writer) tui.Terminal
}

type tuiStartupPlan struct {
	runner         *tuiStartup
	enabled        bool
	cwd            string
	mcpCfg         *mcp.FileConfig
	workspaceMCP   map[string]bool
	trustWorkspace bool
}

type startupLoginResult struct {
	entry authstore.Entry
	err   error
}

func startTUIStartup(d *Deps, cfg *config.Config, mode ui.TUIMode, theme config.ThemeSetting, cwd string, newTerminal func(io.Reader, io.Writer) tui.Terminal) *tuiStartup {
	runner := ui.NewRunner(ui.RunnerOptions{
		Stdin:         d.Stdin,
		Stdout:        d.Stdout,
		Mode:          mode,
		Title:         "smidja",
		Home:          d.Home(),
		WorkspaceRoot: cfg.WorkspaceRoot,
		ProjectPath:   cwd,
		Theme:         theme,
		ImagesEnabled: true,
		NewTerminal:   newTerminal,
	})
	if err := runner.Start(); err != nil {
		fmt.Fprintf(d.Stderr, "smidja: tui unavailable (%v), using line mode\n", err)
		return nil
	}
	return &tuiStartup{runner: runner}
}

func prepareTUIStartup(ctx context.Context, d *Deps, cfg *config.Config, opts tuiStartupOptions) (*tuiStartupPlan, error) {
	cwd, err := d.Getwd()
	if err != nil {
		return nil, err
	}
	startup := startTUIStartup(d, cfg, opts.mode, opts.theme, cwd, opts.newTerminal)
	if startup == nil {
		return &tuiStartupPlan{trustWorkspace: true}, nil
	}
	plan := &tuiStartupPlan{runner: startup, enabled: true, cwd: cwd, trustWorkspace: true}
	mcpCfg, workspaceMCP, err := loadMCPConfig(d.Home(), cwd)
	if err != nil {
		startup.abort()
		return nil, err
	}
	plan.mcpCfg = mcpCfg
	plan.workspaceMCP = workspaceMCP
	needed := workspaceTrustNeeded(cwd, cfg.WorkspaceRoot, workspaceMCP, opts.allowWorkspaceMCP)
	trusted, err := startup.confirmWorkspaceTrust(ctx, cfg.WorkspaceRoot, needed)
	if err != nil {
		startup.abort()
		return nil, err
	}
	plan.trustWorkspace = trusted
	if err := startup.authenticate(ctx, d, cfg, opts.provider); err != nil {
		startup.abort()
		return nil, err
	}
	return plan, nil
}

func (s *tuiStartup) abort() {
	if s == nil || s.runner == nil {
		return
	}
	s.runner.Stop()
}

func (s *tuiStartup) exited() bool {
	if s == nil || s.runner == nil {
		return true
	}
	select {
	case <-s.runner.Done():
		return true
	default:
		return false
	}
}

func resolveTUIMode(flagValue, configured string) (ui.TUIMode, error) {
	value := flagValue
	if strings.TrimSpace(value) == "" {
		value = configured
	}
	return ui.ParseTUIMode(value)
}

func resolveThemeSetting(flagValue, configured string) (config.ThemeSetting, error) {
	value := flagValue
	if strings.TrimSpace(value) == "" {
		value = configured
	}
	return config.ParseThemeSetting(value)
}

func workspaceTrustNeeded(cwd, workspaceRoot string, workspaceMCP map[string]bool, allowWorkspaceMCP bool) bool {
	if allowWorkspaceMCP && len(workspaceMCP) > 0 {
		return true
	}
	if content.HasProjectInstructions(cwd, workspaceRoot) {
		return true
	}
	return content.HasWorkspaceContent(workspaceRoot)
}

func (s *tuiStartup) confirmWorkspaceTrust(ctx context.Context, workspace string, needed bool) (bool, error) {
	if !needed {
		return true, nil
	}
	accepted, err := s.runner.ConfirmTrust(ctx, workspace)
	if err == nil {
		return accepted, nil
	}
	if s.exited() {
		return false, errTUIStartupAborted
	}
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	return false, err
}

func (s *tuiStartup) authenticate(ctx context.Context, d *Deps, cfg *config.Config, providerFlag string) error {
	if strings.TrimSpace(providerFlag) == "" && d.Client != nil {
		return nil
	}
	p, needed, err := interactiveLoginProvider(d, cfg, providerFlag)
	if err != nil || !needed {
		return err
	}
	return s.runLogin(ctx, d, p)
}

func interactiveLoginProvider(d *Deps, cfg *config.Config, providerFlag string) (oauthProvider, bool, error) {
	name := strings.TrimSpace(providerFlag)
	if name == "" {
		name = strings.TrimSpace(cfg.Provider)
	}
	if name == "" || name == openrouterProviderName {
		return oauthProvider{}, false, nil
	}
	p, ok := oauthProviderByID(name)
	if !ok {
		return oauthProvider{}, false, nil
	}
	store, err := loadAuthStore(d)
	if err != nil {
		return p, false, err
	}
	if storeHasAccess(store, p.id) {
		return p, false, nil
	}
	if spec, ok := manifest.Lookup(name); ok {
		if d.Env != nil && d.Env(spec.EnvVar) != "" {
			return p, false, nil
		}
		if storeHasKey(store, spec.ID) {
			return p, false, nil
		}
	}
	return p, true, nil
}

func (s *tuiStartup) runLogin(ctx context.Context, d *Deps, p oauthProvider) error {
	op, err := s.runner.StartLogin(ctx, ui.LoginRequest{
		Provider: p.name,
		Title:    "Sign in to " + p.name,
		Deadline: time.Now().Add(startupLoginDeadline),
	})
	if err != nil {
		if s.exited() {
			return errTUIStartupAborted
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	options := tuiLoginOptions(d, p, op)
	results := make(chan startupLoginResult, 1)
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		entry, loginErr := p.login(op.Context(), options)
		results <- startupLoginResult{entry: entry, err: loginErr}
	}()
	select {
	case res := <-results:
		return s.finishLogin(ctx, d, p, op, res)
	case <-op.Done():
		joinLoginWorker(workerDone, startupLoginJoinGrace)
		return s.loginAborted(ctx, op)
	case <-ctx.Done():
		op.Cancel()
		joinLoginWorker(workerDone, startupLoginJoinGrace)
		return ctx.Err()
	case <-s.runner.Done():
		op.Cancel()
		joinLoginWorker(workerDone, startupLoginJoinGrace)
		return errTUIStartupAborted
	}
}

func joinLoginWorker(workerDone <-chan struct{}, grace time.Duration) bool {
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-workerDone:
		return true
	case <-timer.C:
		return false
	}
}

func (s *tuiStartup) loginAborted(ctx context.Context, op *ui.LoginOperation) error {
	if s.exited() {
		return errTUIStartupAborted
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	switch op.State() {
	case ui.LoginFailed, ui.LoginTimedOut:
		if err := op.Err(); err != nil {
			return err
		}
	}
	return errStartupLoginCanceled
}

func (s *tuiStartup) finishLogin(ctx context.Context, d *Deps, p oauthProvider, op *ui.LoginOperation, res startupLoginResult) error {
	if res.err != nil {
		op.Fail(res.err)
		return s.loginAborted(ctx, op)
	}
	if !loginInProgress(op.State()) {
		return s.loginAborted(ctx, op)
	}
	if res.entry.Access == "" && res.entry.Key == "" {
		op.Fail(errStartupLoginEmpty)
		return s.loginAborted(ctx, op)
	}
	err := op.Commit(func() error {
		store, err := loadAuthStore(d)
		if err != nil {
			return err
		}
		return store.Set(p.id, res.entry)
	})
	if errors.Is(err, ui.ErrLoginSettled) {
		return s.loginAborted(ctx, op)
	}
	return err
}

func loginInProgress(state ui.LoginState) bool {
	switch state {
	case ui.LoginStarting, ui.LoginAwaiting, ui.LoginManual:
		return true
	default:
		return false
	}
}

func tuiLoginOptions(d *Deps, p oauthProvider, op *ui.LoginOperation) oauth.Options {
	manual := func(ctx context.Context, prompt string) (string, error) {
		return op.RequestManualCode(ctx)
	}
	if d.AuthOptions != nil {
		options := d.AuthOptions(p.id)
		base := options.DeviceCode
		options.DeviceCode = func(device oauth.DeviceCode) {
			if base != nil {
				base(device)
			}
			publishDeviceCode(op, device)
		}
		if options.ManualCode == nil {
			options.ManualCode = manual
		}
		return options
	}
	return oauth.Options{
		OpenBrowser: openBrowserURL,
		ManualCode:  manual,
		DeviceCode: func(device oauth.DeviceCode) {
			publishDeviceCode(op, device)
		},
	}
}

func publishDeviceCode(op *ui.LoginOperation, device oauth.DeviceCode) {
	op.Update(ui.LoginUpdate{
		VerificationURL: device.VerificationURI,
		UserCode:        device.UserCode,
		Status:          "waiting for authorization",
	})
}
