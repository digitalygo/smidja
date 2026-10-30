package clientpackages

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/extensiontools"
	"github.com/digitalygo/smidja/internal/machineclient"
	"github.com/digitalygo/smidja/internal/managerpackages"
)

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func testStore(t *testing.T) *managerpackages.Store {
	t.Helper()
	dir := t.TempDir()
	store, err := managerpackages.Open(managerpackages.Config{Dir: dir, Version: "1.0.0", Scope: "client", Target: runtime.GOOS + "/" + runtime.GOARCH})
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
func fixture(t *testing.T, store *managerpackages.Store, version string, modify func(map[string][]byte), dependencies ...managerpackages.Dependency) managerpackages.Revision {
	t.Helper()
	files := map[string][]byte{
		"tools/echo.json":     []byte(`{"id":"example/echo","name":"Echo","description":"Read JSON input","parameters":{"type":"object"},"command":["./bin/echo.sh"],"environment":{"SERVICE_KEY":"SERVICE_KEY_ENV"}}`),
		"teams/team.json":     []byte(`{"id":"example/team","name":"Specialist","agents":[{"name":"worker","instructions":"Use echo","tools":["example/echo"]}]}`),
		"workflows/work.json": []byte(`{"id":"example/work","version":"1.0.0","phases":[{"id":"one","team_ref":"example/team"}]}`),
		"skills/help.md":      []byte("Use the tool with care."),
		"bin/echo.sh":         []byte("#!/bin/sh\nprintf '" + version + ":%s:' \"$SERVICE_KEY\"\ncat\n"),
	}
	if modify != nil {
		modify(files)
	}
	manifest := managerpackages.Manifest{SchemaVersion: 1, ID: "example", Version: version, Publisher: "Example", RequiresSmidja: ">=0.0.0", Scopes: []string{"client", "manager"}, Targets: []string{"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64"}, Tools: []string{"tools/echo.json"}, Skills: []string{"skills/help.md"}, Teams: []string{"teams/team.json"}, Workflows: []string{"workflows/work.json"}}
	manifest.Dependencies = dependencies
	for name, data := range files {
		manifest.Files = append(manifest.Files, managerpackages.File{Path: name, SHA256: digest(data), Executable: name == "bin/echo.sh"})
	}
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	raw, _ := json.Marshal(manifest)
	entry, _ := writer.Create(managerpackages.ManifestFilename)
	entry.Write(raw)
	for name, data := range files {
		entry, _ = writer.Create(name)
		entry.Write(data)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	archive := buffer.Bytes()
	revision, err := store.InstallArchive(managerpackages.Source{URL: "https://fixtures.invalid/release"}, managerpackages.Release{ID: "example", Version: version, SHA256: digest(archive)}, archive)
	if err != nil {
		t.Fatal(err)
	}
	return revision
}
func testRuntime(t *testing.T, store *managerpackages.Store) *Runtime {
	t.Helper()
	r, err := New(Config{Store: store, Environment: []string{"PATH=/usr/bin:/bin"}, LookupEnv: func(name string) string {
		if name == "SERVICE_KEY_ENV" {
			return "fixture-secret"
		}
		return ""
	}, ProtectedEnvironment: []string{"CLIENT_TOKEN"}})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestActiveMetadataUpdatesKeepPinnedToolExecutable(t *testing.T) {
	store := testStore(t)
	first := fixture(t, store, "1.0.0", nil)
	r := testRuntime(t, store)
	inventory, err := r.Inventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.ToolDefinitions) != 1 || len(inventory.TeamDefinitions) != 1 || len(inventory.WorkflowDefinitions) != 1 || len(inventory.SkillDefinitions) != 1 || inventory.Packages[0].Version != "1.0.0" {
		t.Fatal(inventory)
	}
	raw, _ := json.Marshal(inventory)
	if strings.Contains(string(raw), "fixture-secret") || strings.Contains(string(raw), "command") || strings.Contains(string(raw), "SERVICE_KEY_ENV") {
		t.Fatal("local implementation or credentials advertised")
	}
	oldRoute := inventory.ToolDefinitions[0].Route
	if oldRoute != Route(first, "example/echo") {
		t.Fatal(oldRoute)
	}
	result := r.Execute(context.Background(), oldRoute, json.RawMessage(`{"value":1}`))
	if !result.OK || !strings.Contains(result.Data.(extensiontools.ExecutionResult).Content, "1.0.0:fixture-secret:") {
		t.Fatal(result)
	}
	second := fixture(t, store, "1.1.0", nil)
	inventory, err = r.Inventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if inventory.ToolDefinitions[0].Package.Version != "1.1.0" || inventory.ToolDefinitions[0].Route != Route(second, "example/echo") {
		t.Fatal(inventory)
	}
	found := false
	for _, route := range inventory.Tools {
		if route == oldRoute {
			found = true
		}
	}
	if !found {
		t.Fatal("old execution route forgotten")
	}
	r = testRuntime(t, store)
	inventory, err = r.Inventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.ToolDefinitions) != 1 || inventory.ToolDefinitions[0].Package.Version != "1.1.0" {
		t.Fatal("retained metadata leaked into active inventory")
	}
	result = r.Execute(context.Background(), oldRoute, json.RawMessage(`{}`))
	if !result.OK || !strings.HasPrefix(result.Data.(extensiontools.ExecutionResult).Content, "1.0.0:") {
		t.Fatal(result)
	}
	result = r.Execute(context.Background(), inventory.ToolDefinitions[0].Route, json.RawMessage(`{}`))
	if !result.OK || !strings.HasPrefix(result.Data.(extensiontools.ExecutionResult).Content, "1.1.0:") {
		t.Fatal(result)
	}
	if result = r.Execute(context.Background(), "package:remote-supplied-command", json.RawMessage(`{}`)); result.OK {
		t.Fatal("unregistered route executed")
	}
	if result = r.Execute(context.Background(), "unknown", json.RawMessage(`{}`)); result.OK {
		t.Fatal("unknown base tool succeeded")
	}
}

func TestPackageEnvironmentErrorsAreRuntimeOnly(t *testing.T) {
	for _, mode := range []string{"missing", "protected", "no-resolver"} {
		t.Run(mode, func(t *testing.T) {
			store := testStore(t)
			fixture(t, store, "1.0.0", nil)
			cfg := Config{Store: store, Environment: []string{"PATH=/bin:/usr/bin"}, LookupEnv: func(string) string { return "" }}
			if mode == "protected" {
				cfg.ProtectedEnvironment = []string{"SERVICE_KEY_ENV"}
				cfg.LookupEnv = func(string) string { return "must-not-be-read" }
			}
			if mode == "no-resolver" {
				cfg.LookupEnv = nil
			}
			r, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			inventory, err := r.Inventory(context.Background())
			if err != nil {
				t.Fatal("missing credentials blocked install", err)
			}
			result := r.Execute(context.Background(), inventory.ToolDefinitions[0].Route, json.RawMessage(`{}`))
			if result.OK || result.Code != "package_error" {
				t.Fatal(result)
			}
		})
	}
}
func TestInvalidInstalledDefinitionsFailClosed(t *testing.T) {
	for _, mode := range []string{"tool-name", "tool-target", "tool-env", "tool-schema", "team", "workflow"} {
		t.Run(mode, func(t *testing.T) {
			store := testStore(t)
			fixture(t, store, "1.0.0", func(files map[string][]byte) {
				switch mode {
				case "tool-name":
					files["tools/echo.json"] = []byte(`{"id":"example/echo"}`)
				case "tool-target":
					files["tools/echo.json"] = bytes.Replace(files["tools/echo.json"], []byte(`"name":"Echo"`), []byte(`"name":"Echo","client_id":"other-laptop"`), 1)
				case "tool-env":
					files["tools/echo.json"] = bytes.Replace(files["tools/echo.json"], []byte(`"SERVICE_KEY_ENV"`), []byte(`"literal-secret-value"`), 1)
				case "tool-schema":
					files["tools/echo.json"] = bytes.Replace(files["tools/echo.json"], []byte(`{"type":"object"}`), []byte(`[]`), 1)
				case "team":
					files["teams/team.json"] = []byte(`{"id":"example/team"}`)
				case "workflow":
					files["workflows/work.json"] = []byte(`{"id":"example/work"}`)
				}
			})
			r := testRuntime(t, store)
			if _, err := r.Inventory(context.Background()); err == nil {
				t.Fatal("invalid definition advertised")
			}
		})
	}
	if _, err := New(Config{}); err == nil {
		t.Fatal("nil store accepted")
	}
	store := testStore(t)
	fixture(t, store, "1.0.0", nil)
	r := testRuntime(t, store)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Inventory(ctx); err == nil {
		t.Fatal("cancel ignored")
	}
}
func TestPackageScriptsMustBeDeclaredAndUnmodified(t *testing.T) {
	store := testStore(t)
	revision := fixture(t, store, "1.0.0", func(files map[string][]byte) {
		files["tools/echo.json"] = bytes.Replace(files["tools/echo.json"], []byte("./bin/echo.sh"), []byte("./unlisted.sh"), 1)
	})
	r := testRuntime(t, store)
	inventory, err := r.Inventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Execute(context.Background(), inventory.ToolDefinitions[0].Route, json.RawMessage(`{}`)); got.OK || !strings.Contains(got.Error, "not part of") {
		t.Fatal(got)
	}
	path, err := store.FilePath(revision, "tools/echo.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := r.Execute(context.Background(), inventory.ToolDefinitions[0].Route, json.RawMessage(`{}`)); got.OK || !strings.Contains(got.Error, "integrity") {
		t.Fatal(got)
	}
	if _, err = r.Inventory(context.Background()); err == nil {
		t.Fatal("modified tool advertised")
	}
}

func TestUnavailableDependencyDoesNotHideBaseTools(t *testing.T) {
	store := testStore(t)
	fixture(t, store, "1.0.0", nil, managerpackages.Dependency{ID: "uninstalled", Version: "1.0.0", Digest: strings.Repeat("a", 64)})
	r := testRuntime(t, store)
	inventory, err := r.Inventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Tools) == 0 || len(inventory.ToolDefinitions) != 0 || len(inventory.TeamDefinitions) != 0 || inventory.PackageErrors["example"] == "" || len(inventory.Packages) != 1 {
		t.Fatal(inventory)
	}
	if got := r.Execute(context.Background(), "read", json.RawMessage(`{"path":"/does-not-exist"}`)); got.Code == "package_error" {
		t.Fatal("base tool incorrectly routed", got)
	}
}
func TestPackageSkillAndInventoryBounds(t *testing.T) {
	for _, mode := range []string{"binary-skill", "oversized-inventory"} {
		t.Run(mode, func(t *testing.T) {
			store := testStore(t)
			fixture(t, store, "1.0.0", func(files map[string][]byte) {
				if mode == "binary-skill" {
					files["skills/help.md"] = []byte{0xff}
				} else {
					files["skills/help.md"] = []byte(strings.Repeat("x", 1<<20))
				}
			})
			r := testRuntime(t, store)
			if _, err := r.Inventory(context.Background()); err == nil {
				t.Fatal("invalid inventory accepted")
			}
		})
	}
}

