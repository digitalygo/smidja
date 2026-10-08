package interactive

import (
	"go/scanner"
	goToken "go/token"
	"strings"

	"github.com/digitalygo/smidja/internal/tui"
)

const syntaxMaxSource = 256 * 1024

type syntaxToken int

const (
	syntaxNone syntaxToken = iota
	syntaxCommentToken
	syntaxKeywordToken
	syntaxFunctionToken
	syntaxVariableToken
	syntaxStringToken
	syntaxNumberToken
	syntaxTypeToken
	syntaxOperatorToken
	syntaxPunctuationToken
)

func (t syntaxToken) themeColor() tui.ThemeColor {
	switch t {
	case syntaxCommentToken:
		return "syntaxComment"
	case syntaxKeywordToken:
		return "syntaxKeyword"
	case syntaxFunctionToken:
		return "syntaxFunction"
	case syntaxVariableToken:
		return "syntaxVariable"
	case syntaxStringToken:
		return "syntaxString"
	case syntaxNumberToken:
		return "syntaxNumber"
	case syntaxTypeToken:
		return "syntaxType"
	case syntaxOperatorToken:
		return "syntaxOperator"
	case syntaxPunctuationToken:
		return "syntaxPunctuation"
	}
	return "mdCodeBlock"
}

type syntaxSpan struct {
	start int
	end   int
	token syntaxToken
}

type syntaxBuilder struct {
	source string
	spans  []syntaxSpan
}

func (b *syntaxBuilder) add(start, end int, token syntaxToken) {
	if start < 0 {
		start = 0
	}
	if end > len(b.source) {
		end = len(b.source)
	}
	if end <= start || token == syntaxNone {
		return
	}
	b.spans = append(b.spans, syntaxSpan{start: start, end: end, token: token})
}

func (b *syntaxBuilder) addRuneStart(start int, length int, token syntaxToken) {
	b.add(start, start+length, token)
}

func normalizeSpans(source string, spans []syntaxSpan) []syntaxSpan {
	result := make([]syntaxSpan, 0, len(spans))
	cursor := 0
	for _, span := range spans {
		if span.start < cursor {
			span.start = cursor
		}
		if span.start < 0 || span.end > len(source) || span.end <= span.start {
			continue
		}
		result = append(result, span)
		cursor = span.end
	}
	return result
}

func SyntaxHighlight(code, lang string, theme *tui.Theme) []string {
	lines := strings.Split(code, "\n")
	if theme == nil || code == "" || len(code) > syntaxMaxSource {
		return plainSyntaxLines(lines, theme)
	}
	spans := normalizeSpans(code, lexSource(strings.ToLower(lang), code))
	return renderSyntaxLines(code, spans, theme)
}

func plainSyntaxLines(lines []string, theme *tui.Theme) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		if theme == nil {
			out[i] = line
			continue
		}
		out[i] = theme.Fg("mdCodeBlock", line)
	}
	return out
}

func renderSyntaxLines(code string, spans []syntaxSpan, theme *tui.Theme) []string {
	fallback := func(text string) string { return theme.Fg("mdCodeBlock", text) }
	lines := strings.Split(code, "\n")
	lineStarts := make([]int, len(lines))
	offset := 0
	for i, line := range lines {
		lineStarts[i] = offset
		offset += len(line) + 1
	}
	out := make([]string, len(lines))
	spanIndex := 0
	for i, line := range lines {
		start := lineStarts[i]
		end := start + len(line)
		var b strings.Builder
		cursor := start
		for spanIndex < len(spans) && spans[spanIndex].end <= start {
			spanIndex++
		}
		for index := spanIndex; index < len(spans); index++ {
			span := spans[index]
			if span.start >= end {
				break
			}
			if span.start > cursor {
				b.WriteString(fallback(code[cursor:span.start]))
			}
			spanStart := maxInt(span.start, cursor)
			spanEnd := minInt(span.end, end)
			if spanEnd > spanStart {
				b.WriteString(theme.Fg(span.token.themeColor(), code[spanStart:spanEnd]))
				cursor = spanEnd
			}
		}
		if cursor < end {
			b.WriteString(fallback(code[cursor:end]))
		}
		out[i] = b.String()
	}
	return out
}

func lexSource(lang, code string) []syntaxSpan {
	switch lang {
	case "go", "golang":
		return lexGo(code)
	case "javascript", "js", "jsx", "mjs", "cjs":
		return lexJavaScript(code, false)
	case "typescript", "ts", "tsx", "mts", "cts":
		return lexJavaScript(code, true)
	case "json", "jsonc":
		return lexJSON(code)
	case "yaml", "yml":
		return lexYAML(code)
	case "bash", "sh", "shell", "zsh":
		return lexBash(code)
	case "python", "py":
		return lexPython(code)
	case "markdown", "md":
		return lexMarkdown(code)
	case "diff", "patch":
		return lexDiff(code)
	}
	return nil
}

