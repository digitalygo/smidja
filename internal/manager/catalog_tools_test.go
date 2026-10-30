package manager

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/clientpackages"
	"github.com/digitalygo/smidja/internal/envai"
	"github.com/digitalygo/smidja/internal/machineclient"
	"github.com/digitalygo/smidja/internal/managerpackages"
	"github.com/digitalygo/smidja/internal/managerstate"
)

func TestAvailableCatalogsFollowClientPresence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inventory.json")
	broker := machineclient.NewBroker(machineclient.BrokerConfig{InventoryPath: path})
	inventory := capabilityInventory()
	inventory.SkillDefinitions = []machineclient.SkillDefinition{{ID: "example.coding/review-skill", Content: "Exact skill instructions", Package: inventory.Packages[0]}}
	connectCapability(t, broker, "laptop", inventory)
	remote := &teamDelegationRemote{teams: []envai.Team{{ID: "ceo", Name: "CEO"}, {ID: "materialized", Name: "smidja_hidden"}, {ID: "ordinary", Name: "Ordinary"}}}
	private := privateCatalog(t)
	savePrivate(t, private, "private:good", "workflow", 0, managerstate.Workflow{ID: "private:good", Version: "1", Phases: []managerstate.Phase{{ID: "p", TeamRef: "remote"}}})
	savePrivate(t, private, "private:broken", "workflow", 0, managerstate.Workflow{ID: "private:broken", Version: "1", Phases: []managerstate.Phase{{ID: "p", TeamRef: "private:missing"}}})
	s := testServer(t, func(c *Config) { c.Clients = broker; c.Private = private; c.Remote = remote; c.CEOTeamID = "ceo" })
	workflows, err := s.availableWorkflows(context.Background())
	if err != nil || len(workflows) != 2 {
		t.Fatal(workflows, err)
	}
	if workflows[1].ClientID != "laptop" || workflows[1].ID != clientReference("laptop", "example.coding/review") {
		t.Fatal(workflows)
	}
	teams, err := s.teamCatalog(context.Background())
	if err != nil || len(teams) != 2 || teams[0]["id"] != "ordinary" || teams[1]["client_id"] != "laptop" {
		t.Fatal(teams, err)
	}
	skills, err := s.availableSkills(context.Background())
	if err != nil || len(skills) != 1 || skills[0].ID != clientReference("laptop", "example.coding/review-skill") {
		t.Fatal(skills, err)
	}
	result := s.executeManagerTool(context.Background(), envai.Event{}, "skills_list", json.RawMessage(`{}`))
	if !result.OK {
		t.Fatal(result)
	}
	encoded, _ := json.Marshal(result.Data)
	if strings.Contains(string(encoded), "Exact skill instructions") {
		t.Fatal("skill content eagerly loaded into list")
	}
	args, _ := json.Marshal(map[string]string{"id": skills[0].ID})
	result = s.executeManagerTool(context.Background(), envai.Event{}, "manager.skills_read", args)
	if !result.OK || result.Data.(machineclient.SkillDefinition).Content != "Exact skill instructions" {
		t.Fatal(result)
	}
	if result = s.executeManagerTool(context.Background(), envai.Event{}, "workflows_list", json.RawMessage(`{}`)); !result.OK {
		t.Fatal(result)
	}
	s.cfg.Clients = machineclient.NewBroker(machineclient.BrokerConfig{InventoryPath: path})
	if workflows, err = s.availableWorkflows(context.Background()); err != nil || len(workflows) != 1 {
		t.Fatal(workflows, err)
	}
	if teams, err = s.teamCatalog(context.Background()); err != nil || len(teams) != 1 || teams[0]["id"] != "ordinary" {
		t.Fatal("offline materialized team leaked", teams, err)
	}
	if skills, err = s.availableSkills(context.Background()); err != nil || len(skills) != 0 {
		t.Fatal(skills, err)
	}
	if result = s.executeManagerTool(context.Background(), envai.Event{}, "skills_read", args); result.OK {
		t.Fatal("offline skill readable")
	}
	if result = s.executeManagerTool(context.Background(), envai.Event{}, "clients_execute", json.RawMessage(`{"client_id":"laptop","tool":"read","arguments":{"path":"/"}}`)); result.OK {
		t.Fatal("offline tool executed")
	}
	if result = s.executeManagerTool(context.Background(), envai.Event{}, "clients_execute", json.RawMessage(`{"arguments":12,"client_id":1}`)); result.OK {
		t.Fatal("malformed client arguments accepted")
	}
}
func TestConfigurationSchemasAndToolDefinitions(t *testing.T) {
	s := testServer(t, nil)
	for _, kind := range []string{"team", "workflow", "extension"} {
		shape, err := configurationSchema(kind)
		if err != nil {
			t.Fatal(err)
		}
		value := shape.(map[string]any)
		if value["kind"] != kind || value["payload"] == nil {
			t.Fatal(value)
		}
		args, _ := json.Marshal(map[string]string{"kind": kind})
		if result := s.executeManagerTool(context.Background(), envai.Event{}, "config_schema", args); !result.OK {
			t.Fatal(result)
		}
	}
	if _, err := configurationSchema("invalid"); err == nil {
		t.Fatal("invalid schema kind accepted")
	}
	if result := s.executeManagerTool(context.Background(), envai.Event{}, "config_schema", json.RawMessage(`{"kind":"invalid"}`)); result.OK {
		t.Fatal(result)
	}
	names := map[string]bool{}
	for _, definition := range ManagerToolDefinitions() {
		if names[definition.Name] {
			t.Fatal("duplicate tool", definition.Name)
		}
		names[definition.Name] = true
		var schema map[string]any
		if err := json.Unmarshal(definition.Parameters, &schema); err != nil || schema["type"] != "object" {
			t.Fatal(definition, err)
		}
	}
	for _, name := range []string{"clients_execute", "config_schema", "skills_list", "skills_read", "workflows_list", "teams_list"} {
		if !names[name] {
			t.Fatal("missing tool", name)
		}
	}
}
func TestManagerPackageCatalogWorkflowsAndSkills(t *testing.T) {
	fixture := &packageFixtureTransport{}
	setTeamPackage(fixture, "1.0.0", "model")
	store := packageFixtureStore(t, fixture)
	if _, err := store.Install(context.Background(), managerpackages.Source{URL: "https://packages.example/release.json"}); err != nil {
		t.Fatal(err)
	}
	runtime, err := clientpackages.New(clientpackages.Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	s := testServer(t, func(c *Config) { c.Packages = store; c.PackageRuntime = runtime })
	workflows, err := s.availableWorkflows(context.Background())
	if err != nil || len(workflows) != 1 {
		t.Fatal(workflows, err)
	}
	skills, err := s.availableSkills(context.Background())
	if err != nil || len(skills) != 0 {
		t.Fatal(skills, err)
	}
	if result := s.executeManagerTool(context.Background(), envai.Event{}, "skills_read", json.RawMessage(`{"id":"missing"}`)); result.OK {
		t.Fatal(result)
	}
}
