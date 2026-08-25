package sessionimport

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/session"
)

// importFixture is a sanitized Pi session covering every known entry type,
// an unknown future type, and a branch (aaaa00ff is a second child of
// aaaa0003, alongside aaaa0004).
var importFixture = []string{
	`{"type":"session","version":3,"id":"0196b87c-7a2b-7000-8000-000000000001","timestamp":"2026-08-25T10:00:00.000Z","cwd":"/tmp/imports/project"}`,
	`{"type":"message","id":"aaaa0001","parentId":null,"timestamp":"2026-08-25T10:00:01.000Z","message":{"role":"user","content":"hello smidja","timestamp":1000}}`,
	`{"type":"message","id":"aaaa0002","parentId":"aaaa0001","timestamp":"2026-08-25T10:00:02.000Z","message":{"role":"assistant","content":[{"type":"text","text":"ok"},{"type":"toolCall","id":"tc-1","name":"bash","arguments":{"command":"ls"}}],"api":"openai-completions","provider":"openrouter","model":"stealth/ox-alpha","responseId":"resp-1","usage":{"input":10,"output":20,"cacheRead":5,"cacheWrite":0,"totalTokens":30,"cost":{"input":0.1,"output":0.2,"cacheRead":0.05,"cacheWrite":0,"total":0.35}},"stopReason":"toolUse","timestamp":2000}}`,
	`{"type":"message","id":"aaaa0003","parentId":"aaaa0002","timestamp":"2026-08-25T10:00:03.000Z","message":{"role":"toolResult","toolCallId":"tc-1","toolName":"bash","content":[{"type":"text","text":"ok"}],"isError":false,"timestamp":3000}}`,
	`{"type":"thinking_level_change","id":"aaaa0004","parentId":"aaaa0003","timestamp":"2026-08-25T10:00:04.000Z","thinkingLevel":"high"}`,
	`{"type":"message","id":"aaaa00ff","parentId":"aaaa0003","timestamp":"2026-08-25T10:00:04.500Z","message":{"role":"user","content":"branch","timestamp":4500}}`,
	`{"type":"model_change","id":"aaaa0005","parentId":"aaaa0004","timestamp":"2026-08-25T10:00:05.000Z","provider":"openrouter","modelId":"deepseek/deepseek-v4-pro-0813"}`,
	`{"type":"compaction","id":"aaaa0006","parentId":"aaaa0005","timestamp":"2026-08-25T10:00:06.000Z","summary":"summarized prefix","firstKeptEntryId":"aaaa0003","tokensBefore":1234,"details":{"version":2,"pruned":true},"usage":{"input":10,"output":20,"cacheRead":0,"cacheWrite":0,"totalTokens":30,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"fromHook":true}`,
	`{"type":"branch_summary","id":"aaaa0007","parentId":"aaaa0006","timestamp":"2026-08-25T10:00:07.000Z","fromId":"aaaa0001","summary":"abandoned path"}`,
	`{"type":"custom","id":"aaaa0008","parentId":"aaaa0007","timestamp":"2026-08-25T10:00:08.000Z","customType":"context-hygiene-pruned","data":{"toolCallIds":["x"]}}`,
	`{"type":"custom_message","id":"aaaa0009","parentId":"aaaa0008","timestamp":"2026-08-25T10:00:09.000Z","customType":"my-ext","content":"note","display":true,"details":{"k":"v"}}`,
	`{"type":"label","id":"aaaa0010","parentId":"aaaa0009","timestamp":"2026-08-25T10:00:10.000Z","targetId":"aaaa0001","label":"checkpoint"}`,
	`{"type":"session_info","id":"aaaa0011","parentId":"aaaa0010","timestamp":"2026-08-25T10:00:11.000Z","name":"Imported session"}`,
	`{"type":"future_entry","id":"ff000001","parentId":"aaaa0001","timestamp":"2026-08-25T10:00:12.000Z","futureField":{"nested":[1,2],"flag":true}}`,
}

// canonicalDestName mirrors the Store naming for the fixture header.
const canonicalDestName = "2026-08-25T10-00-00-000Z_0196b87c-7a2b-7000-8000-000000000001.jsonl"

