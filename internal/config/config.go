// Package config loads and holds the runtime configuration for smidja.
//
// Configuration sources, in increasing precedence: compiled-in defaults, an
// optional .env file in the working directory, and environment variables.
// Load applies the defaults, then overrides each value from the .env file
// and then the environment when present, and finally falls back to the
// default when an override is empty, unparsable, or below the minimum
// valid value.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Compiled-in default values applied by Load.
const (
	defaultModel           = "anthropic/claude-sonnet-4.5"
	defaultOpenRouterURL   = "https://openrouter.ai/api/v1/chat/completions"
	defaultExecTimeoutSecs = 30
	defaultMaxReadLines    = 2000
	defaultMaxOutputBytes  = 50 * 1024 // 50 KB
)

// Environment variable names consulted by Load.
const (
	envModel           = "SMIDJA_MODEL"
	envOpenRouterURL   = "SMIDJA_OPENROUTER_URL"
	envAPIKey          = "OPENROUTER_API_KEY"
	envSessionDir      = "SMIDJA_SESSION_DIR"
	envExecTimeoutSecs = "SMIDJA_EXEC_TIMEOUT_SECS"
	envMaxReadLines    = "SMIDJA_MAX_READ_LINES"
	envMaxOutputBytes  = "SMIDJA_MAX_OUTPUT_BYTES"
)

// Config is the immutable runtime configuration of one smidja invocation.
type Config struct {
	// Model is the provider model identifier used for assistant turns.
	// Default "anthropic/claude-sonnet-4.5"; overridable via SMIDJA_MODEL.
	Model string

	// OpenRouterURL is the chat completions endpoint.
	// Default "https://openrouter.ai/api/v1/chat/completions";
	// overridable via SMIDJA_OPENROUTER_URL.
	OpenRouterURL string

	// APIKey is the OpenRouter API key. It comes from OPENROUTER_API_KEY
	// and may be empty until request time.
	APIKey string

	// SessionDir is the directory where sessions are persisted as JSONL.
	// Default $HOME/.smidja/sessions; overridable via SMIDJA_SESSION_DIR.
	SessionDir string

	// WorkspaceRoot is the working directory the agent operates in,
	// canonicalized (absolute, cleaned) at Load time. Defaults to the
	// current working directory. Symlink resolution happens later in
	// internal/workspace.
	WorkspaceRoot string

	// ExecTimeoutSecs is the timeout in seconds for exec tool runs.
	// Default 30; values below 1 fall back to the default.
	ExecTimeoutSecs int

	// MaxReadLines is the maximum number of lines a read tool returns.
	// Default 2000; values below 1 fall back to the default.
	MaxReadLines int

	// MaxOutputBytes is the maximum captured output size for exec tool
	// runs, in bytes. Default 50 KB; values below 1 fall back to the
	// default.
	MaxOutputBytes int64
}

// Load builds a Config from the given sources. env returns the value of an
// environment variable (empty string when unset), getwd returns the current
// working directory, and home returns the user's home directory. All three
// functions must be non-nil.
//
// Before resolving values, Load reads an optional .env file in the working
// directory. A real environment variable always wins over the .env file;
// .env values are used only for keys whose environment variable is empty.
// A missing or unreadable .env file is not an error, and malformed lines
// are silently skipped. .env values are never logged and never appear in
// error messages.
//
// Load applies defaults, environment overrides, and default fallbacks for
// invalid values. It fails only when the working directory cannot be
// determined or no home directory can be resolved.
func Load(env func(string) string, getwd func() (string, error), home func() string) (*Config, error) {
	if env == nil {
		return nil, fmt.Errorf("config: nil env function")
	}
	if getwd == nil {
		return nil, fmt.Errorf("config: nil getwd function")
	}
	if home == nil {
		return nil, fmt.Errorf("config: nil home function")
	}

	cwd, err := getwd()
	if err != nil {
		return nil, fmt.Errorf("config: get working directory: %w", err)
	}
	root, err := filepath.Abs(cwd)
	if err != nil {
		return nil, fmt.Errorf("config: canonicalize working directory %q: %w", cwd, err)
	}

	// Real environment variables win over .env values, so resolve through
	// a merged lookup: env first, .env as fallback.
	dotenv := loadDotEnv(cwd)
	lookup := func(k string) string {
		if v := env(k); v != "" {
			return v
		}
		return dotenv[k]
	}

	homeDir := home()
	if homeDir == "" {
		homeDir, err = os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("config: resolve home directory: %w", err)
		}
	}
	if homeDir == "" {
		return nil, fmt.Errorf("config: empty home directory")
	}

	return &Config{
		Model:           strDefault(lookup(envModel), defaultModel),
		OpenRouterURL:   strDefault(lookup(envOpenRouterURL), defaultOpenRouterURL),
		APIKey:          lookup(envAPIKey),
		SessionDir:      strDefault(lookup(envSessionDir), filepath.Join(homeDir, ".smidja", "sessions")),
		WorkspaceRoot:   filepath.Clean(root),
		ExecTimeoutSecs: intDefault(lookup(envExecTimeoutSecs), defaultExecTimeoutSecs),
		MaxReadLines:    intDefault(lookup(envMaxReadLines), defaultMaxReadLines),
		MaxOutputBytes:  int64Default(lookup(envMaxOutputBytes), defaultMaxOutputBytes),
	}, nil
}

// readDotEnvFile is a test seam: Load reads the .env file through it so
// tests can inject content without touching the filesystem. Load treats
// any read error as "no .env file present".
var readDotEnvFile = os.ReadFile

// loadDotEnv reads and parses the .env file in dir and returns its
// key/value pairs. A missing or unreadable file yields no entries, and
// parse problems are silently skipped. Neither the file contents nor any
// values are ever logged or included in errors.
func loadDotEnv(dir string) map[string]string {
	content, err := readDotEnvFile(filepath.Join(dir, ".env"))
	if err != nil {
		return nil
	}
	return parseDotEnv(string(content))
}

// parseDotEnv parses dotenv-formatted content into a map. Blank lines and
// lines starting with # are skipped, as are lines without an "=" and lines
// with an empty key. Keys and values are trimmed of surrounding spaces;
// values may be wrapped in matching single or double quotes, which are
// stripped. The first "=" separates key from value, so values may contain
// further "=" characters.
func parseDotEnv(content string) map[string]string {
	values := make(map[string]string)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		values[key] = stripQuotes(strings.TrimSpace(value))
	}
	return values
}

// stripQuotes removes one pair of matching surrounding single or double
// quotes from v when present. Other quote placements are left untouched.
func stripQuotes(v string) string {
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}

// strDefault returns v when non-empty, otherwise def.
func strDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// intDefault parses v as an int and returns it when valid and >= 1,
// otherwise def. This implements the "invalid values fall back to
// defaults" rule for positive integers.
func intDefault(v string, def int) int {
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return def
	}
	return n
}

// int64Default is intDefault for int64 values.
func int64Default(v string, def int64) int64 {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 1 {
		return def
	}
	return n
}
