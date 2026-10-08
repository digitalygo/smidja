package interactive

import (
	"strings"
	"unicode/utf8"
)

const (
	mathMaxSource       = 4096
	mathMaxDepth        = 8
	mathMaxOutput       = 8192
	mathFallbackWarning = "unsupported or over-budget expression; showing source"
)

var mathSymbols = map[string]string{
	"alpha": "α", "beta": "β", "gamma": "γ", "delta": "δ", "epsilon": "ε",
	"varepsilon": "ε", "zeta": "ζ", "eta": "η", "theta": "θ", "vartheta": "ϑ",
	"iota": "ι", "kappa": "κ", "lambda": "λ", "mu": "μ", "nu": "ν", "xi": "ξ",
	"pi": "π", "varpi": "ϖ", "rho": "ρ", "sigma": "σ", "varsigma": "ς", "tau": "τ",
	"upsilon": "υ", "phi": "φ", "varphi": "φ", "chi": "χ", "psi": "ψ", "omega": "ω",
	"Gamma": "Γ", "Delta": "Δ", "Theta": "Θ", "Lambda": "Λ", "Xi": "Ξ", "Pi": "Π",
	"Sigma": "Σ", "Upsilon": "Υ", "Phi": "Φ", "Psi": "Ψ", "Omega": "Ω",
	"times": "×", "cdot": "·", "pm": "±", "mp": "∓", "le": "≤", "leq": "≤",
	"ge": "≥", "geq": "≥", "neq": "≠", "ne": "≠", "approx": "≈", "equiv": "≡",
	"infty": "∞", "sum": "∑", "prod": "∏", "int": "∫", "oint": "∮",
	"to": "→", "rightarrow": "→", "leftarrow": "←", "leftrightarrow": "↔",
	"Rightarrow": "⇒", "Leftarrow": "⇐", "Leftrightarrow": "⇔",
	"in": "∈", "notin": "∉", "subset": "⊂", "subseteq": "⊆", "supset": "⊃",
	"supseteq": "⊇", "cup": "∪", "cap": "∩", "emptyset": "∅", "varnothing": "∅",
	"forall": "∀", "exists": "∃", "nexists": "∄", "partial": "∂", "nabla": "∇",
	"degree": "°", "angle": "∠", "perp": "⊥", "parallel": "∥",
	"ldots": "…", "cdots": "⋯", "dots": "…", "vdots": "⋮", "ddots": "⋱",
	"langle": "⟨", "rangle": "⟩", "lceil": "⌈", "rceil": "⌉", "lfloor": "⌊", "rfloor": "⌋",
	"star": "⋆", "ast": "∗", "circ": "∘", "bullet": "•", "oplus": "⊕", "otimes": "⊗",
	"land": "∧", "lor": "∨", "neg": "¬", "lnot": "¬", "implies": "⟹", "iff": "⟺",
}

var superscripts = map[rune]rune{
	'0': '⁰', '1': '¹', '2': '²', '3': '³', '4': '⁴', '5': '⁵', '6': '⁶',
	'7': '⁷', '8': '⁸', '9': '⁹', '+': '⁺', '-': '⁻', '=': '⁼', '(': '⁽',
	')': '⁾', 'n': 'ⁿ', 'i': 'ⁱ', 'x': 'ˣ', 'a': 'ᵃ', 'b': 'ᵇ', 'c': 'ᶜ',
	'd': 'ᵈ', 'e': 'ᵉ', 'f': 'ᶠ', 'g': 'ᵍ', 'h': 'ʰ', 'j': 'ʲ', 'k': 'ᵏ',
	'l': 'ˡ', 'm': 'ᵐ', 'o': 'ᵒ', 'p': 'ᵖ', 'r': 'ʳ', 's': 'ˢ', 't': 'ᵗ',
	'u': 'ᵘ', 'v': 'ᵛ', 'w': 'ʷ', 'y': 'ʸ', 'z': 'ᶻ', 'A': 'ᴬ', 'B': 'ᴮ',
	'D': 'ᴰ', 'E': 'ᴱ', 'G': 'ᴳ', 'H': 'ᴴ', 'I': 'ᴵ', 'J': 'ᴶ', 'K': 'ᴷ',
	'L': 'ᴸ', 'M': 'ᴹ', 'N': 'ᴺ', 'O': 'ᴼ', 'P': 'ᴾ', 'R': 'ᴿ', 'T': 'ᵀ',
	'U': 'ᵁ', 'V': 'ⱽ', 'W': 'ᵂ',
}

