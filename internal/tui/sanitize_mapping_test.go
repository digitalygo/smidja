package tui

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func mappingCleanRaw(raw string) string {
	s := strings.ReplaceAll(raw, CursorMarker, "")
	s = strings.ReplaceAll(s, SGRInverse, "")
	s = strings.ReplaceAll(s, SGRInverseOff, "")
	s = strings.ReplaceAll(s, SGRReset, "")
	return s
}

func mappingAssertNoControls(t *testing.T, s string) {
	t.Helper()
	for _, r := range s {
		if r < 0x20 && r != '\n' {
			t.Fatalf("leaked C0 %U in %q", r, s)
		}
		if r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			t.Fatalf("leaked control %U in %q", r, s)
		}
	}
}

func mappingAssertRawClean(t *testing.T, raw string) {
	t.Helper()
	cleaned := mappingCleanRaw(raw)
	if strings.Contains(cleaned, "\x1b") {
		t.Fatalf("raw leaked untrusted ESC in %q", raw)
	}
	if !utf8.ValidString(cleaned) {
		t.Fatalf("raw invalid UTF-8 in %q", raw)
	}
	stripped := StripTerminalSequences(cleaned)
	if !utf8.ValidString(stripped) {
		t.Fatalf("stripped invalid UTF-8 in %q", stripped)
	}
	mappingAssertNoControls(t, stripped)
}

func mappingInputRaws() []string {
	return []string{
		"",
		"a",
		"hello",
		"\x00",
		"\x01",
		"\x07",
		"\x1f",
		"\x7f",
		"a\x00b",
		"a\x07b",
		"\x00é",
		"a\x00é",
		"a\x00b\x7f\x9fc",
		"\u0080",
		"\u0085",
		"\u009b",
		"\u0090",
		"\u0098",
		"\u009d",
		"\u009e",
		"\u009f",
		"\u009c",
		"a\u0085b",
		"a\u009b2Jb",
		"a\u009b38123",
		"a\u009d0;INCOMPLETE",
		"a\u009d0;EVIL\x07b",
		"a\u009fEVIL\x07b",
		"a\u00901$rEVIL\x07b",
		"\x1b[31m",
		"\x1b[2J",
		"\x1b[10;10H",
		"\x1b[38;5;196mVISIBLE",
		"a\x1b[31mb",
		"\x1b[31mhi\x1b[0m",
		"a\x1b",
		"a\x1b[",
		"a\x1b[38",
		"\x1b",
		"\x1b[",
		"\x1b[38",
		"\x1b]0;t",
		"\x1b]0;EVIL\x07VISIBLE",
		"\x1b]0;EVIL\x1b\\VISIBLE",
		"\x1b]0;EVIL\xc2\x9cVISIBLE",
		"\x1b_APP_EVIL\x07VISIBLE",
		"\x1b_APP_EVIL\x1b\\VISIBLE",
		"\x1bP1$rDCS_EVIL\x1b\\VISIBLE",
		"\x1bP1$rDCS_EVIL\x07VISIBLE",
		"\x1bXSYSEVIL\x1b\\VISIBLE",
		"\x1b^PMEVIL\x1b\\VISIBLE",
		"a\x1b\\b",
		"\x1bOPVISIBLE",
		"\x1b7VISIBLE",
		"\x1b(BVISIBLE",
		"e\xcc\x81",
		"a\u0301b",
		"e\u0301\u0302",
		"é",
		"aé",
		"한",
		"한글",
		"🎉",
		"a🎉b",
		"あいう",
		"a\xc3",
		"a\xff",
		"\xff",
		"\x80",
		"a\x80b",
		"\xf0\x9f\x8e",
		"a\rc",
		"a\nc",
		"a\r\nc",
		"a\tc",
		"a\n\nc",
		"\n",
		"\t",
		"a b",
		"  ordinary",
	}
}