func writeSource(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pi-session.jsonl")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestImportByteExactAndStats(t *testing.T) {
	srcData := []byte(strings.Join(importFixture, "\n") + "\n")
	src := writeSource(t, srcData)

	store, err := session.NewStore(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	dest, stats, err := Import(src, store)
	if err != nil {
		t.Fatal(err)
	}

	// The imported file is byte-identical to the source: raw lines were
	// copied, never re-marshaled.
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, srcData) {
		t.Errorf("imported bytes differ:\n got %s\nwant %s", got, srcData)
	}

	// Destination is the canonical name under the munged cwd directory.
	wantDir := filepath.Join(store.Root(), "--tmp-imports-project--")
	if filepath.Dir(dest) != wantDir {
		t.Errorf("dest dir = %q, want %q", filepath.Dir(dest), wantDir)
	}
	if filepath.Base(dest) != canonicalDestName {
		t.Errorf("dest name = %q, want %q", filepath.Base(dest), canonicalDestName)
	}

	// File is 0600.
	fi, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("dest perms = %o, want 600", perm)
	}

	// Stats: 13 entries, one opaque future type, per-type counts.
	if stats.Entries != 13 {
		t.Errorf("Entries = %d, want 13", stats.Entries)
	}
	if stats.Opaque != 1 {
		t.Errorf("Opaque = %d, want 1", stats.Opaque)
	}
	if stats.Idempotent {
		t.Error("Idempotent = true on first import")
	}
	wantPerType := map[string]int{
		"message":               4,
		"thinking_level_change": 1,
		"model_change":          1,
		"compaction":            1,
		"branch_summary":        1,
		"custom":                1,
		"custom_message":        1,
		"label":                 1,
		"session_info":          1,
		"future_entry":          1,
	}
	for typ, want := range wantPerType {
		if stats.PerType[typ] != want {
			t.Errorf("PerType[%q] = %d, want %d", typ, stats.PerType[typ], want)
		}
	}
	if len(stats.PerType) != len(wantPerType) {
		t.Errorf("PerType has %d types, want %d: %v", len(stats.PerType), len(wantPerType), stats.PerType)
	}

	// The imported file loads as a session with a branch.
	l, err := session.Load(dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entries()) != 13 {
		t.Errorf("loaded entries = %d, want 13", len(l.Entries()))
	}
	if children := l.Children("aaaa0003"); len(children) != 2 {
		t.Errorf("children of aaaa0003 = %d, want 2 (branch)", len(children))
	}
}

func TestImportIdempotent(t *testing.T) {
	srcData := []byte(strings.Join(importFixture, "\n") + "\n")
	src := writeSource(t, srcData)
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

	// The file is untouched by the second import, and no temp files remain.
	after, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("dest changed between imports")
	}
	assertNoTempFiles(t, filepath.Dir(first))
}

func TestImportConflict(t *testing.T) {
	srcData := []byte(strings.Join(importFixture, "\n") + "\n")
	src := writeSource(t, srcData)
	store, err := session.NewStore(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	dir, err := store.DirForCwd("/tmp/imports/project")
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, canonicalDestName)
	clobber := []byte("{\"type\":\"session\",\"version\":3,\"id\":\"other\",\"timestamp\":\"2026-08-25T10:00:00.000Z\",\"cwd\":\"/tmp/imports/project\"}\n")
	if err := os.WriteFile(dest, clobber, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := Import(src, store); !errors.Is(err, ErrConflict) {
		t.Fatalf("Import: err = %v, want ErrConflict", err)
	}
	// The existing file is never overwritten.
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, clobber) {
		t.Error("conflict overwrote the existing destination")
	}
	assertNoTempFiles(t, dir)
}

