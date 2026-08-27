package contextmanager

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/subagent"
)

func userMsg(text string) *agent.Message {
	content, _ := json.Marshal(text)
	return &agent.Message{User: &agent.UserMessage{Role: string(agent.RoleUser), Content: content, Timestamp: 1}}
}

func asstText(text string) *agent.Message {
	return &agent.Message{Assistant: &agent.AssistantMessage{
		Role:       string(agent.RoleAssistant),
		Content:    []agent.ContentBlock{{Type: agent.BlockTypeText, Text: text}},
		StopReason: "stop",
		Timestamp:  2,
	}}
}

func asstCall(id, name, args string) *agent.Message {
	return &agent.Message{Assistant: &agent.AssistantMessage{
		Role:       string(agent.RoleAssistant),
		Content:    []agent.ContentBlock{{Type: agent.BlockTypeToolCall, ID: id, Name: name, Arguments: json.RawMessage(args)}},
		StopReason: "toolUse",
		Timestamp:  3,
	}}
}

func toolResult(id, name, out string, isErr bool) *agent.Message {
	return &agent.Message{ToolResult: &agent.ToolResultMessage{
		Role:       string(agent.RoleToolResult),
		ToolCallID: id,
		ToolName:   name,
		Content:    []agent.ContentBlock{{Type: agent.BlockTypeText, Text: out}},
		IsError:    isErr,
		Timestamp:  4,
	}}
}

func baseConfig() Config {
	return Config{
		Enabled:                true,
		ContextWindowTokens:    100_000,
		CacheMissAfter:         5 * time.Minute,
		PruneThreshold:         0.70,
		CompactThreshold:       0.85,
		SafetyCompactThreshold: 0.95,
		CompactTarget:          0.50,
		SelectorChunkTokens:    12_000,
	}
}

func ceilFrac(w int64, t float64) int64 {
	return int64(math.Ceil(t * float64(w)))
}

func windowSearch(pred func(w int64) bool) int64 {
	for w := int64(1); w <= 100_000_000; w++ {
		if pred(w) {
			return w
		}
	}
	return -1
}

func newTestManager(t *testing.T, cfg Config, sel subagent.Selector) (*Manager, *time.Time) {
	t.Helper()
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	m, err := newWithClock(cfg, sel, func() time.Time { return now })
	if err != nil {
		t.Fatalf("newWithClock: %v", err)
	}
	return m, &now
}

func prepare(t *testing.T, m *Manager, req agent.ContextRequest) agent.ContextResult {
	t.Helper()
	res, err := m.Prepare(context.Background(), req)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return res
}

type stubSelector struct {
	fn    func(subagent.SelectionRequest) (subagent.Selection, error)
	reqs  []subagent.SelectionRequest
	calls int
}

func (s *stubSelector) Select(_ context.Context, req subagent.SelectionRequest) (subagent.Selection, error) {
	s.calls++
	s.reqs = append(s.reqs, req)
	if s.fn == nil {
		return subagent.Selection{}, errors.New("stub: no selector function")
	}
	return s.fn(req)
}

