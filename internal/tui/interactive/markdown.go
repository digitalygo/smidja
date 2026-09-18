package interactive

import (
	"strconv"
	"strings"
	"sync"

	"github.com/digitalygo/smidja/internal/tui"
)

const (
	markdownMaxBlocks     = 20000
	markdownMaxNesting    = 12
	markdownMaxInlineDeep = 12
)

type MarkdownStyle struct {
	TextColor func(string) string
	BgColor   func(string) string
	Bold      bool
	Italic    bool
	Strike    bool
	Underline bool
}

type Markdown struct {
	mu         sync.Mutex
	text       string
	paddingX   int
	paddingY   int
	theme      *tui.Theme
	style      MarkdownStyle
	hyperlinks bool
	highlight  func(code, lang string) []string

	version     int
	cacheValid  bool
	cacheWidth  int
	cacheRender []string
}

func NewMarkdown(text string, paddingX, paddingY int, theme *tui.Theme, style MarkdownStyle, hyperlinks bool) *Markdown {
	component := &Markdown{
		paddingX:   paddingX,
		paddingY:   paddingY,
		theme:      theme,
		style:      style,
		hyperlinks: hyperlinks,
	}
	component.SetText(text)
	return component
}

func (m *Markdown) SetText(text string) {
	m.mu.Lock()
	m.text = SanitizeDisplayText(text)
	m.version++
	m.cacheValid = false
	m.mu.Unlock()
}

func (m *Markdown) Text() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.text
}

func (m *Markdown) SetHighlight(highlight func(code, lang string) []string) {
	m.mu.Lock()
	m.highlight = highlight
	m.version++
	m.cacheValid = false
	m.mu.Unlock()
}

func (m *Markdown) Invalidate() {
	m.mu.Lock()
	m.cacheValid = false
	m.mu.Unlock()
}

type mdBlockKind int

const (
	mdParagraph mdBlockKind = iota
	mdHeading
	mdCode
	mdList
	mdQuote
	mdRule
	mdTable
	mdSpace
)

type mdAlign int

const (
	mdAlignNone mdAlign = iota
	mdAlignLeft
	mdAlignCenter
	mdAlignRight
)

type mdListItem struct {
	marker   string
	ordered  bool
	task     bool
	checked  bool
	loose    bool
	content  string
	children []mdListItem
}

type mdBlock struct {
	kind   mdBlockKind
	level  int
	lang   string
	text   string
	items  []mdListItem
	inner  []mdBlock
	header []string
	rows   [][]string
	align  []mdAlign
}

type mdParser struct {
	lines []string
	pos   int
	depth int
}

func parseMarkdownBlocks(text string) []mdBlock {
	parser := &mdParser{lines: strings.Split(text, "\n")}
	return parser.parseBlocks()
}

func (p *mdParser) parseBlocks() []mdBlock {
	var blocks []mdBlock
	for p.pos < len(p.lines) && len(blocks) < markdownMaxBlocks {
		line := p.lines[p.pos]
		linePos := p.pos
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if len(blocks) > 0 && blocks[len(blocks)-1].kind != mdSpace {
				blocks = append(blocks, mdBlock{kind: mdSpace})
			}
			p.pos++
			continue
		}
		switch {
		case p.depth <= markdownMaxNesting && p.atFence(line):
			blocks = append(blocks, p.parseFence(line))
		case p.atHeading(line):
			blocks = append(blocks, p.parseHeading(line))
		case p.atRule(line):
			blocks = append(blocks, mdBlock{kind: mdRule})
			p.pos++
		case p.depth <= markdownMaxNesting && p.atQuote(line):
			blocks = append(blocks, p.parseQuote())
		case p.atTableStart():
			blocks = append(blocks, p.parseTable())
		case p.atListItem(line, 0):
			indent := indentOf(line)
			blocks = append(blocks, mdBlock{kind: mdList, items: p.parseListItems(indent)})
		default:
			blocks = append(blocks, p.parseParagraph())
		}
		if len(blocks) > 0 && p.pos == linePos {
			p.pos++
		}
	}
	return blocks
}

func (p *mdParser) atFence(line string) bool {
	return fenceMarker(line) != ""
}