type goLexToken struct {
	offset int
	end    int
	tok    goToken.Token
	lit    string
}

func lexGo(code string) []syntaxSpan {
	file := goToken.NewFileSet().AddFile("", -1, len(code))
	var lexer scanner.Scanner
	lexer.Init(file, []byte(code), nil, scanner.ScanComments)
	var tokens []goLexToken
	for {
		pos, tok, lit := lexer.Scan()
		if tok == goToken.EOF {
			break
		}
		offset := file.Offset(pos)
		length := len(lit)
		if length == 0 {
			length = len(tok.String())
		}
		end := offset + length
		if offset < 0 || end > len(code) || end <= offset {
			continue
		}
		if lit != "" && code[offset:end] != lit {
			continue
		}
		tokens = append(tokens, goLexToken{offset: offset, end: end, tok: tok, lit: lit})
	}
	builder := &syntaxBuilder{source: code}
	for index, token := range tokens {
		switch {
		case token.tok == goToken.COMMENT:
			builder.add(token.offset, token.end, syntaxCommentToken)
		case token.tok == goToken.STRING || token.tok == goToken.CHAR:
			builder.add(token.offset, token.end, syntaxStringToken)
		case token.tok == goToken.INT || token.tok == goToken.FLOAT || token.tok == goToken.IMAG:
			builder.add(token.offset, token.end, syntaxNumberToken)
		case token.tok.IsLiteral() && token.tok == goToken.IDENT:
			builder.add(token.offset, token.end, goIdentToken(tokens, index, code))
		case token.tok.IsKeyword():
			builder.add(token.offset, token.end, syntaxKeywordToken)
		case isGoOperator(token.tok):
			builder.add(token.offset, token.end, syntaxOperatorToken)
		case token.tok.IsOperator() || isGoPunctuation(token.tok):
			builder.add(token.offset, token.end, syntaxPunctuationToken)
		}
	}
	return builder.spans
}

func goIdentToken(tokens []goLexToken, index int, code string) syntaxToken {
	name := tokens[index].lit
	if goToken.Lookup(name).IsKeyword() {
		return syntaxKeywordToken
	}
	if index+1 < len(tokens) {
		next := tokens[index+1]
		if next.tok == goToken.LPAREN {
			return syntaxFunctionToken
		}
	}
	if index > 0 {
		previous := tokens[index-1]
		switch previous.tok {
		case goToken.TYPE, goToken.STRUCT, goToken.INTERFACE:
			return syntaxTypeToken
		}
	}
	if name == "nil" || name == "true" || name == "false" {
		return syntaxKeywordToken
	}
	first := rune(name[0])
	if first >= 'A' && first <= 'Z' {
		return syntaxTypeToken
	}
	return syntaxVariableToken
}

func isGoOperator(tok goToken.Token) bool {
	switch tok {
	case goToken.ADD, goToken.SUB, goToken.MUL, goToken.QUO, goToken.REM,
		goToken.AND, goToken.OR, goToken.XOR, goToken.SHL, goToken.SHR, goToken.AND_NOT,
		goToken.ADD_ASSIGN, goToken.SUB_ASSIGN, goToken.MUL_ASSIGN, goToken.QUO_ASSIGN,
		goToken.REM_ASSIGN, goToken.AND_ASSIGN, goToken.OR_ASSIGN, goToken.XOR_ASSIGN,
		goToken.SHL_ASSIGN, goToken.SHR_ASSIGN, goToken.AND_NOT_ASSIGN,
		goToken.LAND, goToken.LOR, goToken.ARROW, goToken.INC, goToken.DEC,
		goToken.EQL, goToken.LSS, goToken.GTR, goToken.ASSIGN, goToken.NOT,
		goToken.NEQ, goToken.LEQ, goToken.GEQ, goToken.DEFINE, goToken.ELLIPSIS,
		goToken.TILDE:
		return true
	}
	return false
}

func isGoPunctuation(tok goToken.Token) bool {
	switch tok {
	case goToken.LPAREN, goToken.LBRACK, goToken.LBRACE, goToken.COMMA, goToken.PERIOD,
		goToken.RPAREN, goToken.RBRACK, goToken.RBRACE, goToken.SEMICOLON, goToken.COLON:
		return true
	}
	return false
}
