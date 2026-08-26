package session

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var treeFixture = []string{
	`{"type":"session","version":3,"id":"tree-session","timestamp":"2026-08-25T11:00:00.000Z","cwd":"/tmp/tree"}`,
	`{"type":"message","id":"b0000001","parentId":null,"timestamp":"2026-08-25T11:00:01.000Z","message":{"role":"user","content":"root","timestamp":1}}`,
	`{"type":"message","id":"b0000002","parentId":"b0000001","timestamp":"2026-08-25T11:00:02.000Z","message":{"role":"assistant","content":[],"api":"a","provider":"p","model":"m","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":2}}`,
	`{"type":"thinking_level_change","id":"b0000003","parentId":"b0000001","timestamp":"2026-08-25T11:00:03.000Z","thinkingLevel":"high"}`,
	`{"type":"model_change","id":"b0000004","parentId":"b0000003","timestamp":"2026-08-25T11:00:04.000Z","provider":"openrouter","modelId":"x/y"}`,
	`{"type":"message","id":"b0000005","parentId":"b0000002","timestamp":"2026-08-25T11:00:05.000Z","message":{"role":"toolResult","toolCallId":"t","toolName":"bash","content":[{"type":"text","text":"out"}],"isError":false,"timestamp":5}}`,
}

func TestLoaderTreeAndBranching(t *testing.T) {
	l, err := Load(writeFixture(t, treeFixture))
	if err != nil {
		t.Fatal(err)
	}

	if l.Path() == "" {
		t.Error("Path is empty")
	}
	if l.Header().ID != "tree-session" {
		t.Errorf("header id = %q", l.Header().ID)
	}

	if l.Leaf() == nil {
		t.Fatal("Leaf is nil")
	}
	leafID, _, _ := envelopeOf(l.Leaf())
	if leafID != "b0000005" {
		t.Errorf("leaf = %q, want b0000005", leafID)
	}

	roots := l.Roots()
	if len(roots) != 1 {
		t.Fatalf("roots = %d, want 1", len(roots))
	}
	rootID, _, _ := envelopeOf(roots[0])
	if rootID != "b0000001" {
		t.Errorf("root = %q, want b0000001", rootID)
	}

	children := l.Children("b0000001")
	if len(children) != 2 {
		t.Fatalf("children of root = %d, want 2", len(children))
	}
	c0, _, _ := envelopeOf(children[0])
	c1, _, _ := envelopeOf(children[1])
	if c0 != "b0000002" || c1 != "b0000003" {
		t.Errorf("children order = %q, %q; want b0000002, b0000003", c0, c1)
	}

	active, err := l.ActiveBranch()
	if err != nil {
		t.Fatal(err)
	}
	if ids := entryIDs(active); strings.Join(ids, ",") != "b0000001,b0000002,b0000005" {
		t.Errorf("active branch = %v", ids)
	}

	side, err := l.Branch("b0000004")
	if err != nil {
		t.Fatal(err)
	}
	if ids := entryIDs(side); strings.Join(ids, ",") != "b0000001,b0000003,b0000004" {
		t.Errorf("side branch = %v", ids)
	}

	if _, ok := l.Get("b0000005"); !ok {
		t.Error("Get(b0000005) not found")
	}
	if _, ok := l.Get("missing"); ok {
		t.Error("Get(missing) found")
	}
}

func TestLoaderBranchErrors(t *testing.T) {
	l, err := Load(writeFixture(t, treeFixture))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Branch("nope"); err == nil {
		t.Error("Branch(unknown): want error, got nil")
	}

	cycle := []string{
		treeFixture[0],
		`{"type":"message","id":"c0000001","parentId":"c0000002","timestamp":"2026-08-25T11:01:01.000Z","message":{"role":"user","content":"a","timestamp":1}}`,
		`{"type":"message","id":"c0000002","parentId":"c0000001","timestamp":"2026-08-25T11:01:02.000Z","message":{"role":"user","content":"b","timestamp":2}}`,
	}
	cl, err := Load(writeFixture(t, cycle))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cl.Branch("c0000001"); err == nil {
		t.Error("Branch on cycle: want error, got nil")
	}
}

