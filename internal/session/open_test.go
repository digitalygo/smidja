package session

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
)

func writeSessionFile(t *testing.T, dir, name string, lines []string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	data := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestOpenAppendRoundtripByteContinuity(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	sess, err := st.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []*agent.UserMessage{
		{Role: "user", Content: json.RawMessage(`"first"`), Timestamp: 1},
	} {
		if err := sess.AppendUser(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := sess.AppendAssistant(&agent.AssistantMessage{
		Role:       "assistant",
		Content:    []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "one"}},
		StopReason: "stop",
		Timestamp:  2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}

	before := readLines(t, sess.Path())
	leafBefore := parseObj(t, before[len(before)-1])["id"].(string)

	re, err := st.Open(sess.Path(), OpenOptions{Strict: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if re.Path() != sess.Path() {
		t.Errorf("reopened path = %q, want %q", re.Path(), sess.Path())
	}
	if err := re.AppendUser(&agent.UserMessage{
		Role:      "user",
		Content:   json.RawMessage(`"second"`),
		Timestamp: 3,
	}); err != nil {
		t.Fatalf("append after reopen: %v", err)
	}
	if err := re.Close(); err != nil {
		t.Fatal(err)
	}

	after := readLines(t, sess.Path())
	if len(after) != len(before)+1 {
		t.Fatalf("lines = %d, want %d", len(after), len(before)+1)
	}
	for i := range before {
		if after[i] != before[i] {
			t.Fatalf("line %d changed after reopen-append:\n got %s\nwant %s", i, after[i], before[i])
		}
	}
	appended := parseObj(t, after[len(after)-1])
	if appended["parentId"] != leafBefore {
		t.Errorf("appended parentId = %v, want %q", appended["parentId"], leafBefore)
	}

	l, err := Load(sess.Path())
	if err != nil {
		t.Fatal(err)
	}
	entries := l.Entries()
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(entries))
	}
	for i := 1; i < len(entries); i++ {
		_, parentID, _ := envelopeOf(entries[i])
		prevID, _, _ := envelopeOf(entries[i-1])
		if parentID == nil || *parentID != prevID {
			t.Fatalf("entry %d parentId = %v, want %q", i, parentID, prevID)
		}
	}
}

func TestOpenByIdAndLockRelease(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	sess, err := st.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"x"`), Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}

	byID, err := st.Open(sess.id, OpenOptions{Strict: true})
	if err != nil {
		t.Fatalf("Open by id: %v", err)
	}
	if byID.Path() != sess.Path() {
		t.Fatalf("Open by id path = %q, want %q", byID.Path(), sess.Path())
	}

	held, err := st.Open(sess.Path(), OpenOptions{Strict: true})
	if err == nil {
		held.Close()
		t.Fatal("second concurrent Open must fail while first holds the lock")
	}

	if err := byID.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := st.Open(sess.Path(), OpenOptions{Strict: true})
	if err != nil {
		t.Fatalf("reopen after close: %v", err)
	}
	if err := again.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenHeaderOnlySession(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(st.root, "--tmp-proj--")
	path := writeSessionFile(t, dir, "2026-08-25T10-00-00-000Z_0196b87c-7a2b-7000-8000-0000000000a1.jsonl", []string{
		`{"type":"session","version":3,"id":"0196b87c-7a2b-7000-8000-0000000000a1","timestamp":"2026-08-25T10:00:00.000Z","cwd":"/tmp/proj"}`,
	})
	sess, err := st.Open(path, OpenOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"first"`), Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	lines := readLines(t, path)
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2", len(lines))
	}
	if parseObj(t, lines[1])["parentId"] != nil {
		t.Errorf("first entry on header-only session must have null parentId")
	}
}

