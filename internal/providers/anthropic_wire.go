package providers

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/digitalygo/smidja/internal/agent"
)

// AnthropicRequest is the messages API request body. System, Tools, and
// ToolChoice are omitted when empty; tool_choice is only valid alongside
// tools, so the builder emits it exactly then.
type AnthropicRequest struct {
	Model      string                 `json:"model"`
	MaxTokens  int64                  `json:"max_tokens"`
	System     []AnthropicSystemBlock `json:"system,omitempty"`
	Messages   []AnthropicMessage     `json:"messages"`
	Tools      []AnthropicTool        `json:"tools,omitempty"`
	ToolChoice *AnthropicToolChoice   `json:"tool_choice,omitempty"`
	Stream     bool                   `json:"stream"`
}

// AnthropicSystemBlock is one system prompt block.
type AnthropicSystemBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// AnthropicMessage is one conversation message. Content is a JSON string
// for user text or an array of content blocks for user tool results and
// assistant turns.
type AnthropicMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// AnthropicTextBlock is a text content block.
type AnthropicTextBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// AnthropicThinkingBlock is an extended-thinking block with its signature.
type AnthropicThinkingBlock struct {
	Type      string `json:"type"`
	Thinking  string `json:"thinking"`
	Signature string `json:"signature"`
}

// AnthropicRedactedThinkingBlock replays a redacted thinking block as the
// opaque payload the provider originally sent.
type AnthropicRedactedThinkingBlock struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

// AnthropicToolUseBlock is a tool invocation requested by the model.
type AnthropicToolUseBlock struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// AnthropicToolResultBlock reports one tool execution inside a user
// message.
type AnthropicToolResultBlock struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error"`
}

// AnthropicTool is a tool exposed to the model.
type AnthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// AnthropicToolChoice steers tool use; the driver always sends auto.
type AnthropicToolChoice struct {
	Type string `json:"type"`
}

// buildAnthropicRequest assembles the messages request for one turn.
func buildAnthropicRequest(d *Anthropic, req *agent.TurnRequest) AnthropicRequest {
	out := AnthropicRequest{
		Model:     req.Model,
		MaxTokens: d.maxTokens,
		Messages:  buildAnthropicMessages(req.Messages, d.oauth),
		Stream:    true,
	}
	if req.System != "" || d.oauth {
		out.System = buildAnthropicSystem(req.System, d.oauth)
	}
	if tools := buildAnthropicTools(req.Tools, d.oauth); len(tools) > 0 {
		out.Tools = tools
		out.ToolChoice = &AnthropicToolChoice{Type: "auto"}
	}
	return out
}

// buildAnthropicSystem renders the system prompt as text blocks. With
// subscription auth the Claude Code identity block is prepended, exactly as
// Pi does. It returns nil when there is nothing to send so the system field
// is omitted.
func buildAnthropicSystem(system string, oauth bool) []AnthropicSystemBlock {
	if !oauth && system == "" {
		return nil
	}
	blocks := make([]AnthropicSystemBlock, 0, 2)
	if oauth {
		blocks = append(blocks, AnthropicSystemBlock{Type: "text", Text: claudeCodeSystemPrefix})
	}
	if system != "" {
		blocks = append(blocks, AnthropicSystemBlock{Type: "text", Text: system})
	}
	return blocks
}

// buildAnthropicMessages converts the conversation into messages API
// messages. Consecutive tool results are grouped into a single user message
// with one tool_result block each, mirroring Pi's convertMessages.
func buildAnthropicMessages(messages []*agent.Message, oauth bool) []AnthropicMessage {
	out := make([]AnthropicMessage, 0, len(messages))
	for i := 0; i < len(messages); i++ {
		m := messages[i]
		switch {
		case m.User != nil:
			content := anthropicUserContent(m.User.Content)
			if content == nil {
				continue
			}
			out = append(out, AnthropicMessage{Role: "user", Content: content})
		case m.Assistant != nil:
			blocks := anthropicAssistantContent(m.Assistant, oauth)
			if len(blocks) == 0 {
				continue
			}
			out = append(out, AnthropicMessage{Role: "assistant", Content: rawList(blocks)})
		case m.ToolResult != nil:
			blocks := make([]json.RawMessage, 0, 2)
			for i < len(messages) && messages[i].ToolResult != nil {
				blocks = append(blocks, rawMessage(anthropicToolResultBlock(messages[i].ToolResult)))
				i++
			}
			i--
			out = append(out, AnthropicMessage{Role: "user", Content: rawList(blocks)})
		}
	}
	return out
}

