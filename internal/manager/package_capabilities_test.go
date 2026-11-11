package manager

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/clientpackages"
	"github.com/digitalygo/smidja/internal/envai"
	"github.com/digitalygo/smidja/internal/machineclient"
	"github.com/digitalygo/smidja/internal/machinetools"
	"github.com/digitalygo/smidja/internal/managerpackages"
	"github.com/digitalygo/smidja/internal/managerstate"
	"github.com/digitalygo/smidja/internal/privateconfig"
)

type capabilityRemote struct {
	extensionFake
	teamRemote
}

func capabilityRequest(t *testing.T, b *machineclient.Broker, method, path, id string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(payload)
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer token-long-enough-"+id)
	response := httptest.NewRecorder()
	b.ServeHTTP(response, request)
	return response
}
func connectCapability(t *testing.T, b *machineclient.Broker, id string, inventory machineclient.Inventory) {
	t.Helper()
	if err := b.AddClient(id, "Laptop "+id, "token-long-enough-"+id); err != nil {
		t.Fatal(err)
	}
	response := capabilityRequest(t, b, "POST", "/api/clients/connect", id, machineclient.Registration{ProtocolVersion: 1, ClientID: id, Inventory: inventory})
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
}
func capabilityInventory() machineclient.Inventory {
	revision := machineclient.PackageRevision{ID: "example.coding", Version: "1.0.0", Digest: "digest"}
	team := activationTeam("explicit-model")
	team.Agents[0].Tools = []string{"example.coding/lookup", "read"}
	teamRaw, _ := json.Marshal(team)
	workflow, _ := json.Marshal(managerstate.Workflow{ID: "example.coding/review", Version: "ignored", Phases: []managerstate.Phase{{ID: "review", TeamRef: "example.coding/reviewers"}}, TeamDefinitions: map[string]json.RawMessage{"example.coding/reviewers": teamRaw}})
	return machineclient.Inventory{OS: "linux", Arch: "arm64", Tools: []string{"read", "package:lookup@1"}, Packages: []machineclient.PackageRevision{revision}, ToolDefinitions: []machineclient.ToolDefinition{{ID: "example.coding/lookup", Route: "package:lookup@1", Description: "Lookup item", Parameters: json.RawMessage(`{"type":"object"}`), Package: revision}}, TeamDefinitions: []machineclient.CatalogDefinition{{ID: "example.coding/reviewers", Package: revision, Definition: teamRaw}}, WorkflowDefinitions: []machineclient.CatalogDefinition{{ID: "example.coding/review", Package: revision, Definition: workflow}}}
}
func TestClientCapabilityNamespacesAndOfflineCatalog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inventory.json")
	broker := machineclient.NewBroker(machineclient.BrokerConfig{InventoryPath: path})
	inventory := capabilityInventory()
	connectCapability(t, broker, "laptop", inventory)
	connectCapability(t, broker, "desktop", inventory)
	remote := &capabilityRemote{}
	s := testServer(t, func(c *Config) { c.Clients = broker; c.Remote = remote; c.CEOTeamID = "ceo" })
	if err := s.refreshPackageTools(context.Background()); err != nil {
		t.Fatal(err)
	}
	laptop := s.packageToolRefs["laptop"]["example.coding/lookup"]
	desktop := s.packageToolRefs["desktop"]["example.coding/lookup"]
	if laptop == "" || desktop == "" || laptop == desktop || len(remote.registered) != 4 {
		t.Fatal(s.packageToolRefs, remote.registered)
	}
	if err := s.refreshPackageTools(context.Background()); err != nil || len(remote.registered) != 4 {
		t.Fatal("duplicate registration", err, remote.registered)
	}
	for owner, refs := range s.packageToolRefs {
		for id, name := range refs {
			binding, ok := s.registeredPackageBinding(name)
			if !ok || binding.ClientID != owner {
				t.Fatal(binding, ok)
			}
			expected := "package:lookup@1"
			if id == "read" {
				expected = "read"
			}
			if binding.Name != expected {
				t.Fatal(binding)
			}
		}
	}
	ref := clientReference("laptop", "example.coding/reviewers")
	if _, err := s.ensureTeam(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	if len(remote.agents) != 1 || len(remote.agents[0].ExternalToolsEnabled) != 2 {
		t.Fatal(remote.agents)
	}
	for _, tool := range remote.agents[0].ExternalToolsEnabled {
		binding, _ := s.registeredPackageBinding(tool.Name)
		if binding.ClientID != "laptop" {
			t.Fatal("team routed to other machine", binding)
		}
	}
	workflow, err := s.clientWorkflow(clientReference("laptop", "example.coding/review"))
	if err != nil || workflow.ClientID != "laptop" || workflow.Version != "1.0.0+digest" {
		t.Fatal(workflow, err)
	}
	if _, err = s.ensureWorkflowTeam(context.Background(), workflow, "example.coding/reviewers"); err != nil {
		t.Fatal(err)
	}
	s.cfg.Clients = machineclient.NewBroker(machineclient.BrokerConfig{InventoryPath: path})
	if err = s.refreshPackageTools(context.Background()); err != nil || len(s.packageToolRefs) != 0 {
		t.Fatal(err, s.packageToolRefs)
	}
	if _, err = s.onlineClient("laptop"); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatal(err)
	}
	if _, err = s.ensureTeam(context.Background(), ref); err == nil {
		t.Fatal("offline team exposed")
	}
	if _, err = s.ensureWorkflowTeam(context.Background(), workflow, "example.coding/reviewers"); err == nil {
		t.Fatal("offline pinned workflow executed")
	}
	if _, err = s.clientWorkflow(clientReference("laptop", "example.coding/review")); err == nil {
		t.Fatal("offline workflow exposed")
	}
	if len(s.cfg.Clients.List()[0].Inventory.TeamDefinitions) == 0 {
		t.Fatal("offline historical inventory lost")
	}
}
func TestRegisteredClientToolRoutesOnceToChosenMachine(t *testing.T) {
	broker := machineclient.NewBroker(machineclient.BrokerConfig{PollTimeout: time.Second, ExecutionTimeout: time.Second})
	connectCapability(t, broker, "laptop", capabilityInventory())
	connectCapability(t, broker, "desktop", capabilityInventory())
	remote := &capabilityRemote{}
	s := testServer(t, func(c *Config) { c.Clients = broker; c.Remote = remote; c.CEOTeamID = "ceo" })
	if err := s.refreshPackageTools(context.Background()); err != nil {
		t.Fatal(err)
	}
	name := s.packageToolRefs["laptop"]["example.coding/lookup"]
	request := envai.ToolRequest{CallID: "one-call", ToolName: name, Kind: "client", Args: json.RawMessage(`{}`)}
	event := envai.Event{ChatID: "chat", TurnID: "turn"}
	done := make(chan error, 1)
	go func() { done <- s.executeVerifiedTool(context.Background(), event, request) }()
	response := capabilityRequest(t, broker, "GET", "/api/clients/poll?client_id=laptop", "laptop", nil)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body)
	}
	var execution machineclient.Execution
	if err := json.Unmarshal(response.Body.Bytes(), &execution); err != nil {
		t.Fatal(err)
	}
	if execution.ClientID != "laptop" || execution.Tool != "package:lookup@1" {
		t.Fatal(execution)
	}
	result := machineclient.ResultEnvelope{ClientID: "laptop", RequestID: execution.RequestID, Result: machinetools.Result{OK: true, Data: "from laptop"}}
	response = capabilityRequest(t, broker, "POST", "/api/clients/result", "laptop", result)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"accepted":true`) {
		t.Fatal(response.Code, response.Body)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := s.executeVerifiedTool(context.Background(), event, request); err != nil {
		t.Fatal(err)
	}
	if remote.submits != 2 || remote.outputs[0] != remote.outputs[1] {
		t.Fatal(remote.outputs)
	}
	receipt, err := s.cfg.Store.GetTool("one-call")
	if err != nil || receipt.ClientID != "laptop" {
		t.Fatal(receipt, err)
	}
}
func TestCapabilityReferenceAndRegistrationFailures(t *testing.T) {
	ref := clientReference("laptop: Luca + 1", "example.coding/team")
	owner, id, err := parseClientReference(ref)
	if err != nil || owner != "laptop: Luca + 1" || id != "example.coding/team" {
		t.Fatal(ref, owner, id, err)
	}
	for _, bad := range []string{"other", "client:", "client::id", "client:owner:", "client:%zz:id"} {
		if _, _, err := parseClientReference(bad); err == nil {
			t.Fatal(bad)
		}
	}
	s := testServer(t, nil)
	if err := s.refreshPackageTools(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.onlineClient("missing"); err == nil {
		t.Fatal("unknown client")
	}
	for _, bad := range []string{"client:%zz:id", "client:missing:team"} {
		if _, err := s.ensureTeam(context.Background(), bad); err == nil {
			t.Fatal(bad)
		}
		if _, err := s.clientWorkflow(bad); err == nil {
			t.Fatal(bad)
		}
	}
	if _, ok := s.registeredPackageBinding("missing"); ok {
		t.Fatal("missing registration")
	}
	broker := machineclient.NewBroker(machineclient.BrokerConfig{})
	connectCapability(t, broker, "laptop", capabilityInventory())
	remote := &capabilityRemote{extensionFake: extensionFake{registerErr: errors.New("outcome unknown")}}
	s = testServer(t, func(c *Config) { c.Remote = remote; c.Clients = broker })
	if err := s.refreshPackageTools(context.Background()); err == nil {
		t.Fatal("registration failure hidden")
	}
	remote.registerErr = nil
	if err := s.refreshPackageTools(context.Background()); err == nil || !strings.Contains(err.Error(), "uncertain") || len(remote.registered) != 1 {
		t.Fatal(err, remote.registered)
	}
}
func setExecutableCapabilityPackage(f *packageFixtureTransport) {
	f.mu.Lock()
	defer f.mu.Unlock()
	tool, _ := json.Marshal(struct {
		ID string `json:"id"`
		privateconfig.Extension
	}{"example.coding/lookup", privateconfig.Extension{Name: "lookup", Description: "Lookup", Parameters: json.RawMessage(`{"type":"object"}`), Command: []string{"/bin/sh", "-c", "printf package-result"}, Environment: map[string]string{"ODOO_KEY": "ODOO_API_KEY"}}})
	files := map[string][]byte{"tools/lookup.json": tool}
	manifest := managerpackages.Manifest{SchemaVersion: 1, ID: "example.coding", Version: "1.0.0", Publisher: "Example", RequiresSmidja: ">=1.0.0", Targets: []string{"linux/amd64"}, Scopes: []string{"manager"}, Tools: []string{"tools/lookup.json"}}
	for path, data := range files {
		sum := sha256.Sum256(data)
		manifest.Files = append(manifest.Files, managerpackages.File{Path: path, SHA256: hex.EncodeToString(sum[:])})
	}
	raw, _ := json.Marshal(manifest)
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	entry, _ := writer.Create(managerpackages.ManifestFilename)
	entry.Write(raw)
	for path, data := range files {
		entry, _ := writer.Create(path)
		entry.Write(data)
	}
	writer.Close()
	f.archive = buffer.Bytes()
	sum := sha256.Sum256(f.archive)
	f.release = managerpackages.Release{ID: manifest.ID, Version: manifest.Version, ArchiveURL: "https://packages.example/package.zip", SHA256: hex.EncodeToString(sum[:])}
}
func TestManagerPackageMetadataAndExecution(t *testing.T) {
	fixture := &packageFixtureTransport{}
	setExecutableCapabilityPackage(fixture)
	packages := packageFixtureStore(t, fixture)
	if _, err := packages.Install(context.Background(), managerpackages.Source{URL: "https://packages.example/release.json"}); err != nil {
		t.Fatal(err)
	}
	lookups := 0
	runtime, err := clientpackages.New(clientpackages.Config{Store: packages, Environment: []string{"PATH=/bin:/usr/bin"}, LookupEnv: func(name string) string {
		lookups++
		if name != "ODOO_API_KEY" {
			t.Error(name)
		}
		return "secret-never-publish"
	}})
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := runtime.Inventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	metadata, _ := json.Marshal(inventory)
	if lookups != 0 || strings.Contains(string(metadata), "/bin/sh") || strings.Contains(string(metadata), "ODOO_API_KEY") || strings.Contains(string(metadata), "secret-never-publish") {
		t.Fatal("executable or secret leaked into metadata", string(metadata))
	}
	remote := &capabilityRemote{}
	s := testServer(t, func(c *Config) { c.Remote = remote; c.PackageRuntime = runtime; c.CEOTeamID = "ceo" })
	if err = s.refreshPackageTools(context.Background()); err != nil {
		t.Fatal(err)
	}
	name := s.packageToolRefs["manager"]["example.coding/lookup"]
	if name == "" || lookups != 0 {
		t.Fatal(name, lookups)
	}
	if err = s.executeVerifiedTool(context.Background(), envai.Event{ChatID: "chat", TurnID: "turn"}, envai.ToolRequest{CallID: "package-call", ToolName: name, Args: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if remote.submits != 1 || remote.outputErrors[0] || !strings.Contains(remote.outputs[0], "package-result") || lookups != 1 {
		t.Fatal(remote.outputs, lookups)
	}
}

func TestPinnedClientToolRevisionOverridesCurrentInventory(t *testing.T) {
	broker := machineclient.NewBroker(machineclient.BrokerConfig{})
	original := capabilityInventory()
	connectCapability(t, broker, "laptop", original)
	remote := &capabilityRemote{}
	s := testServer(t, func(c *Config) { c.Remote = remote; c.Clients = broker; c.CEOTeamID = "ceo" })
	workflow, err := s.clientWorkflow(clientReference("laptop", "example.coding/review"))
	if err != nil {
		t.Fatal(err)
	}
	oldDefinition, _ := json.Marshal(original.ToolDefinitions[0])
	workflow.ToolDefinitions = map[string]json.RawMessage{"example.coding/lookup": oldDefinition}
	current := capabilityInventory()
	revision := machineclient.PackageRevision{ID: "example.coding", Version: "2.0.0", Digest: "new-digest"}
	current.Packages = []machineclient.PackageRevision{revision}
	current.Tools[1] = "package:lookup@2"
	current.ToolDefinitions[0].Route = "package:lookup@2"
	current.ToolDefinitions[0].Package = revision
	current.TeamDefinitions[0].Package = revision
	current.WorkflowDefinitions[0].Package = revision
	response := capabilityRequest(t, broker, "POST", "/api/clients/connect", "laptop", machineclient.Registration{ProtocolVersion: 1, ClientID: "laptop", Inventory: current})
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body)
	}
	if _, err = s.ensureWorkflowTeam(context.Background(), workflow, "example.coding/reviewers"); err != nil {
		t.Fatal(err)
	}
	routes := map[string]bool{}
	for _, tool := range remote.agents[0].ExternalToolsEnabled {
		binding, ok := s.registeredPackageBinding(tool.Name)
		if !ok || binding.ClientID != "laptop" {
			t.Fatal(binding, ok)
		}
		routes[binding.Name] = true
	}
	if !routes["package:lookup@1"] || routes["package:lookup@2"] || !routes["read"] {
		t.Fatal("pinned task rebound to newer helper", routes)
	}
	definition, _ := json.Marshal(current.ToolDefinitions[0])
	workflow.ToolDefinitions["example.coding/lookup"] = definition
	workflow.Version = "2.0.0+new-digest"
	if _, err = s.ensureWorkflowTeam(context.Background(), workflow, "example.coding/reviewers"); err != nil {
		t.Fatal(err)
	}
	routes = map[string]bool{}
	for _, tool := range remote.agents[1].ExternalToolsEnabled {
		binding, _ := s.registeredPackageBinding(tool.Name)
		routes[binding.Name] = true
	}
	if !routes["package:lookup@2"] || routes["package:lookup@1"] {
		t.Fatal("new task used outdated helper", routes)
	}
}
func TestPinnedToolMaterializationRejectsInvalidReferences(t *testing.T) {
	remote := &capabilityRemote{}
	s := testServer(t, func(c *Config) { c.Remote = remote })
	team := activationTeam("model")
	team.Agents[0].Tools = []string{"missing"}
	if _, err := s.materializeOwner(context.Background(), "manager", "team", "1", team); err == nil || remote.calls != 0 {
		t.Fatal(err, remote.calls)
	}
	if _, err := s.materializeOwner(context.Background(), "manager", "team", "1", activationTeam("model"), map[string]json.RawMessage{"tool": json.RawMessage(`[]`)}); err == nil {
		t.Fatal("invalid pinned definition")
	}
	noRegistrar := &activatingRemote{}
	s.cfg.Remote = noRegistrar
	definition, _ := json.Marshal(capabilityInventory().ToolDefinitions[0])
	if _, err := s.materializeOwner(context.Background(), "manager", "team", "1", activationTeam("model"), map[string]json.RawMessage{"tool": definition}); err == nil || !strings.Contains(err.Error(), "registration unavailable") {
		t.Fatal(err)
	}
	s.cfg.Remote = remote
	remote.registerErr = errors.New("unknown outcome")
	if _, err := s.materializeOwner(context.Background(), "manager", "team", "1", activationTeam("model"), map[string]json.RawMessage{"tool": definition}); err == nil {
		t.Fatal("pinned registration failure hidden")
	}
}

func setPinnedCapabilityPackage(f *packageFixtureTransport, id, version string, includeWorkflow bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	toolID := id + "/lookup"
	tool, _ := json.Marshal(struct {
		ID string `json:"id"`
		privateconfig.Extension
	}{toolID, privateconfig.Extension{Name: "lookup", Description: "Lookup " + version, Parameters: json.RawMessage(`{"type":"object"}`), Command: []string{"/bin/sh", "-c", "printf version-" + version}}})
	files := map[string][]byte{"tools/lookup.json": tool}
	manifest := managerpackages.Manifest{SchemaVersion: 1, ID: id, Version: version, Publisher: "Example", RequiresSmidja: ">=1.0.0", Targets: []string{"linux/amd64"}, Scopes: []string{"manager"}, Tools: []string{"tools/lookup.json"}}
	if includeWorkflow {
		team := activationTeam("model")
		team.Agents[0].Tools = []string{toolID}
		teamRaw, _ := json.Marshal(struct {
			ID string `json:"id"`
			privateconfig.Team
		}{id + "/reviewers", team})
		workflowRaw, _ := json.Marshal(managerstate.Workflow{ID: id + "/review", Version: version, Phases: []managerstate.Phase{{ID: "one", TeamRef: id + "/reviewers"}, {ID: "two", TeamRef: id + "/reviewers"}}})
		files["teams/reviewers.json"] = teamRaw
		files["workflows/review.json"] = workflowRaw
		manifest.Teams = []string{"teams/reviewers.json"}
		manifest.Workflows = []string{"workflows/review.json"}
	}
	for path, data := range files {
		sum := sha256.Sum256(data)
		manifest.Files = append(manifest.Files, managerpackages.File{Path: path, SHA256: hex.EncodeToString(sum[:])})
	}
	raw, _ := json.Marshal(manifest)
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	entry, _ := writer.Create(managerpackages.ManifestFilename)
	entry.Write(raw)
	for path, data := range files {
		entry, _ := writer.Create(path)
		entry.Write(data)
	}
	writer.Close()
	f.archive = buffer.Bytes()
	sum := sha256.Sum256(f.archive)
	f.release = managerpackages.Release{ID: id, Version: version, ArchiveURL: "https://packages.example/package.zip", SHA256: hex.EncodeToString(sum[:])}
}
func TestManagerPhaseTwoKeepsPinnedToolsAfterUpdateAndUnrelatedInstall(t *testing.T) {
	fixture := &packageFixtureTransport{}
	setPinnedCapabilityPackage(fixture, "example.coding", "1.0.0", true)
	packages := packageFixtureStore(t, fixture)
	if _, err := packages.Install(context.Background(), managerpackages.Source{URL: "https://packages.example/release.json"}); err != nil {
		t.Fatal(err)
	}
	runtime, err := clientpackages.New(clientpackages.Config{Store: packages})
	if err != nil {
		t.Fatal(err)
	}
	remote := &capabilityRemote{}
	s := testServer(t, func(c *Config) {
		c.Remote = remote
		c.Packages = packages
		c.PackageRuntime = runtime
		c.CEOTeamID = "ceo"
	})
	workflows, err := s.packageWorkflows()
	if err != nil || len(workflows) != 1 {
		t.Fatal(workflows, err)
	}
	task, err := s.cfg.Store.CreateTask(managerstate.StartTask{Workflow: workflows[0], Inputs: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.cfg.Store.StartPhase(task.ID, "first-phase"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.cfg.Store.RecordOutput(task.ID, "first-phase", "phase one complete"); err != nil {
		t.Fatal(err)
	}
	if task, err = s.cfg.Store.Classify(task.ID, "first-phase", managerstate.DecisionNotBlocked, "model"); err != nil || task.PhaseIndex != 1 {
		t.Fatal(task, err)
	}
	setPinnedCapabilityPackage(fixture, "example.coding", "2.0.0", true)
	if _, err = packages.Update(context.Background(), "example.coding"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ensureWorkflowTeam(context.Background(), task.Workflow, task.Phases[1].Phase.TeamRef); err != nil {
		t.Fatal(err)
	}
	if len(remote.agents) != 1 || len(remote.agents[0].ExternalToolsEnabled) != 1 {
		t.Fatal(remote.agents)
	}
	binding, ok := s.registeredPackageBinding(remote.agents[0].ExternalToolsEnabled[0].Name)
	if !ok || binding.ClientID != "manager" || !strings.HasPrefix(binding.Name, "package:example.coding@1.0.0+") {
		t.Fatal("phase two rebound to v2", binding, ok)
	}
	workflows, err = s.packageWorkflows()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ensureWorkflowTeam(context.Background(), workflows[0], "example.coding/reviewers"); err != nil {
		t.Fatal(err)
	}
	binding, ok = s.registeredPackageBinding(remote.agents[1].ExternalToolsEnabled[0].Name)
	if !ok || !strings.HasPrefix(binding.Name, "package:example.coding@2.0.0+") {
		t.Fatal("new task did not use v2", binding, ok)
	}
	calls := remote.calls
	registrations := len(remote.registered)
	setPinnedCapabilityPackage(fixture, "example.extra", "1.0.0", false)
	if _, err = packages.Install(context.Background(), managerpackages.Source{URL: "https://packages.example/release.json"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ensureWorkflowTeam(context.Background(), task.Workflow, task.Phases[1].Phase.TeamRef); err != nil {
		t.Fatal(err)
	}
	if remote.calls != calls || len(remote.registered) <= registrations {
		t.Fatal("unrelated install changed team materialization identity", remote.calls, calls, len(remote.registered), registrations)
	}
}

func TestPackageAndClientExtensionRegistrationTimeout(t *testing.T) {
	for _, seconds := range []int{0, 450, -1, 601} {
		remote := &extensionFake{}
		s := testServer(t, nil)
		definition := machineclient.ToolDefinition{ID: "example/echo", Route: "package:echo", TimeoutSeconds: seconds, Parameters: json.RawMessage(`{}`), Package: machineclient.PackageRevision{ID: "example", Version: "1", Digest: "digest"}}
		_, err := s.registerPackageDefinition(context.Background(), remote, "laptop", definition)
		if seconds < 0 || seconds > 600 {
			if err == nil {
				t.Fatal("invalid timeout registered")
			}
			continue
		}
		expected := seconds
		if expected == 0 {
			expected = 30
		}
		if err != nil || remote.registered[0].TimeoutSeconds != expected {
			t.Fatal(remote.registered, err)
		}
		definition.ID = "private:echo"
		definition.Route = "private:echo@1"
		definition.Package = machineclient.PackageRevision{}
		definition.PrivateRevision = 1
		_, err = s.registerPackageDefinition(context.Background(), remote, "laptop", definition)
		if err != nil || remote.registered[1].TimeoutSeconds != expected {
			t.Fatal(remote.registered, err)
		}
	}
}
