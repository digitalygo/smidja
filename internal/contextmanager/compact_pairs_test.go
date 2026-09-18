package contextmanager

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/subagent"
)

func assertOrderedPairIntegrity(t *testing.T, messages []*agent.Message) {
	t.Helper()
	calls := map[string]bool{}
	results := map[string]bool{}
	for index, message := range messages {
		if message == nil {
			continue
		}
		if message.Assistant != nil {
			for _, block := range message.Assistant.Content {
				if block.Type != agent.BlockTypeToolCall {
					continue
				}
				if calls[block.ID] {
					t.Fatalf("message %d repeats tool call %q", index, block.ID)
				}
				calls[block.ID] = true
			}
		}
		if message.ToolResult != nil {
			id := message.ToolResult.ToolCallID
			if !calls[id] {
				t.Fatalf("message %d holds tool result %q before its call", index, id)
			}
			if results[id] {
				t.Fatalf("message %d repeats tool result %q", index, id)
			}
			results[id] = true
		}
	}
	for id := range calls {
		if !results[id] {
			t.Fatalf("tool call %q survived without its result", id)
		}
	}
}

func compactIDs(count int) []string {
	ids := make([]string, count)
	for i := range ids {
		ids[i] = fmt.Sprintf("entry-%d", i)
	}
	return ids
}

func compactSelecting(t *testing.T, cfg Config, keep []string) (*Manager, *stubSelector) {
	t.Helper()
	stub := &stubSelector{fn: func(subagent.SelectionRequest) (subagent.Selection, error) {
		return subagent.Selection{KeptIDs: keep}, nil
	}}
	m, _ := newTestManager(t, cfg, stub)
	return m, stub
}

func TestCompactKeepsCallWhenResultIsRecent(t *testing.T) {
	msgs := []*agent.Message{
		userMsg("u0"),
		asstCall("callR", "read", `{}`),
		userMsg("u2"),
		userMsg("u3"),
		userMsg("u4"),
		userMsg("u5"),
		userMsg("u6"),
		userMsg("u7"),
		toolResult("callR", "read", "result", false),
	}
	ids := compactIDs(len(msgs))
	cfg := baseConfig()
	cfg.KeepRecentMessages = 3
	keep := []string{ids[0], ids[3], ids[4], ids[5]}
	m, _ := compactSelecting(t, cfg, keep)
	result, entry, err := m.compact(context.Background(), "", msgs, 0, map[agent.ToolCallID]struct{}{}, ids)
	if err != nil {
		t.Fatal(err)
	}
	if entry == nil {
		t.Fatal("expected a compaction entry")
	}
	assertOrderedPairIntegrity(t, result)
	if entry.FirstKeptEntryID != ids[0] {
		t.Fatalf("FirstKeptEntryID = %q, want %q", entry.FirstKeptEntryID, ids[0])
	}
	foundCall := false
	for _, message := range result {
		if message.Assistant != nil && message.Assistant.Content[0].ID == "callR" {
			foundCall = true
		}
	}
	if !foundCall {
		t.Fatal("the tool call candidate paired with a recent result must survive")
	}
}

func TestCompactKeepsSurvivingPairSide(t *testing.T) {
	msgs := []*agent.Message{
		userMsg("u0"),
		userMsg("u1"),
		asstCall("callK", "read", `{}`),
		toolResult("callK", "read", "result", false),
		userMsg("u4"),
		userMsg("u5"),
		userMsg("u6"),
		userMsg("u7"),
	}
	ids := compactIDs(len(msgs))
	cfg := baseConfig()
	cfg.KeepRecentMessages = 2
	cases := map[string][]string{
		"call survives":   {ids[2]},
		"result survives": {ids[3]},
	}
	for name, keep := range cases {
		t.Run(name, func(t *testing.T) {
			m, _ := compactSelecting(t, cfg, keep)
			result, entry, err := m.compact(context.Background(), "", msgs, 0, map[agent.ToolCallID]struct{}{}, ids)
			if err != nil {
				t.Fatal(err)
			}
			if entry == nil {
				t.Fatal("expected a compaction entry")
			}
			assertOrderedPairIntegrity(t, result)
			foundCall := false
			foundResult := false
			for _, message := range result {
				if message.Assistant != nil && message.Assistant.Content[0].ID == "callK" {
					foundCall = true
				}
				if message.ToolResult != nil && message.ToolResult.ToolCallID == "callK" {
					foundResult = true
				}
			}
			if !foundCall || !foundResult {
				t.Fatalf("pair sides must survive together: call=%v result=%v", foundCall, foundResult)
			}
			if entry.FirstKeptEntryID != ids[2] {
				t.Fatalf("FirstKeptEntryID = %q, want %q", entry.FirstKeptEntryID, ids[2])
			}
		})
	}
}

