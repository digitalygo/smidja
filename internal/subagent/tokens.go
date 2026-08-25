package subagent

import (
	"encoding/json"

	"github.com/digitalygo/smidja/internal/agent"
)

// estimateTokens mirrors the context manager's occupancy heuristic (ceil
// of the marshaled bytes over four) for one message, so the selector can
// reject over-budget kept sets before the caller falls back. The manager
// re-validates selections authoritatively; this local copy exists
// because the selector package cannot import the manager (the manager
// imports the selector).
func estimateTokens(m *agent.Message) int64 {
	b, err := json.Marshal([]*agent.Message{m})
	if err != nil {
		return roughBytes(m)
	}
	return ceilDiv(int64(len(b)), 4)
}

// roughBytes sums the length of the string-carrying fields of one
// message, as the marshal-failure fallback.
func roughBytes(m *agent.Message) int64 {
	if m == nil {
		return 4 // "null"
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

// ceilDiv returns ceil(a/b) for a >= 0 and b > 0.
func ceilDiv(a, b int64) int64 {
	return (a + b - 1) / b
}
