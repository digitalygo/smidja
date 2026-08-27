package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/session"
)

func continueDeps(t *testing.T, cwd, sessDir string, client agent.Client) (*Deps, *bytes.Buffer) {
	t.Helper()
	cfg := testConfig(t, cwd)
	cfg.SessionDir = sessDir
	store, err := session.NewStore(sessDir)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	deps := &Deps{
		Env:    envFrom(map[string]string{"OPENROUTER_API_KEY": "sk-test"}),
		Getwd:  func() (string, error) { return cwd, nil },
		Home:   func() string { return "/home/tester" },
		Stdin:  strings.NewReader(""),
		Stdout: &stdout,
		Stderr: &stderr,
		Config: cfg,
		Store:  store,
		Client: client,
		Tools:  []agent.Tool{&probeTool{calls: new(int)}},
	}
	return deps, &stderr
}

func seedSessionFile(t *testing.T, store *session.Store, cwd string) string {
	t.Helper()
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"seed"`), Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.PersistRuntimeProfile(session.CurrentProfile{
		ProviderID: "openrouter", ModelID: "test/model",
		SystemPromptSHA256: "sp-seed", ToolSchemasCanonicalJSONSHA256: "ts-seed",
		OrderingVersion: 1, ContentFingerprint: "fp-seed", AffinityKey: "workspace:" + cwd,
	}, nil); err != nil {
		t.Fatal(err)
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	return sess.Path()
}

func TestContinueAppendsToSameSessionFile(t *testing.T) {
	cwd := t.TempDir()
	sessDir := t.TempDir()
	store, err := session.NewStore(sessDir)
	if err != nil {
		t.Fatal(err)
	}
	path := seedSessionFile(t, store, cwd)
	client := &fakeClient{script: []*agent.AssistantMessage{textStop("first reply"), textStop("second reply")}}
	deps, stderr := continueDeps(t, cwd, sessDir, client)

	if err := RunWithDeps([]string{"-p", "first", "-continue", path, "-model", "test/model"}, deps); err != nil {
		t.Fatalf("first continue run: %v (stderr %q)", err, stderr.String())
	}
	prefix, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(prefix), `"first reply"`) {
		t.Fatalf("first continue run did not record the reply:\n%s", prefix)
	}

	deps2, stderr2 := continueDeps(t, cwd, sessDir, client)
	if err := RunWithDeps([]string{"-p", "second", "-continue", path, "-model", "test/model"}, deps2); err != nil {
		t.Fatalf("second continue run: %v (stderr %q)", err, stderr2.String())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, prefix) {
		t.Fatal("second continue run did not preserve the prefix bytes")
	}
	if !strings.Contains(string(data), `"second reply"`) {
		t.Fatalf("second continue run did not append the reply:\n%s", data)
	}

	var files []string
	filepath.WalkDir(sessDir, func(p string, de os.DirEntry, err error) error {
		if err == nil && !de.IsDir() && strings.HasSuffix(de.Name(), ".jsonl") {
			files = append(files, p)
		}
		return nil
	})
	if len(files) != 1 {
		t.Fatalf("session files = %d, want 1 (continue must reopen, not create)", len(files))
	}
}

func TestContinueProfileMismatchResetsOnce(t *testing.T) {
	cwd := t.TempDir()
	sessDir := t.TempDir()
	store, err := session.NewStore(sessDir)
	if err != nil {
		t.Fatal(err)
	}
	path := seedSessionFile(t, store, cwd)

	client := &fakeClient{script: []*agent.AssistantMessage{textStop("ok"), textStop("ok again")}}
	deps, stderr := continueDeps(t, cwd, sessDir, client)
	if err := RunWithDeps([]string{"-p", "hello", "-continue", path, "-model", "other/model"}, deps); err != nil {
		t.Fatalf("continue with a different model: %v (stderr %q)", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "runtime profile changed") {
		t.Errorf("stderr = %q, want the cache-reset notice", stderr.String())
	}
	if n := countProfileEntries(t, path); n != 2 {
		t.Fatalf("profile entries after mismatch = %d, want 2 (initial + reset)", n)
	}

	deps2, stderr2 := continueDeps(t, cwd, sessDir, client)
	if err := RunWithDeps([]string{"-p", "hello again", "-continue", path, "-model", "other/model"}, deps2); err != nil {
		t.Fatalf("continue with the matching model: %v (stderr %q)", err, stderr2.String())
	}
	if strings.Contains(stderr2.String(), "runtime profile changed") {
		t.Errorf("stderr = %q, want no reset notice on a matching profile", stderr2.String())
	}
	if n := countProfileEntries(t, path); n != 2 {
		t.Fatalf("profile entries after matching run = %d, want still 2", n)
	}
}

func TestContinueUnknownPathFails(t *testing.T) {
	cwd := t.TempDir()
	deps, _ := continueDeps(t, cwd, t.TempDir(), &fakeClient{})
	if err := RunWithDeps([]string{"-p", "hello", "-continue", "/no/such/session.jsonl", "-model", "test/model"}, deps); err == nil {
		t.Fatal("continue with a missing session: want error")
	}
}

func TestUsageDocumentsContinueAndGateway(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{"-h"}, testDeps("", &stdout, &stderr)); err != nil {
		t.Fatalf("run -h: %v", err)
	}
	usage := stderr.String()
	if !strings.Contains(usage, "-continue path") {
		t.Errorf("usage missing the -continue flag:\n%s", usage)
	}
	if !strings.Contains(usage, "gateway  run the headless gateway") {
		t.Errorf("usage missing the gateway subcommand:\n%s", usage)
	}
}
