package cli

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/contextmanager"
	"github.com/digitalygo/smidja/sdk"
)

type contextPreparerAdapter struct {
	live          *contextmanager.Manager
	recovery      *contextmanager.Manager
	forceTokens   int64
	contextWindow int64
	selectorModel string

	mu      sync.Mutex
	force   bool
	entries []*agent.CompactionEntry

	compactMu      sync.Mutex
	pendingCompact *sdk.CompactOptions
	explicitMu     sync.Mutex
	explicit       func(ctx context.Context, req agent.ContextRequest, opts sdk.CompactOptions) agent.ContextResult
}

var _ agent.ContextPreparer = (*contextPreparerAdapter)(nil)

func newContextPreparerAdapter(live *contextmanager.Manager, cfg contextmanager.Config) *contextPreparerAdapter {
	recovery, err := contextmanager.New(cfg, nil)
	if err != nil {
		recovery = live
	}
	return &contextPreparerAdapter{
		live:          live,
		recovery:      recovery,
		forceTokens:   int64(math.Ceil(cfg.SafetyCompactThreshold * float64(cfg.ContextWindowTokens))),
		contextWindow: cfg.ContextWindowTokens,
		selectorModel: cfg.SelectorModel,
	}
}

func (a *contextPreparerAdapter) Prepare(ctx context.Context, req agent.ContextRequest) (agent.ContextResult, error) {
	if opts, ok := a.takeCompact(); ok {
		if explicit := a.explicitHook(); explicit != nil {
			return explicit(ctx, req, opts), nil
		}
	}
	a.mu.Lock()
	force := a.force
	a.force = false
	a.mu.Unlock()

	var res agent.ContextResult
	var err error
	if force {
		req.LastUsageInput = a.forceTokens
		res, err = a.recovery.Prepare(ctx, req)
	} else {
		res, err = a.live.Prepare(ctx, req)
	}
	if err != nil {
		return res, err
	}
	if res.Compaction != nil {
		a.mu.Lock()
		a.entries = append(a.entries, res.Compaction)
		a.mu.Unlock()
	}
	return res, nil
}

func (a *contextPreparerAdapter) ObserveRequest(t time.Time) {
	a.live.ObserveRequest(t)
}

func (a *contextPreparerAdapter) ObserveResponse(m *agent.AssistantMessage) {
	a.live.ObserveResponse(m)
}

func (a *contextPreparerAdapter) forceSafety() {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.force = true
	a.mu.Unlock()
}

func (a *contextPreparerAdapter) drain() []*agent.CompactionEntry {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	out := a.entries
	a.entries = nil
	return out
}

func (a *contextPreparerAdapter) requestCompact(opts sdk.CompactOptions) bool {
	a.compactMu.Lock()
	defer a.compactMu.Unlock()
	if a.pendingCompact != nil {
		return false
	}
	pending := opts
	a.pendingCompact = &pending
	return true
}

func (a *contextPreparerAdapter) takeCompact() (sdk.CompactOptions, bool) {
	a.compactMu.Lock()
	defer a.compactMu.Unlock()
	if a.pendingCompact == nil {
		return sdk.CompactOptions{}, false
	}
	opts := *a.pendingCompact
	a.pendingCompact = nil
	return opts, true
}

func (a *contextPreparerAdapter) setExplicit(hook func(ctx context.Context, req agent.ContextRequest, opts sdk.CompactOptions) agent.ContextResult) {
	a.explicitMu.Lock()
	a.explicit = hook
	a.explicitMu.Unlock()
}

func (a *contextPreparerAdapter) explicitHook() func(ctx context.Context, req agent.ContextRequest, opts sdk.CompactOptions) agent.ContextResult {
	a.explicitMu.Lock()
	defer a.explicitMu.Unlock()
	return a.explicit
}

func (a *contextPreparerAdapter) compactNow(ctx context.Context, system string, messages []*agent.Message, entryIDs []string) ([]*agent.Message, *agent.CompactionEntry, error) {
	if a == nil || a.live == nil {
		return nil, nil, contextmanager.ErrDisabled
	}
	return a.live.CompactNow(ctx, system, messages, entryIDs)
}

type compactionSink interface {
	appendCompaction(*agent.CompactionEntry) error
}
