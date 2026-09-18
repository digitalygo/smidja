package tui

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBoundaryStripHostile(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		absent  []string
		present []string
	}{
		{name: "csi-sgr", input: "\x1b[31mVISIBLE\x1b[0m", absent: []string{"\x1b"}, present: []string{"VISIBLE"}},
		{name: "csi-erase", input: "\x1b[2JVISIBLE", absent: []string{"\x1b"}, present: []string{"VISIBLE"}},
		{name: "csi-cursor", input: "\x1b[10;10HVISIBLE", absent: []string{"\x1b"}, present: []string{"VISIBLE"}},
		{name: "csi-incomplete", input: "\x1b[38mVISIBLE", absent: []string{"\x1b"}, present: []string{"VISIBLE"}},
		{name: "osc-bel", input: "\x1b]0;OSC_EVIL_1\x07VISIBLE", absent: []string{"OSC_EVIL_1", "\x1b"}, present: []string{"VISIBLE"}},
		{name: "osc-st", input: "\x1b]0;OSC_EVIL_2\x1b\\VISIBLE", absent: []string{"OSC_EVIL_2", "\x1b"}, present: []string{"VISIBLE"}},
		{name: "osc-c1st", input: "\x1b]0;OSC_EVIL_3\xc2\x9cVISIBLE", absent: []string{"OSC_EVIL_3", "\x1b"}, present: []string{"VISIBLE"}},
		{name: "osc-incomplete", input: "\x1b]0;INCOMPLETE\x07VISIBLE", absent: []string{"INCOMPLETE"}, present: []string{"VISIBLE"}},
		{name: "apc-bel", input: "\x1b_APP_EVIL_4\x07VISIBLE", absent: []string{"APP_EVIL_4", "\x1b"}, present: []string{"VISIBLE"}},
		{name: "apc-st", input: "\x1b_APP_EVIL_5\x1b\\VISIBLE", absent: []string{"APP_EVIL_5", "\x1b"}, present: []string{"VISIBLE"}},
		{name: "dcs-st", input: "\x1bP1$rDCS_EVIL_6\x1b\\VISIBLE", absent: []string{"DCS_EVIL_6", "\x1b"}, present: []string{"VISIBLE"}},
		{name: "dcs-bel", input: "\x1bP1$rDCS_EVIL_7\x07VISIBLE", absent: []string{"DCS_EVIL_7", "\x1b"}, present: []string{"VISIBLE"}},
		{name: "sos", input: "\x1bXSYSSOS_EVIL_8\x1b\\VISIBLE", absent: []string{"SOS_EVIL_8", "\x1b"}, present: []string{"VISIBLE"}},
		{name: "pm", input: "\x1b^PMEVIL_9\x1b\\VISIBLE", absent: []string{"PMEVIL_9", "\x1b"}, present: []string{"VISIBLE"}},
		{name: "st-alone", input: "a\x1b\\bVISIBLE", absent: []string{"\x1b"}, present: []string{"VISIBLE"}},
		{name: "ss3", input: "\x1bOPVISIBLE", absent: []string{"\x1b"}, present: []string{"VISIBLE"}},
		{name: "generic", input: "\x1b7VISIBLE\x1bM", absent: []string{"\x1b"}, present: []string{"VISIBLE"}},
		{name: "charset", input: "\x1b(BVISIBLE", absent: []string{"\x1b"}, present: []string{"VISIBLE"}},
		{name: "bare-esc", input: "a\x1b", absent: []string{"\x1b"}, present: []string{"a"}},
		{name: "c1-csi", input: "a2JbVISIBLE", absent: []string{"\x9b"}, present: []string{"VISIBLE"}},
		{name: "c1-csi-complete", input: "a\u009b2JbVISIBLE", absent: []string{"\u009b"}, present: []string{"VISIBLE"}},
		{name: "c1-osc", input: "a\u009d0;OSC_EVIL_C1\x07bVISIBLE", absent: []string{"OSC_EVIL_C1"}, present: []string{"VISIBLE"}},
		{name: "c1-apc", input: "a\u009fAPP_EVIL_C1\x07bVISIBLE", absent: []string{"APP_EVIL_C1"}, present: []string{"VISIBLE"}},
		{name: "c1-dcs", input: "a\u00901$rDCS_EVIL_C1\x07bVISIBLE", absent: []string{"DCS_EVIL_C1"}, present: []string{"VISIBLE"}},
		{name: "c1-sos", input: "a\u0098SOS_EVIL_C1\x07bVISIBLE", absent: []string{"SOS_EVIL_C1"}, present: []string{"VISIBLE"}},
		{name: "c1-pm", input: "a\u009ePM_EVIL_C1\x07bVISIBLE", absent: []string{"PM_EVIL_C1"}, present: []string{"VISIBLE"}},
		{name: "c1-st", input: "a\u009cbVISIBLE", absent: []string{"\u009c"}, present: []string{"VISIBLE"}},
		{name: "c1-single", input: "abVISIBLE", absent: []string{"\u0085"}, present: []string{"VISIBLE"}},
		{name: "c1-incomplete-csi", input: "a\u009b38123", absent: []string{"\u009b"}, present: []string{"38123"}},
		{name: "c1-incomplete-osc", input: "a\u009d0;INCOMPLETE", absent: []string{"\u009d"}, present: []string{"INCOMPLETE"}},
		{name: "plain-preserved", input: "hello VISIBLE", absent: []string{}, present: []string{"hello", "VISIBLE"}},
		{name: "utf8-preserved", input: "한글 VISIBLE 🎉", absent: []string{}, present: []string{"한글", "VISIBLE"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := StripTerminalSequences(tc.input)
			if strings.Contains(got, "\x1b") {
				t.Fatalf("stripped leaked ESC:\n%q", got)
			}
			for _, bad := range tc.absent {
				if bad != "" && strings.Contains(got, bad) {
					t.Fatalf("stripped leaked %q:\n%q", bad, got)
				}
			}
			for _, want := range tc.present {
				if want != "" && !strings.Contains(got, want) {
					t.Fatalf("stripped missing %q:\n%q", want, got)
				}
			}
			if code, ok := extractANSI(tc.input, 0); ok {
				_ = code
			}
			_ = VisibleWidth(got)
			in := NewInput(InputOptions{})
			in.SetFocused(true)
			in.value = tc.input
			in.cursor = len(tc.input)
			rawLines := in.Render(60)
			if len(rawLines) == 0 {
				t.Fatalf("input render empty for %q", tc.input)
			}
			raw := strings.Join(rawLines, "\n")
			for _, bad := range tc.absent {
				if bad == "" || bad == "\x1b" || bad == "\u009b" || bad == "\u009c" || bad == "\u0085" {
					continue
				}
				if strings.Contains(raw, bad) {
					t.Fatalf("input raw leaked %q:\n%q", bad, raw)
				}
			}
			cleaned := strings.ReplaceAll(raw, CursorMarker, "")
			cleaned = strings.ReplaceAll(cleaned, SGRInverse, "")
			cleaned = strings.ReplaceAll(cleaned, SGRInverseOff, "")
			if strings.Contains(cleaned, "\x1b") {
				t.Fatalf("input raw leaked untrusted ESC after trusted removal:\n%q", raw)
			}
			stripped := StripTerminalSequences(cleaned)
			if !utf8.ValidString(stripped) {
				t.Fatalf("input stripped invalid UTF-8:\n%q", stripped)
			}
			for _, r := range stripped {
				if r < 0x20 && r != '\n' {
					t.Fatalf("input stripped leaked C0 %U:\n%q", r, stripped)
				}
				if r == 0x7f || (r >= 0x80 && r <= 0x9f) {
					t.Fatalf("input stripped leaked control %U:\n%q", r, stripped)
				}
			}
			for _, want := range tc.present {
				if want != "" && !strings.Contains(stripped, want) {
					t.Fatalf("input stripped missing %q:\n%q", want, stripped)
				}
			}
			if got := in.Value(); got != tc.input {
				t.Fatalf("input raw changed %q vs %q", got, tc.input)
			}
			ed := NewEditor(EditorOptions{TerminalRows: 24})
			ed.SetFocused(false)
			ed.buffer.lines = []string{tc.input}
			ed.buffer.cursorLine = 0
			ed.buffer.cursorCol = len(tc.input)
			erawLines := ed.Render(60)
			if len(erawLines) == 0 {
				t.Fatalf("editor render empty for %q", tc.input)
			}
			erawJoined := strings.Join(erawLines, "\n")
			for _, bad := range tc.absent {
				if bad == "" || bad == "\x1b" || bad == "\u009b" || bad == "\u009c" || bad == "\u0085" {
					continue
				}
				if strings.Contains(erawJoined, bad) {
					t.Fatalf("editor raw leaked %q:\n%q", bad, erawJoined)
				}
			}
			ecleaned := strings.ReplaceAll(erawJoined, CursorMarker, "")
			ecleaned = strings.ReplaceAll(ecleaned, SGRInverse, "")
			ecleaned = strings.ReplaceAll(ecleaned, SGRInverseOff, "")
			if strings.Contains(ecleaned, "\x1b") {
				t.Fatalf("editor raw leaked untrusted ESC after trusted removal:\n%q", erawJoined)
			}
			estripped := StripTerminalSequences(ecleaned)
			if !utf8.ValidString(estripped) {
				t.Fatalf("editor stripped invalid UTF-8:\n%q", estripped)
			}
			if got := ed.buffer.Text(); got != tc.input {
				t.Fatalf("editor raw changed %q vs %q", got, tc.input)
			}
		})
	}
}

