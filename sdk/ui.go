package sdk

import "errors"

// Mode identifies the run mode of the harness, mirroring Pi's
// ExtensionMode for the two modes smidja v0 ships. RPC and JSON modes are
// deferred to the gateway phase.
type Mode string

// Run modes.
const (
	// ModeInteractive is the interactive session mode (the default, and
	// the REPL in v0).
	ModeInteractive Mode = "interactive"
	// ModePrint is the single-shot print mode (-p): extensions run but
	// cannot prompt the user.
	ModePrint Mode = "print"
)

// ErrModeUnsupported is returned by UI methods that cannot operate in the
// current mode. In print mode the dialog methods (Confirm, Select, Input,
// Editor) return it so extensions can distinguish "no UI available" from a
// user cancellation, a deliberate divergence from Pi's return-defaults
// behavior; check HasUI() before prompting. The fire-and-forget UI
// methods (Notify, SetStatus, SetWidget, SetWorkingMessage, SetTitle) are
// no-ops in print mode and never return it.
var ErrModeUnsupported = errors.New("sdk: UI operation not supported in the current mode")

// NotifyKind selects the visual kind of a notification, mirroring Pi's
// notify types.
type NotifyKind string

// Notification kinds.
const (
	// NotifyInfo is an informational notification.
	NotifyInfo NotifyKind = "info"
	// NotifyWarning is a warning notification.
	NotifyWarning NotifyKind = "warning"
	// NotifyError is an error notification.
	NotifyError NotifyKind = "error"
)

// UI is the user-interaction surface exposed to extensions, mirroring
// Pi's ExtensionUIContext for the methods v0 backs. Dialog methods
// (Confirm, Select, Input, Editor) block until the user answers and
// return ErrModeUnsupported in print mode; fire-and-forget methods
// (Notify, SetStatus, SetWidget, SetWorkingMessage, SetTitle) are no-ops
// in print mode. Full TUI control (custom components, editors, themes,
// autocomplete, terminal input) is deferred to the TUI phase.
type UI interface {
	// Notify shows a non-blocking notification.
	Notify(message string, kind NotifyKind)

	// Confirm shows a yes/no dialog and returns the user's answer.
	// Cancelling (or timing out) returns false and a nil error.
	Confirm(title, message string) (bool, error)

	// Select shows a single-choice selector and returns the chosen
	// option; cancelling returns "" and a nil error.
	Select(title string, options []string) (string, error)

	// Input shows a one-line text input and returns the entered text;
	// cancelling returns "" and a nil error.
	Input(title, placeholder string) (string, error)

	// Editor shows a multi-line editor prefilled with prefill and
	// returns the resulting text; cancelling returns "" and a nil
	// error.
	Editor(title, prefill string) (string, error)

	// SetStatus sets the extension's status line in the footer. An
	// empty text clears it.
	SetStatus(key, text string)

	// SetWidget sets a widget above the editor. A nil content clears
	// it.
	SetWidget(key string, content []string)

	// SetWorkingMessage sets the message shown while the model streams.
	// An empty message restores the default.
	SetWorkingMessage(message string)

	// SetTitle sets the terminal window title.
	SetTitle(title string)
}
