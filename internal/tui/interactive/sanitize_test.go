package interactive

import (
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/tui"
)

func TestSanitizeDisplayText(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "plain", input: "hello", want: "hello"},
		{name: "keeps newline", input: "a\nb", want: "a\nb"},
		{name: "maps tab", input: "a\tb", want: "a   b"},
		{name: "drops bell", input: "a\ab", want: "ab"},
		{name: "drops delete", input: "a\bb", want: "ab"},
		{name: "drops c1", input: "a\u0085b", want: "ab"},
		{name: "strips sgr", input: "\x1b[31mred\x1b[0m", want: "red"},
		{name: "strips osc8", input: tui.OSC8Hyperlink("", "https://example.com") + "link" + tui.OSC8Close, want: "link"},
		{name: "strips csi", input: "\x1b[2Jclean", want: "clean"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := SanitizeDisplayText(testCase.input); got != testCase.want {
				t.Fatalf("SanitizeDisplayText(%q) = %q, want %q", testCase.input, got, testCase.want)
			}
		})
	}
}

func TestSanitizeSingleLine(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{input: "", want: ""},
		{input: "plain", want: "plain"},
		{input: "a\nb", want: "a b"},
		{input: "a\n\nb", want: "a b"},
		{input: "a\n\n\n", want: "a "},
		{input: "\x1b[31mred\nline\x1b[0m", want: "red line"},
	}
	for _, testCase := range cases {
		if got := SanitizeSingleLine(testCase.input); got != testCase.want {
			t.Fatalf("SanitizeSingleLine(%q) = %q, want %q", testCase.input, got, testCase.want)
		}
	}
}

func TestSanitizeOSC8Target(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{input: "https://example.com", want: "https://example.com"},
		{input: "https://example.com/\x07evil", want: "https://example.com/evil"},
		{input: "https://example.com/\nline", want: "https://example.com/line"},
		{input: "mailto:a@example.com", want: "mailto:a@example.com"},
	}
	for _, testCase := range cases {
		if got := SanitizeOSC8Target(testCase.input); got != testCase.want {
			t.Fatalf("SanitizeOSC8Target(%q) = %q, want %q", testCase.input, got, testCase.want)
		}
	}
}

func TestSafeHyperlinkTarget(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    string
		allowed bool
	}{
		{name: "https", input: "https://example.com/a?b=c", want: "https://example.com/a?b=c", allowed: true},
		{name: "http", input: "http://example.com", want: "http://example.com", allowed: true},
		{name: "mailto", input: "mailto:a@example.com", want: "mailto:a@example.com", allowed: true},
		{name: "uppercase scheme", input: "HTTPS://example.com", want: "HTTPS://example.com", allowed: true},
		{name: "javascript", input: "javascript:alert(1)", want: "javascript:alert(1)", allowed: false},
		{name: "data", input: "data:text/html;base64,AAAA", want: "data:text/html;base64,AAAA", allowed: false},
		{name: "file", input: "file:///etc/passwd", want: "file:///etc/passwd", allowed: false},
		{name: "relative", input: "/tmp/x", want: "/tmp/x", allowed: false},
		{name: "control stripped", input: " https://example.com/\x1b[31m ", want: "https://example.com/[31m", allowed: true},
		{name: "empty", input: "   ", want: "", allowed: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, allowed := safeHyperlinkTarget(testCase.input)
			if got != testCase.want || allowed != testCase.allowed {
				t.Fatalf("safeHyperlinkTarget(%q) = (%q, %v), want (%q, %v)", testCase.input, got, allowed, testCase.want, testCase.allowed)
			}
		})
	}
}

func TestSanitizeDisplayTextLeavesPlainUntouched(t *testing.T) {
	input := strings.Repeat("plain text ", 100)
	if got := SanitizeDisplayText(input); got != input {
		t.Fatal("plain text should be returned unchanged")
	}
}
