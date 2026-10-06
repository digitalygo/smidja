package tui

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"strings"
	"sync"
)

const (
	searchDefaultMaxMatches = 500
	searchDefaultMaxSource  = 1 << 20
	searchMaxQueryLength    = 256
	searchPreviewLength     = 80
)

type SearchLineSpan struct {
	Line  int
	Start int
	End   int
}

type SearchMatch struct {
	Segments []SearchLineSpan
	Preview  string
}

type SearchSource struct {
	Lines      []string
	Width      int
	Generation uint64
	Expansion  string
}

type searchLimits struct {
	source  bool
	matches bool
}

type searchAnchor struct {
	line   int
	column int
	index  int
}

type TranscriptSearch struct {
	mu               sync.Mutex
	query            string
	matches          []SearchMatch
	current          int
	truncatedSource  bool
	truncatedMatches bool
	queryTruncated   bool
	cached           SearchSource
	hasSource        bool
	valid            bool
	maxMatches       int
	maxSource        int
}

func NewTranscriptSearch() *TranscriptSearch {
	return &TranscriptSearch{maxMatches: searchDefaultMaxMatches, maxSource: searchDefaultMaxSource}
}

func (s *TranscriptSearch) SetQuery(query string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	cleaned, truncated := sanitizeSearchQuery(query)
	if cleaned == s.query {
		return false
	}
	s.query = cleaned
	s.queryTruncated = truncated
	s.rebuildLocked(false)
	return true
}

func (s *TranscriptSearch) Query() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.query
}

func (s *TranscriptSearch) Active() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.query != ""
}

func (s *TranscriptSearch) Update(source SearchSource) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.valid && s.hasSource && s.cached.Width == source.Width && s.cached.Generation == source.Generation &&
		s.cached.Expansion == source.Expansion && len(s.cached.Lines) == len(source.Lines) {
		return false
	}
	s.cached = SearchSource{Lines: append([]string(nil), source.Lines...), Width: source.Width, Generation: source.Generation, Expansion: source.Expansion}
	s.hasSource = true
	s.rebuildLocked(true)
	return true
}

func (s *TranscriptSearch) Rebuild() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rebuildLocked(true)
}

func (s *TranscriptSearch) rebuildLocked(preserve bool) {
	s.truncatedSource = false
	s.truncatedMatches = false
	s.valid = true
	if s.query == "" {
		s.matches = nil
		s.current = 0
		return
	}
	anchor := searchAnchor{}
	hasAnchor := preserve && len(s.matches) > 0 && s.current >= 0 && s.current < len(s.matches) &&
		len(s.matches[s.current].Segments) > 0
	if hasAnchor {
		segment := s.matches[s.current].Segments[0]
		anchor = searchAnchor{line: segment.Line, column: segment.Start, index: s.current}
	}
	matches, limits := buildSearchMatches(s.cached.Lines, s.query, s.maxMatches, s.maxSource)
	s.matches = matches
	s.truncatedSource = limits.source
	s.truncatedMatches = limits.matches
	s.current = 0
	if hasAnchor {
		s.current = matchIndexNear(matches, anchor, anchor.index)
	}
}

func matchIndexNear(matches []SearchMatch, anchor searchAnchor, fallback int) int {
	if len(matches) == 0 {
		return 0
	}
	for index, match := range matches {
		if len(match.Segments) > 0 && match.Segments[0].Line == anchor.line && match.Segments[0].Start == anchor.column {
			return index
		}
	}
	best := -1
	bestDistance := 0
	for index, match := range matches {
		if len(match.Segments) == 0 {
			continue
		}
		segment := match.Segments[0]
		distance := absoluteDiff(segment.Line, anchor.line)*1_000_000 + absoluteDiff(segment.Start, anchor.column)
		if best < 0 || distance < bestDistance {
			best = index
			bestDistance = distance
		}
	}
	if best >= 0 {
		return best
	}
	return maxInt(0, minInt(fallback, len(matches)-1))
}

func absoluteDiff(a, b int) int {
	if a > b {
		return a - b
	}
	return b - a
}

