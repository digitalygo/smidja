package interactive

import "strings"

func lexJSON(code string) []syntaxSpan {
	builder := &syntaxBuilder{source: code}
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
		case char == '"':
			end := findClose(code, index+1, '"')
			builder.add(index, end, syntaxStringToken)
			index = end
		case isDigit(char) || char == '-' && index+1 < len(code) && isDigit(code[index+1]):
			end := scanNumberEnd(code, index)
			builder.add(index, end, syntaxNumberToken)
			index = end
		case isIdentStart(char) && !isDigit(char):
			end := scanIdentEnd(code, index)
			word := code[index:end]
			if word == "true" || word == "false" || word == "null" {
				builder.add(index, end, syntaxKeywordToken)
			} else {
				builder.add(index, end, syntaxVariableToken)
			}
			index = end
		case strings.IndexByte("{}[],:", char) >= 0:
			builder.add(index, index+1, syntaxPunctuationToken)
			index++
		case strings.IndexByte("+-*/%=<>!&|^~?", char) >= 0:
			end := scanOperatorRun(code, index)
			builder.add(index, end, syntaxOperatorToken)
			index = end
		default:
			index++
		}
	}
	return builder.spans
}

var yamlKeywordWords = map[string]struct{}{
	"true": {}, "false": {}, "null": {}, "yes": {}, "no": {}, "on": {}, "off": {},
	"True": {}, "False": {}, "Null": {}, "Yes": {}, "No": {}, "On": {}, "Off": {}, "~": {},
}

func lexYAML(code string) []syntaxSpan {
	builder := &syntaxBuilder{source: code}
	lineStart := 0
	blockIndent := -1
	for lineStart <= len(code) {
		lineEnd := findLineEnd(code, lineStart)
		line := code[lineStart:lineEnd]
		indent := yamlIndent(line)
		if blockIndent >= 0 {
			if strings.TrimSpace(line) == "" || indent > blockIndent {
				builder.add(lineStart, lineEnd, syntaxStringToken)
				lineStart = lineEnd + 1
				continue
			}
			blockIndent = -1
		}
		nextBlock := scanYAMLLine(builder, code, lineStart, lineEnd, indent)
		if nextBlock >= 0 {
			blockIndent = nextBlock
		}
		if lineEnd >= len(code) {
			break
		}
		lineStart = lineEnd + 1
	}
	return builder.spans
}

func yamlIndent(line string) int {
	indent := 0
	for indent < len(line) && line[indent] == ' ' {
		indent++
	}
	return indent
}

func scanYAMLLine(builder *syntaxBuilder, code string, start, end, indent int) int {
	index := start + indent
	content := code[start:end]
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return -1
	}
	if strings.HasPrefix(trimmed, "#") {
		builder.add(start+indent, end, syntaxCommentToken)
		return -1
	}
	if trimmed == "---" || trimmed == "..." || strings.HasPrefix(trimmed, "%") {
		builder.add(start+indent, end, syntaxPunctuationToken)
		return -1
	}
	if index < end && code[index] == '-' && (index+1 == end || code[index+1] == ' ' || code[index+1] == '\t') {
		builder.add(index, index+1, syntaxPunctuationToken)
		index++
		for index < end && (code[index] == ' ' || code[index] == '\t') {
			index++
		}
	}
	if index < end && code[index] == '?' && (index+1 == end || code[index+1] == ' ') {
		builder.add(index, index+1, syntaxPunctuationToken)
		index++
		for index < end && code[index] == ' ' {
			index++
		}
	}
	keyEnd := yamlKeyEnd(code, index, end)
	if keyEnd > index {
		builder.add(index, keyEnd, syntaxVariableToken)
		index = keyEnd
		if index < end && code[index] == ':' {
			builder.add(index, index+1, syntaxPunctuationToken)
			index++
		}
	}
	blockIndent := -1
	for index < end {
		char := code[index]
		switch {
		case char == ' ' || char == '\t':
			index++
		case char == '#':
			builder.add(index, end, syntaxCommentToken)
			index = end
		case char == '\'':
			close := findClose(code, index+1, '\'')
			if close > end {
				close = end
			}
			builder.add(index, close, syntaxStringToken)
			index = close
		case char == '"':
			close := findClose(code, index+1, '"')
			if close > end {
				close = end
			}
			builder.add(index, close, syntaxStringToken)
			index = close
		case char == '|' || char == '>':
			close := index + 1
			if close == end {
				blockIndent = indent
			}
			builder.add(index, close, syntaxStringToken)
			index = close
		case isDigit(char) || (char == '-' || char == '+') && index+1 < end && isDigit(code[index+1]):
			close := scanNumberEnd(code, index)
			builder.add(index, close, syntaxNumberToken)
			index = close
		case char == '&' || char == '*' || char == '!':
			close := scanIdentEnd(code, index+1)
			builder.add(index, close, syntaxTypeToken)
			index = close
		case isIdentStart(char) && !isDigit(char):
			close := scanIdentEnd(code, index)
			word := code[index:close]
			if _, ok := yamlKeywordWords[word]; ok {
				builder.add(index, close, syntaxKeywordToken)
			} else {
				builder.add(index, close, syntaxVariableToken)
			}
			index = close
		case strings.IndexByte("[]{}:,", char) >= 0:
			builder.add(index, index+1, syntaxPunctuationToken)
			index++
		case strings.IndexByte("=<>!+-*/%&|^~?", char) >= 0:
			close := scanOperatorRun(code, index)
			builder.add(index, close, syntaxOperatorToken)
			index = close
		default:
			index++
		}
	}
	return blockIndent
}