var subscripts = map[rune]rune{
	'0': '₀', '1': '₁', '2': '₂', '3': '₃', '4': '₄', '5': '₅', '6': '₆',
	'7': '₇', '8': '₈', '9': '₉', '+': '₊', '-': '₋', '=': '₌', '(': '₍',
	')': '₎', 'a': 'ₐ', 'e': 'ₑ', 'h': 'ₕ', 'i': 'ᵢ', 'j': 'ⱼ', 'k': 'ₖ',
	'l': 'ₗ', 'm': 'ₘ', 'n': 'ₙ', 'o': 'ₒ', 'p': 'ₚ', 'r': 'ᵣ', 's': 'ₛ',
	't': 'ₜ', 'u': 'ᵤ', 'v': 'ᵥ', 'x': 'ₓ',
}

type mathParser struct {
	source string
	pos    int
	depth  int
	budget int
}

type mathSpan struct {
	end     int
	content string
}

func findMathSpan(source string, pos int) (mathSpan, bool) {
	if pos >= len(source) || source[pos] != '$' {
		return mathSpan{}, false
	}
	display := strings.HasPrefix(source[pos:], "$$")
	delimiter := "$"
	if display {
		delimiter = "$$"
	}
	contentStart := pos + len(delimiter)
	closeIndex := findMathClose(source, contentStart, delimiter)
	if closeIndex < 0 {
		return mathSpan{}, false
	}
	content := source[contentStart:closeIndex]
	if content == "" {
		return mathSpan{}, false
	}
	if !display {
		if isMathSpace(content[0]) || isMathSpace(content[len(content)-1]) {
			return mathSpan{}, false
		}
		if isDigit(content[0]) {
			return mathSpan{}, false
		}
		after := closeIndex + len(delimiter)
		if after < len(source) && (isDigit(source[after]) || isIdentStart(source[after])) {
			return mathSpan{}, false
		}
	}
	return mathSpan{end: closeIndex + len(delimiter), content: content}, true
}

func parseMathExpression(content string) (string, bool) {
	if content == "" || len(content) > mathMaxSource {
		return "", false
	}
	parser := &mathParser{source: content, budget: mathMaxOutput}
	rendered, ok := parser.parse(0)
	if !ok || parser.pos != len(content) {
		return "", false
	}
	return rendered, true
}

func parseMathSpan(source string, pos int) (string, int, bool) {
	span, ok := findMathSpan(source, pos)
	if !ok {
		return "", 0, false
	}
	rendered, ok := parseMathExpression(span.content)
	if !ok {
		return "", 0, false
	}
	return rendered, span.end - pos, true
}

func isMathSpace(char byte) bool {
	return char == ' ' || char == '\t'
}

func findMathClose(source string, start int, delimiter string) int {
	index := start
	for index < len(source) {
		if source[index] == '\\' {
			index += 2
			continue
		}
		if strings.HasPrefix(source[index:], delimiter) {
			return index
		}
		if source[index] == '\n' {
			return -1
		}
		index++
	}
	return -1
}

