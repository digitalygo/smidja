package cli

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/machineclient"
	"github.com/digitalygo/smidja/internal/managerpackages"
)

func packageDigest(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}
func clientPackageArchive(t *testing.T) []byte {
	t.Helper()
	tool := []byte(`{"id":"fixture/tool","name":"Fixture","description":"fixture tool","parameters":{"type":"object"},"command":["/bin/echo","fixture"]}`)
	manifest := managerpackages.Manifest{SchemaVersion: 1, ID: "fixture", Version: "1.0.0", Publisher: "Fixture", RequiresSmidja: ">=0.0.0", Scopes: []string{"client"}, Targets: []string{runtime.GOOS + "/" + runtime.GOARCH}, Tools: []string{"tools/tool.json"}, Files: []managerpackages.File{{Path: "tools/tool.json", SHA256: packageDigest(tool)}}}
	raw, _ := json.Marshal(manifest)
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	entry, _ := writer.Create(managerpackages.ManifestFilename)
	entry.Write(raw)
	entry, _ = writer.Create("tools/tool.json")
	entry.Write(tool)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
func clientStateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Cleanup(func() {
		filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err == nil && info.IsDir() {
				return os.Chmod(path, 0700)
			}
			return err
		})
	})
	return dir
}
func TestClientPackageCLIInstallsAndPublishesLocalDefinitions(t *testing.T) {
	archive := clientPackageArchive(t)
	registered := make(chan machineclient.Registration, 2)
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/release":
			json.NewEncoder(w).Encode(managerpackages.Release{ID: "fixture", Version: "1.0.0", ArchiveURL: server.URL + "/archive", SHA256: packageDigest(archive)})
		case "/archive":
			w.Write(archive)
		case "/api/clients/connect":
			var registration machineclient.Registration
			if err := json.NewDecoder(r.Body).Decode(&registration); err != nil {
				t.Error(err)
			}
			registered <- registration
			json.NewEncoder(w).Encode(map[string]int{"protocol_version": 1})
		case "/api/clients/poll":
			select {
			case <-r.Context().Done():
			case <-time.After(10 * time.Millisecond):
				w.WriteHeader(204)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	dir := clientStateDir(t)
	cfg := clientFileConfig{ManagerURL: server.URL, ClientID: "laptop", Name: "Laptop", TokenEnv: "CLIENT", StateDir: dir, PackageSources: []managerpackages.Source{{URL: server.URL + "/release"}}}
	for i := 0; i < 2; i++ {
		d := serviceDeps()
		ctx, cancel := context.WithCancel(context.Background())
		d.Context = ctx
		d.HTTPClient = server.Client()
		done := make(chan error, 1)
		path := configPath(t, cfg)
		go func() { done <- runMachineClient([]string{"--config", path}, d) }()
		select {
		case registration := <-registered:
			if !registration.Inventory.ExtensionControls || len(registration.Inventory.ToolDefinitions) != 1 || registration.Inventory.ToolDefinitions[0].ID != "fixture/tool" || registration.Inventory.Packages[0].Version != "1.0.0" {
				t.Fatal(registration)
			}
		case <-time.After(2 * time.Second):
			cancel()
			t.Fatal("client package inventory not registered")
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}
func TestClientPackageCLIConfigurationValidation(t *testing.T) {
	for _, mode := range []string{"missing-state", "negative-interval", "relative-state", "shared-token", "invalid-source"} {
		t.Run(mode, func(t *testing.T) {
			cfg := clientFileConfig{ManagerURL: "http://127.0.0.1:1", ClientID: "laptop", Name: "Laptop", TokenEnv: "CLIENT", StateDir: clientStateDir(t)}
			switch mode {
			case "missing-state":
				cfg.StateDir = ""
				cfg.PackageSources = []managerpackages.Source{{URL: "https://fixture.invalid/release"}}
			case "negative-interval":
				cfg.PackageUpdateIntervalSeconds = -1
			case "relative-state":
				cfg.StateDir = "relative"
			case "shared-token":
				cfg.PackageSources = []managerpackages.Source{{URL: "https://fixture.invalid/release", TokenEnv: "CLIENT"}}
			case "invalid-source":
				cfg.PackageSources = []managerpackages.Source{{URL: "invalid"}}
			}
			if err := runMachineClient([]string{"--config", configPath(t, cfg)}, serviceDeps()); err == nil {
				t.Fatal("invalid client package configuration accepted")
			}
		})
	}
}
