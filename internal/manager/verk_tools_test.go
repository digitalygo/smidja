package manager

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/clientpackages"
	"github.com/digitalygo/smidja/internal/extensiontools"
	"github.com/digitalygo/smidja/internal/machineclient"
	"github.com/digitalygo/smidja/internal/managerpackages"
	"github.com/digitalygo/smidja/internal/managerstate"
	"github.com/digitalygo/smidja/internal/privateconfig"
)

func TestGraphInstalledPackageToolExecution(t *testing.T) {
	fixture := &packageFixtureTransport{}
	setExecutableCapabilityPackage(fixture)
	packages := packageFixtureStore(t, fixture)
	if _, err := packages.Install(context.Background(), managerpackages.Source{URL: "https://packages.example/release.json"}); err != nil {
		t.Fatal(err)
	}
	runtime, err := clientpackages.New(clientpackages.Config{Store: packages, Environment: []string{"PATH=/usr/bin:/bin"}, LookupEnv: func(string) string { return "configured" }})
	if err != nil {
		t.Fatal(err)
	}
	s := testServer(t, func(c *Config) { c.Packages = packages; c.PackageRuntime = runtime })
	workflow, err := s.resolveWorkflow(reviewGraph("tool", map[string]any{"ref": "example.coding/lookup"}, "done", "error"))
	if err != nil {
		t.Fatal(err)
	}
	var binding graphToolBinding
	if err = json.Unmarshal(workflow.ToolDefinitions["graph:action"], &binding); err != nil {
		t.Fatal(err)
	}
	if binding.ClientID != "manager" || binding.Definition.Package.Version != "1.0.0" || !strings.HasPrefix(binding.Definition.Route, "package:example.coding@1.0.0+") {
		t.Fatal(binding)
	}
	task := reviewGraphTask(t, s, workflow)
	setGraphPackageVersion(fixture, "2.0.0", "new-package-result")
	if _, err = packages.Update(context.Background(), "example.coding"); err != nil {
		t.Fatal(err)
	}
	fresh, err := s.resolveWorkflow(reviewGraph("tool", map[string]any{"ref": "example.coding/lookup"}, "done", "error"))
	if err != nil {
		t.Fatal(err)
	}
	var next graphToolBinding
	json.Unmarshal(fresh.ToolDefinitions["graph:action"], &next)
	if next.Definition.Package.Version != "2.0.0" || binding.Definition.Route == next.Definition.Route {
		t.Fatal("new task did not select new revision", next)
	}
	s.runGraphTask(context.Background(), task.ID)
	task = getWorkflowTask(t, s, task.ID)
	if task.Status != managerstate.Completed || task.GraphState.Runs[1].Port != "done" || !strings.Contains(task.GraphState.Runs[1].Output, "package-result") || strings.Contains(task.GraphState.Runs[1].Output, "new-package-result") {
		t.Fatal(task.Status, task.Reason, task.GraphState)
	}
	s.cfg.PackageRuntime = nil
	if _, _, err = s.graphRegisteredTool(context.Background(), task, workflow.Graph.Nodes[1], json.RawMessage(`{}`)); err == nil {
		t.Fatal("missing runtime accepted")
	}
}

