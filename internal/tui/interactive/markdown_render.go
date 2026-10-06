package interactive

import (
	"strings"

	"github.com/digitalygo/smidja/internal/tui"
)

type mdRenderer struct {
	markdown     *Markdown
	theme        *tui.Theme
	width        int
	hyper        bool
	images       *mdImageCollector
	quoteContext *inlineContext
}

func (r *mdRenderer) renderBlock(block mdBlock, last bool, nextIsList bool) []string {
	switch block.kind {
	case mdSpace:
		return []string{""}
	case mdHeading:
		return r.renderHeading(block, last)
	case mdParagraph:
		fallback := false
		context := r.defaultContext()
		context.mathFallback = &fallback
		lines := []string{r.inline(block.text, context)}
		if fallback {
			lines = append(lines, r.mathWarning())
		}
		if !last && !nextIsList {
			lines = append(lines, "")
		}
		return lines
	case mdCode:
		return r.renderCode(block, last)
	case mdList:
		return r.renderListItems(block.items, 0)
	case mdQuote:
		return r.renderQuote(block, last)
	case mdRule:
		lines := []string{r.theme.Fg("mdHr", strings.Repeat("─", minInt(r.width, 80)))}
		if !last {
			lines = append(lines, "")
		}
		return lines
	case mdTable:
		return r.renderTable(block, last)
	case mdMath:
		return r.renderDisplayMath(block, last)
	}
	return nil
}

func (r *mdRenderer) defaultContext() inlineContext {
	if r.quoteContext != nil {
		return *r.quoteContext
	}
	return inlineContext{
		apply:    r.markdown.defaultApply(),
		prefix:   r.markdown.defaultPrefix(),
		resolver: r.markdown.image,
		images:   r.images,
		width:    r.width,
	}
}

func (r *mdRenderer) headingContext(level int) inlineContext {
	apply := func(text string) string {
		if level == 1 {
			return r.theme.Fg("mdHeading", r.theme.Bold(r.theme.Underline(text)))
		}
		return r.theme.Fg("mdHeading", r.theme.Bold(text))
	}
	return inlineContext{apply: apply, prefix: stylePrefix(apply, r.theme, MarkdownStyle{}), resolver: r.markdown.image, images: r.images, width: r.width}
}

func (r *mdRenderer) renderHeading(block mdBlock, last bool) []string {
	fallback := false
	context := r.headingContext(block.level)
	context.mathFallback = &fallback
	text := r.inline(block.text, context)
	line := text
	if block.level >= 3 {
		line = context.apply(strings.Repeat("#", block.level)+" ") + text
	}
	lines := []string{line}
	if fallback {
		lines = append(lines, r.mathWarning())
	}
	if !last {
		lines = append(lines, "")
	}
	return lines
}

func (r *mdRenderer) renderCode(block mdBlock, last bool) []string {
	if block.lang == "mermaid" {
		if rendered, warning, ok := RenderMermaid(block.text, maxInt(1, r.width-2), r.theme); ok {
			lines := make([]string, 0, len(rendered)+1)
			for _, line := range rendered {
				lines = append(lines, "  "+line)
			}
			if !last {
				lines = append(lines, "")
			}
			return lines
		} else if warning != "" {
			lines := r.renderCodeLines(block)
			lines = append(lines, r.theme.Fg("warning", "mermaid: "+warning))
			if !last {
				lines = append(lines, "")
			}
			return lines
		}
	}
	lines := r.renderCodeLines(block)
	if !last {
		lines = append(lines, "")
	}
	return lines
}

func (r *mdRenderer) renderDisplayMath(block mdBlock, last bool) []string {
	lines := make([]string, 0, len(block.raw))
	if block.parsed {
		for _, line := range strings.Split(block.text, "\n") {
			lines = append(lines, "  "+r.theme.Fg("mdCode", line))
		}
	} else {
		apply := r.markdown.defaultApply()
		for _, line := range block.raw {
			lines = append(lines, apply(line))
		}
		if block.closed {
			lines = append(lines, r.mathWarning())
		}
	}
	if !last {
		lines = append(lines, "")
	}
	return lines
}

