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
	"time"
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
	envModel                      = "SMIDJA_MODEL"
	envOpenRouterURL              = "SMIDJA_OPENROUTER_URL"
	envAPIKey                     = "OPENROUTER_API_KEY"
	envSessionDir                 = "SMIDJA_SESSION_DIR"
	envExecTimeoutSecs            = "SMIDJA_EXEC_TIMEOUT_SECS"
	envMaxReadLines               = "SMIDJA_MAX_READ_LINES"
	envMaxOutputBytes             = "SMIDJA_MAX_OUTPUT_BYTES"
	envContextEnabled             = "SMIDJA_CONTEXT"
	envContextWindowTokens        = "SMIDJA_CONTEXT_WINDOW_TOKENS"
	envContextCacheMissAfterSecs  = "SMIDJA_CONTEXT_CACHE_MISS_AFTER_SECS"
	envContextPruneThreshold      = "SMIDJA_CONTEXT_PRUNE_THRESHOLD"
	envContextCompactThreshold    = "SMIDJA_CONTEXT_COMPACT_THRESHOLD"
	envContextSafetyThreshold     = "SMIDJA_CONTEXT_SAFETY_THRESHOLD"
	envContextCompactTarget       = "SMIDJA_CONTEXT_COMPACT_TARGET"
	envContextKeepRecentMessages  = "SMIDJA_CONTEXT_KEEP_RECENT_MESSAGES"
	envContextSelectorChunkTokens = "SMIDJA_CONTEXT_SELECTOR_CHUNK_TOKENS"
	envContextSelectorModel       = "SMIDJA_CONTEXT_SELECTOR_MODEL"
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

	// ContextEnabled turns smart context management on: prune and
	// compact of the conversation before each provider call. Default
	// true; overridable via SMIDJA_CONTEXT (0, false, no, off disable).
	ContextEnabled bool

	// ContextWindowTokens is the model context window in tokens used by
	// the context-manager thresholds. Default 0 resolves the window from
	// the model registry by the active Model; a positive value overrides
	// the lookup. Overridable via SMIDJA_CONTEXT_WINDOW_TOKENS.
	ContextWindowTokens int64

	// ContextCacheMissAfter is how long after the last observed response
	// the provider cache is considered stale, in seconds. Default 0
	// applies the contextmanager default (5 minutes). Overridable via
	// SMIDJA_CONTEXT_CACHE_MISS_AFTER_SECS.
	ContextCacheMissAfter time.Duration

	// ContextPruneThreshold is the occupancy fraction above which
	// stale-cache calls prune old tool results. Default 0 applies the
	// contextmanager default (0.70). Overridable via
	// SMIDJA_CONTEXT_PRUNE_THRESHOLD.
	ContextPruneThreshold float64

	// ContextCompactThreshold is the occupancy fraction above which
	// stale-cache calls compact via the selector. Default 0 applies the
	// contextmanager default (0.85). Overridable via
	// SMIDJA_CONTEXT_COMPACT_THRESHOLD.
	ContextCompactThreshold float64

	// ContextSafetyThreshold is the occupancy fraction above which
	// compaction fires immediately, regardless of cache age. Default 0
	// applies the contextmanager default (0.95). Overridable via
	// SMIDJA_CONTEXT_SAFETY_THRESHOLD.
	ContextSafetyThreshold float64

	// ContextCompactTarget is the fraction of the context window the
	// retained messages may consume after compaction. Default 0 applies
	// the contextmanager default (0.50). Overridable via
	// SMIDJA_CONTEXT_COMPACT_TARGET.
	ContextCompactTarget float64

	// ContextKeepRecentMessages is how many trailing messages are never
	// pruned or compacted. Default 0 applies the contextmanager default
	// (6). Overridable via SMIDJA_CONTEXT_KEEP_RECENT_MESSAGES.
	ContextKeepRecentMessages int

	// ContextSelectorChunkTokens is the token budget below which
	// candidate messages are chunked before being handed to the
	// selector. Default 0 applies the contextmanager default (12_000).
	// Overridable via SMIDJA_CONTEXT_SELECTOR_CHUNK_TOKENS.
	ContextSelectorChunkTokens int64

	// ContextSelectorModel is the provider model identifier used for
	// selector turns. Empty (the default) uses the main Model.
	// Overridable via SMIDJA_CONTEXT_SELECTOR_MODEL.
	ContextSelectorModel string

	// env, dotenv, and bundleDefaults capture the lookup chain behind
	// Default: the environment function, the .env values, and the
	// bundle's ConfigDefaults, all captured at Load time. The zero
	// values make Default report "" for every key.
	env            func(string) string
	dotenv         map[string]string
	bundleDefaults map[string]string
}

