package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/authstore"
	"github.com/digitalygo/smidja/internal/config"
	"github.com/digitalygo/smidja/internal/providers/manifest"
	"github.com/digitalygo/smidja/internal/providers/oauth"
	"github.com/digitalygo/smidja/internal/tui"
	"github.com/digitalygo/smidja/internal/ui"
	"github.com/digitalygo/smidja/sdk"
)

func testLoginProvider(login func(context.Context, oauth.Options) (authstore.Entry, error)) oauthProvider {
	return oauthProvider{id: "test-oauth", name: "test", model: "test/model", login: login}
}

func newLoginFixture(t *testing.T) (*tuiStartup, *fakeBridgeTerminal, *Deps) {
	t.Helper()
	startup, terminal := newStartupFixture(t)
	home := t.TempDir()
	d := &Deps{Env: envFrom(nil), Home: func() string { return home }}
	return startup, terminal, d
}

func loginEntry(t *testing.T, d *Deps) (authstore.Entry, bool) {
	t.Helper()
	store, err := loadAuthStore(d)
	if err != nil {
		t.Fatalf("loadAuthStore: %v", err)
	}
	return store.Get("test-oauth")
}

func TestStartupLoginSuccessShowsDeviceCodeAndStoresCredential(t *testing.T) {
	startup, terminal, d := newLoginFixture(t)
	release := make(chan struct{})
	device := oauth.DeviceCode{VerificationURI: "https://device.test/verify", UserCode: "ABCD-1234"}
	p := testLoginProvider(func(ctx context.Context, options oauth.Options) (authstore.Entry, error) {
		options.DeviceCode(device)
		<-release
		return authstore.Entry{Type: "oauth", Access: "ACCESS-TOKEN", Refresh: "REFRESH-TOKEN"}, nil
	})
	done := make(chan error, 1)
	go func() { done <- startup.runLogin(context.Background(), d, p) }()

	output := waitForTerminalOutput(t, terminal, "https://device.test/verify", 3*time.Second)
	if !strings.Contains(output, "ABCD-1234") {
		t.Fatalf("login dialog missing the device code:\n%s", output)
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runLogin: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runLogin did not return after the credential was issued")
	}

	entry, ok := loginEntry(t, d)
	if !ok || entry.Access != "ACCESS-TOKEN" || entry.Refresh != "REFRESH-TOKEN" {
		t.Fatalf("stored entry = %+v, %v; want the issued credential", entry, ok)
	}
	full := terminal.Output()
	for _, secret := range []string{"ACCESS-TOKEN", "REFRESH-TOKEN"} {
		if strings.Contains(full, secret) {
			t.Fatalf("credential %q leaked into the rendered frames", secret)
		}
	}
}

