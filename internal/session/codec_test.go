package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
)

var goldenAllTypes = []string{
	`{"type":"session","version":3,"id":"0196b87c-7a2b-7000-8000-000000000001","timestamp":"2026-08-25T10:00:00.000Z","cwd":"/tmp/roundtrip"}`,
	`{"type":"message","id":"aaaa0001","parentId":null,"timestamp":"2026-08-25T10:00:01.000Z","message":{"role":"user","content":"hello smidja","timestamp":1000}}`,
	`{"type":"message","id":"aaaa0002","parentId":"aaaa0001","timestamp":"2026-08-25T10:00:02.000Z","message":{"role":"assistant","content":[{"type":"text","text":"ok"},{"type":"toolCall","id":"tc-1","name":"bash","arguments":{"command":"ls"}}],"api":"openai-completions","provider":"openrouter","model":"stealth/ox-alpha","responseId":"resp-1","usage":{"input":10,"output":20,"cacheRead":5,"cacheWrite":0,"totalTokens":30,"cost":{"input":0.1,"output":0.2,"cacheRead":0.05,"cacheWrite":0,"total":0.35}},"stopReason":"toolUse","timestamp":2000}}`,
	`{"type":"message","id":"aaaa0003","parentId":"aaaa0002","timestamp":"2026-08-25T10:00:03.000Z","message":{"role":"toolResult","toolCallId":"tc-1","toolName":"bash","content":[{"type":"text","text":"ok"}],"isError":false,"timestamp":3000}}`,
	`{"type":"thinking_level_change","id":"aaaa0004","parentId":"aaaa0003","timestamp":"2026-08-25T10:00:04.000Z","thinkingLevel":"high"}`,
	`{"type":"model_change","id":"aaaa0005","parentId":"aaaa0004","timestamp":"2026-08-25T10:00:05.000Z","provider":"openrouter","modelId":"deepseek/deepseek-v4-pro-0813"}`,
	`{"type":"compaction","id":"aaaa0006","parentId":"aaaa0005","timestamp":"2026-08-25T10:00:06.000Z","summary":"summarized prefix","firstKeptEntryId":"aaaa0003","tokensBefore":1234,"details":{"version":2,"pruned":true},"usage":{"input":10,"output":20,"cacheRead":0,"cacheWrite":0,"totalTokens":30,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"fromHook":true}`,
	`{"type":"branch_summary","id":"aaaa0007","parentId":"aaaa0006","timestamp":"2026-08-25T10:00:07.000Z","fromId":"aaaa0001","summary":"abandoned path"}`,
	`{"type":"custom","id":"aaaa0008","parentId":"aaaa0007","timestamp":"2026-08-25T10:00:08.000Z","customType":"context-hygiene-pruned","data":{"toolCallIds":["x"]}}`,
	`{"type":"custom_message","id":"aaaa0009","parentId":"aaaa0008","timestamp":"2026-08-25T10:00:09.000Z","customType":"my-ext","content":"note","display":true,"details":{"k":"v"}}`,
	`{"type":"label","id":"aaaa0010","parentId":"aaaa0009","timestamp":"2026-08-25T10:00:10.000Z","targetId":"aaaa0001","label":"checkpoint"}`,
	`{"type":"session_info","id":"aaaa0011","parentId":"aaaa0010","timestamp":"2026-08-25T10:00:11.000Z","name":"Roundtrip session"}`,
	`{"type":"future_entry","id":"ff000001","parentId":"aaaa0001","timestamp":"2026-08-25T10:00:12.000Z","futureField":{"nested":[1,2],"flag":true}}`,
}

