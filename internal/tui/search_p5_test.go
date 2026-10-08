package tui

import (
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

func newSearchTestOverlay(search *TranscriptSearch, onChange func()) *SearchOverlay {
	return NewSearchOverlay(SearchOverlayOptions{
		Keybindings: NewDefaultKeybindingsManager(nil),
		Search:      search,
		OnChange:    onChange,
	})
}

func TestSearchOverlayKittyPrintablePressRepeatRelease(t *testing.T) {
	search := NewTranscriptSearch()
	overlay := newSearchTestOverlay(search, nil)
	overlay.HandleInput("\x1b[97u")
	if search.Query() != "a" {
		t.Fatalf("kitty press failed: %q", search.Query())
	}
	overlay.HandleInput("\x1b[97;1:2u")
	if search.Query() != "aa" {
		t.Fatalf("kitty repeat failed: %q", search.Query())
	}
	overlay.HandleInput("\x1b[97;1:3u")
	if search.Query() != "aa" {
		t.Fatalf("kitty release must be ignored: %q", search.Query())
	}
	overlay.HandleInput("\x1b[97:65;2u")
	if search.Query() != "aaA" {
		t.Fatalf("kitty shifted printable failed: %q", search.Query())
	}
}

func TestSearchOverlayBracketedPaste(t *testing.T) {
	search := NewTranscriptSearch()
	overlay := newSearchTestOverlay(search, nil)
	overlay.HandleInput("\x1b[200~hello\nworld\x1b[201~")
	if search.Query() != "hello world" {
		t.Fatalf("single-chunk paste failed: %q", search.Query())
	}
	overlay.HandleInput("\x15")
	if search.Query() != "" {
		t.Fatalf("ctrl+u did not clear: %q", search.Query())
	}
	overlay.HandleInput("\x1b[200~ab")
	if search.Query() != "" {
		t.Fatalf("partial paste must not commit: %q", search.Query())
	}
	overlay.HandleInput("cd\x1b[201~")
	if search.Query() != "abcd" {
		t.Fatalf("chunked paste failed: %q", search.Query())
	}
}

func TestSearchOverlayPasteBudgetAndExcessData(t *testing.T) {
	search := NewTranscriptSearch()
	overlay := newSearchTestOverlay(search, nil)
	oversized := strings.Repeat("a", searchPasteMaxBytes*2)
	overlay.HandleInput(BracketedPasteStart + oversized + BracketedPasteEnd)
	query := search.Query()
	if len(query) != searchPasteMaxBytes {
		t.Fatalf("oversized paste kept %d bytes, want %d", len(query), searchPasteMaxBytes)
	}
	if query != strings.Repeat("a", searchPasteMaxBytes) {
		t.Fatal("oversized paste did not keep the leading content")
	}
	overlay.HandleInput("\x15")
	if search.Query() != "" {
		t.Fatalf("clear failed after oversized paste: %q", search.Query())
	}
	overlay.HandleInput("z")
	if search.Query() != "z" {
		t.Fatalf("input after an oversized paste was lost: %q", search.Query())
	}
}

func TestSearchOverlayExcessDataBeforeEndMarker(t *testing.T) {
	search := NewTranscriptSearch()
	overlay := newSearchTestOverlay(search, nil)
	overlay.HandleInput(BracketedPasteStart + strings.Repeat("b", searchPasteMaxBytes*2))
	if search.Query() != "" {
		t.Fatalf("unterminated oversized paste committed: %q", search.Query())
	}
	overlay.HandleInput(BracketedPasteEnd)
	query := search.Query()
	if len(query) != searchPasteMaxBytes || query != strings.Repeat("b", searchPasteMaxBytes) {
		t.Fatalf("oversized paste with delayed marker kept %d bytes", len(query))
	}
	overlay.HandleInput("\x15")
	overlay.HandleInput("q")
	if search.Query() != "q" {
		t.Fatalf("input after delayed marker was lost: %q", search.Query())
	}
}

func TestSearchOverlayPasteSplitEndMarkerAcrossChunks(t *testing.T) {
	search := NewTranscriptSearch()
	overlay := newSearchTestOverlay(search, nil)
	overlay.HandleInput(BracketedPasteStart + "ab")
	overlay.HandleInput("cd\x1b[20")
	if search.Query() != "" {
		t.Fatalf("partial marker committed: %q", search.Query())
	}
	overlay.HandleInput("1~")
	if search.Query() != "abcd" {
		t.Fatalf("split end marker failed: %q", search.Query())
	}
}

func TestSearchOverlayPasteUnicodeBudget(t *testing.T) {
	search := NewTranscriptSearch()
	overlay := newSearchTestOverlay(search, nil)
	overlay.HandleInput(BracketedPasteStart + strings.Repeat("é", searchPasteMaxBytes) + BracketedPasteEnd)
	query := search.Query()
	if !utf8.ValidString(query) {
		t.Fatalf("paste budget split a rune: %q", query)
	}
	if len(query) != searchPasteMaxBytes {
		t.Fatalf("unicode paste kept %d bytes, want %d", len(query), searchPasteMaxBytes)
	}
	if utf8.RuneCountInString(query) != searchPasteMaxBytes/2 {
		t.Fatalf("unicode paste kept %d runes, want %d", utf8.RuneCountInString(query), searchPasteMaxBytes/2)
	}
}

func TestSearchOverlayResetClearsInterruptedPaste(t *testing.T) {
	search := NewTranscriptSearch()
	overlay := newSearchTestOverlay(search, nil)
	overlay.HandleInput(BracketedPasteStart + "partial")
	overlay.Reset()
	overlay.HandleInput("x")
	if search.Query() != "x" {
		t.Fatalf("reset retained the interrupted paste: %q", search.Query())
	}
	overlay.HandleInput(BracketedPasteStart + "full" + BracketedPasteEnd)
	if search.Query() != "xfull" {
		t.Fatalf("paste after reset failed: %q", search.Query())
	}
}

func TestSearchOverlayCallbacksCanReenter(t *testing.T) {
	search := NewTranscriptSearch()
	var overlay *SearchOverlay
	overlay = NewSearchOverlay(SearchOverlayOptions{
		Keybindings: NewDefaultKeybindingsManager(nil),
		Search:      search,
		OnChange: func() {
			overlay.Reset()
			_ = overlay.Render(20)
		},
	})
	overlay.HandleInput("a")
	if search.Query() != "a" {
		t.Fatalf("reentrant callback lost the query: %q", search.Query())
	}
}

func TestSearchOverlayConcurrentPasteAndReset(t *testing.T) {
	search := NewTranscriptSearch()
	overlay := newSearchTestOverlay(search, nil)
	var wait sync.WaitGroup
	for index := 0; index < 32; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			overlay.HandleInput(BracketedPasteStart)
			overlay.HandleInput(strings.Repeat("z", 40))
			overlay.HandleInput(BracketedPasteEnd)
		}()
	}
	for index := 0; index < 32; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			overlay.Reset()
			_ = overlay.Render(20)
		}()
	}
	wait.Wait()
	query := search.Query()
	if !utf8.ValidString(query) {
		t.Fatalf("concurrent paste corrupted the query: %q", query)
	}
	if len(query) > searchPasteMaxBytes {
		t.Fatalf("concurrent paste exceeded the budget: %d bytes", len(query))
	}
}

