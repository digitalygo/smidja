package sessionimport

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/digitalygo/smidja/internal/session"
)

const goldenPiV3Rel = "../session/testdata/pi-v3/session-basic.jsonl"

var goldenPiV3Stats = map[string]int{
	"message":               67,
	"thinking_level_change": 1,
	"model_change":          1,
}

const goldenPiV3DestName = "2026-08-24T10-14-35-177Z_0196b87c-7a2b-7000-8000-0000000000a1.jsonl"

func TestImportGoldenPiV3FixtureByteExact(t *testing.T) {
	src, err := filepath.Abs(goldenPiV3Rel)
	if err != nil {
		t.Fatal(err)
	}
	srcData, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read fixture %s: %v", goldenPiV3Rel, err)
	}

	store, err := session.NewStore(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	dest, stats, err := Import(src, store)
	if err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, srcData) {
		t.Errorf("imported bytes differ from source (len %d vs %d)", len(got), len(srcData))
	}

	wantDir := filepath.Join(store.Root(), "--var-home-example-project--")
	if filepath.Dir(dest) != wantDir {
		t.Errorf("dest dir = %q, want %q", filepath.Dir(dest), wantDir)
	}
	if filepath.Base(dest) != goldenPiV3DestName {
		t.Errorf("dest name = %q, want %q", filepath.Base(dest), goldenPiV3DestName)
	}

	fi, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("dest perms = %o, want 600", perm)
	}

	if stats.Entries != 69 {
		t.Errorf("Entries = %d, want 69", stats.Entries)
	}
	if stats.Opaque != 0 {
		t.Errorf("Opaque = %d, want 0", stats.Opaque)
	}
	if stats.Idempotent {
		t.Error("Idempotent = true on first import")
	}
	if len(stats.PerType) != len(goldenPiV3Stats) {
		t.Errorf("PerType has %d types, want %d: %v", len(stats.PerType), len(goldenPiV3Stats), stats.PerType)
	}
	for typ, want := range goldenPiV3Stats {
		if stats.PerType[typ] != want {
			t.Errorf("PerType[%q] = %d, want %d", typ, stats.PerType[typ], want)
		}
	}

	l, err := session.Load(dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entries()) != 69 {
		t.Errorf("loaded entries = %d, want 69", len(l.Entries()))
	}
	if roots := l.Roots(); len(roots) != 1 {
		t.Errorf("roots = %d, want 1", len(roots))
	}
}

func TestImportGoldenPiV3FixtureIdempotent(t *testing.T) {
	src, err := filepath.Abs(goldenPiV3Rel)
	if err != nil {
		t.Fatal(err)
	}
	srcData, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read fixture %s: %v", goldenPiV3Rel, err)
	}

	store, err := session.NewStore(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	first, stats1, err := Import(src, store)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	second, stats2, err := Import(src, store)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Errorf("second dest = %q, want %q", second, first)
	}
	if !stats2.Idempotent {
		t.Error("second import: Idempotent = false, want true")
	}
	if stats1.Idempotent {
		t.Error("first import: Idempotent = true, want false")
	}
	if stats2.Entries != 69 {
		t.Errorf("second import Entries = %d, want 69", stats2.Entries)
	}

	after, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("dest changed between imports")
	}
	if !bytes.Equal(after, srcData) {
		t.Error("dest no longer matches the source after re-import")
	}
	assertNoTempFiles(t, filepath.Dir(first))
}
