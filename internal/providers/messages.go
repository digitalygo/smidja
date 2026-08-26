package providers

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/digitalygo/smidja/internal/agent"
)

// jsonNull is the JSON literal used for assistant content with no text.
var jsonNull = json.RawMessage("null")

// BuildMessages converts the conversation into wire messages, prepending
// the system prompt as the first message when it is non-empty.
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

// userContent renders a user message's raw content: JSON strings pass
// through verbatim, content-block arrays are flattened into text parts.
func userContent(raw json.RawMessage) json.RawMessage {
	if isJSONString(raw) {
		return raw
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil || blocks == nil {
		// Neither a string nor a block array: send it verbatim and let
		// the provider surface the problem.
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

// isJSONString reports whether raw is a JSON string literal.
func isJSONString(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) > 0 && t[0] == '"'
}

// assistantContent renders an assistant message: the joined text of its
// text blocks (or null) and its tool calls as OpenAI function-call wire
// objects.
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

// toolResultContent renders a tool result message with the concatenated
// text of its content blocks.
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

// BuildTools converts agent tools into OpenAI function-tool wire objects.
// It returns nil for an empty tool list so the field is omitted.
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

// jsonString returns v as a JSON string literal.
func jsonString(v string) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