func TestConfigValidate(t *testing.T) {
	valid := baseConfig()
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"zero window", func(c *Config) { c.ContextWindowTokens = 0 }},
		{"negative window", func(c *Config) { c.ContextWindowTokens = -5 }},
		{"zero cache miss", func(c *Config) { c.CacheMissAfter = 0 }},
		{"zero prune", func(c *Config) { c.PruneThreshold = 0 }},
		{"prune at one", func(c *Config) { c.PruneThreshold = 1 }},
		{"prune above compact", func(c *Config) { c.PruneThreshold, c.CompactThreshold = 0.9, 0.85 }},
		{"compact above safety", func(c *Config) { c.CompactThreshold, c.SafetyCompactThreshold = 0.98, 0.95 }},
		{"safety above one", func(c *Config) { c.SafetyCompactThreshold = 1.01 }},
		{"target at prune", func(c *Config) { c.CompactTarget = 0.70 }},
		{"target above prune", func(c *Config) { c.CompactTarget = 0.75 }},
		{"zero target", func(c *Config) { c.CompactTarget = 0 }},
		{"zero chunk tokens", func(c *Config) { c.SelectorChunkTokens = 0 }},
		{"negative keep recent", func(c *Config) { c.KeepRecentMessages = -1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := valid
			tc.mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatalf("Validate() = nil, want error")
			}
		})
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestNewAppliesDefaults(t *testing.T) {
	m, err := New(Config{Enabled: true, ContextWindowTokens: 100_000}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if m.cfg.CacheMissAfter != 5*time.Minute {
		t.Errorf("CacheMissAfter = %s, want 5m", m.cfg.CacheMissAfter)
	}
	if m.cfg.PruneThreshold != 0.70 || m.cfg.CompactThreshold != 0.85 || m.cfg.SafetyCompactThreshold != 0.95 {
		t.Errorf("thresholds = %v/%v/%v, want 0.70/0.85/0.95", m.cfg.PruneThreshold, m.cfg.CompactThreshold, m.cfg.SafetyCompactThreshold)
	}
	if m.cfg.CompactTarget != 0.50 {
		t.Errorf("CompactTarget = %v, want 0.50", m.cfg.CompactTarget)
	}
	if m.cfg.KeepRecentMessages != 6 {
		t.Errorf("KeepRecentMessages = %d, want 6", m.cfg.KeepRecentMessages)
	}
	if m.cfg.SelectorChunkTokens != 12_000 {
		t.Errorf("SelectorChunkTokens = %d, want 12000", m.cfg.SelectorChunkTokens)
	}
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	if _, err := New(Config{Enabled: true}, nil); err == nil {
		t.Fatalf("New with zero window: nil error, want error")
	}
}

func TestPrepareDisabledPassesThrough(t *testing.T) {
	cfg := baseConfig()
	cfg.Enabled = false
	m, _ := newTestManager(t, cfg, nil)
	msgs := []*agent.Message{userMsg("hello"), asstText("hi")}
	res := prepare(t, m, agent.ContextRequest{Messages: msgs, System: "sys"})
	if len(res.Messages) != 2 || res.Messages[0] != msgs[0] {
		t.Fatalf("disabled manager must pass messages through untouched")
	}
	if res.Compacted || len(res.Pruned) != 0 || res.Compaction != nil {
		t.Fatalf("disabled manager must not act: %+v", res)
	}
}

func TestEstimateAnchorVsFull(t *testing.T) {
	cfg := baseConfig()
	m, _ := newTestManager(t, cfg, nil)
	msgs := []*agent.Message{
		userMsg("task: audit the repo"),
		asstText("I will look."),
		userMsg(strings.Repeat("data ", 200)),
		asstText("Found issues."),
		toolResult("c1", "exec", strings.Repeat("output ", 200), false),
	}
	req := agent.ContextRequest{Messages: msgs}
	full := estimateTokens("", msgs)
	boundary := lastAssistantBoundary(msgs)

	t.Run("anchor dominates", func(t *testing.T) {
		req.LastUsageInput = 50_000
		got := m.estimateOccupancy(req, false, 0, 0)
		want := 50_000 + rawTokens(msgs[boundary:])
		if got != want {
			t.Errorf("occupancy = %d, want anchor delta %d", got, want)
		}
		if got <= full {
			t.Errorf("anchor estimate %d must exceed full estimate %d", got, full)
		}
	})

	t.Run("full dominates", func(t *testing.T) {
		req.LastUsageInput = 1
		if got := m.estimateOccupancy(req, false, 0, 0); got != full {
			t.Errorf("occupancy = %d, want full estimate %d", got, full)
		}
	})

	t.Run("no anchor uses full", func(t *testing.T) {
		req.LastUsageInput = 0
		if got := m.estimateOccupancy(req, false, 0, 0); got != full {
			t.Errorf("occupancy = %d, want full estimate %d", got, full)
		}
	})

	t.Run("warm anchor", func(t *testing.T) {
		got := m.estimateOccupancy(req, true, 5000, 3)
		want := max(5000+rawTokens(msgs[3:]), full)
		if got != want {
			t.Errorf("occupancy = %d, want %d", got, want)
		}
	})

	t.Run("anchor boundary clamped", func(t *testing.T) {
		got := m.estimateOccupancy(req, true, 7000, 99)
		want := max(7000+rawTokens(msgs[len(msgs):]), full)
		if got != want {
			t.Errorf("occupancy = %d, want %d", got, want)
		}
	})

	t.Run("no assistant boundary", func(t *testing.T) {
		noAsst := []*agent.Message{userMsg("a"), userMsg("b")}
		r := agent.ContextRequest{Messages: noAsst, LastUsageInput: 9000}
		if got := m.estimateOccupancy(r, false, 0, 0); got != estimateTokens("", noAsst) {
			t.Errorf("occupancy = %d, want full %d", got, estimateTokens("", noAsst))
		}
	})
}

func pruneFixture() []*agent.Message {
	return []*agent.Message{
		userMsg("task: inventory the repo"),
		asstCall("call1", "read", `{"path":"go.mod"}`),
		toolResult("call1", "read", strings.Repeat("package smidja\n", 120), false),
		asstCall("call2", "exec", `{"cmd":"go build ./..."}`),
		toolResult("call2", "exec", strings.Repeat("build line\n", 120), false),
		userMsg("how does it look?"),
		asstText("compiled cleanly"),
	}
}

func expectedPruned(msgs []*agent.Message) []*agent.Message {
	out := make([]*agent.Message, len(msgs))
	copy(out, msgs)
	for i, m := range msgs {
		if m.ToolResult != nil && !m.ToolResult.IsError {
			repl := *m.ToolResult
			repl.Content = []agent.ContentBlock{{Type: agent.BlockTypeText, Text: PrunePlaceholder}}
			out[i] = &agent.Message{ToolResult: &repl}
		}
	}
	return out
}

func TestPruneThresholdBoundary(t *testing.T) {
	msgs := pruneFixture()
	occ := estimateTokens("", msgs)
	post := estimateTokens("", expectedPruned(msgs))
	pruneOnly := func(w int64) bool {
		return occ >= ceilFrac(w, 0.70) && post < ceilFrac(w, 0.85) && occ < ceilFrac(w, 0.95)
	}

	cfg := baseConfig()
	cfg.KeepRecentMessages = 2
	cfg.ContextWindowTokens = windowSearch(pruneOnly)
	m, _ := newTestManager(t, cfg, nil)
	res := prepare(t, m, agent.ContextRequest{Messages: msgs})
	if len(res.Pruned) != 2 {
		t.Fatalf("prune at threshold: Pruned = %v, want 2 ids", res.Pruned)
	}

	cfg2 := baseConfig()
	cfg2.KeepRecentMessages = 2
	cfg2.ContextWindowTokens = windowSearch(func(w int64) bool { return occ < ceilFrac(w, 0.70) })
	m2, _ := newTestManager(t, cfg2, nil)
	res2 := prepare(t, m2, agent.ContextRequest{Messages: msgs})
	if len(res2.Pruned) != 0 {
		t.Fatalf("below prune threshold: Pruned = %v, want none", res2.Pruned)
	}
}

func TestPruneCacheStaleGate(t *testing.T) {
	msgs := pruneFixture()
	occ := estimateTokens("", msgs)
	post := estimateTokens("", expectedPruned(msgs))
	cfg := baseConfig()
	cfg.KeepRecentMessages = 2
	cfg.ContextWindowTokens = windowSearch(func(w int64) bool {
		return occ >= ceilFrac(w, 0.70) && post < ceilFrac(w, 0.85) && occ < ceilFrac(w, 0.95)
	})
	if cfg.ContextWindowTokens < 0 {
		t.Fatalf("no prune-gate window (occ=%d post=%d)", occ, post)
	}

	m, now := newTestManager(t, cfg, nil)
	m.ObserveResponse(&agent.AssistantMessage{Usage: agent.Usage{}})
	res := prepare(t, m, agent.ContextRequest{Messages: msgs})
	if len(res.Pruned) != 0 || res.Compacted {
		t.Fatalf("fresh cache: expected no action, got Pruned=%v Compacted=%v", res.Pruned, res.Compacted)
	}

	*now = now.Add(cfg.CacheMissAfter + time.Second)
	res = prepare(t, m, agent.ContextRequest{Messages: msgs})
	if len(res.Pruned) != 2 {
		t.Fatalf("stale cache: Pruned = %v, want 2 ids", res.Pruned)
	}
	if res.Compacted {
		t.Fatalf("stale cache: unexpected compaction after prune")
	}
}

func TestPrunePairIntegrityAndRecentWindow(t *testing.T) {
	msgs := pruneFixture()
	occ := estimateTokens("", msgs)
	post := estimateTokens("", expectedPruned(msgs))
	cfg := baseConfig()
	cfg.KeepRecentMessages = 2
	cfg.ContextWindowTokens = windowSearch(func(w int64) bool {
		return occ >= ceilFrac(w, 0.70) && post < ceilFrac(w, 0.85) && occ < ceilFrac(w, 0.95)
	})
	m, _ := newTestManager(t, cfg, nil)

	res := prepare(t, m, agent.ContextRequest{Messages: msgs})
	if len(res.Pruned) != 2 || res.Pruned[0] != "call1" || res.Pruned[1] != "call2" {
		t.Fatalf("Pruned = %v, want [call1 call2]", res.Pruned)
	}
	for _, idx := range []int{1, 3} {
		got := res.Messages[idx].Assistant.Content[0]
		if got.Type != agent.BlockTypeToolCall || got.ID == "" {
			t.Fatalf("assistant call block at %d modified: %+v", idx, got)
		}
	}
	for _, idx := range []int{2, 4} {
		got := res.Messages[idx].ToolResult.Content
		if len(got) != 1 || got[0].Text != PrunePlaceholder {
			t.Fatalf("result at %d not pruned: %+v", idx, got)
		}
	}
	for _, idx := range []int{5, 6} {
		if res.Messages[idx] != msgs[idx] {
			t.Fatalf("recent message %d must pass through by pointer", idx)
		}
	}
	if msgs[2].ToolResult.Content[0].Text == PrunePlaceholder {
		t.Fatalf("input message mutated by prune")
	}
}

func TestPruneSkipsPinnedErrorOrphan(t *testing.T) {
	msgs := []*agent.Message{
		asstCall("pin1", "read", `{}`),
		toolResult("pin1", "read", strings.Repeat("P", 2000), false),
		asstCall("err1", "exec", `{}`),
		toolResult("err1", "exec", "boom", true),
		toolResult("orphan1", "read", strings.Repeat("O", 2000), false),
		asstCall("done1", "read", `{}`),
		toolResult("done1", "read", strings.Repeat("D", 2000), false),
		asstCall("old1", "read", `{}`),
		toolResult("old1", "read", strings.Repeat("X", 2000), false),
		userMsg("final"),
		asstText("done"),
	}
	postMsgs := make([]*agent.Message, len(msgs))
	copy(postMsgs, msgs)
	for _, idx := range []int{6, 8} {
		repl := *msgs[idx].ToolResult
		repl.Content = []agent.ContentBlock{{Type: agent.BlockTypeText, Text: PrunePlaceholder}}
		postMsgs[idx] = &agent.Message{ToolResult: &repl}
	}
	occ := estimateTokens("", msgs)
	post := estimateTokens("", postMsgs)
	cfg := baseConfig()
	cfg.KeepRecentMessages = 2
	cfg.ContextWindowTokens = windowSearch(func(w int64) bool {
		return occ >= ceilFrac(w, 0.70) && post < ceilFrac(w, 0.85) && occ < ceilFrac(w, 0.95)
	})
	if cfg.ContextWindowTokens < 0 {
		t.Fatalf("no prune window (occ=%d post=%d)", occ, post)
	}
	m, _ := newTestManager(t, cfg, nil)
	m.PinToolCall("pin1")

	res := prepare(t, m, agent.ContextRequest{Messages: msgs})
	if len(res.Pruned) != 2 || res.Pruned[0] != "done1" || res.Pruned[1] != "old1" {
		t.Fatalf("Pruned = %v, want [done1 old1]", res.Pruned)
	}
	if res.Compacted {
		t.Fatalf("unexpected compaction")
	}
	for _, idx := range []int{1, 3, 4} {
		if res.Messages[idx].ToolResult.Content[0].Text == PrunePlaceholder {
			t.Fatalf("message %d (%s) must not be pruned", idx, res.Messages[idx].ToolResult.ToolCallID)
		}
	}
}

func TestPruneSkipsAlreadyPruned(t *testing.T) {
	msgs := []*agent.Message{
		asstCall("c1", "read", `{}`),
		toolResult("c1", "read", PrunePlaceholder, false),
		asstCall("c2", "read", `{}`),
		toolResult("c2", "read", strings.Repeat("X", 2000), false),
		userMsg("final"),
		asstText("done"),
	}
	postMsgs := make([]*agent.Message, len(msgs))
	copy(postMsgs, msgs)
	repl := *msgs[3].ToolResult
	repl.Content = []agent.ContentBlock{{Type: agent.BlockTypeText, Text: PrunePlaceholder}}
	postMsgs[3] = &agent.Message{ToolResult: &repl}
	occ := estimateTokens("", msgs)
	post := estimateTokens("", postMsgs)
	cfg := baseConfig()
	cfg.KeepRecentMessages = 2
	cfg.ContextWindowTokens = windowSearch(func(w int64) bool {
		return occ >= ceilFrac(w, 0.70) && post < ceilFrac(w, 0.85) && occ < ceilFrac(w, 0.95)
	})
	m, _ := newTestManager(t, cfg, nil)
	res := prepare(t, m, agent.ContextRequest{Messages: msgs})
	if len(res.Pruned) != 1 || res.Pruned[0] != "c2" {
		t.Fatalf("Pruned = %v, want [c2] (already-pruned c1 not re-reported)", res.Pruned)
	}
}

func compactFixture(n, textLen int) []*agent.Message {
	msgs := make([]*agent.Message, 0, n)
	for i := 0; i < n; i++ {
		msgs = append(msgs, userMsg(strings.Repeat("r", textLen)))
	}
	return msgs
}

func parseSummary(t *testing.T, res agent.ContextResult) (strategy string, kept []string, dropped []string) {
	t.Helper()
	if res.Compaction == nil {
		t.Fatalf("no compaction entry")
	}
	var raw struct {
		Strategy string   `json:"strategy"`
		Kept     []string `json:"kept"`
		Dropped  []string `json:"dropped"`
	}
	if err := json.Unmarshal(res.Compaction.Summary, &raw); err != nil {
		t.Fatalf("summary is not valid JSON: %v", err)
	}
	return raw.Strategy, raw.Kept, raw.Dropped
}

func compactWindow(t *testing.T, occ int64) int64 {
	t.Helper()
	w := windowSearch(func(w int64) bool {
		return occ >= ceilFrac(w, 0.85) && occ < ceilFrac(w, 0.95)
	})
	if w < 0 {
		t.Fatalf("no compact window for occ=%d", occ)
	}
	return w
}

func TestCompactSelectorKeepsVerbatim(t *testing.T) {
	msgs := compactFixture(12, 150)
	occ := estimateTokens("", msgs)
	cfg := baseConfig()
	cfg.KeepRecentMessages = 2
	cfg.ContextWindowTokens = compactWindow(t, occ)
	refs := entryRefs(nil, msgs)

	keep := []string{refs[1], refs[4]}
	stub := &stubSelector{fn: func(req subagent.SelectionRequest) (subagent.Selection, error) {
		return subagent.Selection{KeptIDs: keep}, nil
	}}
	m, _ := newTestManager(t, cfg, stub)

	res := prepare(t, m, agent.ContextRequest{Messages: msgs})
	if !res.Compacted || res.Compaction == nil {
		t.Fatalf("expected compaction, got %+v", res)
	}
	want := []*agent.Message{msgs[1], msgs[4], msgs[10], msgs[11]}
	if len(res.Messages) != len(want) {
		t.Fatalf("Messages len = %d, want %d", len(res.Messages), len(want))
	}
	for i := range want {
		if res.Messages[i] != want[i] {
			t.Fatalf("message %d: got different pointer", i)
		}
	}
	strategy, keptRefs, _ := parseSummary(t, res)
	if strategy != verbatimStrategy {
		t.Fatalf("strategy = %q, want %q", strategy, verbatimStrategy)
	}
	if len(keptRefs) != 2 || keptRefs[0] != keep[0] || keptRefs[1] != keep[1] {
		t.Fatalf("kept = %v, want %v", keptRefs, keep)
	}
	if res.Compaction.FirstKeptEntryID != refs[1] {
		t.Fatalf("FirstKeptEntryID = %q, want %q", res.Compaction.FirstKeptEntryID, refs[1])
	}
	if res.Compaction.TokensBefore != occ {
		t.Fatalf("TokensBefore = %d, want %d", res.Compaction.TokensBefore, occ)
	}
	if stub.calls != 1 {
		t.Fatalf("selector calls = %d, want 1", stub.calls)
	}
	got := stub.reqs[0]
	if got.WindowTokens != cfg.ContextWindowTokens {
		t.Fatalf("request window = %d, want %d", got.WindowTokens, cfg.ContextWindowTokens)
	}
	if len(got.Candidates) != 10 || len(got.Chunks) != 1 {
		t.Fatalf("candidates/chunks = %d/%d, want 10/1", len(got.Candidates), len(got.Chunks))
	}
	wantBudget := int64(math.Round(0.5 * float64(cfg.ContextWindowTokens)))
	if got.BudgetTokens != wantBudget {
		t.Fatalf("BudgetTokens = %d, want %d", got.BudgetTokens, wantBudget)
	}
	if len(msgs) != 12 {
		t.Fatalf("input slice mutated")
	}
}

func TestCompactChunkingBelowBudget(t *testing.T) {
	msgs := compactFixture(12, 150)
	occ := estimateTokens("", msgs)
	cfg := baseConfig()
	cfg.KeepRecentMessages = 2
	cfg.ContextWindowTokens = compactWindow(t, occ)
	cfg.SelectorChunkTokens = 60
	stub := &stubSelector{fn: func(req subagent.SelectionRequest) (subagent.Selection, error) {
		return subagent.Selection{KeptIDs: []string{req.Candidates[0].Ref}}, nil
	}}
	m, _ := newTestManager(t, cfg, stub)
	prepare(t, m, agent.ContextRequest{Messages: msgs})
	if stub.calls != 1 {
		t.Fatalf("selector calls = %d, want 1", stub.calls)
	}
	if len(stub.reqs[0].Chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(stub.reqs[0].Chunks))
	}
	for i, chunk := range stub.reqs[0].Chunks {
		var used int64
		for _, c := range chunk {
			used += rawTokensOf(c.Message)
		}
		if used > cfg.SelectorChunkTokens {
			t.Fatalf("chunk %d over budget: %d > %d", i, used, cfg.SelectorChunkTokens)
		}
	}
}

