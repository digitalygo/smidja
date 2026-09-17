package ui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/tui"
	"github.com/digitalygo/smidja/sdk"
)

func waitForDialog(t *testing.T, runner *Runner) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runner.dialogs.overlayFocused() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("dialog overlay was not installed and focused")
}

func TestDialogConfirmAcceptDeclineCancel(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "accept enter", input: "\r", want: true},
		{name: "accept y", input: "y", want: true},
		{name: "decline n", input: "n", want: false},
		{name: "cancel escape", input: "\x1b", want: false},
		{name: "cancel ctrl+c", input: "\x03", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner, terminal := startTestRunner(t, TUIModeRegular, nil)
			result := make(chan bool, 1)
			fail := make(chan error, 1)
			go func() {
				ok, err := runner.Confirm("Title", "Message")
				if err != nil {
					fail <- err
					return
				}
				result <- ok
			}()
			waitForDialog(t, runner)
			runner.view.RenderNow(true)
			terminal.SendInput(tc.input)
			select {
			case err := <-fail:
				t.Fatalf("Confirm error = %v", err)
			case got := <-result:
				if got != tc.want {
					t.Fatalf("Confirm = %v, want %v", got, tc.want)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("Confirm did not resolve")
			}
			if runner.dialogs.Active() {
				t.Fatal("dialog still active after result")
			}
			if runner.view.FocusedComponent() != runner.surface.Editor() {
				t.Fatal("focus was not restored to the editor")
			}
		})
	}
}

