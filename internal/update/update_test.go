package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/buildinfo"
)

// fakeRelease is one release the fake GitHub server can serve.
type fakeRelease struct {
	tag        string
	htmlURL    string
	published  string
	assetNames []string
}

// fakeGitHub is an httptest server that fakes the subset of the GitHub
// releases API the updater uses, plus asset downloads under /downloads/.
type fakeGitHub struct {
	server   *httptest.Server
	releases map[string]*fakeRelease // keyed by "latest" or a tag name
	assets   map[string][]byte       // asset name to content
	paths    []string                // request paths in arrival order
}

// newFakeGitHub serves releases keyed by "latest" or a tag name. Asset
// content is served by name from assets and referenced from the release
// JSON as <server>/downloads/<name>.
func newFakeGitHub(t *testing.T, releases map[string]*fakeRelease, assets map[string][]byte) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{releases: releases, assets: assets}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeGitHub) handle(w http.ResponseWriter, r *http.Request) {
	f.paths = append(f.paths, r.URL.Path)
	if strings.HasPrefix(r.URL.Path, "/downloads/") {
		name := strings.TrimPrefix(r.URL.Path, "/downloads/")
		content, ok := f.assets[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(content)
		return
	}
	// /repos/{owner}/{repo}/releases/latest
	// /repos/{owner}/{repo}/releases/tags/{tag}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/repos/"), "/")
	var key string
	switch {
	case len(parts) == 4 && parts[2] == "releases" && parts[3] == "latest":
		key = "latest"
	case len(parts) == 5 && parts[2] == "releases" && parts[3] == "tags":
		key = parts[4]
	default:
		http.NotFound(w, r)
		return
	}
	rel, ok := f.releases[key]
	if !ok {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	var assets []map[string]string
	for _, name := range rel.assetNames {
		assets = append(assets, map[string]string{
			"name":                 name,
			"browser_download_url": f.server.URL + "/downloads/" + name,
		})
	}
	json.NewEncoder(w).Encode(map[string]any{
		"tag_name":     rel.tag,
		"html_url":     rel.htmlURL,
		"published_at": rel.published,
		"assets":       assets,
	})
}

// saw reports whether a request path was observed.
func (f *fakeGitHub) saw(path string) bool {
	for _, p := range f.paths {
		if p == path {
			return true
		}
	}
	return false
}

// testOrigin is the build identity used by most tests: running v1.0.0
// from the canonical origin.
func testOrigin() buildinfo.Info {
	return buildinfo.Info{Origin: "github.com/digitalygo/smidja", Version: "v1.0.0"}
}

// newClient builds an updater pointed at the fake server.
func newClient(f *fakeGitHub, origin buildinfo.Info) *Client {
	return &Client{BaseURL: f.server.URL, Origin: origin}
}

// applyClient builds an updater whose ExecPath resolves to path.
func applyClient(f *fakeGitHub, path string) *Client {
	c := newClient(f, testOrigin())
	c.ExecPath = func() (string, error) { return path, nil }
	return c
}

// checksums renders a map of filename to hex digest in the standard
// "digest  filename" layout, in sorted filename order.
func checksums(entries map[string]string) []byte {
	names := make([]string, 0, len(entries))
	for n := range entries {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, "%s  %s\n", entries[n], n)
	}
	return []byte(b.String())
}

func sha256hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// standardRelease is the release fixture used by most tests, served for
// both the "latest" endpoint and the explicit v1.2.3 tag.
func standardRelease() map[string]*fakeRelease {
	rel := &fakeRelease{
		tag:        "v1.2.3",
		htmlURL:    "https://github.com/digitalygo/smidja/releases/tag/v1.2.3",
		published:  "2026-08-01T00:00:00Z",
		assetNames: []string{"smidja-linux-amd64", "smidja-linux-arm64", "checksums.txt"},
	}
	return map[string]*fakeRelease{
		"latest": rel,
		"v1.2.3": rel,
	}
}

// applyScratch writes a fake executable and returns its path.
func applyScratch(t *testing.T, content []byte, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "smidja")
	if err := os.WriteFile(path, content, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

// assertBinary asserts that path holds exactly want bytes.
func assertBinary(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("target = %q, want %q", got, want)
	}
}

// assertCleanDir asserts that no temp or lock files were left in dir.
func assertCleanDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".smidja-update-") || strings.HasSuffix(e.Name(), ".update.lock") {
			t.Errorf("leftover file %s", e.Name())
		}
	}
}

