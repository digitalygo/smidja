package manager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/digitalygo/smidja/internal/envai"
	"github.com/digitalygo/smidja/internal/privateconfig"
)

type TeamMaterializer interface {
	CreateAgent(context.Context, envai.AgentInput) (envai.AgentDefinition, error)
	CreateTeam(context.Context, envai.TeamInput) (envai.Team, error)
}
type TeamMaterialization struct {
	Namespace      string            `json:"namespace"`
	Revision       string            `json:"revision"`
	Digest         string            `json:"digest"`
	TeamID         string            `json:"team_id,omitempty"`
	OrchestratorID string            `json:"orchestrator_id,omitempty"`
	AgentIDs       map[string]string `json:"agent_ids"`
}

var remoteToolName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,254}$`)

func MaterializeTeam(ctx context.Context, remote TeamMaterializer, namespace, revision string, team privateconfig.Team, toolRefs map[string]string) (TeamMaterialization, error) {
	result := TeamMaterialization{Namespace: namespace, Revision: revision, AgentIDs: map[string]string{}}
	if remote == nil || !bootstrapNamespace.MatchString(namespace) || revision == "" || strings.TrimSpace(team.Name) == "" || len(team.Agents) == 0 {
		return result, errors.New("Lid materialization requires namespace, revision, name and at least one agent")
	}
	seen := map[string]bool{}
	inputs := make([]envai.AgentInput, 0, len(team.Agents))
	for index, agent := range team.Agents {
		if strings.TrimSpace(agent.Name) == "" || len(agent.Name) > 255 || seen[agent.Name] || strings.TrimSpace(agent.Model) == "" || strings.TrimSpace(agent.Instructions) == "" {
			return result, errors.New("agents require unique names, explicit models and instructions")
		}
		seen[agent.Name] = true
		mode := "subagent"
		if index == 0 {
			mode = "primary"
		}
		input := envai.AgentInput{Model: agent.Model, SystemPrompt: agent.Instructions, Mode: mode}
		toolsSeen := map[string]bool{}
		for _, tool := range agent.Tools {
			name, exists := toolRefs[tool]
			if !exists || !remoteToolName.MatchString(name) {
				return result, fmt.Errorf("agent %s tool %s has no valid registered reference", agent.Name, tool)
			}
			if !toolsSeen[name] {
				input.ExternalToolsEnabled = append(input.ExternalToolsEnabled, envai.ToolReference{Name: name})
				toolsSeen[name] = true
			}
		}
		inputs = append(inputs, input)
	}
	serialized, err := json.Marshal(struct {
		Revision string             `json:"revision"`
		Team     privateconfig.Team `json:"team"`
		Agents   []envai.AgentInput `json:"agents"`
	}{revision, team, inputs})
	if err != nil {
		return result, err
	}
	digest := sha256.Sum256(serialized)
	result.Digest = hex.EncodeToString(digest[:])
	prefix := namespace + "_" + result.Digest[:16]
	members := make([]envai.TeamMember, 0, len(inputs)-1)
	roster := make([]map[string]string, 0, len(inputs)-1)
	for index := range inputs {
		inputs[index].Name = fmt.Sprintf("%s_a%d", prefix, index)
		if index > 0 {
			roster = append(roster, map[string]string{"role": team.Agents[index].Name, "agent_name": inputs[index].Name})
		}
	}
	if len(roster) > 0 {
		encoded, _ := json.Marshal(roster)
		inputs[0].SystemPrompt += "\n\nSmidja Lid routing metadata: delegate_task resolves subagent_type by the exact agent_name, not the role label. Use this mapping for delegation: " + string(encoded)
	}
	for index, input := range inputs {
		agent, err := remote.CreateAgent(ctx, input)
		if err != nil {
			return result, fmt.Errorf("materialize agent %s: %w; partial resources retained, inspect before retry", team.Agents[index].Name, err)
		}
		if agent.ID != "" {
			result.AgentIDs[team.Agents[index].Name] = agent.ID
		}
		if agent.ID == "" || agent.Model != input.Model {
			return result, errors.New("materialized agent identity/model mismatch; inspect remote resource")
		}
		if index == 0 {
			result.OrchestratorID = agent.ID
		} else {
			members = append(members, envai.TeamMember{AgentDefinitionID: agent.ID, Role: team.Agents[index].Name})
		}
	}
	created, err := remote.CreateTeam(ctx, envai.TeamInput{Name: prefix + "_team", Description: team.Description, OrchestratorID: result.OrchestratorID, Members: members})
	if err != nil {
		return result, fmt.Errorf("materialize Lid: %w; created agents retained, inspect before retry", err)
	}
	result.TeamID = created.ID
	if result.TeamID == "" {
		return result, errors.New("materialized Lid returned no ID; outcome uncertain")
	}
	return result, nil
}
