package interactive

import (
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/tui"
)

func testDialogTheme(t *testing.T) DialogTheme {
	t.Helper()
	registry := tui.NewThemeRegistry("", "", tui.ColorModeUnset)
	theme, err := registry.SetTheme("dark")
	if err != nil {
		t.Fatalf("SetTheme: %v", err)
	}
	return NewDialogTheme(theme)
}

func frameText(lines []string) string {
	stripped := make([]string, 0, len(lines))
	for _, line := range lines {
		stripped = append(stripped, tui.StripTerminalSequences(line))
	}
	return strings.Join(stripped, "\n")
}

func TestDialogThemeNilUsesIdentity(t *testing.T) {
	theme := NewDialogTheme(nil)
	if theme.Accent("accent") != "accent" || theme.Border("border") != "border" {
		t.Fatal("nil theme must keep text unchanged")
	}
	if theme.Base != nil {
		t.Fatal("nil theme must not set Base")
	}
}

func TestRenderDialogFrameWidths(t *testing.T) {
	theme := testDialogTheme(t)
	for _, width := range []int{2, 6, 20, 60} {
		lines := renderDialogFrame("Title", "hint", []string{"body line"}, width, theme)
		if len(lines) != 5 {
			t.Fatalf("width %d produced %d lines, want 5", width, len(lines))
		}
		for _, line := range lines {
			if got := tui.VisibleWidth(line); got > maxInt(width, 6) {
				t.Fatalf("width %d line exceeds box width: %d %q", width, got, line)
			}
		}
	}
}

func TestConfirmDialogInputs(t *testing.T) {
	theme := testDialogTheme(t)
	cases := []struct {
		name   string
		input  string
		result bool
	}{
		{name: "enter", input: "\r", result: true},
		{name: "y", input: "y", result: true},
		{name: "n", input: "n", result: false},
		{name: "escape", input: "\x1b", result: false},
		{name: "ctrl+c", input: "\x03", result: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got bool
			var calls int
			dialog := NewConfirmDialog("Title", "Message", theme, func(value bool) {
				calls++
				got = value
			})
			dialog.SetHint("custom hint")
			dialog.SetFocused(true)
			dialog.SetTheme(theme)
			dialog.Invalidate()
			rendered := frameText(dialog.Render(40))
			if !strings.Contains(rendered, "Title") || !strings.Contains(rendered, "Message") || !strings.Contains(rendered, "custom hint") {
				t.Fatalf("render missing content:\n%s", rendered)
			}
			dialog.HandleInput("z")
			dialog.HandleInput(tc.input)
			dialog.HandleInput(tc.input)
			if calls != 1 {
				t.Fatalf("callback calls = %d, want 1", calls)
			}
			if got != tc.result {
				t.Fatalf("result = %v, want %v", got, tc.result)
			}
		})
	}
}

func TestTextDialogClosesOnce(t *testing.T) {
	theme := testDialogTheme(t)
	calls := 0
	dialog := NewTextDialog("Info", []string{"line one", "line two"}, "press enter", theme, func() { calls++ })
	dialog.SetFocused(true)
	dialog.SetTheme(theme)
	dialog.Invalidate()
	if rendered := frameText(dialog.Render(40)); !strings.Contains(rendered, "line one") {
		t.Fatalf("render missing body:\n%s", rendered)
	}
	dialog.HandleInput("x")
	dialog.HandleInput("\r")
	dialog.HandleInput("\x1b")
	if calls != 1 {
		t.Fatalf("close calls = %d, want 1", calls)
	}
}

func TestInputDialogRoundTrip(t *testing.T) {
	theme := testDialogTheme(t)
	var submitted string
	cancelled := false
	dialog := NewInputDialog("Name", "type here", theme, func(value string) { submitted = value }, func() { cancelled = true })
	dialog.SetFocused(true)
	dialog.SetTheme(theme)
	dialog.Invalidate()
	if rendered := frameText(dialog.Render(40)); !strings.Contains(rendered, "Name") {
		t.Fatalf("render missing title:\n%s", rendered)
	}
	dialog.HandleInput("h")
	dialog.HandleInput("i")
	dialog.HandleInput("\r")
	if submitted != "hi" {
		t.Fatalf("submitted = %q, want hi", submitted)
	}
	if cancelled {
		t.Fatal("input dialog reported cancel after submit")
	}
	cancelDialog := NewInputDialog("Name", "", theme, func(string) {}, func() { cancelled = true })
	cancelDialog.HandleInput("\x1b")
	if !cancelled {
		t.Fatal("input dialog did not cancel")
	}
}

