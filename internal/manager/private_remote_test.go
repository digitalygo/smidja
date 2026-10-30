package manager

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/envai"
	"github.com/digitalygo/smidja/internal/privateconfig"
)

type teamRemote struct {
	agents                            []envai.AgentInput
	team                              envai.TeamInput
	calls, failAt                     int
	wrongModel, emptyAgent, emptyTeam bool
}

func (f *teamRemote) CreateAgent(_ context.Context, input envai.AgentInput) (envai.AgentDefinition, error) {
	f.calls++
	if f.calls == f.failAt {
		return envai.AgentDefinition{}, errors.New("uncertain")
	}
	f.agents = append(f.agents, input)
	if f.wrongModel {
		input.Model = "wrong"
	}
	id := fmt.Sprintf("agent-%d", len(f.agents))
	if f.emptyAgent {
		id = ""
	}
	return envai.AgentDefinition{ID: id, AgentInput: input}, nil
}
func (f *teamRemote) CreateTeam(_ context.Context, input envai.TeamInput) (envai.Team, error) {
	f.calls++
	if f.calls == f.failAt {
		return envai.Team{}, errors.New("uncertain")
	}
	f.team = input
	if f.emptyTeam {
		return envai.Team{}, nil
	}
	return envai.Team{ID: "team"}, nil
}
func teamInput() privateconfig.Team {
	return privateconfig.Team{Name: "Odoo", Agents: []privateconfig.Agent{{Name: "manager", Instructions: "Coordinate leads and tasks", Model: "model-a", Tools: []string{"read", "read"}}, {Name: "leads", Instructions: "Handle leads", Model: "model-b", Tools: []string{"read"}}, {Name: "tasks", Instructions: "Handle tasks", Model: "model-c"}}}
}
func TestMaterializeTeam(t *testing.T) {
	fake := &teamRemote{}
	input := teamInput()
	refs := map[string]string{"read": "instance_odoo_read"}
	result, err := MaterializeTeam(context.Background(), fake, "instance", "revision1", input, refs)
	if err != nil {
		t.Fatal(err)
	}
	if result.TeamID != "team" || result.OrchestratorID != "agent-1" || len(result.AgentIDs) != 3 || len(result.Digest) != 64 {
		t.Fatal(result)
	}
	if fake.agents[0].Mode != "primary" || fake.agents[1].Mode != "subagent" || fake.agents[2].Mode != "subagent" || len(fake.agents[0].ExternalToolsEnabled) != 1 || fake.agents[0].ExternalToolsEnabled[0].Name != "instance_odoo_read" {
		t.Fatal(fake.agents)
	}
	if !strings.Contains(fake.agents[0].SystemPrompt, fake.agents[1].Name) || !strings.Contains(fake.agents[0].SystemPrompt, `"role":"leads"`) {
		t.Fatal("missing exact delegation mapping", fake.agents[0].SystemPrompt)
	}
	if len(fake.team.Members) != 2 || fake.team.Members[0].AgentDefinitionID != "agent-2" || fake.team.Members[0].Role != "leads" || fake.team.OrchestratorID != "agent-1" {
		t.Fatal(fake.team)
	}
	changed := &teamRemote{}
	next, err := MaterializeTeam(context.Background(), changed, "instance", "revision2", input, refs)
	if err != nil || next.Digest == result.Digest || changed.team.Name == fake.team.Name {
		t.Fatal(next, err)
	}
	single := teamInput()
	single.Agents = single.Agents[:1]
	if _, err := MaterializeTeam(context.Background(), &teamRemote{}, "instance", "1", single, refs); err != nil {
		t.Fatal(err)
	}
}
func TestMaterializeTeamPreflight(t *testing.T) {
	for _, mutate := range []func(*privateconfig.Team){func(v *privateconfig.Team) { v.Name = "" }, func(v *privateconfig.Team) { v.Agents = nil }, func(v *privateconfig.Team) { v.Agents[1].Model = "" }, func(v *privateconfig.Team) { v.Agents[1].Instructions = "" }, func(v *privateconfig.Team) { v.Agents[1].Name = v.Agents[0].Name }, func(v *privateconfig.Team) { v.Agents[1].Tools = []string{"unknown"} }} {
		input := teamInput()
		mutate(&input)
		fake := &teamRemote{}
		if _, err := MaterializeTeam(context.Background(), fake, "instance", "1", input, map[string]string{"read": "remote_read"}); err == nil || fake.calls != 0 {
			t.Fatal(err, fake.calls)
		}
	}
	if _, err := MaterializeTeam(context.Background(), nil, "instance", "1", teamInput(), nil); err == nil {
		t.Fatal("nil remote")
	}
}
func TestMaterializeTeamPartialFailure(t *testing.T) {
	for at := 1; at <= 4; at++ {
		fake := &teamRemote{failAt: at}
		result, err := MaterializeTeam(context.Background(), fake, "instance", "1", teamInput(), map[string]string{"read": "remote_read"})
		if err == nil || fake.calls != at || len(result.AgentIDs) != at-1 {
			t.Fatal(result, err, fake.calls)
		}
	}
	for _, fake := range []*teamRemote{{wrongModel: true}, {emptyAgent: true}, {emptyTeam: true}} {
		if _, err := MaterializeTeam(context.Background(), fake, "instance", "1", teamInput(), map[string]string{"read": "remote_read"}); err == nil {
			t.Fatal("invalid remote identity")
		}
	}
}
