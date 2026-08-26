// Package smidja_test exercises the public composition seam end to end:
// Run validates the bundle, wires the harness, and executes the CLI,
// returning the process exit code. The tests capture the real process
// streams because Run is deliberately wired to os.Stdout and os.Stderr;
// they stay network-free, and the -p turn path is covered at the
// internal/cli unit level against an httptest server.
package smidja_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/digitalygo/smidja"
	"github.com/digitalygo/smidja/sdk"
)

// captureStream redirects one process stream to a pipe for the duration
// of the test and returns a function that closes the write end, drains
// the pipe, and returns everything written to the stream. The caller must
// invoke the returned function after the code under test finishes.
func captureStream(t *testing.T, target **os.File) func() string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := *target
	*target = w
	t.Cleanup(func() {
		*target = old
		_ = r.Close()
		_ = w.Close()
	})
	return func() string {
		_ = w.Close()
		b, _ := io.ReadAll(r)
		return string(b)
	}
}

// validBundle is a minimal packaged bundle with the canonical origin
// format.
func validBundle() sdk.Bundle {
	return sdk.Bundle{
		ID:     "digitalygo",
		Origin: "github.com/digitalygo/smidja",
	}
}

// validInfo is a minimal build identity.
func validInfo() sdk.BuildInfo {
	return sdk.BuildInfo{
		Origin:  "github.com/digitalygo/smidja",
		Version: "v0.1.0",
		Commit:  "abc1234",
	}
}

func TestRunVersionUsesBuildInfo(t *testing.T) {
	read := captureStream(t, &os.Stdout)
	code := smidja.Run(context.Background(), validBundle(), validInfo(), []string{"-version"})
	if code != 0 {
		t.Fatalf("Run(-version) = %d, want 0", code)
	}
	if got := read(); got != "smidja v0.1.0\n" {
		t.Errorf("stdout = %q, want the build info version", got)
	}
}

func TestRunVersionSubcommandJSON(t *testing.T) {
	read := captureStream(t, &os.Stdout)
	code := smidja.Run(context.Background(), validBundle(), validInfo(), []string{"version", "--json"})
	if code != 0 {
		t.Fatalf("Run(version --json) = %d, want 0", code)
	}
	want := `{"commit":"abc1234","origin":"github.com/digitalygo/smidja","version":"v0.1.0"}`
	if got := strings.TrimSpace(read()); got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestRunBareHarness(t *testing.T) {
	// The bare harness ships an empty bundle: validation is skipped and
	// the CLI runs with the package-variable fallback version.
	read := captureStream(t, &os.Stdout)
	code := smidja.Run(context.Background(), sdk.Bundle{}, sdk.BuildInfo{}, []string{"-version"})
	if code != 0 {
		t.Fatalf("Run(-version) with an empty bundle = %d, want 0", code)
	}
	if got := read(); !strings.Contains(got, "smidja ") {
		t.Errorf("stdout = %q, want a version line", got)
	}
}

func TestRunInvalidBundle(t *testing.T) {
	t.Run("partial identity", func(t *testing.T) {
		read := captureStream(t, &os.Stderr)
		code := smidja.Run(context.Background(), sdk.Bundle{ID: "digitalygo"}, validInfo(), nil)
		if code != 1 {
			t.Fatalf("Run = %d, want 1", code)
		}
		if got := read(); !strings.Contains(got, "bundle: empty Origin") {
			t.Errorf("stderr = %q, want the empty-origin error", got)
		}
	})
	t.Run("https origin rejected", func(t *testing.T) {
		read := captureStream(t, &os.Stderr)
		code := smidja.Run(context.Background(),
			sdk.Bundle{ID: "digitalygo", Origin: "https://github.com/digitalygo/smidja"},
			validInfo(), nil)
		if code != 1 {
			t.Fatalf("Run = %d, want 1", code)
		}
		if got := read(); !strings.Contains(got, "must be github.com/owner/repo") {
			t.Errorf("stderr = %q, want the origin-format error", got)
		}
	})
	t.Run("malformed origin", func(t *testing.T) {
		read := captureStream(t, &os.Stderr)
		code := smidja.Run(context.Background(),
			sdk.Bundle{ID: "x", Origin: "github.com/owner"},
			validInfo(), nil)
		if code != 1 {
			t.Fatalf("Run = %d, want 1", code)
		}
		if got := read(); !strings.Contains(got, "must be github.com/owner/repo") {
			t.Errorf("stderr = %q, want the origin-format error", got)
		}
	})
}

func TestRunInvalidArgumentsExitCode(t *testing.T) {
	read := captureStream(t, &os.Stderr)
	code := smidja.Run(context.Background(), validBundle(), validInfo(), []string{"-nope"})
	if code != 1 {
		t.Fatalf("Run(-nope) = %d, want 1", code)
	}
	if got := read(); !strings.Contains(got, "smidja: flag provided but not defined: -nope") {
		t.Errorf("stderr = %q, want the flag error", got)
	}
}

// TestRunSingleShotThroughBundle proves bundle consumption is real end to
// end: the harness client is configured entirely from the bundle's
// ConfigDefaults (no environment), so a -p turn against an httptest
// server only works if the bundle defaults actually reached the client.
// The startup model-catalogue refresh is best-effort and non-fatal, so
// the test passes regardless of network availability.
func TestRunSingleShotThroughBundle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)
		fmt.Fprintf(w, "data: %s\n\n", `{"id":"gen_1","choices":[{"index":0,"delta":{"content":"hello from bundle"}}]}`)
		fl.Flush()
		fmt.Fprintf(w, "data: %s\n\n", `{"id":"gen_1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
		fl.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		fl.Flush()
	}))
	t.Cleanup(srv.Close)

	sessDir := t.TempDir()
	t.Chdir(t.TempDir())
	// Empty the real environment so the bundle defaults are the only
	// configuration source.
	for _, k := range []string{"SMIDJA_OPENROUTER_URL", "OPENROUTER_API_KEY", "SMIDJA_SESSION_DIR", "SMIDJA_MODEL"} {
		t.Setenv(k, "")
	}

	bundle := sdk.Bundle{
		ID:     "digitalygo",
		Origin: "github.com/digitalygo/smidja",
		ConfigDefaults: map[string]any{
			"SMIDJA_OPENROUTER_URL": srv.URL,
			"OPENROUTER_API_KEY":    "sk-bundle",
			"SMIDJA_SESSION_DIR":    sessDir,
			"SMIDJA_MODEL":          "bundle/model",
		},
	}

	readOut := captureStream(t, &os.Stdout)
	readErr := captureStream(t, &os.Stderr)
	code := smidja.Run(context.Background(), bundle, validInfo(), []string{"-p", "hello from bundle"})
	if code != 0 {
		t.Fatalf("Run(-p) = %d, want 0 (stderr %q)", code, readErr())
	}
	if got := readOut(); !strings.Contains(got, "hello from bundle") {
		t.Errorf("stdout = %q, want the streamed response", got)
	}
}