func TestMaskedInputEditing(t *testing.T) {
	theme := testDialogTheme(t)
	var submitted string
	cancelled := false
	dialog := NewMaskedInput("Token", theme, func(value string) { submitted = value }, func() { cancelled = true })
	dialog.SetFocused(true)
	dialog.SetTheme(theme)
	dialog.Invalidate()
	dialog.HandleInput("a")
	dialog.HandleInput("b")
	dialog.HandleInput("c")
	dialog.HandleInput("\x1b[D")
	dialog.HandleInput("X")
	if dialog.Value() != "abXc" {
		t.Fatalf("Value = %q, want abXc", dialog.Value())
	}
	dialog.HandleInput("\x7f")
	dialog.HandleInput("\x1b[C")
	dialog.HandleInput("\x1b[3~")
	rendered := frameText(dialog.Render(40))
	if strings.Contains(rendered, "abXc") || strings.Contains(rendered, "X") {
		t.Fatalf("masked render leaked characters:\n%s", rendered)
	}
	if !strings.Contains(rendered, "•") {
		t.Fatalf("masked render missing bullets:\n%s", rendered)
	}
	dialog.HandleInput("\x1b[H")
	dialog.HandleInput("\x1b[F")
	dialog.HandleInput("\r")
	if submitted != dialog.Value() {
		t.Fatalf("submitted = %q, want %q", submitted, dialog.Value())
	}
	dialog.HandleInput("late")
	if dialog.Value() != submitted {
		t.Fatal("settled masked input accepted more characters")
	}
	cancelDialog := NewMaskedInput("Token", theme, func(string) {}, func() { cancelled = true })
	cancelDialog.HandleInput("\x03")
	if !cancelled {
		t.Fatal("masked input did not cancel")
	}
}

func TestMaskedInputPasteAndScrolling(t *testing.T) {
	theme := testDialogTheme(t)
	var submitted string
	dialog := NewMaskedInput("Token", theme, func(value string) { submitted = value }, func() {})
	dialog.HandleInput(tui.BracketedPasteStart + "secret\nwith\rjunk" + tui.BracketedPasteEnd)
	if dialog.Value() != "secretwithjunk" {
		t.Fatalf("pasted value = %q", dialog.Value())
	}
	if strings.Contains(frameText(dialog.Render(20)), "secretwithjunk") {
		t.Fatal("paste leaked into render")
	}
	for i := 0; i < 40; i++ {
		dialog.HandleInput("x")
	}
	rendered := frameText(dialog.Render(12))
	if !strings.Contains(rendered, "•") {
		t.Fatalf("scrolled render missing bullets:\n%s", rendered)
	}
	dialog.HandleInput("\r")
	if len(submitted) != 54 {
		t.Fatalf("submitted length = %d, want 54", len(submitted))
	}
}

func TestSelectDialogNavigation(t *testing.T) {
	theme := testDialogTheme(t)
	items := []tui.SelectItem{{Value: "alpha", Label: "alpha"}, {Value: "beta", Label: "beta"}, {Value: "gamma", Label: "gamma"}}
	var selected string
	cancelled := false
	dialog := NewSelectDialog(SelectDialogOptions{Title: "Pick", Items: items, MaxVisible: 2, Searchable: true}, theme,
		func(value string) { selected = value }, func() { cancelled = true })
	dialog.SetFocused(true)
	dialog.SetTheme(theme)
	dialog.Invalidate()
	if rendered := frameText(dialog.Render(50)); !strings.Contains(rendered, "Pick") {
		t.Fatalf("render missing title:\n%s", rendered)
	}
	dialog.HandleInput("\x1b[B")
	dialog.HandleInput("\r")
	if selected != "beta" {
		t.Fatalf("selected = %q, want beta", selected)
	}
	searchDialog := NewSelectDialog(SelectDialogOptions{Title: "Pick", Items: items, Searchable: true}, theme,
		func(value string) { selected = value }, func() { cancelled = true })
	searchDialog.HandleInput("ga")
	searchDialog.HandleInput("\r")
	if selected != "gamma" {
		t.Fatalf("search selected = %q, want gamma", selected)
	}
	cancelDialog := NewSelectDialog(SelectDialogOptions{Title: "Pick", Items: items}, theme, func(string) {}, func() { cancelled = true })
	cancelDialog.HandleInput("\x1b")
	if !cancelled {
		t.Fatal("select dialog did not cancel")
	}
	cancelDialog.HandleInput("\x1b")
	if !cancelled {
		t.Fatal("select dialog cancel regression")
	}
	initial := NewSelectDialog(SelectDialogOptions{Title: "Pick", Items: items, InitialValue: "gamma"}, theme, func(value string) { selected = value }, func() {})
	initial.HandleInput("\r")
	if selected != "gamma" {
		t.Fatalf("initial selection = %q, want gamma", selected)
	}
}

