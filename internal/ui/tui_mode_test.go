package ui

import (
	"strings"
	"testing"
)

func TestParseTUIMode(t *testing.T) {
	cases := []struct {
		input string
		want  TUIMode
	}{
		{"", TUIModeRegular},
		{"regular", TUIModeRegular},
		{"fullscreen", TUIModeFullscreen},
		{"Regular", TUIModeRegular},
		{"FULLSCREEN", TUIModeFullscreen},
		{"  fullscreen  ", TUIModeFullscreen},
	}
	for _, tc := range cases {
		got, err := ParseTUIMode(tc.input)
		if err != nil {
			t.Errorf("ParseTUIMode(%q) error = %v", tc.input, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseTUIMode(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestParseTUIModeRejectsUnknown(t *testing.T) {
	for _, input := range []string{"split", "regular,fullscreen", "full", "0", "--regular"} {
		if mode, err := ParseTUIMode(input); err == nil {
			t.Errorf("ParseTUIMode(%q) = %q, want an error", input, mode)
		} else if !strings.Contains(err.Error(), "regular or fullscreen") {
			t.Errorf("ParseTUIMode(%q) error = %q, want the valid values", input, err)
		}
	}
}

func TestTUIModeHelpers(t *testing.T) {
	if TUIModeRegular.Fullscreen() {
		t.Error("regular mode should not report fullscreen")
	}
	if !TUIModeFullscreen.Fullscreen() {
		t.Error("fullscreen mode should report fullscreen")
	}
	if TUIMode("").String() != "regular" {
		t.Errorf("empty mode String() = %q, want regular", TUIMode("").String())
	}
	if TUIModeFullscreen.String() != "fullscreen" {
		t.Errorf("fullscreen String() = %q", TUIModeFullscreen.String())
	}
}
