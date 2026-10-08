package tui

import (
	"strings"
	"testing"
)

func TestVisibleWidthPlain(t *testing.T) {
	tests := []struct {
		name string
		text string
		want int
	}{
		{name: "empty", text: "", want: 0},
		{name: "ascii", text: "hello world", want: 11},
		{name: "tabs count as three", text: "a\tb", want: 5},
		{name: "sgr resets ignored", text: "\x1b[31mred\x1b[0m", want: 3},
		{name: "osc8 link ignored", text: "\x1b]8;;https://x\x07link\x1b]8;;\x07", want: 4},
		{name: "cursor marker ignored", text: "\x1b_pi:c\x07x", want: 1},
		{name: "cjk wide", text: "你好", want: 4},
		{name: "hangul wide", text: "한글", want: 4},
		{name: "fullwidth forms", text: "ａｂ", want: 4},
		{name: "combining acute", text: "e\u0301", want: 1},
		{name: "variation selector emoji", text: "✌️", want: 2},
		{name: "zwj family", text: "👨‍👩‍👧", want: 2},
		{name: "regional flag", text: "🇺🇸", want: 2},
		{name: "lone regional indicator", text: "🇦", want: 2},
		{name: "mixed", text: "a你b", want: 4},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := VisibleWidth(test.text); got != test.want {
				t.Fatalf("VisibleWidth(%q) = %d, want %d", test.text, got, test.want)
			}
		})
	}
}

func TestStripTerminalSequences(t *testing.T) {
	if got := StripTerminalSequences("\x1b[1mhi\x1b[0m\x1b]8;;u\x07!\x1b]8;;\x07"); got != "hi!" {
		t.Fatalf("StripTerminalSequences = %q", got)
	}
	if got := StripTerminalSequences("plain"); got != "plain" {
		t.Fatalf("plain strip = %q", got)
	}
}

func TestTruncateToWidth(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		width    int
		ellipsis string
		pad      bool
		want     string
	}{
		{name: "zero width", text: "abc", width: 0, ellipsis: "", pad: false, want: ""},
		{name: "no truncation", text: "abc", width: 5, ellipsis: "...", pad: false, want: "abc"},
		{name: "no truncation padded", text: "abc", width: 5, ellipsis: "...", pad: true, want: "abc  "},
		{name: "ascii truncated", text: "abcdef", width: 5, ellipsis: "...", pad: false, want: "ab" + SGRReset + "..." + SGRReset},
		{name: "ascii truncated padded", text: "abcdef", width: 7, ellipsis: "...", pad: true, want: "abcdef "},
		{name: "cjk truncated", text: "你好世界", width: 5, ellipsis: "…", pad: false, want: "你好" + SGRReset + "…" + SGRReset},
		{name: "cjk boundary", text: "你好", width: 3, ellipsis: "...", pad: false, want: SGRReset + "..." + SGRReset},
		{name: "ellipsis wider than width", text: "abcdef", width: 2, ellipsis: "...", pad: false, want: SGRReset + ".." + SGRReset},
		{name: "styled closes", text: "\x1b[31mabcdef\x1b[0m", width: 5, ellipsis: "...", pad: false, want: "\x1b[31mab\x1b[0m...\x1b[0m"},
		{name: "empty padded", text: "", width: 3, ellipsis: "...", pad: true, want: "   "},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := TruncateToWidth(test.text, test.width, test.ellipsis, test.pad)
			if got != test.want {
				t.Fatalf("TruncateToWidth(%q, %d, %q, %v) = %q, want %q", test.text, test.width, test.ellipsis, test.pad, got, test.want)
			}
			if visible := VisibleWidth(got); test.width > 0 && visible > test.width {
				t.Fatalf("result %q wider than %d: %d", got, test.width, visible)
			}
		})
	}
}

func TestTruncateClosesHyperlink(t *testing.T) {
	text := "\x1b]8;;https://example.com\x07abcdefgh\x1b]8;;\x07"
	got := TruncateToWidth(text, 5, "...", false)
	if !strings.Contains(got, OSC8Close) {
		t.Fatalf("truncated link does not close hyperlink: %q", got)
	}
	if VisibleWidth(got) != 5 {
		t.Fatalf("truncated link width = %d, want 5", VisibleWidth(got))
	}
}

func TestSliceByColumn(t *testing.T) {
	tests := []struct {
		name   string
		line   string
		start  int
		length int
		strict bool
		want   string
	}{
		{name: "plain middle", line: "abcdef", start: 1, length: 3, strict: true, want: "bcd"},
		{name: "wide char boundary strict", line: "你好", start: 0, length: 3, strict: true, want: "你"},
		{name: "wide char boundary loose", line: "你好", start: 0, length: 3, strict: false, want: "你好"},
		{name: "styled preserved", line: "\x1b[31mabcdef\x1b[0m", start: 0, length: 3, strict: true, want: "\x1b[31mabc"},
		{name: "start beyond end", line: "abc", start: 5, length: 2, strict: true, want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := SliceByColumn(test.line, test.start, test.length, test.strict); got != test.want {
				t.Fatalf("SliceByColumn(%q, %d, %d, %v) = %q, want %q", test.line, test.start, test.length, test.strict, got, test.want)
			}
		})
	}
}

