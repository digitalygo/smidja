package sdk

import (
	"context"
	"encoding/json"
	"time"
)

type ThinkingLevel string

const (
	ThinkingOff     ThinkingLevel = "off"
	ThinkingMinimal ThinkingLevel = "minimal"
	ThinkingLow     ThinkingLevel = "low"
	ThinkingMedium  ThinkingLevel = "medium"
	ThinkingHigh    ThinkingLevel = "high"
	ThinkingXHigh   ThinkingLevel = "xhigh"
	ThinkingMax     ThinkingLevel = "max"
)

type Model struct {
	ID string

	Name string

	Provider string
}

type Cost struct {
	Input      float64
	Output     float64
	CacheRead  float64
	CacheWrite float64
	Total      float64
}

type Usage struct {
	Input       int64
	Output      int64
	CacheRead   int64
	CacheWrite  int64
	Reasoning   int64
	TotalTokens int64
	Cost        Cost
}

type Block struct {
	Type      string
	Text      string
	Thinking  string
	ID        string
	Name      string
	Arguments json.RawMessage
}

type Message struct {
	Role    string
	Content []Block
	Usage   *Usage
}

type Tool interface {
	Name() string

	Description() string

	Schema() json.RawMessage

	Exec(ctx context.Context, args json.RawMessage) Result
}

type Result struct {
	Content []Block
	Details any
	IsError bool
	Usage   *Usage
}

type ToolInfo struct {
	Name        string
	Description string
	Schema      json.RawMessage
	Source      string
}

type Command struct {
	Description string
	Handler     func(ctx CommandContext, args string) error
}

type CommandInfo struct {
	Name        string
	Description string
}

type CustomMessage struct {
	Type    string
	Content string
	Display bool
	Details any
}

type DeliveryMode string

const (
	DeliverySteer    DeliveryMode = "steer"
	DeliveryFollowUp DeliveryMode = "followUp"
	DeliveryNextTurn DeliveryMode = "nextTurn"
)

type SendOptions struct {
	DeliverAs DeliveryMode

	TriggerTurn bool

	ExpandPromptTemplates bool
}

type ProviderConfig struct {
	Name    string
	BaseURL string
	APIKey  string
	API     string
	Models  []Model
}

type FlagOptions struct {
	Description string
	Type        string
	Default     any
}

type ExecOptions struct {
	Timeout time.Duration
}

type ExecResult struct {
	Stdout string
	Stderr string
	Code   int
	Killed bool
}

type ContextUsage struct {
	Tokens        *int64
	ContextWindow int64
	Percent       *float64
}

type CompactOptions struct {
	CustomInstructions string
	OnComplete         func(result CompactionResult)
	OnError            func(err error)
}

type CompactionResult struct {
	Summary              string
	FirstKeptEntryID     string
	TokensBefore         int64
	EstimatedTokensAfter int64
	Details              any
	Usage                *Usage
	FromHook             bool
}

type SessionView interface {
	ID() string
	Path() string
	Cwd() string
	Name() string
	Messages() []Message
}

type ModelRegistry interface {
	Model() *Model
	Available() []Model
	Find(provider, id string) (m Model, ok bool)
}

type NewSessionOptions struct {
	ParentSession string
	Setup         func(sm SessionView) error
	WithSession   func(ctx CommandContext) error
}

type ForkOptions struct {
	Position    string
	WithSession func(ctx CommandContext) error
}

type TreeOptions struct {
	Summarize           bool
	CustomInstructions  string
	ReplaceInstructions bool
	Label               string
}

type SwitchOptions struct {
	WithSession func(ctx CommandContext) error
}

type SessionSwitchResult struct {
	Cancelled bool
}
