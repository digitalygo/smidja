package subagent

import (
	"context"
	"errors"

	"github.com/digitalygo/smidja/internal/agent"
)

type Candidate struct {
	Ref string

	Message *agent.Message
}

type SelectionRequest struct {
	Model string

	Candidates []Candidate

	Chunks [][]Candidate

	BudgetTokens int64

	WindowTokens int64
}

type Selection struct {
	KeptIDs []string
}

type Selector interface {
	Select(ctx context.Context, req SelectionRequest) (Selection, error)
}

var ErrInvalidSelection = errors.New("subagent: invalid selection")
