package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/sdk"
)

func runSubcommandDeps(t *testing.T, cwd string, script ...*agent.AssistantMessage) (*Deps, *capturingClient, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	client := &capturingClient{script: script}
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	deps := wiringTestDeps(t.TempDir())
	deps.Getwd = func() (string, error) { return cwd, nil }
	deps.Client = client
	deps.Config = testConfig(t, cwd)
	deps.Store = wiringStore(t)
	deps.Stdout = stdout
	deps.Stderr = stderr
	return deps, client, stdout, stderr
}

func countSessionFiles(t *testing.T, root string) int {
	t.Helper()
	count := 0
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".jsonl") {
			count++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return count
}

func TestRunSubcommandPositionalPrompt(t *testing.T) {
	cwd := t.TempDir()
	deps, client, stdout, stderr := runSubcommandDeps(t, cwd, textStop("run answer"))
	if err := RunWithDeps([]string{"run", "-model", "test/model", "hello run"}, deps); err != nil {
		t.Fatalf("run: %v (stderr %q)", err, stderr.String())
	}
	if client.lastUserText() != "hello run" {
		t.Fatalf("wire prompt = %q, want the positional prompt", client.lastUserText())
	}
	if !strings.Contains(stdout.String(), "run answer") {
		t.Fatalf("stdout = %q, want the streamed response", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if countSessionFiles(t, deps.Store.Root()) != 1 {
		t.Fatal("run must persist exactly one session")
	}
	transcript := readOnlySession(t, deps.Store.Root())
	if !strings.Contains(transcript, "hello run") || !strings.Contains(transcript, "run answer") {
		t.Fatalf("session transcript missing the turn:\n%s", transcript)
	}
}

func TestRunSubcommandFlagPrompt(t *testing.T) {
	cwd := t.TempDir()
	deps, client, _, stderr := runSubcommandDeps(t, cwd, textStop("flag answer"))
	if err := RunWithDeps([]string{"run", "-p", "hello flags", "-model", "test/model"}, deps); err != nil {
		t.Fatalf("run: %v (stderr %q)", err, stderr.String())
	}
	if client.lastUserText() != "hello flags" {
		t.Fatalf("wire prompt = %q", client.lastUserText())
	}
}

func TestRunSubcommandFlagsAroundPositional(t *testing.T) {
	for _, args := range [][]string{
		{"run", "-model", "test/model", "hello order"},
		{"run", "hello order", "-model", "test/model"},
	} {
		cwd := t.TempDir()
		deps, client, _, stderr := runSubcommandDeps(t, cwd, textStop("order answer"))
		if err := RunWithDeps(args, deps); err != nil {
			t.Fatalf("run %v: %v (stderr %q)", args, err, stderr.String())
		}
		if client.lastUserText() != "hello order" {
			t.Fatalf("run %v wire prompt = %q", args, client.lastUserText())
		}
	}
}

func TestRunSubcommandRejectsInvalidInvocations(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "both", args: []string{"run", "-p", "one", "two"}, want: "not both"},
		{name: "missing", args: []string{"run"}, want: "a prompt is required"},
		{name: "missing with flags", args: []string{"run", "-model", "test/model"}, want: "a prompt is required"},
		{name: "empty positional", args: []string{"run", ""}, want: "must not be empty"},
		{name: "whitespace positional", args: []string{"run", "   "}, want: "must not be empty"},
		{name: "empty flag", args: []string{"run", "-p", ""}, want: "must not be empty"},
		{name: "extra positionals", args: []string{"run", "one", "two"}, want: "expected exactly one prompt"},
		{name: "unknown flag", args: []string{"run", "-nope", "one"}, want: "flag provided but not defined"},
		{name: "malformed flag", args: []string{"run", "-model"}, want: "flag needs an argument"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cwd := t.TempDir()
			deps, client, _, stderr := runSubcommandDeps(t, cwd)
			err := RunWithDeps(tc.args, deps)
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want %q", err, tc.want)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
			if !strings.Contains(stderr.String(), "usage: smidja run") {
				t.Fatalf("stderr = %q, want the run usage", stderr.String())
			}
			if client.calls != 0 {
				t.Fatalf("client calls = %d, want no turn", client.calls)
			}
			if countSessionFiles(t, deps.Store.Root()) != 0 {
				t.Fatal("rejected invocation must not create a session")
			}
		})
	}
}

