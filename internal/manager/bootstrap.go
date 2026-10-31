package manager

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/digitalygo/smidja/internal/envai"
	"github.com/digitalygo/smidja/internal/machinetools"
)

type BootstrapRemote interface {
	RegisterTool(context.Context, envai.ToolDefinition) (envai.ToolDefinition, error)
	CreateAgent(context.Context, envai.AgentInput) (envai.AgentDefinition, error)
	CreateTeam(context.Context, envai.TeamInput) (envai.Team, error)
}

type BootstrapResult struct {
	Namespace    string                 `json:"namespace"`
	Model        string                 `json:"model"`
	AgentID      string                 `json:"agent_id,omitempty"`
	TeamID       string                 `json:"team_id,omitempty"`
	ToolIDs      map[string]string      `json:"tool_ids"`
	ToolBindings map[string]ToolBinding `json:"tool_bindings"`
}

var bootstrapNamespace = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func Bootstrap(ctx context.Context, remote BootstrapRemote, namespace, model string) (BootstrapResult, error) {
	result := BootstrapResult{Namespace: namespace, Model: model, ToolIDs: map[string]string{}, ToolBindings: map[string]ToolBinding{}}
	if remote == nil || !bootstrapNamespace.MatchString(namespace) || strings.TrimSpace(model) == "" {
		return result, errors.New("bootstrap requires a remote, lowercase namespace (1-64 letters/digits/underscores), and explicit model")
	}
	type bindingDefinition struct {
		definition machinetools.Definition
		binding    ToolBinding
	}
	definitions := make([]bindingDefinition, 0)
	for _, definition := range machinetools.Definitions() {
		definitions = append(definitions, bindingDefinition{definition, ToolBinding{Name: definition.Name}})
	}
	for _, definition := range ManagerToolDefinitions() {
		definitions = append(definitions, bindingDefinition{definition, ToolBinding{Name: "manager." + definition.Name}})
	}
	references := make([]envai.ToolReference, 0, len(definitions))
	for _, entry := range definitions {
		name := namespace + "_" + entry.definition.Name
		registered, err := remote.RegisterTool(ctx, envai.ToolDefinition{Name: name, Kind: "client", Description: entry.definition.Description, Parameters: entry.definition.Parameters, TimeoutSeconds: 600})
		if err != nil {
			return result, fmt.Errorf("bootstrap tool %s: %w; partial resources retained, inspect before retry", name, err)
		}
		if registered.ID == "" || registered.Name != name {
			return result, fmt.Errorf("bootstrap tool %s returned invalid identity; outcome uncertain", name)
		}
		result.ToolIDs[name] = registered.ID
		result.ToolBindings[name] = entry.binding
		references = append(references, envai.ToolReference{Name: name})
	}
	prompt := "You are Smidr, the owner's assistant in Smidja. Coordinate Lid and sequential Verk; do not pretend to execute tools or invent successful results. Start long-running Verk with the supplied tasks_start client tool, then remain available for new requests. Native completion events supply results later. Before reporting a task status, read tasks_get because a newer resume or cancellation may have superseded the event. Read task progress and full outputs with tasks_get. Use clients_list to discover online devices and clients_get to inspect an offline device only when relevant. Never claim an offline device is available. Use the owner's requested device, or ask when ambiguous. Ask for clarification when a Verk is blocked, then resume that task at its blocked phase. Do not replace Verk phases with prompt-only shortcuts. Bash is not a sandbox. Obtain user approval before persistent private Lid or extension changes. Installed package updates do not require a generic approval. Do not request or expose provider credentials. Tools are installation-namespaced; match their descriptions to the actions above."
	agent, err := remote.CreateAgent(ctx, envai.AgentInput{Name: namespace + "_ceo", Description: "Smidr", SystemPrompt: prompt, Model: model, Mode: "primary", ExternalToolsEnabled: references})
	if err != nil {
		return result, fmt.Errorf("bootstrap Smidr: %w; registered tools retained, inspect before retry", err)
	}
	result.AgentID = agent.ID
	if agent.ID == "" || agent.Model != model {
		return result, errors.New("bootstrap Smidr identity/model does not match requested configuration; inspect remote agent")
	}
	team, err := remote.CreateTeam(ctx, envai.TeamInput{Name: namespace + "_ceo", Description: "Smidja personal manager Smidr", OrchestratorID: agent.ID})
	if err != nil {
		return result, fmt.Errorf("bootstrap Lid: %w; created agent and tools retained, inspect before retry", err)
	}
	result.TeamID = team.ID
	if team.ID == "" {
		return result, errors.New("bootstrap Lid returned no ID; outcome uncertain")
	}
	return result, nil
}
