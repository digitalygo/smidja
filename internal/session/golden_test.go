package session

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
)

const goldenPiV3Path = "testdata/pi-v3/session-basic.jsonl"

var goldenPiV3Counts = struct {
	entries              int
	perType              map[string]int
	perRole              map[string]int
	assistantWithRaw     int
	toolResultWithDetail int
}{
	entries: 69,
	perType: map[string]int{
		EntryTypeMessage:             67,
		EntryTypeThinkingLevelChange: 1,
		EntryTypeModelChange:         1,
	},
	perRole: map[string]int{
		"user":       2,
		"assistant":  31,
		"toolResult": 34,
	},
	assistantWithRaw:     31,
	toolResultWithDetail: 3,
}

func readGoldenLines(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(goldenPiV3Path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", goldenPiV3Path, err)
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func TestGoldenPiV3FixtureHeader(t *testing.T) {
	lines := readGoldenLines(t)
	if len(lines) != goldenPiV3Counts.entries+1 {
		t.Fatalf("fixture lines = %d, want %d (header + entries)", len(lines), goldenPiV3Counts.entries+1)
	}
	l, err := Load(goldenPiV3Path)
	if err != nil {
		t.Fatal(err)
	}
	hdr := l.Header()
	if hdr.Type != EntryTypeSession || hdr.Version != 3 {
		t.Errorf("header = %+v, want type session version 3", hdr)
	}
	if hdr.ID != "0196b87c-7a2b-7000-8000-0000000000a1" {
		t.Errorf("header id = %q", hdr.ID)
	}
	if hdr.Timestamp != "2026-08-24T10:14:35.177Z" {
		t.Errorf("header timestamp = %q", hdr.Timestamp)
	}
	if hdr.Cwd != "/var/home/example/project" {
		t.Errorf("header cwd = %q", hdr.Cwd)
	}
	if hdr.ParentSession != nil {
		t.Errorf("header parentSession = %v, want nil", *hdr.ParentSession)
	}
}

func TestGoldenPiV3FixtureEntryCounts(t *testing.T) {
	l, err := Load(goldenPiV3Path)
	if err != nil {
		t.Fatal(err)
	}
	entries := l.Entries()
	if len(entries) != goldenPiV3Counts.entries {
		t.Fatalf("entries = %d, want %d", len(entries), goldenPiV3Counts.entries)
	}
	perType := map[string]int{}
	perRole := map[string]int{}
	assistantWithRaw := 0
	toolResultWithDetail := 0
	for _, e := range entries {
		perType[e.EntryType()]++
		me, ok := e.(*MessageEntry)
		if !ok {
			continue
		}
		perRole[me.MessageRole()]++
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(me.Message, &payload); err != nil {
			t.Fatalf("message payload: %v", err)
		}
		if _, hasRaw := payload["rawStopReason"]; hasRaw {
			assistantWithRaw++
		}
		if _, hasDetail := payload["details"]; hasDetail {
			toolResultWithDetail++
		}
	}
	for typ, want := range goldenPiV3Counts.perType {
		if perType[typ] != want {
			t.Errorf("entries of type %q = %d, want %d", typ, perType[typ], want)
		}
	}
	if len(perType) != len(goldenPiV3Counts.perType) {
		t.Errorf("entry types = %v, want %v", perType, goldenPiV3Counts.perType)
	}
	for role, want := range goldenPiV3Counts.perRole {
		if perRole[role] != want {
			t.Errorf("messages of role %q = %d, want %d", role, perRole[role], want)
		}
	}
	if len(perRole) != len(goldenPiV3Counts.perRole) {
		t.Errorf("message roles = %v, want %v", perRole, goldenPiV3Counts.perRole)
	}
	if assistantWithRaw != goldenPiV3Counts.assistantWithRaw {
		t.Errorf("assistant messages with rawStopReason = %d, want %d", assistantWithRaw, goldenPiV3Counts.assistantWithRaw)
	}
	if toolResultWithDetail != goldenPiV3Counts.toolResultWithDetail {
		t.Errorf("toolResult messages with details = %d, want %d", toolResultWithDetail, goldenPiV3Counts.toolResultWithDetail)
	}
}

func TestGoldenPiV3FixtureTreeIntegrity(t *testing.T) {
	l, err := Load(goldenPiV3Path)
	if err != nil {
		t.Fatal(err)
	}
	entries := l.Entries()

	for i, e := range entries {
		_, parentID, _ := envelopeOf(e)
		if i == 0 {
			if parentID != nil {
				t.Errorf("entry 0 parentId = %v, want nil", *parentID)
			}
			continue
		}
		prevID, _, _ := envelopeOf(entries[i-1])
		if parentID == nil || *parentID != prevID {
			t.Errorf("entry %d parentId = %v, want %q", i, parentID, prevID)
		}
	}

	roots := l.Roots()
	if len(roots) != 1 {
		t.Fatalf("roots = %d, want 1", len(roots))
	}
	rootID, _, _ := envelopeOf(roots[0])
	if rootID != "a0000001" {
		t.Errorf("root = %q, want a0000001", rootID)
	}

	leafID, _, _ := envelopeOf(l.Leaf())
	lastID, _, _ := envelopeOf(entries[len(entries)-1])
	if leafID != lastID {
		t.Errorf("leaf = %q, want last entry %q", leafID, lastID)
	}

	for _, e := range entries {
		id, _, _ := envelopeOf(e)
		if _, ok := l.Get(id); !ok {
			t.Errorf("Get(%q) not found", id)
		}
	}
}

func TestGoldenPiV3FixtureActiveBranchAndContext(t *testing.T) {
	l, err := Load(goldenPiV3Path)
	if err != nil {
		t.Fatal(err)
	}
	entries := l.Entries()

	active, err := l.ActiveBranch()
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != len(entries) {
		t.Fatalf("active branch = %d entries, want %d", len(active), len(entries))
	}
	if ids := entryIDs(active); !equalIDs(ids, entryIDs(entries)) {
		t.Errorf("active branch ids differ from physical order")
	}

	for _, e := range entries {
		if _, isCompaction := e.(*CompactionEntry); isCompaction {
			t.Fatalf("fixture contains compaction entry, want none")
		}
	}
	ctx, err := l.BuildContextEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(ctx) != len(active) {
		t.Fatalf("context entries = %d, want %d", len(ctx), len(active))
	}
	if ids := entryIDs(ctx); !equalIDs(ids, entryIDs(active)) {
		t.Errorf("context entries differ from active branch")
	}
}

func TestGoldenPiV3FixtureByteExactRoundTrip(t *testing.T) {
	lines := readGoldenLines(t)
	l, err := Load(goldenPiV3Path)
	if err != nil {
		t.Fatal(err)
	}
	entries := l.Entries()
	for i, e := range entries {
		out, err := MarshalEntry(e)
		if err != nil {
			t.Fatalf("marshal entry %d: %v", i, err)
		}
		if string(out) != lines[i+1] {
			t.Errorf("entry %d re-marshal differs:\n got %s\nwant %s", i, out, lines[i+1])
		}
	}
}

func TestGoldenPiV3FixtureMessageOpaqueFields(t *testing.T) {
	l, err := Load(goldenPiV3Path)
	if err != nil {
		t.Fatal(err)
	}
	rawCount := 0
	usageCount := 0
	for _, e := range l.Entries() {
		me, ok := e.(*MessageEntry)
		if !ok || me.MessageRole() != "assistant" {
			continue
		}
		msg, err := me.DecodeMessage()
		if err != nil {
			t.Fatalf("decode assistant message: %v", err)
		}
		if msg.Assistant == nil {
			t.Fatal("decoded message has no assistant variant")
		}
		if msg.Assistant.Usage.TotalTokens != 22175 || msg.Assistant.Usage.Cost.Total != 0.025560282 {
			t.Errorf("usage = %+v", msg.Assistant.Usage)
		}
		usageCount++
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(me.Message, &payload); err != nil {
			t.Fatal(err)
		}
		if _, ok := payload["rawStopReason"]; !ok {
			t.Errorf("assistant payload lost rawStopReason: %s", me.Message)
		}
		rawCount++
	}
	if rawCount != goldenPiV3Counts.assistantWithRaw || usageCount != goldenPiV3Counts.assistantWithRaw {
		t.Errorf("checked %d/%d assistant messages", rawCount, usageCount)
	}
}

func TestGoldenPiV3FixtureContentBlockMix(t *testing.T) {
	l, err := Load(goldenPiV3Path)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, e := range l.Entries() {
		me, ok := e.(*MessageEntry)
		if !ok || me.MessageRole() != "assistant" {
			continue
		}
		msg, err := me.DecodeMessage()
		if err != nil {
			t.Fatal(err)
		}
		for _, blk := range msg.Assistant.Content {
			counts[blk.Type]++
			switch blk.Type {
			case agent.BlockTypeThinking:
				if blk.ThinkingSignature == "" {
					t.Errorf("thinking block without signature: %+v", blk)
				}
			case agent.BlockTypeToolCall:
				if blk.ID == "" || blk.Name == "" || len(blk.Arguments) == 0 {
					t.Errorf("toolCall block incomplete: %+v", blk)
				}
			}
		}
	}
	if counts[agent.BlockTypeText] != 2 ||
		counts[agent.BlockTypeThinking] != 29 ||
		counts[agent.BlockTypeToolCall] != 34 {
		t.Errorf("content block counts = %v", counts)
	}
}

func equalIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
