package extensions

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"github.com/digitalygo/smidja/sdk"
)

var (
	ErrProviderName     = errors.New("extensions: invalid provider name")
	ErrProviderURL      = errors.New("extensions: provider base URL must be a plain http(s) URL without user info")
	ErrProviderDialect  = errors.New("extensions: unsupported provider completion dialect")
	ErrProviderModel    = errors.New("extensions: provider models require non-empty ids")
	ErrProviderReserved = errors.New("extensions: provider name collides with a built-in transport")
	ErrProviderNotFound = errors.New("extensions: provider is not registered")
)

const supportedProviderDialect = "openai-completions"

var providerNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type ProviderEntry struct {
	Name string

	BaseURL string

	API string

	Models []sdk.Model
}

type providerRecord struct {
	entry ProviderEntry
	key   string
}

type ProviderRegistry struct {
	mu       sync.Mutex
	order    []string
	entries  map[string]providerRecord
	reserved map[string]struct{}
}

func NewProviderRegistry(reserved ...string) *ProviderRegistry {
	blocked := make(map[string]struct{}, len(reserved))
	for _, name := range reserved {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			blocked[trimmed] = struct{}{}
		}
	}
	return &ProviderRegistry{entries: make(map[string]providerRecord), reserved: blocked}
}

func (r *ProviderRegistry) Register(name string, cfg sdk.ProviderConfig) error {
	if r == nil {
		return ErrUnavailable
	}
	name = strings.TrimSpace(name)
	if !providerNamePattern.MatchString(name) {
		return fmt.Errorf("%w: %q", ErrProviderName, name)
	}
	if _, blocked := r.reserved[name]; blocked {
		return fmt.Errorf("%w: %s", ErrProviderReserved, name)
	}
	base, err := validateProviderBaseURL(cfg.BaseURL)
	if err != nil {
		return err
	}
	api, err := validateProviderDialect(cfg.API)
	if err != nil {
		return err
	}
	models, err := normalizeProviderModels(name, cfg.Models)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.entries[name]; !exists {
		r.order = append(r.order, name)
	}
	r.entries[name] = providerRecord{
		entry: ProviderEntry{Name: name, BaseURL: base, API: api, Models: models},
		key:   cfg.APIKey,
	}
	return nil
}

func validateProviderBaseURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("%w: malformed URL", ErrProviderURL)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("%w: %s requires an http or https scheme", ErrProviderURL, redactedProviderURL(parsed))
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("%w: %s is missing a host", ErrProviderURL, redactedProviderURL(parsed))
	}
	if parsed.User != nil {
		return "", fmt.Errorf("%w: %s must not include user info", ErrProviderURL, redactedProviderURL(parsed))
	}
	return strings.TrimRight(trimmed, "/"), nil
}

func redactedProviderURL(parsed *url.URL) string {
	if parsed == nil {
		return "the provider URL"
	}
	redacted := &url.URL{Scheme: parsed.Scheme, Host: parsed.Host, Path: parsed.Path}
	if parsed.RawQuery != "" {
		redacted.RawQuery = "redacted"
	}
	if parsed.Fragment != "" {
		redacted.Fragment = "redacted"
	}
	return redacted.String()
}

func validateProviderDialect(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return supportedProviderDialect, nil
	}
	if trimmed != supportedProviderDialect {
		return "", fmt.Errorf("%w: %q", ErrProviderDialect, raw)
	}
	return trimmed, nil
}

func normalizeProviderModels(provider string, models []sdk.Model) ([]sdk.Model, error) {
	out := make([]sdk.Model, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		id := strings.TrimSpace(model.ID)
		if id == "" {
			return nil, fmt.Errorf("%w: provider %s", ErrProviderModel, provider)
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, sdk.Model{ID: id, Name: model.Name, Provider: provider})
	}
	return out, nil
}

func (r *ProviderRegistry) Remove(name string) error {
	if r == nil {
		return ErrUnavailable
	}
	name = strings.TrimSpace(name)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.entries[name]; !ok {
		return fmt.Errorf("%w: %s", ErrProviderNotFound, name)
	}
	delete(r.entries, name)
	for i, existing := range r.order {
		if existing == name {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
	return nil
}

func (r *ProviderRegistry) Lookup(name string) (ProviderEntry, bool) {
	if r == nil {
		return ProviderEntry{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	record, ok := r.entries[name]
	if !ok {
		return ProviderEntry{}, false
	}
	return cloneProviderEntry(record.entry), true
}

func (r *ProviderRegistry) Credential(name string) (string, bool) {
	if r == nil {
		return "", false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	record, ok := r.entries[name]
	if !ok {
		return "", false
	}
	return record.key, true
}

func (r *ProviderRegistry) FindModel(modelID string) (ProviderEntry, bool) {
	if r == nil {
		return ProviderEntry{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, name := range r.order {
		record, ok := r.entries[name]
		if !ok {
			continue
		}
		for _, model := range record.entry.Models {
			if model.ID == modelID {
				return cloneProviderEntry(record.entry), true
			}
		}
	}
	return ProviderEntry{}, false
}

func (r *ProviderRegistry) Names() []string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.order...)
}

func (r *ProviderRegistry) Models() []sdk.Model {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []sdk.Model
	for _, name := range r.order {
		record, ok := r.entries[name]
		if !ok {
			continue
		}
		for _, model := range record.entry.Models {
			out = append(out, model)
		}
	}
	return out
}

func cloneProviderEntry(entry ProviderEntry) ProviderEntry {
	clone := entry
	clone.Models = append([]sdk.Model(nil), entry.Models...)
	return clone
}

type providerSnapshot struct {
	order   []string
	entries map[string]providerRecord
}

func (r *ProviderRegistry) snapshot() providerSnapshot {
	if r == nil {
		return providerSnapshot{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	snap := providerSnapshot{
		order:   append([]string(nil), r.order...),
		entries: make(map[string]providerRecord, len(r.entries)),
	}
	for name, record := range r.entries {
		record.entry = cloneProviderEntry(record.entry)
		snap.entries[name] = record
	}
	return snap
}

func (r *ProviderRegistry) restore(snap providerSnapshot) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.order = snap.order
	r.entries = snap.entries
	r.mu.Unlock()
}
