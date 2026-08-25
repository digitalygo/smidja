// Package subagent implements the verbatim compaction selector: a
// lightweight sub-agent that reads candidate older messages and returns
// the entry refs to keep, so the context manager can restore them
// verbatim instead of generating a prose summary.
//
// The selector runs outside the normal turn machinery by construction:
// its turns bypass the hook chain and context management (the manager
// never feeds the selector's own context back into itself), and it is
// never passed tools.
package subagent

import (
	"context"
	"errors"

	"github.com/digitalygo/smidja/internal/agent"
)

// Candidate is one older message eligible for compaction, with the entry
// ref the manager and the selector use to identify it.
type Candidate struct {
	// Ref identifies the message in the selection output and in the
	// compaction transcript.
	Ref string

	// Message is the candidate conversation message.
	Message *agent.Message
}

// SelectionRequest asks the selector which candidate messages to keep.
// Candidates is the flattened chronological candidate list; Chunks
// groups the same candidates below the chunk token budget so the
// selector can read them without overflowing its own context.
type SelectionRequest struct {
	// Model is the provider model identifier to use for selection.
	Model string

	// Candidates is the flattened chronological candidate list.
	Candidates []Candidate

	// Chunks groups Candidates into chunks below the chunk budget.
	Chunks [][]Candidate

	// BudgetTokens is the maximum estimated tokens the kept set may
	// consume. A kept set above the budget is invalid.
	BudgetTokens int64

	// WindowTokens is the context window the selection must fit.
	WindowTokens int64
}

// Selection is the selector's answer: the entry refs to keep.
type Selection struct {
	// KeptIDs are the refs of the candidates to keep, in selection
	// order. Every ref must be a known candidate ref, refs must be
	// unique, the set must be non-empty, and the estimated token cost
	// of the kept set must stay within SelectionRequest.BudgetTokens.
	KeptIDs []string
}

// Selector chooses which candidate messages survive verbatim compaction.
type Selector interface {
	// Select returns the refs of the candidates to keep. Errors are
	// treated by the caller as a selection failure and trigger the
	// deterministic fallback.
	Select(ctx context.Context, req SelectionRequest) (Selection, error)
}

// ErrInvalidSelection reports a selection that violates the validity
// contract (unknown, duplicate, empty, or over-budget kept refs).
var ErrInvalidSelection = errors.New("subagent: invalid selection")