func TestSearchOverlayRealKeySequences(t *testing.T) {
	search := NewTranscriptSearch()
	overlay := newSearchTestOverlay(search, nil)
	overlay.HandleInput("abc")
	overlay.HandleInput("\x1b[D")
	overlay.HandleInput("X")
	if search.Query() != "abXc" {
		t.Fatalf("arrow left failed: %q", search.Query())
	}
	overlay.HandleInput("\x1b[H")
	overlay.HandleInput("Z")
	if search.Query() != "ZabXc" {
		t.Fatalf("home failed: %q", search.Query())
	}
	overlay.HandleInput("\x1b[F")
	overlay.HandleInput("!")
	if search.Query() != "ZabXc!" {
		t.Fatalf("end failed: %q", search.Query())
	}
	overlay.HandleInput("\x15")
	if search.Query() != "" {
		t.Fatalf("ctrl+u failed: %q", search.Query())
	}
	overlay.HandleInput("q")
	overlay.HandleInput("\x7f")
	if search.Query() != "" {
		t.Fatalf("backspace failed: %q", search.Query())
	}
}

func TestSearchOverlayGraphemeEditing(t *testing.T) {
	search := NewTranscriptSearch()
	overlay := newSearchTestOverlay(search, nil)
	overlay.HandleInput("e")
	overlay.HandleInput("\u0301")
	if search.Query() != "e\u0301" {
		t.Fatalf("combining input failed: %q", search.Query())
	}
	overlay.HandleInput("\x7f")
	if search.Query() != "" {
		t.Fatalf("backspace must remove the whole grapheme: %q", search.Query())
	}
	overlay.HandleInput("e")
	overlay.HandleInput("\u0301")
	overlay.HandleInput("x")
	overlay.HandleInput("\x1b[D")
	overlay.HandleInput("!")
	if search.Query() != "e\u0301!x" {
		t.Fatalf("grapheme cursor move failed: %q", search.Query())
	}
}

