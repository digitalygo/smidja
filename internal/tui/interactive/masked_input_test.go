package interactive

import (
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/tui"
)

func maskedBodyLine(lines []string) string {
	for _, line := range lines {
		stripped := tui.StripTerminalSequences(line)
		if index := strings.Index(stripped, "> "); index >= 0 {
			return stripped[index+2:]
		}
	}
	return ""
}

func TestMaskedInputUnicodeCursorAndDelete(t *testing.T) {
	theme := testDialogTheme(t)
	var submitted string
	dialog := NewMaskedInput("Token", theme, func(value string) { submitted = value }, func() {})
	dialog.SetFocused(true)

	for _, r := range "héllo🎉" {
		dialog.HandleInput(string(r))
	}
	if dialog.Value() != "héllo🎉" {
		t.Fatalf("Value = %q, want héllo🎉", dialog.Value())
	}

	dialog.HandleInput("\x1b[D")
	dialog.HandleInput("\x1b[D")
	dialog.HandleInput("\x1b[3~")
	if dialog.Value() != "héll🎉" {
		t.Fatalf("forward delete on unicode = %q, want héll🎉", dialog.Value())
	}

	dialog.HandleInput("\x7f")
	if dialog.Value() != "hél🎉" {
		t.Fatalf("backspace on unicode = %q, want hél🎉", dialog.Value())
	}

	dialog.HandleInput("\x1b[F")
	dialog.HandleInput("ü")
	if dialog.Value() != "hél🎉ü" {
		t.Fatalf("append after unicode = %q, want hél🎉ü", dialog.Value())
	}

	body := maskedBodyLine(dialog.Render(60))
	if strings.Count(body, "•") != len([]rune(dialog.Value())) {
		t.Fatalf("rendered bullets = %q, want %d", body, len([]rune(dialog.Value())))
	}

	dialog.HandleInput("\r")
	if submitted != "hél🎉ü" {
		t.Fatalf("submitted = %q, want hél🎉ü", submitted)
	}
}

func TestMaskedInputEncodedPressRepeatRelease(t *testing.T) {
	theme := testDialogTheme(t)
	dialog := NewMaskedInput("Token", theme, func(string) {}, func() {})
	dialog.HandleInput("\x1b[97u")
	dialog.HandleInput("\x1b[98;1:2u")
	dialog.HandleInput("\x1b[99;1:3u")
	dialog.HandleInput("\x1b[100;1:3u")
	if dialog.Value() != "ab" {
		t.Fatalf("Value = %q, want ab", dialog.Value())
	}
}

func TestMaskedInputIgnoresFocusMouseAndDeviceSequences(t *testing.T) {
	theme := testDialogTheme(t)
	dialog := NewMaskedInput("Token", theme, func(string) {}, func() {})
	sequences := []string{
		tui.FocusIn,
		tui.FocusOut,
		"\x1b[<0;5;5M",
		"\x1b[<0;5;5m",
		"\x1b[<64;5;5M",
		"\x1b[M !!",
		"\x1b[?1;2c",
		"\x1b[>0;276;0c",
		"\x1b[1;1R",
		"\x1b[?2004;1$y",
		"\x1bP1$r0m\x1b\\",
		"\x1b]11;rgb:0000/0000/0000\x07",
		"\x1b[?996n",
		"\x1b[6n",
	}
	for _, sequence := range sequences {
		dialog.HandleInput(sequence)
	}
	if dialog.Value() != "" {
		t.Fatalf("protocol sequences entered the secret: %q", dialog.Value())
	}
	dialog.HandleInput("ok")
	if dialog.Value() != "ok" {
		t.Fatalf("Value = %q, want ok", dialog.Value())
	}
}

func TestMaskedInputBracketedPasteUnicodeAndNewlines(t *testing.T) {
	theme := testDialogTheme(t)
	dialog := NewMaskedInput("Token", theme, func(string) {}, func() {})
	dialog.HandleInput(tui.BracketedPasteStart + "päss\nwörd\r\n🎉" + tui.BracketedPasteEnd)
	if dialog.Value() != "pässwörd🎉" {
		t.Fatalf("pasted value = %q, want pässwörd🎉", dialog.Value())
	}

	dialog.HandleInput(tui.BracketedPasteStart + "ab")
	dialog.HandleInput("cd" + tui.BracketedPasteEnd)
	if dialog.Value() != "pässwörd🎉abcd" {
		t.Fatalf("split paste value = %q", dialog.Value())
	}
}

func TestMaskedInputSameBatchProtocolAndText(t *testing.T) {
	theme := testDialogTheme(t)
	dialog := NewMaskedInput("Token", theme, func(string) {}, func() {})
	dialog.HandleInput(tui.FocusIn + "\x1b[<0;1;1M" + "\x1b[97u" + "bc" + "\x1b[?1;2c")
	if dialog.Value() != "abc" {
		t.Fatalf("same-batch value = %q, want abc", dialog.Value())
	}
}

func TestTokenizeInputProtocolForms(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []string
	}{
		{name: "empty", input: "", want: nil},
		{name: "plain runes", input: "aé", want: []string{"a", "é"}},
		{name: "bare escape", input: "\x1b", want: []string{"\x1b"}},
		{name: "csi final", input: "\x1b[1;1R", want: []string{"\x1b[1;1R"}},
		{name: "csi incomplete", input: "\x1b[38", want: []string{"\x1b[38"}},
		{name: "sgr mouse", input: "\x1b[<0;1;1M", want: []string{"\x1b[<0;1;1M"}},
		{name: "old mouse", input: "\x1b[M !!", want: []string{"\x1b[M !!"}},
		{name: "osc bel", input: "\x1b]0;t\x07", want: []string{"\x1b]0;t\x07"}},
		{name: "osc st", input: "\x1b]0;t\x1b\\", want: []string{"\x1b]0;t\x1b\\"}},
		{name: "osc incomplete", input: "\x1b]0;t", want: []string{"\x1b]0;t"}},
		{name: "dcs", input: "\x1bP1$r0m\x1b\\", want: []string{"\x1bP1$r0m\x1b\\"}},
		{name: "apc", input: "\x1b_data\x1b\\", want: []string{"\x1b_data\x1b\\"}},
		{name: "ss3", input: "\x1bOP", want: []string{"\x1bOP"}},
		{name: "alt key", input: "\x1ba", want: []string{"\x1ba"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tokenizeInput(tc.input)
			if len(got) != len(tc.want) {
				t.Fatalf("tokenizeInput(%q) = %q, want %q", tc.input, got, tc.want)
			}
			for index := range got {
				if got[index] != tc.want[index] {
					t.Fatalf("tokenizeInput(%q) = %q, want %q", tc.input, got, tc.want)
				}
			}
		})
	}
}

func TestMaskedInputRenderNeverLeaksSecretBytes(t *testing.T) {
	theme := testDialogTheme(t)
	secret := "sëcret🎉value-42"
	dialog := NewMaskedInput("Token", theme, func(string) {}, func() {})
	dialog.SetFocused(true)
	dialog.HandleInput(secret)

	body := maskedBodyLine(dialog.Render(60))
	if body == "" {
		t.Fatal("masked render missing body line")
	}
	for _, r := range body {
		if r != ' ' && r != '•' && r != '│' {
			t.Fatalf("masked body leaked %q in %q", r, body)
		}
	}
	if strings.Count(body, "•") != len([]rune(secret)) {
		t.Fatalf("masked body bullets = %q, want %d", body, len([]rune(secret)))
	}
}