func TestCheckLatest(t *testing.T) {
	bin := []byte("amd64 binary")
	assets := map[string][]byte{
		"smidja-linux-amd64": bin,
		"smidja-linux-arm64": []byte("arm64 binary"),
		"checksums.txt":      checksums(map[string]string{"smidja-linux-amd64": sha256hex(bin)}),
	}
	f := newFakeGitHub(t, standardRelease(), assets)
	c := newClient(f, testOrigin())

	got, err := c.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got.Version != "v1.2.3" {
		t.Errorf("Version = %q, want v1.2.3", got.Version)
	}
	if got.URL != "https://github.com/digitalygo/smidja/releases/tag/v1.2.3" {
		t.Errorf("URL = %q", got.URL)
	}
	if got.PublishedAt != "2026-08-01T00:00:00Z" {
		t.Errorf("PublishedAt = %q", got.PublishedAt)
	}
	if got.Asset != "smidja-linux-amd64" {
		t.Errorf("Asset = %q, want smidja-linux-amd64", got.Asset)
	}
	if !got.Available {
		t.Error("Available = false, want true (v1.2.3 > v1.0.0)")
	}
	if !f.saw("/repos/digitalygo/smidja/releases/latest") {
		t.Errorf("latest endpoint not hit, paths = %v", f.paths)
	}
}

func TestCheckReportsNoUpdateWhenCurrentIsNewer(t *testing.T) {
	f := newFakeGitHub(t, standardRelease(), nil)
	c := newClient(f, buildinfo.Info{Origin: "github.com/digitalygo/smidja", Version: "v2.0.0"})

	got, err := c.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got.Available {
		t.Error("Available = true, want false (v1.2.3 < v2.0.0)")
	}
	if got.Version != "v1.2.3" {
		t.Errorf("Version = %q, want v1.2.3", got.Version)
	}
}

func TestCheckSelectsAssetByGOARCH(t *testing.T) {
	f := newFakeGitHub(t, standardRelease(), nil)
	c := newClient(f, testOrigin())
	c.GOARCH = "arm64"

	got, err := c.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got.Asset != "smidja-linux-arm64" {
		t.Errorf("Asset = %q, want smidja-linux-arm64", got.Asset)
	}
}

func TestCheckUnsupportedGOOS(t *testing.T) {
	f := newFakeGitHub(t, standardRelease(), nil)
	c := newClient(f, testOrigin())
	c.GOOS = "darwin"

	_, err := c.Check(context.Background())
	if !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("Check err = %v, want ErrUnsupportedPlatform", err)
	}
	if len(f.paths) != 0 {
		t.Errorf("no request should be made for an unsupported platform, paths = %v", f.paths)
	}
}

func TestCheckInvalidOrigin(t *testing.T) {
	f := newFakeGitHub(t, standardRelease(), nil)
	c := newClient(f, buildinfo.Info{Origin: "github.com/digitalygo"})

	_, err := c.Check(context.Background())
	if !errors.Is(err, ErrInvalidOrigin) {
		t.Fatalf("Check err = %v, want ErrInvalidOrigin", err)
	}
}

func TestCheckAssetNotFound(t *testing.T) {
	rel := map[string]*fakeRelease{
		"latest": {tag: "v1.2.3", htmlURL: "u", published: "p", assetNames: []string{"checksums.txt"}},
	}
	f := newFakeGitHub(t, rel, nil)
	c := newClient(f, testOrigin())
	c.GOARCH = "riscv64"

	_, err := c.Check(context.Background())
	if !errors.Is(err, ErrAssetNotFound) {
		t.Fatalf("Check err = %v, want ErrAssetNotFound", err)
	}
}

