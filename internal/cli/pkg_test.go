package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/packages"
)

func pkgTestDeps(storeDir string) *Deps {
	return &Deps{
		Env:    envFrom(map[string]string{"SMIDJA_PACKAGES_DIR": storeDir}),
		Getwd:  func() (string, error) { return "/work", nil },
		Home:   func() string { return "/home/tester" },
		Stdin:  strings.NewReader(""),
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
	}
}

func pkgTestManifest(id, version, owner, repo string, files map[string]string) packages.Manifest {
	entries := make([]packages.FileEntry, 0, len(files))
	for path, content := range files {
		sum := sha256.Sum256([]byte(content))
		entries = append(entries, packages.FileEntry{Path: path, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(content))})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return packages.Manifest{
		SchemaVersion:  0,
		ID:             id,
		Version:        version,
		Owner:          owner,
		Repo:           repo,
		Description:    "cli test package",
		Contents:       map[string]string{"skills": "skills", "agents": "agents", "config": "config"},
		MinimumHarness: "v0.1.0",
		Files:          entries,
	}
}

func pkgArchive(t *testing.T, m packages.Manifest, files map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	addEntry := func(name string, data []byte) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	manifestData, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	addEntry(packages.ManifestFilename, manifestData)
	for path, content := range files {
		addEntry(path, []byte(content))
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "pkg.tar.gz")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func fixtureFetch(t *testing.T, m packages.Manifest, files map[string]string) packages.FetchArchive {
	t.Helper()
	return func(owner, repo, version string) (string, string, error) {
		if owner != m.Owner || repo != m.Repo || version != m.Version {
			return "", "", &os.PathError{Op: "fixture", Path: owner + "/" + repo + "@" + version, Err: os.ErrNotExist}
		}
		return "abc123def456", pkgArchive(t, m, files), nil
	}
}

func TestPkgInstallActivateListVerifyUninstall(t *testing.T) {
	files := map[string]string{
		"skills/quick.md":        "# quick\nusage",
		"agents/orchestrator.md": "# orchestrator\nagent",
		"config/defaults.env":    "SMIDJA_MODEL=package/model\nSMIDJA_EXEC_TIMEOUT_SECS=42\n",
	}
	m := pkgTestManifest("mypkg", "v1.0.0", "digitalygo", "mypkg", files)
	storeDir := t.TempDir()
	d := pkgTestDeps(storeDir)
	d.FetchArchive = fixtureFetch(t, m, files)

	if err := runPkgInstall([]string{"digitalygo/mypkg@v1.0.0", "--yes"}, d); err != nil {
		t.Fatalf("install: %v (stderr %q)", err, d.Stderr.(*bytes.Buffer).String())
	}
	out := d.Stdout.(*bytes.Buffer).String()
	for _, want := range []string{
		"installed mypkg@v1.0.0 (commit abc123def456)",
		"SMIDJA_MODEL: (unset) -> package/model",
		"SMIDJA_EXEC_TIMEOUT_SECS: (unset) -> 42",
		"activated mypkg@v1.0.0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q:\n%s", want, out)
		}
	}

	var listOut bytes.Buffer
	d.Stdout = &listOut
	if err := runPkgList([]string{}, d); err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(listOut.String(), "* mypkg@v1.0.0  digitalygo/mypkg  abc123def456") {
		t.Errorf("list = %q, want the active marker", listOut.String())
	}

	var inspectOut bytes.Buffer
	d.Stdout = &inspectOut
	if err := runPkgInspect([]string{"mypkg"}, d); err != nil {
		t.Fatalf("inspect: %v", err)
	}
	for _, want := range []string{"id: mypkg", "version: v1.0.0", "status: active", "contents: agents=agents", "agent: orchestrator.md (deferred)"} {
		if !strings.Contains(inspectOut.String(), want) {
			t.Errorf("inspect stdout missing %q:\n%s", want, inspectOut.String())
		}
	}

	var verifyOut bytes.Buffer
	d.Stdout = &verifyOut
	if err := runPkgVerify([]string{"mypkg"}, d); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !strings.Contains(verifyOut.String(), "mypkg@v1.0.0 ok") {
		t.Errorf("verify = %q", verifyOut.String())
	}

	var deactOut bytes.Buffer
	d.Stdout = &deactOut
	if err := runPkgDeactivate([]string{"mypkg"}, d); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	if !strings.Contains(deactOut.String(), "deactivated mypkg@v1.0.0") {
		t.Errorf("deactivate = %q", deactOut.String())
	}

	var uninstallOut bytes.Buffer
	d.Stdout = &uninstallOut
	if err := runPkgUninstall([]string{"mypkg", "--yes"}, d); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if !strings.Contains(uninstallOut.String(), "uninstalled mypkg@v1.0.0") {
		t.Errorf("uninstall = %q", uninstallOut.String())
	}

	var emptyList bytes.Buffer
	d.Stdout = &emptyList
	if err := runPkgList([]string{}, d); err != nil {
		t.Fatalf("list after uninstall: %v", err)
	}
	if !strings.Contains(emptyList.String(), "no packages installed") {
		t.Errorf("list after uninstall = %q", emptyList.String())
	}
}

