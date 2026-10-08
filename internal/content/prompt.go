package content

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxExpandedPromptBytes = 1 << 20

const promptDescriptionRunes = 80

var (
	errPromptUnmatchedSingleQuote = errors.New("prompt arguments: unmatched single quote")
	errPromptUnmatchedDoubleQuote = errors.New("prompt arguments: unmatched double quote")
)

type PromptInfo struct {
	Name        string
	Description string
	Package     string
	Path        string
	Tier        Tier
	Origin      string
}

type PromptCatalog struct {
	prompts map[string]PromptRef
	infos   []PromptInfo
	byName  map[string]PromptInfo
}

func NewPromptCatalog(s Snapshot) PromptCatalog {
	c := PromptCatalog{
		prompts: make(map[string]PromptRef),
		byName:  make(map[string]PromptInfo),
	}
	for name, ref := range s.Prompts {
		if !safePromptName(name) {
			continue
		}
		c.prompts[name] = ref
	}
	names := c.Names()
	c.infos = make([]PromptInfo, 0, len(names))
	for _, name := range names {
		ref := c.prompts[name]
		info := PromptInfo{
			Name:        name,
			Description: promptDescription(ref.Content),
			Package:     ref.Package,
			Path:        ref.Path,
			Tier:        ref.Tier,
			Origin:      ref.Origin,
		}
		c.infos = append(c.infos, info)
		c.byName[name] = info
	}
	return c
}

type PromptEntry struct {
	Ref  PromptRef
	Info PromptInfo
}

func (c PromptCatalog) Entries() []PromptEntry {
	entries := make([]PromptEntry, 0, len(c.infos))
	for _, info := range c.infos {
		entries = append(entries, PromptEntry{Ref: c.prompts[info.Name], Info: info})
	}
	return entries
}

func (c PromptCatalog) Names() []string {
	names := make([]string, 0, len(c.prompts))
	for name := range c.prompts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (c PromptCatalog) Lookup(name string) (PromptRef, bool) {
	ref, ok := c.prompts[name]
	return ref, ok
}

func (c PromptCatalog) Infos() []PromptInfo {
	infos := make([]PromptInfo, len(c.infos))
	copy(infos, c.infos)
	return infos
}

func (c PromptCatalog) Info(name string) (PromptInfo, bool) {
	info, ok := c.byName[name]
	return info, ok
}

func ParsePromptArguments(input string) ([]string, error) {
	var (
		args    []string
		current strings.Builder
		started bool
		quote   byte
		escaped bool
	)
	for i := 0; i < len(input); i++ {
		c := input[i]
		if escaped {
			current.WriteByte(c)
			started = true
			escaped = false
			continue
		}
		switch quote {
		case '\'':
			if c == '\'' {
				quote = 0
			} else {
				current.WriteByte(c)
			}
			started = true
		case '"':
			switch c {
			case '\\':
				escaped = true
			case '"':
				quote = 0
			default:
				current.WriteByte(c)
			}
			started = true
		default:
			switch {
			case c == '\\':
				escaped = true
				started = true
			case c == '\'' || c == '"':
				quote = c
				started = true
			case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f':
				if started {
					args = append(args, current.String())
					current.Reset()
					started = false
				}
			default:
				current.WriteByte(c)
				started = true
			}
		}
	}
	if quote == '\'' {
		return nil, errPromptUnmatchedSingleQuote
	}
	if quote == '"' {
		return nil, errPromptUnmatchedDoubleQuote
	}
	if escaped {
		current.WriteByte('\\')
	}
	if started {
		args = append(args, current.String())
	}
	return args, nil
}

func ExpandPromptTemplate(template string, args []string) (string, error) {
	var out strings.Builder
	var (
		joined      string
		joinedReady bool
	)
	allArguments := func() (string, error) {
		if joinedReady {
			return joined, nil
		}
		size := 0
		for i, arg := range args {
			if i > 0 {
				size++
			}
			size += len(arg)
		}
		if size > MaxExpandedPromptBytes-out.Len() {
			return "", promptBudgetError()
		}
		joined = strings.Join(args, " ")
		joinedReady = true
		return joined, nil
	}
	for i := 0; i < len(template); {
		if template[i] != '$' {
			end := i + 1
			for end < len(template) && template[end] != '$' {
				end++
			}
			if err := writePromptChunk(&out, template[i:end]); err != nil {
				return "", err
			}
			i = end
			continue
		}
		next := i + 1
		if next >= len(template) {
			if err := writePromptChunk(&out, "$"); err != nil {
				return "", err
			}
			break
		}
		switch {
		case template[next] == '@':
			value, err := allArguments()
			if err != nil {
				return "", err
			}
			if err := writePromptChunk(&out, value); err != nil {
				return "", err
			}
			i = next + 1
		case strings.HasPrefix(template[next:], "ARGUMENTS") && !promptIdentifierByte(template, next+len("ARGUMENTS")):
			value, err := allArguments()
			if err != nil {
				return "", err
			}
			if err := writePromptChunk(&out, value); err != nil {
				return "", err
			}
			i = next + len("ARGUMENTS")
		case template[next] >= '0' && template[next] <= '9':
			end := next
			for end < len(template) && template[end] >= '0' && template[end] <= '9' {
				end++
			}
			index, err := strconv.Atoi(template[next:end])
			if err != nil || index < 1 {
				if err := writePromptChunk(&out, "$"); err != nil {
					return "", err
				}
				i = next
				continue
			}
			if index <= len(args) {
				if err := writePromptChunk(&out, args[index-1]); err != nil {
					return "", err
				}
			}
			i = end
		default:
			if err := writePromptChunk(&out, "$"); err != nil {
				return "", err
			}
			i = next
		}
	}
	return out.String(), nil
}

func writePromptChunk(out *strings.Builder, value string) error {
	if len(value) > MaxExpandedPromptBytes-out.Len() {
		return promptBudgetError()
	}
	out.WriteString(value)
	return nil
}

func promptBudgetError() error {
	return fmt.Errorf("prompt: expanded content exceeds the %d byte limit", MaxExpandedPromptBytes)
}

func promptIdentifierByte(template string, at int) bool {
	if at >= len(template) {
		return false
	}
	c := template[at]
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func safePromptName(name string) bool {
	if name == "" || !utf8.ValidString(name) {
		return false
	}
	for _, r := range name {
		if unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '\u2028' || r == '\u2029' {
			return false
		}
	}
	return true
}

func promptDescription(body string) string {
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		return promptDisplayText(truncateRunes(trimmed, promptDescriptionRunes))
	}
	return ""
}

func promptDisplayText(text string) string {
	var out strings.Builder
	for _, r := range text {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

func truncateRunes(text string, limit int) string {
	count := 0
	for i := range text {
		if count == limit {
			return text[:i]
		}
		count++
	}
	return text
}