func TestDialogSelectSearchableAndCancel(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	result := make(chan string, 1)
	go func() {
		value, _ := runner.Select("Pick", []string{"alpha", "beta", "gamma"})
		result <- value
	}()
	waitForDialog(t, runner)
	terminal.SendInput("ga")
	terminal.SendInput("\r")
	select {
	case got := <-result:
		if got != "gamma" {
			t.Fatalf("Select = %q, want gamma", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Select did not resolve")
	}

	cancelled := make(chan string, 1)
	go func() {
		value, _ := runner.Select("Pick", []string{"alpha"})
		cancelled <- value
	}()
	waitForDialog(t, runner)
	terminal.SendInput("\x1b")
	select {
	case got := <-cancelled:
		if got != "" {
			t.Fatalf("cancelled Select = %q, want empty", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled Select did not resolve")
	}
}

func TestDialogInputAndEditorRoundTrip(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	inputResult := make(chan string, 1)
	go func() {
		value, _ := runner.Input("Name", "placeholder")
		inputResult <- value
	}()
	waitForDialog(t, runner)
	terminal.SendInput("hello")
	terminal.SendInput("\r")
	select {
	case got := <-inputResult:
		if got != "hello" {
			t.Fatalf("Input = %q, want hello", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Input did not resolve")
	}

	editorResult := make(chan string, 1)
	go func() {
		value, _ := runner.Editor("Edit", "first")
		editorResult <- value
	}()
	waitForDialog(t, runner)
	terminal.SendInput("+")
	terminal.SendInput("\x1b[13;2~")
	terminal.SendInput("second")
	terminal.SendInput("\r")
	select {
	case got := <-editorResult:
		if got != "first+\nsecond" {
			t.Fatalf("Editor = %q, want %q", got, "first+\nsecond")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Editor did not resolve")
	}
}

func TestDialogEditorEmptyTextRoundTrip(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	result := make(chan string, 1)
	go func() {
		value, _ := runner.Editor("Edit", "")
		result <- value
	}()
	waitForDialog(t, runner)
	terminal.SendInput("\r")
	select {
	case got := <-result:
		if got != "" {
			t.Fatalf("empty Editor = %q, want empty", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Editor did not resolve")
	}
}

func TestDialogExactWhitespacePreserved(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	result := make(chan string, 1)
	go func() {
		value, _ := runner.Editor("Edit", "  leading")
		result <- value
	}()
	waitForDialog(t, runner)
	terminal.SendInput("\r")
	select {
	case got := <-result:
		if got != "  leading" {
			t.Fatalf("Editor = %q, want preserved whitespace", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Editor did not resolve")
	}
}

func TestDialogConcurrentAdmission(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	first := make(chan bool, 1)
	second := make(chan bool, 1)
	go func() {
		ok, _ := runner.Confirm("first", "one")
		first <- ok
	}()
	waitForDialog(t, runner)
	go func() {
		ok, _ := runner.Confirm("second", "two")
		second <- ok
	}()
	time.Sleep(20 * time.Millisecond)
	select {
	case <-second:
		t.Fatal("second dialog resolved before the first")
	default:
	}
	terminal.SendInput("y")
	select {
	case got := <-first:
		if !got {
			t.Fatal("first dialog did not accept")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first dialog did not resolve")
	}
	waitForDialog(t, runner)
	terminal.SendInput("n")
	select {
	case got := <-second:
		if got {
			t.Fatal("second dialog should have declined")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second dialog did not resolve")
	}
}

func TestDialogCallerCancellation(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	ctx, cancel := context.WithCancel(context.Background())
	ui := runner.BoundUI(ctx)
	done := make(chan error, 1)
	go func() {
		_, err := ui.Confirm("t", "m")
		done <- err
	}()
	waitForDialog(t, runner)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Confirm error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled dialog did not resolve")
	}
	if runner.dialogs.Active() {
		t.Fatal("dialog still active after cancellation")
	}
}

func TestDialogExitUnblocksWaiter(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	done := make(chan error, 1)
	go func() {
		_, err := runner.Confirm("t", "m")
		done <- err
	}()
	waitForDialog(t, runner)
	runner.RequestExit()
	select {
	case err := <-done:
		if !errors.Is(err, errDialogsClosed) {
			t.Fatalf("Confirm error = %v, want errDialogsClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("dialog did not resolve on exit")
	}
}

func TestDialogStartFailureUnblocks(t *testing.T) {
	terminal := newFakeUITerminal(80, 24)
	terminal.startErr = errors.New("no terminal here")
	opts := fakeUIRunnerOptions(terminal)
	opts.Home = t.TempDir()
	runner := NewRunner(opts)
	if err := runner.Start(); err == nil {
		t.Fatal("Start() should fail")
	}
	if _, err := runner.Confirm("t", "m"); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("Confirm after start failure = %v, want ErrModeUnsupported", err)
	}
}

func TestDialogModalCapturePreemptsChatAndMouse(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeFullscreen, nil)
	runner.Surface().AddToolExecution("read", nil)
	before := runner.Surface().ToolsExpanded()
	result := make(chan bool, 1)
	go func() {
		ok, _ := runner.Confirm("t", "m")
		result <- ok
	}()
	waitForDialog(t, runner)
	terminal.SendInput("\x0f")
	if runner.Surface().ToolsExpanded() != before {
		t.Fatal("chat shortcut toggled tool expansion while a modal was open")
	}
	terminal.SendInput("\r")
	select {
	case <-result:
	case <-time.After(2 * time.Second):
		t.Fatal("Confirm did not resolve")
	}
}

func TestDialogWorkerBlockedWhileRenderAndResizeContinue(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	done := make(chan string, 1)
	go func() {
		value, _ := runner.Input("Name", "")
		done <- value
	}()
	waitForDialog(t, runner)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			runner.view.RenderNow(true)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			terminal.SetSize(80+i, 24)
		}
	}()
	wg.Wait()
	terminal.SendInput("ok")
	terminal.SendInput("\r")
	select {
	case got := <-done:
		if got != "ok" {
			t.Fatalf("Input = %q, want ok", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("dialog did not resolve while rendering continued")
	}
}

func TestMaskedInputNeverEmitsPlaintext(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	done := make(chan string, 1)
	go func() {
		value, _ := runner.PromptSecret(context.Background(), "Token")
		done <- value
	}()
	waitForDialog(t, runner)
	secret := "sup3r-secret-value"
	terminal.SendInput(secret)
	runner.view.RenderNow(true)
	rendered := terminal.Output()
	if strings.Contains(rendered, secret) {
		t.Fatalf("terminal output leaked the secret:\n%s", rendered)
	}
	terminal.SendInput("\r")
	select {
	case got := <-done:
		if got != secret {
			t.Fatalf("PromptSecret = %q, want the typed value", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("PromptSecret did not resolve")
	}
	if strings.Contains(terminal.Output(), secret) {
		t.Fatalf("secret leaked after accept:\n%s", terminal.Output())
	}
}

func TestMaskedInputCancel(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	done := make(chan string, 1)
	go func() {
		value, _ := runner.PromptSecret(context.Background(), "Token")
		done <- value
	}()
	waitForDialog(t, runner)
	terminal.SendInput("\x1b")
	select {
	case got := <-done:
		if got != "" {
			t.Fatalf("cancelled PromptSecret = %q, want empty", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("PromptSecret did not resolve on cancel")
	}
}

func TestMaskedInputProtocolSequencesNeverEnterSecret(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	done := make(chan string, 1)
	go func() {
		value, _ := runner.PromptSecret(context.Background(), "Token")
		done <- value
	}()
	waitForDialog(t, runner)
	terminal.SendInput(tui.FocusIn)
	terminal.SendInput("\x1b[<0;1;1M")
	terminal.SendInput("\x1b[?1;2c")
	terminal.SendInput("\x1b[97u")
	terminal.SendInput("bc")
	terminal.SendInput("\r")
	select {
	case got := <-done:
		if got != "abc" {
			t.Fatalf("PromptSecret = %q, want abc", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("PromptSecret did not resolve")
	}
}

func TestPromptSecretNeverAppearsInFramesTranscriptOrDocument(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	done := make(chan string, 1)
	go func() {
		value, _ := runner.PromptSecret(context.Background(), "Token")
		done <- value
	}()
	waitForDialog(t, runner)
	secret := "s3cret-ünïcode-🎉"
	terminal.SendInput(secret)
	runner.view.RenderNow(true)
	if frames := terminal.Output(); strings.Contains(frames, secret) {
		t.Fatalf("frames leaked the secret:\n%s", frames)
	}
	if document := strings.Join(runner.Surface().RenderDocument(80), "\n"); strings.Contains(document, secret) {
		t.Fatalf("document leaked the secret:\n%s", document)
	}
	terminal.SendInput("\r")
	select {
	case got := <-done:
		if got != secret {
			t.Fatalf("PromptSecret = %q, want the typed value", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("PromptSecret did not resolve")
	}
	runner.FlushFinalDocument()
	if frames := terminal.Output(); strings.Contains(frames, secret) {
		t.Fatalf("post-accept frames leaked the secret:\n%s", frames)
	}
	if document := strings.Join(runner.Surface().RenderDocument(80), "\n"); strings.Contains(document, secret) {
		t.Fatalf("final document leaked the secret:\n%s", document)
	}
}

func TestSelectThemeAppliesAndRecolors(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	runner.Surface().AddUserMessage("retained transcript line")
	before := runner.Surface().Theme()
	done := make(chan string, 1)
	go func() {
		value, _, _ := runner.SelectTheme(context.Background())
		done <- value
	}()
	waitForDialog(t, runner)
	terminal.SendInput("\r")
	select {
	case got := <-done:
		if got == "" {
			t.Fatal("SelectTheme returned no theme")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SelectTheme did not resolve")
	}
	if runner.Surface().Theme() == before {
		t.Fatal("theme was not applied")
	}
	runner.view.RenderNow(true)
	output := terminal.Output()
	if !strings.Contains(output, "retained transcript line") {
		t.Fatalf("retained transcript missing after recolor:\n%s", output)
	}
}

func TestSelectThinkingIsTruthful(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	done := make(chan string, 1)
	go func() {
		value, _, _ := runner.SelectThinking(context.Background())
		done <- value
	}()
	waitForDialog(t, runner)
	terminal.SendInput("\r")
	select {
	case got := <-done:
		if got != "provider-default" {
			t.Fatalf("SelectThinking = %q, want provider-default", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SelectThinking did not resolve")
	}
}

func TestShowSettingsApplyAndCancel(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	apply := make(chan map[string]string, 1)
	go func() {
		values, _, _ := runner.ShowSettings(context.Background(), []tui.SettingItem{
			{ID: "retry", Label: "Retry", Values: []string{"on", "off"}, CurrentValue: "on"},
		})
		apply <- values
	}()
	waitForDialog(t, runner)
	terminal.SendInput("\r")
	terminal.SendInput("\x13")
	select {
	case values := <-apply:
		if values["retry"] != "off" {
			t.Fatalf("applied retry = %q, want off", values["retry"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ShowSettings apply did not resolve")
	}

	cancel := make(chan bool, 1)
	go func() {
		_, ok, _ := runner.ShowSettings(context.Background(), []tui.SettingItem{
			{ID: "retry", Label: "Retry", Values: []string{"on", "off"}, CurrentValue: "on"},
		})
		cancel <- ok
	}()
	waitForDialog(t, runner)
	terminal.SendInput("\x1b")
	select {
	case ok := <-cancel:
		if ok {
			t.Fatal("cancelled settings should not report acceptance")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ShowSettings cancel did not resolve")
	}
}

func TestShowHelpListsCommands(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	done := make(chan error, 1)
	go func() {
		done <- runner.ShowHelp(context.Background(), []HelpEntry{
			{Name: "help", Description: "show command help"},
			{Name: "model", Description: "select a model"},
		})
	}()
	waitForDialog(t, runner)
	runner.view.RenderNow(true)
	if !strings.Contains(terminal.Output(), "show command") {
		t.Fatalf("help did not include command descriptions:\n%s", terminal.Output())
	}
	terminal.SendInput("\r")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ShowHelp error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ShowHelp did not resolve")
	}
}

func TestSelectSessionReturnsPathWithoutMutation(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	done := make(chan string, 1)
	go func() {
		value, _, _ := runner.SelectSession(context.Background(), []SessionChoice{
			{Path: "/tmp/sessions/abc.jsonl", Label: "abc", Description: "12 messages"},
		})
		done <- value
	}()
	waitForDialog(t, runner)
	terminal.SendInput("\r")
	select {
	case got := <-done:
		if got != "/tmp/sessions/abc.jsonl" {
			t.Fatalf("SelectSession = %q, want the path", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SelectSession did not resolve")
	}
}

func TestTrustAndOAuthDialogs(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	trusted := make(chan bool, 1)
	go func() {
		ok, _ := runner.ConfirmTrust(context.Background(), "/work/project")
		trusted <- ok
	}()
	waitForDialog(t, runner)
	runner.view.RenderNow(true)
	if !strings.Contains(terminal.Output(), "/work/project") {
		t.Fatalf("trust dialog missing workspace:\n%s", terminal.Output())
	}
	terminal.SendInput("y")
	select {
	case ok := <-trusted:
		if !ok {
			t.Fatal("trust was not accepted")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("trust dialog did not resolve")
	}

	oauth := make(chan bool, 1)
	go func() {
		ok, _ := runner.ShowOAuthPrompt(context.Background(), OAuthPrompt{
			Title:           "OAuth login",
			Provider:        "openrouter",
			VerificationURL: "https://example.test/device",
			UserCode:        "ABCD-1234",
			State:           "waiting for authorization",
		})
		oauth <- ok
	}()
	waitForDialog(t, runner)
	runner.view.RenderNow(true)
	output := terminal.Output()
	for _, want := range []string{"https://example.test/device", "ABCD-1234", "waiting for authorization"} {
		if !strings.Contains(output, want) {
			t.Fatalf("OAuth dialog missing %q:\n%s", want, output)
		}
	}
	terminal.SendInput("\x1b")
	select {
	case ok := <-oauth:
		if ok {
			t.Fatal("cancelled OAuth prompt should not be accepted")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OAuth dialog did not resolve")
	}
}

func TestModalServiceThemeAppliesToActiveDialog(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	done := make(chan struct{}, 1)
	go func() {
		_, _ = runner.Confirm("t", "m")
		done <- struct{}{}
	}()
	waitForDialog(t, runner)
	if err := runner.ApplyTheme("light"); err != nil {
		t.Fatalf("ApplyTheme: %v", err)
	}
	runner.view.RenderNow(true)
	if !strings.Contains(terminal.Output(), "t") {
		t.Fatalf("active dialog disappeared after theme change:\n%s", terminal.Output())
	}
	terminal.SendInput("y")
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("dialog did not resolve")
	}
}

func TestDialogOverlayNeverInFinalDocument(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	done := make(chan string, 1)
	go func() {
		value, _ := runner.Input("Secret title", "")
		done <- value
	}()
	waitForDialog(t, runner)
	document := strings.Join(runner.Surface().RenderDocument(80), "\n")
	if strings.Contains(document, "Secret title") {
		t.Fatalf("dialog content leaked into the final document:\n%s", document)
	}
	terminal.SendInput("\x1b")
	<-done
}

func TestBoundUIPreservesFireAndForgetMethods(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	bound := runner.BoundUI(context.Background())
	bound.Notify("bound notice", sdk.NotifyInfo)
	bound.SetStatus("k", "v")
	bound.SetWidget("w", []string{"line"})
	runner.SetWorking(true)
	bound.SetWorkingMessage("working")
	bound.SetTitle("bound title")
	runner.view.RenderNow(true)
	output := terminal.Output()
	for _, want := range []string{"bound notice", "v", "line", "working"} {
		if !strings.Contains(output, want) {
			t.Fatalf("bound UI missing %q:\n%s", want, output)
		}
	}
}

func TestDialogLateResultIgnored(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	result := make(chan bool, 1)
	go func() {
		ok, _ := runner.Confirm("t", "m")
		result <- ok
	}()
	waitForDialog(t, runner)
	terminal.SendInput("y")
	select {
	case <-result:
	case <-time.After(2 * time.Second):
		t.Fatal("Confirm did not resolve")
	}
	terminal.SendInput("y")
	terminal.SendInput("\r")
	if runner.dialogs.Active() {
		t.Fatal("late input reopened a dialog")
	}
	next := make(chan bool, 1)
	go func() {
		ok, _ := runner.Confirm("again", "m")
		next <- ok
	}()
	waitForDialog(t, runner)
	terminal.SendInput("y")
	select {
	case ok := <-next:
		if !ok {
			t.Fatal("second dialog did not accept")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second dialog did not resolve")
	}
}

func TestSelectSessionRejectsNonPath(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	done := make(chan error, 1)
	go func() {
		_, _, err := runner.SelectSession(context.Background(), []SessionChoice{{Path: "not-a-path", Label: "bad"}})
		done <- err
	}()
	waitForDialog(t, runner)
	terminal.SendInput("\r")
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("SelectSession accepted a non-path value")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SelectSession did not resolve")
	}
}

func TestConfirmTrustCancel(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	done := make(chan bool, 1)
	go func() {
		ok, _ := runner.ConfirmTrust(context.Background(), "/work/project")
		done <- ok
	}()
	waitForDialog(t, runner)
	terminal.SendInput("\x03")
	select {
	case ok := <-done:
		if ok {
			t.Fatal("Ctrl-C trust decision must not be accepted")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("trust dialog did not resolve")
	}
}

func TestDialogFullscreenMouseCapture(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeFullscreen, nil)
	result := make(chan bool, 1)
	go func() {
		ok, _ := runner.Confirm("t", "m")
		result <- ok
	}()
	waitForDialog(t, runner)
	terminal.SendInput("\x1b[<0;3;22M")
	terminal.SendInput("\x1b[<0;3;22m")
	terminal.SendInput("\r")
	select {
	case ok := <-result:
		if !ok {
			t.Fatal("dialog lost focus after an outside mouse click")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("dialog did not resolve after outside click")
	}
}

func TestSelectModelAndManualCode(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	done := make(chan string, 1)
	go func() {
		value, _, _ := runner.SelectModel(context.Background(), "current", []ModelChoice{
			{ID: "m1", Provider: "prov", ContextWindow: 128000},
			{ID: "m2", Provider: "prov", ContextWindow: 200000},
		})
		done <- value
	}()
	waitForDialog(t, runner)
	runner.view.RenderNow(true)
	if !strings.Contains(terminal.Output(), "128000") {
		t.Fatalf("model dialog missing context window:\n%s", terminal.Output())
	}
	terminal.SendInput("m2")
	terminal.SendInput("\r")
	select {
	case got := <-done:
		if got != "m2" {
			t.Fatalf("SelectModel = %q, want m2", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SelectModel did not resolve")
	}

	manual := make(chan string, 1)
	go func() {
		value, _ := runner.PromptManualCode(context.Background(), "openrouter")
		manual <- value
	}()
	waitForDialog(t, runner)
	terminal.SendInput("\x1b")
	select {
	case got := <-manual:
		if got != "" {
			t.Fatalf("cancelled manual code = %q, want empty", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("PromptManualCode did not resolve")
	}
}

func TestBoundUIDialogDelegationOnInactiveRunner(t *testing.T) {
	terminal := newFakeUITerminal(80, 24)
	opts := fakeUIRunnerOptions(terminal)
	opts.Home = t.TempDir()
	runner := NewRunner(opts)
	bound := runner.BoundUI(context.Background())
	if _, err := bound.Confirm("t", "m"); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("Confirm = %v, want ErrModeUnsupported", err)
	}
	if _, err := bound.Select("t", []string{"a"}); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("Select = %v, want ErrModeUnsupported", err)
	}
	if _, err := bound.Input("t", "p"); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("Input = %v, want ErrModeUnsupported", err)
	}
	if _, err := bound.Editor("t", ""); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("Editor = %v, want ErrModeUnsupported", err)
	}
	if _, err := runner.PromptSecret(context.Background(), "t"); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("PromptSecret = %v, want ErrModeUnsupported", err)
	}
	if _, _, err := runner.ShowSettings(context.Background(), nil); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("ShowSettings = %v, want ErrModeUnsupported", err)
	}
	if _, _, err := runner.SelectTheme(context.Background()); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("SelectTheme = %v, want ErrModeUnsupported", err)
	}
}

func TestOAuthFailureAndLateCallback(t *testing.T) {
	terminal := newFakeUITerminal(80, 24)
	opts := fakeUIRunnerOptions(terminal)
	opts.Home = t.TempDir()
	inactive := NewRunner(opts)
	if _, err := inactive.ShowOAuthPrompt(context.Background(), OAuthPrompt{Title: "OAuth"}); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("inactive OAuth prompt = %v, want ErrModeUnsupported", err)
	}
	if _, err := inactive.PromptManualCode(context.Background(), "prov"); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Fatalf("inactive manual code = %v, want ErrModeUnsupported", err)
	}

	runner, liveTerminal := startTestRunner(t, TUIModeRegular, nil)
	first := make(chan bool, 1)
	go func() {
		ok, _ := runner.ShowOAuthPrompt(context.Background(), OAuthPrompt{
			Title:           "OAuth login",
			VerificationURL: "https://example.test/device",
			UserCode:        "CODE-1",
			State:           "waiting",
		})
		first <- ok
	}()
	waitForDialog(t, runner)
	liveTerminal.SendInput("\r")
	select {
	case <-first:
	case <-time.After(2 * time.Second):
		t.Fatal("OAuth prompt did not resolve")
	}
	liveTerminal.SendInput("\r")
	second := make(chan bool, 1)
	go func() {
		ok, _ := runner.ShowOAuthPrompt(context.Background(), OAuthPrompt{Title: "OAuth again"})
		second <- ok
	}()
	waitForDialog(t, runner)
	liveTerminal.SendInput("\x1b")
	select {
	case ok := <-second:
		if ok {
			t.Fatal("late OAuth prompt should not be accepted on cancel")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second OAuth prompt did not resolve")
	}
}
