package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestParseSettingsAllFields(t *testing.T) {
	s, err := ParseSettings([]byte(`{
		"defaultProvider": "anthropic",
		"defaultModel": "acme/model",
		"sessionDir": "/data/sessions",
		"retry": {"enabled": true, "maxRetries": 7, "baseDelayMs": 900},
		"compaction": {"enabled": false},
		"modelsCatalogUrl": "https://catalog.test/api/models"
	}`))
	if err != nil {
		t.Fatalf("ParseSettings: %v", err)
	}
	if s.DefaultProvider != "anthropic" {
		t.Errorf("DefaultProvider = %q, want anthropic", s.DefaultProvider)
	}
	if s.DefaultModel != "acme/model" {
		t.Errorf("DefaultModel = %q, want acme/model", s.DefaultModel)
	}
	if s.SessionDir != "/data/sessions" {
		t.Errorf("SessionDir = %q, want /data/sessions", s.SessionDir)
	}
	if s.Retry.Enabled == nil || !*s.Retry.Enabled {
		t.Error("Retry.Enabled = nil/false, want true")
	}
	if s.Retry.MaxRetries == nil || *s.Retry.MaxRetries != 7 {
		t.Errorf("Retry.MaxRetries = %v, want 7", s.Retry.MaxRetries)
	}
	if s.Retry.BaseDelayMs == nil || *s.Retry.BaseDelayMs != 900 {
		t.Errorf("Retry.BaseDelayMs = %v, want 900", s.Retry.BaseDelayMs)
	}
	if s.CompactionEnabled == nil || *s.CompactionEnabled {
		t.Error("CompactionEnabled = nil/true, want false")
	}
	if s.ModelsCatalogURL != "https://catalog.test/api/models" {
		t.Errorf("ModelsCatalogURL = %q", s.ModelsCatalogURL)
	}
}

func TestParseSettingsEmptyObject(t *testing.T) {
	s, err := ParseSettings([]byte("{}"))
	if err != nil {
		t.Fatalf("ParseSettings: %v", err)
	}
	if s.DefaultProvider != "" || s.DefaultModel != "" || s.SessionDir != "" || s.ModelsCatalogURL != "" {
		t.Errorf("settings = %+v, want all unset", s)
	}
	if s.Retry.Enabled != nil || s.Retry.MaxRetries != nil || s.Retry.BaseDelayMs != nil || s.CompactionEnabled != nil {
		t.Errorf("settings = %+v, want all pointers nil", s)
	}
}

func TestParseSettingsIgnoresUnsupportedFields(t *testing.T) {
	s, err := ParseSettings([]byte(`{
		"unknownTop": 42,
		"theme": {"dark": true},
		"retry": {"enabled": true, "bogus": "x"},
		"compaction": {"enabled": false, "model": "m"}
	}`))
	if err != nil {
		t.Fatalf("ParseSettings: %v", err)
	}
	if s.Retry.Enabled == nil || !*s.Retry.Enabled {
		t.Error("Retry.Enabled must survive unknown siblings")
	}
	if s.CompactionEnabled == nil || *s.CompactionEnabled {
		t.Error("CompactionEnabled must survive unknown siblings")
	}
}

func TestParseSettingsNullValuesIgnored(t *testing.T) {
	s, err := ParseSettings([]byte(`{
		"defaultProvider": null,
		"retry": null,
		"compaction": {"enabled": null}
	}`))
	if err != nil {
		t.Fatalf("ParseSettings: %v", err)
	}
	if s.DefaultProvider != "" || s.Retry.Enabled != nil || s.CompactionEnabled != nil {
		t.Errorf("explicit nulls must be treated as absent, got %+v", s)
	}
}

func TestParseSettingsFieldErrors(t *testing.T) {
	cases := []struct {
		name    string
		content string
		wantErr string
	}{
		{"defaultProvider number", `{"defaultProvider": 12}`, `field "defaultProvider": want a string`},
		{"defaultModel array", `{"defaultModel": []}`, `field "defaultModel": want a string`},
		{"sessionDir bool", `{"sessionDir": true}`, `field "sessionDir": want a string`},
		{"modelsCatalogUrl number", `{"modelsCatalogUrl": 1.5}`, `field "modelsCatalogUrl": want a string`},
		{"retry array", `{"retry": []}`, `field "retry": want an object`},
		{"retry string", `{"retry": "on"}`, `field "retry": want an object`},
		{"retry.enabled string", `{"retry": {"enabled": "yes"}}`, `field "retry.enabled": want a boolean`},
		{"retry.maxRetries negative", `{"retry": {"maxRetries": -1}}`, `field "retry.maxRetries": want a nonnegative integer`},
		{"retry.maxRetries fractional", `{"retry": {"maxRetries": 1.5}}`, `field "retry.maxRetries": want a nonnegative integer`},
		{"retry.maxRetries string", `{"retry": {"maxRetries": "3"}}`, `field "retry.maxRetries": want a nonnegative integer`},
		{"retry.maxRetries bool", `{"retry": {"maxRetries": true}}`, `field "retry.maxRetries": want a nonnegative integer`},
		{"retry.baseDelayMs negative", `{"retry": {"baseDelayMs": -5}}`, `field "retry.baseDelayMs": want a nonnegative integer`},
		{"retry.baseDelayMs fractional", `{"retry": {"baseDelayMs": 2.25}}`, `field "retry.baseDelayMs": want a nonnegative integer`},
		{"retry.baseDelayMs object", `{"retry": {"baseDelayMs": {}}}`, `field "retry.baseDelayMs": want a nonnegative integer`},
		{"compaction array", `{"compaction": []}`, `field "compaction": want an object`},
		{"compaction.enabled string", `{"compaction": {"enabled": "no"}}`, `field "compaction.enabled": want a boolean`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseSettings([]byte(tc.content))
			if err == nil {
				t.Fatalf("ParseSettings(%s): expected an error", tc.content)
			}
			if err.Error() != "settings: "+tc.wantErr && !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("ParseSettings(%s) error = %v, want it to mention %q", tc.content, err, tc.wantErr)
			}
		})
	}
}

