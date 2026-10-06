package config

import (
	"fmt"
	"strings"
)

const (
	tuiModeRegular    = "regular"
	tuiModeFullscreen = "fullscreen"
)

type ThemeSetting struct {
	Single string
	Light  string
	Dark   string
}

func ParseThemeSetting(value string) (ThemeSetting, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ThemeSetting{}, nil
	}
	switch strings.Count(trimmed, "/") {
	case 0:
		if err := validateThemeName(trimmed); err != nil {
			return ThemeSetting{}, err
		}
		return ThemeSetting{Single: trimmed}, nil
	case 1:
		parts := strings.SplitN(trimmed, "/", 2)
		light := strings.TrimSpace(parts[0])
		dark := strings.TrimSpace(parts[1])
		if err := validateThemeName(light); err != nil {
			return ThemeSetting{}, err
		}
		if err := validateThemeName(dark); err != nil {
			return ThemeSetting{}, err
		}
		return ThemeSetting{Light: light, Dark: dark}, nil
	default:
		return ThemeSetting{}, fmt.Errorf("%q: want a theme name or lightTheme/darkTheme", value)
	}
}

func validateThemeName(name string) error {
	if name == "" {
		return fmt.Errorf("empty theme name")
	}
	if name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("%q: theme names cannot contain path separators or dot segments", name)
	}
	return nil
}

func (t ThemeSetting) Empty() bool {
	return t.Single == "" && t.Light == "" && t.Dark == ""
}

func (t ThemeSetting) Auto() bool {
	return t.Single == "" && t.Light != "" && t.Dark != ""
}

func (t ThemeSetting) Resolve(light bool) string {
	if t.Single != "" {
		return t.Single
	}
	if t.Light != "" {
		if light {
			return t.Light
		}
		return t.Dark
	}
	if light {
		return "light"
	}
	return "dark"
}

func normalizeTUIMode(value string) (string, error) {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	switch trimmed {
	case "":
		return "", nil
	case tuiModeRegular, tuiModeFullscreen:
		return trimmed, nil
	default:
		return "", fmt.Errorf("%q: want regular or fullscreen", value)
	}
}
