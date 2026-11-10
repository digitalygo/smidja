package manager

import (
	"context"
	"encoding/json"
	"github.com/digitalygo/smidja/internal/envai"
	"github.com/digitalygo/smidja/internal/privateconfig"
	"strings"
	"testing"
)

func TestTeamCatalogMetadata(t *testing.T) {
	private := privateCatalog(t)
	savePrivate(t, private, "private:metadata", "team", 0, privateconfig.Team{Name: "Team", Description: "Purpose", Agents: []privateconfig.Agent{{Name: "Manager", Instructions: "secret instructions", Model: "model", Tools: []string{"read"}}}})
	remote := &teamDelegationRemote{teams: []envai.Team{{ID: "remote", Name: "Remote", Description: "Remote purpose", OrchestratorID: "orchestrator", Members: []envai.TeamMember{{AgentDefinitionID: "specialist", Role: "reviewer"}}}}}
	s := testServer(t, func(c *Config) { c.Private = private; c.Remote = remote })
	entries, err := s.teamCatalog(context.Background())
	if err != nil || len(entries) != 2 {
		t.Fatal(entries, err)
	}
	if entries[0]["description"] != "Remote purpose" || entries[0]["orchestrator_id"] != "orchestrator" || entries[1]["description"] != "Purpose" {
		t.Fatal(entries)
	}
	agents := entries[1]["agents"].([]map[string]any)
	if len(agents) != 1 || agents[0]["name"] != "Manager" || agents[0]["model"] != "model" {
		t.Fatal(agents)
	}
	encoded, _ := json.Marshal(entries)
	if strings.Contains(string(encoded), "secret instructions") {
		t.Fatal("instructions leaked into catalog")
	}
}
