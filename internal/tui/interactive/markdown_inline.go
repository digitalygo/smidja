package interactive

import (
	"strings"

	"github.com/digitalygo/smidja/internal/tui"
)

func renderInline(text string, context inlineContext, theme *tui.Theme, hyperlinks bool) string {
	return renderInlineDepth(text, context, theme, hyperlinks, 0)
}

func renderInlineDepth(text string, context inlineContext, theme *tui.Theme, hyperlinks bool, depth int) string {
	if text == "" {
		return ""
	}
	if depth > markdownMaxInlineDeep {
		return context.apply(text)
	}
	hyper := context.hyper || hyperlinks
	renderer := &mdInline{source: text, theme: theme, hyperlinks: hyper, depth: depth}
	return renderer.run(context)
}

type mdInline struct {
	source     string
	pos        int
	theme      *tui.Theme
	hyperlinks bool
	depth      int
	out        strings.Builder
}

func (r *mdInline) run(context inlineContext) string {
	var plain strings.Builder
	flush := func() {
		if plain.Len() == 0 {
			return
		}
		r.out.WriteString(context.apply(plain.String()))
		plain.Reset()
	}
	for r.pos < len(r.source) {
		char := r.source[r.pos]
		switch {
		case char == '\\' && r.pos+1 < len(r.source) && isInlinePunct(r.source[r.pos+1]):
			plain.WriteByte(r.source[r.pos+1])
			r.pos += 2
		case char == '`':
			if code, ok := r.consumeCodeSpan(); ok {
				flush()
				r.out.WriteString(r.theme.Fg("mdCode", code) + context.prefix)
			} else {
				plain.WriteByte(char)
				r.pos++
			}
		case char == '~' && strings.HasPrefix(r.source[r.pos:], "~~"):
			if content, ok := r.consumeDelimiter("~~"); ok {
				flush()
				inner := renderInlineDepth(content, context, r.theme, r.hyperlinks, r.depth+1)
				r.out.WriteString(r.theme.Strike(inner) + context.prefix)
			} else {
				plain.WriteString("~~")
				r.pos += 2
			}
		case char == '*' && strings.HasPrefix(r.source[r.pos:], "**") || char == '_' && strings.HasPrefix(r.source[r.pos:], "__"):
			delimiter := r.source[r.pos : r.pos+2]
			if content, ok := r.consumeDelimiter(delimiter); ok {
				flush()
				inner := renderInlineDepth(content, context, r.theme, r.hyperlinks, r.depth+1)
				r.out.WriteString(r.theme.Bold(inner) + context.prefix)
			} else {
				plain.WriteString(delimiter)
				r.pos += 2
			}
		case char == '*' || char == '_':
			delimiter := r.source[r.pos : r.pos+1]
			if !r.emphasisOpens(char) {
				plain.WriteByte(char)
				r.pos++
				continue
			}
			if content, ok := r.consumeDelimiter(delimiter); ok {
				flush()
				inner := renderInlineDepth(content, context, r.theme, r.hyperlinks, r.depth+1)
				r.out.WriteString(r.theme.Italic(inner) + context.prefix)
			} else {
				plain.WriteByte(char)
				r.pos++
			}
		case char == '[':
			if label, target, ok := r.consumeLink(); ok {
				flush()
				r.out.WriteString(r.renderLink(label, target, context))
			} else {
				plain.WriteByte(char)
				r.pos++
			}
		case char == '<' && r.autolinkAhead():
			url := r.consumeAutolink()
			flush()
			r.out.WriteString(r.renderLink(url, url, context))
		case strings.HasPrefix(r.source[r.pos:], "http://") || strings.HasPrefix(r.source[r.pos:], "https://"):
			url := r.consumeBareURL()
			flush()
			r.out.WriteString(r.renderLink(url, url, context))
		default:
			plain.WriteByte(char)
			r.pos++
		}
	}
	flush()
	result := r.out.String()
	if context.prefix != "" {
		for strings.HasSuffix(result, context.prefix) {
			result = strings.TrimSuffix(result, context.prefix)
		}
	}
	return result
}

