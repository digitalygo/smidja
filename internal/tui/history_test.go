package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSanitizeProjectKey(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"", "default"},
		{"   ", "default"},
		{"my/project", "my_project"},
		{"/tmp/workspace", "tmp_workspace"},
		{"a:b*c?d", "a_b_c_d"},
		{"___", "default"},
		{"valid-name_1.2", "valid-name_1.2"},
	}
	for _, tc := range cases {
		if got := sanitizeProjectKey(tc.input); got != tc.want {
			t.Fatalf("sanitizeProjectKey(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
	long := make([]byte, 200)
	for i := range long {
		long[i] = 'a'
	}
	if got := sanitizeProjectKey(string(long)); len(got) != 128 {
		t.Fatalf("long key length = %d, want 128", len(got))
	}
}

func TestHistoryFilePath(t *testing.T) {
	home := t.TempDir()
	got := HistoryFilePath(home, "/tmp/proj")
	want := filepath.Join(home, ".smidja", "prompt-history", "tmp_proj.json")
	if got != want {
		t.Fatalf("HistoryFilePath = %q, want %q", got, want)
	}
	if got := HistoryFilePath(home, ""); got != filepath.Join(home, ".smidja", "prompt-history", "default.json") {
		t.Fatalf("empty project path = %q", got)
	}
}

func TestHistoryStoreAddLoadSave(t *testing.T) {
	home := t.TempDir()
	store := NewHistoryStore(home, "/tmp/proj-a")
	if err := store.Load(); err != nil {
		t.Fatalf("load missing = %v", err)
	}
	if len(store.Entries()) != 0 {
		t.Fatalf("empty store entries = %v", store.Entries())
	}
	if store.Add("") {
		t.Fatalf("empty add should return false")
	}
	if store.Add("   ") {
		t.Fatalf("whitespace add should return false")
	}
	if !store.Add("first prompt") {
		t.Fatalf("add should return true")
	}
	if store.Add("first prompt") {
		t.Fatalf("consecutive duplicate should return false")
	}
	if !store.Add("second prompt") {
		t.Fatalf("second add should return true")
	}
	entries := store.Entries()
	if len(entries) != 2 || entries[0] != "second prompt" || entries[1] != "first prompt" {
		t.Fatalf("entries = %q", entries)
	}
	if err := store.Save(); err != nil {
		t.Fatalf("save = %v", err)
	}
	second := NewHistoryStore(home, "/tmp/proj-a")
	if err := second.Load(); err != nil {
		t.Fatalf("reload = %v", err)
	}
	reloaded := second.Entries()
	if len(reloaded) != 2 || reloaded[0] != "second prompt" {
		t.Fatalf("reloaded = %q", reloaded)
	}
	other := NewHistoryStore(home, "/tmp/proj-b")
	if err := other.Load(); err != nil {
		t.Fatalf("other load = %v", err)
	}
	if len(other.Entries()) != 0 {
		t.Fatalf("per-project isolation failed: %q", other.Entries())
	}
	if other.Path() == store.Path() {
		t.Fatalf("per-project paths should differ")
	}
}

func TestHistoryStoreCorruptAndEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.json")
	store := NewHistoryStoreAtPath(path)
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatalf("write = %v", err)
	}
	if err := store.Load(); err == nil {
		t.Fatalf("corrupt load should error")
	}
	if err := os.WriteFile(path, []byte("  "), 0o600); err != nil {
		t.Fatalf("write = %v", err)
	}
	if err := store.Load(); err != nil {
		t.Fatalf("empty load = %v", err)
	}
	if len(store.Entries()) != 0 {
		t.Fatalf("empty entries = %v", store.Entries())
	}
	if err := os.WriteFile(path, []byte(`["a", "", "  ", "b"]`), 0o600); err != nil {
		t.Fatalf("write = %v", err)
	}
	if err := store.Load(); err != nil {
		t.Fatalf("load = %v", err)
	}
	if entries := store.Entries(); len(entries) != 2 || entries[0] != "a" || entries[1] != "b" {
		t.Fatalf("filtered entries = %q", entries)
	}
}

func TestHistoryStoreMaxEntries(t *testing.T) {
	dir := t.TempDir()
	store := NewHistoryStoreAtPath(filepath.Join(dir, "h.json"))
	store.SetMaxEntries(3)
	for _, text := range []string{"one", "two", "three", "four"} {
		store.Add(text)
	}
	entries := store.Entries()
	if len(entries) != 3 || entries[0] != "four" || entries[2] != "two" {
		t.Fatalf("max entries = %q", entries)
	}
	store.SetMaxEntries(1)
	if entries := store.Entries(); len(entries) != 1 {
		t.Fatalf("shrink entries = %q", entries)
	}
	store.SetMaxEntries(0)
	store.Clear()
	if len(store.Entries()) != 0 {
		t.Fatalf("clear failed")
	}
	added, err := store.AddAndSave("hello")
	if err != nil || !added {
		t.Fatalf("AddAndSave = %v %v", added, err)
	}
	added, err = store.AddAndSave("hello")
	if err != nil || added {
		t.Fatalf("duplicate AddAndSave = %v %v", added, err)
	}
}

func TestHistoryStoreSaveCreatesDirs(t *testing.T) {
	home := t.TempDir()
	store := NewHistoryStore(home, "proj")
	store.Add("entry one")
	if err := store.Save(); err != nil {
		t.Fatalf("save = %v", err)
	}
	data, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatalf("read = %v", err)
	}
	if len(data) == 0 {
		t.Fatalf("saved file empty")
	}
}
