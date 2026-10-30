package manager

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/envai"
	"github.com/digitalygo/smidja/internal/machinetools"
)

type bootstrapFake struct {
	calls                      int
	failAt                     int
	badTool, badAgent, badTeam bool
	inputs                     []envai.ToolDefinition
	agent                      envai.AgentInput
	team                       envai.TeamInput
}

func (f *bootstrapFake) next() error {
	f.calls++
	if f.calls == f.failAt {
		return errors.New("unknown outcome")
	}
	return nil
}
func (f *bootstrapFake) RegisterTool(_ context.Context, input envai.ToolDefinition) (envai.ToolDefinition, error) {
	if err := f.next(); err != nil {
		return envai.ToolDefinition{}, err
	}
	f.inputs = append(f.inputs, input)
	input.ID = "id_" + input.Name
	if f.badTool {
		input.Name = "wrong"
	}
	return input, nil
}
func (f *bootstrapFake) CreateAgent(_ context.Context, input envai.AgentInput) (envai.AgentDefinition, error) {
	if err := f.next(); err != nil {
		return envai.AgentDefinition{}, err
	}
	f.agent = input
	if f.badAgent {
		input.Model = "fallback"
	}
	return envai.AgentDefinition{ID: "agent", AgentInput: input}, nil
}
func (f *bootstrapFake) CreateTeam(_ context.Context, input envai.TeamInput) (envai.Team, error) {
	if err := f.next(); err != nil {
		return envai.Team{}, err
	}
	f.team = input
	if f.badTeam {
		return envai.Team{}, nil
	}
	return envai.Team{ID: "team"}, nil
}

func TestBootstrap(t *testing.T) {
	fake := &bootstrapFake{}
	result, err := Bootstrap(context.Background(), fake, "luca_manager", "explicit-model")
	if err != nil {
		t.Fatal(err)
	}
	count := len(machinetools.Definitions()) + len(ManagerToolDefinitions())
	if result.TeamID != "team" || result.AgentID != "agent" || len(result.ToolIDs) != count || len(result.ToolBindings) != count || len(fake.agent.ExternalToolsEnabled) != count {
		t.Fatal(result, fake)
	}
	if fake.agent.Model != "explicit-model" || fake.agent.Mode != "primary" || fake.team.OrchestratorID != "agent" {
		t.Fatal(fake)
	}
	for _, tool := range fake.inputs {
		if !strings.HasPrefix(tool.Name, "luca_manager_") || tool.TimeoutSeconds != 600 || tool.Kind != "client" {
			t.Fatal(tool)
		}
	}
	if result.ToolBindings["luca_manager_tasks_start"].Name != "manager.tasks_start" || result.ToolBindings["luca_manager_bash"].Name != "bash" {
		t.Fatal(result.ToolBindings)
	}
}
func TestBootstrapPartialFailure(t *testing.T) {
	count := len(machinetools.Definitions()) + len(ManagerToolDefinitions())
	for _, at := range []int{1, 3, count + 1, count + 2} {
		fake := &bootstrapFake{failAt: at}
		result, err := Bootstrap(context.Background(), fake, "instance", "model")
		if err == nil || fake.calls != at {
			t.Fatal(err, fake.calls)
		}
		expected := at - 1
		if expected > count {
			expected = count
		}
		if len(result.ToolIDs) != expected {
			t.Fatal(result, expected)
		}
		if at == count+2 && result.AgentID != "agent" {
			t.Fatal(result)
		}
	}
	for _, fake := range []*bootstrapFake{{badTool: true}, {badAgent: true}, {badTeam: true}} {
		if _, err := Bootstrap(context.Background(), fake, "instance", "model"); err == nil {
			t.Fatal("invalid remote result")
		}
	}
}
func TestBootstrapValidation(t *testing.T) {
	for _, namespace := range []string{"", "Upper", "bad-name", strings.Repeat("a", 65)} {
		fake := &bootstrapFake{}
		if _, err := Bootstrap(context.Background(), fake, namespace, "model"); err == nil || fake.calls != 0 {
			t.Fatal(namespace, err)
		}
	}
	if _, err := Bootstrap(context.Background(), nil, "instance", "model"); err == nil {
		t.Fatal("nil remote")
	}
	fake := &bootstrapFake{}
	if _, err := Bootstrap(context.Background(), fake, "instance", ""); err == nil || fake.calls != 0 {
		t.Fatal("empty model")
	}
}
