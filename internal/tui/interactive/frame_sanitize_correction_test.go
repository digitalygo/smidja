package interactive

import (
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/tui"
)

func assertNoRawTerminalControls(t *testing.T, cleaned string) {
	t.Helper()
	for _, forbidden := range []string{"\r", "\b", "\n", "\x1b[2J", "\x1b[2A", "\x1b[?1049", "\x1bP", "\x1b]52", "\x9b", "\x9d", "\u0085"} {
		if strings.Contains(cleaned, forbidden) {
			t.Fatalf("cleaned %q still contains raw %q", cleaned, forbidden)
		}
	}
}

func TestSanitizeComponentFrameRejectsOSC8ParamInjections(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		contains []string
		absent   []string
	}{
		{
			name:     "clear screen in params",
			input:    "\x1b]8;id=x\x1b[2J;https://example.com\x07label",
			contains: []string{"label"},
			absent:   []string{"https://example.com"},
		},
		{
			name:     "alt screen in params",
			input:    "\x1b]8;id=x\x1b[?1049h;https://example.com\x07label",
			contains: []string{"label"},
			absent:   []string{"https://example.com"},
		},
		{
			name:     "cursor movement in params",
			input:    "\x1b]8;id=x\x1b[2A;https://example.com\x07label",
			contains: []string{"label"},
			absent:   []string{"https://example.com"},
		},
		{
			name:     "device control string in params",
			input:    "\x1b]8;id=x\x1bPq;https://example.com\x07label",
			contains: []string{"label"},
			absent:   []string{"https://example.com"},
		},
		{
			name:     "clipboard sequence in params",
			input:    "\x1b]8;id=x\x1b]52;c;payload;https://example.com\x07label",
			contains: []string{"label"},
			absent:   []string{"https://example.com"},
		},
		{
			name:     "carriage return backspace and c1 in params",
			input:    "\x1b]8;id=x\r\n\b\u0085;https://example.com\x07label",
			contains: []string{"label"},
			absent:   []string{"https://example.com"},
		},
		{
			name:     "space in params",
			input:    "\x1b]8;id=x y;https://example.com\x07label",
			contains: []string{"label"},
			absent:   []string{"https://example.com"},
		},
		{
			name:     "oversized params",
			input:    "\x1b]8;id=" + strings.Repeat("a", 128) + ";https://example.com\x07label",
			contains: []string{"label"},
			absent:   []string{"https://example.com"},
		},
		{
			name:     "unterminated payload",
			input:    "\x1b]8;id=ok;https://example.com",
			contains: nil,
			absent:   []string{"https://example.com"},
		},
		{
			name:     "malformed params missing value",
			input:    "\x1b]8;broken;https://example.com\x07label",
			contains: []string{"label"},
			absent:   []string{"https://example.com"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			cleaned := SanitizeComponentFrame([]string{testCase.input})[0]
			assertNoRawTerminalControls(t, cleaned)
			for _, want := range testCase.contains {
				if !strings.Contains(cleaned, want) {
					t.Fatalf("cleaned %q does not contain %q", cleaned, want)
				}
			}
			for _, unwanted := range testCase.absent {
				if strings.Contains(cleaned, unwanted) {
					t.Fatalf("cleaned %q still contains %q", cleaned, unwanted)
				}
			}
		})
	}
}

func TestSanitizeComponentFrameCleansOSC8Targets(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		target string
	}{
		{
			name:   "clear screen in target",
			input:  "\x1b]8;;https://example.com/\x1b[2Jmore\x07label",
			target: "https://example.com/[2Jmore",
		},
		{
			name:   "alt screen in target",
			input:  "\x1b]8;;https://example.com/\x1b[?1049hmore\x07label",
			target: "https://example.com/[?1049hmore",
		},
		{
			name:   "movement in target",
			input:  "\x1b]8;;https://example.com/\x1b[2Amore\x07label",
			target: "https://example.com/[2Amore",
		},
		{
			name:   "device control string in target",
			input:  "\x1b]8;;https://example.com/\x1bPqmore\x07label",
			target: "https://example.com/Pqmore",
		},
		{
			name:   "clipboard sequence in target",
			input:  "\x1b]8;;https://example.com/\x1b]52;c;payload\x07label",
			target: "https://example.com/]52;c;payload",
		},
		{
			name:   "carriage return backspace and c1 in target",
			input:  "\x1b]8;;https://example.com/\r\n\b\u0085x\x07label",
			target: "https://example.com/x",
		},
		{
			name:   "control bearing target stays scheme valid after strip",
			input:  "\x1b]8;;https://example.com/\x1b[31mred\x07label",
			target: "https://example.com/[31mred",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			cleaned := SanitizeComponentFrame([]string{testCase.input})[0]
			assertNoRawTerminalControls(t, cleaned)
			want := tui.OSC8Hyperlink("", testCase.target) + "label"
			if cleaned != want {
				t.Fatalf("cleaned = %q, want %q", cleaned, want)
			}
		})
	}
}

func TestSanitizeComponentFrameKeepsLegitimateOSC8(t *testing.T) {
	open := tui.OSC8Hyperlink("id=abc-1.2_3", "https://example.com/a?b=c")
	input := open + "label" + tui.OSC8Close
	if got := SanitizeComponentFrame([]string{input})[0]; got != input {
		t.Fatalf("legitimate OSC8 changed: %q", got)
	}
	mailto := tui.OSC8Hyperlink("id=mail", "mailto:a@example.com")
	if got := SanitizeComponentFrame([]string{mailto + "mail" + tui.OSC8Close})[0]; got != mailto+"mail"+tui.OSC8Close {
		t.Fatalf("legitimate mailto OSC8 changed: %q", got)
	}
	stInput := "\x1b]8;id=7;https://example.com\x1b\\link\x1b]8;;\x1b\\"
	stWant := tui.OSC8Hyperlink("id=7", "https://example.com") + "link" + tui.OSC8Close
	if got := SanitizeComponentFrame([]string{stInput})[0]; got != stWant {
		t.Fatalf("st terminated OSC8 = %q, want %q", got, stWant)
	}
	plain := "héllo → 世界 \x1b[31mred\x1b[0m"
	if got := SanitizeComponentFrame([]string{plain})[0]; got != plain {
		t.Fatalf("legitimate frame changed: %q", got)
	}
}

func TestSetHiddenThinkingLabelSanitizesBeforeStorage(t *testing.T) {
	theme := mustTheme(t)
	surface, _ := newTestSurface(t, SurfaceOptions{Theme: theme, Home: "/home/tester"})
	surface.StartAssistantTurn()
	surface.AppendAssistantThinking("secret")
	surface.SetThinkingExpanded(false)
	surface.SetHiddenThinkingLabel("first\nsecond\x1b[31m\x07third")
	if surface.ext.hiddenThinkingLabel != "first secondthird" {
		t.Fatalf("stored hidden label = %q, want %q", surface.ext.hiddenThinkingLabel, "first secondthird")
	}
	lines := renderPlainFrame(surface, 60, 20)
	if !strings.Contains(strings.Join(lines, "\n"), "first secondthird") {
		t.Fatalf("sanitized hidden label missing:\n%s", strings.Join(lines, "\n"))
	}
}