func TestLoaderBuildContextEntriesCompaction(t *testing.T) {
	lines := []string{
		`{"type":"session","version":3,"id":"comp-session","timestamp":"2026-08-25T12:00:00.000Z","cwd":"/tmp/comp"}`,
		`{"type":"message","id":"c0000001","parentId":null,"timestamp":"2026-08-25T12:00:01.000Z","message":{"role":"user","content":"first","timestamp":1}}`,
		`{"type":"message","id":"c0000002","parentId":"c0000001","timestamp":"2026-08-25T12:00:02.000Z","message":{"role":"assistant","content":[],"api":"a","provider":"p","model":"m","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":2}}`,
		`{"type":"message","id":"c0000003","parentId":"c0000002","timestamp":"2026-08-25T12:00:03.000Z","message":{"role":"toolResult","toolCallId":"t","toolName":"bash","content":[{"type":"text","text":"out"}],"isError":false,"timestamp":3}}`,
		`{"type":"compaction","id":"c0000004","parentId":"c0000003","timestamp":"2026-08-25T12:00:04.000Z","summary":"sum","firstKeptEntryId":"c0000003","tokensBefore":100}`,
		`{"type":"message","id":"c0000005","parentId":"c0000004","timestamp":"2026-08-25T12:00:05.000Z","message":{"role":"user","content":"second","timestamp":5}}`,
		`{"type":"message","id":"c0000006","parentId":"c0000005","timestamp":"2026-08-25T12:00:06.000Z","message":{"role":"assistant","content":[],"api":"a","provider":"p","model":"m","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":6}}`,
	}
	l, err := Load(writeFixture(t, lines))
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := l.BuildContextEntries()
	if err != nil {
		t.Fatal(err)
	}
	if ids := entryIDs(ctx); strings.Join(ids, ",") != "c0000004,c0000003,c0000005,c0000006" {
		t.Errorf("context entries = %v", ids)
	}
}

func TestLoaderBuildContextEntriesNoCompaction(t *testing.T) {
	l, err := Load(writeFixture(t, treeFixture))
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := l.BuildContextEntries()
	if err != nil {
		t.Fatal(err)
	}
	if ids := entryIDs(ctx); strings.Join(ids, ",") != "b0000001,b0000002,b0000005" {
		t.Errorf("context entries = %v", ids)
	}
}

func TestLoaderSkipsBlankAndMalformedLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	var b strings.Builder
	for i, line := range treeFixture {
		if i == 2 || i == 4 {
			b.WriteString("this is not json\n")
			continue
		}
		if i == 3 {
			b.WriteString("\n")
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entries()) != 3 {
		t.Errorf("entries = %d, want 3", len(l.Entries()))
	}
	if ids := entryIDs(l.Entries()); strings.Join(ids, ",") !=
		"b0000001,b0000003,b0000005" {
		t.Errorf("entry order = %v", ids)
	}
}

func TestLoaderCRLFAndNoTrailingNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	var b strings.Builder
	for i, line := range treeFixture {
		b.WriteString(line)
		if i == len(treeFixture)-1 {
			b.WriteString("\r")
		} else {
			b.WriteString("\r\n")
		}
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entries()) != len(treeFixture)-1 {
		t.Errorf("entries = %d, want %d", len(l.Entries()), len(treeFixture)-1)
	}
}

func TestLoadInvalidFiles(t *testing.T) {
	cases := map[string][]byte{
		"empty":       {},
		"no header":   []byte(`{"type":"message","id":"x","parentId":null,"timestamp":"2026-08-25T00:00:00.000Z","message":{}}`),
		"array first": []byte(`[1,2]\n`),
		"wrong type":  []byte(`{"type":"not_session","id":"x"}`),
		"id not str":  []byte(`{"type":"session","id":42}`),
		"not json":    []byte(`garbage\n`),
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "session.jsonl")
			if err := os.WriteFile(path, content, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); !errors.Is(err, ErrNotASession) {
				t.Errorf("Load: err = %v, want ErrNotASession", err)
			}
		})
	}
}

func TestLoadHeaderOnlySession(t *testing.T) {
	path := writeFixture(t, []string{
		`{"type":"session","version":3,"id":"hdr-only","timestamp":"2026-08-25T00:00:00.000Z","cwd":"/tmp"}`,
	})
	l, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entries()) != 0 {
		t.Errorf("entries = %d, want 0", len(l.Entries()))
	}
	if l.Leaf() != nil {
		t.Errorf("leaf = %v, want nil", l.Leaf())
	}
	ctx, err := l.BuildContextEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(ctx) != 0 {
		t.Errorf("context entries = %d, want 0", len(ctx))
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.jsonl")); err == nil {
		t.Error("Load(missing): want error, got nil")
	}
}

func entryIDs(entries []Entry) []string {
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		id, _, _ := envelopeOf(e)
		ids = append(ids, id)
	}
	return ids
}
