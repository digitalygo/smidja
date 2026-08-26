package providers

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/digitalygo/smidja/internal/agent"
)

var jsonNull = json.RawMessage("null")

func BuildMessages(system string, messages []*agent.Message) []WireMessage {
	out := make([]WireMessage, 0, len(messages)+1)
	if system != "" {
		out = append(out, WireMessage{Role: "system", Content: jsonString(system)})
	}
	for _, m := range messages {
		switch {
		case m.User != nil:
			out = append(out, WireMessage{Role: "user", Content: userContent(m.User.Content)})
		case m.Assistant != nil:
			out = append(out, assistantContent(m.Assistant))
		case m.ToolResult != nil:
			out = append(out, toolResultContent(m.ToolResult))
		}
	}
	return out
}

func userContent(raw json.RawMessage) json.RawMessage {
	if isJSONString(raw) {
		return raw
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil || blocks == nil {
		return raw
	}
	parts := make([]WirePart, 0, len(blocks))
	for _, b := range blocks {
		parts = append(parts, WirePart{Type: "text", Text: b.Text})
	}
	if b, err := json.Marshal(parts); err == nil {
		return b
	}
	return raw
}

func isJSONString(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) > 0 && t[0] == '"'
}

func assistantContent(a *agent.AssistantMessage) WireMessage {
	w := WireMessage{Role: "assistant"}
	var text strings.Builder
	for _, b := range a.Content {
		switch b.Type {
		case agent.BlockTypeText:
			text.WriteString(b.Text)
		case agent.BlockTypeToolCall:
			w.ToolCalls = append(w.ToolCalls, WireToolCall{
				ID:   b.ID,
				Type: "function",
				Function: WireFunctionCall{
					Name:      b.Name,
					Arguments: string(b.Arguments),
				},
			})
		}
	}
	if text.Len() > 0 {
		w.Content = jsonString(text.String())
	} else {
		w.Content = jsonNull
	}
	return w
}

func toolResultContent(t *agent.ToolResultMessage) WireMessage {
	w := WireMessage{Role: "tool", ToolCallID: t.ToolCallID}
	var text strings.Builder
	for _, b := range t.Content {
		if b.Type == agent.BlockTypeText {
			text.WriteString(b.Text)
		}
	}
	w.Content = jsonString(text.String())
	return w
}

func BuildTools(tools []agent.Tool) []WireTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]WireTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, WireTool{
			Type: "function",
			Function: WireFunction{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  t.Schema(),
			},
		})
	}
	return out
}

func jsonString(v string) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