func TestOpenValidatesHeader(t *testing.T) {
	dir := t.TempDir()
	base := `{"type":"session","version":3,"id":"0196b87c-7a2b-7000-8000-0000000000a1","timestamp":"2026-08-25T10:00:00.000Z","cwd":"/tmp/proj"}`
	cases := []struct {
		name    string
		line    string
		wantErr bool
		substr  string
	}{
		{"valid", base, false, ""},
		{"wrong version", `{"type":"session","version":2,"id":"0196b87c-7a2b-7000-8000-0000000000a1","timestamp":"2026-08-25T10:00:00.000Z","cwd":"/tmp/proj"}`, true, "version"},
		{"not a session", `{"type":"message","id":"x","parentId":null,"timestamp":"2026-08-25T10:00:01.000Z","message":{}}`, true, "not a valid session"},
		{"empty id", `{"type":"session","version":3,"id":"","timestamp":"2026-08-25T10:00:00.000Z","cwd":"/tmp/proj"}`, true, "empty id"},
		{"bad timestamp", `{"type":"session","version":3,"id":"0196b87c-7a2b-7000-8000-0000000000a1","timestamp":"nope","cwd":"/tmp/proj"}`, true, "not RFC3339"},
	}
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeSessionFile(t, dir, tc.name+".jsonl", []string{tc.line})
			sess, err := st.Open(path, OpenOptions{Strict: true})
			if tc.wantErr {
				if err == nil {
					sess.Close()
					t.Fatalf("Open(%q): want error", tc.name)
				}
				if !strings.Contains(err.Error(), tc.substr) {
					t.Fatalf("error %q does not contain %q", err, tc.substr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			sess.Close()
		})
	}
}

func TestOpenStrictRejectsMalformedInterior(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := writeSessionFile(t, filepath.Join(st.root, "--tmp-proj--"), "2026-08-25T10-00-00-000Z_0196b87c-7a2b-7000-8000-0000000000a1.jsonl", []string{
		`{"type":"session","version":3,"id":"0196b87c-7a2b-7000-8000-0000000000a1","timestamp":"2026-08-25T10:00:00.000Z","cwd":"/tmp/proj"}`,
		`{"type":"message","id":"a0000001","parentId":null,"timestamp":"2026-08-25T10:00:01.000Z","message":{"role":"user","content":"ok","timestamp":1}}`,
		`this line is not json`,
		`{"type":"message","id":"a0000002","parentId":"a0000001","timestamp":"2026-08-25T10:00:02.000Z","message":{"role":"user","content":"later","timestamp":2}}`,
	})
	if _, err := st.Open(path, OpenOptions{Strict: true}); err == nil {
		t.Fatal("strict Open on malformed interior: want error")
	} else if !strings.Contains(err.Error(), "line 3") {
		t.Fatalf("error %q must name line 3", err)
	}
	sess, err := st.Open(path, OpenOptions{})
	if err != nil {
		t.Fatalf("lenient Open: %v", err)
	}
	sess.Close()
}

