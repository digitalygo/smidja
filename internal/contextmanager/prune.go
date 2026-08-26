package contextmanager

import (
	"github.com/digitalygo/smidja/internal/agent"
)

const PrunePlaceholder = "[Tool result pruned to reduce context. Re-run the tool if you need this output.]"

type pruneOutcome struct {
	messages []*agent.Message
	ids      []agent.ToolCallID
}

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

func alreadyPruned(tr *agent.ToolResultMessage) bool {
	return len(tr.Content) == 1 && tr.Content[0].Type == agent.BlockTypeText && tr.Content[0].Text == PrunePlaceholder
}
