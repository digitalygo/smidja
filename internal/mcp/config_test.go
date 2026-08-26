package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConfigFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func TestReadConfigValid(t *testing.T) {
	path := writeConfigFile(t, `{
		"schemaVersion": 1,
		"servers": {
			"chrome": {
				"enabled": true,
				"command": "npx",
				"args": ["-y", "chrome-devtools-mcp"],
				"dir": "/tmp",
				"env": {"FOO": "bar"},
				"passEnv": ["OPENROUTER_API_KEY"],
				"framing": "ndjson",
				"toolPrefix": "cdp",
				"restart": {"maxRestarts": 5, "window": "120s", "backoff": ["100ms", "2s"]}
			},
			"disabled": {"enabled": false}
		}
	}`)
	cfg, err := ReadConfig(path)
	if err != nil {
		t.Fatalf("ReadConfig: %v", err)
	}
	if cfg.SchemaVersion != 1 {
		t.Fatalf("SchemaVersion = %d", cfg.SchemaVersion)
	}
	chrome, ok := cfg.Servers["chrome"]
	if !ok {
		t.Fatal("chrome server missing")
	}
	if !chrome.Enabled || chrome.Command != "npx" || chrome.ToolPrefix != "cdp" {
		t.Fatalf("chrome = %+v", chrome)
	}
	if chrome.Framing != "ndjson" {
		t.Fatalf("framing = %q", chrome.Framing)
	}
	if chrome.Restart == nil || chrome.Restart.MaxRestarts != 5 || time.Duration(chrome.Restart.Window) != 120*time.Second {
		t.Fatalf("restart = %+v", chrome.Restart)
	}
	if len(chrome.Restart.Backoff) != 2 || time.Duration(chrome.Restart.Backoff[0]) != 100*time.Millisecond {
		t.Fatalf("backoff = %v", chrome.Restart.Backoff)
	}
	if cfg.Servers["disabled"].Enabled {
		t.Fatal("disabled server should stay disabled")
	}
}

func TestReadConfigMissingFile(t *testing.T) {
	if _, err := ReadConfig(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("ReadConfig succeeded for missing file")
	}
}

func TestReadConfigInvalidJSON(t *testing.T) {
	path := writeConfigFile(t, `{not json`)
	if _, err := ReadConfig(path); err == nil {
		t.Fatal("ReadConfig succeeded for invalid json")
	}
}

func TestReadConfigBadSchemaVersion(t *testing.T) {
	path := writeConfigFile(t, `{"schemaVersion": 99}`)
	if _, err := ReadConfig(path); err == nil || !strings.Contains(err.Error(), "schemaVersion") {
		t.Fatalf("error = %v, want schemaVersion error", err)
	}
}

func TestReadConfigEnabledWithoutCommand(t *testing.T) {
	path := writeConfigFile(t, `{"schemaVersion":1,"servers":{"x":{"enabled":true}}}`)
	if _, err := ReadConfig(path); err == nil {
		t.Fatal("ReadConfig accepted enabled server without command")
	}
}

func TestReadConfigBadFraming(t *testing.T) {
	path := writeConfigFile(t, `{"schemaVersion":1,"servers":{"x":{"enabled":true,"command":"true","framing":"weird"}}}`)
	if _, err := ReadConfig(path); err == nil {
		t.Fatal("ReadConfig accepted invalid framing")
	}
}

func TestMergeConfigsWholeEntryReplacement(t *testing.T) {
	low := &FileConfig{SchemaVersion: 1, Servers: map[string]ServerConfig{
		"a": {Enabled: true, Command: "/bin/a"},
		"b": {Enabled: true, Command: "/bin/b", Args: []string{"old"}},
		"c": {Enabled: true, Command: "/bin/c"},
	}}
	high := &FileConfig{SchemaVersion: 1, Servers: map[string]ServerConfig{
		"a": {Enabled: false},
		"b": {Enabled: true, Command: "/bin/b2", Args: []string{"new"}, ToolPrefix: "bx"},
		"d": {Enabled: true, Command: "/bin/d"},
	}}
	merged := MergeConfigs(low, high)
	if _, ok := merged.Servers["a"]; ok {
		t.Fatal("tombstoned server a still present")
	}
	b, ok := merged.Servers["b"]
	if !ok {
		t.Fatal("server b missing")
	}
	if b.Command != "/bin/b2" || len(b.Args) != 1 || b.Args[0] != "new" || b.ToolPrefix != "bx" {
		t.Fatalf("server b not fully replaced: %+v", b)
	}
	if _, ok := merged.Servers["c"]; !ok {
		t.Fatal("low-only server c missing")
	}
	if _, ok := merged.Servers["d"]; !ok {
		t.Fatal("high-only server d missing")
	}
	if len(merged.Servers) != 3 {
		t.Fatalf("merged servers = %d, want 3", len(merged.Servers))
	}
}

func TestMergeConfigsNilHandling(t *testing.T) {
	low := &FileConfig{Servers: map[string]ServerConfig{"a": {Enabled: true, Command: "x"}}}
	merged := MergeConfigs(low, nil)
	if _, ok := merged.Servers["a"]; !ok {
		t.Fatal("merge with nil high lost low server")
	}
	merged = MergeConfigs(nil, &FileConfig{Servers: map[string]ServerConfig{"b": {Enabled: true, Command: "y"}}})
	if _, ok := merged.Servers["b"]; !ok {
		t.Fatal("merge with nil low lost high server")
	}
	if merged.Servers == nil {
		t.Fatal("merged servers map is nil")
	}
}