func fenceMarker(line string) string {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 || len(trimmed) < 3 {
		return ""
	}
	first := trimmed[0]
	if first != '`' && first != '~' {
		return ""
	}
	count := 0
	for count < len(trimmed) && trimmed[count] == first {
		count++
	}
	if count < 3 {
		return ""
	}
	info := strings.TrimSpace(trimmed[count:])
	if first == '`' && strings.Contains(info, "`") {
		return ""
	}
	return strings.Repeat(string(first), count)
}

func (p *mdParser) parseFence(line string) mdBlock {
	marker := fenceMarker(line)
	trimmed := strings.TrimLeft(line, " ")
	lang := strings.TrimSpace(trimmed[len(marker):])
	block := mdBlock{kind: mdCode, lang: sanitizeCodeLanguage(lang)}
	p.pos++
	var content []string
	for p.pos < len(p.lines) {
		current := p.lines[p.pos]
		if closingFence(current, marker) {
			p.pos++
			break
		}
		content = append(content, current)
		p.pos++
	}
	block.text = strings.Join(content, "\n")
	return block
}

func closingFence(line, marker string) bool {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 {
		return false
	}
	run := 0
	for run < len(trimmed) && trimmed[run] == marker[0] {
		run++
	}
	if run < 3 || run < len(marker) {
		return false
	}
	return strings.TrimSpace(trimmed[run:]) == ""
}

