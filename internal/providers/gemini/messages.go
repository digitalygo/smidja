package gemini

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/digitalygo/smidja/internal/agent"
)

// BuildContents converts the conversation into Gemini contents, mirroring
// pi-ai's google-shared convertMessages: user messages become user turns,
// assistant messages become model turns (thinking blocks only stay
// thought parts when the message came from the same provider and model,
// otherwise they degrade to plain text), and tool results become user
// turns with functionResponse parts that merge into a preceding
// functionResponse turn. It returns the contents and the optional
// systemInstruction content.
func BuildContents(system string, messages []*agent.Message, providerID, modelID string) ([]Content, *Content) {
	needsID := requiresToolCallID(modelID)
	contents := make([]Content, 0, len(messages))
	for _, m := range messages {
		switch {
		case m.User != nil:
			if c, ok := userContent(m.User); ok {
				contents = append(contents, c)
			}
		case m.Assistant != nil:
			if c, ok := assistantContent(m.Assistant, providerID, modelID, needsID); ok {
				contents = append(contents, c)
			}
		case m.ToolResult != nil:
			contents = appendFunctionResponse(contents, toolResultContent(m.ToolResult, needsID))
		}
	}
	var systemInstruction *Content
	if system != "" {
		text := system
		systemInstruction = &Content{Parts: []Part{{Text: &text}}}
	}
	return contents, systemInstruction
}

