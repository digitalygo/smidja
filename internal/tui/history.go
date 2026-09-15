package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const historyMaxEntries = 100

func sanitizeProjectKey(project string) string {
	trimmed := strings.TrimSpace(project)
	if trimmed == "" {
		return "default"
	}
	var b strings.Builder
	for _, r := range trimmed {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	result := strings.Trim(b.String(), "_.")
	if result == "" {
		return "default"
	}
	if len(result) > 128 {
		result = result[:128]
	}
	return result
}

func HistoryFilePath(home, project string) string {
	key := sanitizeProjectKey(project)
	return filepath.Join(home, ".smidja", "prompt-history", key+".json")
}

type HistoryStore struct {
	mu         sync.Mutex
	path       string
	entries    []string
	maxEntries int
}

func NewHistoryStore(home, project string) *HistoryStore {
	return &HistoryStore{
		path:       HistoryFilePath(home, project),
		maxEntries: historyMaxEntries,
	}
}

func NewHistoryStoreAtPath(path string) *HistoryStore {
	return &HistoryStore{
		path:       path,
		maxEntries: historyMaxEntries,
	}
}

func (h *HistoryStore) Path() string {
	return h.path
}

func (h *HistoryStore) SetMaxEntries(n int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if n <= 0 {
		n = historyMaxEntries
	}
	h.maxEntries = n
	if len(h.entries) > h.maxEntries {
		h.entries = append([]string(nil), h.entries[:h.maxEntries]...)
	}
}

func (h *HistoryStore) Entries() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.entries...)
}

func (h *HistoryStore) Load() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	data, err := os.ReadFile(h.path)
	if err != nil {
		if os.IsNotExist(err) {
			h.entries = nil
			return nil
		}
		return err
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		h.entries = nil
		return nil
	}
	var decoded []string
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	filtered := make([]string, 0, len(decoded))
	for _, entry := range decoded {
		if strings.TrimSpace(entry) == "" {
			continue
		}
		filtered = append(filtered, entry)
		if len(filtered) >= h.maxEntries {
			break
		}
	}
	h.entries = filtered
	return nil
}

func (h *HistoryStore) Save() error {
	h.mu.Lock()
	entries := append([]string(nil), h.entries...)
	path := h.path
	h.mu.Unlock()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func (h *HistoryStore) Add(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.entries) > 0 && h.entries[0] == trimmed {
		return false
	}
	entries := make([]string, 0, len(h.entries)+1)
	entries = append(entries, trimmed)
	entries = append(entries, h.entries...)
	if len(entries) > h.maxEntries {
		entries = entries[:h.maxEntries]
	}
	h.entries = entries
	return true
}

func (h *HistoryStore) AddAndSave(text string) (bool, error) {
	added := h.Add(text)
	if !added {
		return false, nil
	}
	return true, h.Save()
}

func (h *HistoryStore) Clear() {
	h.mu.Lock()
	h.entries = nil
	h.mu.Unlock()
}
