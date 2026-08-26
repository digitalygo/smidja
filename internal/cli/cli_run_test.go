package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/internal/ui"
	"github.com/digitalygo/smidja/sdk"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeClient struct {
	script []*agent.AssistantMessage
	calls  int
}

func (f *fakeClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	if f.calls >= len(f.script) {
		return nil, errors.New("fakeClient: script exhausted")
	}
	m := f.script[f.calls]
	f.calls++
	for _, b := range m.Content {
		switch b.Type {
		case agent.BlockTypeText:
			if onText != nil {
				onText(b.Text)
			}
		case agent.BlockTypeThinking:
			if onThinking != nil {
				onThinking(b.Thinking)
			}
		}
	}
	return m, nil
}

func textStop(text string) *agent.AssistantMessage {
	return &agent.AssistantMessage{
		Role:       string(agent.RoleAssistant),
		Content:    []agent.ContentBlock{{Type: agent.BlockTypeText, Text: text}},
		API:        "openai-completions",
		Provider:   "openrouter",
		Model:      "test/model",
		StopReason: "stop",
		Timestamp:  1,
	}
}

func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func testDeps(stdin string, stdout, stderr *bytes.Buffer) *Deps {
	return &Deps{
		Env:    envFrom(nil),
		Getwd:  func() (string, error) { return "/work/dir", nil },
		Home:   func() string { return "/home/tester" },
		Stdin:  strings.NewReader(stdin),
		Stdout: stdout,
		Stderr: stderr,
	}
}

func TestRunVersion(t *testing.T) {
	old := Version
	Version = "9.9.9"
	defer func() { Version = old }()

	var stdout, stderr bytes.Buffer
	if err := run([]string{"-version"}, testDeps("", &stdout, &stderr)); err != nil {
		t.Fatalf("run -version: %v", err)
	}
	if got := stdout.String(); got != "smidja 9.9.9\n" {
		t.Errorf("stdout = %q, want %q", got, "smidja 9.9.9\n")
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunUnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{"-nope"}, testDeps("", &stdout, &stderr))
	if err == nil {
		t.Fatal("unknown flag: want error")
	}
	if !strings.Contains(stderr.String(), "smidja: flag provided but not defined: -nope") {
		t.Errorf("stderr = %q, want the flag error", stderr.String())
	}
	if !strings.Contains(stderr.String(), "usage: smidja") {
		t.Errorf("stderr = %q, want usage", stderr.String())
	}
}

func TestRunHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{"-h"}, testDeps("", &stdout, &stderr)); err != nil {
		t.Fatalf("run -h: %v", err)
	}
	if !strings.Contains(stderr.String(), "usage: smidja") {
		t.Errorf("stderr = %q, want usage", stderr.String())
	}
	if !strings.Contains(stderr.String(), "-p prompt") {
		t.Errorf("stderr = %q, want the flag list", stderr.String())
	}
}