func (p *mathParser) parse(depth int) (string, bool) {
	if depth > mathMaxDepth {
		return "", false
	}
	var builder strings.Builder
	for p.pos < len(p.source) {
		if p.budget <= 0 {
			return "", false
		}
		char := p.source[p.pos]
		switch char {
		case '\\':
			rendered, ok := p.parseCommand(depth)
			if !ok {
				return "", false
			}
			builder.WriteString(rendered)
		case '{':
			p.pos++
			inner, ok := p.parse(depth + 1)
			if !ok || p.pos >= len(p.source) || p.source[p.pos] != '}' {
				return "", false
			}
			p.pos++
			builder.WriteString(inner)
		case '}':
			return builder.String(), true
		case '^', '_':
			marker := char
			p.pos++
			group, ok := p.parseGroupArgument(depth)
			if !ok {
				return "", false
			}
			converted, ok := convertScript(group, marker == '^')
			if !ok {
				return "", false
			}
			builder.WriteString(converted)
		default:
			r, size := utf8.DecodeRuneInString(p.source[p.pos:])
			builder.WriteRune(r)
			p.pos += size
		}
		p.budget -= builder.Len()/2 + 1
	}
	return builder.String(), true
}

func (p *mathParser) parseCommand(depth int) (string, bool) {
	p.pos++
	start := p.pos
	for p.pos < len(p.source) && (p.source[p.pos] >= 'a' && p.source[p.pos] <= 'z' || p.source[p.pos] >= 'A' && p.source[p.pos] <= 'Z') {
		p.pos++
	}
	name := p.source[start:p.pos]
	if name == "" {
		if p.pos < len(p.source) {
			r, size := utf8.DecodeRuneInString(p.source[p.pos:])
			p.pos += size
			return string(r), true
		}
		return "", false
	}
	switch name {
	case "frac":
		numerator, ok := p.parseGroupArgument(depth)
		if !ok {
			return "", false
		}
		denominator, ok := p.parseGroupArgument(depth)
		if !ok {
			return "", false
		}
		return renderFraction(numerator, denominator), true
	case "sqrt":
		argument, ok := p.parseGroupArgument(depth)
		if !ok {
			return "", false
		}
		if utf8.RuneCountInString(argument) <= 1 {
			return "√" + argument, true
		}
		return "√(" + argument + ")", true
	case "text", "mathrm", "mathbf", "mathit":
		return p.parseGroupArgument(depth)
	}
	symbol, ok := mathSymbols[name]
	if !ok {
		return "", false
	}
	return symbol, true
}

func (p *mathParser) parseGroupArgument(depth int) (string, bool) {
	if p.pos >= len(p.source) {
		return "", false
	}
	if p.source[p.pos] == '{' {
		p.pos++
		inner, ok := p.parse(depth + 1)
		if !ok || p.pos >= len(p.source) || p.source[p.pos] != '}' {
			return "", false
		}
		p.pos++
		return inner, true
	}
	r, size := utf8.DecodeRuneInString(p.source[p.pos:])
	if r == '\\' {
		p.pos++
		start := p.pos
		for p.pos < len(p.source) && (p.source[p.pos] >= 'a' && p.source[p.pos] <= 'z' || p.source[p.pos] >= 'A' && p.source[p.pos] <= 'Z') {
			p.pos++
		}
		name := p.source[start:p.pos]
		if symbol, ok := mathSymbols[name]; ok {
			return symbol, true
		}
		return "", false
	}
	p.pos += size
	return string(r), true
}

func renderFraction(numerator, denominator string) string {
	if utf8.RuneCountInString(numerator) <= 1 && utf8.RuneCountInString(denominator) <= 1 {
		return numerator + "/" + denominator
	}
	return "(" + numerator + ")/(" + denominator + ")"
}

func convertScript(text string, superscript bool) (string, bool) {
	table := subscripts
	if superscript {
		table = superscripts
	}
	var builder strings.Builder
	for _, r := range text {
		if r == ' ' {
			builder.WriteRune(' ')
			continue
		}
		converted, ok := table[r]
		if !ok {
			return "", false
		}
		builder.WriteRune(converted)
	}
	return builder.String(), true
}