// anthropicUserContent renders a user message's raw content: JSON strings
// pass through verbatim, content-block arrays become text blocks with empty
// blocks dropped. It returns nil when nothing remains to send, mirroring
// Pi's convertMessages which skips empty user turns.
func anthropicUserContent(raw json.RawMessage) json.RawMessage {
	if isJSONString(raw) {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil && strings.TrimSpace(s) == "" {
			return nil
		}
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
	parts := make([]json.RawMessage, 0, len(blocks))
	for _, b := range blocks {
		if strings.TrimSpace(b.Text) == "" {
			continue
		}
		parts = append(parts, rawMessage(AnthropicTextBlock{Type: "text", Text: b.Text}))
	}
	if len(parts) == 0 {
		return nil
	}
	return rawList(parts)
}

// anthropicAssistantContent renders an assistant message as content blocks:
// text blocks, thinking blocks with their signatures (redacted thinking as
// the opaque redacted_thinking payload), and tool_use blocks. Block order
// is preserved. A thinking block without a signature is demoted to plain
// text, and one without text or signature is dropped, mirroring Pi's
// convertMessages.
func anthropicAssistantContent(a *agent.AssistantMessage, oauth bool) []json.RawMessage {
	blocks := make([]json.RawMessage, 0, len(a.Content))
	for _, b := range a.Content {
		switch b.Type {
		case agent.BlockTypeText:
			if strings.TrimSpace(b.Text) == "" {
				continue
			}
			blocks = append(blocks, rawMessage(AnthropicTextBlock{Type: "text", Text: b.Text}))
		case agent.BlockTypeThinking:
			if b.Redacted {
				blocks = append(blocks, rawMessage(AnthropicRedactedThinkingBlock{Type: "redacted_thinking", Data: b.ThinkingSignature}))
				continue
			}
			sig := strings.TrimSpace(b.ThinkingSignature)
			if strings.TrimSpace(b.Thinking) == "" && sig == "" {
				continue
			}
			if sig != "" {
				blocks = append(blocks, rawMessage(AnthropicThinkingBlock{Type: "thinking", Thinking: b.Thinking, Signature: b.ThinkingSignature}))
			} else {
				blocks = append(blocks, rawMessage(AnthropicTextBlock{Type: "text", Text: b.Thinking}))
			}
		case agent.BlockTypeToolCall:
			blocks = append(blocks, rawMessage(AnthropicToolUseBlock{
				Type:  "tool_use",
				ID:    b.ID,
				Name:  toolNameToWire(b.Name, oauth),
				Input: toolInput(b.Arguments),
			}))
		}
	}
	return blocks
}

// anthropicToolResultBlock renders one tool result as a tool_result block,
// with the text of its content blocks joined by newlines (Pi joins with
// "\n").
func anthropicToolResultBlock(t *agent.ToolResultMessage) AnthropicToolResultBlock {
	parts := make([]string, 0, len(t.Content))
	for _, b := range t.Content {
		if b.Type == agent.BlockTypeText {
			parts = append(parts, b.Text)
		}
	}
	return AnthropicToolResultBlock{
		Type:      "tool_result",
		ToolUseID: t.ToolCallID,
		Content:   strings.Join(parts, "\n"),
		IsError:   t.IsError,
	}
}

// buildAnthropicTools converts agent tools into messages API tools, using
// the Claude Code canonical casing for tool names under subscription auth.
func buildAnthropicTools(tools []agent.Tool, oauth bool) []AnthropicTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]AnthropicTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, AnthropicTool{
			Name:        toolNameToWire(t.Name(), oauth),
			Description: t.Description(),
			InputSchema: anthropicInputSchema(t.Schema()),
		})
	}
	return out
}

// anthropicInputSchema builds the input_schema the messages API expects: an
// object carrying the tool schema's properties and required lists, ported
// from Pi's legacyInputSchema. Unparseable schemas degrade to an empty
// object schema.
func anthropicInputSchema(raw json.RawMessage) json.RawMessage {
	var schema map[string]json.RawMessage
	if err := json.Unmarshal(raw, &schema); err != nil {
		return json.RawMessage(`{"type":"object","properties":{},"required":[]}`)
	}
	properties := json.RawMessage(`{}`)
	if p, ok := schema["properties"]; ok {
		properties = p
	}
	required := json.RawMessage(`[]`)
	if r, ok := schema["required"]; ok {
		required = r
	}
	return rawMessage(struct {
		Type       string          `json:"type"`
		Properties json.RawMessage `json:"properties"`
		Required   json.RawMessage `json:"required"`
	}{Type: "object", Properties: properties, Required: required})
}

// toolInput returns the tool arguments as a JSON object, defaulting to an
// empty object when the block carries none (the messages API rejects null
// and non-object input).
func toolInput(args json.RawMessage) json.RawMessage {
	if isJSONObject(args) {
		return args
	}
	return json.RawMessage("{}")
}

// isJSONObject reports whether raw is a JSON object literal.
func isJSONObject(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) > 0 && t[0] == '{'
}

// claudeCodeToolNames maps lowercase Claude Code tool names to their
// canonical casing, which subscription (OAuth) auth requires so the gateway
// recognizes the tools. Ported from Pi's stealth-mode tool list.
var claudeCodeToolNames = map[string]string{
	"read": "Read", "write": "Write", "edit": "Edit", "bash": "Bash",
	"grep": "Grep", "glob": "Glob", "askuserquestion": "AskUserQuestion",
	"enterplanmode": "EnterPlanMode", "exitplanmode": "ExitPlanMode",
	"killshell": "KillShell", "notebookedit": "NotebookEdit", "skill": "Skill",
	"task": "Task", "taskoutput": "TaskOutput", "todowrite": "TodoWrite",
	"webfetch": "WebFetch", "websearch": "WebSearch",
}

// toolNameToWire applies the Claude Code canonical casing when the turn
// uses subscription auth; other names pass through unchanged.
func toolNameToWire(name string, oauth bool) string {
	if !oauth {
		return name
	}
	if cc, ok := claudeCodeToolNames[strings.ToLower(name)]; ok {
		return cc
	}
	return name
}

// toolNameFromWire maps a received tool name back to the actual tool
// registered for the turn when subscription auth is in use (case-insensitive
// match, like Pi's fromClaudeCodeName); other names pass through unchanged.
func toolNameFromWire(name string, oauth bool, tools []agent.Tool) string {
	if !oauth {
		return name
	}
	lower := strings.ToLower(name)
	for _, t := range tools {
		if strings.ToLower(t.Name()) == lower {
			return t.Name()
		}
	}
	return name
}

// rawMessage marshals v to a JSON literal; conversion inputs are driver
// types that cannot fail to encode.
func rawMessage(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// rawList renders a list of already-encoded values as a JSON array.
func rawList(items []json.RawMessage) json.RawMessage {
	return rawMessage(items)
}
