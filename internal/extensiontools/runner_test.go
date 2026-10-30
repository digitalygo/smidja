package extensiontools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/privateconfig"
)

func TestExtensionHelper(t *testing.T) {
	if os.Getenv("SMIDJA_EXTENSION_HELPER") != "1" {
		return
	}
	split := 0
	for i, arg := range os.Args {
		if arg == "--" {
			split = i + 1
			break
		}
	}
	args := os.Args[split:]
	switch args[0] {
	case "echo":
		data, _ := io.ReadAll(os.Stdin)
		json.NewEncoder(os.Stdout).Encode(map[string]any{"argv": args[1:], "stdin": string(data), "allowed": os.Getenv("ALLOWED"), "secret": os.Getenv("SHOULD_NOT_LEAK")})
	case "fail":
		fmt.Fprint(os.Stdout, "partial content")
		fmt.Fprint(os.Stderr, "separate diagnostic")
		os.Exit(7)
	case "flood":
		fmt.Fprint(os.Stdout, strings.Repeat("x", maximumOutput*2))
		fmt.Fprint(os.Stderr, strings.Repeat("y", maximumOutput*2))
	case "sleep":
		time.Sleep(5 * time.Second)
	case "tree", "orphan":
		executable, _ := os.Executable()
		command := exec.Command(executable, "-test.run=TestExtensionHelper", "--", "child", args[1])
		command.Env = os.Environ()
		if err := command.Start(); err != nil {
			os.Exit(3)
		}
		os.WriteFile(args[1]+".ready", []byte("ready"), 0600)
		if args[0] == "orphan" {
			os.Exit(0)
		}
		time.Sleep(5 * time.Second)
	case "child":
		time.Sleep(700 * time.Millisecond)
		os.WriteFile(args[1], []byte("survived"), 0600)
	}
	os.Exit(0)
}
func helperDefinition(t *testing.T, mode string, args ...string) privateconfig.Extension {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := append([]string{executable, "-test.run=TestExtensionHelper", "--", mode}, args...)
	return privateconfig.Extension{Name: "example", Command: command, Parameters: json.RawMessage(`{"type":"object"}`)}
}
func helperRunner(t *testing.T) *Runner {
	return New(nil, []string{"SMIDJA_EXTENSION_HELPER=1", "GORACE=atexit_sleep_ms=0", "ALLOWED=explicit", "GOCOVERDIR=" + t.TempDir()})
}
func TestLiteralArgumentsJSONInputAndExplicitEnvironment(t *testing.T) {
	t.Setenv("SHOULD_NOT_LEAK", "private-token")
	r := helperRunner(t)
	literal := "$(touch /never-run); $HOME && echo injected"
	input := json.RawMessage("{\n \"literal\":\"$(not-shell)\",\"integer\":9007199254740993\n}")
	result := r.ExecuteDefinition(context.Background(), helperDefinition(t, "echo", literal, "a b"), input)
	if !result.OK {
		t.Fatal(result)
	}
	output := result.Data.(ExecutionResult)
	var data struct {
		Argv                   []string
		Stdin, Allowed, Secret string
	}
	if err := json.Unmarshal([]byte(output.Content), &data); err != nil {
		t.Fatal(err)
	}
	if data.Stdin != string(input) || len(data.Argv) != 2 || data.Argv[0] != literal || data.Argv[1] != "a b" || data.Secret != "" || data.Allowed != "explicit" {
		t.Fatal(data)
	}
	if output.Diagnostics != "" || output.ExitCode != 0 || output.TimedOut || result.Truncated {
		t.Fatal(output, result)
	}
}
func TestBoundedSeparateStreamsAndExitFailure(t *testing.T) {
	r := helperRunner(t)
	failed := r.ExecuteDefinition(context.Background(), helperDefinition(t, "fail"), json.RawMessage(`{}`))
	output := failed.Data.(ExecutionResult)
	if failed.OK || failed.Code != "process_failed" || output.ExitCode != 7 || output.Content != "partial content" || output.Diagnostics != "separate diagnostic" {
		t.Fatal(failed)
	}
	flooded := r.ExecuteDefinition(context.Background(), helperDefinition(t, "flood"), json.RawMessage(`{}`))
	output = flooded.Data.(ExecutionResult)
	if !flooded.OK || !flooded.Truncated || len(output.Content) != maximumOutput || len(output.Diagnostics) != maximumOutput {
		t.Fatal("unbounded result", flooded.OK, flooded.Truncated, len(output.Content), len(output.Diagnostics))
	}
}
func TestExtensionValidationAndMissingDependency(t *testing.T) {
	r := helperRunner(t)
	definition := helperDefinition(t, "echo")
	for _, args := range []string{"null", "[]", "{", `{} {}`, strings.Repeat(" ", maximumInput+1)} {
		if result := r.ExecuteDefinition(context.Background(), definition, json.RawMessage(args)); result.OK || result.Code != "invalid_arguments" {
			t.Fatal(result)
		}
	}
	for _, command := range [][]string{nil, {" "}} {
		bad := definition
		bad.Command = command
		if result := r.ExecuteDefinition(context.Background(), bad, json.RawMessage(`{}`)); result.Code != "invalid_definition" {
			t.Fatal(result)
		}
	}
	wrong := definition
	wrong.ClientID = "another-machine"
	if result := r.ExecuteDefinition(context.Background(), wrong, json.RawMessage(`{}`)); result.Code != "wrong_target" {
		t.Fatal(result)
	}
	for _, command := range []string{"missing-extension-binary-849124", "/nonexistent/extension-helper"} {
		missing := definition
		missing.Command = []string{command}
		if result := r.ExecuteDefinition(context.Background(), missing, json.RawMessage(`{}`)); result.Code != "missing_dependency" {
			t.Fatal(result)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result := r.ExecuteDefinition(ctx, definition, json.RawMessage(`{}`)); result.Code != "cancelled" {
		t.Fatal(result)
	}
}
func TestTimeoutKillsProcessGroup(t *testing.T) {
	r := helperRunner(t)
	r.timeout = 20 * time.Millisecond
	start := time.Now()
	result := r.ExecuteDefinition(context.Background(), helperDefinition(t, "sleep"), json.RawMessage(`{}`))
	if result.OK || result.Code != "cancelled" || !result.Data.(ExecutionResult).TimedOut || time.Since(start) > time.Second {
		t.Fatal(result)
	}
	marker := filepath.Join(t.TempDir(), "child-effect")
	r = helperRunner(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	var code string
	go func() {
		result := r.ExecuteDefinition(ctx, helperDefinition(t, "tree", marker), json.RawMessage(`{}`))
		code = result.Code
		close(done)
	}()
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(marker + ".ready"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("helper child not started")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("process group cancellation hung")
	}
	if code != "cancelled" {
		t.Fatal(code)
	}
	time.Sleep(800 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("descendant survived cancellation", err)
	}
}
func TestConfiguredPathEnvironmentCopyAndBaseTools(t *testing.T) {
	dir := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(executable, filepath.Join(dir, "myhelper")); err != nil {
		t.Fatal(err)
	}
	environment := []string{"PATH=" + dir, "SMIDJA_EXTENSION_HELPER=1", "GORACE=atexit_sleep_ms=0", "ALLOWED=original", "GOCOVERDIR=" + t.TempDir()}
	r := New(nil, environment)
	environment[3] = "ALLOWED=mutated"
	definition := helperDefinition(t, "echo")
	definition.Command[0] = "myhelper"
	result := r.ExecuteDefinition(context.Background(), definition, json.RawMessage(`{}`))
	if !result.OK || !strings.Contains(result.Data.(ExecutionResult).Content, `"allowed":"original"`) {
		t.Fatal(result)
	}
	path := filepath.Join(dir, "text")
	os.WriteFile(path, []byte("hello"), 0600)
	args, _ := json.Marshal(map[string]string{"path": path})
	if result = r.Execute(context.Background(), "read", args); !result.OK {
		t.Fatal(result)
	}
	if _, err = executablePathForTest(dir); err == nil {
		t.Fatal("directory accepted as executable")
	}
}
func executablePathForTest(dir string) (string, error) {
	return executable(filepath.Base(dir), []string{"PATH=" + filepath.Dir(dir)})
}

func TestSuccessfulHelperCannotLeaveBackgroundChildren(t *testing.T) {
	r := helperRunner(t)
	marker := filepath.Join(t.TempDir(), "unexpected-effect")
	result := r.ExecuteDefinition(context.Background(), helperDefinition(t, "orphan", marker), json.RawMessage(`{}`))
	if !result.OK {
		t.Fatal(result)
	}
	time.Sleep(800 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("background child outlived invocation", err)
	}
}

func TestWithEnvironmentIsScopedCopiedAndReplacesValues(t *testing.T) {
	base := helperRunner(t)
	values := map[string]string{"ALLOWED": "per-tool-value", "NEW_EXPLICIT": "configured"}
	scoped := base.WithEnvironment(values)
	values["ALLOWED"] = "mutated-after-copy"
	if scoped == base || scoped.base != base.base {
		t.Fatal("incorrect runner clone")
	}
	result := scoped.ExecuteDefinition(context.Background(), helperDefinition(t, "echo"), json.RawMessage(`{}`))
	if !result.OK || !strings.Contains(result.Data.(ExecutionResult).Content, `"allowed":"per-tool-value"`) {
		t.Fatal(result)
	}
	original := base.ExecuteDefinition(context.Background(), helperDefinition(t, "echo"), json.RawMessage(`{}`))
	if !original.OK || !strings.Contains(original.Data.(ExecutionResult).Content, `"allowed":"explicit"`) {
		t.Fatal("base environment changed", original)
	}
	scoped.environment[0] = "MUTATED=only-clone"
	if base.environment[0] == scoped.environment[0] {
		t.Fatal("environment slice aliased")
	}
	empty := base.WithEnvironment(nil)
	if len(empty.environment) != len(base.environment) {
		t.Fatal("nil overlay lost base environment")
	}
}
