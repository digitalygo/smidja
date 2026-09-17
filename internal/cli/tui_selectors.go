package cli

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/digitalygo/smidja/internal/tui"
	"github.com/digitalygo/smidja/internal/tui/interactive"
	"github.com/digitalygo/smidja/internal/ui"
)

const (
	multiModelTransport       = "openrouter"
	currentModelProviderLabel = "current"
)

var transportModelProviderAliases = map[string]string{
	"openrouter-oauth":       multiModelTransport,
	"anthropic-oauth":        "anthropic",
	"codex":                  "openai",
	"azure-openai-responses": "openai",
	"xai-subscription":       "xai",
	"kimi-coding-oauth":      "kimi",
	"kimi-coding":            "kimi",
	"gemini":                 "google",
}

func canonicalTransportProvider(transport string) string {
	trimmed := strings.TrimSpace(transport)
	if mapped, ok := transportModelProviderAliases[trimmed]; ok {
		return mapped
	}
	return trimmed
}

func transportSupportsModel(transport, modelProvider string) bool {
	canonical := canonicalTransportProvider(transport)
	if canonical == "" || canonical == multiModelTransport {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(modelProvider), canonical)
}

var fixedDeploymentTransports = map[string]struct{}{
	"azure-openai-responses": {},
}

func transportAllowsModelSelection(transport string) bool {
	_, fixed := fixedDeploymentTransports[strings.TrimSpace(transport)]
	return !fixed
}

func (b *tuiBridge) warn(err error) {
	if err == nil {
		return
	}
	b.runner.Surface().AddNotice(interactive.NoticeWarning, err.Error())
}

func (b *tuiBridge) inform(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	b.runner.Surface().AddNotice(interactive.NoticeInfo, text)
}

func (b *tuiBridge) helpEntries() []ui.HelpEntry {
	entries := make([]ui.HelpEntry, 0, 8)
	if b.rd.commands != nil {
		for _, command := range b.rd.commands.List() {
			entries = append(entries, ui.HelpEntry{Name: command.Name, Description: command.Description})
		}
	}
	entries = append(entries,
		ui.HelpEntry{Name: "help", Description: "show command help"},
		ui.HelpEntry{Name: "model", Description: "select the model for the next turn"},
		ui.HelpEntry{Name: "theme", Description: "select and apply a theme"},
		ui.HelpEntry{Name: "settings", Description: "change session settings"},
		ui.HelpEntry{Name: "quit", Description: "end the session"},
		ui.HelpEntry{Name: "exit", Description: "end the session"},
	)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries
}

func (b *tuiBridge) showHelp() {
	if err := b.runner.ShowHelp(b.ctx, b.helpEntries()); err != nil {
		b.warn(err)
	}
}

func (b *tuiBridge) modelChoices() []ui.ModelChoice {
	transport := b.transportName()
	if !transportAllowsModelSelection(transport) {
		return []ui.ModelChoice{{ID: b.rd.model, Provider: currentModelProviderLabel}}
	}
	choices := make([]ui.ModelChoice, 0, 16)
	if registry := b.rd.modelRegistry; registry != nil {
		for _, key := range registry.Keys() {
			info, ok := registry.GetByKey(key)
			if !ok || info.ID == "" || !transportSupportsModel(transport, info.Provider) {
				continue
			}
			if _, ok := resolveWireModel(transport, info.ID); !ok {
				continue
			}
			choices = append(choices, ui.ModelChoice{ID: info.ID, Provider: info.Provider, ContextWindow: info.ContextWindow})
		}
	}
	current := strings.TrimSpace(b.rd.model)
	if current != "" && !hasModelChoice(choices, current) {
		choices = append(choices, ui.ModelChoice{ID: b.rd.model, Provider: currentModelProviderLabel})
	}
	return choices
}

func hasModelChoice(choices []ui.ModelChoice, id string) bool {
	for _, choice := range choices {
		if choice.ID == id {
			return true
		}
	}
	return false
}

func (b *tuiBridge) modelSelectable(model string) bool {
	if model == b.rd.model {
		return true
	}
	for _, choice := range b.modelChoices() {
		if choice.ID == model {
			return true
		}
	}
	return false
}

func (b *tuiBridge) transportName() string {
	if strings.TrimSpace(b.rd.provider) == "" {
		return multiModelTransport
	}
	return b.rd.provider
}

