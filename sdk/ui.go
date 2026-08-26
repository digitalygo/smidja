package sdk

import "errors"

type Mode string

const (
	ModeInteractive Mode = "interactive"
	ModePrint       Mode = "print"
)

var ErrModeUnsupported = errors.New("sdk: UI operation not supported in the current mode")

type NotifyKind string

const (
	NotifyInfo    NotifyKind = "info"
	NotifyWarning NotifyKind = "warning"
	NotifyError   NotifyKind = "error"
)

type UI interface {
	Notify(message string, kind NotifyKind)

	Confirm(title, message string) (bool, error)

	Select(title string, options []string) (string, error)

	Input(title, placeholder string) (string, error)

	Editor(title, prefill string) (string, error)

	SetStatus(key, text string)

	SetWidget(key string, content []string)

	SetWorkingMessage(message string)

	SetTitle(title string)
}
