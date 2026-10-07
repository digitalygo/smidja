package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type mutableActiveCatalog struct {
	tools  []Tool
	active map[string]bool
}

func (c *mutableActiveCatalog) Tools() []Tool {
	var out []Tool
	for _, t := range c.tools {
		if t != nil && c.active[t.Name()] {
			out = append(out, t)
		}
	}
	return out
}

func (c *mutableActiveCatalog) Get(name string) (Tool, bool) {
	for _, t := range c.tools {
		if t != nil && t.Name() == name {
			return t, true
		}
	}
	return nil, false
}

func (c *mutableActiveCatalog) GetActive(name string) (Tool, bool) {
	if !c.active[name] {
		return nil, false
	}
	return c.Get(name)
}

func TestRunTurnDisabledActiveToolIsNotExecuted(t *testing.T) {
	probe := &fakeTool{name: "exec", result: TextResult("ran")}
	catalog := &mutableActiveCatalog{tools: []Tool{probe}, active: map[string]bool{"exec": true}}
	hooks := &fakeHooks{toolCallFn: func(ctx context.Context, name, callID string, args json.RawMessage) (ToolCallDecision, error) {
		catalog.active["exec"] = false
		return ToolCallDecision{}, nil
	}}
	client := &fakeClient{script: []*AssistantMessage{
		toolUseMsg(toolCallBlock("c1", "exec", `{"command":`)),
		textStop("done"),
	}}
	deps := &LoopDeps{Client: client, Catalog: catalog, Hooks: hooks}
	history, err := RunTurn(context.Background(), deps, "test/model", "", nil, "hello")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if probe.executed() != 0 {
		t.Fatalf("disabled tool executed %d times, want 0", probe.executed())
	}
	if len(history) != 4 {
		t.Fatalf("history length = %d, want 4", len(history))
	}
	tr := history[2].ToolResult
	if tr == nil || !tr.IsError {
		t.Fatal("disabled tool must record an error result")
	}
	if len(tr.Content) != 1 || !strings.Contains(tr.Content[0].Text, "unknown tool") {
		t.Fatalf("tool result = %+v, want an unknown-tool error", tr.Content)
	}
	if client.lastReq == nil {
		t.Fatal("client received no request")
	}
	for _, tool := range client.lastReq.Tools {
		if tool != nil && tool.Name() == "exec" {
			t.Fatal("disabled tool must not stay advertised to the model")
		}
	}
}