func TestRunSingleShot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)
		fmt.Fprintf(w, "data: %s\n\n", `{"id":"gen_1","choices":[{"index":0,"delta":{"content":"hello from fake"}}]}`)
		fl.Flush()
		fmt.Fprintf(w, "data: %s\n\n", `{"id":"gen_1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
		fl.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		fl.Flush()
	}))
	defer srv.Close()

	cwd := t.TempDir()
	sessDir := t.TempDir()
	var stdout, stderr bytes.Buffer
	err := run([]string{"-p", "hello smidja", "-model", "test/model"}, &Deps{
		Env: envFrom(map[string]string{
			"SMIDJA_OPENROUTER_URL": srv.URL,
			"OPENROUTER_API_KEY":    "sk-test",
			"SMIDJA_SESSION_DIR":    sessDir,
		}),
		Getwd:  func() (string, error) { return cwd, nil },
		Home:   func() string { return "/home/tester" },
		Stdin:  strings.NewReader(""),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("run: %v (stderr %q)", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "hello from fake") {
		t.Errorf("stdout = %q, want the streamed response", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}

	var jsonls []string
	filepath.WalkDir(sessDir, func(p string, de fs.DirEntry, err error) error {
		if err == nil && !de.IsDir() && strings.HasSuffix(de.Name(), ".jsonl") {
			jsonls = append(jsonls, p)
		}
		return nil
	})
	if len(jsonls) != 1 {
		t.Fatalf("session files = %d, want 1 (under %s)", len(jsonls), sessDir)
	}
	b, err := os.ReadFile(jsonls[0])
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("session lines = %d, want >= 3 (header + user + assistant)", len(lines))
	}
	if !strings.Contains(lines[0], `"type":"session"`) {
		t.Errorf("header = %q, want a session header", lines[0])
	}
}

func TestRunOnceWithFakeClient(t *testing.T) {
	cwd := t.TempDir()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	var stdout bytes.Buffer
	d := &runDeps{
		model:        "test/model",
		system:       "be terse",
		showThinking: true,
		sessionPath:  sess.Path(),
		client:       &fakeClient{script: []*agent.AssistantMessage{textStop("hello back")}},
		recorder:     &sessionRecorder{sess},
		stdout:       &stdout,
	}
	if err := runOnce(context.Background(), d, "hello"); err != nil {
		t.Fatalf("runOnce: %v", err)
	}
	if got := stdout.String(); got != "hello back\n" {
		t.Errorf("stdout = %q, want the response plus a final newline", got)
	}
	b, err := os.ReadFile(sess.Path())
	if err != nil {
		t.Fatalf("session file not created: %v", err)
	}
	if !strings.Contains(string(b), "hello") || !strings.Contains(string(b), "hello back") {
		t.Errorf("session file does not contain the turn:\n%s", b)
	}
}

func TestRepl(t *testing.T) {
	cwd := t.TempDir()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	var stdout, stderr bytes.Buffer
	d := &runDeps{
		model:       "test/model",
		system:      "be terse",
		sessionPath: sess.Path(),
		client: &fakeClient{script: []*agent.AssistantMessage{
			textStop("first answer"),
			textStop("second answer"),
		}},
		recorder: &sessionRecorder{sess},
		stdout:   &stdout,
	}
	lineUI := ui.New(strings.NewReader("first question\nsecond question\n/quit\n"), &stdout, &stderr, sdk.ModeInteractive)
	if err := repl(context.Background(), lineUI, d); err != nil {
		t.Fatalf("repl: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "first answer") || !strings.Contains(out, "second answer") {
		t.Errorf("stdout = %q, want both responses", out)
	}
	if got := strings.Count(out, "session: "); got != 1 {
		t.Errorf("session path printed %d times, want 1 (stdout %q)", got, out)
	}
	if !strings.Contains(out, "session: "+sess.Path()) {
		t.Errorf("stdout = %q, want the session path %q", out, sess.Path())
	}
}

func TestReplQuitVariants(t *testing.T) {
	for _, input := range []string{"/quit\n", "/exit\n", "last line\n"} {
		t.Run(fmt.Sprintf("input %q", input), func(t *testing.T) {
			store, err := session.NewStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			sess, err := store.Create(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer sess.Close()
			var stdout, stderr bytes.Buffer
			d := &runDeps{
				model:       "m",
				sessionPath: sess.Path(),
				client:      &fakeClient{script: []*agent.AssistantMessage{textStop("ok")}},
				recorder:    &sessionRecorder{sess},
				stdout:      &stdout,
			}
			lineUI := ui.New(strings.NewReader(input), &stdout, &stderr, sdk.ModeInteractive)
			if err := repl(context.Background(), lineUI, d); err != nil {
				t.Fatalf("repl: %v", err)
			}
		})
	}
}

func TestThinkingGatedByShowThinking(t *testing.T) {
	asst := &agent.AssistantMessage{
		Role: string(agent.RoleAssistant),
		Content: []agent.ContentBlock{
			{Type: agent.BlockTypeThinking, Thinking: "secret reasoning"},
			{Type: agent.BlockTypeText, Text: "visible answer"},
		},
		API:        "openai-completions",
		Provider:   "openrouter",
		Model:      "test/model",
		StopReason: "stop",
		Timestamp:  1,
	}

	run := func(show bool) string {
		t.Helper()
		store, err := session.NewStore(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		sess, err := store.Create(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		defer sess.Close()
		var stdout bytes.Buffer
		d := &runDeps{
			model:        "m",
			showThinking: show,
			sessionPath:  sess.Path(),
			client:       &fakeClient{script: []*agent.AssistantMessage{asst}},
			recorder:     &sessionRecorder{sess},
			stdout:       &stdout,
		}
		if err := runOnce(context.Background(), d, "q"); err != nil {
			t.Fatalf("runOnce: %v", err)
		}
		return stdout.String()
	}

	if out := run(false); strings.Contains(out, "secret reasoning") {
		t.Errorf("thinking leaked to stdout with SMIDJA_SHOW_THINKING off: %q", out)
	}
	if out := run(true); !strings.Contains(out, "secret reasoning") {
		t.Errorf("thinking missing from stdout with SMIDJA_SHOW_THINKING on: %q", out)
	}
}

func TestEnvTruthy(t *testing.T) {
	cases := map[string]bool{
		"":         false,
		"0":        false,
		"false":    false,
		"FALSE":    false,
		"no":       false,
		"off":      false,
		"1":        true,
		"true":     true,
		"yes":      true,
		"anything": true,
		" true ":   true,
	}
	for v, want := range cases {
		if got := envTruthy(v); got != want {
			t.Errorf("envTruthy(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestRunVersionSubcommand(t *testing.T) {
	old := Version
	Version = "9.9.9"
	defer func() { Version = old }()

	var stdout, stderr bytes.Buffer
	if err := run([]string{"version"}, testDeps("", &stdout, &stderr)); err != nil {
		t.Fatalf("run version: %v", err)
	}
	if got := stdout.String(); got != "smidja 9.9.9\n" {
		t.Errorf("stdout = %q, want %q", got, "smidja 9.9.9\n")
	}
}

func TestRunVersionJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{"version", "--json"}, testDeps("", &stdout, &stderr)); err != nil {
		t.Fatalf("run version --json: %v", err)
	}
	want := `{"commit":"none","origin":"github.com/digitalygo/smidja","version":"dev"}`
	if got := strings.TrimSpace(stdout.String()); got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestRunUnexpectedArgument(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{"badcmd"}, testDeps("", &stdout, &stderr))
	if err == nil {
		t.Fatal("badcmd: want error")
	}
	if !strings.Contains(stderr.String(), `unexpected argument "badcmd"`) {
		t.Errorf("stderr = %q, want the unexpected-argument error", stderr.String())
	}
	if !strings.Contains(stderr.String(), "usage: smidja") {
		t.Errorf("stderr = %q, want usage", stderr.String())
	}
}

func piSessionFile(t *testing.T, cwd, timestamp, id string, entries ...string) string {
	t.Helper()
	var b strings.Builder
	hdr, err := json.Marshal(map[string]any{
		"type": "session", "version": 3, "id": id, "timestamp": timestamp, "cwd": cwd,
	})
	if err != nil {
		t.Fatal(err)
	}
	b.Write(hdr)
	b.WriteString("\n")
	for _, e := range entries {
		b.WriteString(e)
		b.WriteString("\n")
	}
	p := filepath.Join(t.TempDir(), "source.jsonl")
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const piUserEntry = `{"type":"message","id":"e1","parentId":null,"timestamp":"2026-08-25T00:00:00.000Z","message":{"role":"user","content":"\"hello\""}}`

const piAssistantEntry = `{"type":"message","id":"e2","parentId":"e1","timestamp":"2026-08-25T00:00:01.000Z","message":{"role":"assistant","content":"\"hi there\""}}`
