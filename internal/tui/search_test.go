package tui

import (
	"strings"
	"testing"
)

func TestTranscriptSearchLiteralCaseInsensitive(t *testing.T) {
	search := NewTranscriptSearch()
	search.SetQuery("HELLO")
	search.Update(SearchSource{Lines: []string{"hello world", "Hello again"}, Width: 40, Generation: 1})
	matches := search.Matches()
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(matches))
	}
	if matches[0].Segments[0].Start != 0 || matches[0].Segments[0].End != 5 {
		t.Fatalf("unexpected first segment %+v", matches[0].Segments)
	}
	if search.Status() != "1/2" {
		t.Fatalf("unexpected status %q", search.Status())
	}
}

func TestTranscriptSearchStripsANSIAndMapsCells(t *testing.T) {
	search := NewTranscriptSearch()
	search.SetQuery("world")
	line := "\x1b[31mhello\x1b[0m world"
	search.Update(SearchSource{Lines: []string{line}, Width: 40, Generation: 1})
	matches := search.Matches()
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	segment := matches[0].Segments[0]
	if segment.Start != 6 || segment.End != 11 {
		t.Fatalf("bad cell mapping %+v", segment)
	}
}

func TestTranscriptSearchMatchesAcrossWrappedLines(t *testing.T) {
	search := NewTranscriptSearch()
	search.SetQuery("hello world")
	search.Update(SearchSource{Lines: []string{"hello", "world", "", "unrelated"}, Width: 5, Generation: 1})
	matches := search.Matches()
	if len(matches) != 1 {
		t.Fatalf("expected cross-wrap match, got %d", len(matches))
	}
	segments := matches[0].Segments
	if len(segments) != 2 {
		t.Fatalf("expected two line segments, got %+v", segments)
	}
	if segments[0].Line != 0 || segments[0].Start != 0 || segments[0].End != 5 {
		t.Fatalf("bad first segment %+v", segments[0])
	}
	if segments[1].Line != 1 || segments[1].Start != 0 || segments[1].End != 5 {
		t.Fatalf("bad second segment %+v", segments[1])
	}
}

func TestTranscriptSearchUnicodeCells(t *testing.T) {
	search := NewTranscriptSearch()
	search.SetQuery("世界")
	search.Update(SearchSource{Lines: []string{"hello 世界!"}, Width: 40, Generation: 1})
	matches := search.Matches()
	if len(matches) != 1 {
		t.Fatalf("expected unicode match, got %d", len(matches))
	}
	segment := matches[0].Segments[0]
	if segment.Start != 6 || segment.End != 10 {
		t.Fatalf("wide rune cells wrong: %+v", segment)
	}
}

func TestTranscriptSearchLimitsAndStatus(t *testing.T) {
	search := NewTranscriptSearch()
	search.maxMatches = 2
	search.SetQuery("a")
	search.Update(SearchSource{Lines: []string{"a a a a"}, Width: 40, Generation: 1})
	if len(search.Matches()) != 2 {
		t.Fatalf("expected limit of 2, got %d", len(search.Matches()))
	}
	if !search.Truncated() {
		t.Fatal("expected truncation flag")
	}
	if !strings.Contains(search.Status(), "limit reached") {
		t.Fatalf("expected explicit limit message, got %q", search.Status())
	}
}

func TestTranscriptSearchInvalidatesOnGenerationWidthExpansion(t *testing.T) {
	search := NewTranscriptSearch()
	search.SetQuery("needle")
	source := SearchSource{Lines: []string{"needle"}, Width: 40, Generation: 1}
	if !search.Update(source) {
		t.Fatal("first update should rebuild")
	}
	if search.Update(source) {
		t.Fatal("identical update should not rebuild")
	}
	source.Generation = 2
	if !search.Update(source) {
		t.Fatal("generation change should rebuild")
	}
	source.Width = 80
	if !search.Update(source) {
		t.Fatal("width change should rebuild")
	}
	source.Expansion = "tools"
	if !search.Update(source) {
		t.Fatal("expansion change should rebuild")
	}
	if search.Update(source) {
		t.Fatal("repeat update should not rebuild")
	}
}

