package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/packages"
)

func pkgTestManifestNoAgents(id, version, owner, repo string, files map[string]string) packages.Manifest {
	m := pkgTestManifest(id, version, owner, repo, files)
	m.Contents = map[string]string{"skills": "skills", "config": "config"}
	return m
}

func TestPkgSetActiveVersionAndErrors(t *testing.T) {
	files := map[string]string{"skills/quick.md": "# quick"}
	m := pkgTestManifest("mypkg", "v1.0.0", "digitalygo", "mypkg", files)
	storeDir := t.TempDir()
	d := pkgTestDeps(storeDir)
	d.FetchArchive = fixtureFetch(t, m, files)
	if err := runPkgInstall([]string{"digitalygo/mypkg@v1.0.0", "--yes"}, d); err != nil {
		t.Fatalf("install: %v", err)
	}

	if err := runPkgActivate([]string{"mypkg", "--version", "v1.0.0"}, d); err != nil {
		t.Fatalf("activate --version: %v", err)
	}
	if err := runPkgActivate([]string{"mypkg", "--version", "nope"}, d); err == nil {
		t.Fatal("activate with an invalid version succeeded")
	}
	if err := runPkgActivate([]string{}, d); err == nil {
		t.Fatal("activate without an id succeeded")
	}
	if err := runPkgDeactivate([]string{"mypkg", "--version", "v9.9.9"}, d); err == nil {
		t.Fatal("deactivate of a missing version succeeded")
	}
	if err := runPkgActivate([]string{"mypkg", "extra"}, d); err == nil {
		t.Fatal("activate with an extra positional succeeded")
	}
}

func TestPkgUninstallDeclineAndConfirm(t *testing.T) {
	files := map[string]string{"skills/quick.md": "# quick"}
	m := pkgTestManifest("mypkg", "v1.0.0", "digitalygo", "mypkg", files)
	storeDir := t.TempDir()
	d := pkgTestDeps(storeDir)
	d.FetchArchive = fixtureFetch(t, m, files)
	if err := runPkgInstall([]string{"digitalygo/mypkg@v1.0.0", "--yes"}, d); err != nil {
		t.Fatalf("install: %v", err)
	}

	d.Stdin = strings.NewReader("n\n")
	if err := runPkgUninstall([]string{"mypkg"}, d); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	store, err := packages.Open(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := store.Index()
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Installed) != 1 {
		t.Fatalf("installed = %d, want the package still installed after declining", len(idx.Installed))
	}

	d.Stdin = strings.NewReader("y\n")
	if err := runPkgUninstall([]string{"mypkg", "--version", "v1.0.0"}, d); err != nil {
		t.Fatalf("uninstall confirmed: %v", err)
	}
	idx, err = store.Index()
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Installed) != 0 {
		t.Fatalf("installed = %d, want 0 after uninstalling", len(idx.Installed))
	}
	if err := runPkgUninstall([]string{}, d); err == nil {
		t.Fatal("uninstall without an id succeeded")
	}
}

func TestPkgUninstallInactiveWithoutPromptPath(t *testing.T) {
	files := map[string]string{"skills/quick.md": "# quick"}
	m := pkgTestManifest("mypkg", "v1.0.0", "digitalygo", "mypkg", files)
	storeDir := t.TempDir()
	d := pkgTestDeps(storeDir)
	d.FetchArchive = fixtureFetch(t, m, files)
	d.Stdin = strings.NewReader("n\n")
	if err := runPkgInstall([]string{"digitalygo/mypkg@v1.0.0"}, d); err != nil {
		t.Fatalf("install: %v", err)
	}
	d.Stdin = strings.NewReader("y\n")
	if err := runPkgUninstall([]string{"mypkg"}, d); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
}

func TestPkgListInspectErrors(t *testing.T) {
	d := pkgTestDeps(t.TempDir())
	if err := runPkgList([]string{"extra"}, d); err == nil {
		t.Fatal("pkg list with a positional succeeded")
	}
	if err := runPkgInspect([]string{}, d); err == nil {
		t.Fatal("pkg inspect without an id succeeded")
	}
	if err := runPkgInspect([]string{"ghost"}, d); err == nil {
		t.Fatal("pkg inspect of an unknown id succeeded")
	}
	if err := runPkgVerify([]string{"ghost"}, d); err == nil {
		t.Fatal("pkg verify of an unknown id succeeded")
	}
}

func TestPkgInspectJSONAndWithoutAgents(t *testing.T) {
	files := map[string]string{"skills/quick.md": "# quick", "config/env": "K=1\n"}
	m := pkgTestManifestNoAgents("mypkg", "v1.0.0", "digitalygo", "mypkg", files)
	storeDir := t.TempDir()
	d := pkgTestDeps(storeDir)
	d.FetchArchive = fixtureFetch(t, m, files)
	if err := runPkgInstall([]string{"digitalygo/mypkg@v1.0.0", "--yes"}, d); err != nil {
		t.Fatalf("install: %v", err)
	}

	var out bytes.Buffer
	d.Stdout = &out
	if err := runPkgInspect([]string{"mypkg", "--json"}, d); err != nil {
		t.Fatalf("inspect --json: %v", err)
	}
	var info pkgInfo
	if err := json.Unmarshal(out.Bytes(), &info); err != nil {
		t.Fatalf("inspect --json output is not valid JSON: %v\n%s", err, out.String())
	}
	if info.ID != "mypkg" || !info.Active || len(info.Agents) != 0 {
		t.Fatalf("info = %+v", info)
	}

	out.Reset()
	if err := runPkgInspect([]string{"mypkg"}, d); err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if strings.Contains(out.String(), "deferred") {
		t.Fatalf("inspect stdout = %q, want no agent lines for a package without agents", out.String())
	}
}