func TestCheckNoRelease(t *testing.T) {
	f := newFakeGitHub(t, map[string]*fakeRelease{}, nil)
	c := newClient(f, testOrigin())

	_, err := c.Check(context.Background())
	if !errors.Is(err, ErrNoRelease) {
		t.Fatalf("Check err = %v, want ErrNoRelease", err)
	}
}

func TestApplySwapsBinaryAtomically(t *testing.T) {
	oldBytes := []byte("old binary bytes")
	newBytes := []byte("new binary bytes")
	target := applyScratch(t, oldBytes, 0o755)

	assets := map[string][]byte{
		"smidja-linux-amd64": newBytes,
		"checksums.txt":      checksums(map[string]string{"smidja-linux-amd64": sha256hex(newBytes)}),
	}
	f := newFakeGitHub(t, standardRelease(), assets)
	c := applyClient(f, target)

	if err := c.Apply(context.Background(), "v1.2.3"); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	assertBinary(t, target, newBytes)
	st, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v, want 0755", st.Mode().Perm())
	}
	if !f.saw("/repos/digitalygo/smidja/releases/tags/v1.2.3") {
		t.Errorf("tags endpoint not hit for target version, paths = %v", f.paths)
	}
	if !f.saw("/downloads/smidja-linux-amd64") || !f.saw("/downloads/checksums.txt") {
		t.Errorf("asset downloads missing, paths = %v", f.paths)
	}
	assertCleanDir(t, filepath.Dir(target))
}

func TestApplyUsesLatestWhenNoTargetVersion(t *testing.T) {
	oldBytes := []byte("old")
	newBytes := []byte("new")
	target := applyScratch(t, oldBytes, 0o755)
	assets := map[string][]byte{
		"smidja-linux-amd64": newBytes,
		"checksums.txt":      checksums(map[string]string{"smidja-linux-amd64": sha256hex(newBytes)}),
	}
	f := newFakeGitHub(t, standardRelease(), assets)
	c := applyClient(f, target)

	if err := c.Apply(context.Background(), ""); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !f.saw("/repos/digitalygo/smidja/releases/latest") {
		t.Errorf("latest endpoint not hit, paths = %v", f.paths)
	}
}

func TestApplySelectsAssetByGOARCH(t *testing.T) {
	oldBytes := []byte("old")
	armBytes := []byte("arm64 binary")
	target := applyScratch(t, oldBytes, 0o755)
	assets := map[string][]byte{
		"smidja-linux-arm64": armBytes,
		"checksums.txt":      checksums(map[string]string{"smidja-linux-arm64": sha256hex(armBytes)}),
	}
	f := newFakeGitHub(t, standardRelease(), assets)
	c := applyClient(f, target)
	c.GOARCH = "arm64"

	if err := c.Apply(context.Background(), "v1.2.3"); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	assertBinary(t, target, armBytes)
	if !f.saw("/downloads/smidja-linux-arm64") {
		t.Errorf("arm64 asset not downloaded, paths = %v", f.paths)
	}
}

func TestApplyChecksumMismatchLeavesOldBinary(t *testing.T) {
	oldBytes := []byte("old binary bytes")
	newBytes := []byte("new binary bytes")
	target := applyScratch(t, oldBytes, 0o755)
	assets := map[string][]byte{
		"smidja-linux-amd64": newBytes,
		"checksums.txt":      checksums(map[string]string{"smidja-linux-amd64": strings.Repeat("0", 64)}),
	}
	f := newFakeGitHub(t, standardRelease(), assets)
	c := applyClient(f, target)

	err := c.Apply(context.Background(), "v1.2.3")
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("Apply err = %v, want ErrChecksumMismatch", err)
	}
	assertBinary(t, target, oldBytes)
	assertCleanDir(t, filepath.Dir(target))
}