func TestSanitizeMappingInputExhaustive(t *testing.T) {
	raws := mappingInputRaws()
	widths := []int{10, 30, 60}
	for _, raw := range raws {
		for cur := 0; cur <= len(raw); cur++ {
			for _, w := range widths {
				func() {
					defer func() {
						if r := recover(); r != nil {
							t.Fatalf("input panic raw=%q cur=%d width=%d: %v", raw, cur, w, r)
						}
					}()
					in := NewInput(InputOptions{})
					in.SetFocused(true)
					in.value = raw
					in.cursor = cur
					lines := in.Render(w)
					if len(lines) != 1 {
						t.Fatalf("input lines=%d raw=%q cur=%d", len(lines), raw, cur)
					}
					line := lines[0]
					if VisibleWidth(line) > w {
						t.Fatalf("input width %d > %d raw=%q cur=%d line=%q", VisibleWidth(line), w, raw, cur, line)
					}
					rawJoined := line
					mappingAssertRawClean(t, rawJoined)
					stripped := StripTerminalSequences(mappingCleanRaw(rawJoined))
					if strings.Contains(stripped, "\x00") {
						t.Fatalf("stripped leaked NUL raw=%q cur=%d", raw, cur)
					}
					if got := in.Value(); got != raw {
						t.Fatalf("input raw changed %q vs %q", got, raw)
					}
					sanitized, mapped := sanitizeSingleLineWithMapping(raw, cur)
					if mapped < 0 || mapped > len(sanitized) {
						t.Fatalf("mapped out of range %d len=%d raw=%q cur=%d", mapped, len(sanitized), raw, cur)
					}
					if mapped > 0 && mapped < len(sanitized) {
						c := sanitized[mapped]
						if c >= 0x80 && c < 0xC0 {
							t.Fatalf("mapped splits rune %d in %q raw=%q cur=%d", mapped, sanitized, raw, cur)
						}
					}
					if !utf8.ValidString(sanitized) {
						t.Fatalf("sanitized invalid UTF-8 %q raw=%q", sanitized, raw)
					}
					prefix := sanitized
					if mapped < len(prefix) {
						prefix = sanitized[:mapped]
					}
					if !utf8.ValidString(prefix) {
						t.Fatalf("sanitized prefix invalid %q raw=%q cur=%d", prefix, raw, cur)
					}
				}()
			}
		}
	}
}

func TestSanitizeMappingInputEquivalence(t *testing.T) {
	for _, raw := range mappingInputRaws() {
		got, _ := sanitizeRawAndBuildMap(raw, true)
		want := sanitizeSelectSingleLine(raw)
		if got != want {
			t.Fatalf("single equivalence raw=%q got=%q want=%q", raw, got, want)
		}
		got2, _ := sanitizeRawAndBuildMap(raw, false)
		want2 := sanitizeEditorLine(raw)
		if got2 != want2 {
			t.Fatalf("editor equivalence raw=%q got=%q want=%q", raw, got2, want2)
		}
		sanitized, table := sanitizeRawAndBuildMap(raw, true)
		if len(table) != len(raw)+1 {
			t.Fatalf("table len %d vs %d", len(table), len(raw)+1)
		}
		for i := 0; i <= len(raw); i++ {
			d := table[i]
			if d < 0 || d > len(sanitized) {
				t.Fatalf("table range raw=%q i=%d d=%d", raw, i, d)
			}
			if d > 0 && d < len(sanitized) {
				c := sanitized[d]
				if c >= 0x80 && c < 0xC0 {
					t.Fatalf("table splits rune raw=%q i=%d d=%d", raw, i, d)
				}
			}
		}
	}
}

func TestSanitizeMappingEditorExhaustive(t *testing.T) {
	raws := mappingInputRaws()
	widths := []int{20, 40, 60}
	for _, raw := range raws {
		for cur := 0; cur <= len(raw); cur++ {
			for _, w := range widths {
				func() {
					defer func() {
						if r := recover(); r != nil {
							t.Fatalf("editor panic raw=%q cur=%d width=%d: %v", raw, cur, w, r)
						}
					}()
					ed := NewEditor(EditorOptions{TerminalRows: 24})
					ed.SetFocused(true)
					ed.buffer.lines = []string{raw}
					ed.buffer.cursorLine = 0
					ed.buffer.cursorCol = cur
					lines := ed.Render(w)
					if len(lines) == 0 {
						t.Fatalf("editor no lines raw=%q cur=%d", raw, cur)
					}
					for _, line := range lines {
						if VisibleWidth(line) > w {
							t.Fatalf("editor width %d > %d raw=%q cur=%d", VisibleWidth(line), w, raw, cur)
						}
						mappingAssertRawClean(t, line)
					}
					if got := ed.buffer.Text(); got != raw {
						t.Fatalf("editor raw changed %q vs %q", got, raw)
					}
					sanitized, mapped := sanitizeEditorLineWithMapping(raw, cur)
					if mapped < 0 || mapped > len(sanitized) {
						t.Fatalf("editor mapped range %d raw=%q cur=%d", mapped, raw, cur)
					}
					if !utf8.ValidString(sanitized) {
						t.Fatalf("editor sanitized invalid %q", sanitized)
					}
				}()
			}
		}
	}
}