func TestSettingsDialogApplyAndCancel(t *testing.T) {
	theme := testDialogTheme(t)
	items := []tui.SettingItem{{ID: "retry", Label: "Retry", Values: []string{"on", "off"}, CurrentValue: "on"}}
	var applied map[string]string
	cancelled := false
	dialog := NewSettingsDialog("Settings", items, theme, func(values map[string]string) { applied = values }, func() { cancelled = true })
	dialog.SetFocused(true)
	dialog.SetTheme(theme)
	dialog.Invalidate()
	if rendered := frameText(dialog.Render(50)); !strings.Contains(rendered, "Retry") {
		t.Fatalf("render missing setting:\n%s", rendered)
	}
	dialog.HandleInput("\r")
	dialog.HandleInput("\x13")
	if applied["retry"] != "off" {
		t.Fatalf("applied retry = %q, want off", applied["retry"])
	}
	dialog.HandleInput("\x13")
	if applied["retry"] != "" && applied["retry"] != "off" {
		t.Fatalf("late apply changed the draft: %q", applied["retry"])
	}
	cancelDialog := NewSettingsDialog("Settings", items, theme, func(map[string]string) {}, func() { cancelled = true })
	cancelDialog.HandleInput("\x1b")
	if !cancelled {
		t.Fatal("settings dialog did not cancel")
	}
}

func TestEditorDialogSubmitAndCancel(t *testing.T) {
	theme := testDialogTheme(t)
	var submitted string
	cancelled := false
	dialog := NewEditorDialog("Edit", "  prefill", theme, func(value string) { submitted = value }, func() { cancelled = true })
	dialog.SetFocused(true)
	dialog.SetTheme(theme)
	dialog.Invalidate()
	if rendered := frameText(dialog.Render(60)); !strings.Contains(rendered, "Edit") {
		t.Fatalf("render missing title:\n%s", rendered)
	}
	dialog.HandleInput("x")
	dialog.HandleInput("\r")
	if submitted != "  prefillx" {
		t.Fatalf("submitted = %q, want preserved prefill", submitted)
	}
	cancelDialog := NewEditorDialog("Edit", "", theme, func(string) {}, func() { cancelled = true })
	cancelDialog.HandleInput("\x1b")
	if !cancelled {
		t.Fatal("editor dialog did not cancel")
	}
}

func TestRethemeBlocksAndChrome(t *testing.T) {
	theme := testDialogTheme(t)
	next := testDialogTheme(t)
	registry := tui.NewThemeRegistry("", "", tui.ColorModeUnset)
	light, err := registry.SetTheme("light")
	if err != nil {
		t.Fatalf("SetTheme: %v", err)
	}
	if NewDialogTheme(light).Border("x") == "" {
		t.Fatal("light theme border did not render")
	}

	markdown := NewMarkdown("text", 0, 0, theme.Base, MarkdownStyle{}, false)
	markdown.SetTheme(light)
	if markdown.theme != light {
		t.Fatal("markdown theme was not updated")
	}
	user := NewUserMessage("hello", theme.Base, false)
	user.SetTheme(light)
	if user.theme != light || user.markdown.theme != light {
		t.Fatal("user message theme was not updated")
	}
	notice := NewNotice(NoticeInfo, "note", theme.Base)
	notice.SetTheme(light)
	if notice.theme != light {
		t.Fatal("notice theme was not updated")
	}
	skill := NewSkillBlock("skill", "content", theme.Base, false, "")
	skill.SetTheme(light)
	if skill.theme != light {
		t.Fatal("skill block theme was not updated")
	}
	compaction := NewCompactionBlock("summary", 10, theme.Base, false, "")
	compaction.SetTheme(light)
	tool := NewToolExecution("read", nil, theme.Base, "", nil)
	tool.SetTheme(light)
	if tool.theme != light {
		t.Fatal("tool block theme was not updated")
	}
	subagent := NewSubagentBlock("agent", theme.Base, "", nil)
	subagent.SetTheme(light)
	if subagent.theme != light {
		t.Fatal("subagent block theme was not updated")
	}
	bash := NewBashExecution("ls", theme.Base, "", "", &tui.LoaderIndicator{}, nil, nil)
	bash.SetTheme(light)
	if bash.theme != light {
		t.Fatal("bash block theme was not updated")
	}
	footer := NewFooter(theme.Base, nil, "")
	footer.SetTheme(light)
	if footer.theme != light {
		t.Fatal("footer theme was not updated")
	}
	status := &StatusIndicator{}
	status.SetTheme(light)
	if status.theme != light {
		t.Fatal("status theme was not updated")
	}
	widgets := NewWidgetPanel()
	widgets.SetTheme(light)
	applyComponentTheme(user, light)
	_ = next
}
