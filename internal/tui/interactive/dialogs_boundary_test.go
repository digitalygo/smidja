package interactive

import (
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/tui"
)

func boundaryTheme() DialogTheme {
	return NewDialogTheme(nil)
}

func boundaryFrameText(lines []string) string {
	return strings.Join(lines, "\n")
}

func assertBoundaryClean(t *testing.T, frame string, visible []string, forbidden []string) {
	t.Helper()
	for _, bad := range forbidden {
		if bad != "" && strings.Contains(frame, bad) {
			t.Fatalf("frame leaked payload %q:\n%s", bad, frame)
		}
	}
	stripped := tui.StripTerminalSequences(frame)
	if strings.Contains(stripped, "\x1b") {
		t.Fatalf("stripped frame leaked escape byte:\n%q", stripped)
	}
	for _, r := range stripped {
		if r < 0x20 && r != '\n' {
			t.Fatalf("stripped frame leaked C0 %U:\n%q", r, stripped)
		}
		if r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			t.Fatalf("stripped frame leaked control %U:\n%q", r, stripped)
		}
	}
	for _, bad := range forbidden {
		if bad != "" && strings.Contains(stripped, bad) {
			t.Fatalf("stripped frame leaked payload %q:\n%s", bad, stripped)
		}
	}
	for _, want := range visible {
		if want != "" && !strings.Contains(stripped, want) {
			t.Fatalf("frame missing visible %q:\n%s", want, stripped)
		}
	}
}

func TestBoundaryConfirmHostile(t *testing.T) {
	cases := []struct {
		name      string
		title     string
		message   string
		visible   []string
		forbidden []string
	}{
		{name: "csi", title: "T\x1b[2JVISIBLE_TITLE", message: "M\x1b[31mVISIBLE_MSG\x1b[0m", visible: []string{"VISIBLE_TITLE", "VISIBLE_MSG"}, forbidden: []string{"[2J", "[31m"}},
		{name: "osc", title: "T\x1b]0;OSC_EVIL_TITLE_999\x07VISIBLE_TITLE", message: "M\x1b]0;OSC_EVIL_MSG_999\x07VISIBLE_MSG", visible: []string{"VISIBLE_TITLE", "VISIBLE_MSG"}, forbidden: []string{"OSC_EVIL_TITLE_999", "OSC_EVIL_MSG_999"}},
		{name: "apc", title: "T\x1b_APP_EVIL_999\x1b\\VISIBLE_TITLE", message: "M\x1b_APP_EVIL_MSG_999\x1b\\VISIBLE_MSG", visible: []string{"VISIBLE_TITLE", "VISIBLE_MSG"}, forbidden: []string{"APP_EVIL_999", "APP_EVIL_MSG_999"}},
		{name: "dcs", title: "T\x1bP1$rDCS_EVIL_999\x1b\\VISIBLE_TITLE", message: "M\x1bP1$rDCS_EVIL_MSG_999\x1b\\VISIBLE_MSG", visible: []string{"VISIBLE_TITLE", "VISIBLE_MSG"}, forbidden: []string{"DCS_EVIL_999", "DCS_EVIL_MSG_999"}},
		{name: "c0", title: "T\x00VISIBLE_TITLE\x07", message: "M\x01VISIBLE_MSG\x1f", visible: []string{"VISIBLE_TITLE", "VISIBLE_MSG"}, forbidden: []string{}},
		{name: "c1", title: "T  VISIBLE_TITLE", message: "MVISIBLE_MSG", visible: []string{"VISIBLE_TITLE", "VISIBLE_MSG"}, forbidden: []string{}},
		{name: "c1csi", title: "T2JVISIBLE_TITLE", message: "M2JVISIBLE_MSG", visible: []string{"VISIBLE_TITLE", "VISIBLE_MSG"}, forbidden: []string{}},
		{name: "newline-collapse", title: "A\nB\n\nVISIBLE_TITLE", message: "line1\nline2", visible: []string{"A", "VISIBLE_TITLE", "line1"}, forbidden: []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			theme := boundaryTheme()
			dialog := NewConfirmDialog(tc.title, tc.message, theme, func(bool) {})
			frame := boundaryFrameText(dialog.Render(60))
			assertBoundaryClean(t, frame, tc.visible, tc.forbidden)
		})
	}
}