func TestGraphPrivateClientToolApprovedRevision(t *testing.T) {
	catalog := privateCatalog(t)
	broker := machineclient.NewBroker(machineclient.BrokerConfig{})
	extensionClient(t, broker, "laptop")
	definition := privateconfig.Extension{Name: "echo", Description: "test", ClientID: "laptop", Parameters: json.RawMessage(`{"type":"object"}`), Command: []string{"/bin/sh", "-c", "printf client-result"}}
	savePrivate(t, catalog, "private:echo", "extension", 0, definition)
	s := testServer(t, func(c *Config) { c.Private = catalog; c.Clients = broker })
	s.syncClientExtensions(context.Background())
	workflow, err := s.resolveWorkflow(reviewGraph("tool", map[string]any{"ref": "private:echo"}, "done", "error"))
	if err != nil {
		t.Fatal(err)
	}
	var binding graphToolBinding
	json.Unmarshal(workflow.ToolDefinitions["graph:action"], &binding)
	if binding.ClientID != "laptop" || binding.Definition.Route != "private:echo@1" {
		t.Fatal(binding)
	}
	task := reviewGraphTask(t, s, workflow)
	s.runGraphTask(context.Background(), task.ID)
	task = getWorkflowTask(t, s, task.ID)
	if task.Status != managerstate.Completed || !strings.Contains(task.GraphState.Runs[1].Output, "client-result") {
		t.Fatal(task.Status, task.Reason, task.GraphState)
	}
	definition.Command = []string{"/bin/sh", "-c", "printf newer"}
	savePrivate(t, catalog, "private:echo", "extension", 1, definition)
	result, handled, err := s.graphRegisteredTool(context.Background(), task, workflow.Graph.Nodes[1], json.RawMessage(`{}`))
	if err != nil || !handled || result.OK || !strings.Contains(result.Error, "approved") {
		t.Fatal(result, handled, err)
	}
	if _, err = s.resolveWorkflow(reviewGraph("tool", map[string]any{"ref": "private:echo", "client_id": "desktop"}, "done", "error")); err == nil {
		t.Fatal("configured client overridden")
	}
}

func TestGraphClientPackageSnapshotIsStable(t *testing.T) {
	broker := machineclient.NewBroker(machineclient.BrokerConfig{})
	inventory := capabilityInventory()
	connectCapability(t, broker, "laptop", inventory)
	s := testServer(t, func(c *Config) { c.Clients = broker })
	input := reviewGraph("tool", map[string]any{"ref": clientReference("laptop", "example.coding/lookup")}, "done", "error")
	pinned, err := s.resolveWorkflow(input)
	if err != nil {
		t.Fatal(err)
	}
	before := string(pinned.ToolDefinitions["graph:action"])
	current := capabilityInventory()
	current.ToolDefinitions[0].Route = "package:lookup@2"
	current.ToolDefinitions[0].Package.Version = "2.0.0"
	current.Tools[1] = "package:lookup@2"
	current.Packages[0].Version = "2.0.0"
	current.TeamDefinitions[0].Package.Version = "2.0.0"
	current.WorkflowDefinitions[0].Package.Version = "2.0.0"
	response := capabilityRequest(t, broker, "POST", "/api/clients/connect", "laptop", machineclient.Registration{ProtocolVersion: 1, ClientID: "laptop", Inventory: current})
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body)
	}
	updated, err := s.resolveWorkflow(input)
	if err != nil {
		t.Fatal(err)
	}
	if string(pinned.ToolDefinitions["graph:action"]) != before || !strings.Contains(before, "lookup@1") || !strings.Contains(string(updated.ToolDefinitions["graph:action"]), "lookup@2") {
		t.Fatal("snapshot changed")
	}
	if _, err = s.resolveWorkflow(reviewGraph("tool", map[string]any{"ref": clientReference("laptop", "example.coding/lookup"), "client_id": "desktop"}, "done", "error")); err == nil {
		t.Fatal("conflicting client accepted")
	}
}

func TestGraphToolBindingRejectsUnavailableAndForged(t *testing.T) {
	s := testServer(t, nil)
	for _, ref := range []string{"private:missing", "example.missing/tool", "client:bad", "client:laptop:example/tool"} {
		if _, err := s.resolveWorkflow(reviewGraph("tool", map[string]any{"ref": ref}, "done", "error")); err == nil {
			t.Fatal("unregistered tool accepted", ref)
		}
	}
	workflow := reviewGraph("tool", map[string]any{"ref": "example/tool"}, "done", "error")
	task := managerstate.Task{Workflow: workflow}
	for _, raw := range []string{`{`, `{"client_id":"manager","definition":{"id":"wrong","route":"package:test"}}`, `{"client_id":"manager","definition":{"id":"example/tool","route":"bash"}}`} {
		task.Workflow.ToolDefinitions = map[string]json.RawMessage{"graph:action": json.RawMessage(raw)}
		if _, handled, err := s.graphRegisteredTool(context.Background(), task, workflow.Graph.Nodes[1], json.RawMessage(`{}`)); !handled || err == nil {
			t.Fatal("invalid route accepted", raw, handled, err)
		}
	}
}