func TestWrapTextWithANSI(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		width int
		want  []string
	}{
		{name: "empty", text: "", width: 10, want: []string{""}},
		{name: "no wrap", text: "hello world", width: 20, want: []string{"hello world"}},
		{name: "word wrap", text: "hello world", width: 8, want: []string{"hello", "world"}},
		{name: "multiple spaces trimmed", text: "hello   world", width: 8, want: []string{"hello", "world"}},
		{name: "long word breaks", text: "abcdefghijkl", width: 5, want: []string{"abcde", "fghij", "kl"}},
		{name: "style carried to next line", text: "\x1b[31mhello world\x1b[0m", width: 6,
			want: []string{"\x1b[31mhello", "\x1b[31mworld\x1b[0m"}},
		{name: "cjk breaks anywhere", text: "你好世界", width: 3, want: []string{"你", "好", "世", "界"}},
		{name: "newline preserved", text: "ab\ncd", width: 10, want: []string{"ab", "cd"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := WrapTextWithANSI(test.text, test.width)
			if len(got) != len(test.want) {
				t.Fatalf("WrapTextWithANSI(%q, %d) = %q, want %q", test.text, test.width, got, test.want)
			}
			for i := range got {
				if got[i] != test.want[i] {
					t.Fatalf("WrapTextWithANSI(%q, %d) line %d = %q, want %q", test.text, test.width, i, got[i], test.want[i])
				}
			}
		})
	}
}

func TestWrapPreservesVisibleContent(t *testing.T) {
	text := "\x1b[1mthe quick brown fox jumps over the lazy dog\x1b[0m"
	lines := WrapTextWithANSI(text, 10)
	var parts []string
	for _, line := range lines {
		parts = append(parts, strings.TrimRight(StripTerminalSequences(line), " "))
	}
	joined := strings.Join(parts, " ")
	if joined != StripTerminalSequences(text) {
		t.Fatalf("wrapped content changed: %q vs %q", joined, StripTerminalSequences(text))
	}
	for _, line := range lines {
		if VisibleWidth(line) > 10 {
			t.Fatalf("wrapped line too wide: %q", line)
		}
	}
}

func TestApplyBackgroundToLine(t *testing.T) {
	calls := 0
	result := ApplyBackgroundToLine("\x1b[31mred\x1b[0m", 6, func(line string) string {
		calls++
		return "bg(" + line + ")"
	})
	if calls != 1 {
		t.Fatalf("bg called %d times", calls)
	}
	if !strings.HasPrefix(result, "bg(") || !strings.Contains(result, "   ") {
		t.Fatalf("ApplyBackgroundToLine = %q", result)
	}
}

func TestStyleTrackerState(t *testing.T) {
	tracker := &styleTracker{}
	updateTracker("\x1b[1;4;38;5;9mX", tracker)
	if !tracker.bold || !tracker.underline || tracker.fg != "38;5;9" {
		t.Fatalf("tracker state after bold+underline+fg: %+v", tracker)
	}
	codes := tracker.activeCodes()
	if !strings.Contains(codes, "\x1b[1;4;38;5;9m") {
		t.Fatalf("activeCodes = %q", codes)
	}
	updateTracker("\x1b[22;39m", tracker)
	if tracker.bold || tracker.fg != "" {
		t.Fatalf("tracker state after reset codes: %+v", tracker)
	}
}

func TestRuneWidthTables(t *testing.T) {
	wide := []rune{'你', '한', 0xFF01, '红', 0x1F600, 0x20000}
	for _, r := range wide {
		if runeWidth(r) != 2 {
			t.Errorf("runeWidth(U+%04X) = %d, want 2", r, runeWidth(r))
		}
	}
	narrow := []rune{'a', '0', '~', 0x00E9}
	for _, r := range narrow {
		if runeWidth(r) != 1 {
			t.Errorf("runeWidth(U+%04X) = %d, want 1", r, runeWidth(r))
		}
	}
	zero := []rune{0x0301, 0x200D, 0xFE0F, 0x200B, 0x0007, 0x1161}
	for _, r := range zero {
		if runeWidth(r) != 0 {
			t.Errorf("runeWidth(U+%04X) = %d, want 0", r, runeWidth(r))
		}
	}
}

func TestHexTo256Conversion(t *testing.T) {
	tests := []struct {
		hex  string
		want int
	}{
		{"#000000", 16},
		{"#ffffff", 231},
		{"#808080", 244},
		{"#ff0000", 196},
		{"#00ff00", 46},
	}
	for _, test := range tests {
		got, err := hexTo256(test.hex)
		if err != nil {
			t.Fatalf("hexTo256(%s) error = %v", test.hex, err)
		}
		if got != test.want {
			t.Errorf("hexTo256(%s) = %d, want %d", test.hex, got, test.want)
		}
	}
	if _, err := hexTo256("#12345"); err == nil {
		t.Error("hexTo256 with short hex should fail")
	}
}
