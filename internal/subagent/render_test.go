package subagent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
)

func TestRenderMessageNil(t *testing.T) {
	if got := renderMessage(nil); got != "(unknown message)" {
		t.Errorf("renderMessage(nil) = %q, want the unknown placeholder", got)
	}
}

func TestRenderMessageUnknown(t *testing.T) {
	if got := renderMessage(&agent.Message{}); got != "(unknown message)" {
		t.Errorf("renderMessage(empty) = %q, want the unknown placeholder", got)
	}
}

func TestRenderMessageUser(t *testing.T) {
	m := cand("r1", "hello world").Message
	got := renderMessage(m)
	if !strings.HasPrefix(got, "user: ") || !strings.Contains(got, "hello world") {
		t.Errorf("renderMessage(user) = %q, want a user-prefixed render", got)
	}
}

func TestRenderMessageAssistantText(t *testing.T) {
	m := &agent.Message{Assistant: &agent.AssistantMessage{
		Role:  string(agent.RoleAssistant),
		Model: "test/model",
		Content: []agent.ContentBlock{
			{Type: agent.BlockTypeText, Text: "first part"},
			{Type: agent.BlockTypeThinking, Thinking: "hidden"},
			{Type: agent.BlockTypeText, Text: "second part"},
		},
	}}
	got := renderMessage(m)
	if !strings.HasPrefix(got, "assistant: ") {
		t.Errorf("renderMessage(assistant) = %q, want the assistant prefix", got)
	}
	if !strings.Contains(got, "first partsecond part") {
		t.Errorf("renderMessage(assistant) = %q, want the concatenated text blocks", got)
	}
	if strings.Contains(got, "hidden") {
		t.Errorf("renderMessage(assistant) = %q, must not include thinking text", got)
	}
}

func TestRenderMessageAssistantToolCall(t *testing.T) {
	m := &agent.Message{Assistant: &agent.AssistantMessage{
		Role: string(agent.RoleAssistant),
		Content: []agent.ContentBlock{{
			Type: agent.BlockTypeToolCall, Name: "read", ID: "call_9", Arguments: json.RawMessage(`{"path":"a.go"}`),
		}},
	}}
	got := renderMessage(m)
	if !strings.Contains(got, "tool call read(") || !strings.Contains(got, `{"path":"a.go"}`) || !strings.Contains(got, "call_9") {
		t.Errorf("renderMessage(toolCall) = %q, want the tool call render", got)
	}
}

func TestRenderMessageAssistantEmpty(t *testing.T) {
	m := &agent.Message{Assistant: &agent.AssistantMessage{Role: string(agent.RoleAssistant)}}
	if got := renderMessage(m); got != "assistant: (empty)" {
		t.Errorf("renderMessage(empty assistant) = %q, want assistant: (empty)", got)
	}
}

func TestRenderMessageToolResult(t *testing.T) {
	m := &agent.Message{ToolResult: &agent.ToolResultMessage{
		Role: string(agent.RoleToolResult), ToolCallID: "call_1", ToolName: "read",
		Content: []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "file body"}},
	}}
	got := renderMessage(m)
	if !strings.HasPrefix(got, "tool result for call_1 (read): file body") {
		t.Errorf("renderMessage(toolResult) = %q, want the tool result render", got)
	}
	if strings.Contains(got, "error") {
		t.Errorf("renderMessage(toolResult success) = %q, must not flag an error", got)
	}
}

func TestRenderMessageToolResultError(t *testing.T) {
	m := &agent.Message{ToolResult: &agent.ToolResultMessage{
		Role: string(agent.RoleToolResult), ToolCallID: "call_2", ToolName: "edit", IsError: true,
		Content: []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "boom"}},
	}}
	got := renderMessage(m)
	if !strings.Contains(got, "(error)") || !strings.Contains(got, "boom") {
		t.Errorf("renderMessage(toolResult error) = %q, want the error flag and body", got)
	}
}

func TestUserContentTextString(t *testing.T) {
	raw, _ := json.Marshal("plain string")
	if got := userContentText(raw); got != "plain string" {
		t.Errorf("userContentText(string) = %q, want the decoded string", got)
	}
}

func TestUserContentTextBlocks(t *testing.T) {
	raw := json.RawMessage(`[{"type":"text","text":"one"},{"type":"thinking","text":"skip"},{"type":"text","text":"two"}]`)
	got := userContentText(raw)
	if got != "one two" {
		t.Errorf("userContentText(blocks) = %q, want the joined text blocks", got)
	}
}

func TestUserContentTextRawFallback(t *testing.T) {
	raw := json.RawMessage(`{not json`)
	if got := userContentText(raw); got != string(raw) {
		t.Errorf("userContentText(garbage) = %q, want the raw bytes", got)
	}
}

func TestTextOfNil(t *testing.T) {
	if got := textOf(nil); got != "" {
		t.Errorf("textOf(nil) = %q, want empty string", got)
	}
}

func TestTextOfTrims(t *testing.T) {
	m := &agent.AssistantMessage{Content: []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "  padded  "}}}
	if got := textOf(m); got != "padded" {
		t.Errorf("textOf = %q, want trimmed text", got)
	}
}