func TestApplyMalformedChecksums(t *testing.T) {
	oldBytes := []byte("old")
	target := applyScratch(t, oldBytes, 0o755)
	assets := map[string][]byte{
		"smidja-linux-amd64": []byte("new"),
		"checksums.txt":      []byte("this is not a checksums file\nneither is this\n"),
	}
	f := newFakeGitHub(t, standardRelease(), assets)
	c := applyClient(f, target)

	err := c.Apply(context.Background(), "v1.2.3")
	if !errors.Is(err, ErrChecksumEntry) {
		t.Fatalf("Apply err = %v, want ErrChecksumEntry", err)
	}
	assertBinary(t, target, oldBytes)
}

func TestApplyMissingChecksumEntry(t *testing.T) {
	oldBytes := []byte("old")
	target := applyScratch(t, oldBytes, 0o755)
	assets := map[string][]byte{
		"smidja-linux-amd64": []byte("new"),
		"checksums.txt":      checksums(map[string]string{"smidja-linux-arm64": strings.Repeat("ab", 32)}),
	}
	f := newFakeGitHub(t, standardRelease(), assets)
	c := applyClient(f, target)

	err := c.Apply(context.Background(), "v1.2.3")
	if !errors.Is(err, ErrChecksumEntry) {
		t.Fatalf("Apply err = %v, want ErrChecksumEntry", err)
	}
	assertBinary(t, target, oldBytes)
}

func TestApplyMissingChecksumsAsset(t *testing.T) {
	oldBytes := []byte("old")
	target := applyScratch(t, oldBytes, 0o755)
	rel := map[string]*fakeRelease{
		"latest": {tag: "v1.2.3", htmlURL: "u", published: "p", assetNames: []string{"smidja-linux-amd64"}},
	}
	assets := map[string][]byte{"smidja-linux-amd64": []byte("new")}
	f := newFakeGitHub(t, rel, assets)
	c := applyClient(f, target)

	err := c.Apply(context.Background(), "")
	if !errors.Is(err, ErrChecksumsMissing) {
		t.Fatalf("Apply err = %v, want ErrChecksumsMissing", err)
	}
	assertBinary(t, target, oldBytes)
	if f.saw("/downloads/checksums.txt") {
		t.Error("checksums download attempted despite the asset being absent")
	}
}

func TestApplyChecksumsDownloadFailure(t *testing.T) {
	oldBytes := []byte("old")
	target := applyScratch(t, oldBytes, 0o755)
	assets := map[string][]byte{"smidja-linux-amd64": []byte("new")} // checksums.txt listed but not served
	f := newFakeGitHub(t, standardRelease(), assets)
	c := applyClient(f, target)

	err := c.Apply(context.Background(), "v1.2.3")
	if err == nil {
		t.Fatal("Apply succeeded, want checksums download failure")
	}
	assertBinary(t, target, oldBytes)
}

func TestApplyAssetNotFound(t *testing.T) {
	oldBytes := []byte("old")
	target := applyScratch(t, oldBytes, 0o755)
	rel := map[string]*fakeRelease{
		"latest": {tag: "v1.2.3", htmlURL: "u", published: "p", assetNames: []string{"checksums.txt"}},
	}
	f := newFakeGitHub(t, rel, nil)
	c := applyClient(f, target)
	c.GOARCH = "riscv64"

	err := c.Apply(context.Background(), "")
	if !errors.Is(err, ErrAssetNotFound) {
		t.Fatalf("Apply err = %v, want ErrAssetNotFound", err)
	}
	assertBinary(t, target, oldBytes)
}

func TestApplyNoReleaseForTag(t *testing.T) {
	oldBytes := []byte("old")
	target := applyScratch(t, oldBytes, 0o755)
	f := newFakeGitHub(t, standardRelease(), nil)
	c := applyClient(f, target)

	err := c.Apply(context.Background(), "v9.9.9")
	if !errors.Is(err, ErrNoRelease) {
		t.Fatalf("Apply err = %v, want ErrNoRelease", err)
	}
	assertBinary(t, target, oldBytes)
}