func (s *TranscriptSearch) Matches() []SearchMatch {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]SearchMatch(nil), s.matches...)
}

func (s *TranscriptSearch) MatchCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.matches)
}

func (s *TranscriptSearch) Current() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current
}

func (s *TranscriptSearch) Truncated() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.truncatedSource || s.truncatedMatches
}

func (s *TranscriptSearch) SourceTruncated() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.truncatedSource
}

func (s *TranscriptSearch) QueryTruncated() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.queryTruncated
}

func (s *TranscriptSearch) CurrentMatch() (SearchMatch, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.matches) == 0 {
		return SearchMatch{}, false
	}
	return s.matches[s.current], true
}

func (s *TranscriptSearch) SelectNext() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.matches) == 0 {
		return false
	}
	s.current = (s.current + 1) % len(s.matches)
	return true
}

func (s *TranscriptSearch) SelectPrevious() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.matches) == 0 {
		return false
	}
	s.current--
	if s.current < 0 {
		s.current = len(s.matches) - 1
	}
	return true
}

func (s *TranscriptSearch) Status() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.query == "" {
		return "type to search"
	}
	if len(s.matches) == 0 {
		if s.truncatedSource {
			return "no matches (source limit reached)"
		}
		return "no matches"
	}
	status := fmt.Sprintf("%d/%d", s.current+1, len(s.matches))
	notes := make([]string, 0, 3)
	if s.truncatedMatches {
		notes = append(notes, fmt.Sprintf("showing first %d, limit reached", s.maxMatches))
	}
	if s.truncatedSource {
		notes = append(notes, "source limit reached")
	}
	if s.queryTruncated {
		notes = append(notes, "query limit reached")
	}
	if len(notes) > 0 {
		status += " (" + strings.Join(notes, ", ") + ")"
	}
	return status
}

func sanitizeSearchQuery(query string) (string, bool) {
	truncated := false
	if len(query) > searchMaxQueryLength {
		query = query[:searchMaxQueryLength]
		truncated = true
	}
	cleaned := StripTerminalSequences(query)
	var builder strings.Builder
	for _, r := range cleaned {
		switch {
		case r == '\t':
			builder.WriteByte(' ')
		case r == '\n' || r == '\r':
		case r < 0x20 || r == 0x7f:
		default:
			builder.WriteRune(r)
		}
	}
	return builder.String(), truncated
}

type flatPosition struct {
	line      int
	column    int
	synthetic bool
}

func buildSearchMatches(lines []string, query string, maxMatches, maxSource int) ([]SearchMatch, searchLimits) {
	var limits searchLimits
	if query == "" {
		return nil, limits
	}
	if len(query) > searchMaxQueryLength {
		query = query[:searchMaxQueryLength]
	}
	loweredQuery := strings.ToLower(query)

	var flat strings.Builder
	positions := make([]flatPosition, 0, 256)
	lineWidths := make([]int, 0, minInt(len(lines), 256))
	lastLine := 0
	lastColumn := 0
	started := false
	for lineIndex, line := range lines {
		plain := StripTerminalSequences(normalizeTerminalOutput(line))
		separator := 0
		if started {
			separator = 1
		}
		if flat.Len()+separator+len(plain) > maxSource {
			limits.source = true
			break
		}
		if started {
			flat.WriteByte(' ')
			positions = append(positions, flatPosition{line: lineIndex - 1, column: lineWidths[lineIndex-1], synthetic: true})
		}
		column := 0
		for _, cluster := range splitGraphemes(plain) {
			flat.WriteString(cluster.text)
			for offset := 0; offset < len(cluster.text); offset++ {
				positions = append(positions, flatPosition{line: lineIndex, column: column})
			}
			column += graphemeWidth(cluster)
		}
		lineWidths = append(lineWidths, column)
		lastLine = lineIndex
		lastColumn = column
		started = true
	}
	positions = append(positions, flatPosition{line: lastLine, column: lastColumn, synthetic: true})

	flatText := flat.String()
	lowered := strings.ToLower(flatText)
	useFolded := len(lowered) == len(flatText) && len(loweredQuery) == len(query)
	limit := len(flatText)
	var matches []SearchMatch
	cursor := 0
	for len(matches) < maxMatches {
		index := -1
		if useFolded {
			if offset := strings.Index(lowered[cursor:limit], loweredQuery); offset >= 0 {
				index = cursor + offset
			}
		} else {
			index = indexFold(flatText, query, cursor, limit)
		}
		if index < 0 {
			break
		}
		start := index
		end := start + len(query)
		if end > len(flatText) {
			break
		}
		segments := searchSegments(positions, lineWidths, start, end)
		if len(segments) > 0 {
			matches = append(matches, SearchMatch{Segments: segments, Preview: searchPreview(flatText[start:end])})
		}
		cursor = start + maxInt(1, len(query))
		if cursor >= limit {
			break
		}
	}
	if len(matches) == maxMatches {
		if useFolded {
			limits.matches = strings.Contains(lowered[cursor:limit], loweredQuery)
		} else {
			limits.matches = indexFold(flatText, query, cursor, limit) >= 0
		}
	}
	return matches, limits
}