func (r *mdRenderer) renderCodeLines(block mdBlock) []string {
	label := "```" + block.lang
	lines := []string{r.theme.Fg("mdCodeBlockBorder", label)}
	highlight := r.markdown.highlight
	if highlight == nil {
		highlight = func(code, lang string) []string { return SyntaxHighlight(code, lang, r.theme) }
	}
	for _, line := range highlight(block.text, block.lang) {
		lines = append(lines, "  "+line)
	}
	lines = append(lines, r.theme.Fg("mdCodeBlockBorder", "```"))
	return lines
}

func (r *mdRenderer) renderQuote(block mdBlock, last bool) []string {
	quoteApply := func(text string) string {
		return r.theme.Fg("mdQuote", r.theme.Italic(text))
	}
	quotePrefix := stylePrefix(quoteApply, r.theme, MarkdownStyle{Italic: true})
	context := inlineContext{apply: func(text string) string { return text }, prefix: quotePrefix, resolver: r.markdown.image, images: r.images, width: maxInt(1, r.width-2)}
	inner := &mdRenderer{markdown: r.markdown, theme: r.theme, width: maxInt(1, r.width-2), hyper: r.hyper, images: r.images, quoteContext: &context}
	var rendered []string
	for index, child := range block.inner {
		childLast := index == len(block.inner)-1
		nextIsList := !childLast && block.inner[index+1].kind == mdList
		rendered = append(rendered, inner.renderBlock(child, childLast, nextIsList)...)
	}
	for len(rendered) > 0 && rendered[len(rendered)-1] == "" {
		rendered = rendered[:len(rendered)-1]
	}
	lines := make([]string, 0, len(rendered))
	for _, line := range rendered {
		styled := quoteApply(reapplyPrefix(line, quotePrefix))
		for _, wrapped := range tui.WrapTextWithANSI(styled, maxInt(1, r.width-2)) {
			lines = append(lines, r.theme.Fg("mdQuoteBorder", "│ ")+wrapped)
		}
	}
	if !last {
		lines = append(lines, "")
	}
	return lines
}

func reapplyPrefix(line, prefix string) string {
	if prefix == "" {
		return line
	}
	return strings.ReplaceAll(line, tui.SGRReset, tui.SGRReset+prefix)
}

func (r *mdRenderer) renderListItems(items []mdListItem, depth int) []string {
	var lines []string
	indent := strings.Repeat("    ", depth)
	for _, item := range items {
		marker := indent + r.theme.Fg("mdListBullet", item.marker+taskMarker(item))
		continuation := indent + strings.Repeat(" ", tui.VisibleWidth(item.marker+taskMarker(item)))
		itemWidth := maxInt(1, r.width-tui.VisibleWidth(indent+item.marker+taskMarker(item)))
		rendered := false
		if item.content != "" {
			fallback := false
			context := r.defaultContext()
			context.mathFallback = &fallback
			text := r.inline(item.content, context)
			for _, wrapped := range tui.WrapTextWithANSI(text, itemWidth) {
				if rendered {
					lines = append(lines, continuation+wrapped)
				} else {
					lines = append(lines, marker+wrapped)
				}
				rendered = true
			}
			if fallback {
				lines = append(lines, continuation+r.mathWarning())
			}
		}
		if len(item.children) > 0 {
			lines = append(lines, r.renderListItems(item.children, depth+1)...)
			rendered = true
		}
		if !rendered {
			lines = append(lines, marker)
		}
		if item.loose {
			lines = append(lines, "")
		}
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func taskMarker(item mdListItem) string {
	if !item.task {
		return ""
	}
	if item.checked {
		return "[x] "
	}
	return "[ ] "
}

type inlineContext struct {
	apply        func(string) string
	prefix       string
	hyper        bool
	theme        *tui.Theme
	resolver     tui.ImageResolver
	images       *mdImageCollector
	width        int
	mathFallback *bool
}

func (r *mdRenderer) inline(text string, context inlineContext) string {
	return renderInline(text, context, r.theme, r.hyper)
}

func (r *mdRenderer) mathWarning() string {
	return r.theme.Fg("warning", "math: "+mathFallbackWarning)
}