func isInlinePunct(char byte) bool {
	return strings.IndexByte("!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", char) >= 0
}

func (r *mdInline) emphasisOpens(char byte) bool {
	if char == '*' {
		return true
	}
	if r.pos == 0 {
		return true
	}
	previous := r.source[r.pos-1]
	return previous == ' ' || previous == '\t' || isInlinePunct(previous)
}

func (r *mdInline) consumeCodeSpan() (string, bool) {
	start := r.pos + 1
	end := strings.IndexByte(r.source[start:], '`')
	if end < 0 {
		return "", false
	}
	r.pos = start + end + 1
	return r.source[start : start+end], true
}

func (r *mdInline) consumeDelimiter(delimiter string) (string, bool) {
	start := r.pos + len(delimiter)
	end := strings.Index(r.source[start:], delimiter)
	if end < 0 {
		return "", false
	}
	closed := start + end + len(delimiter)
	if delimiter == "_" && closed < len(r.source) && isWordRune(r.source[closed]) {
		return "", false
	}
	r.pos = closed
	return r.source[start : start+end], true
}

func isWordRune(char byte) bool {
	return char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char >= 0x80
}

func (r *mdInline) consumeLink() (string, string, bool) {
	closeBracket := strings.IndexByte(r.source[r.pos:], ']')
	if closeBracket < 0 {
		return "", "", false
	}
	labelEnd := r.pos + closeBracket
	after := labelEnd + 1
	if after >= len(r.source) || r.source[after] != '(' {
		return "", "", false
	}
	closeParen := strings.IndexByte(r.source[after:], ')')
	if closeParen < 0 {
		return "", "", false
	}
	label := r.source[r.pos+1 : labelEnd]
	target := strings.TrimSpace(r.source[after+1 : after+closeParen])
	r.pos = after + closeParen + 1
	return label, target, true
}

func (r *mdInline) autolinkAhead() bool {
	rest := r.source[r.pos:]
	open := strings.IndexByte(rest, '>')
	if open < 0 {
		return false
	}
	inner := rest[1:open]
	if inner == "" || strings.ContainsAny(inner, " \t<") {
		return false
	}
	schemeEnd := strings.Index(inner, ":")
	if schemeEnd <= 0 {
		return false
	}
	for _, char := range inner[:schemeEnd] {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '+' || char == '.' || char == '-') {
			return false
		}
	}
	return true
}

func (r *mdInline) consumeAutolink() string {
	rest := r.source[r.pos:]
	end := strings.IndexByte(rest, '>')
	url := rest[1:end]
	r.pos += end + 1
	return url
}

func (r *mdInline) consumeBareURL() string {
	end := r.pos
	for end < len(r.source) {
		char := r.source[end]
		if char == ' ' || char == '\t' || char == ')' || char == '<' {
			break
		}
		end++
	}
	url := r.source[r.pos:end]
	r.pos = end
	return strings.TrimRight(url, ".,;:!?")
}

func (r *mdInline) renderLink(label, target string, context inlineContext) string {
	cleaned := SanitizeOSC8Target(strings.TrimSpace(target))
	safeTarget, allowed := safeHyperlinkTarget(cleaned)
	comparison := cleaned
	if mailto := strings.TrimPrefix(comparison, "mailto:"); mailto != comparison {
		comparison = mailto
	}
	styled := r.theme.Fg("mdLink", r.theme.Underline(renderInlineDepth(label, context, r.theme, r.hyperlinks, r.depth+1)))
	if r.hyperlinks && allowed {
		styled = tui.OSC8Hyperlink("", safeTarget) + styled + tui.OSC8Close
	}
	suffix := ""
	if label != cleaned && label != comparison {
		suffix = r.theme.Fg("mdLinkUrl", " ("+SanitizeSingleLine(cleaned)+")")
	}
	return styled + suffix + context.prefix
}
