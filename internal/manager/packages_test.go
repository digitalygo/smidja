package manager

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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/managerpackages"
	"github.com/digitalygo/smidja/internal/managerstate"
)

type packageFixtureTransport struct {
	mu      sync.Mutex
	release managerpackages.Release
	archive []byte
	status  int
}

func (f *packageFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	status := f.status
	if status == 0 {
		status = 200
	}
	data := f.archive
	if r.URL.Path == "/release.json" {
		data, _ = json.Marshal(f.release)
	}
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(data))}, nil
}
func (f *packageFixtureTransport) set(version, team string, dependencies []managerpackages.Dependency) {
	f.mu.Lock()
	defer f.mu.Unlock()
	workflow, _ := json.Marshal(managerstate.Workflow{ID: "example.coding/review", Version: "ignored-local", Phases: []managerstate.Phase{{ID: "review", Name: "Review", TeamRef: team}}})
	digest := sha256.Sum256(workflow)
	manifest := managerpackages.Manifest{SchemaVersion: 1, ID: "example.coding", Version: version, Publisher: "Example", RequiresSmidja: ">=1.0.0", Targets: []string{"linux/amd64"}, Scopes: []string{"manager"}, Workflows: []string{"workflows/review.json"}, Files: []managerpackages.File{{Path: "workflows/review.json", SHA256: hex.EncodeToString(digest[:])}}, Dependencies: dependencies}
	data, _ := json.Marshal(manifest)
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	entry, _ := writer.Create(managerpackages.ManifestFilename)
	entry.Write(data)
	entry, _ = writer.Create("workflows/review.json")
	entry.Write(workflow)
	writer.Close()
	f.archive = buffer.Bytes()
	sum := sha256.Sum256(f.archive)
	f.release = managerpackages.Release{ID: manifest.ID, Version: version, ArchiveURL: "https://packages.example/package.zip", SHA256: hex.EncodeToString(sum[:])}
}
func packageFixtureStore(t *testing.T, f *packageFixtureTransport) *managerpackages.Store {
	t.Helper()
	dir := t.TempDir()
	store, err := managerpackages.Open(managerpackages.Config{Dir: dir, Version: "1.0.0", Target: "linux/amd64", Scope: "manager", HTTPClient: &http.Client{Transport: f}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		store.Close()
		filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err == nil && info.IsDir() {
				return os.Chmod(path, 0700)
			}
			return err
		})
	})
	return store
}

type packagePauseRuntime struct{}

func (packagePauseRuntime) Ready(context.Context) error { return nil }
func (packagePauseRuntime) StartPhase(context.Context, managerstate.Task, managerstate.PhaseContext) (PhaseExecution, error) {
	return PhaseExecution{}, errors.New("fixture pauses before any remote call")
}
func (packagePauseRuntime) FollowPhase(context.Context, PhaseExecution) (string, error) {
	return "", errors.New("unexpected follow")
}
func (packagePauseRuntime) Classify(context.Context, managerstate.Task, managerstate.PhaseContext, string) (Classification, error) {
	return Classification{}, errors.New("unexpected classify")
}
func (packagePauseRuntime) Notify(context.Context, managerstate.Task) error { return nil }

