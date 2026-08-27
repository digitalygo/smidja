package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadStrictRejectsMalformedInteriorWithLineNumber(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	var b strings.Builder
	b.WriteString(treeFixture[0])
	b.WriteString("\n")
	b.WriteString(treeFixture[1])
	b.WriteString("\n")
	b.WriteString("broken json line\n")
	b.WriteString(treeFixture[2])
	b.WriteString("\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadWithOptions(path, LoadOptions{Strict: true}); err == nil {
		t.Fatal("strict load of malformed interior: want error, got nil")
	} else if !strings.Contains(err.Error(), "line 3") {
		t.Fatalf("strict error %q must name line 3", err)
	}
	l, err := LoadWithOptions(path, LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if ids := entryIDs(l.Entries()); strings.Join(ids, ",") != "b0000001,b0000002" {
		t.Errorf("lenient entries = %v, want b0000001,b0000002", ids)
	}
}

func TestLoadStrictRecoversTrailingPartialLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	data := strings.Join(treeFixture, "\n") + "\n" + `{"type":"message","id":"b0000006","parentId":"b0000005","timestamp":"2026-08-25T11:00:06.000Z","message":{"role":"user","content":"truncated","ti`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := LoadWithOptions(path, LoadOptions{Strict: true})
	if err != nil {
		t.Fatalf("strict load must recover a trailing partial line: %v", err)
	}
	if ids := entryIDs(l.Entries()); strings.Join(ids, ",") != "b0000001,b0000002,b0000003,b0000004,b0000005" {
		t.Errorf("entries = %v, want b0000001..b0000005", ids)
	}
}

func TestLoadStrictMalformedBeforeHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	data := "not json\n" + strings.Join(treeFixture, "\n") + "\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadWithOptions(path, LoadOptions{Strict: true}); err == nil {
		t.Fatal("strict load of malformed line before header: want error")
	} else if !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("strict error %q must name line 1", err)
	}
	l, err := LoadWithOptions(path, LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entries()) != len(treeFixture)-1 {
		t.Errorf("lenient entries = %d, want %d", len(l.Entries()), len(treeFixture)-1)
	}
}

func TestLoadStrictBlankLinesTolerated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	var b strings.Builder
	for i, line := range treeFixture {
		b.WriteString(line)
		b.WriteString("\n")
		if i == 1 {
			b.WriteString("\n")
		}
		if i == 3 {
			b.WriteString("  \n")
		}
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := LoadWithOptions(path, LoadOptions{Strict: true})
	if err != nil {
		t.Fatalf("strict load with blank lines: %v", err)
	}
	if len(l.Entries()) != len(treeFixture)-1 {
		t.Errorf("entries = %d, want %d", len(l.Entries()), len(treeFixture)-1)
	}
}

func TestLoadStrictDuplicateSessionLinesIgnored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	var b strings.Builder
	b.WriteString(treeFixture[0])
	b.WriteString("\n")
	b.WriteString(treeFixture[0])
	b.WriteString("\n")
	b.WriteString(treeFixture[1])
	b.WriteString("\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := LoadWithOptions(path, LoadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	if ids := entryIDs(l.Entries()); strings.Join(ids, ",") != "b0000001" {
		t.Errorf("entries = %v, want b0000001", ids)
	}
}

func TestLoadStrictValidFilePasses(t *testing.T) {
	l, err := LoadWithOptions(writeFixture(t, treeFixture), LoadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entries()) != len(treeFixture)-1 {
		t.Errorf("entries = %d, want %d", len(l.Entries()), len(treeFixture)-1)
	}
}
