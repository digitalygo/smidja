package ui

import (
	"errors"
	"fmt"
	"sync"

	"github.com/digitalygo/smidja/internal/tui"
	"github.com/digitalygo/smidja/sdk"
)

var ErrExtensionUINilEditor = errors.New("ui: editor factory returned nil")

func invokeEditorCallback(reporter func(kind string, recovered any), kind string, action func()) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if reporter != nil {
				reporter(kind, recovered)
			}
			err = fmt.Errorf("ui: %s callback panic: %v", kind, recovered)
		}
	}()
	action()
	return nil
}

func (r *Runner) invokeEditorAccessor(kind string, action func()) error {
	return invokeEditorCallback(r.reportExtensionPanic, kind, action)
}

func (r *Runner) readEditorText(custom sdk.EditorComponent) (text string, ok bool) {
	defer func() {
		if recovered := recover(); recovered != nil {
			r.reportExtensionPanic("editor-text", recovered)
			text = ""
			ok = false
		}
	}()
	text = custom.Text()
	r.ext.rememberEditorText(text)
	return text, true
}

func (r *Runner) SetEditorComponent(factory sdk.EditorFactory) error {
	if r.surface == nil {
		return ErrExtensionUIUnavailable
	}
	if !r.Active() {
		return sdk.ErrModeUnsupported
	}
	if factory == nil {
		if r.ext.isCleaned() {
			return ErrExtensionUIUnavailable
		}
		r.restoreDefaultEditor()
		return nil
	}
	current := r.GetEditorText()
	ctx := sdk.EditorContext{
		Theme:        sdkTheme{theme: r.surface.Theme()},
		Keybindings:  sdkKeybindings{manager: r.keys},
		TerminalRows: r.terminal.Rows(),
		Width:        r.surface.EditorWidth(),
	}
	var (
		component sdk.EditorComponent
		buildErr  error
	)
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				buildErr = fmt.Errorf("ui: editor factory panic: %v", recovered)
			}
		}()
		component = factory(ctx)
	}()
	if buildErr != nil {
		return buildErr
	}
	if component == nil {
		return ErrExtensionUINilEditor
	}
	if err := r.invokeEditorAccessor("editor-set-text", func() { component.SetText(current) }); err != nil {
		disposeTUIComponent(component)
		return err
	}
	adapter, err := newSDKEditorAdapter(component, r.reportExtensionPanic, r.ext.cachedEditorText)
	if err != nil {
		disposeTUIComponent(component)
		return err
	}
	adapter.SetOnSubmit(func(text string) { r.surface.NotifyEditorSubmit(text) })
	adapter.SetOnChange(func(text string) { r.surface.NotifyEditorChange(text) })
	r.ext.mu.Lock()
	if r.ext.cleaned {
		r.ext.mu.Unlock()
		disposeTUIComponent(adapter)
		return ErrExtensionUIUnavailable
	}
	r.ext.editorGeneration++
	previous := r.surface.ReplaceActiveEditor(adapter)
	r.view.SetFocus(adapter)
	r.ext.editorFactory = factory
	r.ext.editorComponent = component
	r.ext.mu.Unlock()
	disposeTUIComponent(previous)
	r.surface.RequestRender()
	return nil
}

func (r *Runner) GetEditorComponent() sdk.EditorComponent {
	if r.surface == nil {
		return nil
	}
	r.ext.mu.Lock()
	custom := r.ext.editorComponent
	if custom != nil {
		r.ext.mu.Unlock()
		return custom
	}
	if r.ext.editorDefault == nil {
		r.ext.editorDefault = newDefaultEditorComponent(r.surface.Editor(), r.reportExtensionPanic)
	}
	defaultComponent := r.ext.editorDefault
	r.ext.mu.Unlock()
	return defaultComponent
}

func (r *Runner) restoreDefaultEditor() {
	if r.surface == nil {
		return
	}
	r.ext.mu.Lock()
	component := r.ext.editorComponent
	r.ext.editorComponent = nil
	r.ext.editorFactory = nil
	generation := r.ext.editorGeneration
	defaultEditor := r.surface.Editor()
	r.ext.mu.Unlock()
	if component != nil {
		current := ""
		if text, ok := r.readEditorText(component); ok {
			current = text
		} else {
			current = r.ext.cachedEditorText()
		}
		defaultEditor.SetText(current)
	}
	r.ext.mu.Lock()
	if r.ext.editorGeneration != generation {
		r.ext.mu.Unlock()
		return
	}
	r.ext.editorGeneration++
	previous := r.surface.ReplaceActiveEditor(defaultEditor)
	r.view.SetFocus(defaultEditor)
	r.ext.mu.Unlock()
	disposeTUIComponent(previous)
	r.surface.RequestRender()
}

func (r *Runner) PasteToEditor(text string) {
	if r.surface == nil || text == "" {
		return
	}
	r.ext.mu.Lock()
	custom := r.ext.editorComponent
	r.ext.mu.Unlock()
	if custom == nil {
		r.surface.Editor().Paste(text)
		r.ext.rememberEditorText(r.surface.Editor().Text())
		return
	}
	if inserter, ok := custom.(sdk.EditorInserter); ok {
		r.invokeEditorAccessor("editor-insert-text", func() { inserter.InsertTextAtCursor(text) })
		return
	}
	current, ok := r.readEditorText(custom)
	if !ok {
		current = r.ext.cachedEditorText()
	}
	updated := current + text
	if r.invokeEditorAccessor("editor-set-text", func() { custom.SetText(updated) }) == nil {
		r.ext.rememberEditorText(updated)
	}
}