func TestPkgInstallDeclineLeavesInactive(t *testing.T) {
	files := map[string]string{"skills/quick.md": "# quick"}
	m := pkgTestManifest("mypkg", "v1.0.0", "digitalygo", "mypkg", files)
	storeDir := t.TempDir()
	d := pkgTestDeps(storeDir)
	d.FetchArchive = fixtureFetch(t, m, files)
	d.Stdin = strings.NewReader("n\n")

	if err := runPkgInstall([]string{"digitalygo/mypkg@v1.0.0"}, d); err != nil {
		t.Fatalf("install: %v", err)
	}
	out := d.Stdout.(*bytes.Buffer).String()
	if !strings.Contains(out, "not activated") {
		t.Errorf("stdout = %q, want the not-activated note", out)
	}
	if !strings.Contains(out, "config defaults: none") {
		t.Errorf("stdout = %q, want the empty config diff", out)
	}
	store, err := packages.Open(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	active, err := store.Active()
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 {
		t.Fatalf("active = %v, want none after declining", active)
	}
}

func TestPkgInstallConfirmActivates(t *testing.T) {
	files := map[string]string{"skills/quick.md": "# quick"}
	m := pkgTestManifest("mypkg", "v1.0.0", "digitalygo", "mypkg", files)
	storeDir := t.TempDir()
	d := pkgTestDeps(storeDir)
	d.FetchArchive = fixtureFetch(t, m, files)
	d.Stdin = strings.NewReader("y\n")

	if err := runPkgInstall([]string{"digitalygo/mypkg@v1.0.0"}, d); err != nil {
		t.Fatalf("install: %v", err)
	}
	if !strings.Contains(d.Stdout.(*bytes.Buffer).String(), "activated mypkg@v1.0.0") {
		t.Fatalf("stdout = %q", d.Stdout.(*bytes.Buffer).String())
	}
}

func TestPkgInstallInvalidSpec(t *testing.T) {
	d := pkgTestDeps(t.TempDir())
	for _, spec := range []string{"noatversion", "owner/repo", "owner/@v1.0.0", "owner/repo@nope", "/repo@v1.0.0"} {
		if err := runPkgInstall([]string{spec, "--yes"}, d); err == nil {
			t.Errorf("install %q succeeded, want error", spec)
		}
	}
	if err := runPkgInstall([]string{}, d); err == nil {
		t.Error("install without positional succeeded")
	}
}

func TestPkgInstallPinMismatchFails(t *testing.T) {
	files := map[string]string{"skills/quick.md": "# quick"}
	m := pkgTestManifest("mypkg", "v1.0.0", "digitalygo", "mypkg", files)
	d := pkgTestDeps(t.TempDir())
	d.FetchArchive = fixtureFetch(t, m, files)
	err := runPkgInstall([]string{"digitalygo/mypkg@v1.0.0", "--yes", "--pin", "deadbeef"}, d)
	if err == nil || !strings.Contains(err.Error(), "deadbeef") {
		t.Fatalf("pin mismatch error = %v, want the pin note", err)
	}
}

func TestPkgActivateUnknownId(t *testing.T) {
	d := pkgTestDeps(t.TempDir())
	if err := runPkgActivate([]string{"ghost"}, d); err == nil {
		t.Fatal("activate unknown id succeeded")
	}
}

func TestPkgListJSON(t *testing.T) {
	storeDir := t.TempDir()
	d := pkgTestDeps(storeDir)
	var out bytes.Buffer
	d.Stdout = &out
	if err := runPkgList([]string{"--json"}, d); err != nil {
		t.Fatalf("list --json: %v", err)
	}
	if strings.TrimSpace(out.String()) != "[]" {
		t.Fatalf("list --json = %q, want []", out.String())
	}
}

func TestPkgVerifyCorruptedFileFails(t *testing.T) {
	files := map[string]string{"skills/quick.md": "# quick"}
	m := pkgTestManifest("mypkg", "v1.0.0", "digitalygo", "mypkg", files)
	storeDir := t.TempDir()
	d := pkgTestDeps(storeDir)
	d.FetchArchive = fixtureFetch(t, m, files)
	if err := runPkgInstall([]string{"digitalygo/mypkg@v1.0.0", "--yes"}, d); err != nil {
		t.Fatalf("install: %v", err)
	}
	quickPath := filepath.Join(storeDir, "mypkg", "v1.0.0", "skills", "quick.md")
	if err := os.WriteFile(quickPath, []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runPkgVerify([]string{"mypkg"}, d); err == nil {
		t.Fatal("verify accepted a tampered file")
	}
}

func TestConfigDefaultsDiffPrecedence(t *testing.T) {
	files := map[string]string{"config/env": "SMIDJA_MODEL=package/model\n"}
	m := pkgTestManifest("cfg-pkg", "v1.0.0", "digitalygo", "cfg-pkg", files)
	storeDir := t.TempDir()
	d := pkgTestDeps(storeDir)
	d.FetchArchive = fixtureFetch(t, m, files)
	if err := runPkgInstall([]string{"digitalygo/cfg-pkg@v1.0.0", "--yes"}, d); err != nil {
		t.Fatalf("install: %v", err)
	}
	store, err := packages.Open(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	uninstalled := map[string]string{"SMIDJA_MODEL": "other/model", "SMIDJA_NEW_KEY": "1"}
	rows, err := configDefaultsDiff(d, store, uninstalled)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want the two uninstalled keys", rows)
	}
	if rows[0].Key != "SMIDJA_MODEL" || rows[0].From != "package/model" || rows[0].To != "other/model" {
		t.Fatalf("rows[0] = %+v, want the package chain to override the active package default", rows[0])
	}
	d.Env = envFrom(map[string]string{"SMIDJA_MODEL": "env/model"})
	rows, err = configDefaultsDiff(d, store, uninstalled)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Key != "SMIDJA_NEW_KEY" {
		t.Fatalf("rows with env override = %+v, want only the untouched key", rows)
	}
}

func TestPkgUpdateToNewerVersion(t *testing.T) {
	oldFiles := map[string]string{"skills/quick.md": "# quick v1"}
	oldM := pkgTestManifest("mypkg", "v1.0.0", "digitalygo", "mypkg", oldFiles)
	newFiles := map[string]string{"skills/quick.md": "# quick v2"}
	newM := pkgTestManifest("mypkg", "v1.1.0", "digitalygo", "mypkg", newFiles)
	storeDir := t.TempDir()
	d := pkgTestDeps(storeDir)
	d.FetchArchive = func(owner, repo, version string) (string, string, error) {
		if version == "v1.0.0" {
			return "commit-a", pkgArchive(t, oldM, oldFiles), nil
		}
		if version == "v1.1.0" {
			return "commit-b", pkgArchive(t, newM, newFiles), nil
		}
		return "", "", &os.PathError{Op: "fixture", Path: version, Err: os.ErrNotExist}
	}
	d.FetchLatestVersion = func(owner, repo string) (string, error) {
		return "v1.1.0", nil
	}
	if err := runPkgInstall([]string{"digitalygo/mypkg@v1.0.0", "--yes"}, d); err != nil {
		t.Fatalf("install: %v", err)
	}
	d.Stdout = &bytes.Buffer{}
	if err := runPkgUpdate([]string{"mypkg", "--yes"}, d); err != nil {
		t.Fatalf("update: %v", err)
	}
	out := d.Stdout.(*bytes.Buffer).String()
	if !strings.Contains(out, "updated mypkg v1.0.0 -> v1.1.0 (active)") {
		t.Fatalf("update stdout = %q", out)
	}
	store, err := packages.Open(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	active, err := store.Active()
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].ID != "mypkg" || active[0].Version != "v1.1.0" {
		t.Fatalf("active = %+v, want mypkg@v1.1.0", active)
	}
}

func TestPkgUpdateAlreadyUpToDate(t *testing.T) {
	files := map[string]string{"skills/quick.md": "# quick"}
	m := pkgTestManifest("mypkg", "v1.0.0", "digitalygo", "mypkg", files)
	storeDir := t.TempDir()
	d := pkgTestDeps(storeDir)
	d.FetchArchive = fixtureFetch(t, m, files)
	d.FetchLatestVersion = func(owner, repo string) (string, error) { return "v1.0.0", nil }
	if err := runPkgInstall([]string{"digitalygo/mypkg@v1.0.0", "--yes"}, d); err != nil {
		t.Fatalf("install: %v", err)
	}
	d.Stdout = &bytes.Buffer{}
	if err := runPkgUpdate([]string{"mypkg"}, d); err != nil {
		t.Fatalf("update: %v", err)
	}
	if !strings.Contains(d.Stdout.(*bytes.Buffer).String(), "is up to date") {
		t.Fatalf("update stdout = %q", d.Stdout.(*bytes.Buffer).String())
	}
}

func TestPkgUpdateUnknownId(t *testing.T) {
	d := pkgTestDeps(t.TempDir())
	d.FetchLatestVersion = func(owner, repo string) (string, error) { return "v1.0.0", nil }
	if err := runPkgUpdate([]string{"ghost"}, d); err == nil {
		t.Fatal("update unknown id succeeded")
	}
}

func TestResolveTagToCommitLightweightAndAnnotated(t *testing.T) {
	var api *httptest.Server
	api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r/git/refs/tags/v1.0.0":
			json.NewEncoder(w).Encode(map[string]any{"object": map[string]any{"type": "commit", "sha": "aaa111"}})
		case "/repos/o/r/git/refs/tags/v2.0.0":
			json.NewEncoder(w).Encode(map[string]any{"object": map[string]any{"type": "tag", "sha": "tagobj1"}})
		case "/repos/o/r/git/tags/tagobj1":
			json.NewEncoder(w).Encode(map[string]any{"object": map[string]any{"sha": "bbb222"}})
		case "/repos/o/r/git/refs/tags/missing":
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer api.Close()
	old := githubAPIBase
	githubAPIBase = api.URL
	defer func() { githubAPIBase = old }()

	if got, err := resolveTagToCommit("o", "r", "v1.0.0"); err != nil || got != "aaa111" {
		t.Fatalf("lightweight tag = %q, %v", got, err)
	}
	if got, err := resolveTagToCommit("o", "r", "v2.0.0"); err != nil || got != "bbb222" {
		t.Fatalf("annotated tag = %q, %v", got, err)
	}
	if _, err := resolveTagToCommit("o", "r", "missing"); err == nil || !strings.Contains(err.Error(), "not found or private") {
		t.Fatalf("missing tag error = %v", err)
	}
}

func TestGithubLatestVersionPrefersCanonical(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"name": "release-2024"},
			{"name": "v2.0.0"},
			{"name": "v1.0.0"},
		})
	}))
	defer api.Close()
	old := githubAPIBase
	githubAPIBase = api.URL
	defer func() { githubAPIBase = old }()

	got, err := githubLatestVersion("o", "r")
	if err != nil || got != "v2.0.0" {
		t.Fatalf("latest = %q, %v", got, err)
	}
}