func TestBoundaryTextHostile(t *testing.T) {
	cases := []struct {
		name      string
		title     string
		body      []string
		visible   []string
		forbidden []string
	}{
		{name: "csi", title: "T\x1b[2JVISIBLE", body: []string{"\x1b[31mVISIBLE_BODY\x1b[0m"}, visible: []string{"VISIBLE"}, forbidden: []string{"[2J"}},
		{name: "osc", title: "T\x1b]0;OSC_EVIL_T_111\x07VISIBLE", body: []string{"a\x1b]0;OSC_EVIL_B_111\x07bVISIBLE_BODY"}, visible: []string{"VISIBLE_BODY"}, forbidden: []string{"OSC_EVIL_T_111", "OSC_EVIL_B_111"}},
		{name: "apc-dcs", title: "T\x1b_APP_EVIL_222\x1b\\VISIBLE", body: []string{"x\x1bPqDCS_EVIL_222\x1b\\yVISIBLE_BODY"}, visible: []string{"VISIBLE_BODY"}, forbidden: []string{"APP_EVIL_222", "DCS_EVIL_222"}},
		{name: "c0c1", title: "T\x00\x07VISIBLE", body: []string{"a\x00bcVISIBLE_BODY"}, visible: []string{"VISIBLE_BODY"}, forbidden: []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			theme := boundaryTheme()
			dialog := NewTextDialog(tc.title, tc.body, "hint\x1b[2J", theme, func() {})
			frame := boundaryFrameText(dialog.Render(60))
			assertBoundaryClean(t, frame, tc.visible, tc.forbidden)
		})
	}
}

func TestBoundaryInputHostile(t *testing.T) {
	cases := []struct {
		name        string
		title       string
		placeholder string
		visible     []string
		forbidden   []string
	}{
		{name: "csi", title: "T\x1b[2JVISIBLE", placeholder: "P\x1b[31mVISIBLE_PH\x1b[0m", visible: []string{"VISIBLE_PH"}, forbidden: []string{"[2J"}},
		{name: "osc", title: "T\x1b]0;OSC_EVIL_333\x07VISIBLE", placeholder: "P\x1b]0;OSC_EVIL_PH_333\x07VISIBLE_PH", visible: []string{"VISIBLE_PH"}, forbidden: []string{"OSC_EVIL_333", "OSC_EVIL_PH_333"}},
		{name: "apc", title: "T\x1b_APP_444\x1b\\VISIBLE", placeholder: "P\x1b_APP_PH_444\x1b\\VISIBLE_PH", visible: []string{"VISIBLE_PH"}, forbidden: []string{"APP_444", "APP_PH_444"}},
		{name: "dcs", title: "T\x1bPqDCS_555\x1b\\VISIBLE", placeholder: "P\x1bPqDCS_PH_555\x1b\\VISIBLE_PH", visible: []string{"VISIBLE_PH"}, forbidden: []string{"DCS_555", "DCS_PH_555"}},
		{name: "c0", title: "T\x00VISIBLE", placeholder: "P\x07VISIBLE_PH", visible: []string{"VISIBLE_PH"}, forbidden: []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			theme := boundaryTheme()
			var submitted string
			dialog := NewInputDialog(tc.title, tc.placeholder, theme, func(v string) { submitted = v }, func() {})
			frame := boundaryFrameText(dialog.Render(60))
			assertBoundaryClean(t, frame, tc.visible, tc.forbidden)
			dialog.HandleInput("ok")
			dialog.HandleInput("\r")
			if submitted != "ok" {
				t.Fatalf("input result = %q, want ok", submitted)
			}
		})
	}
}

func TestBoundaryInputValueDisplayPreservesRaw(t *testing.T) {
	theme := boundaryTheme()
	dialog := NewInputDialog("Title", "ph", theme, func(string) {}, func() {})
	hostile := "a\x1b[2Jb\x1b]0;OSC_EVIL_VAL_666\x07c\x00d"
	dialog.input.SetValue(hostile)
	if got := dialog.input.Value(); got != hostile {
		t.Fatalf("input raw = %q, want exact hostile", got)
	}
	frame := boundaryFrameText(dialog.Render(60))
	assertBoundaryClean(t, frame, []string{"a", "b", "c", "d"}, []string{"OSC_EVIL_VAL_666", "[2J"})
}

func TestBoundaryMaskedTitleHostile(t *testing.T) {
	cases := []struct {
		name      string
		title     string
		forbidden []string
	}{
		{name: "csi", title: "T\x1b[2JVISIBLE", forbidden: []string{"[2J"}},
		{name: "osc", title: "T\x1b]0;OSC_EVIL_M_777\x07VISIBLE", forbidden: []string{"OSC_EVIL_M_777"}},
		{name: "apc", title: "T\x1b_APP_M_777\x1b\\VISIBLE", forbidden: []string{"APP_M_777"}},
		{name: "dcs", title: "T\x1bPqDCS_M_777\x1b\\VISIBLE", forbidden: []string{"DCS_M_777"}},
		{name: "c0", title: "T\x00VISIBLE\x07", forbidden: []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			theme := boundaryTheme()
			dialog := NewMaskedInput(tc.title, theme, func(string) {}, func() {})
			dialog.SetFocused(true)
			frame := boundaryFrameText(dialog.Render(60))
			assertBoundaryClean(t, frame, []string{"VISIBLE"}, tc.forbidden)
		})
	}
}

