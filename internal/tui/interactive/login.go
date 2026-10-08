package interactive

import (
	"sync"
	"time"

	"github.com/digitalygo/smidja/internal/tui"
)

type LoginDialogState struct {
	Provider        string
	State           string
	Status          string
	VerificationURL string
	UserCode        string
	Deadline        time.Time
}

type LoginDialog struct {
	mu               sync.Mutex
	theme            DialogTheme
	title            string
	state            LoginDialogState
	manual           *MaskedInput
	manualGeneration uint64
	onCancel         func()
	onContinue       func()
	settled          bool
}

func NewLoginDialog(title string, theme DialogTheme, onCancel func()) *LoginDialog {
	return &LoginDialog{theme: theme, title: SanitizeSingleLine(title), onCancel: onCancel}
}

func (d *LoginDialog) SetContinue(handler func()) {
	d.mu.Lock()
	d.onContinue = handler
	d.mu.Unlock()
}

func (d *LoginDialog) SetState(state LoginDialogState) {
	d.mu.Lock()
	d.state = state
	d.mu.Unlock()
}

func (d *LoginDialog) PromptManual(generation uint64, title string, onSubmit func(string), onCancel func()) bool {
	d.mu.Lock()
	if d.settled || generation <= d.manualGeneration {
		d.mu.Unlock()
		return false
	}
	d.manualGeneration = generation
	var manual *MaskedInput
	manual = NewMaskedInput(title, d.theme, func(code string) {
		if d.releaseManual(manual) && onSubmit != nil {
			onSubmit(code)
		}
	}, func() {
		if d.releaseManual(manual) && onCancel != nil {
			onCancel()
		}
	})
	d.manual = manual
	d.mu.Unlock()
	return true
}

func (d *LoginDialog) releaseManual(manual *MaskedInput) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.manual != manual {
		return false
	}
	d.manual = nil
	return true
}

func (d *LoginDialog) ClearManual(generation uint64) {
	d.mu.Lock()
	if d.manualGeneration == generation {
		d.manual = nil
	}
	d.mu.Unlock()
}

func (d *LoginDialog) Settle() {
	d.mu.Lock()
	d.settled = true
	d.manual = nil
	d.mu.Unlock()
}

func (d *LoginDialog) SetFocused(focused bool) {
	d.mu.Lock()
	manual := d.manual
	d.mu.Unlock()
	if manual != nil {
		manual.SetFocused(focused)
	}
}

func (d *LoginDialog) SetTheme(theme DialogTheme) {
	d.mu.Lock()
	d.theme = theme
	manual := d.manual
	d.mu.Unlock()
	if manual != nil {
		manual.SetTheme(theme)
	}
}

func (d *LoginDialog) Invalidate() {
	d.mu.Lock()
	manual := d.manual
	d.mu.Unlock()
	if manual != nil {
		manual.Invalidate()
	}
}

func (d *LoginDialog) Render(width int) []string {
	d.mu.Lock()
	if d.manual != nil {
		manual := d.manual
		d.mu.Unlock()
		return manual.Render(width)
	}
	theme := d.theme
	title := d.title
	state := d.state
	continueHandler := d.onContinue
	d.mu.Unlock()

	bodyWidth := dialogBodyWidth(width)
	body := make([]string, 0, 8)
	appendField := func(label, value string) {
		value = SanitizeSingleLine(value)
		if value == "" {
			return
		}
		body = append(body, tui.WrapTextWithANSI(label+value, bodyWidth)...)
	}
	appendField("Status: ", state.State)
	appendField("", state.Status)
	appendField("Provider: ", state.Provider)
	appendField("Open: ", state.VerificationURL)
	appendField("Code: ", state.UserCode)
	if !state.Deadline.IsZero() {
		appendField("Expires in: ", loginRemaining(state.Deadline))
	}

	hint := "Esc cancel"
	if continueHandler != nil {
		hint = "Enter continue · Esc cancel"
	}
	return renderDialogFrame(SanitizeSingleLine(title), hint, body, width, theme)
}

func (d *LoginDialog) HandleInput(data string) {
	d.mu.Lock()
	if d.settled {
		d.mu.Unlock()
		return
	}
	if d.manual != nil {
		manual := d.manual
		d.mu.Unlock()
		manual.HandleInput(data)
		return
	}
	keybindings := tui.GlobalKeybindings()
	confirmed := keybindings.Matches(data, "tui.select.confirm") || data == "y" || data == "Y"
	switch {
	case keybindings.Matches(data, "tui.select.cancel"):
		d.settled = true
		handler := d.onCancel
		d.mu.Unlock()
		if handler != nil {
			handler()
		}
	case confirmed && d.onContinue != nil:
		handler := d.onContinue
		d.settled = true
		d.mu.Unlock()
		handler()
	default:
		d.mu.Unlock()
	}
}

func loginRemaining(deadline time.Time) string {
	remaining := time.Until(deadline)
	if remaining < 0 {
		remaining = 0
	}
	return remaining.Round(time.Second).String()
}
