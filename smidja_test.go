package smidja_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/digitalygo/smidja"
	"github.com/digitalygo/smidja/sdk"
)

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

func validBundle() sdk.Bundle {
	return sdk.Bundle{
		ID:     "digitalygo",
		Origin: "github.com/digitalygo/smidja",
	}
}

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

func TestRunCorruptPackagesIndexFailsCompose(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".smidja", "packages"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".smidja", "packages", "index.json"), []byte("{bad json"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	read := captureStream(t, &os.Stderr)
	code := smidja.Run(context.Background(), validBundle(), validInfo(), []string{"-version"})
	if code == 0 {
		t.Fatal("Run succeeded despite a corrupt packages index")
	}
	if !strings.Contains(read(), "packages") {
		t.Errorf("stderr = %q, want the packages store error", read())
	}
}