func TestStartupLoginCanceledByDialog(t *testing.T) {
	startup, terminal, d := newLoginFixture(t)
	p := testLoginProvider(func(ctx context.Context, options oauth.Options) (authstore.Entry, error) {
		<-ctx.Done()
		return authstore.Entry{}, ctx.Err()
	})
	done := make(chan error, 1)
	go func() { done <- startup.runLogin(context.Background(), d, p) }()

	waitForTerminalOutput(t, terminal, "Sign in to test", 3*time.Second)
	terminal.SendInput("\x03")
	select {
	case err := <-done:
		if !errors.Is(err, errStartupLoginCanceled) {
			t.Fatalf("canceled login error = %v, want errStartupLoginCanceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runLogin did not return after the dialog was canceled")
	}
	if _, ok := loginEntry(t, d); ok {
		t.Fatal("a canceled login must not store a credential")
	}
	if !startup.runner.Active() {
		t.Fatal("a canceled login must leave the runner usable")
	}
}

func TestStartupLoginFailureIsReportedAndNotStored(t *testing.T) {
	startup, _, d := newLoginFixture(t)
	boom := errors.New("oauth provider exploded")
	p := testLoginProvider(func(ctx context.Context, options oauth.Options) (authstore.Entry, error) {
		return authstore.Entry{}, boom
	})
	err := startup.runLogin(context.Background(), d, p)
	if !errors.Is(err, boom) {
		t.Fatalf("runLogin error = %v, want the provider error", err)
	}
	if _, ok := loginEntry(t, d); ok {
		t.Fatal("a failed login must not store a credential")
	}
	if !startup.runner.Active() {
		t.Fatal("a failed login must leave the runner usable")
	}
}

func TestStartupLoginCanceledByContext(t *testing.T) {
	startup, terminal, d := newLoginFixture(t)
	p := testLoginProvider(func(ctx context.Context, options oauth.Options) (authstore.Entry, error) {
		<-ctx.Done()
		return authstore.Entry{}, ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- startup.runLogin(ctx, d, p) }()
	waitForTerminalOutput(t, terminal, "Sign in to test", 3*time.Second)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runLogin error = %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runLogin did not return after the context was canceled")
	}
	if _, ok := loginEntry(t, d); ok {
		t.Fatal("a canceled login must not store a credential")
	}
}

func TestStartupLoginManualCodeMasked(t *testing.T) {
	startup, terminal, d := newLoginFixture(t)
	received := make(chan string, 1)
	p := testLoginProvider(func(ctx context.Context, options oauth.Options) (authstore.Entry, error) {
		code, err := options.ManualCode(ctx, "Paste the code")
		if err != nil {
			return authstore.Entry{}, err
		}
		received <- code
		return authstore.Entry{Type: "oauth", Access: "token", Refresh: "refresh"}, nil
	})
	done := make(chan error, 1)
	go func() { done <- startup.runLogin(context.Background(), d, p) }()

	waitForTerminalOutput(t, terminal, "paste the authorization code", 3*time.Second)
	terminal.SendInput("SECRET-CODE\r")
	select {
	case code := <-received:
		if code != "SECRET-CODE" {
			t.Fatalf("manual code = %q, want the entered value", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("manual code was not delivered")
	}
	if err := <-done; err != nil {
		t.Fatalf("runLogin: %v", err)
	}
	if strings.Contains(terminal.Output(), "SECRET-CODE") {
		t.Fatal("the manual authorization code leaked into the rendered frames")
	}
}

func TestStartupLoginHonorsInjectedAuthOptions(t *testing.T) {
	startup, _, d := newLoginFixture(t)
	devices := make(chan oauth.DeviceCode, 1)
	d.AuthOptions = func(provider string) oauth.Options {
		return oauth.Options{
			DeviceCode: func(device oauth.DeviceCode) { devices <- device },
			ManualCode: func(ctx context.Context, prompt string) (string, error) { return "INJECTED-CODE", nil },
		}
	}
	p := testLoginProvider(func(ctx context.Context, options oauth.Options) (authstore.Entry, error) {
		if options.DeviceCode == nil || options.ManualCode == nil {
			return authstore.Entry{}, errors.New("missing injected options")
		}
		options.DeviceCode(oauth.DeviceCode{VerificationURI: "https://device.test", UserCode: "WXYZ"})
		code, err := options.ManualCode(ctx, "prompt")
		if err != nil {
			return authstore.Entry{}, err
		}
		if code != "INJECTED-CODE" {
			return authstore.Entry{}, errors.New("injected manual code was not used")
		}
		return authstore.Entry{Type: "oauth", Access: "token", Refresh: "refresh"}, nil
	})
	if err := startup.runLogin(context.Background(), d, p); err != nil {
		t.Fatalf("runLogin: %v", err)
	}
	select {
	case device := <-devices:
		if device.UserCode != "WXYZ" {
			t.Fatalf("injected device sink received %+v", device)
		}
	default:
		t.Fatal("the injected device sink was not called")
	}
	if _, ok := loginEntry(t, d); !ok {
		t.Fatal("the login must store the credential on success")
	}
}

func TestStartupLoginEmptyCredentialRejected(t *testing.T) {
	startup, _, d := newLoginFixture(t)
	p := testLoginProvider(func(ctx context.Context, options oauth.Options) (authstore.Entry, error) {
		return authstore.Entry{Type: "oauth"}, nil
	})
	err := startup.runLogin(context.Background(), d, p)
	if !errors.Is(err, errStartupLoginEmpty) {
		t.Fatalf("runLogin error = %v, want errStartupLoginEmpty", err)
	}
	if _, ok := loginEntry(t, d); ok {
		t.Fatal("an empty credential must not be stored")
	}
}

func TestStartupAbortAndExitGuards(t *testing.T) {
	var nilStartup *tuiStartup
	nilStartup.abort()
	if !nilStartup.exited() {
		t.Fatal("a nil startup must report exit")
	}
	empty := &tuiStartup{}
	empty.abort()
	if !empty.exited() {
		t.Fatal("a startup without a runner must report exit")
	}
}

func TestStartupConfirmTrustWithoutActiveRunner(t *testing.T) {
	runner := ui.NewRunner(ui.RunnerOptions{
		Stdin:       strings.NewReader(""),
		Stdout:      io.Discard,
		Home:        t.TempDir(),
		NewTerminal: func(io.Reader, io.Writer) tui.Terminal { return newFakeBridgeTerminal() },
	})
	startup := &tuiStartup{runner: runner}
	accepted, err := startup.confirmWorkspaceTrust(context.Background(), "/work", true)
	if err == nil || accepted {
		t.Fatalf("confirmWorkspaceTrust = %v, %v, want an error without an active runner", accepted, err)
	}
}

func TestStartupAuthenticateWithoutActiveRunner(t *testing.T) {
	runner := ui.NewRunner(ui.RunnerOptions{
		Stdin:       strings.NewReader(""),
		Stdout:      io.Discard,
		Home:        t.TempDir(),
		NewTerminal: func(io.Reader, io.Writer) tui.Terminal { return newFakeBridgeTerminal() },
	})
	startup := &tuiStartup{runner: runner}
	d := &Deps{Env: envFrom(nil), Home: func() string { return t.TempDir() }}
	cfg := &config.Config{Provider: "anthropic"}
	err := startup.authenticate(context.Background(), d, cfg, "")
	if err == nil {
		t.Fatal("authenticate without an active runner must return the login error")
	}
	if !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("authenticate error = %v, want ErrModeUnsupported from StartLogin", err)
	}
}

func TestInteractiveLoginProviderCorruptStore(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".smidja")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{nope`), 0o644); err != nil {
		t.Fatal(err)
	}
	d := &Deps{Env: envFrom(nil), Home: func() string { return home }}
	if _, _, err := interactiveLoginProvider(d, &config.Config{}, "anthropic"); err == nil {
		t.Fatal("a corrupt auth store must surface the read error")
	}
}

func TestInteractiveLoginProviderStoredAPIKey(t *testing.T) {
	home := t.TempDir()
	d := &Deps{Env: envFrom(nil), Home: func() string { return home }}
	store, err := loadAuthStore(d)
	if err != nil {
		t.Fatal(err)
	}
	spec, ok := manifest.Lookup("deepseek")
	if !ok {
		t.Skip("deepseek is not in the manifest")
	}
	if err := store.Set(spec.ID, authstore.Entry{Type: "api_key", Key: "sk-store"}); err != nil {
		t.Fatal(err)
	}
	if _, needed, err := interactiveLoginProvider(d, &config.Config{}, spec.ID); err != nil || needed {
		t.Fatalf("stored api key = needed %v, err %v, want false", needed, err)
	}
}

func TestStartupLoginAbortedByRunnerExit(t *testing.T) {
	startup, terminal, d := newLoginFixture(t)
	blocked := make(chan struct{})
	p := testLoginProvider(func(ctx context.Context, options oauth.Options) (authstore.Entry, error) {
		<-ctx.Done()
		close(blocked)
		return authstore.Entry{}, ctx.Err()
	})
	errs := make(chan error, 1)
	go func() { errs <- startup.runLogin(context.Background(), d, p) }()
	waitForTerminalOutput(t, terminal, "Sign in to test", 3*time.Second)
	terminal.FireEOF()
	select {
	case err := <-errs:
		if !errors.Is(err, errTUIStartupAborted) {
			t.Fatalf("runLogin error = %v, want errTUIStartupAborted", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runLogin did not return after the runner exited")
	}
	select {
	case <-blocked:
	case <-time.After(3 * time.Second):
		t.Fatal("the login goroutine did not observe the cancellation")
	}
}

func TestStartupLoginAbortedStates(t *testing.T) {
	startup, terminal, _ := newLoginFixture(t)
	op, err := startup.runner.StartLogin(context.Background(), ui.LoginRequest{Provider: "test", Title: "Sign in"})
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	waitForTerminalOutput(t, terminal, "Sign in", time.Second)
	boom := errors.New("provider failed")
	op.Fail(boom)
	if got := startup.loginAborted(context.Background(), op); !errors.Is(got, boom) {
		t.Fatalf("loginAborted failed state = %v, want the provider error", got)
	}
	if got := startup.loginAborted(context.Background(), &ui.LoginOperation{}); !errors.Is(got, errStartupLoginCanceled) {
		t.Fatalf("loginAborted starting state = %v, want errStartupLoginCanceled", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := startup.loginAborted(ctx, &ui.LoginOperation{}); !errors.Is(got, context.Canceled) {
		t.Fatalf("loginAborted canceled context = %v, want context.Canceled", got)
	}
	nilRunner := &tuiStartup{}
	if got := nilRunner.loginAborted(context.Background(), &ui.LoginOperation{}); !errors.Is(got, errTUIStartupAborted) {
		t.Fatalf("loginAborted exited runner = %v, want errTUIStartupAborted", got)
	}
}

func TestLoginInProgressStates(t *testing.T) {
	for _, state := range []ui.LoginState{ui.LoginStarting, ui.LoginAwaiting, ui.LoginManual} {
		if !loginInProgress(state) {
			t.Fatalf("loginInProgress(%q) = false, want true", state)
		}
	}
	for _, state := range []ui.LoginState{ui.LoginSucceeded, ui.LoginFailed, ui.LoginCanceled, ui.LoginTimedOut} {
		if loginInProgress(state) {
			t.Fatalf("loginInProgress(%q) = true, want false", state)
		}
	}
}

func TestStartupFinishLoginSettledAndStoreErrors(t *testing.T) {
	p := testLoginProvider(nil)
	valid := startupLoginResult{entry: authstore.Entry{Type: "oauth", Access: "access", Refresh: "refresh"}}

	startup, terminal, d := newLoginFixture(t)
	op, err := startup.runner.StartLogin(context.Background(), ui.LoginRequest{Provider: "test", Title: "Sign in"})
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	waitForTerminalOutput(t, terminal, "Sign in", time.Second)
	op.Succeed()
	if got := startup.finishLogin(context.Background(), d, p, op, valid); !errors.Is(got, errStartupLoginCanceled) {
		t.Fatalf("finishLogin settled = %v, want errStartupLoginCanceled", got)
	}

	startup, terminal, d = newLoginFixture(t)
	op, err = startup.runner.StartLogin(context.Background(), ui.LoginRequest{Provider: "test", Title: "Sign in"})
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	waitForTerminalOutput(t, terminal, "Sign in", time.Second)
	home := d.Home()
	if err := os.MkdirAll(filepath.Join(home, ".smidja"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".smidja", "auth.json"), []byte(`{nope`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := startup.finishLogin(context.Background(), d, p, op, valid); got == nil {
		t.Fatal("finishLogin with a corrupt store must error")
	}
	op.Cancel()
}

func TestStartupLoginStartErrorOnStoppedRunner(t *testing.T) {
	terminal := newFakeBridgeTerminal()
	runner := ui.NewRunner(ui.RunnerOptions{
		Stdin:       strings.NewReader(""),
		Stdout:      io.Discard,
		Home:        t.TempDir(),
		NewTerminal: bridgeTerminalFactory(terminal),
	})
	startup := &tuiStartup{runner: runner}
	p := testLoginProvider(func(ctx context.Context, options oauth.Options) (authstore.Entry, error) {
		return authstore.Entry{}, errors.New("must not run")
	})
	err := startup.runLogin(context.Background(), &Deps{Env: envFrom(nil), Home: func() string { return t.TempDir() }}, p)
	if !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("runLogin error = %v, want sdk.ErrModeUnsupported", err)
	}
}

func TestStartupLoginMissingStoreReportsError(t *testing.T) {
	startup, _, _ := newLoginFixture(t)
	base := t.TempDir()
	blocker := filepath.Join(base, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := &Deps{Env: envFrom(nil), Home: func() string { return filepath.Join(blocker, "home") }}
	p := testLoginProvider(func(ctx context.Context, options oauth.Options) (authstore.Entry, error) {
		return authstore.Entry{Type: "oauth", Access: "token", Refresh: "refresh"}, nil
	})
	err := startup.runLogin(context.Background(), d, p)
	if err == nil || !strings.Contains(err.Error(), "authstore") {
		t.Fatalf("runLogin error = %v, want the auth store error", err)
	}
}

func TestStartupLoginOptionsFillManualCode(t *testing.T) {
	startup, _, _ := newLoginFixture(t)
	home := t.TempDir()
	d := &Deps{
		Env:  envFrom(nil),
		Home: func() string { return home },
		AuthOptions: func(provider string) oauth.Options {
			return oauth.Options{}
		},
	}
	op, err := startup.runner.StartLogin(context.Background(), ui.LoginRequest{Provider: "test", Title: "Sign in to test"})
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	options := tuiLoginOptions(d, oauthProvider{id: "test-oauth", name: "test"}, op)
	if options.ManualCode == nil {
		t.Fatal("an injected AuthOptions without ManualCode must fall back to the masked dialog")
	}
	if options.DeviceCode == nil {
		t.Fatal("device progress must always be published to the dialog")
	}
	options.DeviceCode(oauth.DeviceCode{VerificationURI: "https://device.test", UserCode: "WXYZ"})
	op.Cancel()
	<-op.Done()
}

func TestStartupExitedAfterRunnerExitAndAbort(t *testing.T) {
	startup, terminal := newStartupFixture(t)
	if startup.exited() {
		t.Fatal("a running startup must not count as exited")
	}
	startup.runner.RequestExit()
	deadline := time.Now().Add(2 * time.Second)
	for !startup.exited() {
		if time.Now().After(deadline) {
			t.Fatal("startup never observed the runner exit")
		}
		time.Sleep(time.Millisecond)
	}
	startup.abort()
	if terminal.Started() {
		t.Fatal("abort must stop the terminal")
	}
}
