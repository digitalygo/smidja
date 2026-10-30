package managerpackages

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func hash(data []byte) string { value := sha256.Sum256(data); return hex.EncodeToString(value[:]) }
func fixture(version string) (Manifest, map[string][]byte) {
	files := map[string][]byte{"teams/review.json": []byte(`{"id":"example.coding/reviewer","model":"configured"}`), "skills/review.md": []byte("Review code"), "bin/tool.sh": []byte("#!/bin/sh\ncat\n")}
	m := Manifest{SchemaVersion: 1, ID: "example.coding", Version: version, Publisher: "Example", RequiresSmidja: ">=1.0.0", Targets: []string{"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64"}, Scopes: []string{"manager", "client"}, Teams: []string{"teams/review.json"}, Skills: []string{"skills/review.md"}, Configuration: map[string]Configuration{"ODOO_API_KEY": {Secret: true}}}
	for _, name := range []string{"teams/review.json", "skills/review.md", "bin/tool.sh"} {
		m.Files = append(m.Files, File{Path: name, SHA256: hash(files[name]), Executable: name == "bin/tool.sh"})
	}
	return m, files
}
func archiveBytes(t *testing.T, m Manifest, files map[string][]byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	manifest, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range files {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		file.Write(data)
	}
	file, err := writer.Create(ManifestFilename)
	if err != nil {
		t.Fatal(err)
	}
	file.Write(manifest)
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
func packageStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(Config{Dir: t.TempDir(), Version: "1.2.0", Target: "linux/amd64", Scope: "manager"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.Close()
		filepath.Walk(s.cfg.Dir, func(path string, info os.FileInfo, err error) error {
			if err == nil && info.IsDir() {
				return os.Chmod(path, 0700)
			}
			return err
		})
	})
	return s
}
func installFixture(t *testing.T, s *Store, version string) Revision {
	t.Helper()
	m, files := fixture(version)
	data := archiveBytes(t, m, files)
	r, err := s.InstallArchive(Source{URL: "https://packages.example/release.json"}, Release{ID: m.ID, Version: m.Version, SHA256: hash(data)}, data)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestAtomicActivationPinsOldRevisionAndDetectsChanges(t *testing.T) {
	s := packageStore(t)
	first := installFixture(t, s, "1.0.0")
	second := installFixture(t, s, "1.1.0")
	active, err := s.Active(first.ID)
	if err != nil || active.Version != second.Version {
		t.Fatal(active, err)
	}
	pinned, err := s.GetPinned(first.ID, first.Version, first.Digest)
	if err != nil || !reflect.DeepEqual(first, pinned) {
		t.Fatal("old revision lost", err)
	}
	content, err := s.ReadFile(pinned, "skills/review.md")
	if err != nil || string(content) != "Review code" {
		t.Fatal(err)
	}
	resolved, err := s.Resolve(first)
	if err != nil || len(resolved) != 1 {
		t.Fatal(err)
	}
	first.Manifest.Teams[0] = "mutated"
	listed := s.List()
	if len(listed) != 1 || listed[0].Manifest.Teams[0] != "teams/review.json" {
		t.Fatal("mutable active catalog")
	}
	if _, err = s.ReadFile(second, "../secret"); err == nil {
		t.Fatal("path escaped")
	}
	path := filepath.Join(s.revisionPath(second.ID, second.Version, second.Digest), "payload", "skills/review.md")
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0444 {
		t.Fatal("payload writable")
	}
	os.Chmod(path, 0644)
	os.WriteFile(path, []byte("external modification"), 0644)
	if _, err = s.ReadFile(second, "skills/review.md"); err == nil {
		t.Fatal("external edit not detected")
	}
	os.WriteFile(path, content, 0644)
	os.Chmod(path, 0444)
	cfg := s.cfg
	s.Close()
	reopened, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restored, err := reopened.Active(second.ID)
	if err != nil || restored.Digest != second.Digest {
		t.Fatal("active replay failed", err)
	}
}
func TestInvalidUpdateLeavesPreviousActive(t *testing.T) {
	s := packageStore(t)
	first := installFixture(t, s, "1.0.0")
	cases := []func(*Manifest, map[string][]byte){
		func(m *Manifest, f map[string][]byte) { m.Files[0].SHA256 = strings.Repeat("0", 64) },
		func(m *Manifest, f map[string][]byte) { f["extra"] = []byte("unlisted") },
		func(m *Manifest, f map[string][]byte) { delete(f, "bin/tool.sh") },
		func(m *Manifest, f map[string][]byte) { f["../escape"] = []byte("escape") },
		func(m *Manifest, f map[string][]byte) { m.Teams = []string{"absent"} },
		func(m *Manifest, f map[string][]byte) { m.Targets = []string{"linux/armv7"} },
		func(m *Manifest, f map[string][]byte) { m.RequiresSmidja = ">=9.0.0" },
		func(m *Manifest, f map[string][]byte) { f["teams/review.json"] = []byte(`{"id":"other/team"}`) },
	}
	for _, mutate := range cases {
		m, f := fixture("1.1.0")
		mutate(&m, f)
		data := archiveBytes(t, m, f)
		_, err := s.InstallArchive(first.Source, Release{ID: m.ID, Version: m.Version, SHA256: hash(data)}, data)
		if err == nil {
			t.Fatal("invalid update activated")
		}
		active, _ := s.Active(first.ID)
		if active.Digest != first.Digest {
			t.Fatal("active replaced by failed update")
		}
	}
	m, f := fixture("0.9.0")
	data := archiveBytes(t, m, f)
	if _, err := s.InstallArchive(first.Source, Release{ID: m.ID, Version: m.Version, SHA256: hash(data)}, data); err == nil {
		t.Fatal("downgrade accepted")
	}
	m, f = fixture("1.0.0")
	f["skills/review.md"] = []byte("changed")
	m.Files[1].SHA256 = hash(f["skills/review.md"])
	data = archiveBytes(t, m, f)
	if _, err := s.InstallArchive(first.Source, Release{ID: m.ID, Version: m.Version, SHA256: hash(data)}, data); err == nil {
		t.Fatal("version rewritten")
	}
	m, f = fixture("1.1.0")
	data = archiveBytes(t, m, f)
	if _, err := s.InstallArchive(Source{URL: "https://other.example/release"}, Release{ID: m.ID, Version: m.Version, SHA256: hash(data)}, data); err == nil {
		t.Fatal("publisher source changed")
	}
	if _, err := s.InstallArchive(first.Source, Release{SHA256: "invalid"}, data); err == nil {
		t.Fatal("bad archive digest")
	}
	if _, err := s.InstallArchive(first.Source, Release{ID: "other", Version: m.Version, SHA256: hash(data)}, data); err == nil {
		t.Fatal("identity mismatch")
	}
}
func TestManifestValidation(t *testing.T) {
	baseline, _ := fixture("1.0.0")
	cases := []func(*Manifest){
		func(m *Manifest) { m.SchemaVersion = 0 }, func(m *Manifest) { m.ID = "../bad" }, func(m *Manifest) { m.Publisher = "" }, func(m *Manifest) { m.Version = "latest" }, func(m *Manifest) { m.RequiresSmidja = "*" }, func(m *Manifest) { m.RequiresSmidja = ">=1" }, func(m *Manifest) { m.Targets = nil }, func(m *Manifest) { m.Scopes = []string{"invalid"} }, func(m *Manifest) { m.Scopes = []string{"client"} }, func(m *Manifest) { m.Files = append(m.Files, m.Files[0]) }, func(m *Manifest) { m.Files[0].Path = "/absolute" }, func(m *Manifest) { m.Skills = m.Teams }, func(m *Manifest) { m.Configuration = map[string]Configuration{"bad-name": {}} }, func(m *Manifest) {
			m.Dependencies = []Dependency{{ID: m.ID, Version: "1.0.0", Digest: strings.Repeat("0", 64)}}
		}, func(m *Manifest) {
			m.Dependencies = []Dependency{{ID: "other", Version: "bad", Digest: strings.Repeat("0", 64)}}
		},
	}
	for _, mutate := range cases {
		m := copyValue(baseline)
		mutate(&m)
		if err := validateManifest(m, "1.2.0", "linux/amd64", "manager"); err == nil {
			t.Fatal("invalid manifest accepted", m)
		}
	}
	for _, name := range []string{"../escape", "x/../escape", "x\\bad", "/absolute", "x:bad", "", "."} {
		if validPath(name) {
			t.Fatal("bad path", name)
		}
	}
	for _, pair := range [][2]string{{"1.1.0", "1.0.0"}, {"2.0.0", "1.9.9"}, {"1.0.1", "1.0.0"}} {
		if comparison, err := compareVersion(pair[0], pair[1]); err != nil || comparison != 1 {
			t.Fatal(err)
		}
	}
	if _, err := compareVersion("999999999999999999999999.0.0", "1.0.0"); err == nil {
		t.Fatal("overflow version accepted")
	}
}
func TestArchiveRefusesLinksDuplicatesAndInvalidData(t *testing.T) {
	for _, kind := range []string{"link", "duplicate", "directory"} {
		var buffer bytes.Buffer
		w := zip.NewWriter(&buffer)
		h := &zip.FileHeader{Name: "item"}
		if kind == "link" {
			h.SetMode(os.ModeSymlink | 0777)
		}
		if kind == "directory" {
			h.SetMode(os.ModeDir | 0755)
		}
		f, _ := w.CreateHeader(h)
		f.Write([]byte("target"))
		if kind == "duplicate" {
			f, _ = w.Create("item")
			f.Write([]byte("again"))
		}
		w.Close()
		if _, _, err := unpack(buffer.Bytes()); err == nil {
			t.Fatal("unsafe zip accepted", kind)
		}
	}
	if _, _, err := unpack([]byte("not a ZIP")); err == nil {
		t.Fatal("invalid ZIP")
	}
}
func TestStoreLockAndCorruption(t *testing.T) {
	s := packageStore(t)
	if _, err := Open(s.cfg); err == nil {
		t.Fatal("second writer accepted")
	}
	cfg := s.cfg
	s.Close()
	s.Close()
	os.WriteFile(filepath.Join(cfg.Dir, "active.json"), []byte(`{`), 0600)
	if _, err := Open(cfg); err == nil {
		t.Fatal("corrupt index accepted")
	}
	os.WriteFile(filepath.Join(cfg.Dir, "active.json"), []byte(`null`), 0600)
	if _, err := Open(cfg); err == nil {
		t.Fatal("null index accepted")
	}
	for _, change := range []func(*Config){func(c *Config) { c.Dir = "relative" }, func(c *Config) { c.Target = "linux/armv7" }, func(c *Config) { c.Scope = "other" }, func(c *Config) { c.Version = "dev" }} {
		bad := cfg
		change(&bad)
		if _, err := Open(bad); err == nil {
			t.Fatal("bad store config")
		}
	}
	if _, err := s.Active("missing"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if _, err := s.GetPinned("../bad", "1.0.0", strings.Repeat("0", 64)); err == nil {
		t.Fatal("bad pin")
	}
	if _, err := s.GetPinned("missing", "1.0.0", strings.Repeat("0", 64)); err == nil {
		t.Fatal("missing pin")
	}
	m, f := fixture("1.0.0")
	data := archiveBytes(t, m, f)
	if _, err := s.InstallArchive(Source{}, Release{}, data); err == nil {
		t.Fatal("closed store accepted install")
	}
}
func TestMissingDependencyDoesNotBlockInstallButBlocksResolution(t *testing.T) {
	s := packageStore(t)
	m, f := fixture("1.0.0")
	m.Dependencies = []Dependency{{ID: "missing.package", Version: "1.0.0", Digest: strings.Repeat("a", 64)}}
	data := archiveBytes(t, m, f)
	revision, err := s.InstallArchive(Source{URL: "https://packages.example/release.json"}, Release{ID: m.ID, Version: m.Version, SHA256: hash(data)}, data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Resolve(revision); err == nil || !strings.Contains(err.Error(), "dependency missing.package") {
		t.Fatal("missing dependency hidden", err)
	}
}

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(data []byte, status int) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(data))}
}
func TestPrivateSourceUpdatesWithoutApprovalAndRetainsPins(t *testing.T) {
	s := packageStore(t)
	var mu sync.Mutex
	version := "1.0.0"
	requests := 0
	archives := map[string][]byte{}
	for _, v := range []string{"1.0.0", "1.1.0"} {
		m, f := fixture(v)
		archives[v] = archiveBytes(t, m, f)
	}
	s.cfg.LookupEnv = func(name string) string {
		if name == "REGISTRY_TOKEN" {
			return "private-secret"
		}
		return ""
	}
	s.cfg.HTTPClient = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		requests++
		if r.Host == "packages.example" {
			if r.Header.Get("Authorization") != "Bearer private-secret" {
				t.Error("source auth missing")
			}
			release := Release{ID: "example.coding", Version: version, ArchiveURL: "https://cdn.example/archive.zip", SHA256: hash(archives[version])}
			data, _ := json.Marshal(release)
			return response(data, 200), nil
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("credential leaked cross origin")
		}
		return response(archives[version], 200), nil
	})}
	first, err := s.Install(context.Background(), Source{URL: "https://packages.example/release", TokenEnv: "REGISTRY_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := s.Update(context.Background(), first.ID)
	if err != nil || unchanged.Digest != first.Digest {
		t.Fatal(err)
	}
	mu.Lock()
	version = "1.1.0"
	mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	reports := make(chan map[string]error, 2)
	done := make(chan struct{})
	go func() {
		s.RunUpdates(ctx, time.Millisecond, func(result map[string]error) { reports <- result })
		close(done)
	}()
	for i := 0; i < 2; i++ {
		select {
		case result := <-reports:
			if result[first.ID] != nil {
				t.Fatal(result)
			}
		case <-time.After(time.Second):
			t.Fatal("periodic updater did not run")
		}
	}
	cancel()
	<-done
	active, _ := s.Active(first.ID)
	if active.Version != "1.1.0" {
		t.Fatal("update not activated")
	}
	if _, err = s.GetPinned(first.ID, first.Version, first.Digest); err != nil {
		t.Fatal("old pin lost", err)
	}
	receipt, _ := os.ReadFile(filepath.Join(s.cfg.Dir, "active.json"))
	if bytes.Contains(receipt, []byte("private-secret")) {
		t.Fatal("credential persisted")
	}
	cancelled, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	s.RunUpdates(cancelled, 0, nil)
	if result := s.CheckUpdates(cancelled); result[first.ID] == nil {
		t.Fatal("cancellation hidden")
	}
}
func TestSourceAndDownloadErrors(t *testing.T) {
	s := packageStore(t)
	for _, raw := range []string{"http://example.test/release", "https://user:secret@example.test/release", "https://example.test/release?token=secret", "https://example.test/release#fragment", "invalid"} {
		if _, err := sourceURL(raw); err == nil {
			t.Fatal("unsafe source accepted")
		}
	}
	source := Source{URL: "https://packages.example/release", TokenEnv: "MISSING"}
	if _, err := s.fetch(context.Background(), source.URL, source, 100); err == nil {
		t.Fatal("missing credential")
	}
	source.TokenEnv = ""
	for _, status := range []int{302, 401, 500} {
		s.cfg.HTTPClient = &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
			return response([]byte("sensitive upstream details"), status), nil
		})}
		if _, err := s.Install(context.Background(), source); err == nil || strings.Contains(err.Error(), "sensitive") {
			t.Fatal("HTTP error unsafe", err)
		}
	}
	s.cfg.HTTPClient = &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) { return nil, errors.New("transport secret") })}
	if _, err := s.Install(context.Background(), source); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal(err)
	}
	for _, body := range []string{`{`, `{}`, `{"id":"ok","version":"bad","sha256":"bad"}`} {
		s.cfg.HTTPClient = &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) { return response([]byte(body), 200), nil })}
		if _, err := s.Install(context.Background(), source); err == nil {
			t.Fatal("bad release accepted")
		}
	}
	s.cfg.HTTPClient = &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) { return response([]byte("12345"), 200), nil })}
	if _, err := s.fetch(context.Background(), source.URL, source, 3); err == nil {
		t.Fatal("oversized download accepted")
	}
}

