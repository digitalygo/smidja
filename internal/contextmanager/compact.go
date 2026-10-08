package contextmanager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/subagent"
)

const (
	verbatimStrategy = "smidja-verbatim-v1"
	fallbackStrategy = "smidja-fallback-v1"
)

type verbatimSummary struct {
	Strategy string   `json:"strategy"`
	Kept     []string `json:"kept"`
}

type fallbackSummary struct {
	Strategy string   `json:"strategy"`
	Dropped  []string `json:"dropped"`
}

func (m *Manager) compact(ctx context.Context, system string, messages []*agent.Message, occ int64, pinned map[agent.ToolCallID]struct{}, entryIDs []string) ([]*agent.Message, *agent.CompactionEntry, error) {
	cfg := m.cfg
	keepStart := len(messages) - cfg.KeepRecentMessages
	if keepStart < 0 {
		keepStart = 0
	}

	refs := entryRefs(entryIDs, messages)

	var cands []*agent.Message
	var candRefs []string
	var candIndexes []int
	for i := 0; i < keepStart; i++ {
		if messages[i] == nil || pinnedMessage(messages[i], pinned) {
			continue
		}
		cands = append(cands, messages[i])
		candRefs = append(candRefs, refs[i])
		candIndexes = append(candIndexes, i)
	}
	if len(cands) == 0 {
		return messages, nil, nil
	}

	target := int64(math.Round(cfg.CompactTarget * float64(cfg.ContextWindowTokens)))

	keptSet := make(map[string]struct{})
	selectorOK := false
	if m.selector != nil {
		req := subagent.SelectionRequest{
			Model:        cfg.SelectorModel,
			Candidates:   make([]subagent.Candidate, 0, len(cands)),
			Chunks:       chunkCandidates(cands, candRefs, cfg.SelectorChunkTokens),
			BudgetTokens: target,
			WindowTokens: cfg.ContextWindowTokens,
		}
		for i := range cands {
			req.Candidates = append(req.Candidates, subagent.Candidate{Ref: candRefs[i], Message: cands[i]})
		}
		sel, err := m.selector.Select(ctx, req)
		switch {
		case err != nil:
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, nil, ctxErr
			}
		case validateSelection(sel, candRefs, cands, target) == nil:
			for _, id := range sel.KeptIDs {
				keptSet[id] = struct{}{}
			}
			selectorOK = true
		}
	}

	var strategy string
	var keptOrder []string
	if selectorOK {
		strategy = verbatimStrategy
		for _, r := range candRefs {
			if _, ok := keptSet[r]; ok {
				keptOrder = append(keptOrder, r)
			}
		}
	} else {
		strategy = fallbackStrategy
		var used int64
		for i := len(cands) - 1; i >= 0; i-- {
			t := rawTokensOf(cands[i])
			if used+t > target && len(keptOrder) > 0 {
				break
			}
			keptOrder = append(keptOrder, candRefs[i])
			used += t
		}
		for l, r := 0, len(keptOrder)-1; l < r; l, r = l+1, r-1 {
			keptOrder[l], keptOrder[r] = keptOrder[r], keptOrder[l]
		}
		for _, r := range keptOrder {
			keptSet[r] = struct{}{}
		}
	}

	partners := toolPairPartners(messages)
	keptIdx := make([]bool, len(messages))
	for i := range messages {
		if i >= keepStart || messages[i] == nil || pinnedMessage(messages[i], pinned) {
			keptIdx[i] = true
		}
	}
	for j, ref := range candRefs {
		if _, ok := keptSet[ref]; ok {
			keptIdx[candIndexes[j]] = true
		}
	}
	keptIdx = closeToolPairs(keptIdx, partners)
	for j, index := range candIndexes {
		if keptIdx[index] {
			keptSet[candRefs[j]] = struct{}{}
		}
	}

	var dropped []string
	for _, r := range candRefs {
		if _, ok := keptSet[r]; !ok {
			dropped = append(dropped, r)
		}
	}
	if len(dropped) == 0 {
		return messages, nil, nil
	}

	anchor := compactionAnchor(keptIdx, partners)
	if anchor < 0 {
		return messages, nil, nil
	}

	keptMsgs := make([]*agent.Message, 0, len(messages)-len(dropped))
	for i := 0; i < keepStart; i++ {
		msg := messages[i]
		if msg == nil || pinnedMessage(msg, pinned) {
			keptMsgs = append(keptMsgs, msg)
			continue
		}
		if _, ok := keptSet[refs[i]]; !ok {
			continue
		}
		keptMsgs = append(keptMsgs, msg)
	}
	keptMsgs = append(keptMsgs, messages[keepStart:]...)
	firstKept := refs[anchor]

	var summary json.RawMessage
	var err error
	if selectorOK {
		summary, err = marshalSummary(verbatimSummary{Strategy: strategy, Kept: keptOrder})
	} else {
		summary, err = marshalSummary(fallbackSummary{Strategy: strategy, Dropped: dropped})
	}
	if err != nil {
		return nil, nil, err
	}

	entry := &agent.CompactionEntry{
		Summary:          summary,
		FirstKeptEntryID: firstKept,
		TokensBefore:     occ,
	}
	return keptMsgs, entry, nil
}

