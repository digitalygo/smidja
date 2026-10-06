package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/authstore"
	"github.com/digitalygo/smidja/internal/config"
	"github.com/digitalygo/smidja/internal/ui"
)

func newStartupFixture(t *testing.T) (*tuiStartup, *fakeBridgeTerminal) {
	t.Helper()
	terminal := newFakeBridgeTerminal()
	runner := ui.NewRunner(ui.RunnerOptions{
		Stdin:       strings.NewReader(""),
		Stdout:      io.Discard,
		Home:        t.TempDir(),
		Mode:        ui.TUIModeRegular,
		NewTerminal: bridgeTerminalFactory(terminal),
	})
	if err := runner.Start(); err != nil {
		t.Fatalf("runner.Start: %v", err)
	}
	t.Cleanup(runner.Stop)
	return &tuiStartup{runner: runner}, terminal
}

func waitForTerminalOutput(t *testing.T, terminal *fakeBridgeTerminal, fragment string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		output := terminal.Output()
		if strings.Contains(output, fragment) {
			return output
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %q, output:\n%s", fragment, output)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestStartTUIStartupOwnsTerminalOnce(t *testing.T) {
	terminal := newFakeBridgeTerminal()
	home := t.TempDir()
	d := &Deps{Env: envFrom(nil), Home: func() string { return home }, Stderr: io.Discard}
	cfg := &config.Config{WorkspaceRoot: t.TempDir()}
	startup := startTUIStartup(d, cfg, ui.TUIModeRegular, config.ThemeSetting{}, t.TempDir(), bridgeTerminalFactory(terminal))
	if startup == nil {
		t.Fatal("startTUIStartup must own the terminal")
	}
	if !startup.runner.ImagesEnabled() {
		t.Fatal("the actual TUI must enable inline images by default")
	}
	if !terminal.Started() {
		t.Fatal("terminal not started")
	}
	if got := terminal.StartCount(); got != 1 {
		t.Fatalf("terminal start count = %d, want 1", got)
	}
	startup.runner.SetImagesEnabled(false)
	if startup.runner.ImagesEnabled() {
		t.Fatal("the runtime disable selector must win over the default")
	}
	startup.abort()
	if got := terminal.StopCount(); got != 1 {
		t.Fatalf("terminal stop count = %d, want 1", got)
	}
	startup.abort()
	if got := terminal.StopCount(); got != 1 {
		t.Fatalf("abort must be idempotent, stop count = %d", got)
	}
}

func TestStartTUIStartupFallsBackWhenStartFails(t *testing.T) {
	terminal := newFakeBridgeTerminal()
	terminal.startErr = errors.New("stdin is not a terminal")
	var stderr bytes.Buffer
	home := t.TempDir()
	d := &Deps{Env: envFrom(nil), Home: func() string { return home }, Stderr: &stderr}
	cfg := &config.Config{WorkspaceRoot: t.TempDir()}
	startup := startTUIStartup(d, cfg, ui.TUIModeRegular, config.ThemeSetting{}, t.TempDir(), bridgeTerminalFactory(terminal))
	if startup != nil {
		t.Fatal("a failed terminal start must fall back to line mode")
	}
	if !strings.Contains(stderr.String(), "tui unavailable") {
		t.Fatalf("stderr = %q, want the fallback notice", stderr.String())
	}
	if terminal.Started() {
		t.Fatal("a failed terminal start must leave the terminal stopped")
	}
}

func TestStartupTrustDecisionAcceptedAndRefused(t *testing.T) {
	for _, accepted := range []bool{true, false} {
		name := "refused"
		if accepted {
			name = "accepted"
		}
		t.Run(name, func(t *testing.T) {
			startup, terminal := newStartupFixture(t)
			done := make(chan bool, 1)
			errs := make(chan error, 1)
			go func() {
				ok, err := startup.confirmWorkspaceTrust(context.Background(), "/work/project", true)
				done <- ok
				errs <- err
			}()
			waitForTerminalOutput(t, terminal, "Trust this workspace?", 3*time.Second)
			output := waitForTerminalOutput(t, terminal, "/work/project", time.Second)
			if strings.Contains(output, "workspace mcp") {
				t.Fatalf("trust prompt must not imply workspace MCP opt-in:\n%s", output)
			}
			if accepted {
				terminal.SendInput("y")
			} else {
				terminal.SendInput("n")
			}
			select {
			case ok := <-done:
				if ok != accepted {
					t.Fatalf("confirmed = %v, want %v", ok, accepted)
				}
				if err := <-errs; err != nil {
					t.Fatalf("confirm error = %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("trust dialog did not resolve")
			}
		})
	}
}

func TestStartupWorkspaceTrustNotNeededSkipsPrompt(t *testing.T) {
	startup, terminal := newStartupFixture(t)
	ok, err := startup.confirmWorkspaceTrust(context.Background(), "/work/project", false)
	if err != nil || !ok {
		t.Fatalf("confirmWorkspaceTrust = %v, %v, want true and nil", ok, err)
	}
	if strings.Contains(terminal.Output(), "Trust this workspace?") {
		t.Fatal("an irrelevant workspace must not prompt for trust")
	}
}

func TestStartupTrustCanceledByEOF(t *testing.T) {
	startup, terminal := newStartupFixture(t)
	errs := make(chan error, 1)
	go func() {
		_, err := startup.confirmWorkspaceTrust(context.Background(), "/work/project", true)
		errs <- err
	}()
	waitForTerminalOutput(t, terminal, "Trust this workspace?", 3*time.Second)
	terminal.FireEOF()
	select {
	case err := <-errs:
		if !errors.Is(err, errTUIStartupAborted) {
			t.Fatalf("trust error after EOF = %v, want errTUIStartupAborted", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("trust dialog did not resolve after EOF")
	}
}

func TestStartupTrustCanceledBySignal(t *testing.T) {
	startup, terminal := newStartupFixture(t)
	errs := make(chan error, 1)
	go func() {
		_, err := startup.confirmWorkspaceTrust(context.Background(), "/work/project", true)
		errs <- err
	}()
	waitForTerminalOutput(t, terminal, "Trust this workspace?", 3*time.Second)
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("kill: %v", err)
	}
	select {
	case err := <-errs:
		if !errors.Is(err, errTUIStartupAborted) {
			t.Fatalf("trust error after signal = %v, want errTUIStartupAborted", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("trust dialog did not resolve after the signal")
	}
}

func TestStartupTrustCanceledByContext(t *testing.T) {
	startup, terminal := newStartupFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	errs := make(chan error, 1)
	go func() {
		_, err := startup.confirmWorkspaceTrust(ctx, "/work/project", true)
		errs <- err
	}()
	waitForTerminalOutput(t, terminal, "Trust this workspace?", 3*time.Second)
	cancel()
	select {
	case err := <-errs:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("trust error after cancel = %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("trust dialog did not resolve after cancellation")
	}
}

func TestWorkspaceTrustNeeded(t *testing.T) {
	empty := t.TempDir()
	if workspaceTrustNeeded(empty, empty, nil, false) {
		t.Fatal("an empty workspace must not need trust")
	}
	if workspaceTrustNeeded(empty, empty, map[string]bool{"server": true}, false) {
		t.Fatal("workspace MCP without the flag must not need trust")
	}
	if !workspaceTrustNeeded(empty, empty, map[string]bool{"server": true}, true) {
		t.Fatal("workspace MCP with the flag must need trust")
	}

	withInstructions := t.TempDir()
	if err := os.WriteFile(filepath.Join(withInstructions, "AGENTS.md"), []byte("# rules"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !workspaceTrustNeeded(withInstructions, withInstructions, nil, false) {
		t.Fatal("project instructions must need trust")
	}

	withContent := t.TempDir()
	dir := filepath.Join(withContent, ".smidja", "skills")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "demo.md"), []byte("# demo"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !workspaceTrustNeeded(withContent, withContent, nil, false) {
		t.Fatal("workspace content must need trust")
	}
}

func TestResolveTUIModePrecedence(t *testing.T) {
	mode, err := resolveTUIMode("", "fullscreen")
	if err != nil || mode != ui.TUIModeFullscreen {
		t.Fatalf("resolveTUIMode(\"\", fullscreen) = %v, %v", mode, err)
	}
	mode, err = resolveTUIMode("regular", "fullscreen")
	if err != nil || mode != ui.TUIModeRegular {
		t.Fatalf("resolveTUIMode(regular, fullscreen) = %v, %v", mode, err)
	}
	mode, err = resolveTUIMode("", "")
	if err != nil || mode != ui.TUIModeRegular {
		t.Fatalf("resolveTUIMode(\"\", \"\") = %v, %v", mode, err)
	}
	if _, err := resolveTUIMode("bogus", "regular"); err == nil {
		t.Fatal("an invalid CLI mode must error")
	}
}

func TestResolveThemeSettingPrecedence(t *testing.T) {
	theme, err := resolveThemeSetting("", "light/dark")
	if err != nil || theme.Light != "light" || theme.Dark != "dark" {
		t.Fatalf("resolveThemeSetting(\"\", pair) = %+v, %v", theme, err)
	}
	theme, err = resolveThemeSetting("custom", "light/dark")
	if err != nil || theme.Single != "custom" {
		t.Fatalf("resolveThemeSetting(custom, pair) = %+v, %v", theme, err)
	}
	if _, err := resolveThemeSetting("../escape", "dark"); err == nil {
		t.Fatal("a traversal theme must error")
	}
	if _, err := resolveThemeSetting("", "../escape"); err == nil {
		t.Fatal("a configured traversal theme must error")
	}
}

func TestInteractiveLoginProviderNeedsCredential(t *testing.T) {
	home := t.TempDir()
	d := &Deps{Env: envFrom(nil), Home: func() string { return home }}
	cfg := &config.Config{}

	if _, needed, err := interactiveLoginProvider(d, cfg, "openrouter"); err != nil || needed {
		t.Fatalf("plain openrouter = needed %v, err %v, want false", needed, err)
	}
	if _, needed, err := interactiveLoginProvider(d, cfg, ""); err != nil || needed {
		t.Fatalf("default provider = needed %v, err %v, want false", needed, err)
	}
	if _, needed, err := interactiveLoginProvider(d, cfg, "deepseek"); err != nil || needed {
		t.Fatalf("api-key provider = needed %v, err %v, want false", needed, err)
	}

	p, needed, err := interactiveLoginProvider(d, cfg, "anthropic")
	if err != nil || !needed {
		t.Fatalf("missing anthropic credential = needed %v, err %v, want true", needed, err)
	}
	if p.id != "anthropic-oauth" {
		t.Fatalf("provider id = %q, want anthropic", p.id)
	}

	store, err := loadAuthStore(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("anthropic-oauth", authstore.Entry{Type: "oauth", Access: "access", Refresh: "refresh"}); err != nil {
		t.Fatal(err)
	}
	if _, needed, err := interactiveLoginProvider(d, cfg, "anthropic"); err != nil || needed {
		t.Fatalf("stored credential = needed %v, err %v, want false", needed, err)
	}

	envHome := t.TempDir()
	envDeps := &Deps{Env: envFrom(map[string]string{"ANTHROPIC_API_KEY": "sk-env"}), Home: func() string { return envHome }}
	if _, needed, err := interactiveLoginProvider(envDeps, cfg, "anthropic"); err != nil || needed {
		t.Fatalf("env API key = needed %v, err %v, want false", needed, err)
	}
	if _, needed, err := interactiveLoginProvider(envDeps, cfg, "codex"); err != nil || !needed {
		t.Fatalf("codex without a stored credential = needed %v, err %v, want true", needed, err)
	}
}

func TestStartupAuthenticateSkipsStoredCredential(t *testing.T) {
	home := t.TempDir()
	store, err := authstore.Load(filepath.Join(home, ".smidja", "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("anthropic-oauth", authstore.Entry{Type: "oauth", Access: "access", Refresh: "refresh"}); err != nil {
		t.Fatal(err)
	}
	startup, terminal := newStartupFixture(t)
	d := &Deps{Env: envFrom(nil), Home: func() string { return home }}
	if err := startup.authenticate(context.Background(), d, &config.Config{Provider: "anthropic"}, ""); err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if strings.Contains(terminal.Output(), "Sign in") {
		t.Fatal("a stored credential must not open the login dialog")
	}
}

func TestStartupAuthenticateSkipsInjectedClient(t *testing.T) {
	startup, terminal := newStartupFixture(t)
	home := t.TempDir()
	d := &Deps{
		Env:    envFrom(nil),
		Home:   func() string { return home },
		Client: &capturingClient{},
	}
	if err := startup.authenticate(context.Background(), d, &config.Config{Provider: "anthropic"}, ""); err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if strings.Contains(terminal.Output(), "Sign in") {
		t.Fatal("an injected client must not open the login dialog")
	}
}

func TestPrepareTUIStartupFallback(t *testing.T) {
	terminal := newFakeBridgeTerminal()
	terminal.startErr = errors.New("stdin is not a terminal")
	var stderr bytes.Buffer
	home := t.TempDir()
	d := &Deps{
		Env:    envFrom(nil),
		Getwd:  func() (string, error) { return t.TempDir(), nil },
		Home:   func() string { return home },
		Stderr: &stderr,
	}
	cfg := &config.Config{WorkspaceRoot: t.TempDir()}
	plan, err := prepareTUIStartup(context.Background(), d, cfg, tuiStartupOptions{
		mode:        ui.TUIModeRegular,
		newTerminal: bridgeTerminalFactory(terminal),
	})
	if err != nil {
		t.Fatalf("prepareTUIStartup: %v", err)
	}
	if plan.enabled || plan.runner != nil {
		t.Fatalf("plan = %+v, want a disabled fallback plan", plan)
	}
	if !plan.trustWorkspace {
		t.Fatal("a fallback plan must keep the workspace trusted")
	}
	if plan.cwd != "" {
		t.Fatalf("fallback cwd = %q, want the later lookup", plan.cwd)
	}
}

func TestPrepareTUIStartupGetwdError(t *testing.T) {
	d := &Deps{
		Env:   envFrom(nil),
		Getwd: func() (string, error) { return "", errors.New("no working directory") },
		Home:  func() string { return t.TempDir() },
	}
	plan, err := prepareTUIStartup(context.Background(), d, &config.Config{}, tuiStartupOptions{})
	if err == nil || plan != nil {
		t.Fatalf("prepareTUIStartup = %+v, %v, want the working directory error", plan, err)
	}
}

func TestPrepareTUIStartupInvalidMCPStopsTerminal(t *testing.T) {
	terminal := newFakeBridgeTerminal()
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, ".smidja"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".smidja", "mcp.json"), []byte(`{nope`), 0o644); err != nil {
		t.Fatal(err)
	}
	d := &Deps{
		Env:   envFrom(nil),
		Getwd: func() (string, error) { return workspace, nil },
		Home:  func() string { return t.TempDir() },
	}
	plan, err := prepareTUIStartup(context.Background(), d, &config.Config{WorkspaceRoot: workspace}, tuiStartupOptions{
		mode:        ui.TUIModeRegular,
		newTerminal: bridgeTerminalFactory(terminal),
	})
	if err == nil || plan != nil {
		t.Fatalf("prepareTUIStartup = %+v, %v, want the mcp config error", plan, err)
	}
	if terminal.Started() {
		t.Fatal("a failed startup plan must stop the terminal")
	}
}

func TestPrepareTUIStartupSuccess(t *testing.T) {
	terminal := newFakeBridgeTerminal()
	workspace := t.TempDir()
	home := t.TempDir()
	d := &Deps{
		Env:   envFrom(nil),
		Getwd: func() (string, error) { return workspace, nil },
		Home:  func() string { return home },
	}
	plan, err := prepareTUIStartup(context.Background(), d, &config.Config{WorkspaceRoot: workspace}, tuiStartupOptions{
		mode:        ui.TUIModeRegular,
		newTerminal: bridgeTerminalFactory(terminal),
	})
	if err != nil {
		t.Fatalf("prepareTUIStartup: %v", err)
	}
	if !plan.enabled || plan.runner == nil {
		t.Fatalf("plan = %+v, want an enabled startup", plan)
	}
	t.Cleanup(plan.runner.abort)
	if plan.cwd != workspace {
		t.Fatalf("cwd = %q, want %q", plan.cwd, workspace)
	}
	if !plan.trustWorkspace {
		t.Fatal("an empty workspace must stay trusted")
	}
	if !terminal.Started() {
		t.Fatal("the terminal must be started")
	}
}

func TestRunChatRejectsInvalidConfiguredUISettings(t *testing.T) {
	cases := []struct {
		name    string
		cfg     *config.Config
		wantErr string
	}{
		{"tui mode", &config.Config{Model: "test/model", TUIMode: "bogus"}, "regular or fullscreen"},
		{"theme", &config.Config{Model: "test/model", Theme: "../escape"}, "theme name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			deps := wiringTestDeps(t.TempDir())
			deps.Config = tc.cfg
			deps.Client = &capturingClient{}
			deps.Store = wiringStore(t)
			deps.Stdout = &stdout
			deps.Stderr = &stderr
			deps.Stdin = strings.NewReader("")
			err := RunWithDeps(nil, deps)
			if err == nil {
				t.Fatalf("RunWithDeps: want the configured %s error", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("RunWithDeps error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}
