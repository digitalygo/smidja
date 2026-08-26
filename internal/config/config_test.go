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

func TestLoadContextDefaults(t *testing.T) {
	c, err := Load(
		envFrom(nil),
		func() (string, error) { return "/work", nil },
		func() string { return "/home/tester" },
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !c.ContextEnabled {
		t.Error("ContextEnabled = false, want true by default")
	}
	if c.ContextWindowTokens != 0 {
		t.Errorf("ContextWindowTokens = %d, want 0 (registry lookup)", c.ContextWindowTokens)
	}
	if c.ContextCacheMissAfter != 0 {
		t.Errorf("ContextCacheMissAfter = %s, want 0 (contextmanager default)", c.ContextCacheMissAfter)
	}
	if c.ContextPruneThreshold != 0 || c.ContextCompactThreshold != 0 ||
		c.ContextSafetyThreshold != 0 || c.ContextCompactTarget != 0 {
		t.Errorf("thresholds = %v/%v/%v/%v, want all 0 (contextmanager defaults)",
			c.ContextPruneThreshold, c.ContextCompactThreshold, c.ContextSafetyThreshold, c.ContextCompactTarget)
	}
	if c.ContextKeepRecentMessages != 0 || c.ContextSelectorChunkTokens != 0 {
		t.Errorf("KeepRecentMessages/SelectorChunkTokens = %d/%d, want 0",
			c.ContextKeepRecentMessages, c.ContextSelectorChunkTokens)
	}
	if c.ContextSelectorModel != "" {
		t.Errorf("ContextSelectorModel = %q, want empty (main model)", c.ContextSelectorModel)
	}
}

func TestLoadContextEnvOverrides(t *testing.T) {
	env := map[string]string{
		"SMIDJA_CONTEXT":                       "false",
		"SMIDJA_CONTEXT_WINDOW_TOKENS":         "32000",
		"SMIDJA_CONTEXT_CACHE_MISS_AFTER_SECS": "300",
		"SMIDJA_CONTEXT_PRUNE_THRESHOLD":       "0.5",
		"SMIDJA_CONTEXT_COMPACT_THRESHOLD":     "0.8",
		"SMIDJA_CONTEXT_SAFETY_THRESHOLD":      "0.9",
		"SMIDJA_CONTEXT_COMPACT_TARGET":        "0.4",
		"SMIDJA_CONTEXT_KEEP_RECENT_MESSAGES":  "10",
		"SMIDJA_CONTEXT_SELECTOR_CHUNK_TOKENS": "8000",
		"SMIDJA_CONTEXT_SELECTOR_MODEL":        "acme/selector",
	}
	c, err := Load(
		envFrom(env),
		func() (string, error) { return "/work", nil },
		func() string { return "/home/tester" },
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.ContextEnabled {
		t.Error("ContextEnabled = true, want false override")
	}
	if c.ContextWindowTokens != 32000 {
		t.Errorf("ContextWindowTokens = %d, want 32000", c.ContextWindowTokens)
	}
	if c.ContextCacheMissAfter.Seconds() != 300 {
		t.Errorf("ContextCacheMissAfter = %s, want 300s", c.ContextCacheMissAfter)
	}
	if c.ContextPruneThreshold != 0.5 || c.ContextCompactThreshold != 0.8 ||
		c.ContextSafetyThreshold != 0.9 || c.ContextCompactTarget != 0.4 {
		t.Errorf("thresholds = %v/%v/%v/%v, want 0.5/0.8/0.9/0.4",
			c.ContextPruneThreshold, c.ContextCompactThreshold, c.ContextSafetyThreshold, c.ContextCompactTarget)
	}
	if c.ContextKeepRecentMessages != 10 {
		t.Errorf("ContextKeepRecentMessages = %d, want 10", c.ContextKeepRecentMessages)
	}
	if c.ContextSelectorChunkTokens != 8000 {
		t.Errorf("ContextSelectorChunkTokens = %d, want 8000", c.ContextSelectorChunkTokens)
	}
	if c.ContextSelectorModel != "acme/selector" {
		t.Errorf("ContextSelectorModel = %q, want override", c.ContextSelectorModel)
	}
}

func TestLoadContextInvalidValuesFallBack(t *testing.T) {
	// Invalid, negative, and out-of-range values fall back to 0 (or the
	// boolean default), letting the contextmanager defaults apply.
	env := map[string]string{
		"SMIDJA_CONTEXT":                       "banana",
		"SMIDJA_CONTEXT_WINDOW_TOKENS":         "-5",
		"SMIDJA_CONTEXT_CACHE_MISS_AFTER_SECS": "not-a-number",
		"SMIDJA_CONTEXT_PRUNE_THRESHOLD":       "1.5",
		"SMIDJA_CONTEXT_SAFETY_THRESHOLD":      "2",
		"SMIDJA_CONTEXT_COMPACT_TARGET":        "-0.1",
		"SMIDJA_CONTEXT_KEEP_RECENT_MESSAGES":  "-1",
		"SMIDJA_CONTEXT_SELECTOR_CHUNK_TOKENS": "abc",
	}
	c, err := Load(
		envFrom(env),
		func() (string, error) { return "/work", nil },
		func() string { return "/home/tester" },
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !c.ContextEnabled {
		t.Error("ContextEnabled = false, want the boolean default true for a non-false value")
	}
	if c.ContextWindowTokens != 0 {
		t.Errorf("ContextWindowTokens = %d, want 0", c.ContextWindowTokens)
	}
	if c.ContextCacheMissAfter != 0 {
		t.Errorf("ContextCacheMissAfter = %s, want 0", c.ContextCacheMissAfter)
	}
	if c.ContextPruneThreshold != 0 || c.ContextSafetyThreshold != 0 || c.ContextCompactTarget != 0 {
		t.Errorf("thresholds = %v/%v/%v, want all 0",
			c.ContextPruneThreshold, c.ContextSafetyThreshold, c.ContextCompactTarget)
	}
	if c.ContextKeepRecentMessages != 0 || c.ContextSelectorChunkTokens != 0 {
		t.Errorf("KeepRecentMessages/SelectorChunkTokens = %d/%d, want 0",
			c.ContextKeepRecentMessages, c.ContextSelectorChunkTokens)
	}
}

func TestLoadWithBundleDefaults(t *testing.T) {
	// The fallback chain is env > .env > bundle default > compiled
	// default. Each field exercises one layer.
	defaults := map[string]string{
		"SMIDJA_MODEL":             "bundle/model",
		"SMIDJA_OPENROUTER_URL":    "https://bundle.test/v1",
		"SMIDJA_EXEC_TIMEOUT_SECS": "7",
		"SMIDJA_MAX_OUTPUT_BYTES":  "1024",
		"SMIDJA_CONTEXT":           "false",
		"OPENROUTER_API_KEY":       "bundle-key",
	}
	env := map[string]string{
		"SMIDJA_MODEL":            "env/model",
		"SMIDJA_MAX_OUTPUT_BYTES": "2048",
	}
	withDotEnv(t, "SMIDJA_EXEC_TIMEOUT_SECS=9\nSMIDJA_MODEL=dotenv/model\n")

	c, err := LoadWithDefaults(
		envFrom(env),
		func() (string, error) { return "/work", nil },
		func() string { return "/home/tester" },
		defaults,
	)
	if err != nil {
		t.Fatalf("LoadWithDefaults: %v", err)
	}

	if c.Model != "env/model" {
		t.Errorf("Model = %q, want the env value", c.Model)
	}
	if c.APIKey != "bundle-key" {
		t.Errorf("APIKey = %q, want the bundle default", c.APIKey)
	}
	if c.OpenRouterURL != "https://bundle.test/v1" {
		t.Errorf("OpenRouterURL = %q, want the bundle default", c.OpenRouterURL)
	}
	if c.ExecTimeoutSecs != 9 {
		t.Errorf("ExecTimeoutSecs = %d, want the dotenv value 9", c.ExecTimeoutSecs)
	}
	if c.MaxOutputBytes != 2048 {
		t.Errorf("MaxOutputBytes = %d, want the env value 2048", c.MaxOutputBytes)
	}
	if c.ContextEnabled {
		t.Error("ContextEnabled = true, want false from the bundle default")
	}
}

func TestLoadWithBundleDefaultsBelowCompiledDefaults(t *testing.T) {
	// A bundle default for an unknown model still applies; keys the
	// bundle does not define keep the compiled-in default.
	c, err := LoadWithDefaults(
		envFrom(nil),
		func() (string, error) { return "/work", nil },
		func() string { return "/home/tester" },
		map[string]string{"SMIDJA_MODEL": "bundle/model"},
	)
	if err != nil {
		t.Fatalf("LoadWithDefaults: %v", err)
	}
	if c.Model != "bundle/model" {
		t.Errorf("Model = %q, want the bundle default", c.Model)
	}
	if c.OpenRouterURL != defaultOpenRouterURL {
		t.Errorf("OpenRouterURL = %q, want the compiled default", c.OpenRouterURL)
	}
}

func TestConfigDefaultLookupChain(t *testing.T) {
	defaults := map[string]string{"SMIDJA_MODEL": "bundle/model"}
	c := &Config{
		env:            envFrom(map[string]string{"SMIDJA_MODEL": "env/model"}),
		dotenv:         map[string]string{"SMIDJA_MODEL": "dotenv/model"},
		bundleDefaults: defaults,
	}
	if got := c.Default("SMIDJA_MODEL"); got != "env/model" {
		t.Errorf("Default = %q, want the env value", got)
	}
	c.env = envFrom(nil)
	if got := c.Default("SMIDJA_MODEL"); got != "dotenv/model" {
		t.Errorf("Default = %q, want the dotenv value", got)
	}
	c.dotenv = nil
	if got := c.Default("SMIDJA_MODEL"); got != "bundle/model" {
		t.Errorf("Default = %q, want the bundle default", got)
	}
	if got := c.Default("SMIDJA_UNKNOWN"); got != "" {
		t.Errorf("Default(unknown) = %q, want empty", got)
	}
}

func TestLoadDefaultsKeepsCompiledDefaults(t *testing.T) {
	// Load (no bundle) behaves exactly as before: Default reports only
	// what the env and .env sources define.
	c, err := Load(
		envFrom(nil),
		func() (string, error) { return "/work", nil },
		func() string { return "/home/tester" },
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Model != defaultModel {
		t.Errorf("Model = %q, want %q", c.Model, defaultModel)
	}
	if got := c.Default("SMIDJA_MODEL"); got != "" {
		t.Errorf("Default(SMIDJA_MODEL) = %q, want empty (no env, no .env, no bundle)", got)
	}
}
