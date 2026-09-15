package tui

import "testing"

func TestIsDeviceAttributesResponse(t *testing.T) {
	tests := []struct {
		name     string
		sequence string
		want     bool
	}{
		{name: "standard", sequence: "\x1b[?1;2c", want: true},
		{name: "empty body", sequence: "\x1b[?c", want: true},
		{name: "multiple attributes", sequence: "\x1b[?62;1;6;22c", want: true},
		{name: "higher private marker", sequence: "\x1b[?64;1c", want: true},
		{name: "wrong suffix", sequence: "\x1b[?1;2x", want: false},
		{name: "missing private marker", sequence: "\x1b[1;2c", want: false},
		{name: "plain sequence", sequence: "\x1b[A", want: false},
		{name: "invalid character", sequence: "\x1b[?1;ac", want: false},
		{name: "empty sequence", sequence: "", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isDeviceAttributesResponse(test.sequence); got != test.want {
				t.Fatalf("isDeviceAttributesResponse(%q) = %v, want %v", test.sequence, got, test.want)
			}
		})
	}
}
