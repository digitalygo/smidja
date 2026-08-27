package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var osCreateTemp = os.CreateTemp
var osRename = os.Rename

type bindingStore struct {
	path string
	mu   sync.Mutex
	keys map[string]string
}

func loadBindings(path string) (*bindingStore, error) {
	b := &bindingStore{path: path, keys: make(map[string]string)}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return b, nil
		}
		return nil, fmt.Errorf("gateway: read bindings %s: %w", path, err)
	}
	if len(data) == 0 {
		return b, nil
	}
	if err := json.Unmarshal(data, &b.keys); err != nil {
		return nil, fmt.Errorf("gateway: decode bindings %s: %w", path, err)
	}
	if b.keys == nil {
		b.keys = make(map[string]string)
	}
	return b, nil
}

func (b *bindingStore) lookup(key string) (string, bool) {
	if b == nil {
		return "", false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	path, ok := b.keys[key]
	return path, ok
}

func (b *bindingStore) set(key, sessionPath string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.keys[key] = sessionPath
	return b.writeLocked()
}

func (b *bindingStore) writeLocked() error {
	dir := filepath.Dir(b.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("gateway: create bindings dir %s: %w", dir, err)
	}
	body, err := json.MarshalIndent(b.keys, "", "  ")
	if err != nil {
		return fmt.Errorf("gateway: encode bindings: %w", err)
	}
	tmp, err := osCreateTemp(dir, ".bindings-*.tmp")
	if err != nil {
		return fmt.Errorf("gateway: create bindings temp: %w", err)
	}
	name := tmp.Name()
	cleanup := func(wrapErr error) error {
		tmp.Close()
		os.Remove(name)
		return wrapErr
	}
	if err := tmp.Chmod(0o600); err != nil {
		return cleanup(fmt.Errorf("gateway: chmod bindings temp: %w", err))
	}
	if _, err := tmp.Write(body); err != nil {
		return cleanup(fmt.Errorf("gateway: write bindings temp: %w", err))
	}
	if err := tmp.Sync(); err != nil {
		return cleanup(fmt.Errorf("gateway: sync bindings temp: %w", err))
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return fmt.Errorf("gateway: close bindings temp: %w", err)
	}
	if err := osRename(name, b.path); err != nil {
		os.Remove(name)
		return fmt.Errorf("gateway: rename bindings %s: %w", b.path, err)
	}
	return nil
}
