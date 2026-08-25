package contextmanager

import (
	"encoding/json"

	"github.com/digitalygo/smidja/internal/agent"
)

// FramingOverheadTokens is added to every full-context estimate to cover
// the API envelope: role markers, tool definitions, and provider framing
// that the byte-based message estimate does not see.
const FramingOverheadTokens = 256

// estimateTokens returns the estimated token count of the system prompt
// plus messages: ceil(JSON bytes / 4) plus the framing overhead. It is
// the full-context estimate the manager compares against the thresholds.
func estimateTokens(system string, messages []*agent.Message) int64 {
	return ceilDiv(messageBytes(messages)+int64(len(system)), 4) + FramingOverheadTokens
}

// rawTokens estimates a message list alone, without system prompt or
// framing overhead; the anchor-based delta estimate uses it because the
// anchor input count already includes system and framing.
func rawTokens(messages []*agent.Message) int64 {
	return ceilDiv(messageBytes(messages), 4)
}

// rawTokensOf estimates one message alone.
func rawTokensOf(m *agent.Message) int64 {
	return rawTokens([]*agent.Message{m})
}

// ceilDiv returns ceil(a/b) for a >= 0 and b > 0.
func ceilDiv(a, b int64) int64 {
	return (a + b - 1) / b
}

// messageBytes returns the JSON byte length of the messages in their
// persisted shape. Marshaling that shape is a deterministic byte proxy
// for the wire shape the provider sees; the /4 heuristic absorbs the
// difference. On a marshal error (an invalid RawMessage in a tool call)
// it falls back to summing the string fields so estimation never fails a
// turn.
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

// estimateOccupancy estimates the context size of one request: the
// full-context estimate, or the max of it and the anchor-based delta
// estimate when an anchor is available. The anchor is the real input
// token count of the most recent provider call; the delta adds the
// estimated tokens of the messages appended since the anchor boundary.
// The delta is deliberately conservative: it never undercounts what the
// provider measured last turn, so the manager acts slightly early rather
// than late.
func (m *Manager) estimateOccupancy(req agent.ContextRequest, anchored bool, anchorInput int64, anchorCount int) int64 {
	full := estimateTokens(req.System, req.Messages)
	boundary, input := -1, int64(0)
	switch {
	case anchored && anchorInput > 0:
		// Warm: the previous call sent anchorCount messages, and the
		// provider billed anchorInput for them plus the system prompt.
		boundary, input = anchorCount, anchorInput
	case req.LastUsageInput > 0:
		// Fresh: the caller anchors to the last assistant message's
		// usage, whose input covered everything before that message.
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

// lastAssistantBoundary returns the index of the last assistant message,
// or -1 when there is none.
func lastAssistantBoundary(messages []*agent.Message) int {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i] != nil && messages[i].Assistant != nil {
			return i
		}
	}
	return -1
}
