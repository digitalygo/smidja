package subagent

import (
	"encoding/json"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
)

func TestEstimateTokensNormal(t *testing.T) {
	m := cand("r1", "hello world").Message
	if got := estimateTokens(m); got <= 0 {
		t.Errorf("estimateTokens = %d, want a positive count", got)
	}
}

func TestEstimateTokensNil(t *testing.T) {
	if got := estimateTokens(nil); got <= 0 {
		t.Errorf("estimateTokens(nil) = %d, want a positive count", got)
	}
}

func TestEstimateTokensMarshalFallbackUser(t *testing.T) {
	m := &agent.Message{User: &agent.UserMessage{
		Role: string(agent.RoleUser), Content: json.RawMessage(`{invalid`),
	}}
	if got := estimateTokens(m); got <= 0 {
		t.Errorf("estimateTokens(malformed user) = %d, want a positive fallback count", got)
	}
}

func TestEstimateTokensMarshalFallbackAssistant(t *testing.T) {
	m := &agent.Message{Assistant: &agent.AssistantMessage{
		Role: string(agent.RoleAssistant), Model: "m",
		Content: []agent.ContentBlock{{Type: agent.BlockTypeToolCall, Arguments: json.RawMessage(`{invalid`)}},
	}}
	if got := estimateTokens(m); got <= 0 {
		t.Errorf("estimateTokens(malformed assistant) = %d, want a positive fallback count", got)
	}
}

func TestRoughBytesNil(t *testing.T) {
	if got := roughBytes(nil); got != 4 {
		t.Errorf("roughBytes(nil) = %d, want 4", got)
	}
}

func TestRoughBytesUser(t *testing.T) {
	m := &agent.Message{User: &agent.UserMessage{Role: string(agent.RoleUser), Content: json.RawMessage(`"abc"`)}}
	want := int64(len(agent.RoleUser) + len(`"abc"`))
	if got := roughBytes(m); got != want {
		t.Errorf("roughBytes(user) = %d, want %d", got, want)
	}
}

func TestRoughBytesAssistant(t *testing.T) {
	m := &agent.Message{Assistant: &agent.AssistantMessage{
		Role: string(agent.RoleAssistant), Model: "m", ErrorMessage: "e",
		Content: []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "t", Thinking: "th", ID: "i", Name: "n", Arguments: json.RawMessage(`{}`)}},
	}}
	if got := roughBytes(m); got <= 0 {
		t.Errorf("roughBytes(assistant) = %d, want a positive count", got)
	}
}

func TestRoughBytesToolResult(t *testing.T) {
	m := &agent.Message{ToolResult: &agent.ToolResultMessage{
		Role: string(agent.RoleToolResult), ToolCallID: "c", ToolName: "n",
		Content: []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "t"}},
	}}
	if got := roughBytes(m); got <= 0 {
		t.Errorf("roughBytes(toolResult) = %d, want a positive count", got)
	}
}