func TestApplyLockContention(t *testing.T) {
	oldBytes := []byte("old")
	target := applyScratch(t, oldBytes, 0o755)
	assets := map[string][]byte{
		"smidja-linux-amd64": []byte("new"),
		"checksums.txt":      checksums(map[string]string{"smidja-linux-amd64": sha256hex([]byte("new"))}),
	}
	f := newFakeGitHub(t, standardRelease(), assets)
	c := applyClient(f, target)

	lockPath := target + ".update.lock"
	if err := os.WriteFile(lockPath, []byte("pid=999"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := c.Apply(context.Background(), "v1.2.3")
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("Apply err = %v, want ErrLocked", err)
	}
	assertBinary(t, target, oldBytes)
}

func TestApplyReclaimsStaleLock(t *testing.T) {
	oldBytes := []byte("old")
	newBytes := []byte("new")
	target := applyScratch(t, oldBytes, 0o755)
	assets := map[string][]byte{
		"smidja-linux-amd64": newBytes,
		"checksums.txt":      checksums(map[string]string{"smidja-linux-amd64": sha256hex(newBytes)}),
	}
	f := newFakeGitHub(t, standardRelease(), assets)
	c := applyClient(f, target)

	lockPath := target + ".update.lock"
	if err := os.WriteFile(lockPath, []byte("pid=999"), 0o644); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-2 * staleLockAge)
	if err := os.Chtimes(lockPath, stale, stale); err != nil {
		t.Fatal(err)
	}
	if err := c.Apply(context.Background(), "v1.2.3"); err != nil {
		t.Fatalf("Apply with stale lock: %v", err)
	}
	assertBinary(t, target, newBytes)
}

func TestApplySelfCheckDisabledByDefault(t *testing.T) {
	// The default (SelfCheck false) installs checksum-verified bytes
	// without executing them, so a plain non-executable asset works.
	oldBytes := []byte("old")
	newBytes := []byte("new")
	target := applyScratch(t, oldBytes, 0o755)
	assets := map[string][]byte{
		"smidja-linux-amd64": newBytes,
		"checksums.txt":      checksums(map[string]string{"smidja-linux-amd64": sha256hex(newBytes)}),
	}
	f := newFakeGitHub(t, standardRelease(), assets)
	c := applyClient(f, target)

	if err := c.Apply(context.Background(), "v1.2.3"); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	assertBinary(t, target, newBytes)
}

func TestApplySelfCheckVerifiesBinary(t *testing.T) {
	script := []byte("#!/bin/sh\nprintf '{\"origin\":\"github.com/digitalygo/smidja\",\"version\":\"v1.2.3\",\"commit\":\"abc\"}'\n")
	oldBytes := []byte("old")
	target := applyScratch(t, oldBytes, 0o755)
	assets := map[string][]byte{
		"smidja-linux-amd64": script,
		"checksums.txt":      checksums(map[string]string{"smidja-linux-amd64": sha256hex(script)}),
	}
	f := newFakeGitHub(t, standardRelease(), assets)
	c := applyClient(f, target)
	c.SelfCheck = true

	if err := c.Apply(context.Background(), "v1.2.3"); err != nil {
		t.Fatalf("Apply with SelfCheck: %v", err)
	}
	assertBinary(t, target, script)
}

func TestApplySelfCheckRejectsWrongVersion(t *testing.T) {
	script := []byte("#!/bin/sh\nprintf '{\"origin\":\"github.com/digitalygo/smidja\",\"version\":\"v0.0.1\",\"commit\":\"abc\"}'\n")
	oldBytes := []byte("old")
	target := applyScratch(t, oldBytes, 0o755)
	assets := map[string][]byte{
		"smidja-linux-amd64": script,
		"checksums.txt":      checksums(map[string]string{"smidja-linux-amd64": sha256hex(script)}),
	}
	f := newFakeGitHub(t, standardRelease(), assets)
	c := applyClient(f, target)
	c.SelfCheck = true

	err := c.Apply(context.Background(), "v1.2.3")
	if err == nil {
		t.Fatal("Apply succeeded, want self-check version mismatch")
	}
	assertBinary(t, target, oldBytes)
}