func expectedFallback(candRefs []string, cands []*agent.Message, target int64) []string {
	var kept []string
	var used int64
	for i := len(cands) - 1; i >= 0; i-- {
		t := rawTokensOf(cands[i])
		if used+t > target && len(kept) > 0 {
			break
		}
		kept = append(kept, candRefs[i])
		used += t
	}
	for l, r := 0, len(kept)-1; l < r; l, r = l+1, r-1 {
		kept[l], kept[r] = kept[r], kept[l]
	}
	return kept
}

func TestCompactFallbackDeterminism(t *testing.T) {
	msgs := compactFixture(12, 300)
	occ := estimateTokens("", msgs)
	cfg := baseConfig()
	cfg.KeepRecentMessages = 2
	cfg.ContextWindowTokens = compactWindow(t, occ)
	refs := entryRefs(nil, msgs)
	keepStart := len(msgs) - cfg.KeepRecentMessages
	candRefs := refs[:keepStart]
	cands := msgs[:keepStart]
	target := int64(math.Round(cfg.CompactTarget * float64(cfg.ContextWindowTokens)))
	wantKept := expectedFallback(candRefs, cands, target)
	if len(wantKept) == len(candRefs) {
		t.Fatalf("fixture premise broken: fallback keeps everything")
	}
	wantDropped := candRefs[:len(candRefs)-len(wantKept)]

	run := func() agent.ContextResult {
		stub := &stubSelector{fn: func(subagent.SelectionRequest) (subagent.Selection, error) {
			return subagent.Selection{}, errors.New("selector exploded")
		}}
		m, _ := newTestManager(t, cfg, stub)
		return prepare(t, m, agent.ContextRequest{Messages: msgs})
	}

	res1 := run()
	res2 := run()
	if res1.Compaction == nil || res2.Compaction == nil {
		t.Fatalf("expected compaction in both runs")
	}
	if string(res1.Compaction.Summary) != string(res2.Compaction.Summary) {
		t.Fatalf("summaries differ:\n%s\n%s", res1.Compaction.Summary, res2.Compaction.Summary)
	}
	if len(res1.Messages) != len(res2.Messages) {
		t.Fatalf("message counts differ: %d vs %d", len(res1.Messages), len(res2.Messages))
	}

	strategy, _, dropped := parseSummary(t, res1)
	if strategy != fallbackStrategy {
		t.Fatalf("strategy = %q, want %q", strategy, fallbackStrategy)
	}
	if len(dropped) != len(wantDropped) {
		t.Fatalf("dropped = %v, want %v", dropped, wantDropped)
	}
	for i := range dropped {
		if dropped[i] != wantDropped[i] {
			t.Fatalf("dropped[%d] = %q, want %q (oldest first)", i, dropped[i], wantDropped[i])
		}
	}
	keptSet := make(map[*agent.Message]bool, len(res1.Messages))
	for _, msg := range res1.Messages {
		keptSet[msg] = true
	}
	for i := len(wantDropped); i < keepStart; i++ {
		if !keptSet[msgs[i]] {
			t.Fatalf("kept set must contain candidate message %d", i)
		}
	}
	if res1.Compaction.FirstKeptEntryID != refs[len(wantDropped)] {
		t.Fatalf("FirstKeptEntryID = %q, want %q", res1.Compaction.FirstKeptEntryID, refs[len(wantDropped)])
	}
}