func (b *tuiBridge) selectModel() {
	if !transportAllowsModelSelection(b.transportName()) {
		b.warn(fmt.Errorf("model selection is not available for transport %q: it uses a fixed deployment", b.transportName()))
		return
	}
	model, ok, err := b.runner.SelectModel(b.ctx, b.rd.model, b.modelChoices())
	if err != nil {
		b.warn(err)
		return
	}
	if !ok {
		return
	}
	b.applyModel(model)
}

func (b *tuiBridge) applyModel(model string) {
	selected := strings.TrimSpace(model)
	if selected == "" {
		b.warn(errors.New("model selector returned an empty model"))
		return
	}
	transport := b.transportName()
	if !transportAllowsModelSelection(transport) {
		b.warn(fmt.Errorf("model %q cannot be selected for transport %q: it uses a fixed deployment", selected, transport))
		return
	}
	if !b.modelSelectable(selected) {
		b.warn(fmt.Errorf("model %q is not available for the active transport %q", selected, transport))
		return
	}
	if selected == b.rd.model {
		b.runner.Surface().SetModel(selected)
		b.inform("model: " + selected)
		return
	}
	wire, ok := resolveWireModel(transport, selected)
	if !ok {
		b.warn(fmt.Errorf("model %q has no verified native wire model for transport %q", selected, transport))
		return
	}

	previousModel := b.rd.model
	previousWire := b.rd.wireModel
	previousPreparer := b.rd.preparer

	var preparer *contextPreparerAdapter
	if b.rd.reprepare != nil {
		built, err := b.rd.reprepare(selected, wire)
		if err != nil {
			b.restoreModel(previousModel, previousWire, previousPreparer)
			b.warn(err)
			return
		}
		preparer = built
	}
	if b.rd.persistModel != nil {
		if err := b.rd.persistModel(selected); err != nil {
			b.restoreModel(previousModel, previousWire, previousPreparer)
			b.warn(err)
			return
		}
	}
	b.rd.model = selected
	b.rd.wireModel = wire
	if b.rd.reprepare != nil {
		b.rd.preparer = preparer
	}
	b.runner.Surface().SetModel(selected)
	b.inform("model: " + selected)
}

func (b *tuiBridge) restoreModel(model, wire string, preparer *contextPreparerAdapter) {
	b.rd.model = model
	b.rd.wireModel = wire
	b.rd.preparer = preparer
}

func (b *tuiBridge) selectTheme() {
	name, ok, err := b.runner.SelectTheme(b.ctx)
	if err != nil {
		b.warn(err)
		return
	}
	if !ok {
		return
	}
	b.inform("theme: " + name)
}

func (b *tuiBridge) showSettings() {
	values, ok, err := b.runner.ShowSettings(b.ctx, b.settingItems())
	if err != nil {
		b.warn(err)
		return
	}
	if !ok {
		return
	}
	b.applySettings(values)
}

func (b *tuiBridge) settingItems() []tui.SettingItem {
	retry := "off"
	if b.rd.retryPolicy.Enabled {
		retry = "on"
	}
	tools := "collapsed"
	if b.runner.Surface().ToolsExpanded() {
		tools = "expanded"
	}
	thinking := "hidden"
	if b.runner.Surface().ThinkingExpanded() {
		thinking = "visible"
	}
	return []tui.SettingItem{
		{
			ID:           "retry",
			Label:        "Auto retry",
			Description:  "Retry a failed request with backoff.",
			CurrentValue: retry,
			Values:       []string{"on", "off"},
		},
		{
			ID:           "tools",
			Label:        "Tool output",
			Description:  "Expand or collapse tool output blocks.",
			CurrentValue: tools,
			Values:       []string{"expanded", "collapsed"},
		},
		{
			ID:           "thinking",
			Label:        "Thinking blocks",
			Description:  "Show or hide reasoning blocks in the transcript.",
			CurrentValue: thinking,
			Values:       []string{"visible", "hidden"},
		},
	}
}

func (b *tuiBridge) applySettings(values map[string]string) {
	if value, ok := values["retry"]; ok {
		b.rd.retryPolicy.Enabled = value == "on"
	}
	if value, ok := values["tools"]; ok {
		b.runner.Surface().SetToolsExpanded(value == "expanded")
	}
	if value, ok := values["thinking"]; ok {
		b.runner.Surface().SetThinkingExpanded(value == "visible")
	}
	b.inform(fmt.Sprintf("settings applied: %d change(s)", len(values)))
}
