package interactive

import (
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/tui"
)

func TestLoginDialogRendersCoarseStates(t *testing.T) {
	theme := testDialogTheme(t)
	for _, state := range []string{"starting", "awaiting authorization", "awaiting manual code", "signed in", "failed", "canceled", "timed out"} {
		dialog := NewLoginDialog("Sign in", theme, nil)
		dialog.SetState(LoginDialogState{Provider: "openrouter", State: state, Status: "detail " + state})
		rendered := frameText(dialog.Render(60))
		if !strings.Contains(rendered, state) {
			t.Fatalf("state %q missing from frame:\n%s", state, rendered)
		}
		if !strings.Contains(rendered, "detail "+state) {
			t.Fatalf("status for %q missing from frame:\n%s", state, rendered)
		}
	}
}

func TestLoginDialogRendersDeviceCodeAndDeadline(t *testing.T) {
	theme := testDialogTheme(t)
	dialog := NewLoginDialog("Sign in", theme, nil)
	dialog.SetState(LoginDialogState{
		Provider:        "openrouter",
		State:           "awaiting authorization",
		VerificationURL: "https://example.test/device",
		UserCode:        "ABCD-1234",
		Deadline:        time.Now().Add(90 * time.Second),
	})
	rendered := frameText(dialog.Render(60))
	for _, want := range []string{"openrouter", "https://example.test/device", "ABCD-1234", "Expires in", "Esc cancel"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("frame missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(strings.ToLower(rendered), "poll") {
		t.Fatalf("frame fabricated a poll count:\n%s", rendered)
	}
}

func TestLoginDialogSanitizesDisplayFields(t *testing.T) {
	theme := testDialogTheme(t)
	dialog := NewLoginDialog("Ti\x1b[2Jtle", theme, nil)
	dialog.SetState(LoginDialogState{
		Provider:        "P\x1b[2JVISIBLE_PROV",
		State:           "S\x1b[2JVISIBLE_STATE",
		Status:          "D\x1b]0;OSC_EVIL\x07VISIBLE_STATUS",
		VerificationURL: "https://example.test/\x1b[2JVISIBLE_URL",
		UserCode:        "C\x1b[31mVISIBLE_CODE",
	})
	rendered := frameText(dialog.Render(70))
	if strings.Contains(rendered, "OSC_EVIL") {
		t.Fatalf("OSC sequence survived sanitization:\n%s", rendered)
	}
	for _, want := range []string{"VISIBLE_PROV", "VISIBLE_STATE", "VISIBLE_STATUS", "VISIBLE_URL", "VISIBLE_CODE"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("sanitized frame missing %q:\n%s", want, rendered)
		}
	}
}

func TestLoginDialogManualInputMaskedAndDelivered(t *testing.T) {
	theme := testDialogTheme(t)
	dialog := NewLoginDialog("Sign in", theme, nil)
	var submitted string
	dialog.PromptManual(1, "Paste code", func(code string) { submitted = code }, nil)
	dialog.SetFocused(true)

	const secret = "SECRET-CODE-42"
	for _, r := range secret {
		dialog.HandleInput(string(r))
	}
	rendered := frameText(dialog.Render(60))
	if strings.Contains(rendered, secret) {
		t.Fatalf("secret leaked into the manual frame:\n%s", rendered)
	}
	if !strings.Contains(rendered, "•") {
		t.Fatalf("manual input was not masked:\n%s", rendered)
	}

	dialog.HandleInput("\r")
	if submitted != secret {
		t.Fatalf("submitted code = %q, want %q", submitted, secret)
	}
	after := frameText(dialog.Render(60))
	if strings.Contains(after, secret) {
		t.Fatalf("secret leaked into the frame after submit:\n%s", after)
	}
	if !strings.Contains(after, "Sign in") {
		t.Fatalf("dialog did not return to the login frame:\n%s", after)
	}
}

func TestLoginDialogManualCancelInvokesHandler(t *testing.T) {
	theme := testDialogTheme(t)
	canceled := 0
	dialog := NewLoginDialog("Sign in", theme, nil)
	dialog.PromptManual(1, "Paste code", nil, func() { canceled++ })
	dialog.HandleInput("\x1b")
	if canceled != 1 {
		t.Fatalf("manual cancel calls = %d, want 1", canceled)
	}
	dialog.HandleInput("\x1b")
	if canceled != 1 {
		t.Fatalf("manual cancel calls = %d after settled, want 1", canceled)
	}
}

