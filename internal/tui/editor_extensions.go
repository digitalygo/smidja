package tui

import (
	"strings"
	"sync"
)

type ExternalAutocompleteContext struct {
	Text  string
	Line  int
	Col   int
	Token string
	Kind  string
	Width int
}

type ExternalAutocompleteProvider func(ctx ExternalAutocompleteContext) []AutocompleteItem

type externalAutocompleteProvider struct {
	id       int
	provider ExternalAutocompleteProvider
}

func (e *Editor) AddAutocompleteProvider(provider ExternalAutocompleteProvider) func() {
	if provider == nil {
		return func() {}
	}
	e.mu.Lock()
	e.nextExternalProviderID++
	id := e.nextExternalProviderID
	e.externalProviders = append(e.externalProviders, externalAutocompleteProvider{id: id, provider: provider})
	e.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			e.mu.Lock()
			for index, registered := range e.externalProviders {
				if registered.id == id {
					e.externalProviders = append(e.externalProviders[:index], e.externalProviders[index+1:]...)
					break
				}
			}
			if len(e.externalProviders) == 0 && e.autocompleteKind == "external" {
				e.dismissAutocompleteLocked()
			}
			e.mu.Unlock()
		})
	}
}

func (e *Editor) externalAutocompleteContextLocked() ExternalAutocompleteContext {
	line, col := e.buffer.Cursor()
	text := e.buffer.Text()
	before := e.currentLineBeforeCursorLocked()
	token := before
	if idx := strings.LastIndexAny(token, " \t\"'=()[],"); idx >= 0 {
		token = token[idx+1:]
	}
	kind := "text"
	if _, ok := slashPrefixOf(before); ok && line == 0 {
		kind = "slash"
	} else if strings.HasPrefix(token, "@") {
		kind = "path"
	}
	return ExternalAutocompleteContext{
		Text:  text,
		Line:  line,
		Col:   col,
		Token: token,
		Kind:  kind,
		Width: e.lastWidth,
	}
}

func sanitizeAutocompleteText(text string) string {
	if !strings.ContainsFunc(text, isAutocompleteControl) {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		if isAutocompleteControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func isAutocompleteControl(r rune) bool {
	return r <= 0x1f || r == 0x7f || (r >= 0x80 && r <= 0x9f)
}

func sanitizeAutocompleteItems(items []AutocompleteItem) []AutocompleteItem {
	out := make([]AutocompleteItem, 0, len(items))
	for _, item := range items {
		value := sanitizeAutocompleteText(item.Value)
		if value == "" {
			continue
		}
		out = append(out, AutocompleteItem{
			Value:       value,
			Label:       sanitizeAutocompleteText(item.Label),
			Description: sanitizeAutocompleteText(item.Description),
		})
	}
	return out
}

func mergeAutocompleteItems(external []AutocompleteItem, builtin []AutocompleteItem) []AutocompleteItem {
	merged := make([]AutocompleteItem, 0, len(external)+len(builtin))
	seen := make(map[string]struct{}, len(external)+len(builtin))
	for _, item := range append(append([]AutocompleteItem{}, external...), builtin...) {
		if item.Value == "" {
			continue
		}
		if _, ok := seen[item.Value]; ok {
			continue
		}
		seen[item.Value] = struct{}{}
		merged = append(merged, item)
	}
	return merged
}

func externalAutocompleteCompletion(line string, col int, value string, tokenStart int) (string, int) {
	if col < 0 {
		col = 0
	}
	if col > len(line) {
		col = len(line)
	}
	if tokenStart < 0 {
		tokenStart = 0
	}
	if tokenStart > col {
		tokenStart = col
	}
	tokenStart = clampRuneIndex(line, tokenStart)
	col = clampRuneIndex(line, col)
	cleaned := sanitizeAutocompleteText(value)
	completed := line[:tokenStart] + cleaned + line[col:]
	return completed, tokenStart + len(cleaned)
}

func clampRuneIndex(text string, index int) int {
	if index <= 0 {
		return 0
	}
	if index >= len(text) {
		return len(text)
	}
	for index < len(text) && !utf8Start(text[index]) {
		index++
	}
	return index
}

func utf8Start(b byte) bool {
	return b&0xc0 != 0x80
}

func (e *Editor) flushExternalAutocomplete() {
	e.mu.Lock()
	if len(e.externalProviders) == 0 || !e.autocompleteDirty || e.autocompleteFlushing {
		e.mu.Unlock()
		return
	}
	e.autocompleteFlushing = true
	e.autocompleteDirty = false
	force := e.autocompleteForce
	e.autocompleteForce = false
	providers := make([]ExternalAutocompleteProvider, 0, len(e.externalProviders))
	for _, registered := range e.externalProviders {
		providers = append(providers, registered.provider)
	}
	context := e.externalAutocompleteContextLocked()
	beforeText := e.buffer.Text()
	prefix, builtinItems, builtinKind, builtinOK := e.builtinAutocompleteSuggestionsLocked(force)
	e.mu.Unlock()

	var externalItems []AutocompleteItem
	for _, provider := range providers {
		externalItems = append(externalItems, sanitizeAutocompleteItems(provider(context))...)
	}

	e.mu.Lock()
	defer func() {
		e.autocompleteFlushing = false
		e.mu.Unlock()
	}()
	if e.buffer.Text() != beforeText {
		return
	}
	hasExternal := len(externalItems) > 0
	switch {
	case builtinOK:
		merged := mergeAutocompleteItems(externalItems, builtinItems)
		if len(merged) == 0 {
			e.dismissAutocompleteLocked()
			return
		}
		e.showAutocompleteLocked(prefix, merged, builtinKind)
	case hasExternal:
		tokenStart := context.Col - len(context.Token)
		if tokenStart < 0 {
			tokenStart = 0
		}
		e.autocompleteTokenStart = tokenStart
		e.showAutocompleteLocked(context.Token, externalItems, "external")
	default:
		e.dismissAutocompleteLocked()
	}
}

func (e *Editor) Paste(text string) {
	if text == "" {
		return
	}
	e.mu.Lock()
	e.buffer.HandlePaste(text)
	e.exitHistoryLocked()
	e.dismissAutocompleteLocked()
	e.afterEditLocked()
	changed := e.buffer.Text()
	onChange := e.onChange
	e.mu.Unlock()
	if onChange != nil {
		onChange(changed)
	}
}