func TestRestartPolicyDefaults(t *testing.T) {
	def := (&RestartPolicy{}).withDefaults()
	if def.MaxRestarts != 3 {
		t.Fatalf("default MaxRestarts = %d", def.MaxRestarts)
	}
	if time.Duration(def.Window) != time.Minute {
		t.Fatalf("default Window = %v", def.Window)
	}
	if len(def.Backoff) != 3 ||
		time.Duration(def.Backoff[0]) != 250*time.Millisecond ||
		time.Duration(def.Backoff[1]) != time.Second ||
		time.Duration(def.Backoff[2]) != 4*time.Second {
		t.Fatalf("default backoff = %v", def.Backoff)
	}
	custom := (&RestartPolicy{MaxRestarts: 1, Window: Duration(time.Second)}).withDefaults()
	if custom.MaxRestarts != 1 || time.Duration(custom.Window) != time.Second {
		t.Fatalf("custom restart = %+v", custom)
	}
	if len(custom.Backoff) != 3 {
		t.Fatalf("custom backoff = %v", custom.Backoff)
	}
}

func TestDurationJSON(t *testing.T) {
	raw, err := json.Marshal(Duration(1500 * time.Millisecond))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(raw) != `"1.5s"` {
		t.Fatalf("marshaled = %s", raw)
	}
	var d Duration
	if err := json.Unmarshal([]byte(`"3m30s"`), &d); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if time.Duration(d) != 210*time.Second {
		t.Fatalf("unmarshaled = %v", d)
	}
	if err := json.Unmarshal([]byte(`"bogus"`), &d); err == nil {
		t.Fatal("Unmarshal accepted invalid duration")
	}
	if err := json.Unmarshal([]byte(`1000000000`), &d); err != nil {
		t.Fatalf("Unmarshal number: %v", err)
	}
	if time.Duration(d) != time.Second {
		t.Fatalf("numeric unmarshal = %v, want 1s", d)
	}
	if err := json.Unmarshal([]byte(`{}`), &d); err == nil {
		t.Fatal("Unmarshal accepted non-string non-number")
	}
}

func TestParseFraming(t *testing.T) {
	cases := map[string]Framing{
		"":               FramingAuto,
		"auto":           FramingAuto,
		"AUTO":           FramingAuto,
		"ndjson":         FramingNDJSON,
		"content-length": FramingContentLength,
	}
	for input, want := range cases {
		got, err := parseFraming(input)
		if err != nil {
			t.Fatalf("parseFraming(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("parseFraming(%q) = %v, want %v", input, got, want)
		}
	}
	if _, err := parseFraming("garbage"); err == nil {
		t.Fatal("parseFraming accepted garbage")
	}
}

func TestChildEnvAllowlist(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("HOME", "/home/tester")
	t.Setenv("TMPDIR", "/var/tmp")
	t.Setenv("LANG", "en_US.UTF-8")
	t.Setenv("LC_ALL", "C")
	t.Setenv("LC_MESSAGES", "it_IT.UTF-8")
	t.Setenv("MCP_SECRET_LEAK", "topsecret")
	t.Setenv("MCP_KEEP_VAR", "should-not-leak")
	cfg := ServerConfig{
		Env:     map[string]string{"MCP_EXTRA": "1", "MCP_SECRET_LEAK": "overridden"},
		PassEnv: []string{"MCP_PASSED", "MCP_MISSING"},
	}
	resolver := func(key string) (string, bool) {
		if key == "MCP_PASSED" {
			return "resolved-value", true
		}
		return "", false
	}
	env := childEnv(cfg, resolver)
	got := map[string]string{}
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			got[key] = value
		}
	}
	for _, key := range []string{"PATH", "HOME", "TMPDIR", "LANG", "LC_ALL", "LC_MESSAGES"} {
		if got[key] == "" {
			t.Fatalf("inherited var %s missing from child env", key)
		}
	}
	if got["MCP_KEEP_VAR"] != "" {
		t.Fatal("non-allowlisted var leaked into child env")
	}
	if got["MCP_EXTRA"] != "1" {
		t.Fatalf("explicit env entry missing: %v", got)
	}
	if got["MCP_SECRET_LEAK"] != "overridden" {
		t.Fatalf("explicit env entry should override inherited leak: %v", got)
	}
	if got["MCP_PASSED"] != "resolved-value" {
		t.Fatalf("passEnv via resolver missing: %v", got)
	}
	if got["MCP_MISSING"] != "" {
		t.Fatal("unresolved passEnv name leaked")
	}
}

func TestChildEnvNilResolver(t *testing.T) {
	cfg := ServerConfig{PassEnv: []string{"MCP_ANY"}}
	env := childEnv(cfg, nil)
	for _, entry := range env {
		if strings.HasPrefix(entry, "MCP_ANY=") {
			t.Fatal("nil resolver passed a passEnv entry")
		}
	}
}

func TestIsInheritedEnvKey(t *testing.T) {
	for _, key := range []string{"PATH", "HOME", "TMPDIR", "LANG", "LC_ALL", "LC_NUMERIC"} {
		if !isInheritedEnvKey(key) {
			t.Fatalf("%s should be inherited", key)
		}
	}
	for _, key := range []string{"PWD", "SHELL", "MCP_X", "LC"} {
		if isInheritedEnvKey(key) {
			t.Fatalf("%s should not be inherited", key)
		}
	}
}
