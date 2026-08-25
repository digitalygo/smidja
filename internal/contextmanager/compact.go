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

// Compaction transcript strategy tags. The strategy tag discriminates
// the two deterministic transcript shapes and versions the format.
const (
	verbatimStrategy = "smidja-verbatim-v1"
	fallbackStrategy = "smidja-fallback-v1"
)

// verbatimSummary is the deterministic transcript of a selector-driven
// compaction: which candidate messages were kept verbatim.
type verbatimSummary struct {
	Strategy string   `json:"strategy"`
	Kept     []string `json:"kept"`
}

// fallbackSummary is the deterministic transcript of a fallback
// compaction: which oldest messages were dropped.
type fallbackSummary struct {
	Strategy string   `json:"strategy"`
	Dropped  []string `json:"dropped"`
}

// compact removes the oldest messages outside the protected recent
// window and pin set. The remaining older messages are the candidates;
// the selector chooses which of them to keep, and the kept messages are
// restored chronologically and verbatim (same pointers). On any selector
// failure, or when no selector is injected, it falls back to the newest
// complete message window fitting the compact target and records the
// dropped oldest messages in the transcript.
//
// The compaction entry is returned for the caller to persist; it is
// never inserted into the message list. The entry is nil when there is
// nothing to compact.
func (m *Manager) compact(ctx context.Context, system string, messages []*agent.Message, occ int64, pinned map[agent.ToolCallID]struct{}, entryIDs []string) ([]*agent.Message, *agent.CompactionEntry, error) {
	cfg := m.cfg
	keepStart := len(messages) - cfg.KeepRecentMessages
	if keepStart < 0 {
		keepStart = 0
	}

	refs := entryRefs(entryIDs, messages)

	var cands []*agent.Message
	var candRefs []string
	for i := 0; i < keepStart; i++ {
		if messages[i] == nil || pinnedMessage(messages[i], pinned) {
			continue
		}
		cands = append(cands, messages[i])
		candRefs = append(candRefs, refs[i])
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
			// Selector failure: fall back, unless the turn is dying.
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
	var keptOrder []string // kept candidate refs, chronological
	if selectorOK {
		strategy = verbatimStrategy
		for _, r := range candRefs {
			if _, ok := keptSet[r]; ok {
				keptOrder = append(keptOrder, r)
			}
		}
	} else {
		strategy = fallbackStrategy
		// Newest complete messages fitting the target; the newest
		// message survives even when it alone exceeds the target
		// (messages are never split).
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

	var dropped []string
	for _, r := range candRefs {
		if _, ok := keptSet[r]; !ok {
			dropped = append(dropped, r)
		}
	}
	if len(dropped) == 0 {
		// Nothing was dropped: the compaction is a no-op.
		return messages, nil, nil
	}

	keptMsgs := make([]*agent.Message, 0, len(messages)-len(dropped))
	firstKept := ""
	for i := 0; i < keepStart; i++ {
		msg := messages[i]
		if msg == nil || pinnedMessage(msg, pinned) {
			keptMsgs = append(keptMsgs, msg)
			if firstKept == "" {
				firstKept = refs[i]
			}
			continue
		}
		if _, ok := keptSet[refs[i]]; !ok {
			continue
		}
		keptMsgs = append(keptMsgs, msg)
		if firstKept == "" {
			firstKept = refs[i]
		}
	}
	keptMsgs = append(keptMsgs, messages[keepStart:]...)
	if firstKept == "" && len(keptMsgs) > 0 && keepStart < len(messages) {
		// Nothing older was kept: the first survivor is the recent
		// window.
		firstKept = refs[keepStart]
	}

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

// entryRefs returns the entry ref of every message: the caller-provided
// session entry ids when aligned with the message list, otherwise a
// deterministic request-local ref of the form "<role>:<timestamp>#<index>".
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

// msgTimestamp returns the message's persisted timestamp.
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

// pinnedMessage reports whether a message carries a pinned tool call:
// either it is the result message of a pinned call, or it is the
// assistant message containing a pinned toolCall block. Both halves of a
// pinned pair are protected from compaction.
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

// chunkCandidates greedily packs consecutive candidates into chunks whose
// raw token estimate stays below chunkTokens. A single oversized message
// gets its own chunk; messages are never split.
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

// validateSelection checks the selector's answer against the validity
// contract: every kept ref must be a known candidate ref, refs must be
// unique and non-empty, and the estimated token cost of the kept set
// must stay within the budget. Any violation makes the selection invalid
// and the caller falls back to the deterministic window.
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

// marshalSummary marshals a transcript struct deterministically (struct
// field order) into the raw JSON payload the caller persists.
func marshalSummary(v any) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("contextmanager: marshal compaction summary: %w", err)
	}
	return json.RawMessage(b), nil
}