func TestSanitizeMappingEditorMultiline(t *testing.T) {
	multis := [][]string{
		{"hello", "world"},
		{"a\x1b[31mb", "c\x00d"},
		{"", ""},
		{"", "VISIBLE"},
		{"e\xcc\x81", "한글", "🎉"},
		{"a", "", "b"},
		{"line one", "line two with \x1b[2J hostile", "line three"},
		{"a\x1b]0;EVIL\x07b", "c"},
		{"first", "second", "third"},
		{"0123456789ABCDEFGHIJ", "short"},
		{"a\xff", "b\x80c"},
		{"a\tb", "c"},
	}
	for _, lines := range multis {
		for lineIdx := 0; lineIdx < len(lines); lineIdx++ {
			for col := 0; col <= len(lines[lineIdx]); col++ {
				func() {
					defer func() {
						if r := recover(); r != nil {
							t.Fatalf("multiline panic lines=%q idx=%d col=%d: %v", lines, lineIdx, col, r)
						}
					}()
					ed := NewEditor(EditorOptions{TerminalRows: 24})
					ed.SetFocused(true)
					ed.buffer.lines = append([]string(nil), lines...)
					ed.buffer.cursorLine = lineIdx
					ed.buffer.cursorCol = col
					rendered := ed.Render(40)
					if len(rendered) == 0 {
						t.Fatalf("multiline no render")
					}
					for _, line := range rendered {
						if VisibleWidth(line) > 40 {
							t.Fatalf("multiline width overflow %q", line)
						}
						mappingAssertRawClean(t, line)
					}
					joined := strings.Join(ed.buffer.Lines(), "\n")
					want := strings.Join(lines, "\n")
					if joined != want {
						t.Fatalf("multiline raw changed %q vs %q", joined, want)
					}
				}()
			}
		}
	}
}

func TestSanitizeMappingEditorSelections(t *testing.T) {
	raws := []string{
		"hello world",
		"a\x1b[31mbVISIBLE",
		"a\x00bVISIBLE",
		"e\xcc\x81VISIBLE",
		"한글VISIBLE🎉",
		"a\xffVISIBLE",
		"0123456789ABCDEFGHIJ",
		"first",
		"",
		"a",
	}
	for _, raw := range raws {
		spans := [][2]int{{0, 0}, {0, len(raw)}}
		if len(raw) > 1 {
			spans = append(spans, [2]int{0, 1}, [2]int{1, len(raw)}, [2]int{len(raw) - 1, len(raw)})
		}
		for i := 0; i <= len(raw); i++ {
			for j := i; j <= len(raw); j++ {
				spans = append(spans, [2]int{i, j})
			}
		}
		seen := map[[2]int]bool{}
		for _, sp := range spans {
			if seen[sp] {
				continue
			}
			seen[sp] = true
			for _, cur := range []int{sp[0], sp[1], 0, len(raw)} {
				func() {
					defer func() {
						if r := recover(); r != nil {
							t.Fatalf("selection panic raw=%q sel=%v cur=%d: %v", raw, sp, cur, r)
						}
					}()
					ed := NewEditor(EditorOptions{TerminalRows: 24})
					ed.SetFocused(true)
					ed.buffer.lines = []string{raw}
					ed.buffer.cursorLine = 0
					ed.buffer.cursorCol = cur
					if cur > len(raw) {
						cur = len(raw)
					}
					if sp[0] == sp[1] {
						ed.ClearSelection()
					} else {
						a := sp[0]
						b := sp[1]
						if a > len(raw) {
							a = len(raw)
						}
						if b > len(raw) {
							b = len(raw)
						}
						ed.SetSelection(0, a, 0, b)
					}
					rendered := ed.Render(40)
					if len(rendered) == 0 {
						t.Fatalf("selection no render")
					}
					for _, line := range rendered {
						if VisibleWidth(line) > 40 {
							t.Fatalf("selection width overflow raw=%q sel=%v", raw, sp)
						}
						mappingAssertRawClean(t, line)
					}
					if got := ed.buffer.Text(); got != raw {
						t.Fatalf("selection raw changed %q vs %q", got, raw)
					}
				}()
			}
		}
	}
}