func TestWorkflowSnapshotPinsToolMetadataAcrossUpdates(t *testing.T) {
	store := testStore(t)
	first := fixture(t, store, "1.0.0", nil)
	r := testRuntime(t, store)
	inventory, err := r.Inventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	snapshot := append(json.RawMessage(nil), inventory.WorkflowDefinitions[0].Definition...)
	fixture(t, store, "1.1.0", nil)
	inventory, err = r.Inventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var original, current struct {
		ToolDefinitions map[string]machineclient.ToolDefinition `json:"tool_definitions"`
	}
	if err = json.Unmarshal(snapshot, &original); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(inventory.WorkflowDefinitions[0].Definition, &current); err != nil {
		t.Fatal(err)
	}
	old := original.ToolDefinitions["example/echo"]
	newer := current.ToolDefinitions["example/echo"]
	if old.Route != Route(first, "example/echo") || old.Package.Version != "1.0.0" || newer.Package.Version != "1.1.0" || old.Route == newer.Route {
		t.Fatal(original, current)
	}
	if bytes.Contains(snapshot, []byte(`"command"`)) || bytes.Contains(snapshot, []byte("SERVICE_KEY_ENV")) {
		t.Fatal("implementation or env leaked into workflow snapshot")
	}
	result := r.Execute(context.Background(), old.Route, json.RawMessage(`{}`))
	if !result.OK || !strings.HasPrefix(result.Data.(extensiontools.ExecutionResult).Content, "1.0.0:") {
		t.Fatal(result)
	}
}
