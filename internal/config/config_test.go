package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// envFrom wraps a map as an env lookup function; nil means no variables set.
func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	c, err := Load(
		envFrom(nil),
		func() (string, error) { return "/work/dir", nil },
		func() string { return "/home/tester" },
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if c.Model != defaultModel {
		t.Errorf("Model = %q, want %q", c.Model, defaultModel)
	}
	if c.OpenRouterURL != defaultOpenRouterURL {
		t.Errorf("OpenRouterURL = %q, want %q", c.OpenRouterURL, defaultOpenRouterURL)
	}
	if c.APIKey != "" {
		t.Errorf("APIKey = %q, want empty", c.APIKey)
	}
	wantSessionDir := filepath.Join("/home/tester", ".smidja", "sessions")
	if c.SessionDir != wantSessionDir {
		t.Errorf("SessionDir = %q, want %q", c.SessionDir, wantSessionDir)
	}
	if c.WorkspaceRoot != "/work/dir" {
		t.Errorf("WorkspaceRoot = %q, want %q", c.WorkspaceRoot, "/work/dir")
	}
	if c.ExecTimeoutSecs != defaultExecTimeoutSecs {
		t.Errorf("ExecTimeoutSecs = %d, want %d", c.ExecTimeoutSecs, defaultExecTimeoutSecs)
	}
	if c.MaxReadLines != defaultMaxReadLines {
		t.Errorf("MaxReadLines = %d, want %d", c.MaxReadLines, defaultMaxReadLines)
	}
	if c.MaxOutputBytes != defaultMaxOutputBytes {
		t.Errorf("MaxOutputBytes = %d, want %d", c.MaxOutputBytes, defaultMaxOutputBytes)
	}
}

