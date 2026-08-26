package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultModel           = "anthropic/claude-sonnet-4.5"
	defaultOpenRouterURL   = "https://openrouter.ai/api/v1/chat/completions"
	defaultExecTimeoutSecs = 30
	defaultMaxReadLines    = 2000
	defaultMaxOutputBytes  = 50 * 1024
)

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

type Config struct {
	Model string

	OpenRouterURL string

	APIKey string

	SessionDir string

	WorkspaceRoot string

	ExecTimeoutSecs int

	MaxReadLines int

	MaxOutputBytes int64

	ContextEnabled bool

	ContextWindowTokens int64

	ContextCacheMissAfter time.Duration

	ContextPruneThreshold float64

	ContextCompactThreshold float64

	ContextSafetyThreshold float64

	ContextCompactTarget float64

	ContextKeepRecentMessages int

	ContextSelectorChunkTokens int64

	ContextSelectorModel string

	env            func(string) string
	dotenv         map[string]string
	bundleDefaults map[string]string
}

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

func Load(env func(string) string, getwd func() (string, error), home func() string) (*Config, error) {
	return LoadWithDefaults(env, getwd, home, nil)
}

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

	dotenv := loadDotEnv(cwd)
	lookup := func(k string) string {
		if v := env(k); v != "" {
			return v
		}
		return dotenv[k]
	}
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

var readDotEnvFile = os.ReadFile

func loadDotEnv(dir string) map[string]string {
	content, err := readDotEnvFile(filepath.Join(dir, ".env"))
	if err != nil {
		return nil
	}
	return parseDotEnv(string(content))
}

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

func stripQuotes(v string) string {
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}

func intDefault(v string, def int) int {
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return def
	}
	return n
}

func int64Default(v string, def int64) int64 {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 1 {
		return def
	}
	return n
}

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

func intAtLeastZero(v string) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func int64AtLeastZero(v string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func fracDefault(v string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || f <= 0 || f >= 1 {
		return 0
	}
	return f
}

func safetyFracDefault(v string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || f <= 0 || f > 1 {
		return 0
	}
	return f
}