func TestBoundarySelectHostile(t *testing.T) {
	hostiles := []struct {
		name      string
		label     string
		desc      string
		value     string
		visible   []string
		forbidden []string
	}{
		{name: "csi", label: "L\x1b[2JVISIBLE_L", desc: "D\x1b[31mVISIBLE_D\x1b[0m", value: "raw-id-\x1b[2J-1", visible: []string{"VISIBLE_L", "VISIBLE_D"}, forbidden: []string{"[2J"}},
		{name: "osc", label: "L\x1b]0;OSC_EVIL_L_888\x07VISIBLE_L", desc: "D\x1b]0;OSC_EVIL_D_888\x07VISIBLE_D", value: "raw-id-osc-2", visible: []string{"VISIBLE_L", "VISIBLE_D"}, forbidden: []string{"OSC_EVIL_L_888", "OSC_EVIL_D_888"}},
		{name: "apc", label: "L\x1b_APP_L_888\x1b\\VISIBLE_L", desc: "D\x1b_APP_D_888\x1b\\VISIBLE_D", value: "raw-id-apc-3", visible: []string{"VISIBLE_L", "VISIBLE_D"}, forbidden: []string{"APP_L_888", "APP_D_888"}},
		{name: "dcs", label: "L\x1bPqDCS_L_888\x1b\\VISIBLE_L", desc: "D\x1bPqDCS_D_888\x1b\\VISIBLE_D", value: "raw-id-dcs-4", visible: []string{"VISIBLE_L", "VISIBLE_D"}, forbidden: []string{"DCS_L_888", "DCS_D_888"}},
		{name: "c0c1", label: "L\x00VISIBLE_L\x07", desc: "DVISIBLE_D", value: "raw-id-c0-5", visible: []string{"VISIBLE_L", "VISIBLE_D"}, forbidden: []string{}},
	}
	for _, tc := range hostiles {
		t.Run(tc.name, func(t *testing.T) {
			theme := boundaryTheme()
			items := []tui.SelectItem{{Value: tc.value, Label: tc.label, Description: tc.desc}}
			var selected string
			dialog := NewSelectDialog(SelectDialogOptions{Title: "T\x1b[2JVISIBLE_T", Placeholder: "P\x1b]0;OSC_EVIL_PH_999\x07VISIBLE_PH", Items: items, Searchable: true}, theme, func(v string) { selected = v }, func() {})
			frame := boundaryFrameText(dialog.Render(60))
			assertBoundaryClean(t, frame, tc.visible, tc.forbidden)
			if strings.Contains(frame, "OSC_EVIL_PH_999") {
				t.Fatalf("placeholder payload leaked:\n%s", frame)
			}
			dialog.HandleInput("\r")
			if selected != tc.value {
				t.Fatalf("selected = %q, want exact raw %q", selected, tc.value)
			}
		})
	}
}

