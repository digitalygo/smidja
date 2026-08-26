package contextmanager

import (
	"fmt"
	"time"
)

const (
	DefaultCacheMissAfter = 5 * time.Minute

	DefaultPruneThreshold = 0.70

	DefaultCompactThreshold = 0.85

	DefaultSafetyCompactThreshold = 0.95

	DefaultCompactTarget = 0.50

	DefaultKeepRecentMessages = 6

	DefaultSelectorChunkTokens = 12_000
)

type Config struct {
	Enabled bool

	ContextWindowTokens int64

	CacheMissAfter time.Duration

	PruneThreshold float64

	CompactThreshold float64

	SafetyCompactThreshold float64

	CompactTarget float64

	KeepRecentMessages int

	SelectorModel string

	SelectorChunkTokens int64
}

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