func TestPkgVerifyVersionMismatch(t *testing.T) {
	files := map[string]string{"skills/quick.md": "# quick"}
	m := pkgTestManifest("mypkg", "v1.0.0", "digitalygo", "mypkg", files)
	storeDir := t.TempDir()
	d := pkgTestDeps(storeDir)
	d.FetchArchive = fixtureFetch(t, m, files)
	if err := runPkgInstall([]string{"digitalygo/mypkg@v1.0.0", "--yes"}, d); err != nil {
		t.Fatalf("install: %v", err)
	}
	var out bytes.Buffer
	d.Stdout = &out
	if err := runPkgVerify([]string{"mypkg", "--version", "v1.0.0"}, d); err != nil {
		t.Fatalf("verify --version: %v", err)
	}
	if !strings.Contains(out.String(), "mypkg@v1.0.0 ok") {
		t.Fatalf("verify = %q", out.String())
	}
}

func TestDependencyRowsMinimumVersion(t *testing.T) {
	rows := dependencyRows([]packages.Dependency{
		{ID: "dep-a", Owner: "o", Repo: "r", MinimumVersion: "v1.0.0"},
		{ID: "dep-b", Owner: "o", Repo: "r", ExactVersion: "v2.0.0"},
	})
	if len(rows) != 2 || rows[0].ID != "dep-a" || rows[0].Version != ">=v1.0.0" || rows[1].Version != "v2.0.0" {
		t.Fatalf("rows = %+v", rows)
	}
	if got := dependencyRows(nil); len(got) != 0 {
		t.Fatalf("dependencyRows(nil) = %+v", got)
	}
}

func TestStatusWordAndShortCommit(t *testing.T) {
	if statusWord(true) != "active" || statusWord(false) != "installed" {
		t.Fatal("statusWord mapping wrong")
	}
	if got := shortCommit("0123456789abcdef"); got != "0123456789ab" {
		t.Fatalf("shortCommit = %q", got)
	}
	if got := shortCommit("short"); got != "short" {
		t.Fatalf("shortCommit = %q", got)
	}
}

func TestGithubFetchArchiveServerError(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/repos/") {
			json.NewEncoder(w).Encode(map[string]any{"object": map[string]any{"type": "commit", "sha": "abc"}})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer api.Close()
	oldAPI, oldCodeload := githubAPIBase, codeloadBase
	githubAPIBase = api.URL
	codeloadBase = api.URL
	defer func() { githubAPIBase, codeloadBase = oldAPI, oldCodeload }()

	_, _, err := githubFetchArchive("o", "r", "v1.0.0")
	if err == nil || !strings.Contains(err.Error(), "missing, private or rate-limited") {
		t.Fatalf("error = %v, want the download failure note", err)
	}
}

func TestGithubFetchArchiveUnreachable(t *testing.T) {
	oldAPI, oldCodeload := githubAPIBase, codeloadBase
	githubAPIBase = "http://127.0.0.1:1"
	codeloadBase = "http://127.0.0.1:1"
	defer func() { githubAPIBase, codeloadBase = oldAPI, oldCodeload }()
	if _, _, err := githubFetchArchive("o", "r", "v1.0.0"); err == nil {
		t.Fatal("githubFetchArchive succeeded against an unreachable host")
	}
}

func TestResolveTagToCommitServerErrorAndEmpty(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/err500") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte("not json"))
	}))
	defer api.Close()
	old := githubAPIBase
	githubAPIBase = api.URL
	defer func() { githubAPIBase = old }()

	if _, err := resolveTagToCommit("o", "r", "err500"); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("error = %v, want the status note", err)
	}
	if _, err := resolveTagToCommit("o", "r", "badjson"); err == nil {
		t.Fatal("resolveTagToCommit accepted invalid JSON")
	}
}

func TestDerefTagObjectErrorPaths(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/missing"):
			w.WriteHeader(http.StatusNotFound)
		case strings.Contains(r.URL.Path, "/empty"):
			json.NewEncoder(w).Encode(map[string]any{"object": map[string]any{}})
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer api.Close()
	old := githubAPIBase
	githubAPIBase = api.URL
	defer func() { githubAPIBase = old }()

	if _, err := derefTagObject("o", "r", "missing"); err == nil {
		t.Fatal("derefTagObject succeeded on 404")
	}
	if _, err := derefTagObject("o", "r", "empty"); err == nil {
		t.Fatal("derefTagObject succeeded with an empty sha")
	}
	if _, err := derefTagObject("o", "r", "err"); err == nil {
		t.Fatal("derefTagObject succeeded on 500")
	}
}

func TestGithubLatestVersionErrorPaths(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/notfound"):
			w.WriteHeader(http.StatusNotFound)
		case strings.Contains(r.URL.Path, "/badjson"):
			w.Write([]byte("nope"))
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer api.Close()
	old := githubAPIBase
	githubAPIBase = api.URL
	defer func() { githubAPIBase = old }()

	for _, repo := range []string{"notfound", "badjson", "err"} {
		if _, err := githubLatestVersion("o", repo); err == nil {
			t.Fatalf("githubLatestVersion(%s) succeeded", repo)
		}
	}
}

func TestPkgInstallFlagErrors(t *testing.T) {
	d := pkgTestDeps(t.TempDir())
	if err := runPkgInstall([]string{"--nope"}, d); err == nil {
		t.Fatal("install with an unknown flag succeeded")
	}
	if err := runPkgUpdate([]string{"--nope"}, d); err == nil {
		t.Fatal("update with an unknown flag succeeded")
	}
	if err := runPkgInspect([]string{"--nope"}, d); err == nil {
		t.Fatal("inspect with an unknown flag succeeded")
	}
}
