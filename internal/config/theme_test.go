package config

import (
	"strings"
	"testing"
)

func TestParseThemeSetting(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		want    ThemeSetting
		wantErr string
	}{
		{"empty", "", ThemeSetting{}, ""},
		{"single", "dark", ThemeSetting{Single: "dark"}, ""},
		{"single spaced", "  light  ", ThemeSetting{Single: "light"}, ""},
		{"pair", "light/dark", ThemeSetting{Light: "light", Dark: "dark"}, ""},
		{"pair spaced", " light / dark ", ThemeSetting{Light: "light", Dark: "dark"}, ""},
		{"dot", ".", ThemeSetting{}, "dot segments"},
		{"dotdot", "..", ThemeSetting{}, "dot segments"},
		{"traversal single", "../dark", ThemeSetting{}, "dot segments"},
		{"traversal pair", "light/..", ThemeSetting{}, "dot segments"},
		{"absolute", "/etc/passwd", ThemeSetting{}, "lightTheme/darkTheme"},
		{"backslash", `..\\dark`, ThemeSetting{}, "path separators"},
		{"windows traversal", `..\\..\\dark`, ThemeSetting{}, "path separators"},
		{"two separators", "a/b/c", ThemeSetting{}, "lightTheme/darkTheme"},
		{"empty light", "/dark", ThemeSetting{}, "empty theme name"},
		{"empty dark", "light/", ThemeSetting{}, "empty theme name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseThemeSetting(tc.value)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("ParseThemeSetting(%q): want error mentioning %q", tc.value, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("ParseThemeSetting(%q) error = %v, want %q", tc.value, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseThemeSetting(%q): %v", tc.value, err)
			}
			if got != tc.want {
				t.Fatalf("ParseThemeSetting(%q) = %+v, want %+v", tc.value, got, tc.want)
			}
		})
	}
}

func TestThemeSettingResolve(t *testing.T) {
	empty := ThemeSetting{}
	if !empty.Empty() || empty.Auto() {
		t.Fatalf("empty setting = %+v, want empty and not auto", empty)
	}
	if empty.Resolve(true) != "light" || empty.Resolve(false) != "dark" {
		t.Fatal("an empty setting must resolve to the built-in appearance pair")
	}
	single := ThemeSetting{Single: "custom"}
	if single.Empty() || single.Auto() {
		t.Fatalf("single setting = %+v, want not empty and not auto", single)
	}
	if single.Resolve(true) != "custom" || single.Resolve(false) != "custom" {
		t.Fatal("a single setting must ignore the background appearance")
	}
	pair := ThemeSetting{Light: "day", Dark: "night"}
	if pair.Empty() || !pair.Auto() {
		t.Fatalf("pair setting = %+v, want auto", pair)
	}
	if pair.Resolve(true) != "day" || pair.Resolve(false) != "night" {
		t.Fatal("a pair setting must follow the background appearance")
	}
}

func TestParseSettingsThemeAndTUIMode(t *testing.T) {
	s, err := ParseSettings([]byte(`{"theme": " light / dark ", "tuiMode": "FULLSCREEN"}`))
	if err != nil {
		t.Fatalf("ParseSettings: %v", err)
	}
	if s.Theme == nil || *s.Theme != "light / dark" {
		t.Fatalf("Theme = %v, want the trimmed pair", s.Theme)
	}
	if s.TUIMode == nil || *s.TUIMode != "fullscreen" {
		t.Fatalf("TUIMode = %v, want the normalized mode", s.TUIMode)
	}
}

func TestParseSettingsThemeAndTUIModeErrors(t *testing.T) {
	cases := []struct {
		name    string
		content string
		wantErr string
	}{
		{"theme traversal", `{"theme": "../dark"}`, "dot segments"},
		{"theme dotdot", `{"theme": ".."}`, "dot segments"},
		{"theme bad type", `{"theme": 7}`, `field "theme": want a string`},
		{"tuimode bad type", `{"tuiMode": true}`, `field "tuiMode": want a string`},
		{"tuimode unknown", `{"tuiMode": "bogus"}`, "want regular or fullscreen"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseSettings([]byte(tc.content))
			if err == nil {
				t.Fatalf("ParseSettings(%s): want error mentioning %q", tc.content, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ParseSettings(%s) error = %v, want %q", tc.content, err, tc.wantErr)
			}
		})
	}
}