func TestCompactSelectorFailureFallsBack(t *testing.T) {
	msgs := compactFixture(12, 300)
	occ := estimateTokens("", msgs)
	cfg := baseConfig()
	cfg.KeepRecentMessages = 2
	cfg.ContextWindowTokens = compactWindow(t, occ)

	cases := []struct {
		name string
		fn   func(req subagent.SelectionRequest) (subagent.Selection, error)
	}{
		{"selector error", func(subagent.SelectionRequest) (subagent.Selection, error) {
			return subagent.Selection{}, errors.New("boom")
		}},
		{"unknown ref", func(subagent.SelectionRequest) (subagent.Selection, error) {
			return subagent.Selection{KeptIDs: []string{"nope"}}, nil
		}},
		{"duplicate ref", func(req subagent.SelectionRequest) (subagent.Selection, error) {
			return subagent.Selection{KeptIDs: []string{req.Candidates[0].Ref, req.Candidates[0].Ref}}, nil
		}},
		{"empty kept", func(subagent.SelectionRequest) (subagent.Selection, error) {
			return subagent.Selection{KeptIDs: nil}, nil
		}},
		{"over budget", func(req subagent.SelectionRequest) (subagent.Selection, error) {
			all := make([]string, len(req.Candidates))
			for i, c := range req.Candidates {
				all[i] = c.Ref
			}
			return subagent.Selection{KeptIDs: all}, nil
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &stubSelector{fn: tc.fn}
			m, _ := newTestManager(t, cfg, stub)
			res := prepare(t, m, agent.ContextRequest{Messages: msgs})
			if !res.Compacted {
				t.Fatalf("expected fallback compaction")
			}
			strategy, _, _ := parseSummary(t, res)
			if strategy != fallbackStrategy {
				t.Fatalf("strategy = %q, want fallback %q", strategy, fallbackStrategy)
			}
			if tc.name == "over budget" {
				var used int64
				for _, c := range stub.reqs[0].Candidates {
					used += rawTokensOf(c.Message)
				}
				if used <= stub.reqs[0].BudgetTokens {
					t.Fatalf("premise broken: candidate cost %d within budget %d", used, stub.reqs[0].BudgetTokens)
				}
			}
		})
	}
}