func TestLoginDialogCancelAndContinue(t *testing.T) {
	theme := testDialogTheme(t)
	canceled := 0
	continued := 0
	dialog := NewLoginDialog("Sign in", theme, func() { canceled++ })
	dialog.HandleInput("\x1b")
	if canceled != 1 {
		t.Fatalf("cancel calls = %d, want 1", canceled)
	}
	dialog.HandleInput("\x1b")
	if canceled != 1 {
		t.Fatalf("cancel calls = %d after settled, want 1", canceled)
	}

	acknowledged := NewLoginDialog("Sign in", theme, nil)
	acknowledged.SetContinue(func() { continued++ })
	acknowledged.HandleInput("y")
	if continued != 1 {
		t.Fatalf("continue calls = %d, want 1", continued)
	}
	acknowledged.HandleInput("\r")
	if continued != 1 {
		t.Fatalf("continue calls = %d after settled, want 1", continued)
	}

	plain := NewLoginDialog("Sign in", theme, nil)
	plain.HandleInput("\r")
	if settled := frameText(plain.Render(40)); !strings.Contains(settled, "Sign in") {
		t.Fatalf("dialog without continue handler dissolved on enter:\n%s", settled)
	}
}

func TestLoginDialogSettleAndClearManual(t *testing.T) {
	theme := testDialogTheme(t)
	dialog := NewLoginDialog("Sign in", theme, nil)
	dialog.PromptManual(1, "Paste code", nil, nil)
	dialog.ClearManual(1)
	rendered := frameText(dialog.Render(50))
	if !strings.Contains(rendered, "Sign in") {
		t.Fatalf("cleared manual view did not restore the login frame:\n%s", rendered)
	}
	dialog.SetTheme(theme)
	dialog.SetFocused(true)
	dialog.Invalidate()
	dialog.PromptManual(1, "Paste code", nil, nil)
	dialog.Settle()
	dialog.PromptManual(2, "Late", nil, nil)
	if !strings.Contains(frameText(dialog.Render(50)), "Sign in") {
		t.Fatal("settled dialog accepted a late manual prompt")
	}
}

func TestLoginDialogStopHidesManualFocus(t *testing.T) {
	theme := testDialogTheme(t)
	dialog := NewLoginDialog("Sign in", theme, nil)
	dialog.PromptManual(1, "Paste code", nil, nil)
	dialog.SetFocused(false)
	if rendered := dialog.Render(50); len(rendered) == 0 {
		t.Fatal("manual frame was empty")
	}
	_ = tui.VisibleWidth("x")
}

func TestLoginDialogStaleManualGenerationIgnored(t *testing.T) {
	theme := testDialogTheme(t)
	dialog := NewLoginDialog("Sign in", theme, nil)
	oldSubmit, oldCancel := 0, 0
	newSubmit := ""
	if !dialog.PromptManual(2, "New code", func(code string) { newSubmit = code }, nil) {
		t.Fatal("current generation install was rejected")
	}
	if dialog.PromptManual(1, "Old code", func(string) { oldSubmit++ }, func() { oldCancel++ }) {
		t.Fatal("stale generation install was accepted")
	}
	dialog.ClearManual(1)
	if dialog.manual == nil {
		t.Fatal("stale generation clear removed the current manual input")
	}
	dialog.HandleInput("CODE-2")
	dialog.HandleInput("\r")
	if newSubmit != "CODE-2" {
		t.Fatalf("current generation submit = %q, want CODE-2", newSubmit)
	}
	if oldSubmit != 0 || oldCancel != 0 {
		t.Fatalf("stale callbacks fired: submit=%d cancel=%d", oldSubmit, oldCancel)
	}
}

func TestLoginDialogSupersededComponentInputIgnored(t *testing.T) {
	theme := testDialogTheme(t)
	dialog := NewLoginDialog("Sign in", theme, nil)
	oldSubmit, oldCancel := 0, 0
	if !dialog.PromptManual(1, "Old code", func(string) { oldSubmit++ }, func() { oldCancel++ }) {
		t.Fatal("first generation install was rejected")
	}
	old := dialog.manual
	dialog.PromptManual(2, "New code", nil, nil)
	if dialog.manual == old {
		t.Fatal("superseding install did not replace the manual component")
	}
	old.HandleInput("\r")
	old.HandleInput("\x1b")
	if oldSubmit != 0 || oldCancel != 0 {
		t.Fatalf("superseded component callbacks fired: submit=%d cancel=%d", oldSubmit, oldCancel)
	}
	if dialog.manual == nil {
		t.Fatal("superseded component input cleared the current manual input")
	}
	dialog.SetFocused(true)
	dialog.HandleInput("x")
	if rendered := frameText(dialog.Render(50)); !strings.Contains(rendered, "•") {
		t.Fatalf("current manual input was not displayed after superseded input:\n%s", rendered)
	}
}
