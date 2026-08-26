package responses

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/digitalygo/smidja/internal/agent"
)

func BuildInput(messages []*agent.Message) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(messages))
	msgIndex := 0
	for _, m := range messages {
		switch {
		case m.User != nil:
			out = append(out, userItems(m.User)...)
		case m.Assistant != nil:
			out = append(out, assistantItems(m.Assistant, msgIndex)...)
		case m.ToolResult != nil:
			out = append(out, toolResultItem(m.ToolResult))
		}
		msgIndex++
	}
	return out
}

func userItems(u *agent.UserMessage) []json.RawMessage {
	if isJSONString(u.Content) {
		var text string
		if err := json.Unmarshal(u.Content, &text); err != nil {
			return nil
		}
		return []json.RawMessage{marshalItem(inputItem{
			Type:    "message",
			Role:    "user",
			Content: []any{inputContent{Type: "input_text", Text: text}},
		})}
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(u.Content, &blocks); err != nil || blocks == nil {
		return nil
	}
	content := make([]any, 0, len(blocks))
	for _, b := range blocks {
		if b.Type == agent.BlockTypeText {
			content = append(content, inputContent{Type: "input_text", Text: b.Text})
		}
	}
	if len(content) == 0 {
		return nil
	}
	return []json.RawMessage{marshalItem(inputItem{Type: "message", Role: "user", Content: content})}
}

func assistantItems(a *agent.AssistantMessage, msgIndex int) []json.RawMessage {
	var out []json.RawMessage
	textBlockIndex := 0
	for _, b := range a.Content {
		switch b.Type {
		case agent.BlockTypeThinking:
			if b.ThinkingSignature != "" {
				out = append(out, json.RawMessage(b.ThinkingSignature))
			}
		case agent.BlockTypeText:
			id := fmt.Sprintf("msg_pi_%d", msgIndex)
			if textBlockIndex > 0 {
				id = fmt.Sprintf("msg_pi_%d_%d", msgIndex, textBlockIndex)
			}
			textBlockIndex++
			out = append(out, marshalItem(inputItem{
				Type:    "message",
				Role:    "assistant",
				Status:  "completed",
				ID:      id,
				Content: []any{outputContent{Type: "output_text", Text: b.Text, Annotations: []any{}}},
			}))
		case agent.BlockTypeToolCall:
			callID, itemID := splitToolCallID(b.ID)
			if itemID != "" && !strings.HasPrefix(itemID, "fc_") {
				itemID = ""
			}
			args := string(b.Arguments)
			if args == "" {
				args = "{}"
			}
			out = append(out, marshalItem(inputItem{
				Type:      "function_call",
				ID:        itemID,
				CallID:    callID,
				Name:      b.Name,
				Arguments: args,
			}))
		}
	}
	return out
}

func toolResultItem(t *agent.ToolResultMessage) json.RawMessage {
	callID, _ := splitToolCallID(t.ToolCallID)
	var text strings.Builder
	first := true
	for _, b := range t.Content {
		if b.Type != agent.BlockTypeText {
			continue
		}
		if !first {
			text.WriteString("\n")
		}
		first = false
		text.WriteString(b.Text)
	}
	output := text.String()
	if output == "" {
		output = "(no tool output)"
	}
	return marshalItem(inputItem{Type: "function_call_output", CallID: callID, Output: output})
}

func splitToolCallID(id string) (callID, itemID string) {
	if i := strings.Index(id, "|"); i >= 0 {
		return id[:i], id[i+1:]
	}
	return id, ""
}

func BuildTools(tools []agent.Tool, codex bool) []tool {
	if len(tools) == 0 {
		return nil
	}
	var strict *bool
	if !codex {
		f := false
		strict = &f
	}
	out := make([]tool, 0, len(tools))
	for _, t := range tools {
		out = append(out, tool{
			Type:        "function",
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Schema(),
			Strict:      strict,
		})
	}
	return out
}

func marshalItem(it inputItem) json.RawMessage {
	b, _ := json.Marshal(it)
	return b
}

func isJSONString(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) > 0 && t[0] == '"'
}
