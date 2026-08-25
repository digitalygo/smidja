package contextmanager

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/subagent"
)

// Manager implements agent.ContextPreparer with the smart context
// policy: occupancy estimation, cache-stale gated tool-result pruning,
// verbatim compaction through an injected selector, and tool call
// pinning. Prepare never mutates the request; it returns a new message
// list. Extension context hooks are dispatched by the loop, not here.
//
// The Manager is safe for concurrent use; the loop drives it
// sequentially (Prepare, then ObserveRequest/ObserveResponse around each
// provider call).
type Manager struct {
	cfg      Config
	selector subagent.Selector
	clock    func() time.Time

	mu sync.Mutex
	// pinned holds the tool call ids that must never be pruned or
	// compacted away.
	pinned map[agent.ToolCallID]struct{}
	// lastRequest and lastResponse drive cache staleness: the cache is
	// stale when no response has been observed yet or the time since the
	// last observed response exceeds CacheMissAfter.
	lastRequest  time.Time
	lastResponse time.Time
	// anchored records that a real provider usage measurement is
	// available (from ObserveResponse), anchorInput is its Input token
	// count, and anchorCount is how many messages the previous call sent
	// (so the delta estimate knows what has been appended since).
	anchored         bool
	anchorInput      int64
	anchorCount      int
	lastPrepareCount int
}

// New returns a Manager for the given policy and selector. The selector
// may be nil: compaction then always uses the deterministic fallback. New
// applies the configuration defaults and validates the result.
func New(cfg Config, selector subagent.Selector) (*Manager, error) {
	return newWithClock(cfg, selector, time.Now)
}

// newWithClock is the test seam: New builds the manager with time.Now,
// tests inject a fixed clock.
func newWithClock(cfg Config, selector subagent.Selector, clock func() time.Time) (*Manager, error) {
	cfg = cfg.withDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if clock == nil {
		clock = time.Now
	}
	return &Manager{
		cfg:      cfg,
		selector: selector,
		clock:    clock,
		pinned:   make(map[agent.ToolCallID]struct{}),
	}, nil
}

// Prepare assembles the context for one provider call:
//
//  1. It estimates the occupancy: the full-context estimate, or the max
//     of it and the anchor-based delta estimate when the request or a
//     previously observed response carries a real input-token count.
//  2. Safety: an occupancy at or above the safety threshold compacts
//     immediately, regardless of cache age.
//  3. Otherwise, when the cache is stale and the occupancy is at or
//     above the prune threshold, it prunes old tool results.
//  4. It re-estimates and, when the cache is stale and the occupancy is
//     still at or above the compact threshold, compacts via the
//     selector (with the deterministic fallback on any failure).
//
// The returned ContextResult carries the final message list and the
// actions taken (Pruned, Compacted, Compaction) for the caller to report
// and persist.
func (m *Manager) Prepare(ctx context.Context, req agent.ContextRequest) (agent.ContextResult, error) {
	m.mu.Lock()
	if !m.cfg.Enabled {
		m.mu.Unlock()
		return agent.ContextResult{Messages: req.Messages, System: req.System}, nil
	}
	stale := m.cacheStaleLocked(m.clock())
	anchored, anchorInput, anchorCount := m.anchored, m.anchorInput, m.anchorCount
	pinned := make(map[agent.ToolCallID]struct{}, len(m.pinned))
	for id := range m.pinned {
		pinned[id] = struct{}{}
	}
	cfg := m.cfg
	m.mu.Unlock()

	occ := m.estimateOccupancy(req, anchored, anchorInput, anchorCount)

	// Safety compact: fires immediately, regardless of cache age.
	if occ >= thresholdTokens(cfg, cfg.SafetyCompactThreshold) {
		msgs, entry, err := m.compact(ctx, req.System, req.Messages, occ, pinned, req.EntryIDs)
		if err != nil {
			return agent.ContextResult{}, err
		}
		res := agent.ContextResult{Messages: msgs, System: req.System}
		if entry != nil {
			res.Compacted = true
			res.Compaction = entry
		}
		m.recordSent(len(msgs))
		return res, nil
	}

	res := agent.ContextResult{Messages: req.Messages, System: req.System}

	if stale && occ >= thresholdTokens(cfg, cfg.PruneThreshold) {
		out := pruneMessages(req.Messages, cfg.KeepRecentMessages, pinned)
		if len(out.ids) > 0 {
			res.Messages = out.messages
			res.Pruned = out.ids
			occ = m.estimateOccupancy(agent.ContextRequest{System: req.System, Messages: out.messages}, anchored, anchorInput, anchorCount)
		}
	}

	if stale && occ >= thresholdTokens(cfg, cfg.CompactThreshold) {
		msgs, entry, err := m.compact(ctx, res.System, res.Messages, occ, pinned, req.EntryIDs)
		if err != nil {
			return agent.ContextResult{}, err
		}
		if entry != nil {
			res.Messages = msgs
			res.Compacted = true
			res.Compaction = entry
		}
	}

	m.recordSent(len(res.Messages))
	return res, nil
}

// ObserveRequest records the start time of a provider request. It is a
// no-op when context management is disabled.
func (m *Manager) ObserveRequest(t time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.cfg.Enabled {
		return
	}
	m.lastRequest = t
}

// ObserveResponse records the completed assistant message: the response
// time refreshes cache staleness, and a positive usage input anchors the
// next occupancy estimate to what the provider really billed.
func (m *Manager) ObserveResponse(msg *agent.AssistantMessage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.cfg.Enabled {
		return
	}
	m.lastResponse = m.clock()
	if msg != nil && msg.Usage.Input > 0 {
		m.anchorInput = msg.Usage.Input
		m.anchorCount = m.lastPrepareCount
		m.anchored = true
	}
}

// PinToolCall marks a tool call id as protected: its tool result is never
// pruned and neither the result nor the assistant message carrying the
// call block is ever compacted away.
func (m *Manager) PinToolCall(id agent.ToolCallID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pinned[id] = struct{}{}
}

// UnpinToolCall removes a pin set by PinToolCall.
func (m *Manager) UnpinToolCall(id agent.ToolCallID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pinned, id)
}

// recordSent remembers how many messages the last Prepare sent, so the
// next observed response can anchor the delta estimate to that boundary.
func (m *Manager) recordSent(n int) {
	m.mu.Lock()
	m.lastPrepareCount = n
	m.mu.Unlock()
}

// cacheStaleLocked reports whether the provider cache is stale: no
// response has been observed yet, or the time since the last observed
// response exceeds CacheMissAfter. Callers hold mu.
func (m *Manager) cacheStaleLocked(now time.Time) bool {
	return m.lastResponse.IsZero() || now.Sub(m.lastResponse) > m.cfg.CacheMissAfter
}

// thresholdTokens returns the occupancy in tokens that crosses the
// threshold: ceil(threshold * window).
func thresholdTokens(cfg Config, threshold float64) int64 {
	return int64(math.Ceil(threshold * float64(cfg.ContextWindowTokens)))
}