func TestGraphLocalPrivateToolKeepsConfiguredOwner(t *testing.T) {
	catalog := privateCatalog(t)
	definition := privateconfig.Extension{Name: "echo", Description: "test", Parameters: json.RawMessage(`{"type":"object"}`), Command: []string{"/bin/sh", "-c", "printf manager-only"}}
	savePrivate(t, catalog, "private:local", "extension", 0, definition)
	s := testServer(t, func(c *Config) {
		c.Private = catalog
		c.Extensions = extensiontools.New(nil, []string{"PATH=/usr/bin:/bin"})
	})
	w := reviewGraph("tool", map[string]any{"ref": "private:local"}, "done", "error")
	w.ClientID = "laptop"
	if err := s.pinGraphTools(&w); err != nil {
		t.Fatal(err)
	}
	task := reviewGraphTask(t, s, w)
	s.runGraphTask(context.Background(), task.ID)
	task = getWorkflowTask(t, s, task.ID)
	if task.Status != managerstate.Completed || !strings.Contains(task.GraphState.Runs[1].Output, "manager-only") {
		t.Fatal(task.Status, task.Reason, task.GraphState)
	}
}

func TestGraphClientBuiltinAndUnregisteredRoute(t *testing.T) {
	s := testServer(t, nil)
	w := reviewGraph("tool", map[string]any{"ref": "read", "client_id": "laptop"}, "done", "error")
	if _, err := s.resolveWorkflow(w); err != nil {
		t.Fatal(err)
	}
	w.Graph.Nodes[1].Params["ref"] = "package:unregistered"
	if _, err := s.resolveWorkflow(w); err == nil {
		t.Fatal("raw client route accepted")
	}
	task, run := reviewGraphRun(t, s, w)
	if _, _, err := s.graphTool(context.Background(), task, w.Graph.Nodes[1], run); err == nil {
		t.Fatal("unregistered client route executed")
	}
}

func setGraphPackageVersion(f *packageFixtureTransport, version, output string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	tool, _ := json.Marshal(struct {
		ID string `json:"id"`
		privateconfig.Extension
	}{"example.coding/lookup", privateconfig.Extension{Name: "lookup", Description: "Lookup", Parameters: json.RawMessage(`{"type":"object"}`), Command: []string{"/bin/sh", "-c", "printf " + output}}})
	sum := sha256.Sum256(tool)
	manifest := managerpackages.Manifest{SchemaVersion: 1, ID: "example.coding", Version: version, Publisher: "Example", RequiresSmidja: ">=1.0.0", Targets: []string{"linux/amd64"}, Scopes: []string{"manager"}, Tools: []string{"tools/lookup.json"}, Files: []managerpackages.File{{Path: "tools/lookup.json", SHA256: hex.EncodeToString(sum[:])}}}
	raw, _ := json.Marshal(manifest)
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	entry, _ := writer.Create(managerpackages.ManifestFilename)
	entry.Write(raw)
	entry, _ = writer.Create("tools/lookup.json")
	entry.Write(tool)
	writer.Close()
	f.archive = buffer.Bytes()
	sum = sha256.Sum256(f.archive)
	f.release = managerpackages.Release{ID: manifest.ID, Version: version, ArchiveURL: "https://packages.example/package.zip", SHA256: hex.EncodeToString(sum[:])}
}
