package gemini

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/digitalygo/smidja/internal/agent"
)

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

func hasFunctionResponse(c Content) bool {
	for i := range c.Parts {
		if c.Parts[i].FunctionResponse != nil {
			return true
		}
	}
	return false
}

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

var geminiMajorVersionRE = regexp.MustCompile(`^gemini(?:-live)?-(\d+)`)

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

var base64SignaturePattern = regexp.MustCompile(`^[A-Za-z0-9+/]+={0,2}$`)

func isValidThoughtSignature(signature string) bool {
	if signature == "" || len(signature)%4 != 0 {
		return false
	}
	return base64SignaturePattern.MatchString(signature)
}

func resolveThoughtSignature(isSameProviderAndModel bool, signature string) string {
	if !isSameProviderAndModel || !isValidThoughtSignature(signature) {
		return ""
	}
	return signature
}

func retainThoughtSignature(existing, incoming string) string {
	if incoming != "" {
		return incoming
	}
	return existing
}

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

func isJSONString(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) > 0 && t[0] == '"'
}