func compactWindowMax(t *testing.T, occ int64) int64 {
	t.Helper()
	w := int64(math.Floor(float64(occ) / 0.85))
	if occ >= ceilFrac(w, 0.85) && occ < ceilFrac(w, 0.95) {
		return w
	}
	return windowSearch(func(w int64) bool {
		return occ >= ceilFrac(w, 0.85) && occ < ceilFrac(w, 0.95)
	})
}

func TestCompactNoopWhenNothingDropped(t *testing.T) {
	msgs := compactFixture(12, 60)
	occ := estimateTokens("", msgs)
	cfg := baseConfig()
	cfg.KeepRecentMessages = 2
	cfg.ContextWindowTokens = compactWindowMax(t, occ)
	refs := entryRefs(nil, msgs)
	all := refs[:10]
	stub := &stubSelector{fn: func(req subagent.SelectionRequest) (subagent.Selection, error) {
		var used int64
		for _, c := range req.Candidates {
			used += rawTokensOf(c.Message)
		}
		if used > req.BudgetTokens {
			return subagent.Selection{}, errors.New("premise broken: candidates exceed budget")
		}
		return subagent.Selection{KeptIDs: all}, nil
	}}
	m, _ := newTestManager(t, cfg, stub)
	res := prepare(t, m, agent.ContextRequest{Messages: msgs})
	if res.Compacted || res.Compaction != nil {
		t.Fatalf("expected no-op compaction, got Compacted=%v", res.Compacted)
	}
	if len(res.Messages) != len(msgs) {
		t.Fatalf("message list must be unchanged")
	}
	for i := range msgs {
		if res.Messages[i] != msgs[i] {
			t.Fatalf("message %d must pass through by pointer", i)
		}
	}
}

