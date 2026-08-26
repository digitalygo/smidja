package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/digitalygo/smidja/internal/buildinfo"
	"github.com/digitalygo/smidja/internal/update"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

	dest := filepath.Join(sessDir, "--work-dir--", "2026-08-25T00-00-00-000Z_0196b87c-7a2b-7000-8000-000000000002.jsonl")
	if _, err := os.Stat(dest); err != nil {
		t.Errorf("imported destination %q missing: %v", dest, err)
	}

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

func updateTestDeps(srv *httptest.Server, origin buildinfo.Info, execPath func() (string, error), stdout, stderr *bytes.Buffer) *Deps {
	return &Deps{
		Env:    envFrom(nil),
		Getwd:  func() (string, error) { return "/work/dir", nil },
		Home:   func() string { return "/home/tester" },
		Stdin:  strings.NewReader(""),
		Stdout: stdout,
		Stderr: stderr,
		NewUpdateClient: func() *update.Client {
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