func (r *Runner) SetEditorText(text string) {
	if r.surface == nil {
		return
	}
	r.ext.mu.Lock()
	custom := r.ext.editorComponent
	r.ext.mu.Unlock()
	if custom != nil {
		if r.invokeEditorAccessor("editor-set-text", func() { custom.SetText(text) }) == nil {
			r.ext.rememberEditorText(text)
		}
		return
	}
	r.surface.Editor().SetText(text)
	r.ext.rememberEditorText(text)
}

func (r *Runner) GetEditorText() string {
	if r.surface == nil {
		return ""
	}
	r.ext.mu.Lock()
	custom := r.ext.editorComponent
	r.ext.mu.Unlock()
	if custom != nil {
		text, ok := r.readEditorText(custom)
		if !ok {
			return r.ext.cachedEditorText()
		}
		return text
	}
	text := r.surface.Editor().Text()
	r.ext.rememberEditorText(text)
	return text
}

func (r *Runner) AddAutocompleteProvider(provider sdk.AutocompleteProvider) (func(), error) {
	if !r.Active() {
		return nil, sdk.ErrModeUnsupported
	}
	if provider == nil {
		return nil, ErrExtensionUINilHandler
	}
	if r.surface == nil {
		return nil, ErrExtensionUIUnavailable
	}
	r.ext.mu.Lock()
	if r.ext.cleaned {
		r.ext.mu.Unlock()
		return nil, ErrExtensionUIUnavailable
	}
	r.ext.nextAutocomplete++
	id := r.ext.nextAutocomplete
	r.ext.autocomplete = append(r.ext.autocomplete, registeredAutocomplete{id: id, provider: provider})
	r.ext.mu.Unlock()
	r.ensureAutocompleteRegistration()
	var once sync.Once
	return func() {
		once.Do(func() {
			r.ext.mu.Lock()
			for index, registered := range r.ext.autocomplete {
				if registered.id == id {
					r.ext.autocomplete = append(r.ext.autocomplete[:index], r.ext.autocomplete[index+1:]...)
					break
				}
			}
			r.ext.mu.Unlock()
		})
	}, nil
}

func (r *Runner) ensureAutocompleteRegistration() {
	r.ext.mu.Lock()
	defer r.ext.mu.Unlock()
	if r.ext.autocompleteRegistered || r.surface == nil {
		return
	}
	r.ext.autocompleteRegistered = true
	r.ext.autocompleteStop = r.surface.Editor().AddAutocompleteProvider(r.runExtensionAutocomplete)
}

func (r *Runner) runExtensionAutocomplete(ctx tui.ExternalAutocompleteContext) []tui.AutocompleteItem {
	r.ext.mu.Lock()
	providers := make([]sdk.AutocompleteProvider, 0, len(r.ext.autocomplete))
	for _, registered := range r.ext.autocomplete {
		if registered.provider != nil {
			providers = append(providers, registered.provider)
		}
	}
	r.ext.mu.Unlock()
	var items []tui.AutocompleteItem
	for _, provider := range providers {
		suggestions, ok := r.runAutocompleteProvider(provider, ctx)
		if !ok {
			continue
		}
		for _, suggestion := range suggestions {
			items = append(items, tui.AutocompleteItem{
				Value:       suggestion.Value,
				Label:       suggestion.Label,
				Description: suggestion.Description,
			})
		}
	}
	return items
}

func (r *Runner) runAutocompleteProvider(provider sdk.AutocompleteProvider, ctx tui.ExternalAutocompleteContext) (suggestions []sdk.AutocompleteSuggestion, ok bool) {
	defer func() {
		if recovered := recover(); recovered != nil {
			ok = false
			r.reportExtensionPanic("autocomplete-provider", recovered)
		}
	}()
	return provider.Suggest(sdk.AutocompleteContext{
		Text:  ctx.Text,
		Line:  ctx.Line,
		Col:   ctx.Col,
		Token: ctx.Token,
		Kind:  ctx.Kind,
		Width: ctx.Width,
	}), true
}

func (r *Runner) syncEditorForExternalEditor() {
	r.ext.mu.Lock()
	custom := r.ext.editorComponent
	r.ext.mu.Unlock()
	if custom == nil || r.surface == nil {
		return
	}
	text, ok := r.readEditorText(custom)
	if !ok {
		text = r.ext.cachedEditorText()
	}
	r.surface.Editor().SetText(text)
}

func (r *Runner) syncEditorFromExternalEditor() {
	r.ext.mu.Lock()
	custom := r.ext.editorComponent
	r.ext.mu.Unlock()
	if custom == nil || r.surface == nil {
		return
	}
	text := r.surface.Editor().Text()
	if r.invokeEditorAccessor("editor-set-text", func() { custom.SetText(text) }) == nil {
		r.ext.rememberEditorText(text)
	}
}

func (r *Runner) stopAutocompleteProviders() {
	if r.ext == nil {
		return
	}
	r.ext.mu.Lock()
	stop := r.ext.autocompleteStop
	r.ext.autocompleteStop = nil
	r.ext.autocompleteRegistered = false
	r.ext.autocomplete = nil
	r.ext.mu.Unlock()
	if stop != nil {
		stop()
	}
}
