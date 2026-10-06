package interactive

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/digitalygo/smidja/internal/tui"
)

const imageTokenBoundary = "\x00\x02"

type mdImageEntry struct {
	label      string
	descriptor tui.ImageDescriptor
}

type mdImageCollector struct {
	entries []mdImageEntry
}

func (c *mdImageCollector) register(resolved tui.ResolvedImage, source, alt string) string {
	index := len(c.entries)
	label := resolved.Label
	if label == "" {
		label = imageFallbackLabel(alt, source)
	}
	c.entries = append(c.entries, mdImageEntry{
		label: label,
		descriptor: tui.ImageDescriptor{
			RowSpan:  maxInt(1, resolved.Rows),
			Columns:  maxInt(1, resolved.Columns),
			Source:   source,
			CacheKey: resolved.CacheKey,
			Alt:      alt,
			Protocol: resolved.Protocol,
		},
	})
	return imageToken(index)
}

func imageToken(index int) string {
	return imageTokenBoundary + strconv.Itoa(index) + imageTokenBoundary
}

func nextImageToken(line string) (int, string, string, bool) {
	start := strings.Index(line, imageTokenBoundary)
	if start < 0 {
		return 0, line, "", false
	}
	rest := line[start+len(imageTokenBoundary):]
	end := strings.Index(rest, imageTokenBoundary)
	if end < 0 {
		return 0, line, "", false
	}
	index, err := strconv.Atoi(rest[:end])
	if err != nil || index < 0 {
		return 0, line, "", false
	}
	return index, line[:start], rest[end+len(imageTokenBoundary):], true
}

func (m *Markdown) expandImageTokens(lines []string, collector *mdImageCollector, width int) ([]string, []tui.ImageDescriptor) {
	if collector == nil || len(collector.entries) == 0 {
		out := make([]string, 0, len(lines))
		for _, line := range lines {
			out = append(out, tui.WrapTextWithANSI(line, width)...)
		}
		return out, nil
	}
	out := make([]string, 0, len(lines)+len(collector.entries))
	images := make([]tui.ImageDescriptor, 0, len(collector.entries))
	for _, line := range lines {
		hadToken := false
		for {
			index, before, after, ok := nextImageToken(line)
			if !ok {
				if !hadToken || line != "" {
					out = append(out, tui.WrapTextWithANSI(line, width)...)
				}
				break
			}
			hadToken = true
			if trimmed := trimSpacesRight(before); trimmed != "" {
				out = append(out, tui.WrapTextWithANSI(trimmed, width)...)
			}
			entry := collector.entries[index]
			descriptor := entry.descriptor
			descriptor.Row = len(out)
			descriptor.OffsetX = m.paddingX
			if descriptor.Columns > width {
				descriptor.Columns = width
			}
			if descriptor.Columns < 1 {
				descriptor.Columns = 1
			}
			images = append(images, descriptor)
			out = append(out, tui.TruncateToWidth(entry.label, width, "", false))
			for extra := 1; extra < maxInt(1, descriptor.RowSpan); extra++ {
				out = append(out, "")
			}
			line = trimSpacesLeft(after)
			if line == "" {
				break
			}
		}
	}
	return out, images
}

func imageFallbackLabel(alt, source string) string {
	cleaned := SanitizeSingleLine(alt)
	target := SanitizeSingleLine(SanitizeOSC8Target(strings.TrimSpace(source)))
	if cleaned == "" {
		return "[image: " + target + "]"
	}
	return "[image: " + cleaned + "] (" + target + ")"
}

func trimSpacesRight(text string) string {
	last := 0
	pos := 0
	for pos < len(text) {
		if length, ok := ansiSequenceLength(text, pos); ok {
			pos += length
			continue
		}
		r, size := utf8.DecodeRuneInString(text[pos:])
		pos += size
		if r != ' ' && r != '\t' {
			last = pos
		}
	}
	return text[:last]
}

func trimSpacesLeft(text string) string {
	var builder strings.Builder
	pos := 0
	started := false
	for pos < len(text) {
		if length, ok := ansiSequenceLength(text, pos); ok {
			builder.WriteString(text[pos : pos+length])
			pos += length
			continue
		}
		r, size := utf8.DecodeRuneInString(text[pos:])
		if !started && (r == ' ' || r == '\t') {
			pos += size
			continue
		}
		started = true
		builder.WriteString(text[pos : pos+size])
		pos += size
	}
	return builder.String()
}

func ansiSequenceLength(text string, pos int) (int, bool) {
	if pos >= len(text) || text[pos] != 0x1b {
		return 0, false
	}
	if pos+1 >= len(text) {
		return len(text) - pos, true
	}
	switch text[pos+1] {
	case '[':
		end := pos + 2
		for end < len(text) && text[end] >= 0x20 && text[end] <= 0x3f {
			end++
		}
		if end < len(text) && text[end] >= 0x40 && text[end] <= 0x7e {
			return end + 1 - pos, true
		}
		return 0, false
	case ']', 'P', '_', 'X', '^':
		end := pos + 2
		for end < len(text) {
			if text[end] == 0x07 {
				return end + 1 - pos, true
			}
			if text[end] == 0x1b && end+1 < len(text) && text[end+1] == '\\' {
				return end + 2 - pos, true
			}
			end++
		}
		return 0, false
	default:
		if pos+1 < len(text) {
			return 2, true
		}
		return len(text) - pos, true
	}
}