func sanitizeCodeLanguage(lang string) string {
	sanitized := SanitizeSingleLine(lang)
	var b strings.Builder
	for _, r := range sanitized {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '+' || r == '_' || r == '#' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (p *mdParser) atHeading(line string) bool {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 || !strings.HasPrefix(trimmed, "#") {
		return false
	}
	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	if level > 6 {
		return false
	}
	if level == len(trimmed) {
		return true
	}
	return trimmed[level] == ' ' || trimmed[level] == '\t'
}

func (p *mdParser) parseHeading(line string) mdBlock {
	trimmed := strings.TrimLeft(line, " ")
	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	text := trimClosingHashes(strings.TrimSpace(trimmed[level:]))
	p.pos++
	return mdBlock{kind: mdHeading, level: level, text: text}
}

func trimClosingHashes(text string) string {
	trimmed := strings.TrimRight(text, " \t")
	end := len(trimmed)
	for end > 0 && trimmed[end-1] == '#' {
		end--
	}
	if end == len(trimmed) {
		return trimmed
	}
	if end == 0 {
		return ""
	}
	if trimmed[end-1] != ' ' && trimmed[end-1] != '\t' {
		return trimmed
	}
	return strings.TrimRight(trimmed[:end], " \t")
}

func (p *mdParser) atRule(line string) bool {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 || trimmed == "" {
		return false
	}
	marker := byte(0)
	count := 0
	for i := 0; i < len(trimmed); i++ {
		char := trimmed[i]
		switch char {
		case '-', '*', '_':
			if marker == 0 {
				marker = char
			} else if char != marker {
				return false
			}
			count++
		case ' ', '\t':
		default:
			return false
		}
	}
	return count >= 3
}

func (p *mdParser) atQuote(line string) bool {
	trimmed := strings.TrimLeft(line, " ")
	return len(line)-len(trimmed) <= 3 && strings.HasPrefix(trimmed, ">")
}

func (p *mdParser) parseQuote() mdBlock {
	var inner []string
	for p.pos < len(p.lines) {
		line := p.lines[p.pos]
		if !p.atQuote(line) {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || len(inner) == 0 {
				break
			}
			inner = append(inner, trimmed)
			p.pos++
			continue
		}
		trimmed := strings.TrimLeft(line, " ")
		trimmed = strings.TrimPrefix(trimmed, ">")
		if strings.HasPrefix(trimmed, " ") {
			trimmed = trimmed[1:]
		}
		inner = append(inner, trimmed)
		p.pos++
	}
	nested := &mdParser{lines: inner, depth: p.depth + 1}
	return mdBlock{kind: mdQuote, inner: nested.parseBlocks()}
}

func (p *mdParser) parseParagraph() mdBlock {
	var parts []string
	for p.pos < len(p.lines) {
		line := p.lines[p.pos]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			break
		}
		if p.depth <= markdownMaxNesting && (p.atFence(line) || p.atHeading(line) || p.atQuote(line) || p.atListItem(line, 0)) {
			break
		}
		if len(parts) > 0 && p.atRule(line) {
			break
		}
		parts = append(parts, trimmed)
		p.pos++
	}
	return mdBlock{kind: mdParagraph, text: strings.Join(parts, " ")}
}

func (p *mdParser) atListItem(line string, minIndent int) bool {
	indent, _, _, _, ok := listItemStart(line)
	return ok && indent >= minIndent
}

func listItemStart(line string) (indent int, marker string, ordered bool, rest string, ok bool) {
	for indent < len(line) && (line[indent] == ' ' || line[indent] == '\t') {
		indent++
	}
	rest = line[indent:]
	if rest == "" {
		return 0, "", false, "", false
	}
	switch rest[0] {
	case '-', '+', '*':
		if len(rest) == 1 {
			return 0, "", false, "", false
		}
		if rest[1] != ' ' && rest[1] != '\t' {
			return 0, "", false, "", false
		}
		return indent, rest[:1], false, strings.TrimLeft(rest[1:], " \t"), true
	}
	digits := 0
	for digits < len(rest) && rest[digits] >= '0' && rest[digits] <= '9' {
		digits++
	}
	if digits == 0 || digits > 9 || digits >= len(rest) {
		return 0, "", false, "", false
	}
	if rest[digits] != '.' && rest[digits] != ')' {
		return 0, "", false, "", false
	}
	if digits+1 >= len(rest) || rest[digits+1] != ' ' && rest[digits+1] != '\t' {
		return 0, "", false, "", false
	}
	return indent, rest[:digits+1], true, strings.TrimLeft(rest[digits+1:], " \t"), true
}

func (p *mdParser) parseListItems(indent int) []mdListItem {
	var items []mdListItem
	for p.pos < len(p.lines) {
		line := p.lines[p.pos]
		lineIndent, marker, ordered, rest, ok := listItemStart(line)
		if !ok || lineIndent < indent {
			break
		}
		if len(items) > 0 && lineIndent > indent {
			break
		}
		p.pos++
		item := mdListItem{marker: marker + " ", ordered: ordered}
		content := rest
		if _, checked, remainder, isTask := parseTaskMarker(rest); isTask {
			item.task = true
			item.checked = checked
			content = remainder
		}
		item.content, item.children = p.collectItemContent(lineIndent, len(marker)+1, content)
		items = append(items, item)
		if p.skipBlankInList(indent) && hasSiblingItem(p.lines, p.pos, indent) {
			items[len(items)-1].loose = true
		}
	}
	return items
}

func hasSiblingItem(lines []string, start, indent int) bool {
	if start >= len(lines) {
		return false
	}
	lineIndent, _, _, _, ok := listItemStart(lines[start])
	return ok && lineIndent == indent
}

func parseTaskMarker(text string) (task bool, checked bool, remainder string, ok bool) {
	if !strings.HasPrefix(text, "[") || len(text) < 3 || text[2] != ']' {
		return false, false, text, false
	}
	switch text[1] {
	case ' ', 'x', 'X':
	default:
		return false, false, text, false
	}
	if len(text) > 3 && text[3] != ' ' && text[3] != '\t' {
		return false, false, text, false
	}
	return true, text[1] != ' ', strings.TrimLeft(text[3:], " \t"), true
}

func (p *mdParser) collectItemContent(itemIndent, contentIndent int, first string) (string, []mdListItem) {
	var parts []string
	var children []mdListItem
	if first != "" {
		parts = append(parts, first)
	}
	for p.pos < len(p.lines) {
		line := p.lines[p.pos]
		if strings.TrimSpace(line) == "" {
			next := p.peekNextNonBlank()
			if next == "" {
				break
			}
			nextIndent, _, _, _, isItem := listItemStart(next)
			if isItem && nextIndent <= itemIndent {
				break
			}
			contentLike := indentOf(next) >= itemIndent+contentIndent || isItem && nextIndent >= itemIndent
			if !contentLike {
				break
			}
			p.pos++
			continue
		}
		lineIndent := indentOf(line)
		if _, _, _, _, isItem := listItemStart(line); isItem && lineIndent >= itemIndent+contentIndent {
			children = append(children, p.parseListItems(lineIndent)...)
			continue
		}
		if lineIndent <= itemIndent {
			if _, _, _, _, isItem := listItemStart(line); isItem {
				break
			}
			if lineIndent == 0 && itemIndent > 0 {
				break
			}
		}
		p.pos++
		parts = append(parts, strings.TrimSpace(line))
	}
	return strings.Join(parts, " "), children
}

func (p *mdParser) skipBlankInList(indent int) bool {
	skipped := false
	for p.pos < len(p.lines) && strings.TrimSpace(p.lines[p.pos]) == "" {
		next := p.peekNextNonBlank()
		if next == "" {
			p.pos++
			return skipped
		}
		nextIndent, _, _, _, isItem := listItemStart(next)
		if isItem && nextIndent >= indent || indentOf(next) > indent {
			p.pos++
			skipped = true
			continue
		}
		return skipped
	}
	return skipped
}

func (p *mdParser) peekNextNonBlank() string {
	for index := p.pos; index < len(p.lines); index++ {
		if strings.TrimSpace(p.lines[index]) != "" {
			return p.lines[index]
		}
	}
	return ""
}

func indentOf(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}

func (m *Markdown) Render(width int) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cacheValid && m.cacheWidth == width {
		return m.cacheRender
	}
	lines := m.render(width)
	m.cacheRender = lines
	m.cacheWidth = width
	m.cacheValid = true
	return lines
}

