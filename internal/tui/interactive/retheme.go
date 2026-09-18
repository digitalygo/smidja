package interactive

import (
	"github.com/digitalygo/smidja/internal/tui"
)

type ThemeSetter interface {
	SetTheme(*tui.Theme)
}

func (m *Markdown) SetTheme(theme *tui.Theme) {
	m.mu.Lock()
	m.theme = theme
	m.version++
	m.cacheValid = false
	m.mu.Unlock()
}

func (u *UserMessage) SetTheme(theme *tui.Theme) {
	u.mu.Lock()
	text := u.markdown.Text()
	hyperlinks := u.markdown.hyperlinks
	u.theme = theme
	style := MarkdownStyle{TextColor: func(value string) string { return theme.Fg("userMessageText", value) }}
	u.markdown = NewMarkdown(text, 0, 0, theme, style, hyperlinks)
	u.mu.Unlock()
}

func (a *AssistantMessage) SetTheme(theme *tui.Theme) {
	a.mu.Lock()
	a.theme = theme
	a.version++
	a.cacheValid = false
	a.mu.Unlock()
}

func (n *Notice) SetTheme(theme *tui.Theme) {
	n.theme = theme
}

func (s *SkillBlock) SetTheme(theme *tui.Theme) {
	s.mu.Lock()
	s.theme = theme
	s.mu.Unlock()
}

func (c *CompactionBlock) SetTheme(theme *tui.Theme) {
	c.mu.Lock()
	c.theme = theme
	c.mu.Unlock()
}

func (t *ToolExecution) SetTheme(theme *tui.Theme) {
	t.mu.Lock()
	t.theme = theme
	t.cacheValid = false
	t.mu.Unlock()
}

func (s *SubagentBlock) SetTheme(theme *tui.Theme) {
	s.mu.Lock()
	s.theme = theme
	s.mu.Unlock()
}

func (b *BashExecution) SetTheme(theme *tui.Theme) {
	b.mu.Lock()
	b.theme = theme
	b.mu.Unlock()
}

func (f *Footer) SetTheme(theme *tui.Theme) {
	f.mu.Lock()
	f.theme = theme
	f.mu.Unlock()
}

func (s *StatusIndicator) SetTheme(theme *tui.Theme) {
	s.mu.Lock()
	s.theme = theme
	s.mu.Unlock()
}

func (w *WidgetPanel) SetTheme(theme *tui.Theme) {}

func applyComponentTheme(component tui.Component, theme *tui.Theme) {
	if setter, ok := component.(ThemeSetter); ok {
		setter.SetTheme(theme)
	}
}

type dialogThemeSetter interface {
	SetTheme(DialogTheme)
}

type placeholderStyleSetter interface {
	SetPlaceholderStyle(func(string) string)
}

func applyDialogTheme(component tui.Component, theme DialogTheme) {
	switch typed := component.(type) {
	case *tui.SelectList:
		typed.SetTheme(tui.DefaultSelectListTheme(theme.Accent, theme.Muted))
	case *tui.SettingsList:
		typed.SetTheme(tui.DefaultSettingsListTheme(theme.Accent, theme.Muted, theme.Dim))
		typed.SetSubmenuTheme(func(child tui.Component) { applyDialogTheme(child, theme) })
	case *tui.Editor:
		typed.SetTheme(theme.Base)
	case placeholderStyleSetter:
		typed.SetPlaceholderStyle(theme.Dim)
	case dialogThemeSetter:
		typed.SetTheme(theme)
	case interface{ Children() []tui.Component }:
		for _, child := range typed.Children() {
			applyDialogTheme(child, theme)
		}
	}
}