func indexFold(haystack, needle string, from, limit int) int {
	size := len(needle)
	if size == 0 {
		return -1
	}
	for index := from; index+size <= limit; index++ {
		if strings.EqualFold(haystack[index:index+size], needle) {
			return index
		}
	}
	return -1
}

func searchPreview(text string) string {
	runes := []rune(text)
	if len(runes) <= searchPreviewLength {
		return text
	}
	return string(runes[:searchPreviewLength]) + "…"
}

func searchSegments(positions []flatPosition, lineWidths []int, start, end int) []SearchLineSpan {
	if start < 0 || end > len(positions) || start >= end {
		return nil
	}
	startPosition := positions[start]
	endPosition := positions[end]
	startLine := startPosition.line
	endLine := endPosition.line
	if startLine < 0 || endLine >= len(lineWidths) {
		return nil
	}
	var segments []SearchLineSpan
	for line := startLine; line <= endLine; line++ {
		segmentStart := 0
		if line == startLine {
			segmentStart = startPosition.column
		}
		segmentEnd := lineWidths[line]
		if line == endLine {
			segmentEnd = endPosition.column
		}
		if segmentEnd > lineWidths[line] {
			segmentEnd = lineWidths[line]
		}
		if segmentEnd > segmentStart {
			segments = append(segments, SearchLineSpan{Line: line, Start: segmentStart, End: segmentEnd})
		}
	}
	return segments
}

func searchGeneration(lines []string, width int) uint64 {
	hash := fnv.New64a()
	var buffer [8]byte
	binary.LittleEndian.PutUint64(buffer[:], uint64(width))
	hash.Write(buffer[:])
	for _, line := range lines {
		hash.Write([]byte(StripTerminalSequences(line)))
		hash.Write([]byte{'\n'})
	}
	return hash.Sum64()
}

func replaceColumnRange(line string, start, end int, replacement string) string {
	total := VisibleWidth(line)
	if start < 0 {
		start = 0
	}
	if end > total {
		end = total
	}
	if end <= start {
		return line
	}
	before := sliceByColumns(line, 0, start, true)
	after := sliceByColumns(line, end, maxInt(0, total-end), true)
	padding := strings.Repeat(" ", maxInt(0, start-before.width))
	return before.text + padding + SegmentReset + replacement + after.text
}

func applySearchCellStyle(line string, start, end int, style func(text string) string) string {
	total := VisibleWidth(line)
	if start < 0 {
		start = 0
	}
	if end > total {
		end = total
	}
	if end <= start {
		return line
	}
	before := sliceByColumns(line, 0, start, true)
	segment := sliceByColumns(line, start, end-start, true)
	after := sliceByColumns(line, end, maxInt(0, total-end), true)
	plain := StripTerminalSequences(segment.text)
	if plain == "" {
		return line
	}
	padding := strings.Repeat(" ", maxInt(0, start-before.width))
	return before.text + padding + SegmentReset + style(plain) + SGRReset + after.text
}