func TestRunSubcommandHelp(t *testing.T) {
	for _, arg := range []string{"-h", "--help"} {
		deps, client, _, stderr := runSubcommandDeps(t, t.TempDir())
		if err := RunWithDeps([]string{"run", arg}, deps); err != nil {
			t.Fatalf("run %s: %v", arg, err)
		}
		if !strings.Contains(stderr.String(), "usage: smidja run") {
			t.Fatalf("stderr = %q, want the run usage", stderr.String())
		}
		if !strings.Contains(stderr.String(), "-p prompt") {
			t.Fatalf("stderr = %q, want the flag list", stderr.String())
		}
		if client.calls != 0 {
			t.Fatal("help must not run a turn")
		}
	}
}

func TestRootUsageDescribesRunSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{"-h"}, testDeps("", &stdout, &stderr)); err != nil {
		t.Fatalf("run -h: %v", err)
	}
	usage := stderr.String()
	if !strings.Contains(usage, "run one turn with a prompt and exit") {
		t.Fatalf("usage = %q, want the accurate run description", usage)
	}
	if strings.Contains(usage, "not implemented") {
		t.Fatalf("usage = %q, must not describe run as unimplemented", usage)
	}
}

func TestRunSubcommandRejectsInvalidRendererFlags(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "tui mode", args: []string{"run", "-tui-mode", "bogus", "hello"}, want: "invalid --tui-mode"},
		{name: "theme", args: []string{"run", "-use-theme", "a/b/c", "hello"}, want: "--use-theme"},
		{name: "invalid boolean", args: []string{"run", "-version=maybe", "hello"}, want: "invalid boolean value"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps, client, _, stderr := runSubcommandDeps(t, t.TempDir())
			err := RunWithDeps(tc.args, deps)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
			if client.calls != 0 {
				t.Fatalf("client calls = %d, want no turn", client.calls)
			}
		})
	}
}

func TestRunSubcommandVersion(t *testing.T) {
	old := Version
	Version = "9.9.9"
	defer func() { Version = old }()

	deps, client, stdout, _ := runSubcommandDeps(t, t.TempDir())
	if err := RunWithDeps([]string{"run", "-version"}, deps); err != nil {
		t.Fatalf("run -version: %v", err)
	}
	if stdout.String() != "smidja 9.9.9\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if client.calls != 0 {
		t.Fatal("version must not run a turn")
	}
}

func TestRunSubcommandContinueResumesSession(t *testing.T) {
	cwd := t.TempDir()
	deps, client, _, stderr := runSubcommandDeps(t, cwd, textStop("continued answer"))
	sess, err := createLockedSession(deps.Store, cwd)
	if err != nil {
		t.Fatal(err)
	}
	path := sess.Path()
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	if err := RunWithDeps([]string{"run", "-model", "test/model", "-continue", path, "next prompt"}, deps); err != nil {
		t.Fatalf("run -continue: %v (stderr %q)", err, stderr.String())
	}
	if client.lastUserText() != "next prompt" {
		t.Fatalf("wire prompt = %q", client.lastUserText())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "next prompt") || !strings.Contains(string(data), "continued answer") {
		t.Fatalf("session transcript missing the continued turn:\n%s", data)
	}
	if countSessionFiles(t, deps.Store.Root()) != 1 {
		t.Fatal("run -continue must reuse the existing session")
	}
}

func TestRunSubcommandExpandsPromptTemplate(t *testing.T) {
	cwd := t.TempDir()
	deps, client, stdout, stderr := runSubcommandDeps(t, cwd, textStop("template answer"))
	deps.Bundle = sdk.Bundle{
		ID: "test-bundle",
		FS: fstest.MapFS{
			"content/prompts/greet.md": {Data: []byte("hello $1 from $ARGUMENTS")},
		},
	}
	if err := RunWithDeps([]string{"run", "-model", "test/model", "/prompt greet Alice"}, deps); err != nil {
		t.Fatalf("run: %v (stderr %q)", err, stderr.String())
	}
	want := "hello Alice from Alice"
	if client.lastUserText() != want {
		t.Fatalf("wire prompt = %q, want %q", client.lastUserText(), want)
	}
	if !strings.Contains(stdout.String(), "template answer") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	transcript := readOnlySession(t, deps.Store.Root())
	if !strings.Contains(transcript, want) {
		t.Fatalf("session transcript missing the expanded prompt:\n%s", transcript)
	}
	if strings.Contains(transcript, "/prompt greet") {
		t.Fatalf("session transcript kept the raw invocation:\n%s", transcript)
	}
}

func readOnlySession(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		b.Write(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}
