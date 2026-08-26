package contextmanager

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/subagent"
)

type Manager struct {
	cfg      Config
	selector subagent.Selector
	clock    func() time.Time

	mu               sync.Mutex
	pinned           map[agent.ToolCallID]struct{}
	lastRequest      time.Time
	lastResponse     time.Time
	anchored         bool
	anchorInput      int64
	anchorCount      int
	lastPrepareCount int
}

func New(cfg Config, selector subagent.Selector) (*Manager, error) {
	return newWithClock(cfg, selector, time.Now)
}

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

func (m *Manager) ObserveRequest(t time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.cfg.Enabled {
		return
	}
	m.lastRequest = t
}

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

func (m *Manager) PinToolCall(id agent.ToolCallID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pinned[id] = struct{}{}
}

func (m *Manager) UnpinToolCall(id agent.ToolCallID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pinned, id)
}

func (m *Manager) recordSent(n int) {
	m.mu.Lock()
	m.lastPrepareCount = n
	m.mu.Unlock()
}

func (m *Manager) cacheStaleLocked(now time.Time) bool {
	return m.lastResponse.IsZero() || now.Sub(m.lastResponse) > m.cfg.CacheMissAfter
}

func thresholdTokens(cfg Config, threshold float64) int64 {
	return int64(math.Ceil(threshold * float64(cfg.ContextWindowTokens)))
}