func TestGithubLatestVersionNoTags(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{{"name": "nope"}})
	}))
	defer api.Close()
	old := githubAPIBase
	githubAPIBase = api.URL
	defer func() { githubAPIBase = old }()
	if _, err := githubLatestVersion("o", "r"); err == nil {
		t.Fatal("githubLatestVersion succeeded without canonical tags")
	}
}

func TestGithubFetchArchiveDownloadsByCommit(t *testing.T) {
	payload := []byte("tarball-bytes")
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/o/r/git/refs/tags/v1.0.0" {
			json.NewEncoder(w).Encode(map[string]any{"object": map[string]any{"type": "commit", "sha": "abc123"}})
			return
		}
		w.Write(payload)
	}))
	defer api.Close()
	oldAPI, oldCodeload := githubAPIBase, codeloadBase
	githubAPIBase = api.URL
	codeloadBase = api.URL
	defer func() { githubAPIBase, codeloadBase = oldAPI, oldCodeload }()

	commit, archive, err := githubFetchArchive("o", "r", "v1.0.0")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	defer os.Remove(archive)
	if commit != "abc123" {
		t.Fatalf("commit = %q", commit)
	}
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(payload) {
		t.Fatalf("archive content = %q", data)
	}
}

func TestGithubFetchArchiveNotFound(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer api.Close()
	old := githubAPIBase
	githubAPIBase = api.URL
	defer func() { githubAPIBase = old }()
	_, _, err := githubFetchArchive("o", "r", "v1.0.0")
	if err == nil || !strings.Contains(err.Error(), "not found or private") {
		t.Fatalf("error = %v, want the private-repo note", err)
	}
}

