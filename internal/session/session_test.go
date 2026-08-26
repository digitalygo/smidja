package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
)

var (
	entryIDRe  = regexp.MustCompile(`^[0-9a-f]{8}$`)
	uuidV7Re   = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	isoStampRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`)
)

func readLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func parseObj(t *testing.T, line string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("unmarshal %q: %v", line, err)
	}
	return m
}

func assertKeys(t *testing.T, m map[string]any, want ...string) {
	t.Helper()
	var got []string
	for k := range m {
		got = append(got, k)
	}
	sort.Strings(got)
	w := append([]string(nil), want...)
	sort.Strings(w)
	if strings.Join(got, ",") != strings.Join(w, ",") {
		t.Fatalf("keys = %v, want exactly %v", got, want)
	}
}

func checkUUIDv7(t *testing.T, id string) {
	t.Helper()
	if !uuidV7Re.MatchString(id) {
		t.Fatalf("id %q is not a lowercase UUIDv7", id)
	}
	hex := strings.ReplaceAll(id, "-", "")
	b := make([]byte, 16)
	for i := 0; i < 16; i++ {
		var v int
		if _, err := fmt.Sscanf(hex[i*2:i*2+2], "%02x", &v); err != nil {
			t.Fatalf("decode %q: %v", hex[i*2:i*2+2], err)
		}
		b[i] = byte(v)
	}
	if got := b[6] >> 4; got != 7 {
		t.Fatalf("version nibble = %#x, want 7", got)
	}
	if got := b[8] >> 6; got != 0b10 {
		t.Fatalf("variant bits = %#b, want 10", got)
	}
	ts := uint64(b[0])<<40 | uint64(b[1])<<32 | uint64(b[2])<<24 |
		uint64(b[3])<<16 | uint64(b[4])<<8 | uint64(b[5])
	now := uint64(time.Now().UnixMilli())
	if ts > now || now-ts > 5000 {
		t.Fatalf("embedded timestamp %d is not within 5s of now %d", ts, now)
	}
}

func TestHeaderGolden(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	sess, err := st.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendUser(&agent.UserMessage{
		Role:      string(agent.RoleUser),
		Content:   json.RawMessage(`"hi"`),
		Timestamp: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}

	lines := readLines(t, sess.Path())
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2 (header + one entry)", len(lines))
	}

	hdr := parseObj(t, lines[0])
	assertKeys(t, hdr, "type", "version", "id", "timestamp", "cwd")
	if hdr["type"] != "session" {
		t.Errorf("type = %v, want session", hdr["type"])
	}
	if hdr["version"] != float64(3) {
		t.Errorf("version = %v, want 3", hdr["version"])
	}
	id, _ := hdr["id"].(string)
	checkUUIDv7(t, id)
	ts, _ := hdr["timestamp"].(string)
	if !isoStampRe.MatchString(ts) {
		t.Errorf("timestamp %q does not match %s", ts, isoStampRe)
	}
	if _, err := time.Parse(time.RFC3339Nano, ts); err != nil {
		t.Errorf("timestamp %q is not RFC3339: %v", ts, err)
	}
	if hdr["cwd"] != cwd {
		t.Errorf("cwd = %v, want %q", hdr["cwd"], cwd)
	}

	base := filepath.Base(sess.Path())
	wantPrefix := strings.ReplaceAll(strings.ReplaceAll(ts, ":", "-"), ".", "-") + "_" + id
	if !strings.HasPrefix(base, wantPrefix) || !strings.HasSuffix(base, ".jsonl") {
		t.Errorf("file name %q does not follow <stamp>_<id>.jsonl", base)
	}
}

func TestAppendChainingAndMessageShapes(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := st.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	user := &agent.UserMessage{
		Role:      "user",
		Content:   json.RawMessage(`"hello smidja"`),
		Timestamp: 1000,
	}
	asst := &agent.AssistantMessage{
		Role:       "assistant",
		API:        "openai-completions",
		Provider:   "openrouter",
		Model:      "stealth/ox-alpha",
		ResponseID: "resp-1",
		Content: []agent.ContentBlock{
			{Type: agent.BlockTypeText, Text: "let me think"},
			{Type: agent.BlockTypeThinking, Thinking: "reasoning here"},
			{Type: agent.BlockTypeToolCall, ID: "tc-1", Name: "bash", Arguments: json.RawMessage(`{"command":"ls"}`)},
		},
		Usage:      agent.Usage{Input: 10, Output: 20, TotalTokens: 30, Cost: agent.Cost{Total: 0.0001}},
		StopReason: "toolUse",
		Timestamp:  2000,
	}
	tool := &agent.ToolResultMessage{
		Role:       "toolResult",
		ToolCallID: "tc-1",
		ToolName:   "bash",
		Content:    []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "ok"}},
		IsError:    false,
		Timestamp:  3000,
	}

	if err := sess.AppendUser(user); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendAssistant(asst); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendToolResult(tool); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendUser(&agent.UserMessage{
		Role:      "user",
		Content:   json.RawMessage(`"again"`),
		Timestamp: 4000,
	}); err != nil {
		t.Fatal(err)
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}

	lines := readLines(t, sess.Path())
	if len(lines) != 5 {
		t.Fatalf("lines = %d, want 5 (header + 4 entries)", len(lines))
	}

	var prevID any
	for i, line := range lines[1:] {
		e := parseObj(t, line)
		assertKeys(t, e, "type", "id", "parentId", "timestamp", "message")
		if e["type"] != "message" {
			t.Errorf("entry %d type = %v, want message", i, e["type"])
		}
		id, _ := e["id"].(string)
		if !entryIDRe.MatchString(id) {
			t.Errorf("entry %d id %q is not 8 lowercase hex", i, id)
		}
		if i == 0 {
			if e["parentId"] != nil {
				t.Errorf("first entry parentId = %v, want null", e["parentId"])
			}
		} else if e["parentId"] != prevID {
			t.Errorf("entry %d parentId = %v, want %v", i, e["parentId"], prevID)
		}
		prevID = e["id"]
		ts, _ := e["timestamp"].(string)
		if !isoStampRe.MatchString(ts) {
			t.Errorf("entry %d timestamp %q not ISO", i, ts)
		}
	}

	msg0 := parseObj(t, lines[1])["message"].(map[string]any)
	assertKeys(t, msg0, "role", "content", "timestamp")
	if msg0["role"] != "user" {
		t.Errorf("role = %v, want user", msg0["role"])
	}
	if msg0["content"] != "hello smidja" {
		t.Errorf("content = %v, want hello smidja", msg0["content"])
	}
	if msg0["timestamp"] != float64(1000) {
		t.Errorf("timestamp = %v, want 1000", msg0["timestamp"])
	}

	msg1 := parseObj(t, lines[2])["message"].(map[string]any)
	assertKeys(t, msg1, "role", "content", "api", "provider", "model", "responseId",
		"usage", "stopReason", "timestamp")
	if msg1["role"] != "assistant" || msg1["api"] != "openai-completions" ||
		msg1["provider"] != "openrouter" || msg1["model"] != "stealth/ox-alpha" ||
		msg1["responseId"] != "resp-1" || msg1["stopReason"] != "toolUse" {
		t.Errorf("assistant scalars = %v", msg1)
	}
	if msg1["timestamp"] != float64(2000) {
		t.Errorf("assistant timestamp = %v, want 2000", msg1["timestamp"])
	}
	blocks := msg1["content"].([]any)
	if len(blocks) != 3 {
		t.Fatalf("content blocks = %d, want 3", len(blocks))
	}
	b0 := blocks[0].(map[string]any)
	assertKeys(t, b0, "type", "text")
	if b0["type"] != "text" || b0["text"] != "let me think" {
		t.Errorf("text block = %v", b0)
	}
	b1 := blocks[1].(map[string]any)
	assertKeys(t, b1, "type", "thinking")
	if b1["type"] != "thinking" || b1["thinking"] != "reasoning here" {
		t.Errorf("thinking block = %v", b1)
	}
	b2 := blocks[2].(map[string]any)
	assertKeys(t, b2, "type", "id", "name", "arguments")
	if b2["type"] != "toolCall" || b2["id"] != "tc-1" || b2["name"] != "bash" {
		t.Errorf("toolCall block = %v", b2)
	}
	args := b2["arguments"].(map[string]any)
	if args["command"] != "ls" {
		t.Errorf("toolCall arguments = %v", args)
	}
	usage := msg1["usage"].(map[string]any)
	assertKeys(t, usage, "input", "output", "cacheRead", "cacheWrite", "totalTokens", "cost")
	if usage["input"] != float64(10) || usage["output"] != float64(20) ||
		usage["totalTokens"] != float64(30) {
		t.Errorf("usage = %v", usage)
	}
	cost := usage["cost"].(map[string]any)
	assertKeys(t, cost, "input", "output", "cacheRead", "cacheWrite", "total")
	if cost["total"] != 0.0001 {
		t.Errorf("cost.total = %v, want 0.0001", cost["total"])
	}

	msg2 := parseObj(t, lines[3])["message"].(map[string]any)
	assertKeys(t, msg2, "role", "toolCallId", "toolName", "content", "isError", "timestamp")
	if msg2["role"] != "toolResult" || msg2["toolCallId"] != "tc-1" ||
		msg2["toolName"] != "bash" || msg2["isError"] != false ||
		msg2["timestamp"] != float64(3000) {
		t.Errorf("toolResult = %v", msg2)
	}
	trBlocks := msg2["content"].([]any)
	if len(trBlocks) != 1 {
		t.Fatalf("toolResult content blocks = %d, want 1", len(trBlocks))
	}
	assertKeys(t, trBlocks[0].(map[string]any), "type", "text")

	msg3 := parseObj(t, lines[4])["message"].(map[string]any)
	if msg3["content"] != "again" || msg3["timestamp"] != float64(4000) {
		t.Errorf("last user message = %v", msg3)
	}
}

func TestUUIDv7Bits(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := st.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	checkUUIDv7(t, sess.id)

	for i := 0; i < 10; i++ {
		id, err := newUUIDv7()
		if err != nil {
			t.Fatal(err)
		}
		checkUUIDv7(t, id)
	}
}

func TestMungedDirNameAndList(t *testing.T) {
	root := t.TempDir()
	st, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	sess, err := st.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendUser(&agent.UserMessage{
		Role:      "user",
		Content:   json.RawMessage(`"first"`),
		Timestamp: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}

	cleaned := filepath.Clean(cwd)
	munged := "--" + strings.ReplaceAll(strings.ReplaceAll(strings.TrimPrefix(cleaned, "/"), "/", "-"), ":", "-") + "--"
	wantDir := filepath.Join(root, munged)
	if got := filepath.Dir(sess.Path()); got != wantDir {
		t.Fatalf("session dir = %q, want %q", got, wantDir)
	}

	listed, err := st.List(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0] != sess.Path() {
		t.Fatalf("List = %v, want [%s]", listed, sess.Path())
	}

	other, err := st.List(filepath.Join(t.TempDir(), "no-sessions"))
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Fatalf("List for empty cwd = %v, want []", other)
	}
}

func TestListNewestFirst(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	paths := make([]string, 3)
	for i := range paths {
		sess, err := st.Create(cwd)
		if err != nil {
			t.Fatal(err)
		}
		if err := sess.AppendUser(&agent.UserMessage{
			Role:      "user",
			Content:   json.RawMessage(`"m"`),
			Timestamp: int64(i),
		}); err != nil {
			t.Fatal(err)
		}
		if err := sess.Close(); err != nil {
			t.Fatal(err)
		}
		paths[i] = sess.Path()
	}
	listed, err := st.List(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 3 {
		t.Fatalf("List = %d sessions, want 3", len(listed))
	}
	if listed[0] != paths[2] {
		t.Fatalf("List[0] = %q, want newest %q", listed[0], paths[2])
	}
}

func TestLazyCreationAndPerms(t *testing.T) {
	st, err := NewStore(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	sess, err := st.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sess.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file exists before first append (err = %v), want not-exist", err)
	}
	if err := sess.AppendUser(&agent.UserMessage{
		Role:      "user",
		Content:   json.RawMessage(`"first"`),
		Timestamp: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(sess.Path())
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("file perms = %o, want 600", perm)
	}
	di, err := os.Stat(filepath.Dir(sess.Path()))
	if err != nil {
		t.Fatal(err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Errorf("session dir perms = %o, want 700", perm)
	}
	ri, err := os.Stat(st.root)
	if err != nil {
		t.Fatal(err)
	}
	if perm := ri.Mode().Perm(); perm != 0o700 {
		t.Errorf("root dir perms = %o, want 700", perm)
	}
}

func TestMarshalFailureLeavesLeafUnchanged(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := st.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	bad := &agent.UserMessage{Role: "user", Content: json.RawMessage("not-json"), Timestamp: 1}
	if err := sess.AppendUser(bad); err == nil {
		t.Fatal("AppendUser with invalid raw content: want error, got nil")
	}
	if _, err := os.Stat(sess.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file exists after failed append (err = %v), want not-exist", err)
	}

	if err := sess.AppendUser(&agent.UserMessage{
		Role:      "user",
		Content:   json.RawMessage(`"ok"`),
		Timestamp: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	lines := readLines(t, sess.Path())
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2 (header + one entry)", len(lines))
	}
	e := parseObj(t, lines[1])
	if e["parentId"] != nil {
		t.Errorf("first entry parentId = %v, want null", e["parentId"])
	}
	msg := e["message"].(map[string]any)
	if msg["content"] != "ok" {
		t.Errorf("content = %v, want ok", msg["content"])
	}
}

func TestUserContentPassthrough(t *testing.T) {
	cases := []string{
		`"plain string"`,
		`[{"type":"text","text":"a"},{"type":"thinking","thinking":"b"}]`,
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			st, err := NewStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			sess, err := st.Create(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			m := &agent.UserMessage{Role: "user", Content: json.RawMessage(raw), Timestamp: 7}
			if err := sess.AppendUser(m); err != nil {
				t.Fatal(err)
			}
			if err := sess.Close(); err != nil {
				t.Fatal(err)
			}
			msg := parseObj(t, readLines(t, sess.Path())[1])["message"].(map[string]any)

			var want, got any
			if err := json.Unmarshal([]byte(raw), &want); err != nil {
				t.Fatal(err)
			}
			reparsed, err := json.Marshal(msg["content"])
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(reparsed, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(want, got) {
				t.Errorf("content = %s, want %s", reparsed, raw)
			}
		})
	}
}

func TestNilMessagesRejected(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := st.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	for name, appendFn := range map[string]func() error{
		"user":       func() error { return sess.AppendUser(nil) },
		"assistant":  func() error { return sess.AppendAssistant(nil) },
		"toolResult": func() error { return sess.AppendToolResult(nil) },
	} {
		if err := appendFn(); err == nil {
			t.Errorf("%s: nil message accepted, want error", name)
		}
	}
}

func TestSessionFileNameValidation(t *testing.T) {
	validTS := "2026-08-25T10:00:00.000Z"
	validID := "0196b87c-7a2b-7000-8000-000000000001"
	cases := []struct {
		name    string
		ts, id  string
		wantErr bool
		want    string
	}{
		{"valid uuid", validTS, validID, false, "2026-08-25T10-00-00-000Z_0196b87c-7a2b-7000-8000-000000000001.jsonl"},
		{"valid uppercase hex", validTS, "0196B87C-7A2B-7000-8000-000000000001", false, "2026-08-25T10-00-00-000Z_0196B87C-7A2B-7000-8000-000000000001.jsonl"},
		{"valid no fraction", "2026-08-25T10:00:00Z", validID, false, "2026-08-25T10-00-00Z_0196b87c-7a2b-7000-8000-000000000001.jsonl"},
		{"valid plain hex", validTS, "abcdef0123456789", false, "2026-08-25T10-00-00-000Z_abcdef0123456789.jsonl"},
		{"valid 36 char id", validTS, "0123456789abcdef0123456789abcdefabcd", false, "2026-08-25T10-00-00-000Z_0123456789abcdef0123456789abcdefabcd.jsonl"},

		{"empty id", validTS, "", true, ""},
		{"id too long", validTS, strings.Repeat("a", 37), true, ""},
		{"traversal id", validTS, "/../../../../target/poison", true, ""},
		{"dotdot id", validTS, "..", true, ""},
		{"id with slash", validTS, "abc/def", true, ""},
		{"id with backslash", validTS, "..\\..\\evil", true, ""},
		{"id with colon", validTS, "abc:def", true, ""},
		{"id with non-hex letters", validTS, "session-1", true, ""},
		{"id with punctuation", validTS, "abc!def", true, ""},
		{"id with space", validTS, "abc def", true, ""},

		{"empty timestamp", "", validID, true, ""},
		{"timestamp not rfc3339", "not-a-timestamp", validID, true, ""},
		{"timestamp missing zone", "2026-08-25T10:00:00.000", validID, true, ""},
		{"timestamp with offset", "2026-08-25T10:00:00+02:00", validID, true, ""},
		{"timestamp date only", "2026-08-25", validID, true, ""},
		{"timestamp with slash", "2026/08/25T10:00:00.000Z", validID, true, ""},
		{"timestamp with space", "2026-08-25 10:00:00Z", validID, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SessionFileName(tc.ts, tc.id)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("SessionFileName(%q, %q) = %q, want error", tc.ts, tc.id, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("SessionFileName(%q, %q): %v", tc.ts, tc.id, err)
			}
			if got != tc.want {
				t.Errorf("SessionFileName(%q, %q) = %q, want %q", tc.ts, tc.id, got, tc.want)
			}
		})
	}
}

func TestSessionFilePathContainment(t *testing.T) {
	dir := filepath.Join(string(filepath.Separator), "tmp", "sessions", "--work-dir--")
	ts := "2026-08-25T10:00:00.000Z"
	id := "0196b87c-7a2b-7000-8000-000000000001"
	name := "2026-08-25T10-00-00-000Z_0196b87c-7a2b-7000-8000-000000000001.jsonl"

	got, err := SessionFilePath(dir, ts, id)
	if err != nil {
		t.Fatalf("SessionFilePath: %v", err)
	}
	if want := filepath.Join(dir, name); got != want {
		t.Errorf("SessionFilePath = %q, want %q", got, want)
	}

	if _, err := SessionFilePath(dir, ts, "../../../evil"); err == nil {
		t.Error("SessionFilePath with traversal id: want error")
	}

	for _, hostile := range []string{"../escape.jsonl", "../../x", "a/../../x", "/abs/escape.jsonl", ""} {
		if _, err := filePathUnder(dir, hostile); err == nil {
			t.Errorf("filePathUnder(%q, %q): want error", dir, hostile)
		}
	}

	nested, err := filePathUnder(dir, "sub/x.jsonl")
	if err != nil {
		t.Errorf("filePathUnder nested: %v", err)
	} else if want := filepath.Join(dir, "sub/x.jsonl"); nested != want {
		t.Errorf("filePathUnder nested = %q, want %q", nested, want)
	}
}

func TestClose(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := st.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendUser(&agent.UserMessage{
		Role:      "user",
		Content:   json.RawMessage(`"x"`),
		Timestamp: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	if err := sess.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if err := sess.AppendUser(&agent.UserMessage{
		Role:      "user",
		Content:   json.RawMessage(`"y"`),
		Timestamp: 2,
	}); err == nil {
		t.Fatal("append after Close: want error, got nil")
	}
	lines := readLines(t, sess.Path())
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2", len(lines))
	}
}