func TestBoundaryExtractForms(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantANSI  bool
		forbidden []string
	}{
		{name: "csi", input: "\x1b[31m", wantANSI: true, forbidden: []string{"[31m"}},
		{name: "osc", input: "\x1b]0;t\x07", wantANSI: true, forbidden: []string{"t"}},
		{name: "apc", input: "\x1b_Gi=1\x07", wantANSI: true, forbidden: []string{"Gi=1"}},
		{name: "dcs", input: "\x1bP1$r0m\x1b\\", wantANSI: true, forbidden: []string{"1$r0m"}},
		{name: "sos", input: "\x1bXtest\x1b\\", wantANSI: true, forbidden: []string{"test"}},
		{name: "pm", input: "\x1b^test\x1b\\", wantANSI: true, forbidden: []string{"test"}},
		{name: "st", input: "\x1b\\", wantANSI: true, forbidden: []string{}},
		{name: "ss3", input: "\x1bOP", wantANSI: true, forbidden: []string{}},
		{name: "generic", input: "\x1b7", wantANSI: true, forbidden: []string{}},
		{name: "charset", input: "\x1b(B", wantANSI: true, forbidden: []string{}},
		{name: "bare", input: "\x1b", wantANSI: true, forbidden: []string{}},
		{name: "incomplete-csi", input: "\x1b[38", wantANSI: false, forbidden: []string{}},
		{name: "incomplete-osc", input: "\x1b]0;t", wantANSI: false, forbidden: []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, gotANSI := extractANSI(tc.input, 0)
			if gotANSI != tc.wantANSI {
				t.Fatalf("extractANSI(%q) = %v, want %v", tc.input, gotANSI, tc.wantANSI)
			}
			stripped := StripTerminalSequences(tc.input)
			if tc.wantANSI && strings.Contains(stripped, "\x1b") {
				t.Fatalf("complete form stripped leaked ESC:\n%q", stripped)
			}
			if !tc.wantANSI {
				sanitized, _ := sanitizeSingleLineWithMapping(tc.input+"VISIBLE", 0)
				if strings.Contains(sanitized, "\x1b") {
					t.Fatalf("incomplete sanitized leaked ESC:\n%q", sanitized)
				}
			}
			count := 0
			forEachANSI(tc.input, func(code ansiCode) bool {
				count++
				if code.code == "" || code.length <= 0 {
					t.Fatalf("invalid ansi code %+v for %q", code, tc.input)
				}
				return true
			})
			if tc.wantANSI && count != 1 {
				t.Fatalf("forEachANSI count = %d, want 1 for %q", count, tc.input)
			}
			if !tc.wantANSI && count != 0 {
				t.Fatalf("forEachANSI count = %d, want 0 for incomplete %q", count, tc.input)
			}
			payload := tc.input + "VISIBLE"
			in := NewInput(InputOptions{})
			in.SetFocused(true)
			in.value = payload
			in.cursor = len(payload)
			rawLines := in.Render(60)
			if len(rawLines) == 0 {
				t.Fatalf("input render empty for %q", tc.input)
			}
			raw := strings.Join(rawLines, "\n")
			for _, bad := range tc.forbidden {
				if bad != "" && strings.Contains(raw, bad) {
					t.Fatalf("input raw leaked %q:\n%q", bad, raw)
				}
			}
			cleaned := strings.ReplaceAll(raw, CursorMarker, "")
			cleaned = strings.ReplaceAll(cleaned, SGRInverse, "")
			cleaned = strings.ReplaceAll(cleaned, SGRInverseOff, "")
			if strings.Contains(cleaned, "\x1b") {
				t.Fatalf("input raw leaked untrusted ESC after trusted removal:\n%q", raw)
			}
			cleanStripped := StripTerminalSequences(cleaned)
			if !utf8.ValidString(cleanStripped) {
				t.Fatalf("input stripped invalid UTF-8:\n%q", cleanStripped)
			}
			if !strings.Contains(cleanStripped, "ISIBLE") {
				t.Fatalf("input stripped missing ISIBLE:\n%q", cleanStripped)
			}
			if VisibleWidth(cleanStripped) > 60 {
				t.Fatalf("input width overflow %d:\n%q", VisibleWidth(cleanStripped), cleanStripped)
			}
			if got := in.Value(); got != payload {
				t.Fatalf("input raw changed %q vs %q", got, payload)
			}
			ed := NewEditor(EditorOptions{TerminalRows: 24})
			ed.SetFocused(false)
			ed.buffer.lines = []string{payload}
			ed.buffer.cursorLine = 0
			ed.buffer.cursorCol = len(payload)
			erawLines := ed.Render(60)
			if len(erawLines) == 0 {
				t.Fatalf("editor render empty for %q", tc.input)
			}
			erawJoined := strings.Join(erawLines, "\n")
			for _, bad := range tc.forbidden {
				if bad != "" && strings.Contains(erawJoined, bad) {
					t.Fatalf("editor raw leaked %q:\n%q", bad, erawJoined)
				}
			}
			ecleaned := strings.ReplaceAll(erawJoined, CursorMarker, "")
			ecleaned = strings.ReplaceAll(ecleaned, SGRInverse, "")
			ecleaned = strings.ReplaceAll(ecleaned, SGRInverseOff, "")
			if strings.Contains(ecleaned, "\x1b") {
				t.Fatalf("editor raw leaked untrusted ESC after trusted removal:\n%q", erawJoined)
			}
		})
	}
}