func TestCompactNothingToCompact(t *testing.T) {
	msgs := compactFixture(4, 400)
	occ := estimateTokens("", msgs)
	cfg := baseConfig()
	cfg.KeepRecentMessages = 6
	cfg.ContextWindowTokens = windowSearch(func(w int64) bool { return occ >= ceilFrac(w, 0.85) })
	m, _ := newTestManager(t, cfg, nil)
	res := prepare(t, m, agent.ContextRequest{Messages: msgs})
	if res.Compacted || res.Compaction != nil {
		t.Fatalf("all messages protected: expected no compaction")
	}
}

func TestCompactPinnedCandidatesProtected(t *testing.T) {
	msgs := []*agent.Message{
		asstCall("pin1", "read", `{}`),
		toolResult("pin1", "read", strings.Repeat("P", 300), false),
		asstCall("gone1", "read", `{}`),
		toolResult("gone1", "read", strings.Repeat("G", 300), false),
	}
	for i := 0; i < 8; i++ {
		msgs = append(msgs, userMsg(strings.Repeat("r", 400)))
	}
	occ := estimateTokens("", msgs)
	cfg := baseConfig()
	cfg.KeepRecentMessages = 2
	cfg.ContextWindowTokens = compactWindow(t, occ)
	keepStart := len(msgs) - cfg.KeepRecentMessages
	target := int64(math.Round(cfg.CompactTarget * float64(cfg.ContextWindowTokens)))
	var candCost int64
	for i := 0; i < keepStart; i++ {
		if msgs[i] != nil {
			candCost += rawTokensOf(msgs[i])
		}
	}
	if candCost <= target {
		t.Fatalf("premise broken: candidate cost %d within target %d", candCost, target)
	}
	m, _ := newTestManager(t, cfg, nil)
	m.PinToolCall("pin1")
	res := prepare(t, m, agent.ContextRequest{Messages: msgs})
	if !res.Compacted {
		t.Fatalf("expected compaction")
	}
	if res.Messages[0] != msgs[0] || res.Messages[1] != msgs[1] {
		t.Fatalf("pinned pair must survive: got %p %p", res.Messages[0], res.Messages[1])
	}
	if res.Messages[1].ToolResult.Content[0].Text == PrunePlaceholder {
		t.Fatalf("pinned result pruned")
	}
}

