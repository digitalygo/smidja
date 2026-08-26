package extensions

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/sdk"
)

func TestDefaultContextZeroValueAccessors(t *testing.T) {
	c := &defaultContext{API: &stubAPI{}, signal: context.Background()}

	if got := c.Cwd(); got != "" {
		t.Errorf("Cwd = %q, want empty", got)
	}
	if got := c.SessionManager(); got != nil {
		t.Errorf("SessionManager = %v, want nil", got)
	}
	if got := c.ModelRegistry(); got != nil {
		t.Errorf("ModelRegistry = %v, want nil", got)
	}
	if got := c.Model(); got != nil {
		t.Errorf("Model = %v, want nil", got)
	}
	if got := c.ThinkingLevel(); got != sdk.ThinkingOff {
		t.Errorf("ThinkingLevel = %q, want ThinkingOff", got)
	}
	if got := c.Signal(); got == nil {
		t.Error("Signal must not be nil")
	}
	if got := c.ContextUsage(); got != nil {
		t.Errorf("ContextUsage = %v, want nil", got)
	}
	if got := c.SystemPrompt(); got != "" {
		t.Errorf("SystemPrompt = %q, want empty", got)
	}
	c.Abort()
	c.Shutdown()
	c.Compact(sdk.CompactOptions{})

	ui := c.UI()
	if got := c.Mode(); got != sdk.ModePrint {
		t.Errorf("Mode = %q, want ModePrint", got)
	}
	if c.HasUI() {
		t.Error("HasUI = true, want false")
	}
	if _, err := ui.Select("t", nil); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Errorf("Select error = %v, want ErrModeUnsupported", err)
	}
	if _, err := ui.Input("t", ""); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Errorf("Input error = %v, want ErrModeUnsupported", err)
	}
	if _, err := ui.Editor("t", ""); !errors.Is(err, sdk.ErrModeUnsupported) {
		t.Errorf("Editor error = %v, want ErrModeUnsupported", err)
	}
	ui.Notify("n", sdk.NotifyInfo)
	ui.SetStatus("k", "v")
	ui.SetWidget("k", []string{"a"})
	ui.SetWorkingMessage("m")
	ui.SetTitle("t")
}

func TestConversionRoundTrips(t *testing.T) {
	assistant := &agent.Message{Assistant: &agent.AssistantMessage{
		Role: string(agent.RoleAssistant),
		Content: []agent.ContentBlock{
			{Type: "text", Text: "hello"},
			{Type: "thinking", Thinking: "reasoning"},
			{Type: "toolCall", ID: "call_1", Name: "read", Arguments: json.RawMessage(`{"path":"a.go"}`)},
		},
		Usage: agent.Usage{
			Input: 10, Output: 20, CacheRead: 1, CacheWrite: 2,
			Reasoning: 5, TotalTokens: 35,
			Cost: agent.Cost{Input: 0.1, Output: 0.2, Total: 0.3},
		},
	}}
	asSDK := messageToSDK(assistant)
	if asSDK.Role != string(agent.RoleAssistant) || len(asSDK.Content) != 3 {
		t.Fatalf("assistant conversion = %+v", asSDK)
	}
	if asSDK.Content[2].Type != "toolCall" || asSDK.Content[2].ID != "call_1" ||
		!reflect.DeepEqual(asSDK.Content[2].Arguments, json.RawMessage(`{"path":"a.go"}`)) {
		t.Fatalf("toolCall block conversion = %+v", asSDK.Content[2])
	}
	if asSDK.Usage == nil || asSDK.Usage.TotalTokens != 35 || asSDK.Usage.Cost.Total != 0.3 {
		t.Fatalf("usage conversion = %+v", asSDK.Usage)
	}
	back := messageFromSDK(&asSDK)
	if back.Role() != string(agent.RoleAssistant) || len(back.Assistant.Content) != 3 ||
		back.Assistant.Content[1].Thinking != "reasoning" || back.Assistant.Usage.TotalTokens != 35 {
		t.Fatalf("assistant round trip = %+v", back)
	}
	asSDK.Content[2].Arguments[0] = 'X'
	if assistant.Assistant.Content[2].Arguments[0] == 'X' {
		t.Fatal("conversion aliases the input arguments")
	}

	user := &agent.Message{User: &agent.UserMessage{
		Role:      string(agent.RoleUser),
		Content:   json.RawMessage(`"plain text"`),
		Timestamp: 7,
	}}
	userSDK := messageToSDK(user)
	if len(userSDK.Content) != 1 || userSDK.Content[0].Type != "text" || userSDK.Content[0].Text != "plain text" {
		t.Fatalf("user conversion = %+v", userSDK)
	}
	userBack := messageFromSDK(&userSDK)
	if userBack.Role() != string(agent.RoleUser) || string(userBack.User.Content) != `"plain text"` {
		t.Fatalf("user round trip = %+v", userBack)
	}

	multi := &agent.Message{User: &agent.UserMessage{
		Role:    string(agent.RoleUser),
		Content: json.RawMessage(`[{"type":"text","text":"a"},{"type":"text","text":"b"}]`),
	}}
	multiSDK := messageToSDK(multi)
	if len(multiSDK.Content) != 2 || multiSDK.Content[1].Text != "b" {
		t.Fatalf("multi-block user conversion = %+v", multiSDK)
	}
	multiBack := messageFromSDK(&multiSDK)
	var blocks []sdk.Block
	if err := json.Unmarshal(multiBack.User.Content, &blocks); err != nil || len(blocks) != 2 {
		t.Fatalf("multi-block user round trip = %s, %v", multiBack.User.Content, err)
	}

	toolRes := &agent.Message{ToolResult: &agent.ToolResultMessage{
		Role:       string(agent.RoleToolResult),
		ToolCallID: "call_9",
		Content:    []agent.ContentBlock{{Type: "text", Text: "out"}},
		IsError:    true,
	}}
	toolSDK := messageToSDK(toolRes)
	if toolSDK.Role != string(agent.RoleToolResult) || len(toolSDK.Content) != 1 {
		t.Fatalf("tool result conversion = %+v", toolSDK)
	}
	toolBack := messageFromSDK(&toolSDK)
	if toolBack.Role() != string(agent.RoleToolResult) || len(toolBack.ToolResult.Content) != 1 {
		t.Fatalf("tool result round trip = %+v", toolBack)
	}

	if got := messageToSDK(nil); got.Role != "" {
		t.Fatalf("nil conversion = %+v, want the zero message", got)
	}
	unknown := messageFromSDK(&sdk.Message{Role: "alien"})
	if unknown.Role() != "" {
		t.Fatalf("unknown role round trip = %+v, want the empty message", unknown)
	}
	if got := messageFromSDK(nil); got != nil {
		t.Fatalf("nil from-sdk = %v, want nil", got)
	}
}

func TestRawContentEdgeCases(t *testing.T) {
	if got := rawContentToBlocks(nil); got != nil {
		t.Fatalf("nil raw content = %v, want nil", got)
	}
	if got := rawContentToBlocks(json.RawMessage("null")); got != nil {
		t.Fatalf("null raw content = %v, want nil", got)
	}
	if got := rawContentToBlocks(json.RawMessage(`{"unexpected":true}`)); got != nil {
		t.Fatalf("unparseable raw content = %v, want nil", got)
	}
	if got := blocksToRawContent(nil); got != nil {
		t.Fatalf("nil blocks = %v, want nil", got)
	}
}
