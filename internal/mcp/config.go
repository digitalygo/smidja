package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

const schemaVersion = 1

type FileConfig struct {
	SchemaVersion int                     `json:"schemaVersion"`
	Servers       map[string]ServerConfig `json:"servers,omitempty"`
}

type ServerConfig struct {
	Enabled    bool              `json:"enabled,omitempty"`
	Required   bool              `json:"required,omitempty"`
	Command    string            `json:"command,omitempty"`
	Args       []string          `json:"args,omitempty"`
	Dir        string            `json:"dir,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	PassEnv    []string          `json:"passEnv,omitempty"`
	Framing    string            `json:"framing,omitempty"`
	ToolPrefix string            `json:"toolPrefix,omitempty"`
	Restart    *RestartPolicy    `json:"restart,omitempty"`
}

type RestartPolicy struct {
	MaxRestarts int        `json:"maxRestarts,omitempty"`
	Window      Duration   `json:"window,omitempty"`
	Backoff     []Duration `json:"backoff,omitempty"`
}

type Duration time.Duration

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		v, err := time.ParseDuration(s)
		if err != nil {
			return fmt.Errorf("mcp: invalid duration %q: %w", s, err)
		}
		*d = Duration(v)
		return nil
	}
	var n int64
	if err := json.Unmarshal(b, &n); err != nil {
		return fmt.Errorf("mcp: invalid duration value %s", b)
	}
	*d = Duration(time.Duration(n))
	return nil
}

func ReadConfig(path string) (*FileConfig, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("mcp: read config %s: %w", path, err)
	}
	var cfg FileConfig
	if err := json.Unmarshal(content, &cfg); err != nil {
		return nil, fmt.Errorf("mcp: parse config %s: %w", path, err)
	}
	if cfg.SchemaVersion != schemaVersion {
		return nil, fmt.Errorf("mcp: config %s: unsupported schemaVersion %d, want %d", path, cfg.SchemaVersion, schemaVersion)
	}
	for name, srv := range cfg.Servers {
		if !srv.Enabled {
			continue
		}
		if strings.TrimSpace(srv.Command) == "" {
			return nil, fmt.Errorf("mcp: config %s: server %q enabled without command", path, name)
		}
		if _, err := parseFraming(srv.Framing); err != nil {
			return nil, fmt.Errorf("mcp: config %s: server %q: %w", path, name, err)
		}
	}
	return &cfg, nil
}

func MergeConfigs(low, high *FileConfig) *FileConfig {
	out := &FileConfig{}
	if low != nil {
		out.SchemaVersion = low.SchemaVersion
		out.Servers = make(map[string]ServerConfig, len(low.Servers))
		for name, srv := range low.Servers {
			out.Servers[name] = srv
		}
	}
	if high != nil {
		if high.SchemaVersion != 0 {
			out.SchemaVersion = high.SchemaVersion
		}
		if out.Servers == nil {
			out.Servers = map[string]ServerConfig{}
		}
		for name, srv := range high.Servers {
			if !srv.Enabled {
				delete(out.Servers, name)
				continue
			}
			out.Servers[name] = srv
		}
	}
	if out.Servers == nil {
		out.Servers = map[string]ServerConfig{}
	}
	return out
}

func (p *RestartPolicy) withDefaults() RestartPolicy {
	out := RestartPolicy{}
	if p != nil {
		out = *p
	}
	if out.MaxRestarts <= 0 {
		out.MaxRestarts = 3
	}
	if out.Window <= 0 {
		out.Window = Duration(time.Minute)
	}
	if len(out.Backoff) == 0 {
		out.Backoff = []Duration{
			Duration(250 * time.Millisecond),
			Duration(time.Second),
			Duration(4 * time.Second),
		}
	}
	return out
}

var inheritEnvKeys = [...]string{"PATH", "HOME", "TMPDIR", "LANG"}

func isInheritedEnvKey(key string) bool {
	for _, k := range inheritEnvKeys {
		if key == k {
			return true
		}
	}
	return strings.HasPrefix(key, "LC_")
}

func childEnv(cfg ServerConfig, resolver func(string) (string, bool)) []string {
	if resolver == nil {
		resolver = func(string) (string, bool) { return "", false }
	}
	env := make([]string, 0, 8+len(cfg.Env)+len(cfg.PassEnv))
	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		if ok && isInheritedEnvKey(key) {
			env = append(env, entry)
		}
	}
	for key, value := range cfg.Env {
		env = append(env, key+"="+value)
	}
	for _, name := range cfg.PassEnv {
		if value, ok := resolver(name); ok {
			env = append(env, name+"="+value)
		}
	}
	return env
}

func parseFraming(s string) (Framing, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "auto":
		return FramingAuto, nil
	case "ndjson":
		return FramingNDJSON, nil
	case "content-length":
		return FramingContentLength, nil
	}
	return FramingAuto, fmt.Errorf("mcp: unknown framing %q", s)
}