func TestInterruptedActivationReusesVerifiedRevision(t *testing.T) {
	s := packageStore(t)
	m, files := fixture("1.0.0")
	data := archiveBytes(t, m, files)
	source := Source{URL: "https://packages.example/release.json"}
	release := Release{ID: m.ID, Version: m.Version, SHA256: hash(data)}
	first, err := s.InstallArchive(source, release, data)
	if err != nil {
		t.Fatal(err)
	}
	identical, err := s.InstallArchive(source, release, data)
	if err != nil || identical.Digest != first.Digest {
		t.Fatal("idempotent install failed", err)
	}
	s.active = map[string]Revision{}
	if err = atomicJSON(filepath.Join(s.cfg.Dir, "active.json"), s.active); err != nil {
		t.Fatal(err)
	}
	recovered, err := s.InstallArchive(source, release, data)
	if err != nil || recovered.Digest != first.Digest {
		t.Fatal("interrupted activation not recovered", err)
	}
	path := filepath.Join(s.revisionPath(first.ID, first.Version, first.Digest), "payload", "skills/review.md")
	os.Chmod(path, 0644)
	os.WriteFile(path, []byte("tampered"), 0644)
	if _, err = s.InstallArchive(source, release, data); err == nil {
		t.Fatal("modified package returned as valid")
	}
	cfg := s.cfg
	s.Close()
	if _, err = Open(cfg); err == nil {
		t.Fatal("startup accepted modified active payload")
	}
}
func TestDependencyPinUsesOldInstalledVersion(t *testing.T) {
	s := packageStore(t)
	dep := installFixture(t, s, "1.0.0")
	installFixture(t, s, "1.1.0")
	m, files := fixture("1.0.0")
	m.ID = "example.consumer"
	files["teams/review.json"] = []byte(`{"id":"example.consumer/reviewer"}`)
	m.Files[0].SHA256 = hash(files["teams/review.json"])
	m.Dependencies = []Dependency{{ID: dep.ID, Version: dep.Version, Digest: dep.Digest}}
	data := archiveBytes(t, m, files)
	consumer, err := s.InstallArchive(Source{URL: "https://packages.example/consumer.json"}, Release{ID: m.ID, Version: m.Version, SHA256: hash(data)}, data)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := s.Resolve(consumer)
	if err != nil || len(resolved) != 2 || resolved[0].Version != "1.0.0" || resolved[1].ID != consumer.ID {
		t.Fatal("dependency not pinned", resolved, err)
	}
}
func TestUpdateRejectsChangedIdentityDigestAndDowngrade(t *testing.T) {
	s := packageStore(t)
	first := installFixture(t, s, "1.0.0")
	for _, release := range []Release{{ID: "other", Version: "1.1.0"}, {ID: first.ID, Version: "bad"}, {ID: first.ID, Version: "0.9.0"}, {ID: first.ID, Version: "1.0.0", SHA256: strings.Repeat("a", 64)}} {
		data, _ := json.Marshal(release)
		s.cfg.HTTPClient = &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) { return response(data, 200), nil })}
		if _, err := s.Update(context.Background(), first.ID); err == nil {
			t.Fatal("invalid update accepted", release)
		}
	}
	if _, err := s.Update(context.Background(), "missing"); err == nil {
		t.Fatal("unknown package updated")
	}
	s.cfg.HTTPClient = &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) { return response([]byte(`{`), 200), nil })}
	if _, err := s.Update(context.Background(), first.ID); err == nil {
		t.Fatal("invalid JSON update accepted")
	}
}