// Default resolves one configuration key with the same precedence Load
// applies: the environment variable first, then the .env file value, then
// the bundle's ConfigDefaults. It returns "" when no source defines key.
// Key is the environment variable name, for example "SMIDJA_MODEL". The
// compiled-in defaults are not part of this lookup: Default reports what
// a caller would get for a key Load does not model, so callers asking
// for a known key with nothing set get "" even though the corresponding
// Config field holds its compiled default.
func (c *Config) Default(key string) string {
	if c.env != nil {
		if v := c.env(key); v != "" {
			return v
		}
	}
	if v := c.dotenv[key]; v != "" {
		return v
	}
	return c.bundleDefaults[key]
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
	return LoadWithDefaults(env, getwd, home, nil)
}

// LoadWithDefaults is Load with an additional layer of configuration
// defaults, the bundle's ConfigDefaults. For every key the fallback chain
// becomes: environment variable, then .env file, then bundle default, then
// the compiled-in default. defaults is keyed by environment variable name
// and may be nil (Load passes nil). A non-string bundle default is
// converted with fmt.Sprint before it reaches Load.
func LoadWithDefaults(env func(string) string, getwd func() (string, error), home func() string, defaults map[string]string) (*Config, error) {
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
	// value resolves one key with the full fallback chain; def is the
	// compiled-in default used when nothing in the chain defines the key.
	value := func(k, def string) string {
		if v := lookup(k); v != "" {
			return v
		}
		if v := defaults[k]; v != "" {
			return v
		}
		return def
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
		Model:                      value(envModel, defaultModel),
		OpenRouterURL:              value(envOpenRouterURL, defaultOpenRouterURL),
		APIKey:                     value(envAPIKey, ""),
		SessionDir:                 value(envSessionDir, filepath.Join(homeDir, ".smidja", "sessions")),
		WorkspaceRoot:              filepath.Clean(root),
		ExecTimeoutSecs:            intDefault(value(envExecTimeoutSecs, ""), defaultExecTimeoutSecs),
		MaxReadLines:               intDefault(value(envMaxReadLines, ""), defaultMaxReadLines),
		MaxOutputBytes:             int64Default(value(envMaxOutputBytes, ""), defaultMaxOutputBytes),
		ContextEnabled:             boolDefault(value(envContextEnabled, ""), true),
		ContextWindowTokens:        int64AtLeastZero(value(envContextWindowTokens, "")),
		ContextCacheMissAfter:      time.Duration(intAtLeastZero(value(envContextCacheMissAfterSecs, ""))) * time.Second,
		ContextPruneThreshold:      fracDefault(value(envContextPruneThreshold, "")),
		ContextCompactThreshold:    fracDefault(value(envContextCompactThreshold, "")),
		ContextSafetyThreshold:     safetyFracDefault(value(envContextSafetyThreshold, "")),
		ContextCompactTarget:       fracDefault(value(envContextCompactTarget, "")),
		ContextKeepRecentMessages:  intAtLeastZero(value(envContextKeepRecentMessages, "")),
		ContextSelectorChunkTokens: int64AtLeastZero(value(envContextSelectorChunkTokens, "")),
		ContextSelectorModel:       value(envContextSelectorModel, ""),
		env:                        env,
		dotenv:                     dotenv,
		bundleDefaults:             defaults,
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

// boolDefault parses v as a boolean setting: an empty value uses def, and
// the common false spellings (0, false, no, off, case-insensitive,
// trimmed) are false; anything else is true.
func boolDefault(v string, def bool) bool {
	s := strings.ToLower(strings.TrimSpace(v))
	if s == "" {
		return def
	}
	switch s {
	case "0", "false", "no", "off":
		return false
	}
	return true
}

// intAtLeastZero parses v as a non-negative int. Invalid or negative
// values fall back to 0, which downstream defaults interpret as "unset".
func intAtLeastZero(v string) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// int64AtLeastZero is intAtLeastZero for int64 values.
func int64AtLeastZero(v string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// fracDefault parses v as a fraction in (0,1). Invalid or out-of-range
// values fall back to 0, which the contextmanager default interprets as
// "unset" and replaces with its own default.
func fracDefault(v string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || f <= 0 || f >= 1 {
		return 0
	}
	return f
}

// safetyFracDefault is fracDefault for the safety threshold, which may
// equal 1 (the full window).
func safetyFracDefault(v string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || f <= 0 || f > 1 {
		return 0
	}
	return f
}