// userContent renders a user message as a user turn. String content
// becomes one text part; block arrays are flattened into text parts.
// A message with no renderable parts is dropped.
func userContent(u *agent.UserMessage) (Content, bool) {
	if isJSONString(u.Content) {
		var text string
		if err := json.Unmarshal(u.Content, &text); err == nil {
			return Content{Role: "user", Parts: []Part{{Text: &text}}}, true
		}
		return Content{}, false
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(u.Content, &blocks); err != nil || blocks == nil {
		return Content{}, false
	}
	parts := make([]Part, 0, len(blocks))
	for _, b := range blocks {
		if b.Type != agent.BlockTypeText {
			continue
		}
		text := b.Text
		parts = append(parts, Part{Text: &text})
	}
	if len(parts) == 0 {
		return Content{}, false
	}
	return Content{Role: "user", Parts: parts}, true
}

// assistantContent renders an assistant message as a model turn, mirroring
// pi-ai's convertMessages part rules:
//   - text blocks become text parts; empty text blocks are dropped (pi-ai
//     keeps signature-bearing empty blocks, but smidja has no text
//     signature field to persist);
//   - thinking blocks stay thought parts with their signature only for
//     same-provider/same-model messages; otherwise they degrade to plain
//     text so the model does not mimic reasoning tags;
//   - toolCall blocks become functionCall parts, carrying the id only for
//     models that require explicit tool call ids.
//
// A message with no renderable parts is dropped.
func assistantContent(a *agent.AssistantMessage, providerID, modelID string, needsID bool) (Content, bool) {
	isSame := a.Provider == providerID && a.Model == modelID
	parts := make([]Part, 0, len(a.Content))
	for _, b := range a.Content {
		switch b.Type {
		case agent.BlockTypeText:
			if strings.TrimSpace(b.Text) == "" {
				continue
			}
			text := b.Text
			parts = append(parts, Part{Text: &text})
		case agent.BlockTypeThinking:
			text := b.Thinking
			signature := resolveThoughtSignature(isSame, b.ThinkingSignature)
			if strings.TrimSpace(b.Thinking) == "" && signature == "" {
				continue
			}
			if isSame {
				parts = append(parts, Part{Thought: true, Text: &text, ThoughtSignature: signature})
			} else {
				// Cross-provider or cross-model: the signature is
				// unusable, so the thinking degrades to plain text.
				parts = append(parts, Part{Text: &text})
			}
		case agent.BlockTypeToolCall:
			args := b.Arguments
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			fc := &FunctionCall{Name: b.Name, Args: args}
			if needsID {
				fc.ID = b.ID
			}
			parts = append(parts, Part{FunctionCall: fc})
		}
	}
	if len(parts) == 0 {
		return Content{}, false
	}
	return Content{Role: "model", Parts: parts}, true
}

// toolResultContent renders a tool result as a user turn with one
// functionResponse part: {"output": ...} on success, {"error": ...} on
// failure. The value is the joined text of the content blocks.
func toolResultContent(t *agent.ToolResultMessage, needsID bool) Content {
	var text strings.Builder
	for _, b := range t.Content {
		if b.Type == agent.BlockTypeText {
			text.WriteString(b.Text)
		}
	}
	response := map[string]string{"output": text.String()}
	if t.IsError {
		response = map[string]string{"error": text.String()}
	}
	raw, _ := json.Marshal(response)
	fr := &FunctionResponse{Name: t.ToolName, Response: raw}
	if needsID {
		fr.ID = t.ToolCallID
	}
	return Content{Role: "user", Parts: []Part{{FunctionResponse: fr}}}
}

// appendFunctionResponse merges a functionResponse turn into the previous
// content when that is already a user turn carrying functionResponse
// parts, mirroring pi-ai's Cloud Code Assist merge rule.
func appendFunctionResponse(contents []Content, c Content) []Content {
	if len(contents) > 0 {
		last := &contents[len(contents)-1]
		if last.Role == "user" && hasFunctionResponse(*last) {
			last.Parts = append(last.Parts, c.Parts...)
			return contents
		}
	}
	return append(contents, c)
}

// hasFunctionResponse reports whether any part of the content is a
// functionResponse part.
func hasFunctionResponse(c Content) bool {
	for i := range c.Parts {
		if c.Parts[i].FunctionResponse != nil {
			return true
		}
	}
	return false
}

// BuildTools converts agent tools into Gemini function declarations
// exposed through parametersJsonSchema (the modern full JSON Schema
// field, matching pi-ai's default). It returns nil for an empty tool list
// so the field is omitted.
func BuildTools(tools []agent.Tool) []Tool {
	if len(tools) == 0 {
		return nil
	}
	decls := make([]FunctionDeclaration, 0, len(tools))
	for _, t := range tools {
		decls = append(decls, FunctionDeclaration{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Schema(),
		})
	}
	return []Tool{{FunctionDeclarations: decls}}
}

// geminiMajorVersionRE matches a leading gemini generation, for example
// "gemini-2.5-pro" or "gemini-live-3".
var geminiMajorVersionRE = regexp.MustCompile(`^gemini(?:-live)?-(\d+)`)

// requiresToolCallID mirrors pi-ai's google-shared helper: Gemini 3+ and
// the Claude/gpt-oss models served behind Google APIs require explicit
// ids on functionCall and functionResponse parts; older Gemini models
// ignore them.
func requiresToolCallID(modelID string) bool {
	id := strings.ToLower(modelID)
	if strings.HasPrefix(id, "claude-") || strings.HasPrefix(id, "gpt-oss-") {
		return true
	}
	m := geminiMajorVersionRE.FindStringSubmatch(id)
	if m == nil {
		return false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return false
	}
	return n >= 3
}

// base64SignaturePattern matches base64 with optional padding, the shape
// Google uses for TYPE_BYTES thought signatures.
var base64SignaturePattern = regexp.MustCompile(`^[A-Za-z0-9+/]+={0,2}$`)

// isValidThoughtSignature mirrors pi-ai's google-shared check: a
// signature must be non-empty, base64, and length-aligned to 4.
func isValidThoughtSignature(signature string) bool {
	if signature == "" || len(signature)%4 != 0 {
		return false
	}
	return base64SignaturePattern.MatchString(signature)
}

// resolveThoughtSignature only keeps signatures from the same provider
// and model that are valid base64, mirroring pi-ai's google-shared
// helper.
func resolveThoughtSignature(isSameProviderAndModel bool, signature string) string {
	if !isSameProviderAndModel || !isValidThoughtSignature(signature) {
		return ""
	}
	return signature
}

// retainThoughtSignature preserves the last non-empty signature seen for
// a block, because some backends only send thoughtSignature on the first
// delta of a part and omit it afterwards. It mirrors pi-ai's google-shared
// helper.
func retainThoughtSignature(existing, incoming string) string {
	if incoming != "" {
		return incoming
	}
	return existing
}

// mapStopReason mirrors pi-ai's mapStopReasonString for raw API
// responses: STOP completes, MAX_TOKENS truncates, and every safety,
// blocklist, or other finish reason is an error.
func mapStopReason(reason string) string {
	switch reason {
	case "STOP":
		return "stop"
	case "MAX_TOKENS":
		return "length"
	default:
		return "error"
	}
}

// isJSONString reports whether raw is a JSON string literal.
func isJSONString(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) > 0 && t[0] == '"'
}
