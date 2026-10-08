package ui

import (
	"fmt"
	"strings"
)

type TUIMode string

const (
	TUIModeRegular    TUIMode = "regular"
	TUIModeFullscreen TUIMode = "fullscreen"
)

func ParseTUIMode(value string) (TUIMode, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return TUIModeRegular, nil
	}
	switch TUIMode(strings.ToLower(trimmed)) {
	case TUIModeRegular:
		return TUIModeRegular, nil
	case TUIModeFullscreen:
		return TUIModeFullscreen, nil
	default:
		return "", fmt.Errorf("ui: invalid --tui-mode %q, want regular or fullscreen", value)
	}
}

func (m TUIMode) Fullscreen() bool { return m == TUIModeFullscreen }

func (m TUIMode) String() string {
	if m == "" {
		return string(TUIModeRegular)
	}
	return string(m)
}
