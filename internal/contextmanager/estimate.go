package contextmanager

import (
	"encoding/json"

	"github.com/digitalygo/smidja/internal/agent"
)

const FramingOverheadTokens = 256

func estimateTokens(system string, messages []*agent.Message) int64 {
	return ceilDiv(messageBytes(messages)+int64(len(system)), 4) + FramingOverheadTokens
}

func rawTokens(messages []*agent.Message) int64 {
	return ceilDiv(messageBytes(messages), 4)
}

func rawTokensOf(m *agent.Message) int64 {
	return rawTokens([]*agent.Message{m})
}

func ceilDiv(a, b int64) int64 {
	return (a + b - 1) / b
}

func messageBytes(messages []*agent.Message) int64 {
	b, err := json.Marshal(messages)
	if err == nil {
		return int64(len(b))
	}
	var n int64
	for _, m := range messages {
		n += roughBytes(m)
	}
	return n
}

func roughBytes(m *agent.Message) int64 {
	if m == nil {
		return 4
	}
	var n int64
	switch {
	case m.User != nil:
		n += int64(len(m.User.Role) + len(m.User.Content))
	case m.Assistant != nil:
		n += int64(len(m.Assistant.Role) + len(m.Assistant.Model) + len(m.Assistant.ErrorMessage))
		for _, b := range m.Assistant.Content {
			n += int64(len(b.Type) + len(b.Text) + len(b.Thinking) + len(b.ID) + len(b.Name) + len(b.Arguments))
		}
	case m.ToolResult != nil:
		n += int64(len(m.ToolResult.Role) + len(m.ToolResult.ToolCallID) + len(m.ToolResult.ToolName))
		for _, b := range m.ToolResult.Content {
			n += int64(len(b.Type) + len(b.Text))
		}
	}
	return n
}

func (m *Manager) estimateOccupancy(req agent.ContextRequest, anchored bool, anchorInput int64, anchorCount int) int64 {
	full := estimateTokens(req.System, req.Messages)
	boundary, input := -1, int64(0)
	switch {
	case anchored && anchorInput > 0:
		boundary, input = anchorCount, anchorInput
	case req.LastUsageInput > 0:
		boundary, input = lastAssistantBoundary(req.Messages), req.LastUsageInput
	}
	if boundary < 0 {
		return full
	}
	if boundary > len(req.Messages) {
		boundary = len(req.Messages)
	}
	delta := input + rawTokens(req.Messages[boundary:])
	if delta > full {
		return delta
	}
	return full
}

func lastAssistantBoundary(messages []*agent.Message) int {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i] != nil && messages[i].Assistant != nil {
			return i
		}
	}
	return -1
}