func TestOpenStrictRecoversTrailingPartialLine(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(st.root, "--tmp-proj--")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(path, "2026-08-25T10-00-00-000Z_0196b87c-7a2b-7000-8000-0000000000a1.jsonl")
	data := `{"type":"session","version":3,"id":"0196b87c-7a2b-7000-8000-0000000000a1","timestamp":"2026-08-25T10:00:00.000Z","cwd":"/tmp/proj"}` + "\n" +
		`{"type":"message","id":"a0000001","parentId":null,"timestamp":"2026-08-25T10:00:01.000Z","message":{"role":"user","content":"ok","timestamp":1}}` + "\n" +
		`{"type":"message","id":"a0000002","parentId":"a0000001","timestamp":"2026-08-25T10:00:02.000Z","message":{"role":"user","content":"truncated","ti`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	sess, err := st.Open(path, OpenOptions{Strict: true})
	if err != nil {
		t.Fatalf("strict Open must recover a trailing partial line: %v", err)
	}
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"next"`), Timestamp: 3}); err != nil {
		t.Fatal(err)
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	l, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	entries := l.Entries()
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2 (kept entry + appended entry, partial tail dropped)", len(entries))
	}
	if firstID, _, _ := envelopeOf(entries[0]); firstID != "a0000001" {
		t.Fatalf("first entry = %q, want a0000001", firstID)
	}
	if _, parentID, _ := envelopeOf(entries[1]); parentID == nil || *parentID != "a0000001" {
		t.Fatalf("appended entry parent = %v, want a0000001", parentID)
	}
}

func TestOpenMissingAndAmbiguous(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Open("", OpenOptions{Strict: true}); err == nil {
		t.Error("Open(empty): want error")
	}
	if _, err := st.Open(filepath.Join(t.TempDir(), "nope.jsonl"), OpenOptions{Strict: true}); err == nil {
		t.Error("Open(missing path): want error")
	}
	if _, err := st.Open("0196b87c-7a2b-7000-8000-0000000000a1", OpenOptions{Strict: true}); err == nil {
		t.Error("Open(missing id): want error")
	}

	id := "0196b87c-7a2b-7000-8000-0000000000a1"
	header := `{"type":"session","version":3,"id":"` + id + `","timestamp":"2026-08-25T10:00:00.000Z","cwd":"/tmp/a"}`
	dirA := filepath.Join(st.root, "--a--")
	dirB := filepath.Join(st.root, "--b--")
	writeSessionFile(t, dirA, "2026-08-25T10-00-00-000Z_"+id+".jsonl", []string{header})
	writeSessionFile(t, dirB, "2026-08-25T10-00-00-000Z_"+id+".jsonl", []string{header})
	if _, err := st.Open(id, OpenOptions{Strict: true}); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("Open(ambiguous id): err = %v, want ambiguity error", err)
	}
}

const lockHolderEnv = "SMIDJA_LOCK_HOLDER"

func TestOpenLockContentionSubprocess(t *testing.T) {
	if os.Getenv(lockHolderEnv) == "1" {
		root := os.Getenv("SMIDJA_LOCK_ROOT")
		file := os.Getenv("SMIDJA_LOCK_FILE")
		ready := os.Getenv("SMIDJA_LOCK_READY")
		st, err := NewStore(root)
		if err != nil {
			os.Exit(1)
		}
		sess, err := st.Open(file, OpenOptions{Strict: true})
		if err != nil {
			os.Exit(1)
		}
		if err := os.WriteFile(ready, []byte("ready"), 0o600); err != nil {
			os.Exit(1)
		}
		time.Sleep(2 * time.Second)
		sess.Close()
		os.Exit(0)
	}

	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := st.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"x"`), Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}

	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command(os.Args[0], "-test.run=^TestOpenLockContentionSubprocess$")
	cmd.Env = append(os.Environ(),
		lockHolderEnv+"=1",
		"SMIDJA_LOCK_ROOT="+st.root,
		"SMIDJA_LOCK_FILE="+sess.Path(),
		"SMIDJA_LOCK_READY="+ready,
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	done := make(chan struct{})
	go func() { <-waited; close(done) }()
	defer func() {
		select {
		case <-done:
		default:
			cmd.Process.Kill()
			<-done
		}
	}()

	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child never acquired the lock")
		}
		time.Sleep(20 * time.Millisecond)
	}

	if _, err := st.Open(sess.Path(), OpenOptions{Strict: true}); err == nil {
		t.Fatal("Open while child holds the lock: want error")
	}

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("child never released the lock")
	}

	again, err := st.Open(sess.Path(), OpenOptions{Strict: true})
	if err != nil {
		t.Fatalf("reopen after child release: %v", err)
	}
	again.Close()
}

func TestOpenReadOnlyFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission checks are bypassed as root")
	}
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := st.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"x"`), Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sess.Path(), 0o444); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Open(sess.Path(), OpenOptions{Strict: true}); err == nil {
		t.Fatal("Open of read-only session file: want error")
	}
}

func TestOpenUnreadableFile(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Open(t.TempDir(), OpenOptions{Strict: true}); err == nil {
		t.Fatal("Open(directory): want error")
	}
	if _, err := st.Open("/nonexistent-absolute-path/session.jsonl", OpenOptions{Strict: true}); err == nil {
		t.Fatal("Open(nonexistent absolute path): want error")
	}
}

func TestOpenErrorsWrapLoadFailure(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := writeSessionFile(t, filepath.Join(st.root, "--x--"), "bad.jsonl", []string{`{"type":"not_session"}`})
	_, err = st.Open(path, OpenOptions{Strict: true})
	if !errors.Is(err, ErrNotASession) {
		t.Fatalf("err = %v, want ErrNotASession", err)
	}
}
