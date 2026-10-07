package agents

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/digitalygo/smidja/internal/content"
	"github.com/digitalygo/smidja/sdk"
)

const (
	descriptionRunes = 80
	displayNameRunes = 120
	errorRunes       = 200
	toolNameRunes    = 200
)

var (
	errAgentBody        = errors.New("the agent body is empty")
	errAgentFrontmatter = errors.New("the frontmatter is not terminated")
	errAgentMetadata    = errors.New("unsupported frontmatter syntax")
	errAgentType        = errors.New("unsupported frontmatter value type")
	errAgentDuplicate   = errors.New("duplicate frontmatter field")
	errAgentThinking    = errors.New("unsupported thinking level")
)

var knownMetadataKeys = map[string]struct{}{
	"name":        {},
	"description": {},
	"model":       {},
	"tools":       {},
	"thinking":    {},
}

type Definition struct {
	Name        string
	DisplayName string
	Description string
	Body        string
	Model       string
	Tools       []string
	ToolsSet    bool
	Thinking    sdk.ThinkingLevel
	ThinkingSet bool
	Package     string
	Path        string
	Tier        content.Tier
	Origin      string
}

func parseDefinition(ref content.AgentRef) (Definition, error) {
	def := Definition{
		Name:    ref.Name,
		Package: ref.Package,
		Path:    ref.Path,
		Tier:    ref.Tier,
		Origin:  ref.Origin,
	}
	metadata, body, err := splitFrontmatter(ref.Content)
	if err != nil {
		return Definition{}, err
	}
	if err := applyMetadata(&def, metadata); err != nil {
		return Definition{}, err
	}
	def.Body = strings.TrimSpace(body)
	if def.Body == "" {
		return Definition{}, errAgentBody
	}
	if def.DisplayName == "" {
		def.DisplayName = def.Name
	}
	if def.Description == "" {
		def.Description = firstBodyLine(def.Body)
	}
	return def, nil
}

func splitFrontmatter(raw string) ([]string, string, error) {
	lines := strings.Split(raw, "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r") != "---" {
		return nil, raw, nil
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r") != "---" {
			continue
		}
		return lines[1:i], strings.Join(lines[i+1:], "\n"), nil
	}
	return nil, "", errAgentFrontmatter
}

func applyMetadata(def *Definition, lines []string) error {
	seen := make(map[string]struct{}, len(lines))
	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			return errAgentMetadata
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return errAgentMetadata
		}
		key = strings.TrimSpace(key)
		if !validMetadataKey(key) {
			return errAgentMetadata
		}
		value = strings.TrimSpace(value)
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("%w %q", errAgentDuplicate, key)
		}
		seen[key] = struct{}{}
		if _, known := knownMetadataKeys[key]; !known {
			if value == "" && nextIsListLine(lines, i+1) {
				return errAgentType
			}
			if value != "" && strings.HasPrefix(value, "{") {
				return errAgentType
			}
			continue
		}
		switch key {
		case "name":
			scalar, err := scalarMetadata(lines, i, value)
			if err != nil {
				return err
			}
			def.DisplayName = truncateRunes(scalar, displayNameRunes)
		case "description":
			scalar, err := scalarMetadata(lines, i, value)
			if err != nil {
				return err
			}
			def.Description = sanitizeDisplayText(truncateRunes(scalar, descriptionRunes))
		case "model":
			scalar, err := scalarMetadata(lines, i, value)
			if err != nil {
				return err
			}
			def.Model = scalar
		case "thinking":
			scalar, err := scalarMetadata(lines, i, value)
			if err != nil {
				return err
			}
			if scalar == "" {
				continue
			}
			level, err := parseThinkingLevel(scalar)
			if err != nil {
				return err
			}
			def.Thinking = level
			def.ThinkingSet = true
		case "tools":
			items, consumed, err := parseListMetadata(lines, i, value)
			if err != nil {
				return err
			}
			tools, err := normalizeToolNames(items)
			if err != nil {
				return err
			}
			def.Tools = tools
			def.ToolsSet = true
			i += consumed
		}
	}
	return nil
}

