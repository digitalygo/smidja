// Package authstore persists provider credentials in the Pi-compatible
// ~/.smidja/auth.json file: an object keyed by provider ID whose values
// are credentials of type "api_key" (with a key) or "oauth" (with
// access, refresh, and expires). Unknown fields per entry are preserved
// verbatim across rewrites, the directory is created 0700, the file is
// written 0600 via an atomic temp-file rename, and all read-modify-write
// cycles are serialized by a mutex.
package authstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Entry is one stored credential. Type is "api_key" or "oauth"; the
// remaining fields follow the Pi shape. Unknown JSON fields in the file
// are preserved on rewrite via the custom marshaler.
type Entry struct {
	Type    string `json:"type"`
	Key     string `json:"key,omitempty"`
	Access  string `json:"access,omitempty"`
	Refresh string `json:"refresh,omitempty"`
	Expires int64  `json:"expires,omitempty"`

	// raw holds the unknown fields of this entry, keyed by their JSON
	// name, so a rewrite never drops data the harness does not model.
	raw map[string]json.RawMessage
}

// UnmarshalJSON decodes the typed fields and captures every other field
// into raw.
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

// MarshalJSON emits the typed fields and merges the preserved unknown
// fields back in, so rewrites round-trip the full Pi shape.
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

// Store is an in-memory view of one auth file with mutex-serialized
// read-modify-write persistence. Load once, then use Get, Set, and
// Remove; Set and Remove rewrite the whole file atomically.
type Store struct {
	path string
	mu   sync.Mutex
	data map[string]Entry
}

// Load reads the auth file at path and returns a store over it. A
// missing file yields an empty store without error. A file that is not a
// JSON object, or whose entries do not follow the Pi credential shape,
// yields a clear error.
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

// parse decodes and validates an auth file body into the entry map. The
// top level must be a JSON object and every entry must be a credential
// object matching the Pi shape; anything else is a clear error.
func parse(content []byte) (map[string]Entry, error) {
	var entries map[string]Entry
	if err := json.Unmarshal(content, &entries); err != nil {
		return nil, fmt.Errorf("invalid auth.json: %w", err)
	}
	if entries == nil {
		// The file was "null" or absent content: treat as an empty
		// object only when the body is exactly the JSON null literal;
		// anything else is a parse error caught above.
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

// validate checks one credential against the Pi shape. api_key entries
// accept a key and an env object (env is preserved as an unknown field);
// oauth entries require string access and refresh plus a finite numeric
// expires. Unknown types are rejected, matching Pi.
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
		if e.Access == "" || e.Refresh == "" {
			return fmt.Errorf("invalid auth.json credential for provider %q: oauth requires access and refresh", provider)
		}
		// expires decodes into the typed int64 field; a non-numeric
		// value fails the decode in parse and never reaches here.
		return nil
	default:
		return fmt.Errorf("invalid auth.json credential for provider %q: unknown type %q", provider, e.Type)
	}
}

// isStringObject reports whether raw is a JSON object whose values are
// all JSON strings.
func isStringObject(raw json.RawMessage) bool {
	var obj map[string]string
	return json.Unmarshal(raw, &obj) == nil
}

// Get returns the entry stored for provider. ok is false when the
// provider has no stored credential.
func (s *Store) Get(provider string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.data[provider]
	return e, ok
}

// Set stores the entry for provider and persists the file atomically.
// An empty provider is ignored.
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

// Remove deletes the entry for provider and persists the file
// atomically. Removing an unknown provider is a no-op.
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

// write persists the entry map to the store path: the parent directory
// is created 0700, the body is written to a temp file in the same
// directory, chmod 0600, and renamed over the target. Callers hold the
// mutex, so concurrent read-modify-write cycles never interleave.
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