func TestSanitizeMappingEditorMultilineSelections(t *testing.T) {
	base := []string{"first\x1b[31mVISIBLE", "second\x00VISIBLE", "third🎉VISIBLE"}
	selections := [][4]int{
		{0, 0, 0, 5},
		{0, 2, 2, 3},
		{0, 0, 2, 5},
		{1, 0, 1, 6},
		{0, 5, 1, 2},
		{2, 0, 2, 7},
	}
	for _, sel := range selections {
		for lineIdx := 0; lineIdx < len(base); lineIdx++ {
			for _, col := range []int{0, 2, len(base[lineIdx])} {
				func() {
					defer func() {
						if r := recover(); r != nil {
							t.Fatalf("multiline selection panic sel=%v cursor=%d:%d: %v", sel, lineIdx, col, r)
						}
					}()
					ed := NewEditor(EditorOptions{TerminalRows: 24})
					ed.SetFocused(true)
					ed.buffer.lines = append([]string(nil), base...)
					ed.buffer.cursorLine = lineIdx
					ed.buffer.cursorCol = col
					ed.SetSelection(sel[0], sel[1], sel[2], sel[3])
					rendered := ed.Render(40)
					if len(rendered) == 0 {
						t.Fatalf("multiline selection no render")
					}
					for _, line := range rendered {
						if VisibleWidth(line) > 40 {
							t.Fatalf("multiline selection width overflow")
						}
						mappingAssertRawClean(t, line)
					}
					joined := strings.Join(ed.buffer.Lines(), "\n")
					want := strings.Join(base, "\n")
					if joined != want {
						t.Fatalf("multiline selection raw changed %q vs %q", joined, want)
					}
					_ = ed.SelectedText()
				}()
			}
		}
	}
}

func TestSanitizeMappingIncompletePrefix(t *testing.T) {
	raw := "a\x1b[31mb"
	in := NewInput(InputOptions{})
	in.SetFocused(true)
	in.value = raw
	in.cursor = 2
	lines := in.Render(30)
	rawOut := strings.Join(lines, "\n")
	if strings.Contains(rawOut, "[31m") {
		t.Fatalf("incomplete prefix leaked in %q", rawOut)
	}
	mappingAssertRawClean(t, rawOut)
	if got := in.Value(); got != raw {
		t.Fatalf("raw changed %q", got)
	}
	ed := NewEditor(EditorOptions{TerminalRows: 24})
	ed.SetFocused(true)
	ed.buffer.lines = []string{raw}
	ed.buffer.cursorLine = 0
	ed.buffer.cursorCol = 2
	eraw := strings.Join(ed.Render(40), "\n")
	if strings.Contains(eraw, "[31m") {
		t.Fatalf("editor prefix leaked in %q", eraw)
	}
	mappingAssertRawClean(t, eraw)
	if got := ed.buffer.Text(); got != raw {
		t.Fatalf("editor raw changed %q", got)
	}
	raw2 := "\x00é"
	in2 := NewInput(InputOptions{})
	in2.SetFocused(true)
	in2.value = raw2
	in2.cursor = 1
	lines2 := in2.Render(30)
	mappingAssertRawClean(t, strings.Join(lines2, "\n"))
	if got := in2.Value(); got != raw2 {
		t.Fatalf("raw2 changed %q", got)
	}
	ed2 := NewEditor(EditorOptions{TerminalRows: 24})
	ed2.SetFocused(true)
	ed2.buffer.lines = []string{raw2}
	ed2.buffer.cursorLine = 0
	ed2.buffer.cursorCol = 1
	eraw2out := strings.Join(ed2.Render(40), "\n")
	mappingAssertRawClean(t, eraw2out)
}

func TestSanitizeMappingCursorEdges(t *testing.T) {
	raws := []string{"", "a", "a\x1b[31mb", "\x00é", "🎉"}
	for _, raw := range raws {
		for _, cur := range []int{-10, -1, len(raw) + 1, len(raw) + 10} {
			sanitized, mapped := sanitizeSingleLineWithMapping(raw, cur)
			if mapped < 0 || mapped > len(sanitized) {
				t.Fatalf("single edge range raw=%q cur=%d mapped=%d", raw, cur, mapped)
			}
			if !utf8.ValidString(sanitized) {
				t.Fatalf("single edge invalid %q", sanitized)
			}
			sanitized2, mapped2 := sanitizeEditorLineWithMapping(raw, cur)
			if mapped2 < 0 || mapped2 > len(sanitized2) {
				t.Fatalf("editor edge range raw=%q cur=%d", raw, cur)
			}
			in := NewInput(InputOptions{})
			in.SetFocused(true)
			in.value = raw
			in.cursor = cur
			lines := in.Render(30)
			if len(lines) != 1 {
				t.Fatalf("edge input lines")
			}
			mappingAssertRawClean(t, lines[0])
			ed := NewEditor(EditorOptions{TerminalRows: 24})
			ed.SetFocused(true)
			ed.buffer.lines = []string{raw}
			ed.buffer.cursorLine = 0
			ed.buffer.cursorCol = cur
			rendered := ed.Render(40)
			if len(rendered) == 0 {
				t.Fatalf("edge editor no render")
			}
			for _, line := range rendered {
				mappingAssertRawClean(t, line)
			}
		}
	}
}
