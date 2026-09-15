package tui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type AutocompleteItem struct {
	Value       string
	Label       string
	Description string
}

func DefaultSlashCommands() []AutocompleteItem {
	return []AutocompleteItem{
		{Value: "new", Label: "new", Description: "Start a new session"},
		{Value: "tree", Label: "tree", Description: "Open session tree navigator"},
		{Value: "fork", Label: "fork", Description: "Fork current session"},
		{Value: "resume", Label: "resume", Description: "Resume a session"},
		{Value: "sessions", Label: "sessions", Description: "List sessions"},
		{Value: "help", Label: "help", Description: "Show command help"},
		{Value: "clear", Label: "clear", Description: "Clear the transcript"},
		{Value: "model", Label: "model", Description: "Open model selector"},
		{Value: "thinking", Label: "thinking", Description: "Set thinking level"},
		{Value: "theme", Label: "theme", Description: "Change theme"},
	}
}

type autocompleteFileEntry struct {
	name  string
	isDir bool
}

type AutocompleteProvider struct {
	extraCommands []AutocompleteItem
	workspaceRoot string
	listFiles     func(dir string) ([]autocompleteFileEntry, error)
}

func NewAutocompleteProvider(workspaceRoot string) *AutocompleteProvider {
	return &AutocompleteProvider{
		workspaceRoot: workspaceRoot,
		listFiles:     defaultAutocompleteLister,
	}
}

func containsAutocompleteControl(name string) bool {
	for _, r := range name {
		if r <= 0x1F || r == 0x7F || (r >= 0x80 && r <= 0x9F) {
			return true
		}
	}
	return false
}

func defaultAutocompleteLister(dir string) ([]autocompleteFileEntry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]autocompleteFileEntry, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if name == ".git" {
			continue
		}
		if containsAutocompleteControl(name) {
			continue
		}
		isDir := entry.IsDir()
		if !isDir && entry.Type()&os.ModeSymlink != 0 {
			info, statErr := os.Stat(filepath.Join(dir, name))
			if statErr == nil {
				isDir = info.IsDir()
			}
		}
		out = append(out, autocompleteFileEntry{name: name, isDir: isDir})
	}
	return out, nil
}

func (p *AutocompleteProvider) SetExtraCommands(commands []AutocompleteItem) {
	p.extraCommands = append([]AutocompleteItem(nil), commands...)
}

func (p *AutocompleteProvider) SetWorkspaceRoot(root string) {
	p.workspaceRoot = root
}

func (p *AutocompleteProvider) SetLister(lister func(dir string) ([]autocompleteFileEntry, error)) {
	if lister == nil {
		p.listFiles = defaultAutocompleteLister
		return
	}
	p.listFiles = lister
}

func (p *AutocompleteProvider) allCommands() []AutocompleteItem {
	seen := make(map[string]struct{})
	var out []AutocompleteItem
	for _, cmd := range DefaultSlashCommands() {
		if _, dup := seen[cmd.Value]; dup {
			continue
		}
		seen[cmd.Value] = struct{}{}
		out = append(out, cmd)
	}
	for _, cmd := range p.extraCommands {
		if cmd.Value == "" {
			continue
		}
		if _, dup := seen[cmd.Value]; dup {
			continue
		}
		seen[cmd.Value] = struct{}{}
		out = append(out, cmd)
	}
	return out
}

func (p *AutocompleteProvider) SlashSuggestions(prefix string) []AutocompleteItem {
	trimmed := strings.TrimPrefix(prefix, "/")
	commands := p.allCommands()
	if strings.TrimSpace(trimmed) == "" {
		sorted := append([]AutocompleteItem(nil), commands...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].Value < sorted[j].Value })
		return sorted
	}
	filtered := FuzzyFilter(commands, trimmed, func(item AutocompleteItem) string { return item.Value })
	return filtered
}

func (p *AutocompleteProvider) BestSlashMatchIndex(items []AutocompleteItem, prefix string) int {
	trimmed := strings.TrimPrefix(prefix, "/")
	if trimmed == "" {
		return -1
	}
	bestPrefix := -1
	for i, item := range items {
		if item.Value == trimmed {
			return i
		}
		if bestPrefix == -1 && strings.HasPrefix(item.Value, trimmed) {
			bestPrefix = i
		}
	}
	return bestPrefix
}

func extractAtToken(textBeforeCursor string) (string, bool) {
	delimiters := " \t\"'=()[],"
	lastDelim := -1
	for i := len(textBeforeCursor) - 1; i >= 0; i-- {
		if strings.ContainsRune(delimiters, rune(textBeforeCursor[i])) {
			lastDelim = i
			break
		}
	}
	token := textBeforeCursor[lastDelim+1:]
	if !strings.HasPrefix(token, "@") || len(token) < 1 {
		return "", false
	}
	if strings.Contains(token[1:], " ") {
		return "", false
	}
	return token, true
}

func (p *AutocompleteProvider) workspaceBase() string {
	if p.workspaceRoot == "" {
		return "."
	}
	return p.workspaceRoot
}

func canonicalFilePath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