func TestParseSettingsEmptyThemeAndTUIModeAreUnset(t *testing.T) {
	s, err := ParseSettings([]byte(`{"theme": "", "tuiMode": " "}`))
	if err != nil {
		t.Fatalf("ParseSettings: %v", err)
	}
	if s.Theme == nil || *s.Theme != "" {
		t.Fatalf("Theme = %v, want an empty value", s.Theme)
	}
	if s.TUIMode != nil {
		t.Fatalf("TUIMode = %v, want nil for an empty value", s.TUIMode)
	}
	if _, ok := s.envMap()[envTUIMode]; ok {
		t.Fatalf("envMap tui mode = %q, want an unset entry", s.envMap()[envTUIMode])
	}
}

func TestLoadThemeAndTUIModePrecedence(t *testing.T) {
	home := t.TempDir()
	withUserSettings(t, home, `{"theme": "settings/theme", "tuiMode": "regular"}`)
	packageDefaults := map[string]string{"SMIDJA_THEME": "package", "SMIDJA_TUI_MODE": "regular"}
	bundleDefaults := map[string]string{"SMIDJA_THEME": "bundle", "SMIDJA_TUI_MODE": "fullscreen"}

	c, err := LoadWithSources(
		envFrom(nil),
		func() (string, error) { return "/work", nil },
		func() string { return home },
		bundleDefaults,
		nil,
		packageDefaults,
	)
	if err != nil {
		t.Fatalf("LoadWithSources: %v", err)
	}
	if c.Theme != "bundle" {
		t.Errorf("Theme = %q, want the bundle tier above user settings", c.Theme)
	}
	if c.TUIMode != "fullscreen" {
		t.Errorf("TUIMode = %q, want the bundle tier above user settings", c.TUIMode)
	}

	bundleSettings, err := ParseSettings([]byte(`{"theme": "bundle-settings", "tuiMode": "regular"}`))
	if err != nil {
		t.Fatal(err)
	}
	c, err = LoadWithSources(
		envFrom(nil),
		func() (string, error) { return "/work", nil },
		func() string { return home },
		bundleDefaults,
		bundleSettings,
		packageDefaults,
	)
	if err != nil {
		t.Fatalf("LoadWithSources with bundle settings: %v", err)
	}
	if c.Theme != "bundle" {
		t.Errorf("Theme = %q, want the bundle defaults above bundle settings", c.Theme)
	}

	env := map[string]string{"SMIDJA_THEME": "env/theme", "SMIDJA_TUI_MODE": "fullscreen"}
	c, err = LoadWithSources(
		envFrom(env),
		func() (string, error) { return "/work", nil },
		func() string { return home },
		bundleDefaults,
		nil,
		packageDefaults,
	)
	if err != nil {
		t.Fatalf("LoadWithSources with env: %v", err)
	}
	if c.Theme != "env/theme" {
		t.Errorf("Theme = %q, want the env pair above the bundle tier", c.Theme)
	}
	if c.TUIMode != "fullscreen" {
		t.Errorf("TUIMode = %q, want the env value above the bundle tier", c.TUIMode)
	}

	withDotEnv(t, "SMIDJA_THEME=dotenv/theme\nSMIDJA_TUI_MODE=regular\n")
	c, err = LoadWithSources(
		envFrom(nil),
		func() (string, error) { return "/work", nil },
		func() string { return home },
		bundleDefaults,
		nil,
		packageDefaults,
	)
	if err != nil {
		t.Fatalf("LoadWithSources with dotenv: %v", err)
	}
	if c.Theme != "dotenv/theme" {
		t.Errorf("Theme = %q, want the workspace .env value above the bundle tier", c.Theme)
	}
	if c.TUIMode != "regular" {
		t.Errorf("TUIMode = %q, want the workspace .env value", c.TUIMode)
	}
}

func TestLoadThemeAndTUIModeDefaultsAndErrors(t *testing.T) {
	c, err := Load(
		envFrom(nil),
		func() (string, error) { return "/work", nil },
		func() string { return "/home/tester" },
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Theme != "" {
		t.Errorf("Theme = %q, want the empty core default", c.Theme)
	}
	if c.TUIMode != "regular" {
		t.Errorf("TUIMode = %q, want the regular core default", c.TUIMode)
	}

	_, err = Load(
		envFrom(map[string]string{"SMIDJA_THEME": "../escape"}),
		func() (string, error) { return "/work", nil },
		func() string { return "/home/tester" },
	)
	if err == nil || !strings.Contains(err.Error(), "SMIDJA_THEME") {
		t.Errorf("theme env error = %v, want the SMIDJA_THEME name", err)
	}

	_, err = Load(
		envFrom(map[string]string{"SMIDJA_TUI_MODE": "bogus"}),
		func() (string, error) { return "/work", nil },
		func() string { return "/home/tester" },
	)
	if err == nil || !strings.Contains(err.Error(), "SMIDJA_TUI_MODE") {
		t.Errorf("tui mode env error = %v, want the SMIDJA_TUI_MODE name", err)
	}
}