func TestImportMalformedInput(t *testing.T) {
	store, err := session.NewStore(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"empty":         "",
		"garbage":       "not json at all\n",
		"message first": `{"type":"message","id":"x","parentId":null,"timestamp":"2026-08-25T10:00:00.000Z","message":{}}` + "\n",
		"wrong type":    `{"type":"not_session","id":"x"}` + "\n",
		"id not str":    `{"type":"session","id":42}` + "\n",
		"missing cwd":   `{"type":"session","version":3,"id":"x","timestamp":"2026-08-25T10:00:00.000Z"}` + "\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			src := writeSource(t, []byte(content))
			if _, _, err := Import(src, store); !errors.Is(err, ErrInvalidSource) {
				t.Errorf("Import: err = %v, want ErrInvalidSource", err)
			}
		})
	}

	// A missing source file is an I/O error, not an invalid-source error.
	if _, _, err := Import(filepath.Join(t.TempDir(), "nope.jsonl"), store); err == nil {
		t.Error("Import(missing source): want error, got nil")
	}

	// A nil store is rejected up front.
	src := writeSource(t, []byte(strings.Join(importFixture, "\n")+"\n"))
	if _, _, err := Import(src, nil); err == nil {
		t.Error("Import(nil store): want error, got nil")
	}
}

func TestImportSkipsBlankAndMalformedLines(t *testing.T) {
	header := importFixture[0]
	e1 := importFixture[1]
	e2 := importFixture[2]
	srcData := []byte(header + "\n\n" + "corrupt line\n" + e1 + "\n" + "{\"type\":\n" + e2 + "\n")
	src := writeSource(t, srcData)

	store, err := session.NewStore(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	dest, stats, err := Import(src, store)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte(header + "\n" + e1 + "\n" + e2 + "\n")
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("imported bytes:\n got %s\nwant %s", got, want)
	}
	if stats.Entries != 2 {
		t.Errorf("Entries = %d, want 2", stats.Entries)
	}
	if stats.PerType["message"] != 2 {
		t.Errorf("PerType[message] = %d, want 2", stats.PerType["message"])
	}
}

func TestImportPreservesLineEndings(t *testing.T) {
	// CRLF source: the imported file keeps the CRLF bytes verbatim.
	crlf := []byte(strings.Join(importFixture, "\r\n") + "\r\n")
	store, err := session.NewStore(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	dest, _, err := Import(writeSource(t, crlf), store)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, crlf) {
		t.Errorf("CRLF not preserved:\n got %q", got)
	}

	// A final line without a trailing newline is preserved too, in a
	// fresh store so both imports target a clean destination.
	noNL := []byte(strings.Join(importFixture, "\n"))
	store2, err := session.NewStore(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	dest2, _, err := Import(writeSource(t, noNL), store2)
	if err != nil {
		t.Fatal(err)
	}
	got2, err := os.ReadFile(dest2)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got2, noNL) {
		t.Errorf("missing trailing newline not preserved:\n got %q", got2)
	}
}

func TestImportHeaderOnly(t *testing.T) {
	header := `{"type":"session","version":3,"id":"hdr-only","timestamp":"2026-08-25T14:00:00.000Z","cwd":"/tmp/hdr"}`
	src := writeSource(t, []byte(header+"\n"))
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
	if string(got) != header+"\n" {
		t.Errorf("imported bytes = %q, want header only", got)
	}
	if stats.Entries != 0 {
		t.Errorf("Entries = %d, want 0", stats.Entries)
	}
}

func TestImportParentSessionPreserved(t *testing.T) {
	parent := "/tmp/other/2026-01-01T00-00-00-000Z_01234567-89ab-4cde-f012-3456789abcd.jsonl"
	lines := []string{
		`{"type":"session","version":3,"id":"fork-session","timestamp":"2026-08-25T15:00:00.000Z","cwd":"/tmp/fork","parentSession":"` + parent + `"}`,
		`{"type":"message","id":"f0000001","parentId":null,"timestamp":"2026-08-25T15:00:01.000Z","message":{"role":"user","content":"x","timestamp":1}}`,
	}
	srcData := []byte(strings.Join(lines, "\n") + "\n")
	src := writeSource(t, srcData)
	store, err := session.NewStore(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	dest, _, err := Import(src, store)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, srcData) {
		t.Errorf("imported bytes differ:\n got %s\nwant %s", got, srcData)
	}
	l, err := session.Load(dest)
	if err != nil {
		t.Fatal(err)
	}
	if l.Header().ParentSession == nil || *l.Header().ParentSession != parent {
		t.Errorf("parentSession = %v, want %q", l.Header().ParentSession, parent)
	}
}

// assertNoTempFiles fails the test when any import temp file remains in dir.
func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".smidja-import-") {
			t.Errorf("leftover temp file %q in %q", e.Name(), dir)
		}
	}
}
