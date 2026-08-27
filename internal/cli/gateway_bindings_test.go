package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadBindingsMissingFile(t *testing.T) {
	b, err := loadBindings(filepath.Join(t.TempDir(), "bindings.json"))
	if err != nil {
		t.Fatalf("loadBindings: %v", err)
	}
	if _, ok := b.lookup("k"); ok {
		t.Fatal("missing file must yield no bindings")
	}
}

func TestLoadBindingsEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bindings.json")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := loadBindings(path)
	if err != nil {
		t.Fatalf("loadBindings: %v", err)
	}
	if _, ok := b.lookup("k"); ok {
		t.Fatal("empty file must yield no bindings")
	}
}

func TestLoadBindingsCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bindings.json")
	if err := os.WriteFile(path, []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadBindings(path); err == nil {
		t.Fatal("corrupt bindings file: want error")
	}
}

func TestLoadBindingsNullObject(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bindings.json")
	if err := os.WriteFile(path, []byte("null"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := loadBindings(path)
	if err != nil {
		t.Fatalf("loadBindings: %v", err)
	}
	if _, ok := b.lookup("k"); ok {
		t.Fatal("null file must yield no bindings")
	}
	if err := b.set("k", "/s/k.jsonl"); err != nil {
		t.Fatalf("set after null load: %v", err)
	}
}

func TestLoadBindingsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bindings.json")
	if err := os.WriteFile(path, []byte(`{"web:u":"/s/a.jsonl"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := loadBindings(path)
	if err != nil {
		t.Fatalf("loadBindings: %v", err)
	}
	got, ok := b.lookup("web:u")
	if !ok || got != "/s/a.jsonl" {
		t.Fatalf("lookup = %q %v, want /s/a.jsonl true", got, ok)
	}
}

func TestBindingsSetWritesAtomicallyAndSurvivesReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bindings.json")
	b, err := loadBindings(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.set("telegram:1", "/s/one.jsonl"); err != nil {
		t.Fatal(err)
	}
	if err := b.set("telegram:2", "/s/two.jsonl"); err != nil {
		t.Fatal(err)
	}
	reloaded, err := loadBindings(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if p, ok := reloaded.lookup("telegram:1"); !ok || p != "/s/one.jsonl" {
		t.Fatalf("telegram:1 binding lost after reload: %q %v", p, ok)
	}
	if p, ok := reloaded.lookup("telegram:2"); !ok || p != "/s/two.jsonl" {
		t.Fatalf("telegram:2 binding lost after reload: %q %v", p, ok)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "" || data[len(data)-1] != '}' {
		t.Fatalf("bindings file looks truncated: %q", data)
	}
}

func TestBindingsNilReceiverLookup(t *testing.T) {
	var b *bindingStore
	if _, ok := b.lookup("k"); ok {
		t.Fatal("nil store lookup must be empty")
	}
}

func TestBindingsSetUnwritableDirFails(t *testing.T) {
	dir := t.TempDir()
	b, err := loadBindings(filepath.Join(dir, "bindings.json"))
	if err != nil {
		t.Fatalf("loadBindings: %v", err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := b.set("k", "/s/k.jsonl"); err == nil {
		t.Fatal("set under a file path: want error")
	}
}

func TestBindingsFileReadError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bindings.json")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := loadBindings(path); err == nil {
		t.Fatal("reading a directory as bindings: want error")
	}
}

func TestBindingsSetTempCreateFailure(t *testing.T) {
	orig := osCreateTemp
	osCreateTemp = func(string, string) (*os.File, error) { return nil, os.ErrPermission }
	defer func() { osCreateTemp = orig }()
	b, err := loadBindings(filepath.Join(t.TempDir(), "bindings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := b.set("k", "/s/k.jsonl"); err == nil {
		t.Fatal("temp creation failure: want error")
	}
}

func TestBindingsSetRenameFailure(t *testing.T) {
	orig := osRename
	osRename = func(string, string) error { return os.ErrPermission }
	defer func() { osRename = orig }()
	b, err := loadBindings(filepath.Join(t.TempDir(), "bindings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := b.set("k", "/s/k.jsonl"); err == nil {
		t.Fatal("rename failure: want error")
	}
	if _, ok := b.lookup("k"); !ok {
		t.Fatal("in-memory binding must survive a failed write")
	}
}
