// Package contextmanager implements the smart context management core of
// smidja: occupancy estimation, cache-stale gated tool-result pruning,
// verbatim compaction through an injected selector subagent, and tool
// call pinning. It implements agent.ContextPreparer; the loop drives it
// through Prepare/ObserveRequest/ObserveResponse and persists the
// compaction entries it reports. Extension context hooks are dispatched
// by the loop, not by the manager.
package contextmanager

import (
	"fmt"
	"time"
)

// Default values applied by New for unset Config fields.
const (
	// DefaultCacheMissAfter is how long after the last observed response
	// the provider cache is considered stale.
	DefaultCacheMissAfter = 5 * time.Minute

	// DefaultPruneThreshold is the occupancy fraction above which
	// stale-cache calls prune old tool results.
	DefaultPruneThreshold = 0.70

	// DefaultCompactThreshold is the occupancy fraction above which
	// stale-cache calls compact.
	DefaultCompactThreshold = 0.85

	// DefaultSafetyCompactThreshold is the occupancy fraction above
	// which compaction fires immediately, regardless of cache age.
	DefaultSafetyCompactThreshold = 0.95

	// DefaultCompactTarget is the fraction of the context window the
	// retained messages may consume after compaction.
	DefaultCompactTarget = 0.50

	// DefaultKeepRecentMessages is how many trailing messages are never
	// pruned or compacted.
	DefaultKeepRecentMessages = 6

	// DefaultSelectorChunkTokens is the token budget below which
	// candidate messages are chunked before being handed to the
	// selector.
	DefaultSelectorChunkTokens = 12_000
)

// Config carries the context-management policy. New replaces every unset
// threshold field with its default; Validate enforces the invariants.
type Config struct {
	// Enabled turns context management on. When false, Prepare passes
	// the request through unchanged and the observation methods are
	// no-ops.
	Enabled bool

	// ContextWindowTokens is the model context window in tokens. It is
	// required: every threshold is a fraction of it.
	ContextWindowTokens int64

	// CacheMissAfter is how long after the last observed response the
	// provider cache is considered stale. Prune and compact only fire
	// on a stale cache, except the safety compact, which ignores cache
	// age. Default 5 minutes.
	CacheMissAfter time.Duration

	// PruneThreshold is the occupancy fraction above which stale-cache
	// calls prune old tool results. Default 0.70.
	PruneThreshold float64

	// CompactThreshold is the occupancy fraction above which stale-cache
	// calls compact via the selector. Default 0.85.
	CompactThreshold float64

	// SafetyCompactThreshold is the occupancy fraction above which
	// compaction fires immediately, regardless of cache age. Default
	// 0.95.
	SafetyCompactThreshold float64

	// CompactTarget is the fraction of the context window the retained
	// messages (excluding the protected recent window) may consume
	// after compaction. It must stay below PruneThreshold so compaction
	// lands under the prune line. Default 0.50.
	CompactTarget float64

	// KeepRecentMessages is how many trailing messages are never pruned
	// or compacted. Default 6.
	KeepRecentMessages int

	// SelectorModel is the provider model identifier used for selector
	// turns, for example "anthropic/claude-sonnet-4.5". It may be empty
	// when no selector is injected.
	SelectorModel string

	// SelectorChunkTokens is the token budget below which candidate
	// messages are chunked before being handed to the selector.
	// Messages are never split; an oversized message gets its own
	// chunk. Default 12_000.
	SelectorChunkTokens int64
}

// Validate checks the policy invariants: ordered thresholds in (0,1],
// a compact target below the prune threshold, and positive window,
// cache-miss delay, and chunk budget.
func (c Config) Validate() error {
	switch {
	case c.ContextWindowTokens <= 0:
		return fmt.Errorf("contextmanager: ContextWindowTokens must be > 0, got %d", c.ContextWindowTokens)
	case c.CacheMissAfter <= 0:
		return fmt.Errorf("contextmanager: CacheMissAfter must be > 0, got %s", c.CacheMissAfter)
	case c.PruneThreshold <= 0 || c.PruneThreshold >= 1:
		return fmt.Errorf("contextmanager: PruneThreshold must be in (0,1), got %v", c.PruneThreshold)
	case c.CompactThreshold <= 0 || c.CompactThreshold >= 1:
		return fmt.Errorf("contextmanager: CompactThreshold must be in (0,1), got %v", c.CompactThreshold)
	case c.SafetyCompactThreshold <= 0 || c.SafetyCompactThreshold > 1:
		return fmt.Errorf("contextmanager: SafetyCompactThreshold must be in (0,1], got %v", c.SafetyCompactThreshold)
	case !(c.PruneThreshold < c.CompactThreshold):
		return fmt.Errorf("contextmanager: PruneThreshold %v must be < CompactThreshold %v", c.PruneThreshold, c.CompactThreshold)
	case !(c.CompactThreshold < c.SafetyCompactThreshold):
		return fmt.Errorf("contextmanager: CompactThreshold %v must be < SafetyCompactThreshold %v", c.CompactThreshold, c.SafetyCompactThreshold)
	case c.CompactTarget <= 0 || c.CompactTarget >= c.PruneThreshold:
		return fmt.Errorf("contextmanager: CompactTarget must be in (0, PruneThreshold=%v), got %v", c.PruneThreshold, c.CompactTarget)
	case c.KeepRecentMessages < 0:
		return fmt.Errorf("contextmanager: KeepRecentMessages must be >= 0, got %d", c.KeepRecentMessages)
	case c.SelectorChunkTokens <= 0:
		return fmt.Errorf("contextmanager: SelectorChunkTokens must be > 0, got %d", c.SelectorChunkTokens)
	}
	return nil
}

// withDefaults returns a copy of c with unset threshold fields replaced
// by their defaults. A field set to its zero value means "unset"; there
// is no way to configure a zero threshold, which Validate rejects anyway.
func (c Config) withDefaults() Config {
	if c.CacheMissAfter <= 0 {
		c.CacheMissAfter = DefaultCacheMissAfter
	}
	if c.PruneThreshold <= 0 {
		c.PruneThreshold = DefaultPruneThreshold
	}
	if c.CompactThreshold <= 0 {
		c.CompactThreshold = DefaultCompactThreshold
	}
	if c.SafetyCompactThreshold <= 0 {
		c.SafetyCompactThreshold = DefaultSafetyCompactThreshold
	}
	if c.CompactTarget <= 0 {
		c.CompactTarget = DefaultCompactTarget
	}
	if c.KeepRecentMessages <= 0 {
		c.KeepRecentMessages = DefaultKeepRecentMessages
	}
	if c.SelectorChunkTokens <= 0 {
		c.SelectorChunkTokens = DefaultSelectorChunkTokens
	}
	return c
}
