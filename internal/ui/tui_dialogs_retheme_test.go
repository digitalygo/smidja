package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/tui"
	"github.com/digitalygo/smidja/internal/tui/interactive"
)

func activeDialogRendered(t *testing.T, runner *Runner) string {
	t.Helper()
	active := runner.dialogs.active
	if active == nil {
		t.Fatal("no active dialog")
	}
	return strings.Join(active.Render(60), "\n")
}

func requireThemeAnsi(t *testing.T, theme *tui.Theme, token tui.ThemeColor) string {
	t.Helper()
	if theme == nil {
		t.Fatal("nil theme")
	}
	ansi, ok := theme.GetFgAnsi(token)
	if !ok {
		t.Fatalf("theme %q is missing fg token %q", theme.Name, token)
	}
	return ansi
}

func TestApplyThemeRethemesActiveSelectDialog(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	result := make(chan string, 1)
	go func() {
		value, _ := runner.Select("Pick a value", []string{"alpha", "beta", "gamma"})
		result <- value
	}()
	waitForDialog(t, runner)
	terminal.SendInput("gam")
	dark := runner.surface.Theme()
	before := activeDialogRendered(t, runner)
	if !strings.Contains(before, requireThemeAnsi(t, dark, "accent")) {
		t.Fatalf("active select dialog missing dark accent:\n%s", before)
	}
	if !strings.Contains(before, "gamma") || strings.Contains(before, "alpha") {
		t.Fatalf("search filter not applied before retheme:\n%s", before)
	}

	if err := runner.ApplyTheme("light"); err != nil {
		t.Fatalf("ApplyTheme: %v", err)
	}
	light := runner.surface.Theme()
	after := activeDialogRendered(t, runner)
	if !strings.Contains(after, requireThemeAnsi(t, light, "accent")) {
		t.Fatalf("active select dialog missing light accent:\n%s", after)
	}
	if strings.Contains(after, requireThemeAnsi(t, dark, "accent")) {
		t.Fatalf("active select dialog kept dark accent:\n%s", after)
	}
	if !strings.Contains(after, "gamma") || strings.Contains(after, "alpha") {
		t.Fatalf("search filter lost after retheme:\n%s", after)
	}

	terminal.SendInput("\r")
	select {
	case value := <-result:
		if value != "gamma" {
			t.Fatalf("selected after retheme = %q, want gamma", value)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("select dialog did not resolve")
	}
}

func TestApplyThemeRethemesActiveLoginDialog(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	result := make(chan bool, 1)
	go func() {
		ok, _ := runner.ShowOAuthPrompt(context.Background(), OAuthPrompt{
			Title:           "OAuth login",
			Provider:        "openrouter",
			VerificationURL: "https://example.test/device",
			UserCode:        "ABCD-1234",
			State:           "waiting for authorization",
		})
		result <- ok
	}()
	waitForDialog(t, runner)
	dark := runner.surface.Theme()
	before := activeDialogRendered(t, runner)
	if !strings.Contains(before, requireThemeAnsi(t, dark, "border")) {
		t.Fatalf("active login dialog missing dark border:\n%s", before)
	}

	if err := runner.ApplyTheme("light"); err != nil {
		t.Fatalf("ApplyTheme: %v", err)
	}
	light := runner.surface.Theme()
	after := activeDialogRendered(t, runner)
	if !strings.Contains(after, requireThemeAnsi(t, light, "border")) {
		t.Fatalf("active login dialog missing light border:\n%s", after)
	}
	if strings.Contains(after, requireThemeAnsi(t, dark, "border")) {
		t.Fatalf("active login dialog kept dark border:\n%s", after)
	}
	for _, fragment := range []string{"https://example.test/device", "ABCD-1234", "waiting for authorization"} {
		if !strings.Contains(tui.StripTerminalSequences(after), fragment) {
			t.Fatalf("login dialog lost %q after retheme:\n%s", fragment, after)
		}
	}

	terminal.SendInput("y")
	select {
	case ok := <-result:
		if !ok {
			t.Fatal("login dialog did not accept after retheme")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("login dialog did not resolve")
	}
}

func TestApplyThemeRethemesActiveInputDialog(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	result := make(chan string, 1)
	go func() {
		value, _ := runner.Input("Name", "type here")
		result <- value
	}()
	waitForDialog(t, runner)
	terminal.SendInput("hello")
	dark := runner.surface.Theme()
	before := activeDialogRendered(t, runner)
	if !strings.Contains(before, requireThemeAnsi(t, dark, "border")) {
		t.Fatalf("active input dialog missing dark border:\n%s", before)
	}

	if err := runner.ApplyTheme("light"); err != nil {
		t.Fatalf("ApplyTheme: %v", err)
	}
	light := runner.surface.Theme()
	after := activeDialogRendered(t, runner)
	if !strings.Contains(after, requireThemeAnsi(t, light, "border")) {
		t.Fatalf("active input dialog missing light border:\n%s", after)
	}
	if !strings.Contains(tui.StripTerminalSequences(after), "hello") {
		t.Fatalf("input value lost after retheme:\n%s", after)
	}

	terminal.SendInput("\r")
	select {
	case value := <-result:
		if value != "hello" {
			t.Fatalf("input value after retheme = %q, want hello", value)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("input dialog did not resolve")
	}
}

func TestApplyThemeRethemesActiveEditorDialog(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	result := make(chan string, 1)
	go func() {
		value, _ := runner.Editor("Edit", "line one")
		result <- value
	}()
	waitForDialog(t, runner)
	terminal.SendInput("\x1b[D")
	dark := runner.surface.Theme()
	before := activeDialogRendered(t, runner)
	if !strings.Contains(before, requireThemeAnsi(t, dark, "thinkingOff")) {
		t.Fatalf("active editor dialog missing dark editor border:\n%s", before)
	}

	if err := runner.ApplyTheme("light"); err != nil {
		t.Fatalf("ApplyTheme: %v", err)
	}
	light := runner.surface.Theme()
	after := activeDialogRendered(t, runner)
	if !strings.Contains(after, requireThemeAnsi(t, light, "thinkingOff")) {
		t.Fatalf("active editor dialog missing light editor border:\n%s", after)
	}
	if strings.Contains(after, requireThemeAnsi(t, dark, "thinkingOff")) {
		t.Fatalf("active editor dialog kept dark editor border:\n%s", after)
	}
	if !strings.Contains(tui.StripTerminalSequences(after), "line one") {
		t.Fatalf("editor text lost after retheme:\n%s", after)
	}

	terminal.SendInput("\r")
	select {
	case value := <-result:
		if value != "line one" {
			t.Fatalf("editor value after retheme = %q, want line one", value)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("editor dialog did not resolve")
	}
}

func TestApplyThemeRethemesActiveSettingsDialog(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	items := []tui.SettingItem{
		{ID: "retry", Label: "Retry", CurrentValue: "on", Values: []string{"on", "off"}, Description: "Retry with backoff"},
	}
	applied := make(chan map[string]string, 1)
	go func() {
		values, _, _ := runner.ShowSettings(context.Background(), items)
		applied <- values
	}()
	waitForDialog(t, runner)
	terminal.SendInput("\r")
	dark := runner.surface.Theme()
	before := activeDialogRendered(t, runner)
	if !strings.Contains(before, requireThemeAnsi(t, dark, "accent")) {
		t.Fatalf("active settings dialog missing dark accent:\n%s", before)
	}

	if err := runner.ApplyTheme("light"); err != nil {
		t.Fatalf("ApplyTheme: %v", err)
	}
	light := runner.surface.Theme()
	after := activeDialogRendered(t, runner)
	if !strings.Contains(after, requireThemeAnsi(t, light, "accent")) {
		t.Fatalf("active settings dialog missing light accent:\n%s", after)
	}
	if strings.Contains(after, requireThemeAnsi(t, dark, "accent")) {
		t.Fatalf("active settings dialog kept dark accent:\n%s", after)
	}

	terminal.SendInput("\x13")
	select {
	case values := <-applied:
		if values["retry"] != "off" {
			t.Fatalf("settings draft after retheme = %v, want retry=off", values)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("settings dialog did not resolve")
	}
}

func TestApplyThemeRethemesActiveMaskedInputDialog(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	result := make(chan string, 1)
	go func() {
		value, _ := runner.PromptSecret(context.Background(), "Token")
		result <- value
	}()
	waitForDialog(t, runner)
	terminal.SendInput("s3cret")
	dark := runner.surface.Theme()
	before := activeDialogRendered(t, runner)
	if !strings.Contains(before, requireThemeAnsi(t, dark, "border")) {
		t.Fatalf("active masked dialog missing dark border:\n%s", before)
	}

	if err := runner.ApplyTheme("light"); err != nil {
		t.Fatalf("ApplyTheme: %v", err)
	}
	light := runner.surface.Theme()
	after := activeDialogRendered(t, runner)
	if !strings.Contains(after, requireThemeAnsi(t, light, "border")) {
		t.Fatalf("active masked dialog missing light border:\n%s", after)
	}
	if strings.Contains(after, requireThemeAnsi(t, dark, "border")) {
		t.Fatalf("active masked dialog kept dark border:\n%s", after)
	}
	visible := tui.StripTerminalSequences(after)
	if strings.Contains(visible, "s3cret") {
		t.Fatalf("secret leaked in masked dialog:\n%s", visible)
	}

	terminal.SendInput("\r")
	select {
	case value := <-result:
		if value != "s3cret" {
			t.Fatalf("secret after retheme = %q, want s3cret", value)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("masked dialog did not resolve")
	}
}

func TestActiveDialogTypeAssertionsAreStable(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	result := make(chan string, 1)
	go func() {
		value, _ := runner.Select("Pick", []string{"alpha", "beta"})
		result <- value
	}()
	waitForDialog(t, runner)
	if _, ok := runner.dialogs.active.(*interactive.SelectDialog); !ok {
		t.Fatalf("active dialog type = %T", runner.dialogs.active)
	}
	if err := runner.ApplyTheme("light"); err != nil {
		t.Fatalf("ApplyTheme: %v", err)
	}
	if _, ok := runner.dialogs.active.(*interactive.SelectDialog); !ok {
		t.Fatalf("active dialog type changed after retheme: %T", runner.dialogs.active)
	}
	terminal.SendInput("\x1b")
	<-result
}