func TestLoadEnvOverrides(t *testing.T) {
	env := map[string]string{
		"SMIDJA_MODEL":             "acme/custom-model",
		"SMIDJA_OPENROUTER_URL":    "https://example.test/v1/chat/completions",
		"OPENROUTER_API_KEY":       "sk-test-123",
		"SMIDJA_SESSION_DIR":       "/var/smidja/sessions",
		"SMIDJA_EXEC_TIMEOUT_SECS": "12",
		"SMIDJA_MAX_READ_LINES":    "100",
		"SMIDJA_MAX_OUTPUT_BYTES":  "4096",
	}
	c, err := Load(
		envFrom(env),
		func() (string, error) { return "/work", nil },
		func() string { return "/home/tester" },
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if c.Model != "acme/custom-model" {
		t.Errorf("Model = %q, want override", c.Model)
	}
	if c.OpenRouterURL != "https://example.test/v1/chat/completions" {
		t.Errorf("OpenRouterURL = %q, want override", c.OpenRouterURL)
	}
	if c.APIKey != "sk-test-123" {
		t.Errorf("APIKey = %q, want override", c.APIKey)
	}
	if c.SessionDir != "/var/smidja/sessions" {
		t.Errorf("SessionDir = %q, want override", c.SessionDir)
	}
	if c.ExecTimeoutSecs != 12 {
		t.Errorf("ExecTimeoutSecs = %d, want 12", c.ExecTimeoutSecs)
	}
	if c.MaxReadLines != 100 {
		t.Errorf("MaxReadLines = %d, want 100", c.MaxReadLines)
	}
	if c.MaxOutputBytes != 4096 {
		t.Errorf("MaxOutputBytes = %d, want 4096", c.MaxOutputBytes)
	}
}

func TestLoadInvalidValuesFallBackToDefaults(t *testing.T) {
	// Each invalid value must fall back to the compiled-in default:
	// below-minimum integers, unparsable integers, and empty strings.
	env := map[string]string{
		"SMIDJA_MODEL":             "",
		"SMIDJA_OPENROUTER_URL":    "",
		"SMIDJA_EXEC_TIMEOUT_SECS": "not-a-number",
		"SMIDJA_MAX_READ_LINES":    "",
		"SMIDJA_MAX_OUTPUT_BYTES":  "-100",
	}
	c, err := Load(
		envFrom(env),
		func() (string, error) { return "/work", nil },
		func() string { return "/home/tester" },
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if c.Model != defaultModel {
		t.Errorf("Model = %q, want default %q", c.Model, defaultModel)
	}
	if c.OpenRouterURL != defaultOpenRouterURL {
		t.Errorf("OpenRouterURL = %q, want default %q", c.OpenRouterURL, defaultOpenRouterURL)
	}
	if c.ExecTimeoutSecs != defaultExecTimeoutSecs {
		t.Errorf("ExecTimeoutSecs = %d, want default %d", c.ExecTimeoutSecs, defaultExecTimeoutSecs)
	}
	if c.MaxReadLines != defaultMaxReadLines {
		t.Errorf("MaxReadLines = %d, want default %d", c.MaxReadLines, defaultMaxReadLines)
	}
	if c.MaxOutputBytes != defaultMaxOutputBytes {
		t.Errorf("MaxOutputBytes = %d, want default %d", c.MaxOutputBytes, defaultMaxOutputBytes)
	}
}

func TestLoadWorkspaceRootCanonicalized(t *testing.T) {
	c, err := Load(
		envFrom(nil),
		func() (string, error) { return "/tmp/a/../b/.", nil },
		func() string { return "/home/tester" },
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.WorkspaceRoot != "/tmp/b" {
		t.Errorf("WorkspaceRoot = %q, want canonicalized %q", c.WorkspaceRoot, "/tmp/b")
	}
}

func TestLoadGetwdError(t *testing.T) {
	wantErr := errors.New("cwd unavailable")
	_, err := Load(
		envFrom(nil),
		func() (string, error) { return "", wantErr },
		func() string { return "/home/tester" },
	)
	if err == nil {
		t.Fatal("Load: expected error, got nil")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("Load error = %v, want it to wrap %v", err, wantErr)
	}
}

func TestLoadNilFunctions(t *testing.T) {
	if _, err := Load(nil, func() (string, error) { return "/work", nil }, func() string { return "/home" }); err == nil {
		t.Error("Load with nil env: expected error, got nil")
	}
	if _, err := Load(envFrom(nil), nil, func() string { return "/home" }); err == nil {
		t.Error("Load with nil getwd: expected error, got nil")
	}
	if _, err := Load(envFrom(nil), func() (string, error) { return "/work", nil }, nil); err == nil {
		t.Error("Load with nil home: expected error, got nil")
	}
}

// withDotEnv stubs the .env reader for the duration of a test so tests can
// inject content without touching the filesystem.
func withDotEnv(t *testing.T, content string) {
	t.Helper()
	orig := readDotEnvFile
	readDotEnvFile = func(string) ([]byte, error) {
		return []byte(content), nil
	}
	t.Cleanup(func() { readDotEnvFile = orig })
}

func TestParseDotEnv(t *testing.T) {
	content := `
# Full-line comment
EMPTY=
KEY=value
SPACED = spaced value
DOUBLE="quoted value"
SINGLE='quoted value'
FIRST=has=equals
  INDENTED=ok
NOEQUALS ignored
=no-key
`
	got := parseDotEnv(content)

	want := map[string]string{
		"EMPTY":    "",
		"KEY":      "value",
		"SPACED":   "spaced value",
		"DOUBLE":   "quoted value",
		"SINGLE":   "quoted value",
		"FIRST":    "has=equals",
		"INDENTED": "ok",
	}
	if len(got) != len(want) {
		t.Fatalf("parseDotEnv returned %d entries, want %d: %v", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("parseDotEnv[%q] = %q, want %q", k, got[k], v)
		}
	}
}

func TestParseDotEnvEmpty(t *testing.T) {
	for _, content := range []string{"", "\n\n", "# only a comment\n   \n"} {
		if got := parseDotEnv(content); len(got) != 0 {
			t.Errorf("parseDotEnv(%q) = %v, want empty map", content, got)
		}
	}
}

func TestLoadDotEnvAPIKey(t *testing.T) {
	withDotEnv(t, "OPENROUTER_API_KEY=sk-dotenv-secret\n")
	c, err := Load(
		envFrom(nil),
		func() (string, error) { return "/work", nil },
		func() string { return "/home/tester" },
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.APIKey != "sk-dotenv-secret" {
		t.Errorf("APIKey = %q, want %q", c.APIKey, "sk-dotenv-secret")
	}
}

func TestLoadDotEnvProvidesOtherValues(t *testing.T) {
	withDotEnv(t, "SMIDJA_MODEL=dotenv/model\nSMIDJA_SESSION_DIR=/var/dotenv/sessions\n")
	c, err := Load(
		envFrom(nil),
		func() (string, error) { return "/work", nil },
		func() string { return "/home/tester" },
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Model != "dotenv/model" {
		t.Errorf("Model = %q, want %q", c.Model, "dotenv/model")
	}
	if c.SessionDir != "/var/dotenv/sessions" {
		t.Errorf("SessionDir = %q, want %q", c.SessionDir, "/var/dotenv/sessions")
	}
}

func TestLoadDotEnvRealEnvWins(t *testing.T) {
	withDotEnv(t, "OPENROUTER_API_KEY=sk-dotenv-secret\n")
	c, err := Load(
		envFrom(map[string]string{"OPENROUTER_API_KEY": "sk-real-env"}),
		func() (string, error) { return "/work", nil },
		func() string { return "/home/tester" },
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.APIKey != "sk-real-env" {
		t.Errorf("APIKey = %q, want real env value %q", c.APIKey, "sk-real-env")
	}
}

func TestLoadDotEnvMissingFileNoError(t *testing.T) {
	orig := readDotEnvFile
	readDotEnvFile = func(string) ([]byte, error) {
		return nil, os.ErrNotExist
	}
	t.Cleanup(func() { readDotEnvFile = orig })

	c, err := Load(
		envFrom(nil),
		func() (string, error) { return "/work", nil },
		func() string { return "/home/tester" },
	)
	if err != nil {
		t.Fatalf("Load with missing .env: %v", err)
	}
	if c.APIKey != "" {
		t.Errorf("APIKey = %q, want empty", c.APIKey)
	}
}
