package ui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/tui"
	"github.com/digitalygo/smidja/sdk"
)

func waitForLoginState(t *testing.T, op *LoginOperation, want LoginState) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if op.State() == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("login state = %q, want %q", op.State(), want)
}

func trackManualInstalls(op *LoginOperation) <-chan uint64 {
	installs := make(chan uint64, 8)
	op.setManualInstallHook(func(generation uint64, installed bool) {
		if installed {
			installs <- generation
		}
	})
	return installs
}

func awaitManualInstall(t *testing.T, installs <-chan uint64, generation uint64) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case got := <-installs:
			if got == generation {
				return
			}
		case <-deadline:
			t.Fatalf("manual generation %d was not installed", generation)
		}
	}
}

func TestLoginProgressUpdateRendersDeviceCode(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	op, err := runner.StartLogin(context.Background(), LoginRequest{Provider: "openrouter", Title: "Sign in to openrouter"})
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	defer op.Cancel()
	if op.State() != LoginStarting {
		t.Fatalf("initial state = %q, want %q", op.State(), LoginStarting)
	}
	frame := activeDialogRendered(t, runner)
	if !strings.Contains(frame, string(LoginStarting)) {
		t.Fatalf("starting frame missing coarse state:\n%s", frame)
	}

	op.Update(LoginUpdate{
		VerificationURL: "https://example.test/device",
		UserCode:        "ABCD-1234",
		Status:          "waiting for authorization",
		Deadline:        time.Now().Add(time.Minute),
	})
	if op.State() != LoginAwaiting {
		t.Fatalf("state after update = %q, want %q", op.State(), LoginAwaiting)
	}
	frame = activeDialogRendered(t, runner)
	for _, want := range []string{string(LoginAwaiting), "https://example.test/device", "ABCD-1234", "waiting for authorization", "Expires in"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("frame missing %q:\n%s", want, frame)
		}
	}
	if strings.Contains(strings.ToLower(frame), "poll") {
		t.Fatalf("frame fabricated a poll count:\n%s", frame)
	}

	op.Succeed()
	result := op.Wait()
	if result.State != LoginSucceeded || result.Err != nil {
		t.Fatalf("result = %+v, want succeeded", result)
	}
}

func TestLoginSuccessSettlesExactlyOnce(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	op, err := runner.StartLogin(context.Background(), LoginRequest{Provider: "openrouter"})
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	op.Succeed()
	op.Fail(errors.New("late failure"))
	op.Cancel()
	op.Update(LoginUpdate{Status: "late update"})
	select {
	case <-op.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("login did not settle")
	}
	result := op.Wait()
	if result.State != LoginSucceeded || result.Err != nil {
		t.Fatalf("result = %+v, want succeeded", result)
	}
	if op.State() != LoginSucceeded {
		t.Fatalf("state = %q after late calls, want %q", op.State(), LoginSucceeded)
	}
	if runner.dialogs.Active() {
		t.Fatal("login dialog stayed active after success")
	}
}

func TestLoginProviderFailurePropagates(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	op, err := runner.StartLogin(context.Background(), LoginRequest{Provider: "openrouter"})
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	boom := errors.New("provider boom")
	op.Fail(boom)
	result := op.Wait()
	if result.State != LoginFailed {
		t.Fatalf("state = %q, want %q", result.State, LoginFailed)
	}
	if !errors.Is(result.Err, boom) {
		t.Fatalf("error = %v, want %v", result.Err, boom)
	}
	op.Succeed()
	if op.State() != LoginFailed {
		t.Fatalf("state = %q after late success, want %q", op.State(), LoginFailed)
	}
}

func TestLoginDeadlineTimeoutCancelsFlow(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	op, err := runner.StartLogin(context.Background(), LoginRequest{Provider: "openrouter", Deadline: time.Now().Add(30 * time.Millisecond)})
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	select {
	case <-op.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("login deadline was not honored")
	}
	result := op.Wait()
	if result.State != LoginTimedOut {
		t.Fatalf("state = %q, want %q", result.State, LoginTimedOut)
	}
	if !errors.Is(result.Err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", result.Err)
	}
	if op.Context().Err() == nil {
		t.Fatal("flow context was not canceled on timeout")
	}
}