func TestDownloadRedirectsStripCredentialsPermanently(t *testing.T) {
	for _, private := range []bool{false, true} {
		t.Run(map[bool]string{false: "public", true: "private"}[private], func(t *testing.T) {
			s := packageStore(t)
			source := Source{URL: "https://github.example/release.json"}
			if private {
				source.TokenEnv = "TOKEN"
			}
			s.cfg.LookupEnv = func(string) string { return "secret-bearer" }
			visited := []string{}
			s.cfg.HTTPClient = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
				visited = append(visited, r.URL.Host+r.URL.Path)
				if len(visited) == 1 && private && r.Header.Get("Authorization") != "Bearer secret-bearer" {
					t.Error("initial private auth missing")
				}
				if len(visited) > 1 && r.Header.Get("Authorization") != "" {
					t.Error("credential leaked after cross-origin redirect")
				}
				if len(visited) == 1 {
					result := response(nil, 302)
					result.Header.Set("Location", "https://cdn.example/object?signature=private-signed-query")
					return result, nil
				}
				if len(visited) == 2 {
					if r.URL.RawQuery != "signature=private-signed-query" {
						t.Error("signed query lost")
					}
					result := response(nil, 302)
					result.Header.Set("Location", "https://github.example/final")
					return result, nil
				}
				return response([]byte("archive bytes"), 200), nil
			})}
			data, err := s.fetch(context.Background(), source.URL, source, 100)
			if err != nil || string(data) != "archive bytes" || len(visited) != 3 {
				t.Fatal(string(data), visited, err)
			}
		})
	}
}
func TestDownloadSameOriginRedirectAndUnsafeChains(t *testing.T) {
	s := packageStore(t)
	source := Source{URL: "https://github.example/release", TokenEnv: "TOKEN"}
	s.cfg.LookupEnv = func(string) string { return "private" }
	count := 0
	s.cfg.HTTPClient = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		count++
		if r.Header.Get("Authorization") != "Bearer private" {
			t.Error("same-origin auth lost")
		}
		if count == 1 {
			result := response(nil, 302)
			result.Header.Set("Location", "/download")
			return result, nil
		}
		return response([]byte("ok"), 200), nil
	})}
	if _, err := s.fetch(context.Background(), source.URL, source, 10); err != nil {
		t.Fatal(err)
	}
	for _, location := range []string{"http://cdn.example/plain?secret=signed", "https://user:private@cdn.example/file", "https://cdn.example/file#private"} {
		s.cfg.HTTPClient = &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
			result := response(nil, 302)
			result.Header.Set("Location", location)
			return result, nil
		})}
		_, err := s.fetch(context.Background(), source.URL, source, 10)
		if err == nil || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "signed") {
			t.Fatal("unsafe redirect or leaked error", err)
		}
	}
	count = 0
	s.cfg.HTTPClient = &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
		count++
		result := response(nil, 302)
		result.Header.Set("Location", "https://github.example/loop")
		return result, nil
	})}
	if _, err := s.fetch(context.Background(), source.URL, source, 10); err == nil || count != 6 {
		t.Fatal("redirect loop not bounded", count, err)
	}
}

func TestVerifiedFilePath(t *testing.T) {
	store := packageStore(t)
	revision := installFixture(t, store, "1.0.0")
	name := revision.Manifest.Files[0].Path
	path, err := store.FilePath(revision, name)
	if err != nil || !filepath.IsAbs(path) {
		t.Fatal(path, err)
	}
	if _, err = store.FilePath(revision, "../outside"); err == nil {
		t.Fatal("unlisted path accepted")
	}
}
