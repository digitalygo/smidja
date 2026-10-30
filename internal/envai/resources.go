package envai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

type TeamMember struct {
	AgentDefinitionID string `json:"agent_definition_id"`
	Role              string `json:"role,omitempty"`
}

type Team struct {
	ID             string       `json:"id"`
	Name           string       `json:"name"`
	Description    string       `json:"description,omitempty"`
	OrchestratorID string       `json:"orchestrator_id"`
	Members        []TeamMember `json:"members,omitempty"`
}

type TeamInput struct {
	Name           string       `json:"name"`
	Description    string       `json:"description,omitempty"`
	OrchestratorID string       `json:"orchestrator_id"`
	Members        []TeamMember `json:"members,omitempty"`
}

type Chat struct {
	ID          string  `json:"id"`
	TeamID      string  `json:"team_id"`
	WorkspaceID string  `json:"workspace_id"`
	Title       *string `json:"title"`
}

type Turn struct {
	Status string `json:"status"`
	TurnID string `json:"turn_id"`
}

func (c *Client) ListTeams(ctx context.Context) ([]Team, error) {
	var result struct {
		Data []Team `json:"data"`
	}
	err := c.json(ctx, http.MethodGet, "/api/v1/teams", nil, &result, false)
	return result.Data, err
}

func (c *Client) CreateTeam(ctx context.Context, input TeamInput) (Team, error) {
	var result struct {
		Data Team `json:"data"`
	}
	if input.Name == "" || input.OrchestratorID == "" {
		return result.Data, errors.New("team name and orchestrator are required")
	}
	err := c.json(ctx, http.MethodPost, "/api/v1/teams", input, &result, false)
	if err == nil && result.Data.ID == "" {
		err = errors.New("envai team response has no ID")
	}
	return result.Data, err
}

func (c *Client) CreateChat(ctx context.Context, workspaceID, teamID string) (Chat, error) {
	var result struct {
		Data Chat `json:"data"`
	}
	workspace, err := identifier(workspaceID)
	if err != nil {
		return result.Data, err
	}
	if _, err = identifier(teamID); err != nil {
		return result.Data, err
	}
	err = c.json(ctx, http.MethodPost, "/api/v1/workspaces/"+workspace+"/chats", map[string]string{"team_id": teamID}, &result, false)
	if err == nil && result.Data.ID == "" {
		err = errors.New("envai chat response has no ID")
	}
	return result.Data, err
}

func (c *Client) ListChats(ctx context.Context, workspaceID string) ([]Chat, error) {
	workspace, err := identifier(workspaceID)
	if err != nil {
		return nil, err
	}
	var result struct {
		Data []Chat `json:"data"`
	}
	err = c.json(ctx, http.MethodGet, "/api/v1/workspaces/"+workspace+"/chats", nil, &result, false)
	return result.Data, err
}

func (c *Client) StartTurn(ctx context.Context, chatID, message string) (Turn, error) {
	var result Turn
	chat, err := identifier(chatID)
	if err != nil {
		return result, err
	}
	if message == "" {
		return result, errors.New("message is required")
	}
	if err = c.RequireFeatures(ctx); err != nil {
		return result, err
	}
	err = c.json(ctx, http.MethodPost, "/api/v1/atp/v3/chats/"+chat+"/messages", map[string]string{"message": message}, &result, true)
	if err == nil && result.TurnID == "" {
		err = errors.New("envai accepted response has no turn ID; outcome unknown")
	}
	return result, err
}

func (c *Client) CancelTurn(ctx context.Context, chatID string) error {
	chat, err := identifier(chatID)
	if err != nil {
		return err
	}
	return c.json(ctx, http.MethodPost, "/api/v1/atp/v3/chats/"+chat+"/cancel", struct{}{}, nil, true)
}

func (c *Client) SubmitToolOutput(ctx context.Context, chatID, callID, output string, isError bool) error {
	chat, err := identifier(chatID)
	if err != nil {
		return err
	}
	if callID == "" || len(callID) > 64 || output == "" || len(output) > 131072 {
		return errors.New("invalid tool output: call ID and output required, maximum 128 KiB")
	}
	return c.json(ctx, http.MethodPost, "/api/v1/atp/v3/chats/"+chat+"/tool-output", struct {
		CallID  string `json:"call_id"`
		Output  string `json:"output"`
		IsError bool   `json:"is_error"`
	}{callID, output, isError}, nil, true)
}

type ToolRequest struct {
	CallID    string          `json:"call_id"`
	ToolName  string          `json:"tool_name"`
	Kind      string          `json:"kind"`
	Args      json.RawMessage `json:"args"`
	Signature Signature       `json:"signature"`
}

func (e Event) ToolRequest() (ToolRequest, error) {
	var request ToolRequest
	if e.Type != "agent.tool.request" {
		return request, errors.New("event is not a tool request")
	}
	if err := json.Unmarshal(e.Data, &request); err != nil {
		return request, err
	}
	if request.CallID == "" || request.ToolName == "" || request.Signature.Alg != "EdDSA" || request.Signature.KeyID == "" || request.Signature.Value == "" || e.ChatID == "" || e.TurnID == "" || e.Agent == nil || e.Agent.InstanceID == "" {
		return request, errors.New("incomplete tool request binding or signature")
	}
	return request, nil
}

type Signature struct {
	Alg   string `json:"alg"`
	KeyID string `json:"kid"`
	Value string `json:"sig"`
}

type ToolReference struct {
	Name string `json:"name"`
}
type AgentInput struct {
	Name                 string          `json:"name"`
	Description          string          `json:"description,omitempty"`
	SystemPrompt         string          `json:"system_prompt"`
	Model                string          `json:"model,omitempty"`
	Mode                 string          `json:"mode"`
	ToolsEnabled         []ToolReference `json:"tools_enabled,omitempty"`
	ExternalToolsEnabled []ToolReference `json:"external_tools_enabled,omitempty"`
}
type AgentDefinition struct {
	ID string `json:"id"`
	AgentInput
}

func (c *Client) CreateAgent(ctx context.Context, input AgentInput) (AgentDefinition, error) {
	var result struct {
		Data AgentDefinition `json:"data"`
	}
	if input.Name == "" || input.SystemPrompt == "" || (input.Mode != "primary" && input.Mode != "subagent") {
		return result.Data, errors.New("agent name, system prompt, and valid mode required")
	}
	err := c.json(ctx, http.MethodPost, "/api/v1/agents", input, &result, false)
	if err == nil && result.Data.ID == "" {
		err = errors.New("envai agent response has no ID")
	}
	return result.Data, err
}
