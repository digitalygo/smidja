package agent

import (
	"context"
	"encoding/json"
	"time"
)

type ToolCallID string

type ContextRequest struct {
	Messages []*Message

	System string

	LastUsageInput int64

	EntryIDs []string
}

type ContextResult struct {
	Messages []*Message

	System string

	Pruned []ToolCallID

	Compacted bool

	Compaction *CompactionEntry
}

type CompactionEntry struct {
	Summary json.RawMessage

	FirstKeptEntryID string

	TokensBefore int64
}

type ContextPreparer interface {
	Prepare(ctx context.Context, req ContextRequest) (ContextResult, error)

	ObserveRequest(t time.Time)

	ObserveResponse(m *AssistantMessage)
}

type ToolCallDecision struct {
	Block bool

	Reason string

	FinalArgs json.RawMessage
}

type HookDispatcher interface {
	Context(ctx context.Context, req ContextRequest) (ContextResult, error)

	MessageEnd(ctx context.Context, m *Message) (*Message, error)

	AutoRetryStart(ctx context.Context, attempt int, maxAttempts int, delayMs int64, errorMessage string) error

	AutoRetryEnd(ctx context.Context, success bool, attempt int, finalError string) error

	ToolCall(ctx context.Context, name string, callID string, args json.RawMessage) (ToolCallDecision, error)

	ToolResult(ctx context.Context, name string, callID string, args json.RawMessage, res Result) (Result, error)

	SessionStart(ctx context.Context, reason string) error

	SessionShutdown(ctx context.Context, reason string) error
}

type ToolCatalog interface {
	Tools() []Tool

	Get(name string) (Tool, bool)
}

type RetryPolicy struct {
	Enabled bool

	MaxRetries int

	BaseDelayMs int64
}

func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{Enabled: true, MaxRetries: 10, BaseDelayMs: 2000}
}

type RetryCallbacks struct {
	Scheduled func(attempt, maxAttempts int, delayMs int64, errorMessage string)

	AttemptStart func()

	Finished func(success bool, attempt int, finalError string)
}

type Verdict int

const (
	VerdictNone Verdict = iota

	VerdictWarn

	VerdictBlock
)

func (v Verdict) String() string {
	switch v {
	case VerdictWarn:
		return "warning"
	case VerdictBlock:
		return "force-stop"
	default:
		return "none"
	}
}

type Finding struct {
	Type string

	Message string
}

type ToolCallObs struct {
	ToolCallID string

	Name string

	Arguments json.RawMessage

	Result *ToolResultMessage
}

type Turn struct {
	TurnIndex int

	ThinkingText string

	TextContent string

	ToolCalls []ToolCallObs
}

type Outcome struct {
	Verdict Verdict

	Findings []Finding

	SteerCustomType string

	SteerText string
}

type LoopDetector interface {
	Observe(turn Turn) Outcome
}
