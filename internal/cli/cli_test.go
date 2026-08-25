package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/session"
)

// fakeClient is a scripted agent.Client for CLI tests: each StreamTurn
// call returns the next scripted message and delivers its text and
// thinking blocks through the callbacks it receives.
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

// textStop builds a scripted assistant message that stops with text.
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

// envFrom wraps a map as an env lookup function.
func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// testDeps builds process seams for tests: no env, a fake cwd, and
// in-memory stdio.
func testDeps(stdin string, stdout, stderr *bytes.Buffer) *cliDeps {
	return &cliDeps{
		env:    envFrom(nil),
		getwd:  func() (string, error) { return "/work/dir", nil },
		home:   func() string { return "/home/tester" },
		stdin:  strings.NewReader(stdin),
		stdout: stdout,
		stderr: stderr,
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

// TestRunSingleShot drives the full -p path end to end against an
// httptest server standing in for OpenRouter: env-based wiring, streaming,
// and session persistence.
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
	err := run([]string{"-p", "hello smidja", "-model", "test/model"}, &cliDeps{
		env: envFrom(map[string]string{
			"SMIDJA_OPENROUTER_URL": srv.URL,
			"OPENROUTER_API_KEY":    "sk-test",
			"SMIDJA_SESSION_DIR":    sessDir,
		}),
		getwd:  func() (string, error) { return cwd, nil },
		home:   func() string { return "/home/tester" },
		stdin:  strings.NewReader(""),
		stdout: &stdout,
		stderr: &stderr,
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

	// The turn persisted a session: exactly one jsonl file under sessDir,
	// headed by the session header.
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

// TestRunOnceWithFakeClient drives runOnce directly with an injected fake
// client, a real session, and a buffer, proving the single-shot path is
// testable without the network.
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

// TestRepl drives the interactive path: two turns, then /quit, with the
// session path printed after the first turn.
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

	var stdout bytes.Buffer
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
	if err := repl(context.Background(), strings.NewReader("first question\nsecond question\n/quit\n"), d); err != nil {
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

// TestReplQuitVariants covers the exit commands and EOF.
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
			var stdout bytes.Buffer
			d := &runDeps{
				model:       "m",
				sessionPath: sess.Path(),
				client:      &fakeClient{script: []*agent.AssistantMessage{textStop("ok")}},
				recorder:    &sessionRecorder{sess},
				stdout:      &stdout,
			}
			if err := repl(context.Background(), strings.NewReader(input), d); err != nil {
				t.Fatalf("repl: %v", err)
			}
		})
	}
}

// TestThinkingGatedByShowThinking verifies that the CLI forwards thinking
// deltas only when SMIDJA_SHOW_THINKING is enabled: the loop always
// forwards thinking to the callback, and the CLI decides whether to wire
// one.
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