func TestCompactFallbackSplitKeepsPair(t *testing.T) {
	msgs := []*agent.Message{
		userMsg("u0"),
		asstCall("callF", "read", `{}`),
		toolResult("callF", "read", "result", false),
		userMsg("u3"),
		userMsg("u4"),
		userMsg("u5"),
		userMsg("u6"),
		userMsg("u7"),
		userMsg("u8"),
		userMsg("u9"),
	}
	ids := compactIDs(len(msgs))
	cfg := baseConfig()
	cfg.KeepRecentMessages = 3
	keepStart := len(msgs) - cfg.KeepRecentMessages
	var target int64
	for i := 2; i < keepStart; i++ {
		target += rawTokensOf(msgs[i])
	}
	cfg.ContextWindowTokens = 100_000
	cfg.CompactTarget = float64(target) / float64(cfg.ContextWindowTokens)
	m, _ := newTestManager(t, cfg, nil)
	result, entry, err := m.compact(context.Background(), "", msgs, 0, map[agent.ToolCallID]struct{}{}, ids)
	if err != nil {
		t.Fatal(err)
	}
	if entry == nil {
		t.Fatal("expected the fallback to compact")
	}
	assertOrderedPairIntegrity(t, result)
	foundCall := false
	for _, message := range result {
		if message.Assistant != nil && message.Assistant.Content[0].ID == "callF" {
			foundCall = true
		}
	}
	if !foundCall {
		t.Fatal("a fallback cut between a call and its result must retain the call")
	}
	if entry.FirstKeptEntryID != ids[1] {
		t.Fatalf("FirstKeptEntryID = %q, want %q", entry.FirstKeptEntryID, ids[1])
	}
}

func TestCompactAnchorCoversStraddlingPair(t *testing.T) {
	msgs := []*agent.Message{
		asstCall("callS", "read", `{}`),
		userMsg("u1"),
		userMsg("u2"),
		toolResult("callS", "read", "result", false),
		userMsg("u4"),
		userMsg("u5"),
		userMsg("u6"),
		userMsg("u7"),
		userMsg("u8"),
		userMsg("u9"),
	}
	ids := compactIDs(len(msgs))
	cfg := baseConfig()
	cfg.KeepRecentMessages = 3
	m, _ := compactSelecting(t, cfg, []string{ids[2]})
	result, entry, err := m.compact(context.Background(), "", msgs, 0, map[agent.ToolCallID]struct{}{}, ids)
	if err != nil {
		t.Fatal(err)
	}
	if entry == nil {
		t.Fatal("expected a compaction entry")
	}
	assertOrderedPairIntegrity(t, result)
	if entry.FirstKeptEntryID != ids[0] {
		t.Fatalf("FirstKeptEntryID = %q, want the straddling pair start %q", entry.FirstKeptEntryID, ids[0])
	}
}

func TestCompactKeepsPartnerOfPinnedCall(t *testing.T) {
	msgs := []*agent.Message{
		userMsg("u0"),
		{Assistant: &agent.AssistantMessage{
			Role: "assistant",
			Content: []agent.ContentBlock{
				{Type: agent.BlockTypeToolCall, ID: "pinnedCall", Name: "read", Arguments: json.RawMessage(`{}`)},
				{Type: agent.BlockTypeToolCall, ID: "freeCall", Name: "read", Arguments: json.RawMessage(`{}`)},
			},
			StopReason: "toolUse",
			Timestamp:  2,
		}},
		toolResult("pinnedCall", "read", "pinned result", false),
		toolResult("freeCall", "read", "free result", false),
		userMsg("u4"),
		userMsg("u5"),
		userMsg("u6"),
		userMsg("u7"),
	}
	ids := compactIDs(len(msgs))
	cfg := baseConfig()
	cfg.KeepRecentMessages = 2
	m, _ := compactSelecting(t, cfg, []string{ids[0]})
	m.PinToolCall("pinnedCall")
	result, entry, err := m.compact(context.Background(), "", msgs, 0, m.pinned, ids)
	if err != nil {
		t.Fatal(err)
	}
	if entry == nil {
		t.Fatal("expected a compaction entry")
	}
	assertOrderedPairIntegrity(t, result)
	foundFree := false
	for _, message := range result {
		if message.ToolResult != nil && message.ToolResult.ToolCallID == "freeCall" {
			foundFree = true
		}
	}
	if !foundFree {
		t.Fatal("a pinned assistant call must retain the matching result of its other call")
	}
}

func TestCompactPairHelpers(t *testing.T) {
	msgs := []*agent.Message{
		userMsg("u0"),
		asstCall("callH", "read", `{}`),
		toolResult("callH", "read", "result", false),
		userMsg("u3"),
	}
	partners := toolPairPartners(msgs)
	if len(partners[1]) != 1 || partners[1][0] != 2 {
		t.Fatalf("call partner = %v, want [2]", partners[1])
	}
	if len(partners[2]) != 1 || partners[2][0] != 1 {
		t.Fatalf("result partner = %v, want [1]", partners[2])
	}
	kept := make([]bool, len(msgs))
	kept[2] = true
	kept = closeToolPairs(kept, partners)
	if !kept[1] {
		t.Fatal("closing a kept result must keep its call")
	}
	if anchor := compactionAnchor(kept, partners); anchor != 1 {
		t.Fatalf("anchor = %d, want 1", anchor)
	}
	orphan := []*agent.Message{toolResult("ghost", "read", "result", false)}
	if len(toolPairPartners(orphan)) != 0 {
		t.Fatal("an orphan result must not create a pair")
	}
}
