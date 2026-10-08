package interactive

import "strings"

func isIdentStart(char byte) bool {
	return char == '_' || char == '$' || char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= 0x80
}

func isIdentPart(char byte) bool {
	return isIdentStart(char) || char >= '0' && char <= '9'
}

func isDigit(char byte) bool {
	return char >= '0' && char <= '9'
}

func isHexDigit(char byte) bool {
	return isDigit(char) || char >= 'a' && char <= 'f' || char >= 'A' && char <= 'F'
}

func scanIdentEnd(code string, index int) int {
	for index < len(code) && isIdentPart(code[index]) {
		index++
	}
	return index
}

func scanNumberEnd(code string, index int) int {
	if index < len(code) && (code[index] == '+' || code[index] == '-') {
		index++
	}
	if index+1 < len(code) && code[index] == '0' && (code[index+1] == 'x' || code[index+1] == 'X') {
		index += 2
		for index < len(code) && (isHexDigit(code[index]) || code[index] == '_') {
			index++
		}
		return index
	}
	if index+1 < len(code) && code[index] == '0' && (code[index+1] == 'b' || code[index+1] == 'B') {
		index += 2
		for index < len(code) && (code[index] == '0' || code[index] == '1' || code[index] == '_') {
			index++
		}
		return index
	}
	for index < len(code) && (isDigit(code[index]) || code[index] == '_') {
		index++
	}
	if index < len(code) && code[index] == '.' {
		index++
		for index < len(code) && (isDigit(code[index]) || code[index] == '_') {
			index++
		}
	}
	if index < len(code) && (code[index] == 'e' || code[index] == 'E') {
		probe := index + 1
		if probe < len(code) && (code[probe] == '+' || code[probe] == '-') {
			probe++
		}
		if probe < len(code) && isDigit(code[probe]) {
			index = probe
			for index < len(code) && (isDigit(code[index]) || code[index] == '_') {
				index++
			}
		}
	}
	if index < len(code) && (code[index] == 'n' || code[index] == 'j' || code[index] == 'L') {
		index++
	}
	return index
}

func findClose(code string, index int, quote byte) int {
	for index < len(code) {
		switch code[index] {
		case '\\':
			index += 2
			continue
		case quote:
			return index + 1
		}
		index++
	}
	return len(code)
}

func findLineEnd(code string, index int) int {
	next := strings.IndexByte(code[index:], '\n')
	if next < 0 {
		return len(code)
	}
	return index + next
}

func findBlockClose(code string, index int, open, close string) int {
	next := strings.Index(code[index:], close)
	if next < 0 {
		return len(code)
	}
	return index + next + len(close)
}

var jsKeywords = map[string]struct{}{
	"async": {}, "await": {}, "break": {}, "case": {}, "catch": {}, "class": {},
	"const": {}, "continue": {}, "debugger": {}, "default": {}, "delete": {}, "do": {},
	"else": {}, "enum": {}, "export": {}, "extends": {}, "false": {}, "finally": {},
	"for": {}, "function": {}, "if": {}, "import": {}, "in": {}, "instanceof": {},
	"let": {}, "new": {}, "null": {}, "of": {}, "return": {}, "static": {}, "super": {},
	"switch": {}, "this": {}, "throw": {}, "true": {}, "try": {}, "typeof": {},
	"undefined": {}, "var": {}, "void": {}, "while": {}, "with": {}, "yield": {},
}

var tsKeywords = map[string]struct{}{
	"abstract": {}, "any": {}, "as": {}, "asserts": {}, "bigint": {}, "boolean": {},
	"declare": {}, "infer": {}, "interface": {}, "is": {}, "keyof": {}, "module": {},
	"namespace": {}, "never": {}, "number": {}, "object": {}, "override": {},
	"private": {}, "protected": {}, "public": {}, "readonly": {}, "require": {},
	"string": {}, "symbol": {}, "type": {}, "unknown": {},
}