func yamlKeyEnd(code string, index, end int) int {
	if index >= end {
		return index
	}
	if code[index] == '\'' || code[index] == '"' {
		close := findClose(code, index+1, code[index])
		if close <= end && close < len(code) && close-1 < end && code[close-1] == code[index] {
			probe := close
			for probe < end && code[probe] == ' ' {
				probe++
			}
			if probe < end && code[probe] == ':' {
				return close
			}
		}
		return index
	}
	probe := index
	for probe < end {
		if code[probe] == ':' && (probe+1 == end || code[probe+1] == ' ' || code[probe+1] == '\t') {
			return probe
		}
		if code[probe] == '#' && probe > index && code[probe-1] == ' ' {
			return index
		}
		probe++
	}
	return index
}

func lexMarkdown(code string) []syntaxSpan {
	builder := &syntaxBuilder{source: code}
	lineStart := 0
	inFence := false
	var fenceChar byte
	for lineStart <= len(code) {
		lineEnd := findLineEnd(code, lineStart)
		line := code[lineStart:lineEnd]
		trimmed := strings.TrimLeft(line, " ")
		indent := len(line) - len(trimmed)
		if marker := markdownFenceMarker(trimmed); marker != "" {
			if !inFence {
				inFence = true
				fenceChar = marker[0]
			} else if marker[0] == fenceChar {
				builder.add(lineStart, lineEnd, syntaxStringToken)
				inFence = false
				if lineEnd >= len(code) {
					break
				}
				lineStart = lineEnd + 1
				continue
			}
		}
		if inFence {
			builder.add(lineStart, lineEnd, syntaxStringToken)
			if lineEnd >= len(code) {
				break
			}
			lineStart = lineEnd + 1
			continue
		}
		lexMarkdownLine(builder, code, lineStart, lineEnd, indent, trimmed)
		if lineEnd >= len(code) {
			break
		}
		lineStart = lineEnd + 1
	}
	return builder.spans
}

func markdownFenceMarker(trimmed string) string {
	if len(trimmed) < 3 {
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
	return strings.Repeat(string(first), count)
}

func lexMarkdownLine(builder *syntaxBuilder, code string, start, end, indent int, trimmed string) {
	index := start + indent
	if strings.HasPrefix(trimmed, "#") {
		builder.add(index, end, syntaxKeywordToken)
		return
	}
	if len(trimmed) >= 3 && (strings.Trim(trimmed, "-*_ \t") == "") && strings.ContainsAny(trimmed, "-*_") {
		builder.add(index, end, syntaxPunctuationToken)
		return
	}
	if strings.HasPrefix(trimmed, ">") {
		builder.add(index, index+1, syntaxPunctuationToken)
		index++
		for index < end && code[index] == ' ' {
			index++
		}
	}
	if markerEnd := markdownListMarkerEnd(code, index, end); markerEnd > index {
		builder.add(index, markerEnd, syntaxPunctuationToken)
		index = markerEnd
	}
	for index < end {
		switch code[index] {
		case '`':
			close := index + 1
			for close < end && code[close] != '`' {
				close++
			}
			if close < end {
				close++
			}
			builder.add(index, close, syntaxStringToken)
			index = close
		case '<':
			close := index + 1
			for close < end && code[close] != '>' {
				close++
			}
			if close < end {
				close++
			}
			builder.add(index, close, syntaxTypeToken)
			index = close
		case '[', ']':
			builder.add(index, index+1, syntaxPunctuationToken)
			index++
		case '(':
			if index > start && code[index-1] == ']' {
				close := index + 1
				for close < end && code[close] != ')' {
					close++
				}
				if close < end {
					close++
				}
				builder.add(index, close, syntaxStringToken)
				index = close
				continue
			}
			index++
		case '*', '_', '~':
			close := index
			for close < end && code[close] == code[index] {
				close++
			}
			if close-index >= 1 && index+2 <= end {
				builder.add(index, close, syntaxKeywordToken)
			}
			index = close
		default:
			index++
		}
	}
}

func markdownListMarkerEnd(code string, index, end int) int {
	if index >= end {
		return index
	}
	switch code[index] {
	case '-', '+', '*':
		if index+1 < end && (code[index+1] == ' ' || code[index+1] == '\t') {
			return index + 1
		}
		return index
	}
	digits := index
	for digits < end && isDigit(code[digits]) {
		digits++
	}
	if digits == index || digits >= end {
		return index
	}
	if (code[digits] == '.' || code[digits] == ')') && digits+1 < end && code[digits+1] == ' ' {
		return digits + 1
	}
	return index
}

func lexDiff(code string) []syntaxSpan {
	builder := &syntaxBuilder{source: code}
	lineStart := 0
	for lineStart <= len(code) {
		lineEnd := findLineEnd(code, lineStart)
		line := code[lineStart:lineEnd]
		trimmed := strings.TrimLeft(line, " ")
		indent := len(line) - len(trimmed)
		var token syntaxToken
		switch {
		case strings.HasPrefix(trimmed, "diff ") || strings.HasPrefix(trimmed, "index ") ||
			strings.HasPrefix(trimmed, "+++") || strings.HasPrefix(trimmed, "---") ||
			strings.HasPrefix(trimmed, "new file") || strings.HasPrefix(trimmed, "deleted file") ||
			strings.HasPrefix(trimmed, "similarity index") || strings.HasPrefix(trimmed, "rename "):
			token = syntaxCommentToken
		case strings.HasPrefix(trimmed, "@@"):
			token = syntaxFunctionToken
		case strings.HasPrefix(trimmed, "+"):
			token = syntaxStringToken
		case strings.HasPrefix(trimmed, "-"):
			token = syntaxKeywordToken
		}
		if token != syntaxNone {
			builder.add(lineStart+indent, lineEnd, token)
		}
		if lineEnd >= len(code) {
			break
		}
		lineStart = lineEnd + 1
	}
	return builder.spans
}
