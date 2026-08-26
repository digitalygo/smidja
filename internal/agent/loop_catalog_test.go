package agent

import (
	"context"
	"testing"
)

type staticCatalog struct {
	tools []Tool
}

func (c *staticCatalog) Tools() []Tool {
	return c.tools
}

func (c *staticCatalog) Get(name string) (Tool, bool) {
	for _, t := range c.tools {
		if t != nil && t.Name() == name {
			return t, true
		}
	}
	return nil, false
}

func TestRunTurnCatalogToolsReachRequestAndExecute(t *testing.T) {
	catalogTool := &fakeTool{name: "catalog-tool", result: TextResult("from catalog")}
	client := &fakeClient{script: []*AssistantMessage{
		toolUseMsg(toolCallBlock("c1", "catalog-tool", `{"x":1}`)),
		textStop("done"),
	}}
	deps := &LoopDeps{
		Client:  client,
		Catalog: &staticCatalog{tools: []Tool{catalogTool}},
	}
	history, err := RunTurn(context.Background(), deps, "test/model", "", nil, "hello")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if client.lastReq == nil {
		t.Fatal("client received no request")
	}
	if len(client.lastReq.Tools) != 1 || client.lastReq.Tools[0].Name() != "catalog-tool" {
		t.Fatalf("request tools = %v, want the catalog tool only", requestToolNames(client.lastReq.Tools))
	}
	if catalogTool.executed() != 1 {
		t.Fatalf("catalog tool executed %d times, want 1", catalogTool.executed())
	}
	if got := catalogTool.lastArgs(); string(got) != `{"x":1}` {
		t.Fatalf("catalog tool args = %s", got)
	}
	if len(history) != 4 {
		t.Fatalf("history length = %d, want 4 (user, assistant, tool result, assistant)", len(history))
	}
	if tr := history[len(history)-1].ToolResult; tr != nil && tr.IsError {
		t.Fatalf("tool result errored: %+v", tr.Content)
	}
}

func TestRunTurnCatalogTakesPrecedenceOverTools(t *testing.T) {
	legacyTool := &fakeTool{name: "legacy", result: TextResult("legacy")}
	catalogTool := &fakeTool{name: "catalog", result: TextResult("catalog")}
	client := &fakeClient{script: []*AssistantMessage{
		toolUseMsg(toolCallBlock("c1", "catalog", `{}`)),
		textStop("done"),
	}}
	deps := &LoopDeps{
		Client:  client,
		Tools:   []Tool{legacyTool},
		Catalog: &staticCatalog{tools: []Tool{catalogTool}},
	}
	if _, err := RunTurn(context.Background(), deps, "test/model", "", nil, "hello"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if catalogTool.executed() != 1 {
		t.Fatalf("catalog tool executed %d times, want 1", catalogTool.executed())
	}
	if legacyTool.executed() != 0 {
		t.Fatalf("legacy tool executed %d times, want 0", legacyTool.executed())
	}
}

func TestRunTurnCatalogUnknownToolErrors(t *testing.T) {
	client := &fakeClient{script: []*AssistantMessage{
		toolUseMsg(toolCallBlock("c1", "nope", `{}`)),
		textStop("done"),
	}}
	deps := &LoopDeps{
		Client:  client,
		Catalog: &staticCatalog{tools: []Tool{}},
	}
	history, err := RunTurn(context.Background(), deps, "test/model", "", nil, "hello")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if len(history) != 4 {
		t.Fatalf("history length = %d, want 4 (user, assistant, tool result, assistant)", len(history))
	}
	tr := history[2].ToolResult
	if tr == nil || !tr.IsError {
		t.Fatal("unknown catalog tool must record an error result")
	}
	if len(tr.Content) != 1 || tr.Content[0].Type != BlockTypeText || tr.Content[0].Text == "" {
		t.Fatalf("tool result content = %+v", tr.Content)
	}
}

func TestRunTurnNilCatalogFallsBackToLegacyTools(t *testing.T) {
	legacyTool := &fakeTool{name: "legacy", result: TextResult("legacy")}
	client := &fakeClient{script: []*AssistantMessage{
		toolUseMsg(toolCallBlock("c1", "legacy", `{}`)),
		textStop("done"),
	}}
	deps := &LoopDeps{
		Client: client,
		Tools:  []Tool{legacyTool},
	}
	if _, err := RunTurn(context.Background(), deps, "test/model", "", nil, "hello"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if legacyTool.executed() != 1 {
		t.Fatalf("legacy tool executed %d times, want 1", legacyTool.executed())
	}
	if client.lastReq == nil || len(client.lastReq.Tools) != 1 || client.lastReq.Tools[0].Name() != "legacy" {
		t.Fatalf("request tools = %v, want the legacy tool", requestToolNames(client.lastReq.Tools))
	}
}

func requestToolNames(tools []Tool) []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		if t != nil {
			out = append(out, t.Name())
		}
	}
	return out
}
