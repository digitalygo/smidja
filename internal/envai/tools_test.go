package envai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestToolRegistration(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/tools" && r.Method == "POST" {
			var tool ToolDefinition
			if err := json.NewDecoder(r.Body).Decode(&tool); err != nil || tool.Kind != "client" || tool.Name != "smidja_read" {
				t.Error(tool, err)
			}
			fmt.Fprint(w, `{"data":{"id":"tool","name":"smidja_read"}}`)
			return
		}
		if r.URL.Path != "/api/v1/agents/agent" || r.Method != "PUT" {
			t.Error(r.URL, r.Method)
		}
		var payload map[string]json.RawMessage
		json.NewDecoder(r.Body).Decode(&payload)
		if string(payload["external_tools_enabled"]) != "[]" {
			t.Error(payload)
		}
		fmt.Fprint(w, `{"data":{"id":"agent"}}`)
	})
	input := ToolDefinition{Name: "smidja_read", Parameters: json.RawMessage(`{"type":"object"}`)}
	if tool, err := c.RegisterTool(context.Background(), input); err != nil || tool.ID != "tool" {
		t.Fatal(tool, err)
	}
	if agent, err := c.SetAgentClientTools(context.Background(), "agent", nil); err != nil || agent.ID != "agent" {
		t.Fatal(agent, err)
	}
	for _, mutate := range []func(*ToolDefinition){func(v *ToolDefinition) { v.Name = "bad name" }, func(v *ToolDefinition) { v.ID = "existing" }, func(v *ToolDefinition) { v.Kind = "server" }, func(v *ToolDefinition) { v.Parameters = []byte(`{}`) }, func(v *ToolDefinition) { v.TimeoutSeconds = 601 }} {
		bad := input
		mutate(&bad)
		if _, err := c.RegisterTool(context.Background(), bad); err == nil {
			t.Fatal(bad)
		}
	}
	if _, err := c.SetAgentClientTools(context.Background(), "/", nil); err == nil {
		t.Fatal("bad ID")
	}
	if _, err := c.SetAgentClientTools(context.Background(), "agent", []ToolReference{{Name: "bad name"}}); err == nil {
		t.Fatal("bad name")
	}
	c = fixture(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"data":{}}`) })
	if _, err := c.RegisterTool(context.Background(), input); err == nil {
		t.Fatal("missing tool ID")
	}
	if _, err := c.SetAgentClientTools(context.Background(), "agent", []ToolReference{{Name: "smidja_read"}}); err == nil {
		t.Fatal("missing agent ID")
	}
}