func TestPkgUsagePrints(t *testing.T) {
	var out bytes.Buffer
	d := &Deps{Stdout: &out, Stderr: &bytes.Buffer{}}
	if err := runPkg([]string{"help"}, d); err != nil {
		t.Fatalf("pkg help: %v", err)
	}
	for _, want := range []string{"install", "list", "inspect", "activate", "deactivate", "update", "verify", "uninstall"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("usage missing %q", want)
		}
	}
	if err := runPkg(nil, d); err == nil {
		t.Fatal("pkg without subcommand must fail")
	}
	if err := runPkg([]string{"bogus"}, d); err == nil {
		t.Fatal("pkg bogus must fail")
	}
}

func TestParseOwnerRepoVersion(t *testing.T) {
	owner, repo, version, err := parseOwnerRepoVersion("digitalygo/mypkg@v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if owner != "digitalygo" || repo != "mypkg" || version != "v1.2.3" {
		t.Fatalf("got %s %s %s", owner, repo, version)
	}
	for _, spec := range []string{"", "norepo@v1.0.0", "o/", "o/r@x"} {
		if _, _, _, err := parseOwnerRepoVersion(spec); err == nil {
			t.Errorf("parseOwnerRepoVersion(%q) succeeded", spec)
		}
	}
}

func TestInstallPackageWithDependency(t *testing.T) {
	depFiles := map[string]string{"skills/dep.md": "# dep"}
	depM := pkgTestManifest("dep-pkg", "v1.0.0", "digitalygo", "dep-pkg", depFiles)
	rootFiles := map[string]string{"skills/root.md": "# root"}
	rootM := pkgTestManifest("root-pkg", "v1.0.0", "digitalygo", "root-pkg", rootFiles)
	rootM.Depends = []packages.Dependency{{ID: "dep-pkg", Owner: "digitalygo", Repo: "dep-pkg", ExactVersion: "v1.0.0"}}

	storeDir := t.TempDir()
	d := pkgTestDeps(storeDir)
	d.FetchArchive = func(owner, repo, version string) (string, string, error) {
		switch repo {
		case "dep-pkg":
			return "commit-dep", pkgArchive(t, depM, depFiles), nil
		case "root-pkg":
			return "commit-root", pkgArchive(t, rootM, rootFiles), nil
		}
		return "", "", &os.PathError{Op: "fixture", Path: repo, Err: os.ErrNotExist}
	}
	if err := runPkgInstall([]string{"digitalygo/root-pkg@v1.0.0", "--yes"}, d); err != nil {
		t.Fatalf("install: %v (stderr %q)", err, d.Stderr.(*bytes.Buffer).String())
	}
	store, err := packages.Open(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := store.Index()
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Installed) != 2 {
		t.Fatalf("installed = %d, want 2 (root + dependency)", len(idx.Installed))
	}
	var rootRec packages.InstalledRecord
	for _, rec := range idx.Installed {
		if rec.ID == "root-pkg" {
			rootRec = rec
		}
	}
	if rootRec.ID == "" || len(rootRec.ResolvedDepends) != 1 || rootRec.ResolvedDepends[0].ID != "dep-pkg" {
		t.Fatalf("root record = %+v, want dep-pkg resolved", rootRec)
	}
}

func TestManifestFetchRoundTrip(t *testing.T) {
	files := map[string]string{"skills/quick.md": "# quick"}
	m := pkgTestManifest("mypkg", "v1.0.0", "digitalygo", "mypkg", files)
	fetch := fixtureFetch(t, m, files)
	node, err := manifestFetch(fetch)(packages.Request{Owner: "digitalygo", Repo: "mypkg", ID: "mypkg", Version: "v1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if node.ID != "mypkg" || node.Version != "v1.0.0" || node.Manifest.ID != "mypkg" {
		t.Fatalf("node = %+v", node)
	}
}

func TestPkgStoreRootEnv(t *testing.T) {
	d := &Deps{Env: envFrom(map[string]string{"SMIDJA_PACKAGES_DIR": "/custom"})}
	if got := packageStoreRoot(d); got != "/custom" {
		t.Fatalf("packageStoreRoot = %q", got)
	}
	d = &Deps{Env: envFrom(nil)}
	if got := packageStoreRoot(d); got == "" {
		t.Fatal("packageStoreRoot empty without env")
	}
}