func writeFixture(t *testing.T, lines []string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	data := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCodecRoundTripAllEntryTypes(t *testing.T) {
	path := writeFixture(t, goldenAllTypes)
	l, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	hdr := l.Header()
	if hdr.Type != EntryTypeSession || hdr.Version != 3 {
		t.Errorf("header = %+v", hdr)
	}
	if hdr.ID != "0196b87c-7a2b-7000-8000-000000000001" {
		t.Errorf("header id = %q", hdr.ID)
	}
	if hdr.Cwd != "/tmp/roundtrip" {
		t.Errorf("header cwd = %q", hdr.Cwd)
	}
	if hdr.ParentSession != nil {
		t.Errorf("header parentSession = %v, want nil", *hdr.ParentSession)
	}
	reHdr, err := json.Marshal(hdr)
	if err != nil {
		t.Fatal(err)
	}
	if string(reHdr) != goldenAllTypes[0] {
		t.Errorf("header re-marshal:\n got %s\nwant %s", reHdr, goldenAllTypes[0])
	}

	entries := l.Entries()
	if len(entries) != len(goldenAllTypes)-1 {
		t.Fatalf("entries = %d, want %d", len(entries), len(goldenAllTypes)-1)
	}
	for i, e := range entries {
		line, err := MarshalEntry(e)
		if err != nil {
			t.Fatalf("entry %d: %v", i, err)
		}
		if string(line) != goldenAllTypes[i+1] {
			t.Errorf("entry %d re-marshal:\n got %s\nwant %s", i, line, goldenAllTypes[i+1])
		}
	}

	if e := entries[0].(*MessageEntry); e.MessageRole() != "user" || e.EntryType() != EntryTypeMessage {
		t.Errorf("entry 0 = %v", e)
	}
	if e := entries[5].(*CompactionEntry); e.Summary != "summarized prefix" ||
		e.FirstKeptEntryID != "aaaa0003" || e.TokensBefore != 1234 || e.FromHook == nil || !*e.FromHook {
		t.Errorf("compaction = %+v", e)
	}
	if e := entries[6].(*BranchSummaryEntry); e.FromID != "aaaa0001" || e.Summary != "abandoned path" {
		t.Errorf("branch_summary = %+v", e)
	}
	if e := entries[7].(*CustomEntry); e.CustomType != "context-hygiene-pruned" {
		t.Errorf("custom = %+v", e)
	}
	if e := entries[8].(*CustomMessageEntry); e.CustomType != "my-ext" || !e.Display {
		t.Errorf("custom_message = %+v", e)
	}
	if e := entries[9].(*LabelEntry); e.TargetID != "aaaa0001" || e.Label == nil || *e.Label != "checkpoint" {
		t.Errorf("label = %+v", e)
	}
	if e := entries[10].(*SessionInfoEntry); e.Name == nil || *e.Name != "Roundtrip session" {
		t.Errorf("session_info = %+v", e)
	}
}

func TestOpaqueEntryPreservedByteExact(t *testing.T) {
	path := writeFixture(t, goldenAllTypes)
	l, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	entries := l.Entries()
	last := entries[len(entries)-1]
	op, ok := last.(*OpaqueEntry)
	if !ok {
		t.Fatalf("last entry is %T, want *OpaqueEntry", last)
	}
	if op.TypeName != "future_entry" {
		t.Errorf("TypeName = %q, want future_entry", op.TypeName)
	}
	line, err := MarshalEntry(op)
	if err != nil {
		t.Fatal(err)
	}
	if string(line) != goldenAllTypes[len(goldenAllTypes)-1] {
		t.Errorf("opaque re-marshal:\n got %s\nwant %s", line, goldenAllTypes[len(goldenAllTypes)-1])
	}
	if op.EnvelopeID() != "ff000001" || op.EnvelopeParentID() == nil ||
		*op.EnvelopeParentID() != "aaaa0001" || op.EnvelopeTimestamp() != "2026-08-25T10:00:12.000Z" {
		t.Errorf("opaque envelope = %q parent %v ts %q",
			op.EnvelopeID(), op.EnvelopeParentID(), op.EnvelopeTimestamp())
	}
}

func TestDecodeEntryShapeMismatchFallsBackToOpaque(t *testing.T) {
	line := `{"type":"compaction","id":"x","parentId":null,"timestamp":"2026-08-25T10:00:00.000Z","summary":42}`
	e, err := DecodeEntry([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	op, ok := e.(*OpaqueEntry)
	if !ok {
		t.Fatalf("decoded %T, want *OpaqueEntry", e)
	}
	if op.TypeName != EntryTypeCompaction {
		t.Errorf("TypeName = %q, want %q", op.TypeName, EntryTypeCompaction)
	}
	out, err := MarshalEntry(op)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != line {
		t.Errorf("re-marshal:\n got %s\nwant %s", out, line)
	}
}

func TestDecodeEntryMalformed(t *testing.T) {
	for _, line := range []string{
		"",
		"not json",
		`{"type":`,
		`[1,2,3]`,
	} {
		if _, err := DecodeEntry([]byte(line)); err == nil {
			t.Errorf("DecodeEntry(%q): want error, got nil", line)
		}
	}
}

func TestMessageEntryDecodeMessage(t *testing.T) {
	path := writeFixture(t, goldenAllTypes)
	l, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	entries := l.Entries()

	user := entries[0].(*MessageEntry)
	msg, err := user.DecodeMessage()
	if err != nil {
		t.Fatal(err)
	}
	if msg.Role() != "user" || msg.User == nil || string(msg.User.Content) != `"hello smidja"` {
		t.Errorf("user message = %+v", msg)
	}

	asst := entries[1].(*MessageEntry)
	msg, err = asst.DecodeMessage()
	if err != nil {
		t.Fatal(err)
	}
	if msg.Role() != "assistant" || msg.Assistant == nil || msg.Assistant.Model != "stealth/ox-alpha" {
		t.Errorf("assistant message = %+v", msg)
	}
	if len(msg.Assistant.Content) != 2 || msg.Assistant.Content[1].Name != "bash" {
		t.Errorf("assistant content = %+v", msg.Assistant.Content)
	}
	if msg.Assistant.Usage.TotalTokens != 30 || msg.Assistant.Usage.Cost.Total != 0.35 {
		t.Errorf("assistant usage = %+v", msg.Assistant.Usage)
	}

	tool := entries[2].(*MessageEntry)
	msg, err = tool.DecodeMessage()
	if err != nil {
		t.Fatal(err)
	}
	if msg.Role() != "toolResult" || msg.ToolResult == nil || msg.ToolResult.ToolCallID != "tc-1" {
		t.Errorf("toolResult message = %+v", msg)
	}

	opaque := &MessageEntry{Message: json.RawMessage(`{"role":"custom","content":"x"}`)}
	if _, err := opaque.DecodeMessage(); err == nil {
		t.Error("DecodeMessage on custom role: want error, got nil")
	}
}

func TestMessageRole(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{`{"role":"user"}`, "user"},
		{`{"role":"assistant"}`, "assistant"},
		{`{"role":"toolResult"}`, "toolResult"},
		{`{"role":"custom","content":"x"}`, "custom"},
		{`{}`, ""},
		{`"not an object"`, ""},
	}
	for _, c := range cases {
		e := &MessageEntry{Message: json.RawMessage(c.raw)}
		if got := e.MessageRole(); got != c.want {
			t.Errorf("MessageRole(%s) = %q, want %q", c.raw, got, c.want)
		}
	}
}

func TestHeaderParentSession(t *testing.T) {
	parent := "/tmp/other/2026-01-01T00-00-00-000Z_01234567-89ab-4cde-f012-3456789abcd.jsonl"
	lines := []string{
		`{"type":"session","version":3,"id":"ps-session","timestamp":"2026-08-25T13:00:00.000Z","cwd":"/tmp/ps","parentSession":"` + parent + `"}`,
		`{"type":"session_info","id":"ps000001","parentId":null,"timestamp":"2026-08-25T13:00:01.000Z","name":"fork"}`,
	}
	l, err := Load(writeFixture(t, lines))
	if err != nil {
		t.Fatal(err)
	}
	if l.Header().ParentSession == nil || *l.Header().ParentSession != parent {
		t.Errorf("parentSession = %v, want %q", l.Header().ParentSession, parent)
	}
	reHdr, err := json.Marshal(l.Header())
	if err != nil {
		t.Fatal(err)
	}
	if string(reHdr) != lines[0] {
		t.Errorf("header re-marshal:\n got %s\nwant %s", reHdr, lines[0])
	}
}

func TestAppendEntryChainsTypedPayloads(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := st.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	level := "high"
	provider := "openrouter"
	modelID := "x/y"
	summary := "compacted"
	firstKept := "k1"
	fromHook := true
	usage := &agent.Usage{Input: 5, Output: 7, TotalTokens: 12}
	label := "mark"
	name := "Session name"

	entries := []Entry{
		&ThinkingLevelChangeEntry{ThinkingLevel: level},
		&ModelChangeEntry{Provider: provider, ModelID: modelID},
		&CompactionEntry{Summary: summary, FirstKeptEntryID: firstKept, TokensBefore: 99, Usage: usage, FromHook: &fromHook},
		&BranchSummaryEntry{FromID: "k1", Summary: "abandoned"},
		&CustomEntry{CustomType: "ext", Data: json.RawMessage(`{"a":1}`)},
		&CustomMessageEntry{CustomType: "ext", Content: json.RawMessage(`"note"`), Display: true},
		&LabelEntry{TargetID: "k1", Label: &label},
		&SessionInfoEntry{Name: &name},
	}
	for _, e := range entries {
		if err := sess.AppendEntry(e); err != nil {
			t.Fatalf("AppendEntry(%T): %v", e, err)
		}
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}

	l, err := Load(sess.Path())
	if err != nil {
		t.Fatal(err)
	}
	got := l.Entries()
	if len(got) != len(entries) {
		t.Fatalf("entries = %d, want %d", len(got), len(entries))
	}
	var prevID any
	for i, e := range got {
		id, parentID, ts := envelopeOf(e)
		if id == "" || !entryIDRe.MatchString(id) {
			t.Errorf("entry %d id %q is not 8 lowercase hex", i, id)
		}
		if i == 0 {
			if parentID != nil {
				t.Errorf("entry %d parentId = %v, want nil", i, parentID)
			}
		} else if parentID == nil || *parentID != prevID {
			t.Errorf("entry %d parentId = %v, want %v", i, parentID, prevID)
		}
		prevID = id
		if ts == "" {
			t.Errorf("entry %d has no timestamp", i)
		}
	}

	if e := got[0].(*ThinkingLevelChangeEntry); e.ThinkingLevel != level {
		t.Errorf("thinkingLevel = %q", e.ThinkingLevel)
	}
	if e := got[1].(*ModelChangeEntry); e.Provider != provider || e.ModelID != modelID {
		t.Errorf("model change = %+v", e)
	}
	if e := got[2].(*CompactionEntry); e.Summary != summary || e.FirstKeptEntryID != firstKept ||
		e.TokensBefore != 99 || e.Usage == nil || e.Usage.TotalTokens != 12 ||
		e.FromHook == nil || !*e.FromHook {
		t.Errorf("compaction = %+v", e)
	}
	if e := got[3].(*BranchSummaryEntry); e.FromID != "k1" || e.Summary != "abandoned" {
		t.Errorf("branch summary = %+v", e)
	}
	if e := got[4].(*CustomEntry); e.CustomType != "ext" || string(e.Data) != `{"a":1}` {
		t.Errorf("custom = %+v", e)
	}
	if e := got[5].(*CustomMessageEntry); e.CustomType != "ext" || !e.Display {
		t.Errorf("custom message = %+v", e)
	}
	if e := got[6].(*LabelEntry); e.TargetID != "k1" || e.Label == nil || *e.Label != label {
		t.Errorf("label = %+v", e)
	}
	if e := got[7].(*SessionInfoEntry); e.Name == nil || *e.Name != name {
		t.Errorf("session info = %+v", e)
	}
}

func TestAppendEntryRejectsOpaqueAndNil(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := st.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	if err := sess.AppendEntry(nil); err == nil {
		t.Error("AppendEntry(nil): want error, got nil")
	}
	op := newOpaqueEntry("future", []byte(`{"type":"future","id":"x","parentId":null,"timestamp":"t"}`))
	if err := sess.AppendEntry(op); err == nil {
		t.Error("AppendEntry(opaque): want error, got nil")
	}
	if _, err := os.Stat(sess.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("file exists after rejected appends (err = %v)", err)
	}
}

func TestCodecBytesStable(t *testing.T) {
	path := writeFixture(t, goldenAllTypes)
	l, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	var first, second bytes.Buffer
	for _, e := range l.Entries() {
		b, err := MarshalEntry(e)
		if err != nil {
			t.Fatal(err)
		}
		first.Write(b)
		first.WriteByte('\n')
	}
	l2, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range l2.Entries() {
		b, err := MarshalEntry(e)
		if err != nil {
			t.Fatal(err)
		}
		second.Write(b)
		second.WriteByte('\n')
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Error("codec output is not stable across loads")
	}
}
