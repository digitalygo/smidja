package contextmanager

import (
	"github.com/digitalygo/smidja/internal/agent"
)

// PrunePlaceholder replaces the content of pruned tool results, so the
// model still sees the call but not the full output. Keeping the exact
// string stable lets the manager recognize already-pruned results and
// lets operators grep sessions for pruned context.
const PrunePlaceholder = "[Tool result pruned to reduce context. Re-run the tool if you need this output.]"

// pruneOutcome is the result of one prune pass.
type pruneOutcome struct {
	messages []*agent.Message
	ids      []agent.ToolCallID
}

// pruneMessages replaces the content of prunable tool results with the
// placeholder, operating on tool-call/result pairs: the assistant
// toolCall block stays intact and only the result content is replaced.
// A tool result is prunable when all of the following hold:
//
//   - it is not an error result (errors are never pruned),
//   - its ToolCallID is not pinned,
//   - its ToolCallID matches a toolCall block in the conversation (it is
//     the result half of a real pair),
//   - its content is not already the prune placeholder,
//   - it sits outside the protected recent window (the last
//     keepRecent messages are untouched).
//
// The input slice and messages are not mutated: pruned messages are
// replaced by copies in the returned list.
func pruneMessages(messages []*agent.Message, keepRecent int, pinned map[agent.ToolCallID]struct{}) pruneOutcome {
	out := make([]*agent.Message, len(messages))
	copy(out, messages)
	keepStart := len(messages) - keepRecent
	if keepStart < 0 {
		keepStart = 0
	}
	known := knownToolCallIDs(messages)
	var ids []agent.ToolCallID
	for i := 0; i < keepStart; i++ {
		msg := messages[i]
		if msg == nil || msg.ToolResult == nil {
			continue
		}
		tr := msg.ToolResult
		if tr.IsError {
			continue
		}
		if _, ok := pinned[agent.ToolCallID(tr.ToolCallID)]; ok {
			continue
		}
		if _, ok := known[tr.ToolCallID]; !ok {
			continue
		}
		if alreadyPruned(tr) {
			continue
		}
		repl := *tr
		repl.Content = []agent.ContentBlock{{Type: agent.BlockTypeText, Text: PrunePlaceholder}}
		out[i] = &agent.Message{ToolResult: &repl}
		ids = append(ids, agent.ToolCallID(tr.ToolCallID))
	}
	return pruneOutcome{messages: out, ids: ids}
}

// knownToolCallIDs returns every tool call id that appears in the
// assistant messages of the conversation.
func knownToolCallIDs(messages []*agent.Message) map[string]struct{} {
	known := make(map[string]struct{})
	for _, msg := range messages {
		if msg == nil || msg.Assistant == nil {
			continue
		}
		for _, b := range msg.Assistant.Content {
			if b.Type == agent.BlockTypeToolCall && b.ID != "" {
				known[b.ID] = struct{}{}
			}
		}
	}
	return known
}

// alreadyPruned reports whether a tool result already carries exactly
// the prune placeholder, so an already-pruned result is never
// re-reported or re-processed.
func alreadyPruned(tr *agent.ToolResultMessage) bool {
	return len(tr.Content) == 1 && tr.Content[0].Type == agent.BlockTypeText && tr.Content[0].Text == PrunePlaceholder
}
