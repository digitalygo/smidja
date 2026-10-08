package tui

import (
	"strings"
	"sync"
	"unicode/utf8"
)

const maxSelectionClipboardBytes = 64 * 1024

const selectionImageToken = "\x00\x02"

type SelectionPoint struct {
	Line   int
	Column int
}

type Selection struct {
	mu         sync.Mutex
	active     bool
	anchor     SelectionPoint
	focus      SelectionPoint
	generation uint64
}

func NewSelection() *Selection { return &Selection{} }

func (s *Selection) Begin(point SelectionPoint, generation uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = true
	s.anchor = point
	s.focus = point
	s.generation = generation
}

func (s *Selection) Update(point SelectionPoint) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return
	}
	s.focus = point
}

func (s *Selection) End() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = false
}

func (s *Selection) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = false
	s.anchor = SelectionPoint{}
	s.focus = SelectionPoint{}
}

func (s *Selection) Active() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active && !s.isEmptyLocked()
}

func (s *Selection) isEmptyLocked() bool {
	return s.anchor == s.focus
}

func (s *Selection) Generated() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.generation
}

func (s *Selection) InvalidateOnGeneration(generation uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active && s.generation != generation {
		s.active = false
	}
	if s.generation != generation {
		s.generation = generation
	}
}

func (s *Selection) Range() (SelectionPoint, SelectionPoint, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active || s.isEmptyLocked() {
		return SelectionPoint{}, SelectionPoint{}, false
	}
	start, end := s.anchor, s.focus
	if start.Line > end.Line || (start.Line == end.Line && start.Column > end.Column) {
		start, end = end, start
	}
	return start, end, true
}

func SelectionText(lines []string, start, end SelectionPoint) string {
	if start.Line > end.Line || (start.Line == end.Line && start.Column > end.Column) {
		start, end = end, start
	}
	var parts []string
	for line := start.Line; line <= end.Line; line++ {
		if line < 0 || line >= len(lines) {
			continue
		}
		plain := plainSelectionLine(lines[line])
		segmentStart := 0
		if line == start.Line {
			segmentStart = start.Column
		}
		total := VisibleWidth(plain)
		segmentEnd := total
		if line == end.Line {
			segmentEnd = end.Column
		}
		if segmentEnd > total {
			segmentEnd = total
		}
		if segmentEnd <= segmentStart {
			parts = append(parts, "")
			continue
		}
		parts = append(parts, strings.TrimRight(SliceByColumn(plain, segmentStart, segmentEnd-segmentStart, true), " \t"))
	}
	return strings.Join(parts, "\n")
}

func plainSelectionLine(line string) string {
	stripped := StripTerminalSequences(line)
	var builder strings.Builder
	for index := 0; index < len(stripped); {
		if strings.HasPrefix(stripped[index:], selectionImageToken) {
			if end := strings.Index(stripped[index+len(selectionImageToken):], selectionImageToken); end >= 0 {
				index += len(selectionImageToken) + end + len(selectionImageToken)
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(stripped[index:])
		if size <= 0 {
			size = 1
		}
		if r < 0x20 || r == 0x7f {
			index += size
			continue
		}
		builder.WriteString(stripped[index : index+size])
		index += size
	}
	return builder.String()
}

func boundedSelectionText(text string, maxBytes int) string {
	if maxBytes <= 0 || len(text) <= maxBytes {
		return text
	}
	truncated := text[:maxBytes]
	for len(truncated) > 0 && !utf8.ValidString(truncated) {
		truncated = truncated[:len(truncated)-1]
	}
	return truncated
}
