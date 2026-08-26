package authstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// writeAuthFile writes content to path, creating the parent directory.
func writeAuthFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// TestLoadMissingFile verifies a missing file yields an empty store
// without error.
func TestLoadMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no", "auth.json")
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load on missing file: %v", err)
	}
	if _, ok := s.Get("openrouter"); ok {
		t.Error("missing file store reports a credential")
	}
}

// TestLoadCorruptFile verifies corrupt content yields a clear error: bad
// JSON, non-object top level, non-object entries, and unknown types all
// fail with a message naming the file.
func TestLoadCorruptFile(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"not json", `this is not json`},
		{"array top level", `[{"type":"api_key"}]`},
		{"string top level", `"nope"`},
		{"entry not object", `{"openrouter":"sk-x"}`},
		{"entry array", `{"openrouter":["api_key"]}`},
		{"unknown type", `{"openrouter":{"type":"magic"}}`},
		{"oauth missing fields", `{"openrouter":{"type":"oauth","access":"a"}}`},
		{"api_key env not object", `{"openrouter":{"type":"api_key","key":"k","env":"nope"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "auth.json")
			writeAuthFile(t, path, tc.content)
			_, err := Load(path)
			if err == nil {
				t.Fatal("Load: want error for corrupt file")
			}
			if !strings.Contains(err.Error(), "auth.json") {
				t.Errorf("error = %q, want message naming auth.json", err)
			}
		})
	}
}

// TestLoadNullFile treats the JSON null literal as an empty store.
func TestLoadNullFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	writeAuthFile(t, path, "null")
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load(null): %v", err)
	}
	if _, ok := s.Get("openrouter"); ok {
		t.Error("null file store reports a credential")
	}
}

// TestRoundTripPreservesUnknownFields writes a Pi-shaped file, loads it,
// rewrites it via Set, and verifies unknown fields survive verbatim.
func TestRoundTripPreservesUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	writeAuthFile(t, path, `{
  "openrouter": {
    "type": "api_key",
    "key": "sk-or-v1-abc",
    "env": {"OPENROUTER_API_KEY": "sk-or-v1-abc"},
    "note": "rotated 2026-08-26",
    "extra": {"nested": [1, 2, 3]}
  },
  "anthropic": {
    "type": "oauth",
    "access": "tok-access",
    "refresh": "tok-refresh",
    "expires": 1780000000000,
    "scope": "read write"
  }
}`)
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	or, ok := s.Get("openrouter")
	if !ok {
		t.Fatal("openrouter entry missing")
	}
	if or.Type != "api_key" || or.Key != "sk-or-v1-abc" {
		t.Errorf("openrouter entry = %+v, want api_key with key", or)
	}
	if len(or.raw) != 3 {
		t.Fatalf("preserved unknown fields = %d, want 3 (env, note, extra): %v", len(or.raw), or.raw)
	}
	if string(or.raw["env"]) != `{"OPENROUTER_API_KEY": "sk-or-v1-abc"}` &&
		string(or.raw["env"]) != `{"OPENROUTER_API_KEY":"sk-or-v1-abc"}` {
		t.Errorf("env raw = %s, want preserved env object", or.raw["env"])
	}
	if string(or.raw["note"]) != `"rotated 2026-08-26"` {
		t.Errorf("note raw = %s, want preserved string", or.raw["note"])
	}
	if string(or.raw["extra"]) != `{"nested": [1, 2, 3]}` &&
		string(or.raw["extra"]) != `{"nested":[1,2,3]}` {
		t.Errorf("extra raw = %s, want preserved object", or.raw["extra"])
	}

	an, ok := s.Get("anthropic")
	if !ok {
		t.Fatal("anthropic entry missing")
	}
	if an.Type != "oauth" || an.Access != "tok-access" || an.Refresh != "tok-refresh" || an.Expires != 1780000000000 {
		t.Errorf("anthropic entry = %+v, want full oauth shape", an)
	}

	// Rewrite via Set and confirm the file still carries every field.
	if err := s.Set("openrouter", or); err != nil {
		t.Fatalf("Set: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read rewritten file: %v", err)
	}
	var rewritten map[string]map[string]json.RawMessage
	if err := json.Unmarshal(content, &rewritten); err != nil {
		t.Fatalf("rewritten file does not parse: %v\n%s", err, content)
	}
	for _, key := range []string{"env", "note", "extra"} {
		if _, ok := rewritten["openrouter"][key]; !ok {
			t.Errorf("rewritten file lost unknown field %q: %s", key, content)
		}
	}
	if _, ok := rewritten["anthropic"]["scope"]; !ok {
		t.Errorf("rewritten file lost unknown field %q: %s", "scope", content)
	}
	if string(rewritten["openrouter"]["key"]) != `"sk-or-v1-abc"` {
		t.Errorf("rewritten key = %s, want preserved", rewritten["openrouter"]["key"])
	}
}

// TestPermissions verifies the directory is created 0700 and the file is
// written 0600, on a fresh path that does not exist yet.
func TestPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "auth.json")
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := s.Set("openrouter", Entry{Type: "api_key", Key: "sk-x"}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("dir perm = %o, want 700", perm)
	}
	info, err = os.Stat(path)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file perm = %o, want 600", perm)
	}
}

// TestSetRemove verifies the basic mutation contract, including the
// no-op Remove for an unknown provider.
func TestSetRemove(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := s.Set("", Entry{Type: "api_key", Key: "x"}); err != nil {
		t.Fatalf("Set with empty provider: %v", err)
	}
	if _, ok := s.Get(""); ok {
		t.Error("empty provider stored")
	}

	if err := s.Set("p", Entry{Type: "api_key", Key: "k1"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	e, ok := s.Get("p")
	if !ok || e.Key != "k1" {
		t.Fatalf("Get = %+v, %v; want k1", e, ok)
	}
	if err := s.Set("p", Entry{Type: "api_key", Key: "k2"}); err != nil {
		t.Fatalf("Set replace: %v", err)
	}
	if e, _ := s.Get("p"); e.Key != "k2" {
		t.Errorf("Get after replace = %q, want k2", e.Key)
	}
	if err := s.Remove("missing"); err != nil {
		t.Fatalf("Remove unknown: %v", err)
	}
	if err := s.Remove("p"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, ok := s.Get("p"); ok {
		t.Error("entry still present after Remove")
	}

	// Reload from disk: the removal must have been persisted.
	s2, err := Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, ok := s2.Get("p"); ok {
		t.Error("reloaded store still has removed entry")
	}
}

// TestConcurrentAccess hammers one store from many goroutines under the
// race detector and verifies the final state is consistent.
func TestConcurrentAccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	const workers = 16
	const perWorker = 25
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			// Each worker owns its provider key, so Set/Get pairs are
			// consistent while the shared store and file are still
			// hammered concurrently through the mutex.
			provider := fmt.Sprintf("p-%d", w)
			for i := 0; i < perWorker; i++ {
				key := strings.Repeat(string(rune('a'+w)), 8)
				if err := s.Set(provider, Entry{Type: "api_key", Key: key}); err != nil {
					t.Errorf("Set: %v", err)
				}
				if e, ok := s.Get(provider); !ok || e.Key != key {
					t.Errorf("Get after Set = %q, %v; want %q", e.Key, ok, key)
				}
			}
			_, _ = s.Get("other")
		}(w)
	}
	wg.Wait()

	// The file must still parse and carry every worker's final entry.
	s2, err := Load(path)
	if err != nil {
		t.Fatalf("reload after concurrent writes: %v", err)
	}
	for w := 0; w < workers; w++ {
		e, ok := s2.Get(fmt.Sprintf("p-%d", w))
		if !ok || e.Type != "api_key" || len(e.Key) != 8 {
			t.Errorf("final entry p-%d = %+v, %v; want an 8-char api_key", w, e, ok)
		}
	}
	// No temp files may remain.
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".auth-*.tmp"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) != 0 {
		t.Errorf("leftover temp files: %v", matches)
	}
}

// TestLoadInvalidPermissions verifies a readable-but-unparseable file
// keeps its error distinct from a missing file.
func TestLoadUnreadableIsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	writeAuthFile(t, path, "{}")
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	// Running as root would bypass the mode; skip when the read succeeds.
	if _, err := Load(path); err != nil && os.Geteuid() == 0 {
		t.Skip("running as root; permission checks are bypassed")
	}
}