func scalarMetadata(lines []string, index int, value string) (string, error) {
	if value == "" && nextIsListLine(lines, index+1) {
		return "", errAgentType
	}
	if strings.HasPrefix(value, "[") || strings.HasPrefix(value, "{") {
		return "", errAgentType
	}
	unquoted := unquoteValue(value)
	if !validScalarValue(unquoted) {
		return "", errAgentMetadata
	}
	return unquoted, nil
}

func parseListMetadata(lines []string, index int, value string) ([]string, int, error) {
	if value != "" {
		if strings.HasPrefix(value, "[") {
			if !strings.HasSuffix(value, "]") {
				return nil, 0, errAgentType
			}
			inner := strings.TrimSpace(value[1 : len(value)-1])
			if inner == "" {
				return []string{}, 0, nil
			}
			return splitCommaItems(inner), 0, nil
		}
		if strings.HasPrefix(value, "{") {
			return nil, 0, errAgentType
		}
		return splitCommaItems(value), 0, nil
	}
	items := make([]string, 0)
	consumed := 0
	for _, line := range lines[index+1:] {
		trimmed := strings.TrimSpace(strings.TrimRight(line, "\r"))
		if trimmed == "" {
			consumed++
			continue
		}
		if !strings.HasPrefix(trimmed, "-") {
			break
		}
		item := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
		if item == "" {
			return nil, 0, errAgentMetadata
		}
		items = append(items, unquoteValue(item))
		consumed++
	}
	return items, consumed, nil
}

func splitCommaItems(value string) []string {
	parts := strings.Split(value, ",")
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		items = append(items, unquoteValue(strings.TrimSpace(part)))
	}
	return items
}

func normalizeToolNames(items []string) ([]string, error) {
	out := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		name := strings.TrimSpace(item)
		if name == "" {
			continue
		}
		if !validToolName(name) {
			return nil, fmt.Errorf("unsupported tool name %q", bounded(name))
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out, nil
}

func parseThinkingLevel(value string) (sdk.ThinkingLevel, error) {
	normalized := sdk.ThinkingLevel(strings.ToLower(strings.TrimSpace(value)))
	switch normalized {
	case sdk.ThinkingOff, sdk.ThinkingMinimal, sdk.ThinkingLow, sdk.ThinkingMedium,
		sdk.ThinkingHigh, sdk.ThinkingXHigh, sdk.ThinkingMax, sdk.ThinkingDefault:
		return normalized, nil
	default:
		return "", fmt.Errorf("%w %q", errAgentThinking, bounded(value))
	}
}

func nextIsListLine(lines []string, index int) bool {
	if index >= len(lines) {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(strings.TrimRight(lines[index], "\r")), "-")
}

func validMetadataKey(key string) bool {
	if key == "" {
		return false
	}
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

func validToolName(name string) bool {
	if name == "" || utf8.RuneCountInString(name) > toolNameRunes {
		return false
	}
	for _, r := range name {
		if unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return false
		}
	}
	return true
}

func validScalarValue(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '\u2028' || r == '\u2029' {
			return false
		}
	}
	return true
}

func unquoteValue(value string) string {
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}

func safeAgentName(name string) bool {
	if name == "" || !utf8.ValidString(name) {
		return false
	}
	if strings.ContainsRune(name, '\\') {
		return false
	}
	for _, segment := range strings.Split(name, "/") {
		if segment == "" || segment == "." || segment == ".." || strings.HasPrefix(segment, ".") {
			return false
		}
	}
	for _, r := range name {
		if unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '\u2028' || r == '\u2029' {
			return false
		}
	}
	return true
}

func firstBodyLine(body string) string {
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		return sanitizeDisplayText(truncateRunes(trimmed, descriptionRunes))
	}
	return ""
}

func sanitizeDisplayText(text string) string {
	var out strings.Builder
	for _, r := range text {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		out.WriteRune(r)
	}
	return strings.TrimSpace(out.String())
}

func truncateRunes(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	count := 0
	for i := range text {
		if count == limit {
			return text[:i]
		}
		count++
	}
	return text
}

func bounded(text string) string {
	clean := sanitizeDisplayText(text)
	if clean == "" && text != "" {
		return "(unsupported)"
	}
	return truncateRunes(clean, errorRunes)
}