func withinCanonicalRoot(canonicalRoot, canonicalCandidate string) bool {
	rel, err := filepath.Rel(canonicalRoot, canonicalCandidate)
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

func constrainedJoin(root, joined string) (string, bool) {
	cleanedRoot := filepath.Clean(root)
	cleaned := filepath.Clean(joined)
	rel, err := filepath.Rel(cleanedRoot, cleaned)
	if err != nil {
		return "", false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	canonicalRoot := canonicalFilePath(cleanedRoot)
	canonicalCandidate := canonicalFilePath(cleaned)
	if !withinCanonicalRoot(canonicalRoot, canonicalCandidate) {
		return "", false
	}
	return cleaned, true
}

func (p *AutocompleteProvider) PathSuggestions(token string) []AutocompleteItem {
	if !strings.HasPrefix(token, "@") {
		return nil
	}
	raw := token[1:]
	raw = strings.TrimPrefix(raw, "\"")
	raw = strings.TrimSuffix(raw, "\"")
	var dirPart, filePart string
	if idx := strings.LastIndex(raw, "/"); idx >= 0 {
		dirPart = raw[:idx+1]
		filePart = raw[idx+1:]
	} else {
		dirPart = ""
		filePart = raw
	}
	root := p.workspaceBase()
	var searchDir string
	if strings.HasPrefix(dirPart, "/") {
		return nil
	}
	if dirPart == "" {
		searchDir = root
	} else {
		candidate := filepath.Join(root, filepath.FromSlash(dirPart))
		resolved, ok := constrainedJoin(root, candidate)
		if !ok {
			return nil
		}
		searchDir = resolved
	}
	entries, err := p.listFiles(searchDir)
	if err != nil {
		return nil
	}
	canonicalRoot := canonicalFilePath(filepath.Clean(root))
	type scored struct {
		item  AutocompleteItem
		score int
	}
	var matched []scored
	for _, entry := range entries {
		if containsAutocompleteControl(entry.name) {
			continue
		}
		isDir := entry.isDir
		if isDir {
			entryPath := filepath.Join(searchDir, entry.name)
			if resolved, err := filepath.EvalSymlinks(entryPath); err == nil {
				if !withinCanonicalRoot(canonicalRoot, resolved) {
					isDir = false
				}
			}
		}
		if filePart != "" && !strings.HasPrefix(strings.ToLower(entry.name), strings.ToLower(filePart)) {
			candidates := []string{entry.name}
			filtered := FuzzyFilter(candidates, filePart, func(s string) string { return s })
			if len(filtered) == 0 {
				continue
			}
		}
		displayBase := dirPart + entry.name
		if isDir {
			displayBase += "/"
		}
		value := "@" + displayBase
		label := entry.name
		if isDir {
			label += "/"
		}
		matched = append(matched, scored{item: AutocompleteItem{Value: value, Label: label, Description: displayBase}})
	}
	sort.Slice(matched, func(i, j int) bool {
		aDir := strings.HasSuffix(matched[i].item.Value, "/")
		bDir := strings.HasSuffix(matched[j].item.Value, "/")
		if aDir != bDir {
			return aDir
		}
		return matched[i].item.Label < matched[j].item.Label
	})
	out := make([]AutocompleteItem, 0, len(matched))
	for _, m := range matched {
		out = append(out, m.item)
	}
	if filePart != "" {
		out = FuzzyFilter(out, filePart, func(item AutocompleteItem) string { return strings.TrimSuffix(item.Label, "/") })
	}
	return out
}

func (p *AutocompleteProvider) ApplySlashCompletion(line string, cursor int, item AutocompleteItem, prefix string) (string, int) {
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(line) {
		cursor = len(line)
	}
	beforeLen := cursor - len(prefix)
	if beforeLen < 0 {
		beforeLen = 0
	}
	before := line[:beforeLen]
	after := line[cursor:]
	completed := before + "/" + item.Value + " " + after
	return completed, len(before) + len(item.Value) + 2
}

func (p *AutocompleteProvider) ApplyPathCompletion(line string, cursor int, item AutocompleteItem, prefix string) (string, int) {
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(line) {
		cursor = len(line)
	}
	beforeLen := cursor - len(prefix)
	if beforeLen < 0 {
		beforeLen = 0
	}
	before := line[:beforeLen]
	after := line[cursor:]
	isDir := strings.HasSuffix(item.Value, "/")
	suffix := ""
	if !isDir {
		suffix = " "
	}
	completed := before + item.Value + suffix + after
	return completed, len(before) + len(item.Value) + len(suffix)
}

func InlineHint(prefix string, selected AutocompleteItem) string {
	display := selected.Value
	if strings.HasPrefix(prefix, "/") && !strings.HasPrefix(display, "/") {
		display = "/" + display
	}
	if !strings.HasPrefix(display, prefix) {
		trimmedPrefix := strings.TrimPrefix(prefix, "/")
		trimmedDisplay := strings.TrimPrefix(display, "/")
		if strings.HasPrefix(trimmedDisplay, trimmedPrefix) {
			return trimmedDisplay[len(trimmedPrefix):]
		}
		return ""
	}
	return display[len(prefix):]
}

func isSlashContext(textBeforeCursor string, cursorLine int) bool {
	if cursorLine != 0 {
		return false
	}
	trimmed := strings.TrimLeft(textBeforeCursor, " \t")
	return strings.HasPrefix(trimmed, "/") && !strings.Contains(trimmed, " ")
}

func slashPrefixOf(textBeforeCursor string) (string, bool) {
	trimmedLeft := strings.TrimLeft(textBeforeCursor, " \t")
	if !strings.HasPrefix(trimmedLeft, "/") {
		return "", false
	}
	spaceIdx := strings.Index(trimmedLeft, " ")
	if spaceIdx >= 0 {
		return "", false
	}
	lead := len(textBeforeCursor) - len(trimmedLeft)
	return textBeforeCursor[lead:], true
}
