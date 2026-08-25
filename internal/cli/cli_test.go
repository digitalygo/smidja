package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/buildinfo"
	"github.com/digitalygo/smidja/internal/config"
	"github.com/digitalygo/smidja/internal/contextmanager"
	"github.com/digitalygo/smidja/internal/loopdetector"
	"github.com/digitalygo/smidja/internal/retry"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/internal/ui"
	"github.com/digitalygo/smidja/internal/update"
	"github.com/digitalygo/smidja/sdk"
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
// session path printed after the first turn. The prompts run through the
// LineUI, which owns stdin.
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

// ---------------------------------------------------------------------------
// Wave 3: version, import, update subcommands

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

// piSessionFile writes a minimal Pi-format session source file with the
// given header identity and entry lines.
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

func TestRunImport(t *testing.T) {
	sessDir := t.TempDir()
	src := piSessionFile(t, "/work/dir", "2026-08-25T00:00:00.000Z", "0196b87c-7a2b-7000-8000-000000000002", piUserEntry, piAssistantEntry)

	var stdout, stderr bytes.Buffer
	if err := run([]string{"import", src, "--session-dir", sessDir}, testDeps("", &stdout, &stderr)); err != nil {
		t.Fatalf("import: %v (stderr %q)", err, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "imported ") {
		t.Errorf("stdout = %q, want the destination line", out)
	}
	if !strings.Contains(out, "entries: 2") || !strings.Contains(out, "message: 2") {
		t.Errorf("stdout = %q, want the entry stats", out)
	}

	// The destination lands where the store would have written it.
	dest := filepath.Join(sessDir, "--work-dir--", "2026-08-25T00-00-00-000Z_0196b87c-7a2b-7000-8000-000000000002.jsonl")
	if _, err := os.Stat(dest); err != nil {
		t.Errorf("imported destination %q missing: %v", dest, err)
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

func TestRunImportConflict(t *testing.T) {
	sessDir := t.TempDir()
	src1 := piSessionFile(t, "/work/dir", "2026-08-25T00:00:00.000Z", "0196b87c-7a2b-7000-8000-000000000003", piUserEntry)
	src2 := piSessionFile(t, "/work/dir", "2026-08-25T00:00:00.000Z", "0196b87c-7a2b-7000-8000-000000000003",
		`{"type":"message","id":"e1","parentId":null,"timestamp":"2026-08-25T00:00:00.000Z","message":{"role":"user","content":"\"different\""}}`)

	var stdout, stderr bytes.Buffer
	if err := run([]string{"import", src1, "--session-dir", sessDir}, testDeps("", &stdout, &stderr)); err != nil {
		t.Fatalf("first import: %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	err := run([]string{"import", src2, "--session-dir", sessDir}, testDeps("", &stdout, &stderr))
	if err == nil {
		t.Fatal("conflicting import: want error")
	}
	if !strings.Contains(stderr.String(), "exists with different content") {
		t.Errorf("stderr = %q, want the conflict message", stderr.String())
	}
}

func TestRunImportErrors(t *testing.T) {
	t.Run("no file argument", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := run([]string{"import"}, testDeps("", &stdout, &stderr))
		if err == nil {
			t.Fatal("import without a file: want error")
		}
		if !strings.Contains(stderr.String(), "exactly one session file") {
			t.Errorf("stderr = %q, want the argument error", stderr.String())
		}
	})
	t.Run("invalid source", func(t *testing.T) {
		bad := filepath.Join(t.TempDir(), "not-a-session.jsonl")
		if err := os.WriteFile(bad, []byte("not json at all\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		err := run([]string{"import", bad, "--session-dir", t.TempDir()}, testDeps("", &stdout, &stderr))
		if err == nil {
			t.Fatal("invalid source: want error")
		}
		if !strings.Contains(stderr.String(), "no session header found") {
			t.Errorf("stderr = %q, want the invalid-source message", stderr.String())
		}
	})
}

// TestRunImportEscapesControlChars feeds the importer a session whose
// entry type and cwd carry terminal control characters (ESC/BEL, the
// building blocks of CSI/OSC terminal spoofing). Everything the CLI
// prints must be escaped, never emitted as raw control bytes.
func TestRunImportEscapesControlChars(t *testing.T) {
	hdr := `{"type":"session","version":3,"id":"0196b87c-7a2b-7000-8000-000000000001","timestamp":"2026-08-25T00:00:00.000Z","cwd":"/tmp/evil\u001bdir"}`
	evilType := `{"type":"evil\u001b]0;owned\u0007type","id":"e1","parentId":null,"timestamp":"2026-08-25T00:00:01.000Z","message":{"role":"user","content":"\"hi\""}}`
	src := filepath.Join(t.TempDir(), "hostile.jsonl")
	if err := os.WriteFile(src, []byte(hdr+"\n"+evilType+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	sessDir := t.TempDir()
	var stdout, stderr bytes.Buffer
	if err := run([]string{"import", src, "--session-dir", sessDir}, testDeps("", &stdout, &stderr)); err != nil {
		t.Fatalf("import: %v (stderr %q)", err, stderr.String())
	}
	out := stdout.Bytes()
	if bytes.Contains(out, []byte{0x1b}) {
		t.Errorf("stdout contains a raw ESC byte: %q", out)
	}
	if bytes.Contains(out, []byte{0x07}) {
		t.Errorf("stdout contains a raw BEL byte: %q", out)
	}
	if !strings.Contains(stdout.String(), `\x1b`) || !strings.Contains(stdout.String(), `\a`) {
		t.Errorf("stdout = %q, want the quoted \\x1b and \\a escapes", stdout.String())
	}

	// A hostile id on the error path is printed escaped too.
	badHdr := `{"type":"session","version":3,"id":"/..\u001b\u0007/evil","timestamp":"2026-08-25T00:00:00.000Z","cwd":"/tmp/x"}`
	badSrc := filepath.Join(t.TempDir(), "bad.jsonl")
	if err := os.WriteFile(badSrc, []byte(badHdr+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if err := run([]string{"import", badSrc, "--session-dir", sessDir}, testDeps("", &stdout, &stderr)); err == nil {
		t.Fatal("hostile id import: want error")
	}
	if errOut := stderr.Bytes(); bytes.Contains(errOut, []byte{0x1b}) || bytes.Contains(errOut, []byte{0x07}) {
		t.Errorf("stderr contains raw control bytes: %q", errOut)
	}
}

// updateServer serves the GitHub releases API surface the update command
// needs: the latest and tagged release endpoints, the platform asset, and
// checksums.txt. assetBytes is what the asset endpoint returns.
func updateServer(t *testing.T, version string, assetBytes []byte) *httptest.Server {
	t.Helper()
	var base string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"), strings.HasSuffix(r.URL.Path, "/releases/tags/"+version):
			release := fmt.Sprintf(`{"tag_name":%q,"html_url":"https://example.test/releases/%s","published_at":"2026-08-25T00:00:00Z","assets":[
				{"name":"smidja-linux-amd64","browser_download_url":%q},
				{"name":"checksums.txt","browser_download_url":%q}
			]}`, version, version, base+"/asset", base+"/checksums")
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, release)
		case r.URL.Path == "/asset":
			w.Write(assetBytes)
		case r.URL.Path == "/checksums":
			sum := sha256.Sum256(assetBytes)
			fmt.Fprintf(w, "%s  smidja-linux-amd64\n", hex.EncodeToString(sum[:]))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	base = srv.URL
	return srv
}

// updateTestDeps builds process seams with an update client pointed at
// srv, so the update subcommand never touches the real GitHub API.
func updateTestDeps(srv *httptest.Server, origin buildinfo.Info, execPath func() (string, error), stdout, stderr *bytes.Buffer) *cliDeps {
	return &cliDeps{
		env:    envFrom(nil),
		getwd:  func() (string, error) { return "/work/dir", nil },
		home:   func() string { return "/home/tester" },
		stdin:  strings.NewReader(""),
		stdout: stdout,
		stderr: stderr,
		newUpdateClient: func() *update.Client {
			return &update.Client{
				Origin:   origin,
				BaseURL:  srv.URL,
				GOOS:     "linux",
				GOARCH:   "amd64",
				ExecPath: execPath,
			}
		},
	}
}

func TestRunUpdateCheckAvailable(t *testing.T) {
	srv := updateServer(t, "v9.9.9", []byte("asset-bytes"))
	var stdout, stderr bytes.Buffer
	deps := updateTestDeps(srv, buildinfo.Info{Origin: "github.com/digitalygo/smidja", Version: "v1.0.0", Commit: "abc"}, nil, &stdout, &stderr)
	if err := run([]string{"update", "--check"}, deps); err != nil {
		t.Fatalf("update --check: %v (stderr %q)", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "update available: v9.9.9") {
		t.Errorf("stdout = %q, want the availability line", stdout.String())
	}
	if !strings.Contains(stdout.String(), "https://example.test/releases/v9.9.9") {
		t.Errorf("stdout = %q, want the release URL", stdout.String())
	}
}

func TestRunUpdateCheckUpToDate(t *testing.T) {
	srv := updateServer(t, "v1.0.0", []byte("asset-bytes"))
	var stdout, stderr bytes.Buffer
	deps := updateTestDeps(srv, buildinfo.Info{Origin: "github.com/digitalygo/smidja", Version: "v1.0.0", Commit: "abc"}, nil, &stdout, &stderr)
	if err := run([]string{"update", "--check"}, deps); err != nil {
		t.Fatalf("update --check: %v (stderr %q)", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "is up to date") {
		t.Errorf("stdout = %q, want the up-to-date line", stdout.String())
	}
	if strings.Contains(stdout.String(), "update available") {
		t.Errorf("stdout = %q, must not claim an update", stdout.String())
	}
}

func TestRunUpdateApply(t *testing.T) {
	asset := []byte("asset-bytes")
	srv := updateServer(t, "v9.9.9", asset)
	target := filepath.Join(t.TempDir(), "smidja")
	if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	deps := updateTestDeps(srv, buildinfo.Info{Origin: "github.com/digitalygo/smidja", Version: "v1.0.0", Commit: "abc"},
		func() (string, error) { return target, nil }, &stdout, &stderr)
	if err := run([]string{"update"}, deps); err != nil {
		t.Fatalf("update: %v (stderr %q)", err, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "downloading v9.9.9...") || !strings.Contains(out, "installed v9.9.9") {
		t.Errorf("stdout = %q, want the progress lines", out)
	}
	b, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != string(asset) {
		t.Errorf("target binary = %q, want the downloaded asset", b)
	}
}

// ---------------------------------------------------------------------------
// Wave 3: context manager wiring and overflow recovery

// overflowClient returns a context-overflow error on the first call and a
// normal message afterwards, simulating a provider that rejected the
// context once before the forced compaction took effect.
type overflowClient struct {
	text  string
	calls int
}

func (c *overflowClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	c.calls++
	if c.calls == 1 {
		return nil, errors.New("openrouter: 400: prompt is too long")
	}
	if onText != nil {
		onText(c.text)
	}
	return textStop(c.text), nil
}

// alwaysOverflowClient rejects every call with a context-overflow error.
type alwaysOverflowClient struct{}

func (c *alwaysOverflowClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	return nil, errors.New("openrouter: 400: prompt is too long")
}

// testPreparer builds the production context-preparer stack (live plus
// recovery manager) over a small window, for recovery tests.
func testPreparer(t *testing.T) *contextPreparerAdapter {
	t.Helper()
	p, err := newContextPreparer(config.Config{
		Model:               "test/model",
		ContextEnabled:      true,
		ContextWindowTokens: 100_000,
	}, 100_000, nil)
	if err != nil {
		t.Fatalf("newContextPreparer: %v", err)
	}
	return p
}

func TestRunOnceOverflowRecovers(t *testing.T) {
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
		model:       "test/model",
		system:      "be terse",
		sessionPath: sess.Path(),
		client:      &overflowClient{text: "recovered answer"},
		recorder:    &sessionRecorder{sess},
		stdout:      &stdout,
		stderr:      &stderr,
		preparer:    testPreparer(t),
		isOverflow:  retry.IsContextOverflow,
	}
	if err := runOnce(context.Background(), d, "hello"); err != nil {
		t.Fatalf("runOnce: %v", err)
	}
	if !strings.Contains(stdout.String(), "recovered answer") {
		t.Errorf("stdout = %q, want the recovered response", stdout.String())
	}
	if !strings.Contains(stderr.String(), "compacting and retrying once") {
		t.Errorf("stderr = %q, want the recovery notice", stderr.String())
	}
}

func TestRunOnceOverflowTwiceFails(t *testing.T) {
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
		model:       "test/model",
		system:      "be terse",
		sessionPath: sess.Path(),
		client:      &alwaysOverflowClient{},
		recorder:    &sessionRecorder{sess},
		stdout:      &stdout,
		stderr:      &stderr,
		preparer:    testPreparer(t),
		isOverflow:  retry.IsContextOverflow,
	}
	err = runOnce(context.Background(), d, "hello")
	if err == nil {
		t.Fatal("second overflow: want error")
	}
	if !strings.Contains(err.Error(), "context still overflows the model window") {
		t.Errorf("error = %q, want the clear overflow message", err.Error())
	}
	if !strings.Contains(stderr.String(), "compacting and retrying once") {
		t.Errorf("stderr = %q, want the recovery notice", stderr.String())
	}
}

// forcedCompactFixture builds the small-window preparer adapter and a
// 12-message history sized so a normal Prepare with a fresh cache is
// quiet while the forced safety compact drops the oldest messages.
func forcedCompactFixture(t *testing.T) (*contextPreparerAdapter, []*agent.Message) {
	t.Helper()
	cfg := contextmanager.Config{
		Enabled:                true,
		ContextWindowTokens:    10_000,
		CacheMissAfter:         contextmanager.DefaultCacheMissAfter,
		PruneThreshold:         contextmanager.DefaultPruneThreshold,
		CompactThreshold:       contextmanager.DefaultCompactThreshold,
		SafetyCompactThreshold: contextmanager.DefaultSafetyCompactThreshold,
		CompactTarget:          contextmanager.DefaultCompactTarget,
		KeepRecentMessages:     2,
		SelectorChunkTokens:    contextmanager.DefaultSelectorChunkTokens,
		SelectorModel:          "test/model",
	}
	live, err := contextmanager.New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	adapter := newContextPreparerAdapter(live, cfg)
	// Fresh cache: the ordinary prune/compact gates are blocked.
	adapter.ObserveResponse(&agent.AssistantMessage{Usage: agent.Usage{Input: 1}})

	msgs := make([]*agent.Message, 12)
	for i := range msgs {
		content := strings.Repeat("x", 2500)
		if i%2 == 0 {
			msgs[i] = &agent.Message{User: &agent.UserMessage{
				Role: string(agent.RoleUser), Content: json.RawMessage(strconv.Quote(content)), Timestamp: int64(i),
			}}
		} else {
			msgs[i] = &agent.Message{Assistant: &agent.AssistantMessage{
				Role:      string(agent.RoleAssistant),
				Content:   []agent.ContentBlock{{Type: agent.BlockTypeText, Text: content}},
				Timestamp: int64(i),
			}}
		}
	}
	return adapter, msgs
}

func TestContextPreparerAdapterForcedCompaction(t *testing.T) {
	adapter, msgs := forcedCompactFixture(t)

	res, err := adapter.Prepare(context.Background(), agent.ContextRequest{Messages: msgs})
	if err != nil {
		t.Fatal(err)
	}
	if res.Compacted || len(res.Pruned) != 0 {
		t.Fatalf("normal prepare with fresh cache: want no action, got compacted=%v pruned=%d", res.Compacted, len(res.Pruned))
	}

	adapter.forceSafety()
	res, err = adapter.Prepare(context.Background(), agent.ContextRequest{Messages: msgs})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Compacted || res.Compaction == nil {
		t.Fatalf("forced prepare: want compaction, got compacted=%v", res.Compacted)
	}
	if len(res.Messages) >= len(msgs) {
		t.Errorf("forced prepare kept %d messages, want fewer than %d", len(res.Messages), len(msgs))
	}
	entries := adapter.drain()
	if len(entries) != 1 {
		t.Fatalf("drained %d entries, want 1", len(entries))
	}
	if !strings.Contains(string(entries[0].Summary), "smidja-fallback-v1") {
		t.Errorf("summary = %s, want the fallback strategy tag", entries[0].Summary)
	}
}

func TestPersistCompactions(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	adapter, msgs := forcedCompactFixture(t)
	adapter.forceSafety()
	if _, err := adapter.Prepare(context.Background(), agent.ContextRequest{Messages: msgs}); err != nil {
		t.Fatal(err)
	}

	d := &runDeps{preparer: adapter, recorder: &sessionRecorder{sess}}
	if err := d.persistCompactions(); err != nil {
		t.Fatalf("persistCompactions: %v", err)
	}
	b, err := os.ReadFile(sess.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"type":"compaction"`) {
		t.Errorf("session file lacks a compaction entry:\n%s", b)
	}
	if !strings.Contains(string(b), "smidja-fallback-v1") {
		t.Errorf("session file lacks the summary transcript:\n%s", b)
	}
}

func TestRetryAdapter(t *testing.T) {
	calls := 0
	var scheduled, finished []string
	produce := func(ctx context.Context) (*agent.AssistantMessage, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("openrouter: provider returned error: upstream is overloaded")
		}
		return textStop("ok"), nil
	}
	callbacks := &agent.RetryCallbacks{
		Scheduled: func(attempt, maxAttempts int, delayMs int64, errorMessage string) {
			scheduled = append(scheduled, fmt.Sprintf("%d/%d", attempt, maxAttempts))
		},
		Finished: func(success bool, attempt int, finalError string) {
			finished = append(finished, fmt.Sprintf("success=%v attempt=%d", success, attempt))
		},
	}
	msg, err := retryAdapter(context.Background(), produce, agent.RetryPolicy{Enabled: true, MaxRetries: 1, BaseDelayMs: 1}, callbacks)
	if err != nil {
		t.Fatalf("retryAdapter: %v", err)
	}
	if msg == nil || calls != 2 {
		t.Fatalf("calls = %d, want 2 (one retry)", calls)
	}
	if len(scheduled) != 1 || scheduled[0] != "1/1" {
		t.Errorf("scheduled = %v, want [1/1]", scheduled)
	}
	if len(finished) != 1 || finished[0] != "success=true attempt=1" {
		t.Errorf("finished = %v, want the success event", finished)
	}
}

func TestLoopDetectorAdapter(t *testing.T) {
	cfg := loopdetector.Config{
		WindowSize:                    10,
		RepeatSequenceMinLength:       2,
		RepeatPatternMinReps:          1,
		EscalateAfter:                 2,
		EnableToolRepetitionDetection: true,
	}
	adapter := newLoopDetectorAdapter(loopdetector.New(cfg))
	args := json.RawMessage(`{"command":"echo hi"}`)
	turn := agent.Turn{
		TurnIndex: 1,
		ToolCalls: []agent.ToolCallObs{
			{ToolCallID: "c1", Name: "bash", Arguments: args, Result: &agent.ToolResultMessage{ToolCallID: "c1", ToolName: "bash", Content: []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "hi"}}}},
			{ToolCallID: "c2", Name: "bash", Arguments: args, Result: &agent.ToolResultMessage{ToolCallID: "c2", ToolName: "bash", Content: []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "hi"}}}},
		},
	}

	out1 := adapter.Observe(turn)
	if out1.Verdict != agent.VerdictWarn {
		t.Fatalf("first observe verdict = %v, want warn", out1.Verdict)
	}
	if out1.SteerCustomType != loopdetector.SteerTypeWarning || out1.SteerText == "" {
		t.Errorf("first steer = %q/%q, want the warning steer", out1.SteerCustomType, out1.SteerText)
	}
	out2 := adapter.Observe(turn)
	if out2.Verdict != agent.VerdictBlock {
		t.Fatalf("second observe verdict = %v, want block", out2.Verdict)
	}
	if out2.SteerCustomType != loopdetector.SteerTypeForceStop || out2.SteerText == "" {
		t.Errorf("second steer = %q/%q, want the force-stop steer", out2.SteerCustomType, out2.SteerText)
	}
}