func lexJavaScript(code string, typescript bool) []syntaxSpan {
	builder := &syntaxBuilder{source: code}
	expectName := false
	expectType := false
	index := 0
	for index < len(code) {
		char := code[index]
		switch {
		case char == '/' && index+1 < len(code) && code[index+1] == '/':
			end := findLineEnd(code, index)
			builder.add(index, end, syntaxCommentToken)
			index = end
		case char == '/' && index+1 < len(code) && code[index+1] == '*':
			end := findBlockClose(code, index+2, "/*", "*/")
			builder.add(index, end, syntaxCommentToken)
			index = end
		case char == '`':
			end := findTemplateEnd(code, index+1)
			builder.add(index, end, syntaxStringToken)
			index = end
		case char == '\'' || char == '"':
			end := findClose(code, index+1, char)
			builder.add(index, end, syntaxStringToken)
			index = end
		case isIdentStart(char) && !isDigit(char):
			end := scanIdentEnd(code, index)
			word := code[index:end]
			token := classifyScriptIdentifier(word, typescript, expectName, expectType)
			builder.add(index, end, token)
			expectType = false
			switch word {
			case "function", "class", "interface", "enum", "type", "namespace":
				expectName = true
			default:
				expectName = false
			}
			index = end
		case isDigit(char):
			end := scanNumberEnd(code, index)
			builder.add(index, end, syntaxNumberToken)
			index = end
		case char == ':':
			builder.add(index, index+1, syntaxPunctuationToken)
			expectType = true
			index++
		case strings.IndexByte("+-*/%=<>!&|^~?", char) >= 0:
			end := scanOperatorRun(code, index)
			builder.add(index, end, syntaxOperatorToken)
			index = end
		case strings.IndexByte("(){}[],;.", char) >= 0:
			builder.add(index, index+1, syntaxPunctuationToken)
			index++
		default:
			index++
		}
	}
	return builder.spans
}

func findTemplateEnd(code string, index int) int {
	for index < len(code) {
		switch code[index] {
		case '\\':
			index += 2
		case '`':
			return index + 1
		case '$':
			if index+1 < len(code) && code[index+1] == '{' {
				depth := 1
				index += 2
				for index < len(code) && depth > 0 {
					switch code[index] {
					case '{':
						depth++
					case '}':
						depth--
					}
					index++
				}
				continue
			}
			index++
		default:
			index++
		}
	}
	return len(code)
}

func classifyScriptIdentifier(word string, typescript, expectName, expectType bool) syntaxToken {
	if _, ok := jsKeywords[word]; ok {
		return syntaxKeywordToken
	}
	if typescript {
		if _, ok := tsKeywords[word]; ok {
			return syntaxKeywordToken
		}
	}
	if expectName {
		return syntaxFunctionToken
	}
	if expectType {
		return syntaxTypeToken
	}
	if word == "true" || word == "false" || word == "null" || word == "undefined" || word == "this" {
		return syntaxKeywordToken
	}
	if word[0] >= 'A' && word[0] <= 'Z' {
		return syntaxTypeToken
	}
	return syntaxVariableToken
}

func scanOperatorRun(code string, index int) int {
	end := index
	for end < len(code) && strings.IndexByte("+-*/%=<>!&|^~?", code[end]) >= 0 {
		end++
	}
	if end == index {
		end = index + 1
	}
	return end
}

var pythonKeywords = map[string]struct{}{
	"and": {}, "as": {}, "assert": {}, "async": {}, "await": {}, "break": {},
	"class": {}, "continue": {}, "def": {}, "del": {}, "elif": {}, "else": {},
	"except": {}, "False": {}, "finally": {}, "for": {}, "from": {}, "global": {},
	"if": {}, "import": {}, "in": {}, "is": {}, "lambda": {}, "None": {},
	"nonlocal": {}, "not": {}, "or": {}, "pass": {}, "raise": {}, "return": {},
	"True": {}, "try": {}, "while": {}, "with": {}, "yield": {}, "match": {},
	"case": {},
}

func lexPython(code string) []syntaxSpan {
	builder := &syntaxBuilder{source: code}
	expectName := false
	expectType := false
	decorator := false
	index := 0
	for index < len(code) {
		char := code[index]
		switch {
		case char == '#':
			end := findLineEnd(code, index)
			builder.add(index, end, syntaxCommentToken)
			index = end
		case strings.HasPrefix(code[index:], `"""`) || strings.HasPrefix(code[index:], "'''"):
			quote := code[index : index+3]
			end := findBlockClose(code, index+3, quote, quote)
			builder.add(index, end, syntaxStringToken)
			index = end
		case char == '\'' || char == '"':
			end := findClose(code, index+1, char)
			if end < len(code) && code[end-1] != char {
				end = findLineEnd(code, index)
			}
			builder.add(index, end, syntaxStringToken)
			index = end
		case char == '@':
			end := index + 1
			if end < len(code) && isIdentStart(code[end]) {
				end = scanIdentEnd(code, end)
				decorator = true
			}
			builder.add(index, end, syntaxFunctionToken)
			index = end
		case isIdentStart(char) && !isDigit(char):
			end := scanIdentEnd(code, index)
			word := code[index:end]
			token := classifyPythonIdentifier(word, expectName, expectType, decorator)
			builder.add(index, end, token)
			expectType = false
			decorator = false
			switch word {
			case "def":
				expectName = true
			case "class":
				expectName = true
				expectType = true
			default:
				expectName = false
			}
			index = end
		case isDigit(char):
			end := scanNumberEnd(code, index)
			builder.add(index, end, syntaxNumberToken)
			index = end
		case char == ':':
			builder.add(index, index+1, syntaxPunctuationToken)
			expectType = true
			index++
		case strings.IndexByte("+-*/%=<>!&|^~@", char) >= 0:
			end := scanOperatorRun(code, index)
			builder.add(index, end, syntaxOperatorToken)
			index = end
		case strings.IndexByte("(){}[],;.", char) >= 0:
			builder.add(index, index+1, syntaxPunctuationToken)
			index++
		default:
			index++
		}
	}
	return builder.spans
}