func TestLoginUserCancelResolvesCanceled(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	op, err := runner.StartLogin(context.Background(), LoginRequest{Provider: "openrouter"})
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	waitForDialog(t, runner)
	terminal.SendInput("\x1b")
	result := op.Wait()
	if result.State != LoginCanceled {
		t.Fatalf("state = %q, want %q", result.State, LoginCanceled)
	}
	if !errors.Is(result.Err, errLoginCanceled) {
		t.Fatalf("error = %v, want login canceled", result.Err)
	}
}

func TestLoginRunnerShutdownCancelsOperation(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	op, err := runner.StartLogin(context.Background(), LoginRequest{Provider: "openrouter"})
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	waitForDialog(t, runner)
	runner.Stop()
	result := op.Wait()
	if result.State != LoginCanceled {
		t.Fatalf("state = %q, want %q", result.State, LoginCanceled)
	}
	if !errors.Is(result.Err, errDialogsClosed) {
		t.Fatalf("error = %v, want dialogs closed", result.Err)
	}
}

func TestLoginManualBrowserRacePrefersBrowser(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	op, err := runner.StartLogin(context.Background(), LoginRequest{Provider: "openrouter"})
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	installs := trackManualInstalls(op)
	manualErr := make(chan error, 1)
	manualCode := make(chan string, 1)
	go func() {
		code, callErr := op.RequestManualCode(context.Background())
		manualCode <- code
		manualErr <- callErr
	}()
	waitForLoginState(t, op, LoginManual)
	awaitManualInstall(t, installs, 1)
	op.Succeed()

	select {
	case err := <-manualErr:
		if err == nil {
			t.Fatal("manual prompt resolved without an error after browser success")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("manual prompt did not resolve after browser success")
	}
	if code := <-manualCode; code != "" {
		t.Fatalf("manual code = %q, want empty", code)
	}
	result := op.Wait()
	if result.State != LoginSucceeded {
		t.Fatalf("state = %q, want %q", result.State, LoginSucceeded)
	}
}

func TestLoginLateCallbackIgnored(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	op, err := runner.StartLogin(context.Background(), LoginRequest{Provider: "openrouter"})
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	installs := trackManualInstalls(op)
	manualErr := make(chan error, 1)
	go func() {
		_, callErr := op.RequestManualCode(context.Background())
		manualErr <- callErr
	}()
	waitForLoginState(t, op, LoginManual)
	awaitManualInstall(t, installs, 1)
	op.Succeed()
	<-manualErr

	op.dialog.HandleInput("\r")
	op.dialog.HandleInput("\x1b")
	if op.State() != LoginSucceeded {
		t.Fatalf("state = %q after late manual input, want %q", op.State(), LoginSucceeded)
	}
	if op.Err() != nil {
		t.Fatalf("error = %v after late manual input, want nil", op.Err())
	}
	op.Update(LoginUpdate{Status: "late", VerificationURL: "https://late.test"})
	if op.State() != LoginSucceeded {
		t.Fatalf("state = %q after late update, want %q", op.State(), LoginSucceeded)
	}
	if _, err := op.RequestManualCode(context.Background()); err == nil {
		t.Fatal("manual code request after settle must fail")
	}
}

func TestLoginStorageFailurePropagation(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	op, err := runner.StartLogin(context.Background(), LoginRequest{Provider: "openrouter", Title: "Sign in to openrouter"})
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	op.Update(LoginUpdate{Status: "authorized"})
	storageErr := errors.New("ui: store credential: disk full")
	op.Fail(storageErr)
	result := op.Wait()
	if result.State != LoginFailed {
		t.Fatalf("state = %q, want %q", result.State, LoginFailed)
	}
	if !errors.Is(result.Err, storageErr) {
		t.Fatalf("error = %v, want %v", result.Err, storageErr)
	}
	op.Succeed()
	if op.State() != LoginFailed {
		t.Fatalf("state = %q after late success, want %q", op.State(), LoginFailed)
	}
	if !strings.Contains(op.view.Status, "disk full") {
		t.Fatalf("failed view did not carry the storage error: %q", op.view.Status)
	}
}

func TestLoginManualCodeMaskedInFramesAndDocument(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	op, err := runner.StartLogin(context.Background(), LoginRequest{Provider: "openrouter"})
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	installs := trackManualInstalls(op)
	manualErr := make(chan error, 1)
	manualCode := make(chan string, 1)
	go func() {
		code, callErr := op.RequestManualCode(context.Background())
		manualCode <- code
		manualErr <- callErr
	}()
	waitForLoginState(t, op, LoginManual)
	awaitManualInstall(t, installs, 1)

	const secret = "SECRET-CODE-42"
	terminal.SendInput(secret)
	runner.view.RenderNow(true)
	frame := tui.StripTerminalSequences(terminal.Output())
	if strings.Contains(frame, secret) {
		t.Fatalf("manual code leaked into frames:\n%s", frame)
	}
	if !strings.Contains(frame, "•") {
		t.Fatalf("manual code was not masked:\n%s", frame)
	}
	document := strings.Join(runner.Surface().RenderDocument(80), "\n")
	if strings.Contains(document, secret) {
		t.Fatalf("manual code leaked into the final document:\n%s", document)
	}

	terminal.SendInput("\r")
	select {
	case err := <-manualErr:
		if err != nil {
			t.Fatalf("manual submit failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("manual submit did not resolve")
	}
	if code := <-manualCode; code != secret {
		t.Fatalf("manual code = %q, want %q", code, secret)
	}

	runner.view.RenderNow(true)
	after := tui.StripTerminalSequences(terminal.Output())
	if strings.Contains(after, secret) {
		t.Fatalf("manual code leaked after submit:\n%s", after)
	}
	op.Succeed()
	result := op.Wait()
	if result.State != LoginSucceeded {
		t.Fatalf("state = %q, want %q", result.State, LoginSucceeded)
	}
}

func TestLoginCallerContextCancellation(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	ctx, cancel := context.WithCancel(context.Background())
	op, err := runner.StartLogin(ctx, LoginRequest{Provider: "openrouter"})
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	cancel()
	result := op.Wait()
	if result.State != LoginCanceled {
		t.Fatalf("state = %q, want %q", result.State, LoginCanceled)
	}
	if !errors.Is(result.Err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", result.Err)
	}
}

func TestLoginCallerDeadlineMapsToTimeout(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	op, err := runner.StartLogin(ctx, LoginRequest{Provider: "openrouter"})
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	result := op.Wait()
	if result.State != LoginTimedOut {
		t.Fatalf("state = %q, want %q", result.State, LoginTimedOut)
	}
	if !errors.Is(result.Err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", result.Err)
	}
}

func TestLoginDeadlineRearmOnUpdate(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	op, err := runner.StartLogin(context.Background(), LoginRequest{Provider: "openrouter", Deadline: time.Now().Add(5 * time.Minute)})
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	op.Update(LoginUpdate{Status: "waiting", Deadline: time.Now().Add(20 * time.Millisecond)})
	select {
	case <-op.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("re-armed deadline was not honored")
	}
	result := op.Wait()
	if result.State != LoginTimedOut {
		t.Fatalf("state = %q, want %q", result.State, LoginTimedOut)
	}
}

func TestLoginStartWhileBusyHonorsContext(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	first, err := runner.StartLogin(context.Background(), LoginRequest{Provider: "openrouter"})
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runner.StartLogin(ctx, LoginRequest{Provider: "openrouter"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("StartLogin while busy = %v, want context canceled", err)
	}
	first.Cancel()
	first.Wait()
}

func TestLoginUpdateWhileManualPendingKeepsManualState(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	op, err := runner.StartLogin(context.Background(), LoginRequest{Provider: "openrouter"})
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	installs := trackManualInstalls(op)
	manualErr := make(chan error, 1)
	go func() {
		_, callErr := op.RequestManualCode(context.Background())
		manualErr <- callErr
	}()
	waitForLoginState(t, op, LoginManual)
	awaitManualInstall(t, installs, 1)
	op.Update(LoginUpdate{Status: "still waiting", VerificationURL: "https://example.test/device"})
	if op.State() != LoginManual {
		t.Fatalf("state = %q during manual input, want %q", op.State(), LoginManual)
	}
	op.Cancel()
	<-manualErr
}

func TestLoginManualRequestSupersede(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	op, err := runner.StartLogin(context.Background(), LoginRequest{Provider: "openrouter"})
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	installs := trackManualInstalls(op)
	firstErr := make(chan error, 1)
	go func() {
		_, callErr := op.RequestManualCode(context.Background())
		firstErr <- callErr
	}()
	waitForLoginState(t, op, LoginManual)
	awaitManualInstall(t, installs, 1)
	secondCode := make(chan string, 1)
	secondErr := make(chan error, 1)
	go func() {
		code, callErr := op.RequestManualCode(context.Background())
		secondCode <- code
		secondErr <- callErr
	}()
	select {
	case err := <-firstErr:
		if err == nil {
			t.Fatal("superseded manual request resolved without an error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("superseded manual request did not resolve")
	}
	waitForLoginState(t, op, LoginManual)
	awaitManualInstall(t, installs, 2)
	terminal.SendInput("CODE-2")
	terminal.SendInput("\r")
	if code := <-secondCode; code != "CODE-2" {
		t.Fatalf("second manual code = %q, want CODE-2", code)
	}
	if err := <-secondErr; err != nil {
		t.Fatalf("second manual request failed: %v", err)
	}
	op.Succeed()
	op.Wait()
}

func TestLoginManualRequestContextCancel(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	op, err := runner.StartLogin(context.Background(), LoginRequest{Provider: "openrouter"})
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	installs := trackManualInstalls(op)
	manualErr := make(chan error, 1)
	go func() {
		_, callErr := op.RequestManualCode(ctx)
		manualErr <- callErr
	}()
	waitForLoginState(t, op, LoginManual)
	awaitManualInstall(t, installs, 1)
	cancel()
	select {
	case err := <-manualErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("manual request error = %v, want context canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("manual request did not resolve on context cancel")
	}
	if op.State() != LoginAwaiting {
		t.Fatalf("state = %q after manual cancel, want %q", op.State(), LoginAwaiting)
	}
	op.Cancel()
	op.Wait()
}

func TestLoginRunnerShutdownDuringManualPrompt(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	op, err := runner.StartLogin(context.Background(), LoginRequest{Provider: "openrouter"})
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	installs := trackManualInstalls(op)
	manualErr := make(chan error, 1)
	go func() {
		_, callErr := op.RequestManualCode(context.Background())
		manualErr <- callErr
	}()
	waitForLoginState(t, op, LoginManual)
	awaitManualInstall(t, installs, 1)
	runner.Stop()
	select {
	case err := <-manualErr:
		if err == nil {
			t.Fatal("manual prompt resolved without an error during shutdown")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("manual prompt did not resolve during shutdown")
	}
	result := op.Wait()
	if result.State != LoginCanceled {
		t.Fatalf("state = %q, want %q", result.State, LoginCanceled)
	}
}

func TestLoginManualTitleDefaultsToProviderless(t *testing.T) {
	if got := loginManualTitle("  "); got != "Paste the authorization code" {
		t.Fatalf("loginManualTitle blank = %q", got)
	}
	if got := loginManualTitle("openrouter"); !strings.Contains(got, "openrouter") {
		t.Fatalf("loginManualTitle provider = %q", got)
	}
}

func TestOAuthPromptCompatibilityShutdownPropagation(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	done := make(chan error, 1)
	go func() {
		_, err := runner.ShowOAuthPrompt(context.Background(), OAuthPrompt{Title: "OAuth login", Provider: "openrouter"})
		done <- err
	}()
	waitForDialog(t, runner)
	runner.Stop()
	select {
	case err := <-done:
		if !errors.Is(err, errDialogsClosed) {
			t.Fatalf("ShowOAuthPrompt during shutdown = %v, want dialogs closed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ShowOAuthPrompt did not resolve during shutdown")
	}
}

func TestOAuthPromptCompatibilityDefaultsTitle(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	done := make(chan bool, 1)
	go func() {
		ok, _ := runner.ShowOAuthPrompt(context.Background(), OAuthPrompt{Provider: "openrouter"})
		done <- ok
	}()
	waitForDialog(t, runner)
	if !strings.Contains(activeDialogRendered(t, runner), "Sign in") {
		t.Fatal("default login title missing")
	}
	terminal.SendInput("\x1b")
	select {
	case ok := <-done:
		if ok {
			t.Fatal("canceled default login must not be accepted")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("default login did not resolve")
	}
}

func TestPromptManualCodeCompatibilityDefaultsProvider(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	done := make(chan string, 1)
	go func() {
		value, _ := runner.PromptManualCode(context.Background(), "")
		done <- value
	}()
	waitForDialog(t, runner)
	terminal.SendInput("\x1b")
	select {
	case value := <-done:
		if value != "" {
			t.Fatalf("canceled manual code = %q, want empty", value)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("PromptManualCode did not resolve")
	}
}

func TestPromptManualCodeCompatibilityShutdownPropagation(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	done := make(chan error, 1)
	go func() {
		_, err := runner.PromptManualCode(context.Background(), "openrouter")
		done <- err
	}()
	waitForManualPrompt(t, runner, terminal)
	runner.Stop()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("PromptManualCode during shutdown returned no error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("PromptManualCode did not resolve during shutdown")
	}
}

func waitForManualPrompt(t *testing.T, runner *Runner, terminal *fakeUITerminal) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runner.view.RenderNow(true)
		if strings.Contains(tui.StripTerminalSequences(terminal.Output()), "paste the authorization") {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("manual prompt did not become active")
}

func TestLoginInactiveRunnerUnsupported(t *testing.T) {
	terminal := newFakeUITerminal(80, 24)
	opts := fakeUIRunnerOptions(terminal)
	opts.Home = t.TempDir()
	runner := NewRunner(opts)
	if _, err := runner.StartLogin(context.Background(), LoginRequest{Provider: "openrouter"}); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("StartLogin on inactive runner = %v, want ErrModeUnsupported", err)
	}
}

func TestLoginDelayedManualInstallCannotReplaceCurrentPrompt(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	op, err := runner.StartLogin(context.Background(), LoginRequest{Provider: "openrouter"})
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	preInstall := make(chan uint64, 4)
	installed := make(chan uint64, 4)
	releaseFirst := make(chan struct{})
	op.setManualInstallHook(func(generation uint64, ok bool) {
		if !ok {
			preInstall <- generation
			if generation == 1 {
				<-releaseFirst
			}
			return
		}
		installed <- generation
	})

	firstErr := make(chan error, 1)
	go func() {
		_, callErr := op.RequestManualCode(context.Background())
		firstErr <- callErr
	}()
	if generation := <-preInstall; generation != 1 {
		t.Fatalf("first pre-install generation = %d, want 1", generation)
	}

	secondCode := make(chan string, 1)
	secondErr := make(chan error, 1)
	go func() {
		code, callErr := op.RequestManualCode(context.Background())
		secondCode <- code
		secondErr <- callErr
	}()
	if generation := <-preInstall; generation != 2 {
		t.Fatalf("second pre-install generation = %d, want 2", generation)
	}
	if generation := <-installed; generation != 2 {
		t.Fatalf("installed generation = %d, want 2", generation)
	}

	close(releaseFirst)
	select {
	case err := <-firstErr:
		if !errors.Is(err, errManualCanceled) {
			t.Fatalf("superseded request error = %v, want errManualCanceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("superseded request did not resolve")
	}
	select {
	case generation := <-installed:
		t.Fatalf("stale generation %d install was accepted", generation)
	case <-time.After(50 * time.Millisecond):
	}

	terminal.SendInput("CODE-2")
	terminal.SendInput("\r")
	if code := <-secondCode; code != "CODE-2" {
		t.Fatalf("current manual code = %q, want CODE-2", code)
	}
	if err := <-secondErr; err != nil {
		t.Fatalf("current manual request failed: %v", err)
	}
	op.Succeed()
	op.Wait()
}

func TestLoginSupersededManualSubmitCannotSettleCurrentPrompt(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	op, err := runner.StartLogin(context.Background(), LoginRequest{Provider: "openrouter"})
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	preInstall := make(chan uint64, 4)
	installed := make(chan uint64, 4)
	blockSecond := make(chan struct{})
	op.setManualInstallHook(func(generation uint64, ok bool) {
		if !ok {
			preInstall <- generation
			if generation == 2 {
				<-blockSecond
			}
			return
		}
		installed <- generation
	})

	firstErr := make(chan error, 1)
	go func() {
		_, callErr := op.RequestManualCode(context.Background())
		firstErr <- callErr
	}()
	if generation := <-preInstall; generation != 1 {
		t.Fatalf("first pre-install generation = %d, want 1", generation)
	}
	if generation := <-installed; generation != 1 {
		t.Fatalf("first installed generation = %d, want 1", generation)
	}

	secondCode := make(chan string, 1)
	secondErr := make(chan error, 1)
	go func() {
		code, callErr := op.RequestManualCode(context.Background())
		secondCode <- code
		secondErr <- callErr
	}()
	if generation := <-preInstall; generation != 2 {
		t.Fatalf("second pre-install generation = %d, want 2", generation)
	}

	terminal.SendInput("STALE")
	terminal.SendInput("\r")
	if op.State() != LoginManual {
		t.Fatalf("state = %q after superseded submit, want %q", op.State(), LoginManual)
	}
	select {
	case err := <-secondErr:
		t.Fatalf("superseded submit resolved the current request: %v", err)
	default:
	}

	close(blockSecond)
	if generation := <-installed; generation != 2 {
		t.Fatalf("second installed generation = %d, want 2", generation)
	}
	select {
	case err := <-firstErr:
		if !errors.Is(err, errManualCanceled) {
			t.Fatalf("superseded request error = %v, want errManualCanceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("superseded request did not resolve")
	}

	terminal.SendInput("CODE-B")
	terminal.SendInput("\r")
	if code := <-secondCode; code != "CODE-B" {
		t.Fatalf("current manual code = %q, want CODE-B", code)
	}
	if err := <-secondErr; err != nil {
		t.Fatalf("current manual request failed: %v", err)
	}
	op.Succeed()
	op.Wait()
}

func TestLoginSupersededManualCancelCannotSettleCurrentPrompt(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	op, err := runner.StartLogin(context.Background(), LoginRequest{Provider: "openrouter"})
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	preInstall := make(chan uint64, 4)
	installed := make(chan uint64, 4)
	blockSecond := make(chan struct{})
	op.setManualInstallHook(func(generation uint64, ok bool) {
		if !ok {
			preInstall <- generation
			if generation == 2 {
				<-blockSecond
			}
			return
		}
		installed <- generation
	})

	firstErr := make(chan error, 1)
	go func() {
		_, callErr := op.RequestManualCode(context.Background())
		firstErr <- callErr
	}()
	if generation := <-preInstall; generation != 1 {
		t.Fatalf("first pre-install generation = %d, want 1", generation)
	}
	if generation := <-installed; generation != 1 {
		t.Fatalf("first installed generation = %d, want 1", generation)
	}

	secondCode := make(chan string, 1)
	secondErr := make(chan error, 1)
	go func() {
		code, callErr := op.RequestManualCode(context.Background())
		secondCode <- code
		secondErr <- callErr
	}()
	if generation := <-preInstall; generation != 2 {
		t.Fatalf("second pre-install generation = %d, want 2", generation)
	}

	terminal.SendInput("\x1b")
	if op.State() != LoginManual {
		t.Fatalf("state = %q after superseded cancel, want %q", op.State(), LoginManual)
	}
	select {
	case err := <-secondErr:
		t.Fatalf("superseded cancel settled the current request: %v", err)
	default:
	}

	close(blockSecond)
	if generation := <-installed; generation != 2 {
		t.Fatalf("second installed generation = %d, want 2", generation)
	}
	select {
	case err := <-firstErr:
		if !errors.Is(err, errManualCanceled) {
			t.Fatalf("superseded request error = %v, want errManualCanceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("superseded request did not resolve")
	}

	terminal.SendInput("CODE-B")
	terminal.SendInput("\r")
	if code := <-secondCode; code != "CODE-B" {
		t.Fatalf("current manual code = %q, want CODE-B", code)
	}
	if err := <-secondErr; err != nil {
		t.Fatalf("current manual request failed: %v", err)
	}
	op.Succeed()
	op.Wait()
}