func (m *Markdown) render(width int) []string {
	contentWidth := maxInt(1, width-m.paddingX*2)
	if strings.TrimSpace(m.text) == "" {
		return nil
	}
	blocks := parseMarkdownBlocks(m.text)
	rendered := make([]string, 0, len(blocks)*2)
	renderer := &mdRenderer{markdown: m, theme: m.theme, width: contentWidth, hyper: m.hyperlinks}
	for index, block := range blocks {
		last := index == len(blocks)-1
		rendered = append(rendered, renderer.renderBlock(block, last, len(blocks) > index+1 && blocks[index+1].kind == mdList)...)
	}
	wrapped := make([]string, 0, len(rendered))
	for _, line := range rendered {
		wrapped = append(wrapped, tui.WrapTextWithANSI(line, contentWidth)...)
	}
	left := strings.Repeat(" ", m.paddingX)
	right := strings.Repeat(" ", m.paddingX)
	result := make([]string, 0, len(wrapped)+m.paddingY*2)
	for i := 0; i < m.paddingY; i++ {
		result = append(result, m.styledEmpty(width))
	}
	for _, line := range wrapped {
		combined := left + line + right
		if m.style.BgColor != nil {
			result = append(result, tui.ApplyBackgroundToLine(combined, width, m.style.BgColor))
		} else {
			result = append(result, padLineToWidth(combined, width))
		}
	}
	for i := 0; i < m.paddingY; i++ {
		result = append(result, m.styledEmpty(width))
	}
	return result
}

func (m *Markdown) styledEmpty(width int) string {
	line := strings.Repeat(" ", maxInt(0, width))
	if m.style.BgColor != nil {
		return m.style.BgColor(line)
	}
	return line
}

func (m *Markdown) defaultApply() func(string) string {
	if m.style.TextColor == nil {
		return func(text string) string { return text }
	}
	return m.style.TextColor
}

func (m *Markdown) defaultPrefix() string {
	return stylePrefix(m.defaultApply(), m.theme, m.style)
}

func stylePrefix(apply func(string) string, theme *tui.Theme, style MarkdownStyle) string {
	const sentinel = "\x00"
	styled := apply(sentinel)
	if style.Bold {
		styled = theme.Bold(styled)
	}
	if style.Italic {
		styled = theme.Italic(styled)
	}
	if style.Strike {
		styled = theme.Strike(styled)
	}
	if style.Underline {
		styled = theme.Underline(styled)
	}
	index := strings.Index(styled, sentinel)
	if index < 0 {
		return ""
	}
	return styled[:index]
}

func formatTokenCount(count int64) string {
	digits := strconv.FormatInt(count, 10)
	var parts []string
	for len(digits) > 3 {
		parts = append([]string{digits[len(digits)-3:]}, parts...)
		digits = digits[:len(digits)-3]
	}
	parts = append([]string{digits}, parts...)
	return strings.Join(parts, ",")
}
