package authstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type Entry struct {
	Type    string `json:"type"`
	Key     string `json:"key,omitempty"`
	Access  string `json:"access,omitempty"`
	Refresh string `json:"refresh,omitempty"`
	Expires int64  `json:"expires,omitempty"`

	raw map[string]json.RawMessage
}

func (e *Entry) UnmarshalJSON(data []byte) error {
	type alias Entry
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(data, &all); err != nil {
		return err
	}
	e.Type, e.Key, e.Access, e.Refresh, e.Expires = a.Type, a.Key, a.Access, a.Refresh, a.Expires
	e.raw = make(map[string]json.RawMessage, len(all))
	for k, v := range all {
		switch k {
		case "type", "key", "access", "refresh", "expires":
		default:
			e.raw[k] = v
		}
	}
	return nil
}

func (e Entry) MarshalJSON() ([]byte, error) {
	type alias Entry
	out, err := json.Marshal(alias(e))
	if err != nil {
		return nil, err
	}
	if len(e.raw) == 0 {
		return out, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(out, &obj); err != nil {
		return nil, err
	}
	for k, v := range e.raw {
		obj[k] = v
	}
	return json.Marshal(obj)
}

type Store struct {
	path string
	mu   sync.Mutex
	data map[string]Entry
}

func Load(path string) (*Store, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Store{path: path, data: make(map[string]Entry)}, nil
		}
		return nil, fmt.Errorf("authstore: read %s: %w", path, err)
	}
	data, err := parse(content)
	if err != nil {
		return nil, fmt.Errorf("authstore: load %s: %w", path, err)
	}
	return &Store{path: path, data: data}, nil
}

func parse(content []byte) (map[string]Entry, error) {
	var entries map[string]Entry
	if err := json.Unmarshal(content, &entries); err != nil {
		return nil, fmt.Errorf("invalid auth.json: %w", err)
	}
	if entries == nil {
		if string(content) == "null" {
			return make(map[string]Entry), nil
		}
		return nil, errors.New("invalid auth.json: expected an object")
	}
	for provider, entry := range entries {
		if err := validate(provider, entry); err != nil {
			return nil, err
		}
	}
	return entries, nil
}

const openRouterOAuth = "openrouter-oauth"

func validate(provider string, e Entry) error {
	switch e.Type {
	case "api_key":
		if raw, ok := e.raw["env"]; ok {
			if !isStringObject(raw) {
				return fmt.Errorf("invalid auth.json credential for provider %q: env must be an object of strings", provider)
			}
		}
		return nil
	case "oauth":
		if e.Access == "" {
			return fmt.Errorf("invalid auth.json credential for provider %q: oauth requires access", provider)
		}
		if e.Refresh == "" && provider != openRouterOAuth {
			return fmt.Errorf("invalid auth.json credential for provider %q: oauth requires refresh", provider)
		}
		return nil
	default:
		return fmt.Errorf("invalid auth.json credential for provider %q: unknown type %q", provider, e.Type)
	}
}

func isStringObject(raw json.RawMessage) bool {
	var obj map[string]string
	return json.Unmarshal(raw, &obj) == nil
}

func (s *Store) Get(provider string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.data[provider]
	return e, ok
}

func (s *Store) Set(provider string, e Entry) error {
	if provider == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make(map[string]Entry, len(s.data)+1)
	for k, v := range s.data {
		next[k] = v
	}
	next[provider] = e
	if err := s.write(next); err != nil {
		return err
	}
	s.data = next
	return nil
}

func (s *Store) Remove(provider string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data[provider]; !ok {
		return nil
	}
	next := make(map[string]Entry, len(s.data)-1)
	for k, v := range s.data {
		if k != provider {
			next[k] = v
		}
	}
	if err := s.write(next); err != nil {
		return err
	}
	s.data = next
	return nil
}

func (s *Store) write(data map[string]Entry) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("authstore: create %s: %w", dir, err)
	}
	body, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("authstore: encode: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".auth-*.tmp")
	if err != nil {
		return fmt.Errorf("authstore: create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("authstore: chmod temp: %w", err)
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return fmt.Errorf("authstore: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("authstore: close temp: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("authstore: rename %s: %w", s.path, err)
	}
	return nil
}
