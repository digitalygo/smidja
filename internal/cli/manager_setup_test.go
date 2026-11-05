package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/digitalygo/smidja/internal/envai"
	"github.com/digitalygo/smidja/internal/manager"
)

func setupFixture(t *testing.T, failPath string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	tools := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer private-api-key" {
			t.Error("invalid setup method/auth")
			http.Error(w, "bad request", 400)
			return
		}
		if r.URL.Path == failPath && (failPath != "/api/v1/tools" || tools == 1) {
			http.Error(w, "fixture failure", 502)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/tools":
			var input envai.ToolDefinition
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
				http.Error(w, "bad JSON", 400)
				return
			}
			tools++
			input.ID = "tool-" + input.Name
			json.NewEncoder(w).Encode(map[string]any{"data": input})
		case "/api/v1/agents":
			var input envai.AgentInput
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
				http.Error(w, "bad JSON", 400)
				return
			}
			if input.Model != "fixture-model" || len(input.ExternalToolsEnabled) == 0 {
				t.Error("model/tools not passed")
			}
			json.NewEncoder(w).Encode(map[string]any{"data": envai.AgentDefinition{ID: "fixture-ceo", AgentInput: input}})
		case "/api/v1/teams":
			var input envai.TeamInput
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
				http.Error(w, "bad JSON", 400)
				return
			}
			if input.OrchestratorID != "fixture-ceo" {
				t.Error("team does not bind CEO")
			}
			json.NewEncoder(w).Encode(map[string]any{"data": envai.Team{ID: "fixture-team"}})
		default:
			t.Error("unexpected fixture path", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}
func setupConfig(t *testing.T, url string) string {
	return configPath(t, managerSetupConfig{EnvaiURL: url, EnvaiKeyEnv: "KEY", Namespace: "local", Model: "fixture-model"})
}

func TestManagerSetupValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		config *managerSetupConfig
	}{
		{name: "missing arguments"},
		{name: "unknown flag", args: []string{"--unknown"}},
		{name: "missing key reference", config: &managerSetupConfig{EnvaiURL: "http://127.0.0.1", Namespace: "local", Model: "model"}},
		{name: "missing key value", config: &managerSetupConfig{EnvaiURL: "http://127.0.0.1", EnvaiKeyEnv: "UNSET", Namespace: "local", Model: "model"}},
		{name: "invalid URL", config: &managerSetupConfig{EnvaiURL: ":bad", EnvaiKeyEnv: "KEY", Namespace: "local", Model: "model"}},
		{name: "invalid namespace", config: &managerSetupConfig{EnvaiURL: "http://127.0.0.1", EnvaiKeyEnv: "KEY", Namespace: "Invalid!", Model: "model"}},
		{name: "missing model", config: &managerSetupConfig{EnvaiURL: "http://127.0.0.1", EnvaiKeyEnv: "KEY", Namespace: "local"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := serviceDeps()
			args := tc.args
			if tc.config != nil {
				args = []string{"--config", configPath(t, *tc.config)}
			}
			if err := runManagerSetup(args, d); err == nil {
				t.Fatal("invalid setup accepted")
			}
			if strings.Contains(d.Stderr.(*bytes.Buffer).String(), "private-api-key") {
				t.Fatal("credential leaked")
			}
		})
	}
}

func TestManagerSetupRouteBootstrapSuccess(t *testing.T) {
	server := setupFixture(t, "")
	d := serviceDeps()
	d.Context = nil
	path := setupConfig(t, server.URL)
	if err := RunWithDeps([]string{"manager-setup", "--config", path}, d); err != nil {
		t.Fatal(err, d.Stderr)
	}
	var result manager.BootstrapResult
	if err := json.Unmarshal(d.Stdout.(*bytes.Buffer).Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Namespace != "local" || result.AgentID != "fixture-ceo" || result.TeamID != "fixture-team" || len(result.ToolIDs) == 0 || len(result.ToolBindings) != len(result.ToolIDs) {
		t.Fatal(result)
	}
	if strings.Contains(d.Stdout.(*bytes.Buffer).String(), "private-api-key") {
		t.Fatal("credential printed")
	}
}

func TestManagerSetupPreservesPartialResources(t *testing.T) {
	for _, failure := range []string{"/api/v1/tools", "/api/v1/agents", "/api/v1/teams"} {
		t.Run(failure, func(t *testing.T) {
			server := setupFixture(t, failure)
			d := serviceDeps()
			if err := runManagerSetup([]string{"--config", setupConfig(t, server.URL)}, d); err == nil {
				t.Fatal("setup failure hidden")
			}
			var result manager.BootstrapResult
			if err := json.Unmarshal(d.Stdout.(*bytes.Buffer).Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if len(result.ToolIDs) == 0 || result.TeamID != "" {
				t.Fatal("partial resource report missing", result)
			}
			if failure == "/api/v1/teams" && result.AgentID != "fixture-ceo" {
				t.Fatal("created agent omitted")
			}
			if !strings.Contains(d.Stderr.(*bytes.Buffer).String(), "retained") {
				t.Fatal(d.Stderr)
			}
		})
	}
}

func TestManagerSetupOutputFailure(t *testing.T) {
	server := setupFixture(t, "")
	d := serviceDeps()
	d.Stdout = failingWriter{}
	err := runManagerSetup([]string{"--config", setupConfig(t, server.URL)}, d)
	if err == nil || !strings.Contains(err.Error(), "write failed") {
		t.Fatal(err)
	}
}

func TestManagerSetupMalformedConfiguration(t *testing.T) {
	d := serviceDeps()
	path := configPath(t, map[string]string{"unexpected": "field"})
	if err := runManagerSetup([]string{"--config", path}, d); err == nil {
		t.Fatal("unknown config accepted")
	}
}