func TestSearchOverlayConcurrentInputRender(t *testing.T) {
	search := NewTranscriptSearch()
	search.Update(SearchSource{Lines: []string{"alpha", "beta"}, Width: 40, Generation: 1})
	overlay := newSearchTestOverlay(search, nil)
	var wait sync.WaitGroup
	for index := 0; index < 64; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			overlay.HandleInput("a")
		}()
	}
	for index := 0; index < 64; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_ = overlay.Render(20)
		}()
	}
	wait.Wait()
}

func TestTranscriptSearchRebuildsBeforeNavigation(t *testing.T) {
	search := NewTranscriptSearch()
	search.Update(SearchSource{Lines: []string{"aaa", "bbb", "aaa"}, Width: 40, Generation: 1})
	selects := 0
	overlay := NewSearchOverlay(SearchOverlayOptions{
		Keybindings: NewDefaultKeybindingsManager(nil),
		Search:      search,
		OnSelect:    func() { selects++ },
	})
	overlay.HandleInput("a")
	if search.MatchCount() != 6 {
		t.Fatalf("single-character matches: %d", search.MatchCount())
	}
	overlay.HandleInput("a")
	if search.MatchCount() != 2 {
		t.Fatalf("double-character matches: %d", search.MatchCount())
	}
	overlay.HandleInput("a")
	if search.MatchCount() != 2 {
		t.Fatalf("triple-character matches: %d", search.MatchCount())
	}
	overlay.HandleInput("\r")
	if selects != 1 || search.Current() != 1 {
		t.Fatalf("select after query change: selects=%d current=%d", selects, search.Current())
	}
	match, ok := search.CurrentMatch()
	if !ok || match.Segments[0].Line != 2 {
		t.Fatalf("stale match selected: %+v", match.Segments)
	}
}

func TestTranscriptSearchPreservesCurrentMatchAcrossStreaming(t *testing.T) {
	search := NewTranscriptSearch()
	search.Update(SearchSource{Lines: []string{"match one", "other", "match two"}, Width: 40, Generation: 1})
	search.SetQuery("match")
	search.SelectNext()
	if search.Current() != 1 {
		t.Fatalf("expected second match, got %d", search.Current())
	}
	search.Update(SearchSource{Lines: []string{"match one", "other", "match two", "streamed"}, Width: 40, Generation: 2})
	match, ok := search.CurrentMatch()
	if !ok || match.Segments[0].Line != 2 {
		t.Fatalf("streaming lost the current match: %+v", match.Segments)
	}
	search.SetQuery("stream")
	match, ok = search.CurrentMatch()
	if !ok || match.Segments[0].Line != 3 {
		t.Fatalf("query change should reset to the new match: %+v", match.Segments)
	}
}

func TestBuildSearchMatchesGraphemeCellMapping(t *testing.T) {
	cases := []struct {
		name       string
		line       string
		query      string
		start, end int
	}{
		{"family", "a👨‍👩‍👧b", "👨‍👩‍👧", 1, 3},
		{"flag", "a🇯🇵b", "🇯🇵", 1, 3},
		{"combining", "xe\u0301y", "e\u0301", 1, 2},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			search := NewTranscriptSearch()
			search.SetQuery(testCase.query)
			search.Update(SearchSource{Lines: []string{testCase.line}, Width: 40, Generation: 1})
			match, ok := search.CurrentMatch()
			if !ok {
				t.Fatalf("no match for %q", testCase.query)
			}
			segment := match.Segments[0]
			if segment.Start != testCase.start || segment.End != testCase.end {
				t.Fatalf("bad cells %+v", segment)
			}
		})
	}
}

func TestBuildSearchMatchesEmptyLinesDoNotConcatenate(t *testing.T) {
	search := NewTranscriptSearch()
	search.SetQuery("abcdef")
	search.Update(SearchSource{Lines: []string{"abc", "", "def"}, Width: 40, Generation: 1})
	if search.MatchCount() != 0 {
		t.Fatalf("empty line concatenated unrelated text: %d", search.MatchCount())
	}
	search.SetQuery("c d")
	if search.MatchCount() != 0 {
		t.Fatalf("separator collapsed into a single space: %d", search.MatchCount())
	}
	search.SetQuery("def")
	match, ok := search.CurrentMatch()
	if !ok || match.Segments[0].Line != 2 {
		t.Fatalf("match after the empty line missing: %+v", match.Segments)
	}
}