func classifyPythonIdentifier(word string, expectName, expectType, decorator bool) syntaxToken {
	if _, ok := pythonKeywords[word]; ok {
		return syntaxKeywordToken
	}
	if word == "self" || word == "cls" {
		return syntaxVariableToken
	}
	if decorator {
		return syntaxFunctionToken
	}
	if expectName {
		if expectType {
			return syntaxTypeToken
		}
		return syntaxFunctionToken
	}
	if expectType {
		return syntaxTypeToken
	}
	if word[0] >= 'A' && word[0] <= 'Z' {
		return syntaxTypeToken
	}
	return syntaxVariableToken
}

var bashKeywords = map[string]struct{}{
	"if": {}, "then": {}, "else": {}, "elif": {}, "fi": {}, "for": {}, "while": {},
	"until": {}, "do": {}, "done": {}, "case": {}, "esac": {}, "in": {}, "function": {},
	"select": {}, "time": {}, "return": {}, "local": {}, "export": {}, "declare": {},
	"readonly": {}, "typeset": {}, "unset": {}, "shift": {}, "source": {},
}

func lexBash(code string) []syntaxSpan {
	builder := &syntaxBuilder{source: code}
	commandStart := true
	index := 0
	for index < len(code) {
		char := code[index]
		switch {
		case char == '#':
			end := findLineEnd(code, index)
			builder.add(index, end, syntaxCommentToken)
			index = end
		case char == '\'':
			end := findClose(code, index+1, '\'')
			builder.add(index, end, syntaxStringToken)
			index = end
		case char == '"':
			end := findClose(code, index+1, '"')
			builder.add(index, end, syntaxStringToken)
			index = end
		case char == '$':
			end := scanDollarExpansion(code, index)
			builder.add(index, end, syntaxVariableToken)
			index = end
		case char == '`':
			end := findClose(code, index+1, '`')
			builder.add(index, end, syntaxStringToken)
			index = end
		case isIdentStart(char) && !isDigit(char):
			end := scanIdentEnd(code, index)
			word := code[index:end]
			if _, ok := bashKeywords[word]; ok {
				builder.add(index, end, syntaxKeywordToken)
				commandStart = true
			} else if commandStart {
				builder.add(index, end, syntaxFunctionToken)
				commandStart = false
			} else {
				builder.add(index, end, syntaxVariableToken)
			}
			index = end
		case isDigit(char):
			end := scanNumberEnd(code, index)
			builder.add(index, end, syntaxNumberToken)
			index = end
		case char == '\n':
			commandStart = true
			index++
		case strings.IndexByte("|&;()<>\n", char) >= 0:
			builder.add(index, index+1, syntaxOperatorToken)
			if strings.IndexByte("|&;()", char) >= 0 {
				commandStart = true
			}
			index++
		case strings.IndexByte("=+-*/%!~[]{}:", char) >= 0:
			end := scanOperatorRunExact(code, index)
			builder.add(index, end, syntaxOperatorToken)
			index = end
		default:
			index++
		}
	}
	return builder.spans
}

func scanDollarExpansion(code string, index int) int {
	if index+1 >= len(code) {
		return index + 1
	}
	switch code[index+1] {
	case '{':
		depth := 1
		end := index + 2
		for end < len(code) && depth > 0 {
			switch code[end] {
			case '{':
				depth++
			case '}':
				depth--
			}
			end++
		}
		return end
	case '(':
		end := findClose(code, index+2, ')')
		return end
	}
	return scanIdentEnd(code, index+1)
}

func scanOperatorRunExact(code string, index int) int {
	end := index
	for end < len(code) && strings.IndexByte("=+-*/%!~[]{}:", code[end]) >= 0 {
		end++
	}
	return end
}