func TestTranscriptSearchNavigationAndQueryChange(t *testing.T) {
	search := NewTranscriptSearch()
	search.SetQuery("x")
	search.Update(SearchSource{Lines: []string{"x x x"}, Width: 40, Generation: 1})
	if search.Current() != 0 {
		t.Fatalf("unexpected current %d", search.Current())
	}
	if !search.SelectNext() || search.Current() != 1 {
		t.Fatal("SelectNext failed")
	}
	if !search.SelectNext() || search.Current() != 2 {
		t.Fatal("SelectNext wrap failed")
	}
	if !search.SelectNext() || search.Current() != 0 {
		t.Fatal("SelectNext should wrap")
	}
	if !search.SelectPrevious() || search.Current() != 2 {
		t.Fatal("SelectPrevious should wrap backwards")
	}
	if !search.SetQuery("x x") {
		t.Fatal("query change should report true")
	}
	if search.SetQuery("x x") {
		t.Fatal("same query should report false")
	}
}

func TestTranscriptSearchEmptyQueryAndNoMatches(t *testing.T) {
	search := NewTranscriptSearch()
	search.Update(SearchSource{Lines: []string{"anything"}, Width: 40, Generation: 1})
	if search.Active() {
		t.Fatal("empty query should not be active")
	}
	if search.Status() != "type to search" {
		t.Fatalf("unexpected status %q", search.Status())
	}
	search.SetQuery("missing")
	search.Rebuild()
	if search.Status() != "no matches" {
		t.Fatalf("unexpected status %q", search.Status())
	}
	if search.SelectNext() || search.SelectPrevious() {
		t.Fatal("navigation without matches should fail")
	}
}

func TestTranscriptSearchQuerySanitization(t *testing.T) {
	search := NewTranscriptSearch()
	search.SetQuery("\x1b[31mred\x07\ttext")
	if search.Query() != "red text" {
		t.Fatalf("query not sanitized: %q", search.Query())
	}
	long := strings.Repeat("a", searchMaxQueryLength+50)
	search.SetQuery(long)
	if len(search.Query()) != searchMaxQueryLength {
		t.Fatalf("query length not bounded: %d", len(search.Query()))
	}
}

func TestReplaceColumnRange(t *testing.T) {
	line := "\x1b[31mhello world\x1b[0m"
	replaced := replaceColumnRange(line, 6, 11, "[WORLD]")
	if VisibleWidth(replaced) != VisibleWidth(line)+2 {
		t.Fatalf("visible width changed unexpectedly: %q", replaced)
	}
	if !strings.Contains(StripTerminalSequences(replaced), "[WORLD]") {
		t.Fatalf("replacement missing: %q", replaced)
	}
	if !strings.Contains(StripTerminalSequences(replaced), "hello ") {
		t.Fatalf("prefix lost: %q", replaced)
	}
}

func TestSearchOverlayEditingAndActions(t *testing.T) {
	search := NewTranscriptSearch()
	changes := 0
	closes := 0
	selects := 0
	overlay := NewSearchOverlay(SearchOverlayOptions{
		Keybindings: NewDefaultKeybindingsManager(nil),
		Search:      search,
		OnChange:    func() { changes++ },
		OnSelect:    func() { selects++ },
		OnClose:     func() { closes++ },
	})
	overlay.HandleInput("h")
	overlay.HandleInput("i")
	if search.Query() != "hi" {
		t.Fatalf("typing failed: %q", search.Query())
	}
	search.Update(SearchSource{Lines: []string{"hi hi"}, Width: 40, Generation: 1})
	overlay.HandleInput("\x7f")
	if search.Query() != "h" {
		t.Fatalf("backspace failed: %q", search.Query())
	}
	overlay.HandleInput("\x1b")
	if closes != 1 {
		t.Fatalf("escape should close: %d", closes)
	}
	search.SetQuery("hi")
	search.Rebuild()
	overlay.HandleInput("\r")
	if selects != 1 {
		t.Fatalf("enter should select next: %d", selects)
	}
	if overlay.Render(20) == nil {
		t.Fatal("overlay should render")
	}
	if changes == 0 {
		t.Fatal("expected change notifications")
	}
}