func TestTranscriptSearchSourceLimitExplicit(t *testing.T) {
	search := NewTranscriptSearch()
	search.maxSource = 4
	search.SetQuery("abcde")
	search.Update(SearchSource{Lines: []string{"abcd", "efgh"}, Width: 40, Generation: 1})
	if search.MatchCount() != 0 {
		t.Fatalf("match beyond the source limit: %d", search.MatchCount())
	}
	if !search.SourceTruncated() {
		t.Fatal("source limit status missing")
	}
	if !strings.Contains(search.Status(), "source limit reached") {
		t.Fatalf("unexpected status %q", search.Status())
	}
	search.SetQuery("abcd")
	if search.MatchCount() != 1 {
		t.Fatalf("kept prefix should still match: %d", search.MatchCount())
	}
}

func TestTranscriptSearchSourceLimitStopsEarly(t *testing.T) {
	lines := make([]string, 100000)
	for index := range lines {
		lines[index] = "x"
	}
	search := NewTranscriptSearch()
	search.maxSource = 8
	search.SetQuery("x")
	search.Update(SearchSource{Lines: lines, Width: 40, Generation: 1})
	if !search.SourceTruncated() {
		t.Fatal("huge source should report truncation")
	}
	if search.MatchCount() == 0 || search.MatchCount() > search.maxMatches {
		t.Fatalf("bounded match count: %d", search.MatchCount())
	}
}

func TestTranscriptSearchQueryLimitExplicit(t *testing.T) {
	search := NewTranscriptSearch()
	search.SetQuery(strings.Repeat("a", searchMaxQueryLength+10))
	search.Update(SearchSource{Lines: []string{strings.Repeat("a", searchMaxQueryLength+10)}, Width: 40, Generation: 1})
	if !search.QueryTruncated() {
		t.Fatal("query limit status missing")
	}
	if search.MatchCount() != 1 {
		t.Fatalf("truncated query should match: %d", search.MatchCount())
	}
	if !strings.Contains(search.Status(), "query limit reached") {
		t.Fatalf("unexpected status %q", search.Status())
	}
}

func TestTranscriptSearchMatchLimitExplicit(t *testing.T) {
	search := NewTranscriptSearch()
	search.maxMatches = 2
	search.SetQuery("a")
	search.Update(SearchSource{Lines: []string{"a a a a"}, Width: 40, Generation: 1})
	if len(search.Matches()) != 2 {
		t.Fatalf("match limit not enforced: %d", len(search.Matches()))
	}
	if !search.Truncated() {
		t.Fatal("match limit status missing")
	}
	if !strings.Contains(search.Status(), "showing first 2, limit reached") {
		t.Fatalf("unexpected status %q", search.Status())
	}
}

func TestApplySearchCellStyleOverridesEmbeddedANSI(t *testing.T) {
	line := "\x1b[31mhello\x1b[0m world"
	styled := applySearchCellStyle(line, 0, 5, func(text string) string { return "\x1b[45m" + text + "\x1b[49m" })
	if !strings.Contains(styled, "\x1b[45mhello\x1b[49m") {
		t.Fatalf("search style not applied: %q", styled)
	}
	if strings.Contains(styled, "\x1b[31mhello") {
		t.Fatalf("embedded markdown color survived: %q", styled)
	}
	if VisibleWidth(styled) != VisibleWidth(line) {
		t.Fatalf("cell width changed: %d != %d", VisibleWidth(styled), VisibleWidth(line))
	}
}

func TestModalCaptureSources(t *testing.T) {
	terminal := newFakeTerminal(20, 5)
	base := NewBase(terminal, false, "regular")
	base.SetModalCaptureSource("a", true)
	base.SetModalCaptureSource("b", true)
	if !base.ModalCapture() {
		t.Fatal("any source should enable capture")
	}
	base.SetModalCaptureSource("a", false)
	if !base.ModalCapture() {
		t.Fatal("releasing one source must keep the other")
	}
	base.SetModalCaptureSource("a", false)
	if !base.ModalCapture() {
		t.Fatal("idempotent release must not drop the other source")
	}
	base.SetModalCaptureSource("b", false)
	if base.ModalCapture() {
		t.Fatal("all sources released should disable capture")
	}
	base.SetModalCapture(true)
	if !base.ModalCapture() {
		t.Fatal("legacy wrapper should enable the dialog source")
	}
	base.SetModalCapture(false)
	if base.ModalCapture() {
		t.Fatal("legacy wrapper should release the dialog source")
	}
}
