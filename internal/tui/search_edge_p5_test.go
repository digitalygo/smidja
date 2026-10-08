package tui

import (
	"strings"
	"testing"
)

func TestTranscriptSearchNearestAnchorFallback(t *testing.T) {
	search := NewTranscriptSearch()
	search.Update(SearchSource{Lines: []string{"zero", "match here", "middle", "match there"}, Width: 40, Generation: 1})
	search.SetQuery("match")
	if search.Current() != 0 {
		t.Fatalf("expected first match, got %d", search.Current())
	}
	search.Update(SearchSource{Lines: []string{"match here", "middle", "match there"}, Width: 40, Generation: 2})
	match, ok := search.CurrentMatch()
	if !ok || match.Segments[0].Line != 0 {
		t.Fatalf("nearest anchor fallback failed: %+v", match.Segments)
	}
}

func TestTranscriptSearchNonFoldedFallback(t *testing.T) {
	search := NewTranscriptSearch()
	search.SetQuery("ẞ")
	search.Update(SearchSource{Lines: []string{"aẞb"}, Width: 40, Generation: 1})
	match, ok := search.CurrentMatch()
	if !ok {
		t.Fatal("non-folded query should match")
	}
	segment := match.Segments[0]
	if segment.Start != 1 || segment.End != 2 {
		t.Fatalf("bad non-folded cells: %+v", segment)
	}
}

func TestSearchOverlayGraphemeForwardMovement(t *testing.T) {
	search := NewTranscriptSearch()
	overlay := newSearchTestOverlay(search, nil)
	overlay.HandleInput("e")
	overlay.HandleInput("\u0301")
	overlay.HandleInput("x")
	overlay.HandleInput("\x1b[H")
	overlay.HandleInput("\x1b[C")
	overlay.HandleInput("!")
	if search.Query() != "e\u0301!x" {
		t.Fatalf("forward grapheme movement failed: %q", search.Query())
	}
}

func TestSearchOverlayRejectsControlText(t *testing.T) {
	search := NewTranscriptSearch()
	overlay := newSearchTestOverlay(search, nil)
	overlay.HandleInput("ab")
	if search.Query() != "ab" {
		t.Fatalf("multi-byte printable insert failed: %q", search.Query())
	}
	overlay.HandleInput("a\tb")
	if search.Query() != "ab" {
		t.Fatalf("control text must be rejected: %q", search.Query())
	}
}

func TestApplySearchCellStyleGuards(t *testing.T) {
	line := "abc"
	if got := applySearchCellStyle(line, 2, 2, func(text string) string { return text }); got != line {
		t.Fatalf("empty range should be a no-op: %q", got)
	}
	if got := applySearchCellStyle("", 0, 5, func(text string) string { return text }); got != "" {
		t.Fatalf("empty line should be a no-op: %q", got)
	}
	if got := applySearchCellStyle(line, -4, 99, func(text string) string { return "[" + text + "]" }); !strings.Contains(got, "[abc]") {
		t.Fatalf("out-of-range clamp failed: %q", got)
	}
}

func TestReplaceColumnRangeGuards(t *testing.T) {
	line := "hello"
	if got := replaceColumnRange(line, 3, 3, "x"); got != line {
		t.Fatalf("empty range should be a no-op: %q", got)
	}
	if got := replaceColumnRange(line, -3, 2, "X"); !strings.Contains(StripTerminalSequences(got), "Xllo") {
		t.Fatalf("negative start clamp failed: %q", got)
	}
	if got := replaceColumnRange(line, 3, 99, ""); StripTerminalSequences(got) != "hel" {
		t.Fatalf("overshoot end clamp failed: %q", got)
	}
}

func TestGraphemeBoundaries(t *testing.T) {
	if got := graphemeStartBefore("abc", 0); got != 0 {
		t.Fatalf("start before zero: %d", got)
	}
	if got := graphemeStartBefore("abc", 99); got != 2 {
		t.Fatalf("start before overshoot: %d", got)
	}
	if got := graphemeEndAfter("abc", -5); got != 1 {
		t.Fatalf("end after negative: %d", got)
	}
	if got := graphemeEndAfter("abc", 99); got != 3 {
		t.Fatalf("end after overshoot: %d", got)
	}
}

func TestSearchActiveAccessorAndNilGuards(t *testing.T) {
	search := NewTranscriptSearch()
	overlay := NewSearchOverlay(SearchOverlayOptions{Search: search})
	overlay.Invalidate()
	if overlay.WantsKeyRelease() {
		t.Fatal("search overlay should not opt into key releases")
	}
	overlay.HandleInput("\x1b")
	overlay.moveCursor(-1)
	overlay.moveCursor(1)
	overlay.deleteBackward()
	if search.Query() != "" {
		t.Fatalf("nil-callback editing should be safe: %q", search.Query())
	}
	empty := NewSearchOverlay(SearchOverlayOptions{})
	empty.HandleInput("a")
	empty.HandleInput("\r")
	if lines := empty.Render(10); len(lines) != 1 {
		t.Fatalf("nil search render failed: %v", lines)
	}
	noSearch := &SearchOverlay{keybindings: NewDefaultKeybindingsManager(nil)}
	noSearch.insertText("a")
	noSearch.close()
	if query := noSearch.queryText(); query != "" {
		t.Fatalf("nil search query: %q", query)
	}
}

func TestAltScreenSearchActiveAccessor(t *testing.T) {
	screen, _ := newTestAltScreen(t, 30, 6)
	screen.AddChild(&plainComponent{lines: []string{"content"}})
	screen.Start()
	screen.RenderNow(true)
	if screen.SearchActive() {
		t.Fatal("search should start inactive")
	}
	screen.openSearch()
	if !screen.SearchActive() {
		t.Fatal("search accessor should report active")
	}
	screen.CloseSearch()
	if screen.SearchActive() {
		t.Fatal("search accessor should report inactive after close")
	}
	screen.Stop(StopOptions{})
}