func TestSearchOverlayRenderPadsWidth(t *testing.T) {
	search := NewTranscriptSearch()
	search.SetQuery("x")
	overlay := NewSearchOverlay(SearchOverlayOptions{Search: search})
	lines := overlay.Render(30)
	if len(lines) != 1 || VisibleWidth(lines[0]) != 30 {
		t.Fatalf("overlay line width wrong: %q", lines)
	}
}

func TestSearchGenerationStableForSameContent(t *testing.T) {
	first := searchGeneration([]string{"a", "b"}, 40)
	second := searchGeneration([]string{"a", "b"}, 40)
	if first != second {
		t.Fatal("generation should be stable")
	}
	if first == searchGeneration([]string{"a", "b"}, 80) {
		t.Fatal("generation should include width")
	}
	if first == searchGeneration([]string{"a", "c"}, 40) {
		t.Fatal("generation should include content")
	}
}

func TestSearchOverlayCursorMovementAndEditing(t *testing.T) {
	search := NewTranscriptSearch()
	overlay := NewSearchOverlay(SearchOverlayOptions{Search: search, PromptStyle: func(text string) string { return ">" + text }})
	overlay.HandleInput("a")
	overlay.HandleInput("b")
	overlay.HandleInput("c")
	overlay.HandleInput("\x1b[D")
	overlay.HandleInput("X")
	if search.Query() != "abXc" {
		t.Fatalf("cursor insert failed: %q", search.Query())
	}
	overlay.HandleInput("\x01")
	overlay.HandleInput("Z")
	if search.Query() != "ZabXc" {
		t.Fatalf("home insert failed: %q", search.Query())
	}
	overlay.HandleInput("\x05")
	overlay.HandleInput("!")
	if search.Query() != "ZabXc!" {
		t.Fatalf("end insert failed: %q", search.Query())
	}
	overlay.HandleInput("\x15")
	if search.Query() != "" {
		t.Fatalf("ctrl+u did not clear: %q", search.Query())
	}
	if rendered := overlay.Render(20); !strings.Contains(rendered[0], ">") {
		t.Fatalf("prompt style missing: %q", rendered)
	}
}

func TestSearchOverlayIgnoresUnhandledInput(t *testing.T) {
	search := NewTranscriptSearch()
	search.SetQuery("seed")
	overlay := NewSearchOverlay(SearchOverlayOptions{Search: search})
	overlay.HandleInput("\x1b[A")
	overlay.HandleInput("\x1b[<0;1;1M")
	if search.Query() != "seed" {
		t.Fatalf("unhandled input changed the query: %q", search.Query())
	}
}

func TestSearchOverlayNavigationWithoutMatches(t *testing.T) {
	search := NewTranscriptSearch()
	changes := 0
	overlay := NewSearchOverlay(SearchOverlayOptions{Search: search, OnChange: func() { changes++ }})
	overlay.HandleInput("\r")
	overlay.HandleInput("\x1b[13;2u")
	if changes == 0 {
		t.Fatal("navigation without matches should still notify a change")
	}
}

func TestSearchOverlayBackspaceAtStart(t *testing.T) {
	search := NewTranscriptSearch()
	overlay := NewSearchOverlay(SearchOverlayOptions{Search: search})
	overlay.HandleInput("\x7f")
	if search.Query() != "" {
		t.Fatalf("backspace at start changed the query: %q", search.Query())
	}
	search.SetQuery("abc")
	overlay2 := NewSearchOverlay(SearchOverlayOptions{Search: search})
	overlay2.HandleInput("\x7f")
	if search.Query() != "ab" {
		t.Fatalf("backspace failed: %q", search.Query())
	}
}

func TestClampAndPrintableText(t *testing.T) {
	if clamp(-1, 0, 5) != 0 || clamp(9, 0, 5) != 5 || clamp(3, 0, 5) != 3 {
		t.Fatal("clamp bounds wrong")
	}
	if !isPrintableText("hello") {
		t.Fatal("plain text should be printable")
	}
	if isPrintableText("\x1b[A") || isPrintableText("a\tb") || isPrintableText("") {
		t.Fatal("escape and empty text should be rejected")
	}
}