func TestCompactUsesEntryIDs(t *testing.T) {
	msgs := compactFixture(12, 150)
	occ := estimateTokens("", msgs)
	cfg := baseConfig()
	cfg.KeepRecentMessages = 2
	cfg.ContextWindowTokens = compactWindow(t, occ)
	ids := make([]string, len(msgs))
	for i := range ids {
		ids[i] = "e" + string(rune('a'+i))
	}
	keep := []string{ids[1], ids[4]}
	stub := &stubSelector{fn: func(subagent.SelectionRequest) (subagent.Selection, error) {
		return subagent.Selection{KeptIDs: keep}, nil
	}}
	m, _ := newTestManager(t, cfg, stub)
	res := prepare(t, m, agent.ContextRequest{Messages: msgs, EntryIDs: ids})
	_, keptRefs, _ := parseSummary(t, res)
	if len(keptRefs) != 2 || keptRefs[0] != "eb" || keptRefs[1] != "ee" {
		t.Fatalf("kept = %v, want [eb ee]", keptRefs)
	}
	if res.Compaction.FirstKeptEntryID != "eb" {
		t.Fatalf("FirstKeptEntryID = %q, want eb", res.Compaction.FirstKeptEntryID)
	}
}

func TestCompactCacheStaleGate(t *testing.T) {
	msgs := compactFixture(12, 150)
	occ := estimateTokens("", msgs)
	cfg := baseConfig()
	cfg.KeepRecentMessages = 2
	cfg.ContextWindowTokens = compactWindow(t, occ)
	m, now := newTestManager(t, cfg, nil)

	m.ObserveResponse(&agent.AssistantMessage{Usage: agent.Usage{}})
	res := prepare(t, m, agent.ContextRequest{Messages: msgs})
	if res.Compacted || len(res.Pruned) != 0 {
		t.Fatalf("warm cache must freeze the prefix: got Compacted=%v Pruned=%v", res.Compacted, res.Pruned)
	}

	*now = now.Add(cfg.CacheMissAfter + time.Second)
	res = prepare(t, m, agent.ContextRequest{Messages: msgs})
	if !res.Compacted || res.Compaction == nil {
		t.Fatalf("stale cache must allow compact, got %+v", res)
	}
}