func entryRefs(entryIDs []string, messages []*agent.Message) []string {
	if len(entryIDs) == len(messages) {
		out := make([]string, len(entryIDs))
		copy(out, entryIDs)
		return out
	}
	out := make([]string, len(messages))
	for i, msg := range messages {
		if msg == nil {
			out[i] = fmt.Sprintf("null#%d", i)
			continue
		}
		out[i] = fmt.Sprintf("%s:%d#%d", msg.Role(), msgTimestamp(msg), i)
	}
	return out
}

func msgTimestamp(m *agent.Message) int64 {
	switch {
	case m.User != nil:
		return m.User.Timestamp
	case m.Assistant != nil:
		return m.Assistant.Timestamp
	case m.ToolResult != nil:
		return m.ToolResult.Timestamp
	}
	return 0
}

func pinnedMessage(msg *agent.Message, pinned map[agent.ToolCallID]struct{}) bool {
	if msg == nil {
		return false
	}
	if msg.ToolResult != nil {
		_, ok := pinned[agent.ToolCallID(msg.ToolResult.ToolCallID)]
		return ok
	}
	if msg.Assistant != nil {
		for _, b := range msg.Assistant.Content {
			if b.Type == agent.BlockTypeToolCall {
				if _, ok := pinned[agent.ToolCallID(b.ID)]; ok {
					return true
				}
			}
		}
	}
	return false
}

func toolPairPartners(messages []*agent.Message) map[int][]int {
	callIndex := make(map[string]int)
	resultIndex := make(map[string]int)
	for i, message := range messages {
		if message == nil {
			continue
		}
		if message.Assistant != nil {
			for _, block := range message.Assistant.Content {
				if block.Type != agent.BlockTypeToolCall || block.ID == "" {
					continue
				}
				if _, exists := callIndex[block.ID]; !exists {
					callIndex[block.ID] = i
				}
			}
		}
		if message.ToolResult != nil && message.ToolResult.ToolCallID != "" {
			if _, exists := resultIndex[message.ToolResult.ToolCallID]; !exists {
				resultIndex[message.ToolResult.ToolCallID] = i
			}
		}
	}
	partners := make(map[int][]int)
	for id, call := range callIndex {
		result, ok := resultIndex[id]
		if !ok || result == call {
			continue
		}
		partners[call] = append(partners[call], result)
		partners[result] = append(partners[result], call)
	}
	return partners
}

func closeToolPairs(kept []bool, partners map[int][]int) []bool {
	queue := make([]int, 0, len(kept))
	for index, ok := range kept {
		if ok {
			queue = append(queue, index)
		}
	}
	for len(queue) > 0 {
		index := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		for _, partner := range partners[index] {
			if !kept[partner] {
				kept[partner] = true
				queue = append(queue, partner)
			}
		}
	}
	return kept
}

func compactionAnchor(kept []bool, partners map[int][]int) int {
	anchor := -1
	for index, ok := range kept {
		if ok {
			anchor = index
			break
		}
	}
	if anchor < 0 {
		return -1
	}
	for {
		reduced := false
		for index, list := range partners {
			for _, partner := range list {
				low, high := index, partner
				if low > high {
					low, high = high, low
				}
				if low < anchor && anchor <= high {
					anchor = low
					reduced = true
				}
			}
		}
		if !reduced {
			break
		}
	}
	return anchor
}

func chunkCandidates(cands []*agent.Message, candRefs []string, chunkTokens int64) [][]subagent.Candidate {
	var chunks [][]subagent.Candidate
	var cur []subagent.Candidate
	var used int64
	for i, c := range cands {
		t := rawTokensOf(c)
		if len(cur) > 0 && used+t > chunkTokens {
			chunks = append(chunks, cur)
			cur = nil
			used = 0
		}
		cur = append(cur, subagent.Candidate{Ref: candRefs[i], Message: c})
		used += t
	}
	if len(cur) > 0 {
		chunks = append(chunks, cur)
	}
	return chunks
}

func validateSelection(sel subagent.Selection, candRefs []string, cands []*agent.Message, budget int64) error {
	if len(sel.KeptIDs) == 0 {
		return errors.New("contextmanager: selector kept nothing")
	}
	known := make(map[string]struct{}, len(candRefs))
	cost := make(map[string]int64, len(cands))
	for i, r := range candRefs {
		known[r] = struct{}{}
		cost[r] = rawTokensOf(cands[i])
	}
	seen := make(map[string]struct{}, len(sel.KeptIDs))
	var used int64
	for _, id := range sel.KeptIDs {
		if _, ok := known[id]; !ok {
			return fmt.Errorf("contextmanager: selector returned unknown ref %q", id)
		}
		if _, dup := seen[id]; dup {
			return fmt.Errorf("contextmanager: selector returned duplicate ref %q", id)
		}
		seen[id] = struct{}{}
		used += cost[id]
	}
	if used > budget {
		return fmt.Errorf("contextmanager: selector kept set over budget: %d > %d tokens", used, budget)
	}
	return nil
}

func marshalSummary(v any) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("contextmanager: marshal compaction summary: %w", err)
	}
	return json.RawMessage(b), nil
}