func TestParseSettingsSyntaxError(t *testing.T) {
	if _, err := ParseSettings([]byte("{bad json")); err == nil {
		t.Fatal("ParseSettings: expected a decode error")
	} else if !strings.Contains(err.Error(), "decode") {
		t.Errorf("error = %v, want a decode error", err)
	}
}

func TestParseSettingsRootNotAnObject(t *testing.T) {
	if _, err := ParseSettings([]byte("[1,2]")); err == nil {
		t.Fatal("ParseSettings: expected an error for a non-object root")
	}
}

func TestReadUserSettings(t *testing.T) {
	home := t.TempDir()
	s, err := ReadUserSettings(home)
	if err != nil {
		t.Fatalf("ReadUserSettings without a file: %v", err)
	}
	if s != nil {
		t.Errorf("ReadUserSettings = %+v, want nil without a file", s)
	}
	dir := filepath.Join(home, ".smidja")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"defaultModel":"m"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err = ReadUserSettings(home)
	if err != nil {
		t.Fatalf("ReadUserSettings: %v", err)
	}
	if s == nil || s.DefaultModel != "m" {
		t.Errorf("ReadUserSettings = %+v, want defaultModel m", s)
	}

	if s, err := ReadUserSettings(""); err != nil || s != nil {
		t.Errorf("ReadUserSettings(\"\") = %+v, %v; want nil, nil", s, err)
	}

	broken := t.TempDir()
	if err := os.MkdirAll(filepath.Join(broken, ".smidja"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, ".smidja", "settings.json"), []byte(`{"defaultModel": 3}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = ReadUserSettings(broken)
	if err == nil {
		t.Fatal("ReadUserSettings: expected an error for an invalid file")
	}
	if !strings.Contains(err.Error(), filepath.Join(".smidja", "settings.json")) {
		t.Errorf("error = %v, want the settings path in the message", err)
	}
}

func TestReadBundleSettings(t *testing.T) {
	s, err := ReadBundleSettings(nil)
	if err != nil || s != nil {
		t.Fatalf("ReadBundleSettings(nil) = %+v, %v; want nil, nil", s, err)
	}
	s, err = ReadBundleSettings(fstest.MapFS{"skills/a.md": {Data: []byte("x")}})
	if err != nil || s != nil {
		t.Fatalf("ReadBundleSettings without settings = %+v, %v; want nil, nil", s, err)
	}
	rooted := fstest.MapFS{
		"settings.json":         {Data: []byte(`{"defaultModel":"rooted"}`)},
		"content/settings.json": {Data: []byte(`{"defaultModel":"legacy"}`)},
	}
	s, err = ReadBundleSettings(rooted)
	if err != nil {
		t.Fatalf("ReadBundleSettings: %v", err)
	}
	if s.DefaultModel != "rooted" {
		t.Errorf("DefaultModel = %q, want the rooted settings.json to win", s.DefaultModel)
	}
	legacy := fstest.MapFS{"content/settings.json": {Data: []byte(`{"defaultModel":"legacy"}`)}}
	s, err = ReadBundleSettings(legacy)
	if err != nil {
		t.Fatalf("ReadBundleSettings: %v", err)
	}
	if s == nil || s.DefaultModel != "legacy" {
		t.Errorf("ReadBundleSettings = %+v, want the legacy content/settings.json", s)
	}
	broken := fstest.MapFS{"settings.json": {Data: []byte(`{"sessionDir": 5}`)}}
	_, err = ReadBundleSettings(broken)
	if err == nil {
		t.Fatal("ReadBundleSettings: expected an error for invalid settings")
	}
	if !strings.Contains(err.Error(), "bundle settings.json") || !strings.Contains(err.Error(), "sessionDir") {
		t.Errorf("error = %v, want the bundle location and field in the message", err)
	}
}

func TestSettingsEnvMap(t *testing.T) {
	var nilSettings *Settings
	if got := nilSettings.envMap(); got != nil {
		t.Errorf("nil envMap = %v, want nil", got)
	}
	enabled := true
	disabled := false
	maxRetries := 3
	baseDelay := int64(120)
	s := &Settings{
		DefaultProvider:   "deepseek",
		DefaultModel:      "m",
		SessionDir:        "/s",
		ModelsCatalogURL:  "https://c.test",
		Retry:             RetrySettings{Enabled: &enabled, MaxRetries: &maxRetries, BaseDelayMs: &baseDelay},
		CompactionEnabled: &disabled,
	}
	got := s.envMap()
	want := map[string]string{
		"SMIDJA_PROVIDER":            "deepseek",
		"SMIDJA_MODEL":               "m",
		"SMIDJA_SESSION_DIR":         "/s",
		"SMIDJA_MODELS_CATALOG_URL":  "https://c.test",
		"SMIDJA_RETRY":               "true",
		"SMIDJA_RETRY_MAX_RETRIES":   "3",
		"SMIDJA_RETRY_BASE_DELAY_MS": "120",
		"SMIDJA_CONTEXT":             "false",
	}
	if len(got) != len(want) {
		t.Fatalf("envMap = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("envMap[%q] = %q, want %q", k, got[k], v)
		}
	}
}
