package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/envai"
	"github.com/digitalygo/smidja/internal/extensiontools"
	"github.com/digitalygo/smidja/internal/privateconfig"
)

type extensionFake struct {
	chatRemote
	registered                   []envai.ToolDefinition
	references                   [][]envai.ToolReference
	teams                        []envai.Team
	registerErr, setErr, listErr error
	badIdentity                  bool
}

func (f *extensionFake) RegisterTool(_ context.Context, input envai.ToolDefinition) (envai.ToolDefinition, error) {
	f.registered = append(f.registered, input)
	if f.registerErr != nil {
		return envai.ToolDefinition{}, f.registerErr
	}
	input.ID = "id_" + input.Name
	if f.badIdentity {
		input.Name = "wrong"
	}
	return input, nil
}
func (f *extensionFake) SetAgentClientTools(_ context.Context, id string, refs []envai.ToolReference) (envai.AgentDefinition, error) {
	if id != "agent" {
		return envai.AgentDefinition{}, errors.New("wrong agent")
	}
	f.references = append(f.references, append([]envai.ToolReference{}, refs...))
	return envai.AgentDefinition{ID: id}, f.setErr
}
func (f *extensionFake) ListTeams(context.Context) ([]envai.Team, error) { return f.teams, f.listErr }
func extensionDefinition() privateconfig.Extension {
	return privateconfig.Extension{Name: "lookup", Description: "Read explicit configuration", Parameters: json.RawMessage(`{"type":"object"}`), Command: []string{"/bin/sh", "-c", `printf '%s|%s' "$ODOO_KEY" "${HOST_UNDECLARED-unset}"`}, Environment: map[string]string{"ODOO_KEY": "ODOO_API_KEY"}}
}
func extensionServer(t *testing.T, remote *extensionFake, definition privateconfig.Extension) *Server {
	catalog := privateCatalog(t)
	savePrivate(t, catalog, "private:lookup", "extension", 0, definition)
	return testServer(t, func(c *Config) {
		c.Private = catalog
		c.Remote = remote
		c.CEOTeamID = "ceo"
		c.WorkspaceID = "workspace"
		c.Extensions = extensiontools.New(nil, []string{"PATH=/usr/bin:/bin"})
		c.ToolBindings = map[string]ToolBinding{"base_read": {Name: "read"}}
	})
}
func extensionBinding(t *testing.T, s *Server) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, binding := range s.dynamicBindings {
		return binding.Name
	}
	t.Fatal("no dynamic binding")
	return ""
}
func TestExtensionActualAdapterAndZeroRefresh(t *testing.T) {
	registered := 0
	var snapshots [][]envai.ToolReference
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("missing adapter auth")
		}
		switch r.Method + " " + r.URL.Path {
		case "POST /api/v1/tools":
			var input envai.ToolDefinition
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			registered++
			input.ID = "tool"
			json.NewEncoder(w).Encode(map[string]any{"data": input})
		case "GET /api/v1/teams":
			fmt.Fprint(w, `{"data":[{"id":"ceo","orchestrator_id":"agent"}]}`)
		case "PUT /api/v1/agents/agent":
			var input struct {
				Tools []envai.ToolReference `json:"external_tools_enabled"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			snapshots = append(snapshots, input.Tools)
			fmt.Fprint(w, `{"data":{"id":"agent"}}`)
		default:
			t.Error(r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer httpServer.Close()
	remote, err := envai.New(envai.Config{BaseURL: httpServer.URL, APIKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	catalog := privateCatalog(t)
	savePrivate(t, catalog, "private:lookup", "extension", 0, extensionDefinition())
	s := testServer(t, func(c *Config) {
		c.Private = catalog
		c.Remote = remote
		c.CEOTeamID = "ceo"
		c.ToolBindings = map[string]ToolBinding{"base_read": {Name: "read"}}
	})
	if err = s.refreshExtensions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if registered != 1 || len(snapshots) != 1 || len(snapshots[0]) != 2 {
		t.Fatal(registered, snapshots)
	}
	if _, err = catalog.ApplyOwner(privateconfig.Change{Action: "archive", ID: "private:lookup", Kind: "extension", BaseRevision: 1}); err != nil {
		t.Fatal(err)
	}
	if err = s.refreshExtensions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if registered != 1 || len(snapshots) != 2 || len(snapshots[1]) != 1 || snapshots[1][0].Name != "base_read" || len(s.dynamicBindings) != 0 {
		t.Fatal(registered, snapshots, s.dynamicBindings)
	}
	s.cfg.ToolBindings = nil
	if err = s.refreshExtensions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 3 || snapshots[2] == nil || len(snapshots[2]) != 0 {
		t.Fatal("last reference was not explicitly cleared", snapshots)
	}
}
func TestExtensionEnvironmentResolvedOnlyOnInvocation(t *testing.T) {
	t.Setenv("HOST_UNDECLARED", "must-not-leak")
	remote := &extensionFake{teams: []envai.Team{{ID: "ceo", OrchestratorID: "agent"}}}
	s := extensionServer(t, remote, extensionDefinition())
	lookedUp := []string{}
	value := "first-secret"
	s.cfg.LookupEnv = func(name string) string { lookedUp = append(lookedUp, name); return value }
	if err := s.refreshExtensions(context.Background()); err != nil {
		t.Fatal(err)
	}
	key := extensionBinding(t, s)
	if len(lookedUp) != 0 {
		t.Fatal("resolved credentials at installation")
	}
	for _, expected := range []string{"first-secret", "rotated-secret"} {
		value = expected
		result := s.executeExtension(context.Background(), key, json.RawMessage(`{}`))
		if !result.OK {
			t.Fatal(result)
		}
		output, ok := result.Data.(extensiontools.ExecutionResult)
		if !ok || output.Content != expected+"|unset" {
			t.Fatal(result)
		}
	}
	for _, name := range lookedUp {
		if name != "ODOO_API_KEY" {
			t.Fatal("unapproved reference", name)
		}
	}
	value = ""
	if result := s.executeExtension(context.Background(), key, json.RawMessage(`{}`)); result.OK || !strings.Contains(result.Error, "missing") {
		t.Fatal(result)
	}
	s.cfg.LookupEnv = nil
	if result := s.executeExtension(context.Background(), key, json.RawMessage(`{}`)); result.OK || !strings.Contains(result.Error, "resolver") {
		t.Fatal(result)
	}
}
func TestExtensionProtectedSecretsAndStaleVersions(t *testing.T) {
	remote := &extensionFake{teams: []envai.Team{{ID: "ceo", OrchestratorID: "agent"}}}
	definition := extensionDefinition()
	definition.Environment = map[string]string{"ODOO_KEY": "MANAGER_TOKEN"}
	s := extensionServer(t, remote, definition)
	s.cfg.ProtectedEnvironment = []string{"MANAGER_TOKEN", "ENVAI_KEY"}
	s.cfg.LookupEnv = func(string) string { t.Error("protected reference resolved"); return "secret" }
	if err := s.refreshExtensions(context.Background()); err != nil {
		t.Fatal(err)
	}
	old := extensionBinding(t, s)
	if result := s.executeExtension(context.Background(), old, json.RawMessage(`{}`)); result.OK || !strings.Contains(result.Error, "authentication") {
		t.Fatal(result)
	}
	changed := extensionDefinition()
	savePrivate(t, s.cfg.Private, "private:lookup", "extension", 1, changed)
	if result := s.executeExtension(context.Background(), old, json.RawMessage(`{}`)); result.OK || !strings.Contains(result.Error, "superseded") {
		t.Fatal(result)
	}
	if err := s.refreshExtensions(context.Background()); err != nil {
		t.Fatal(err)
	}
	current := extensionBinding(t, s)
	if current == old {
		t.Fatal("revision registration reused")
	}
	if _, err := s.cfg.Private.ApplyOwner(privateconfig.Change{Action: "archive", ID: "private:lookup", Kind: "extension", BaseRevision: 2}); err != nil {
		t.Fatal(err)
	}
	if result := s.executeExtension(context.Background(), current, json.RawMessage(`{}`)); result.OK || !strings.Contains(result.Error, "archived") {
		t.Fatal(result)
	}
}
func TestExtensionRegistrationUnknownDoesNotRetry(t *testing.T) {
	for _, badIdentity := range []bool{false, true} {
		remote := &extensionFake{teams: []envai.Team{{ID: "ceo", OrchestratorID: "agent"}}, badIdentity: badIdentity}
		if !badIdentity {
			remote.registerErr = errors.New("transport outcome unknown")
		}
		s := extensionServer(t, remote, extensionDefinition())
		if err := s.refreshExtensions(context.Background()); err == nil {
			t.Fatal("expected registration error")
		}
		remote.registerErr = nil
		remote.badIdentity = false
		if err := s.refreshExtensions(context.Background()); err == nil || !strings.Contains(err.Error(), "uncertain") || len(remote.registered) != 1 {
			t.Fatal(err, len(remote.registered))
		}
	}
}
func TestExtensionRefreshFailures(t *testing.T) {
	s := testServer(t, nil)
	if err := s.refreshExtensions(context.Background()); err != nil {
		t.Fatal(err)
	}
	catalog := privateCatalog(t)
	s.cfg.Private = catalog
	if err := s.refreshExtensions(context.Background()); err != nil {
		t.Fatal(err)
	}
	savePrivate(t, catalog, "private:lookup", "extension", 0, extensionDefinition())
	if err := s.refreshExtensions(context.Background()); err == nil {
		t.Fatal("unavailable registration")
	}
	for _, remote := range []*extensionFake{{listErr: errors.New("list failed")}, {}, {teams: []envai.Team{{ID: "ceo", OrchestratorID: "agent"}}, setErr: errors.New("update failed")}} {
		s := extensionServer(t, remote, extensionDefinition())
		if err := s.refreshExtensions(context.Background()); err == nil || len(s.dynamicBindings) != 0 {
			t.Fatal(err, s.dynamicBindings)
		}
	}
	remote := &extensionFake{teams: []envai.Team{{ID: "ceo", OrchestratorID: "agent"}}}
	definition := extensionDefinition()
	definition.ClientID = "laptop"
	s = extensionServer(t, remote, definition)
	if err := s.refreshExtensions(context.Background()); err != nil || len(remote.registered) != 0 {
		t.Fatal(err, remote.registered)
	}
	if result := s.executeExtension(context.Background(), "unknown", json.RawMessage(`{}`)); result.OK {
		t.Fatal(result)
	}
	s.cfg.Extensions = nil
	if result := s.executeExtension(context.Background(), "unknown", json.RawMessage(`{}`)); result.OK {
		t.Fatal(result)
	}
}

func TestExtensionSamePayloadRevisionRejectsOldHelper(t *testing.T) {
	remote := &extensionFake{teams: []envai.Team{{ID: "ceo", OrchestratorID: "agent"}}}
	definition := extensionDefinition()
	s := extensionServer(t, remote, definition)
	s.cfg.LookupEnv = func(string) string { return "secret" }
	if err := s.refreshExtensions(context.Background()); err != nil {
		t.Fatal(err)
	}
	old := extensionBinding(t, s)
	savePrivate(t, s.cfg.Private, "private:lookup", "extension", 1, definition)
	if result := s.executeExtension(context.Background(), old, json.RawMessage(`{}`)); result.OK {
		t.Fatal("superseded registration ran after same-payload revision", result)
	}
	if err := s.refreshExtensions(context.Background()); err != nil {
		t.Fatal(err)
	}
	current := extensionBinding(t, s)
	if result := s.executeExtension(context.Background(), current, json.RawMessage(`{}`)); !result.OK {
		t.Fatal(result)
	}
	if _, err := s.cfg.Private.ApplyOwner(privateconfig.Change{Action: "archive", ID: "private:lookup", Kind: "extension", BaseRevision: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.cfg.Private.ApplyOwner(privateconfig.Change{Action: "restore", ID: "private:lookup", Kind: "extension", BaseRevision: 3}); err != nil {
		t.Fatal(err)
	}
	if result := s.executeExtension(context.Background(), current, json.RawMessage(`{}`)); result.OK {
		t.Fatal("pre-archive registration ran after restore", result)
	}
}
