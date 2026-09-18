package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/digitalygo/smidja/internal/tui"
	"github.com/digitalygo/smidja/internal/tui/interactive"
	"github.com/digitalygo/smidja/sdk"
)

type ModelChoice struct {
	ID            string
	Provider      string
	ContextWindow int64
}

type SessionChoice struct {
	Path        string
	Label       string
	Description string
}

type HelpEntry struct {
	Name        string
	Description string
}

type OAuthPrompt struct {
	Title           string
	Provider        string
	VerificationURL string
	UserCode        string
	State           string
}

func (r *Runner) SelectModel(ctx context.Context, current string, models []ModelChoice) (string, bool, error) {
	items := make([]tui.SelectItem, 0, len(models))
	for _, model := range models {
		description := model.Provider
		if model.ContextWindow > 0 {
			description = fmt.Sprintf("%s · %d context", model.Provider, model.ContextWindow)
		}
		items = append(items, tui.SelectItem{Value: model.ID, Label: model.ID, Description: description})
	}
	return r.selectValue(ctx, interactive.SelectDialogOptions{
		Title:        "Select model",
		Items:        items,
		Searchable:   true,
		Placeholder:  "filter models",
		InitialValue: current,
	})
}

func (r *Runner) SelectThinking(ctx context.Context) (string, bool, error) {
	items := []tui.SelectItem{
		{Value: "provider-default", Label: "provider default", Description: "reasoning effort cannot be set: the frozen request contract has no effort field"},
	}
	return r.selectValue(ctx, interactive.SelectDialogOptions{
		Title:        "Select thinking level",
		Items:        items,
		Searchable:   false,
		InitialValue: "provider-default",
	})
}

func (r *Runner) SelectTheme(ctx context.Context) (string, bool, error) {
	if r.themes == nil {
		return "", false, sdk.ErrModeUnsupported
	}
	current := r.themes.ActiveName()
	items := make([]tui.SelectItem, 0, 8)
	for _, source := range r.themes.AvailableThemes() {
		description := "built-in"
		if source.Path != "" {
			description = source.Path
		}
		items = append(items, tui.SelectItem{Value: source.Name, Label: source.Name, Description: description})
	}
	value, accepted, err := r.selectValue(ctx, interactive.SelectDialogOptions{
		Title:        "Select theme",
		Items:        items,
		Searchable:   true,
		Placeholder:  "filter themes",
		InitialValue: current,
	})
	if err != nil || !accepted {
		return value, accepted, err
	}
	if err := r.ApplyTheme(value); err != nil {
		return "", false, err
	}
	return value, true, nil
}

func (r *Runner) ApplyTheme(name string) error {
	if r.themes == nil {
		return sdk.ErrModeUnsupported
	}
	theme, err := r.themes.SetTheme(name)
	if err != nil {
		return fmt.Errorf("ui: load theme %q: %w", name, err)
	}
	r.surface.SetTheme(theme)
	if r.dialogs != nil {
		r.dialogs.SetTheme(theme)
	}
	return nil
}

func (r *Runner) ShowSettings(ctx context.Context, items []tui.SettingItem) (map[string]string, bool, error) {
	if !r.Active() {
		return nil, false, sdk.ErrModeUnsupported
	}
	var applied map[string]string
	var accepted bool
	_, err := r.dialogs.run(r.dialogContext(ctx), func(emit func(DialogResult)) tui.Component {
		return interactive.NewSettingsDialog("Settings", items, interactive.NewDialogTheme(r.surface.Theme()),
			func(values map[string]string) {
				applied = values
				accepted = true
				emit(DialogResult{OK: true})
			},
			func() { emit(DialogResult{}) })
	})
	if err != nil {
		return nil, false, err
	}
	return applied, accepted, nil
}

func (r *Runner) ShowHelp(ctx context.Context, entries []HelpEntry) error {
	items := make([]tui.SelectItem, 0, len(entries))
	for _, entry := range entries {
		items = append(items, tui.SelectItem{Value: entry.Name, Label: "/" + entry.Name, Description: entry.Description})
	}
	_, _, err := r.selectValue(ctx, interactive.SelectDialogOptions{
		Title:       "Command help",
		Items:       items,
		Searchable:  true,
		Placeholder: "filter commands",
		MaxVisible:  12,
	})
	return err
}

func (r *Runner) SelectSession(ctx context.Context, sessions []SessionChoice) (string, bool, error) {
	items := make([]tui.SelectItem, 0, len(sessions))
	for _, session := range sessions {
		items = append(items, tui.SelectItem{Value: session.Path, Label: session.Label, Description: session.Description})
	}
	value, accepted, err := r.selectValue(ctx, interactive.SelectDialogOptions{
		Title:       "Resume session",
		Items:       items,
		Searchable:  true,
		Placeholder: "filter sessions",
	})
	if err != nil || !accepted {
		return "", false, err
	}
	if !isPathLike(value) {
		return "", false, errors.New("ui: session selection did not return a path")
	}
	return value, true, nil
}

func isPathLike(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	return strings.HasSuffix(trimmed, ".jsonl") || strings.ContainsRune(trimmed, '/')
}

func (r *Runner) ConfirmTrust(ctx context.Context, workspace string) (bool, error) {
	message := "Trust this workspace?\n\n" + workspace + "\n\nTrusted workspaces may load workspace instructions and run extensions."
	return r.confirm(ctx, "Project trust", message)
}

func (r *Runner) ShowOAuthPrompt(ctx context.Context, prompt OAuthPrompt) (bool, error) {
	if !r.Active() {
		return false, sdk.ErrModeUnsupported
	}
	title := strings.TrimSpace(prompt.Title)
	if title == "" {
		title = "Sign in"
	}
	op, err := r.StartLogin(ctx, LoginRequest{
		Provider:        prompt.Provider,
		Title:           title,
		VerificationURL: prompt.VerificationURL,
		UserCode:        prompt.UserCode,
		Status:          prompt.State,
		ContinueOnEnter: true,
	})
	if err != nil {
		return false, err
	}
	result := op.Wait()
	if result.State == LoginSucceeded {
		return true, nil
	}
	if errors.Is(result.Err, errLoginCanceled) || errors.Is(result.Err, errManualCanceled) {
		return false, nil
	}
	return false, result.Err
}

func (r *Runner) PromptManualCode(ctx context.Context, provider string) (string, error) {
	if !r.Active() {
		return "", sdk.ErrModeUnsupported
	}
	title := "Sign in"
	if strings.TrimSpace(provider) != "" {
		title = "Sign in to " + strings.TrimSpace(provider)
	}
	op, err := r.StartLogin(ctx, LoginRequest{Provider: provider, Title: title})
	if err != nil {
		return "", err
	}
	code, err := op.RequestManualCode(ctx)
	if err != nil {
		op.Cancel()
		if errors.Is(err, errManualCanceled) || errors.Is(err, errLoginCanceled) || errors.Is(err, context.Canceled) {
			return "", nil
		}
		return "", err
	}
	op.Succeed()
	return code, nil
}