func TestBoundarySettingsHostile(t *testing.T) {
	hostiles := []struct {
		name      string
		label     string
		value     string
		desc      string
		visible   []string
		forbidden []string
	}{
		{name: "csi", label: "L\x1b[2JVISIBLE_L", value: "V\x1b[31mVISIBLE_V", desc: "D\x1b[2JVISIBLE_D", visible: []string{"VISIBLE_L", "VISIBLE_V", "VISIBLE_D"}, forbidden: []string{"[2J"}},
		{name: "osc", label: "L\x1b]0;OSC_EVIL_L_101\x07VISIBLE_L", value: "V\x1b]0;OSC_EVIL_V_101\x07VISIBLE_V", desc: "D\x1b]0;OSC_EVIL_D_101\x07VISIBLE_D", visible: []string{"VISIBLE_L", "VISIBLE_V", "VISIBLE_D"}, forbidden: []string{"OSC_EVIL_L_101", "OSC_EVIL_V_101", "OSC_EVIL_D_101"}},
		{name: "apc", label: "L\x1b_APP_L_102\x1b\\VISIBLE_L", value: "V\x1b_APP_V_102\x1b\\VISIBLE_V", desc: "D\x1b_APP_D_102\x1b\\VISIBLE_D", visible: []string{"VISIBLE_L", "VISIBLE_V", "VISIBLE_D"}, forbidden: []string{"APP_L_102", "APP_V_102", "APP_D_102"}},
		{name: "dcs", label: "L\x1bPqDCS_L_103\x1b\\VISIBLE_L", value: "V\x1bPqDCS_V_103\x1b\\VISIBLE_V", desc: "D\x1bPqDCS_D_103\x1b\\VISIBLE_D", visible: []string{"VISIBLE_L", "VISIBLE_V", "VISIBLE_D"}, forbidden: []string{"DCS_L_103", "DCS_V_103", "DCS_D_103"}},
		{name: "c0", label: "L\x00VISIBLE_L", value: "V\x07VISIBLE_V", desc: "D\x01VISIBLE_D", visible: []string{"VISIBLE_L", "VISIBLE_V", "VISIBLE_D"}, forbidden: []string{}},
	}
	for _, tc := range hostiles {
		t.Run(tc.name, func(t *testing.T) {
			theme := boundaryTheme()
			items := []tui.SettingItem{{ID: "id-" + tc.name, Label: tc.label, CurrentValue: tc.value, Description: tc.desc, Values: []string{tc.value, "other"}}}
			var applied map[string]string
			dialog := NewSettingsDialog("T\x1b[2JVISIBLE_T", items, theme, func(v map[string]string) { applied = v }, func() {})
			frame := boundaryFrameText(dialog.Render(70))
			assertBoundaryClean(t, frame, tc.visible, tc.forbidden)
			dialog.HandleInput("\r")
			dialog.HandleInput("\x13")
			if applied["id-"+tc.name] != "other" {
				t.Fatalf("applied = %q, want other", applied["id-"+tc.name])
			}
		})
	}
}

func TestBoundaryEditorHostile(t *testing.T) {
	cases := []struct {
		name      string
		title     string
		prefill   string
		visible   []string
		forbidden []string
	}{
		{name: "csi", title: "T\x1b[2JVISIBLE_T", prefill: "a\x1b[2JbVISIBLE_P", visible: []string{"VISIBLE_P"}, forbidden: []string{"[2J"}},
		{name: "osc", title: "T\x1b]0;OSC_EVIL_T_201\x07VISIBLE_T", prefill: "a\x1b]0;OSC_EVIL_P_201\x07bVISIBLE_P", visible: []string{"VISIBLE_P"}, forbidden: []string{"OSC_EVIL_T_201", "OSC_EVIL_P_201"}},
		{name: "apc", title: "T\x1b_APP_T_202\x1b\\VISIBLE_T", prefill: "a\x1b_APP_P_202\x1b\\bVISIBLE_P", visible: []string{"VISIBLE_P"}, forbidden: []string{"APP_T_202", "APP_P_202"}},
		{name: "dcs", title: "T\x1bPqDCS_T_203\x1b\\VISIBLE_T", prefill: "a\x1bPqDCS_P_203\x1b\\bVISIBLE_P", visible: []string{"VISIBLE_P"}, forbidden: []string{"DCS_T_203", "DCS_P_203"}},
		{name: "c0", title: "T\x00VISIBLE_T", prefill: "  ordinary prefill", visible: []string{"ordinary"}, forbidden: []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			theme := boundaryTheme()
			var submitted string
			dialog := NewEditorDialog(tc.title, tc.prefill, theme, func(v string) { submitted = v }, func() {})
			frame := boundaryFrameText(dialog.Render(70))
			assertBoundaryClean(t, frame, tc.visible, tc.forbidden)
			dialog.HandleInput("\r")
			if submitted != tc.prefill {
				t.Fatalf("editor result = %q, want exact raw %q", submitted, tc.prefill)
			}
		})
	}
}

func TestBoundaryTrustedANSIPreserved(t *testing.T) {
	registry := tui.NewThemeRegistry("", "", tui.ColorModeUnset)
	real, err := registry.SetTheme("dark")
	if err != nil {
		t.Fatalf("SetTheme: %v", err)
	}
	theme := NewDialogTheme(real)
	dialog := NewConfirmDialog("Title", "Message", theme, func(bool) {})
	frame := strings.Join(dialog.Render(40), "\n")
	if !strings.Contains(frame, "\x1b[") {
		t.Fatalf("trusted theme ANSI was stripped:\n%q", frame)
	}
	if !strings.Contains(tui.StripTerminalSequences(frame), "Title") {
		t.Fatalf("trusted content missing after strip:\n%q", frame)
	}
}