func TestSafetyCompactIgnoresCacheAge(t *testing.T) {
	msgs := compactFixture(12, 60)
	occ := estimateTokens("", msgs)
	cfg := baseConfig()
	cfg.KeepRecentMessages = 2
	cfg.ContextWindowTokens = windowSearch(func(w int64) bool { return occ >= ceilFrac(w, 0.95) })
	m, now := newTestManager(t, cfg, nil)

	m.ObserveResponse(&agent.AssistantMessage{Usage: agent.Usage{}})
	res := prepare(t, m, agent.ContextRequest{Messages: msgs})
	if !res.Compacted || res.Compaction == nil {
		t.Fatalf("safety compact must fire regardless of cache age")
	}
	strategy, _, _ := parseSummary(t, res)
	if strategy != fallbackStrategy {
		t.Fatalf("strategy = %q, want fallback", strategy)
	}

	cfg2 := baseConfig()
	cfg2.KeepRecentMessages = 2
	cfg2.ContextWindowTokens = windowSearch(func(w int64) bool { return occ < ceilFrac(w, 0.95) })
	m2, _ := newTestManager(t, cfg2, nil)
	m2.ObserveResponse(&agent.AssistantMessage{Usage: agent.Usage{}})
	res2 := prepare(t, m2, agent.ContextRequest{Messages: msgs})
	if res2.Compacted || len(res2.Pruned) != 0 {
		t.Fatalf("below safety with fresh cache: expected no action, got %+v", res2)
	}

	*now = now.Add(cfg.CacheMissAfter + time.Second)
	res3 := prepare(t, m, agent.ContextRequest{Messages: msgs})
	if !res3.Compacted {
		t.Fatalf("safety compact must fire on a stale cache too")
	}
}

func TestPruneThenCompact(t *testing.T) {
	msgs := []*agent.Message{
		asstCall("c1", "read", `{}`),
		toolResult("c1", "read", "tiny", false),
		asstCall("c2", "read", `{}`),
		toolResult("c2", "read", "tiny", false),
	}
	for i := 0; i < 8; i++ {
		msgs = append(msgs, userMsg(strings.Repeat("r", 120)))
	}
	occ := estimateTokens("", msgs)
	post := estimateTokens("", expectedPruned(msgs))
	w := windowSearch(func(w int64) bool {
		return occ >= ceilFrac(w, 0.70) && post >= ceilFrac(w, 0.85) && occ < ceilFrac(w, 0.95)
	})
	if w < 0 {
		t.Fatalf("no window for prune+compact (occ=%d post=%d)", occ, post)
	}
	cfg := baseConfig()
	cfg.KeepRecentMessages = 2
	cfg.ContextWindowTokens = w
	m, _ := newTestManager(t, cfg, nil)
	res := prepare(t, m, agent.ContextRequest{Messages: msgs})
	if len(res.Pruned) != 2 {
		t.Fatalf("expected 2 pruned, got %v", res.Pruned)
	}
	if !res.Compacted {
		t.Fatalf("expected compaction after pruning")
	}
}

func TestPruneOnlyWhenBelowCompactAfter(t *testing.T) {
	msgs := pruneFixture()
	occ := estimateTokens("", msgs)
	post := estimateTokens("", expectedPruned(msgs))
	cfg := baseConfig()
	cfg.KeepRecentMessages = 2
	cfg.ContextWindowTokens = windowSearch(func(w int64) bool {
		return occ >= ceilFrac(w, 0.70) && post < ceilFrac(w, 0.85) && occ < ceilFrac(w, 0.95)
	})
	if cfg.ContextWindowTokens < 0 {
		t.Fatalf("no prune-only window (occ=%d post=%d)", occ, post)
	}
	m, _ := newTestManager(t, cfg, nil)
	res := prepare(t, m, agent.ContextRequest{Messages: msgs})
	if len(res.Pruned) != 2 {
		t.Fatalf("expected 2 pruned, got %v", res.Pruned)
	}
	if res.Compacted {
		t.Fatalf("pruning must have dropped occupancy below the compact threshold")
	}
}

func TestPreparePropagatesCancelledContext(t *testing.T) {
	msgs := compactFixture(12, 150)
	occ := estimateTokens("", msgs)
	cfg := baseConfig()
	cfg.KeepRecentMessages = 2
	cfg.ContextWindowTokens = compactWindow(t, occ)
	stub := &stubSelector{fn: func(subagent.SelectionRequest) (subagent.Selection, error) {
		return subagent.Selection{}, errors.New("selector failed")
	}}
	m, _ := newTestManager(t, cfg, stub)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := m.Prepare(ctx, agent.ContextRequest{Messages: msgs})
	if err == nil {
		t.Fatalf("Prepare with cancelled context: nil error, want context.Canceled")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}
