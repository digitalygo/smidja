package ui

import (
	"time"

	"github.com/digitalygo/smidja/internal/tui"
	"github.com/digitalygo/smidja/internal/tui/interactive"
)

const (
	defaultThemeWatchInterval = 2 * time.Second
	themeProbeTimeout         = 100 * time.Millisecond
)

type backgroundQuerier interface {
	QueryBackgroundColor(timeout time.Duration) (*tui.RGBColor, bool)
}

func newRunnerThemeRegistry(home string) *tui.ThemeRegistry {
	userDir := ""
	if home != "" {
		userDir = tui.UserThemesDir(home)
	}
	return tui.NewThemeRegistry(userDir, "", tui.ColorModeUnset)
}

func loadRunnerKeybindings(home string) (*tui.KeybindingsManager, error) {
	if home == "" {
		return tui.NewDefaultKeybindingsManager(nil), nil
	}
	user, err := tui.LoadKeybindingsConfig(tui.KeybindingsFilePath(home))
	if err != nil {
		return nil, err
	}
	return tui.NewDefaultKeybindingsManager(user), nil
}

func (r *Runner) applyInitialTheme() {
	setting := r.themeSetting
	light := false
	if setting.Empty() || setting.Auto() {
		light = r.detectLightBackground()
	}
	name := setting.Resolve(light)
	if name == "" || name == r.themes.ActiveName() {
		return
	}
	if err := r.ApplyTheme(name); err != nil {
		r.surface.AddNotice(interactive.NoticeWarning, "theme: "+err.Error())
	}
}

func (r *Runner) detectLightBackground() bool {
	querier, ok := r.Terminal().(backgroundQuerier)
	if !ok {
		return false
	}
	color, ok := querier.QueryBackgroundColor(themeProbeTimeout)
	if !ok || color == nil {
		return false
	}
	return color.IsLight()
}

func (r *Runner) applyReloadedTheme() {
	r.installTheme(r.themes.Active())
}

func (r *Runner) installTheme(theme *tui.Theme) {
	r.surface.SetTheme(theme)
	if r.dialogs != nil {
		r.dialogs.SetTheme(theme)
	}
	if alt, ok := r.view.(*tui.AltScreen); ok && alt != nil {
		alt.SetTheme(theme)
		alt.Search().Rebuild()
	}
	r.view.Invalidate()
	r.view.RequestRender(true)
}

func resolvedThemeWatchInterval(interval time.Duration) time.Duration {
	if interval <= 0 {
		return defaultThemeWatchInterval
	}
	return interval
}