func TestPackageAPIActivationPinsTaskWorkflow(t *testing.T) {
	fixture := &packageFixtureTransport{}
	fixture.set("1.0.0", "reviewer-original", nil)
	store := packageFixtureStore(t, fixture)
	s := testServer(t, func(cfg *Config) { cfg.Packages = store; cfg.WorkflowRuntime = packagePauseRuntime{} })
	for _, test := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/api/packages", "", 200}, {"GET", "/api/packages/update-status", "", 200},
		{"POST", "/api/packages/install", `{"url":"https://packages.example/release.json"}`, 201},
		{"POST", "/api/packages/install", `{"url":"https://packages.example/release.json","token":"secret"}`, 400},
		{"POST", "/api/packages/update", `{`, 400}, {"POST", "/api/packages/update", `{"id":"missing"}`, 400},
		{"GET", "/api/packages/unknown", "", 404},
	} {
		response := request(s, test.method, test.path, test.body, ownerHeaders())
		if response.Code != test.status {
			t.Fatalf("%s => %d %s", test.path, response.Code, response.Body)
		}
	}
	available := request(s, "GET", "/api/workflows", "", ownerHeaders())
	var workflows []managerstate.Workflow
	if err := json.Unmarshal(available.Body.Bytes(), &workflows); err != nil || len(workflows) != 1 {
		t.Fatal(available.Body, err)
	}
	original := workflows[0]
	if !strings.HasPrefix(original.Version, "1.0.0+") || original.Phases[0].TeamRef != "reviewer-original" {
		t.Fatal(original)
	}
	body := `{"workflow":{"id":"example.coding/review","version":"forged","phases":[{"id":"bypass","team_ref":"attacker"}]},"inputs":{},"invocation_key":"first"}`
	started := request(s, "POST", "/api/tasks", body, ownerHeaders())
	if started.Code != 202 {
		t.Fatal(started.Body)
	}
	var first managerstate.Task
	if err := json.Unmarshal(started.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if first.Workflow.Version != original.Version || first.Workflow.Phases[0].TeamRef != "reviewer-original" {
		t.Fatal("caller overrode packaged workflow", first)
	}
	fixture.set("1.1.0", "reviewer-new", nil)
	updated := request(s, "POST", "/api/packages/update", `{"id":"example.coding"}`, ownerHeaders())
	if updated.Code != 200 {
		t.Fatal(updated.Body)
	}
	next := request(s, "POST", "/api/tasks", strings.Replace(body, `"first"`, `"second"`, 1), ownerHeaders())
	if next.Code != 202 {
		t.Fatal(next.Body)
	}
	var second managerstate.Task
	json.Unmarshal(next.Body.Bytes(), &second)
	if second.Workflow.Phases[0].TeamRef != "reviewer-new" || !strings.HasPrefix(second.Workflow.Version, "1.1.0+") {
		t.Fatal("new task did not adopt update", second)
	}
	pinned, err := s.cfg.Store.GetTask(first.ID)
	if err != nil || pinned.Workflow.Version != original.Version || pinned.Workflow.Phases[0].TeamRef != "reviewer-original" {
		t.Fatal("old task changed", pinned, err)
	}
	if _, err = s.resolveWorkflow(managerstate.Workflow{ID: "private", Version: "custom"}); err != nil {
		t.Fatal("private workflow rejected", err)
	}
	if _, err = s.resolveWorkflow(managerstate.Workflow{ID: "missing/review"}); err == nil {
		t.Fatal("unknown package workflow accepted")
	}
}
func TestPackageMissingDependenciesUnavailable(t *testing.T) {
	fixture := &packageFixtureTransport{}
	fixture.set("1.0.0", "reviewer", []managerpackages.Dependency{{ID: "missing.package", Version: "1.0.0", Digest: strings.Repeat("a", 64)}})
	store := packageFixtureStore(t, fixture)
	s := testServer(t, func(cfg *Config) { cfg.Packages = store; cfg.WorkflowRuntime = packagePauseRuntime{} })
	installed := request(s, "POST", "/api/packages/install", `{"url":"https://packages.example/release.json"}`, ownerHeaders())
	if installed.Code != 201 {
		t.Fatal(installed.Body)
	}
	workflows, err := s.packageWorkflows()
	if err != nil || len(workflows) != 0 {
		t.Fatal("unresolved dependency exposed", workflows, err)
	}
	attempted := request(s, "POST", "/api/tasks", `{"workflow":{"id":"example.coding/review"},"inputs":{}}`, ownerHeaders())
	if attempted.Code != 400 || !strings.Contains(attempted.Body.String(), "unresolved dependencies") {
		t.Fatal(attempted.Body)
	}
}
func TestPackageAPIUnconfiguredAndUpdaterFailure(t *testing.T) {
	empty := testServer(t, nil)
	if response := request(empty, "GET", "/api/packages", "", ownerHeaders()); response.Code != 503 {
		t.Fatal(response.Body)
	}
	if workflows, err := empty.packageWorkflows(); err != nil || len(workflows) != 0 {
		t.Fatal(workflows, err)
	}
	fixture := &packageFixtureTransport{}
	fixture.set("1.0.0", "reviewer", nil)
	store := packageFixtureStore(t, fixture)
	if _, err := store.Install(context.Background(), managerpackages.Source{URL: "https://packages.example/release.json"}); err != nil {
		t.Fatal(err)
	}
	fixture.mu.Lock()
	fixture.status = 503
	fixture.mu.Unlock()
	s := testServer(t, func(cfg *Config) { cfg.Packages = store })
	deadline := time.Now().Add(time.Second)
	for {
		response := request(s, "GET", "/api/packages/update-status", "", ownerHeaders())
		if strings.Contains(response.Body.String(), "HTTP 503") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("startup update failure not reported", response.Body)
		}
		time.Sleep(time.Millisecond)
	}
	failed := request(s, "POST", "/api/packages/install", `{"url":"https://packages.example/release.json"}`, ownerHeaders())
	if failed.Code != 400 {
		t.Fatal(failed.Body)
	}
	s.Close()
	fixture.mu.Lock()
	fixture.status = 200
	fixture.mu.Unlock()
	if _, err := store.Install(context.Background(), managerpackages.Source{URL: "https://packages.example/release.json"}); err == nil {
		t.Fatal("server did not close owned package store")
	}
}
