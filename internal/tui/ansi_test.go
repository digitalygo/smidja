package tui

import (
	"strings"
	"testing"
)

func TestANSIColorHelpers(t *testing.T) {
	if got := SGRFg256(0); got != "\x1b[38;5;0m" {
		t.Fatalf("SGRFg256(0) = %q", got)
	}
	if got := SGRBg256(255); got != "\x1b[48;5;255m" {
		t.Fatalf("SGRBg256(255) = %q", got)
	}
	if got := SGRFgRGB(1, 2, 3); got != "\x1b[38;2;1;2;3m" {
		t.Fatalf("SGRFgRGB = %q", got)
	}
	if got := SGRBgRGB(10, 20, 30); got != "\x1b[48;2;10;20;30m" {
		t.Fatalf("SGRBgRGB = %q", got)
	}
}

func TestANSICursorHelpers(t *testing.T) {
	if got := CursorTo(0, 0); got != "\x1b[1;1H" {
		t.Fatalf("CursorTo(0,0) = %q", got)
	}
	if got := CursorTo(4, 9); got != "\x1b[5;10H" {
		t.Fatalf("CursorTo(4,9) = %q", got)
	}
	if got := CursorColumn(7); got != "\x1b[8G" {
		t.Fatalf("CursorColumn(7) = %q", got)
	}
	if got := CursorMoveLines(3); got != "\x1b[3B" {
		t.Fatalf("CursorMoveLines(3) = %q", got)
	}
	if got := CursorMoveLines(-2); got != "\x1b[2A" {
		t.Fatalf("CursorMoveLines(-2) = %q", got)
	}
	if got := CursorMoveLines(0); got != "" {
		t.Fatalf("CursorMoveLines(0) = %q", got)
	}
}

func TestANSIMouseHelpers(t *testing.T) {
	buttonMotion := MouseEnable(true)
	if strings.Contains(buttonMotion, MouseAllMotionOn) {
		t.Fatalf("button-motion mode must not enable all-motion: %q", buttonMotion)
	}
	if !strings.Contains(buttonMotion, MouseButtonMotionOn) || !strings.Contains(buttonMotion, MouseSGROn) {
		t.Fatalf("button-motion mode missing sequences: %q", buttonMotion)
	}
	allMotion := MouseEnable(false)
	if !strings.Contains(allMotion, MouseAllMotionOn) {
		t.Fatalf("all-motion mode missing all-motion sequence: %q", allMotion)
	}
	disable := MouseDisable()
	for _, part := range []string{MouseSGROff, FocusEventsOff, MouseAllMotionOff, MouseButtonMotionOff, MouseNormalOff} {
		if !strings.Contains(disable, part) {
			t.Fatalf("MouseDisable missing %q in %q", part, disable)
		}
	}
}

func TestANSITitleAndOSCHelpers(t *testing.T) {
	if got := OSCTitle("hello"); got != OSCTitlePrefix+"hello"+ANSIBEL {
		t.Fatalf("OSCTitle = %q", got)
	}
	if got := OSC52Clipboard("Zm9v"); got != "\x1b]52;c;Zm9v\x07" {
		t.Fatalf("OSC52Clipboard = %q", got)
	}
	if got := OSC8Hyperlink("id=1", "https://example.com"); got != "\x1b]8;id=1;https://example.com\x07" {
		t.Fatalf("OSC8Hyperlink = %q", got)
	}
	if got := BracketedPaste("body"); got != BracketedPasteStart+"body"+BracketedPasteEnd {
		t.Fatalf("BracketedPaste = %q", got)
	}
	if SegmentReset != SGRReset+OSC8Close {
		t.Fatalf("SegmentReset = %q", SegmentReset)
	}
}

func TestItoa(t *testing.T) {
	tests := []struct {
		value int
		want  string
	}{
		{value: 0, want: "0"},
		{value: 7, want: "7"},
		{value: 42, want: "42"},
		{value: 1000, want: "1000"},
		{value: -1, want: "-1"},
		{value: -98765, want: "-98765"},
	}
	for _, test := range tests {
		if got := itoa(test.value); got != test.want {
			t.Errorf("itoa(%d) = %q, want %q", test.value, got, test.want)
		}
	}
}
