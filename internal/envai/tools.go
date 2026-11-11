package envai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
)

var toolNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,254}$`)

type ToolDefinition struct {
	ID             string          `json:"id,omitempty"`
	Name           string          `json:"name"`
	Kind           string          `json:"kind"`
	Description    string          `json:"description,omitempty"`
	Parameters     json.RawMessage `json:"parameters"`
	TimeoutSeconds int             `json:"timeout_seconds,omitempty"`
}

func (c *Client) RegisterTool(ctx context.Context, input ToolDefinition) (ToolDefinition, error) {
	var result struct {
		Data ToolDefinition `json:"data"`
	}
	var schema struct {
		Type string `json:"type"`
	}
	if !toolNamePattern.MatchString(input.Name) || input.ID != "" || (input.Kind != "" && input.Kind != "client") || json.Unmarshal(input.Parameters, &schema) != nil || schema.Type != "object" || input.TimeoutSeconds < 0 || input.TimeoutSeconds > 600 {
		return result.Data, errors.New("invalid client tool definition")
	}
	input.Kind = "client"
	err := c.json(ctx, http.MethodPost, "/api/v1/tools", input, &result, false)
	if err == nil && result.Data.ID == "" {
		err = errors.New("envai tool response has no ID")
	}
	return result.Data, err
}

func (c *Client) SetAgentClientTools(ctx context.Context, agentID string, tools []ToolReference) (AgentDefinition, error) {
	var result struct {
		Data AgentDefinition `json:"data"`
	}
	agent, err := identifier(agentID)
	if err != nil {
		return result.Data, err
	}
	if tools == nil {
		tools = []ToolReference{}
	}
	for _, tool := range tools {
		if !toolNamePattern.MatchString(tool.Name) {
			return result.Data, errors.New("invalid client tool name")
		}
	}
	err = c.json(ctx, http.MethodPut, "/api/v1/agents/"+agent, struct {
		Tools []ToolReference `json:"external_tools_enabled"`
	}{tools}, &result, false)
	if err == nil && result.Data.ID == "" {
		err = errors.New("envai agent response has no ID")
	}
	return result.Data, err
}
