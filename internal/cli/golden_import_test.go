package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// goldenPiV3Rel is the committed sanitized Pi 0.84.2 session fixture
// shared by the internal/session and internal/sessionimport golden
// tests: 69 entries (67 messages, one thinking_level_change, one
// model_change) in a single parentId chain.
const goldenPiV3Rel = "../session/testdata/pi-v3/session-basic.jsonl"

// TestRunImportGoldenPiV3Fixture runs the full CLI import path against
// the real-shaped Pi session fixture: it must succeed, print the
// destination and the per-type stats, land the file at the canonical
// Store location, and re-import idempotently.
func TestRunImportGoldenPiV3Fixture(t *testing.T) {
	src, err := filepath.Abs(goldenPiV3Rel)
	if err != nil {
		t.Fatal(err)
	}
	srcData, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read fixture %s: %v", goldenPiV3Rel, err)
	}

	sessDir := t.TempDir()
	var stdout, stderr bytes.Buffer
	if err := run([]string{"import", src, "--session-dir", sessDir}, testDeps("", &stdout, &stderr)); err != nil {
		t.Fatalf("import: %v (stderr %q)", err, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "imported ") {
		t.Errorf("stdout = %q, want the destination line", out)
	}
	if !strings.Contains(out, "entries: 69") {
		t.Errorf("stdout = %q, want the entry count", out)
	}
	for _, want := range []string{"message: 67", "model_change: 1", "thinking_level_change: 1"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout = %q, want %q in the per-type stats", out, want)
		}
	}
	if strings.Contains(out, "opaque:") {
		t.Errorf("stdout = %q, fixture has no opaque entries", out)
	}

	// The destination is the canonical Store location and is
	// byte-identical to the source.
	dest := filepath.Join(sessDir, "--var-home-example-project--",
		"2026-08-24T10-14-35-177Z_0196b87c-7a2b-7000-8000-0000000000a1.jsonl")
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("imported destination %q missing: %v", dest, err)
	}
	if !bytes.Equal(got, srcData) {
		t.Error("imported bytes differ from the source fixture")
	}

	// Re-importing the same source is idempotent.
	stdout.Reset()
	stderr.Reset()
	if err := run([]string{"import", src, "--session-dir", sessDir}, testDeps("", &stdout, &stderr)); err != nil {
		t.Fatalf("re-import: %v (stderr %q)", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "idempotent") {
		t.Errorf("re-import stdout = %q, want the idempotent marker", stdout.String())
	}
}
