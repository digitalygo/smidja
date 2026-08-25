package sdk

import (
	"context"
	"encoding/json"
	"time"
)

// ThinkingLevel mirrors Pi's thinking levels. Smidja v0 stores and passes
// the level through; model-capability clamping (Pi clamps to the model's
// supported levels) is deferred to the model-registry wave.
type ThinkingLevel string

// Thinking levels, in increasing verbosity. "off" disables thinking.
const (
	ThinkingOff     ThinkingLevel = "off"
	ThinkingMinimal ThinkingLevel = "minimal"
	ThinkingLow     ThinkingLevel = "low"
	ThinkingMedium  ThinkingLevel = "medium"
	ThinkingHigh    ThinkingLevel = "high"
	ThinkingXHigh   ThinkingLevel = "xhigh"
	ThinkingMax     ThinkingLevel = "max"
)

// Model is the provider model descriptor. It carries only the fields
// smidja v0 can actually back today: the identifier, the display name, and
// the provider. Richer metadata (context window, max tokens, reasoning
// support, cost rates) is deferred to the model-registry wave; zero values
// mean unknown there.
type Model struct {
	// ID is the provider model identifier used in requests, for example
	// "anthropic/claude-sonnet-4.5".
	ID string

	// Name is the display name, for example "Claude Sonnet 4.5". Empty
	// when no display name is known.
	Name string

	// Provider is the provider identifier, for example "openrouter".
	Provider string
}

// Cost is the per-million-token cost accounting of one turn, mirroring
// the smidja session cost shape.
type Cost struct {
	// Input is the input token cost in USD.
	Input float64
	// Output is the output token cost in USD.
	Output float64
	// CacheRead is the cache-read token cost in USD.
	CacheRead float64
	// CacheWrite is the cache-write token cost in USD.
	CacheWrite float64
	// Total is the total cost of the turn in USD.
	Total float64
}

// Usage is the token accounting of one assistant turn or nested tool
// model call, mirroring the smidja session usage shape.
type Usage struct {
	// Input is the number of input tokens.
	Input int64
	// Output is the number of output tokens.
	Output int64
	// CacheRead is the number of tokens read from the provider cache.
	CacheRead int64
	// CacheWrite is the number of tokens written to the provider cache.
	CacheWrite int64
	// Reasoning is the number of reasoning tokens, when the provider
	// reports them separately. Zero when absent.
	Reasoning int64
	// TotalTokens is the total number of tokens consumed.
	TotalTokens int64
	// Cost is the monetary cost of the turn.
	Cost Cost
}

// Block is one content block of a message. Type discriminates the block:
// "text" blocks carry Text, "thinking" blocks carry Thinking, and
// "toolCall" blocks carry ID, Name, and Arguments.
type Block struct {
	// Type is "text", "thinking", or "toolCall".
	Type string
	// Text holds the block's text for text blocks.
	Text string
	// Thinking holds the block's reasoning for thinking blocks.
	Thinking string
	// ID is the tool call identifier for toolCall blocks.
	ID string
	// Name is the tool name for toolCall blocks.
	Name string
	// Arguments is the raw JSON object of the tool arguments for
	// toolCall blocks.
	Arguments json.RawMessage
}

// Message is the public message shape of the SDK contract. The harness
// converts between this shape and its internal session representation at
// the extension boundary.
type Message struct {
	// Role is "user", "assistant", or "toolResult".
	Role string
	// Content is the message's content blocks.
	Content []Block
	// Usage is the token accounting for assistant messages; nil for
	// other roles and when unknown.
	Usage *Usage
}

// Tool is the contract every smidja tool implements. Extension-registered
// tools satisfy this interface; the harness adapts them into its internal
// tool seam.
type Tool interface {
	// Name returns the tool's canonical name used in toolCall blocks and
	// in the provider's tool list. Names must be stable identifiers.
	Name() string

	// Description returns a human-readable description shown to the
	// model so it can decide when and how to call the tool.
	Description() string

	// Schema returns the JSON schema of the tool's parameters as a raw
	// JSON object, in the provider's expected schema dialect.
	Schema() json.RawMessage

	// Exec runs the tool with the given arguments and returns its
	// result. The context carries cancellation and timeouts; Exec must
	// respect ctx.Done() in long-running work.
	Exec(ctx context.Context, args json.RawMessage) Result
}

// Result is the outcome of one tool execution.
type Result struct {
	// Content holds the output as text blocks (and thinking blocks when
	// a tool produces reasoning).
	Content []Block
	// Details carries extension-specific structured data for rendering
	// and state reconstruction; it is never sent to the model.
	Details any
	// IsError marks the execution as failed; Content then describes the
	// error.
	IsError bool
	// Usage reports nested model usage when the tool made LLM calls; nil
	// when none.
	Usage *Usage
}

// ToolInfo is the read-only metadata of one configured tool, as returned
// by API.AllTools.
type ToolInfo struct {
	// Name is the tool's canonical name.
	Name string
	// Description is the model-facing description.
	Description string
	// Schema is the parameter schema as a raw JSON object.
	Schema json.RawMessage
	// Source names where the tool came from: "builtin", or
	// "extension:<extension id>".
	Source string
}

// Command is one extension-registered slash command.
type Command struct {
	// Description is shown in command listings.
	Description string
	// Handler runs the command with the raw argument string and a
	// command context. Returning an error surfaces it to the user.
	Handler func(ctx CommandContext, args string) error
}

// CommandInfo is the read-only metadata of one invokable command, as
// returned by API.Commands.
type CommandInfo struct {
	// Name is the invokable command name without the leading slash.
	Name string
	// Description is the command description, when provided.
	Description string
}

// CustomMessage is a custom message injected into the session through
// API.SendMessage. Custom messages participate in LLM context, unlike
// custom entries (API.AppendEntry).
type CustomMessage struct {
	// Type is the extension-defined custom type, used to group and
	// render the message.
	Type string
	// Content is the message text.
	Content string
	// Display controls whether the message is shown in the transcript.
	Display bool
	// Details carries extension-specific metadata that is not sent to
	// the model.
	Details any
}

// DeliveryMode selects how an injected message is delivered while the
// agent is streaming, mirroring Pi's deliverAs modes.
type DeliveryMode string

// Delivery modes.
const (
	// DeliverySteer queues the message while streaming; it is delivered
	// after the current assistant turn finishes executing its tool calls
	// and before the next LLM call.
	DeliverySteer DeliveryMode = "steer"
	// DeliveryFollowUp waits for the agent to finish; the message is
	// delivered only when the agent has no more tool calls.
	DeliveryFollowUp DeliveryMode = "followUp"
	// DeliveryNextTurn queues the message for the next user prompt; it
	// does not interrupt or trigger anything.
	DeliveryNextTurn DeliveryMode = "nextTurn"
)

// SendOptions carries the delivery options of API.SendMessage and
// API.SendUserMessage.
type SendOptions struct {
	// DeliverAs selects the delivery mode. The zero value means
	// DeliveryNextTurn for SendMessage, and immediate delivery for
	// SendUserMessage when the agent is idle.
	DeliverAs DeliveryMode

	// TriggerTurn, when true, starts an LLM response if the agent is
	// idle. Only applies to DeliverySteer and DeliveryFollowUp;
	// ignored for DeliveryNextTurn (SendMessage only).
	TriggerTurn bool

	// ExpandPromptTemplates dispatches extension commands and expands
	// skill and prompt templates (SendUserMessage only).
	ExpandPromptTemplates bool
}

// ProviderConfig is the registration payload of API.RegisterProvider,
// mirroring Pi's ProviderConfig for the fields smidja v0 models.
type ProviderConfig struct {
	// Name is the display name of the provider.
	Name string
	// BaseURL is the API endpoint URL. Required when Models is
	// non-empty.
	BaseURL string
	// APIKey is the key literal, an environment reference such as
	// "$VAR", or empty for providers without a key.
	APIKey string
	// API is the API dialect, for example "openai-completions". v0
	// backs the OpenRouter-completions dialect; other dialects are
	// deferred.
	API string
	// Models replaces the provider's model list when non-empty.
	Models []Model
}

// FlagOptions is the registration payload of API.RegisterFlag, mirroring
// Pi's registerFlag options.
type FlagOptions struct {
	// Description is shown in help output.
	Description string
	// Type is "boolean" or "string".
	Type string
	// Default is the default value: a bool for boolean flags, a string
	// for string flags.
	Default any
}

// ExecOptions bounds one API.Exec call.
type ExecOptions struct {
	// Timeout bounds the execution; zero means no timeout.
	Timeout time.Duration
}

// ExecResult is the outcome of one API.Exec call.
type ExecResult struct {
	// Stdout is the captured standard output.
	Stdout string
	// Stderr is the captured standard error.
	Stderr string
	// Code is the process exit code; -1 when the process was killed.
	Code int
	// Killed reports whether the process was terminated by a timeout or
	// cancellation.
	Killed bool
}

// ContextUsage is the estimated context usage for the active model,
// mirroring Pi's ContextUsage. Values that v0 cannot determine stay zero
// or nil.
type ContextUsage struct {
	// Tokens is the estimated context size in tokens; nil when unknown
	// (for example right after compaction, before the next LLM
	// response).
	Tokens *int64
	// ContextWindow is the active model's context window in tokens;
	// zero when unknown (v0 does not carry model metadata yet).
	ContextWindow int64
	// Percent is the usage as a fraction of the context window; nil
	// when Tokens or ContextWindow is unknown.
	Percent *float64
}

// CompactOptions configures one API compaction request.
type CompactOptions struct {
	// CustomInstructions guides the compaction summarizer.
	CustomInstructions string
	// OnComplete runs with the compaction result after completion.
	OnComplete func(result CompactionResult)
	// OnError runs with the failure when compaction fails.
	OnError func(err error)
}

// CompactionResult describes one completed compaction, mirroring Pi's
// CompactionResult and the fields of a compaction session entry
// (summary, firstKeptEntryId, tokensBefore, details, usage; fromHook is
// set when an extension produced it).
type CompactionResult struct {
	// Summary is the compaction summary text.
	Summary string
	// FirstKeptEntryID is the session entry id of the first kept entry.
	FirstKeptEntryID string
	// TokensBefore is the context size in tokens before compaction.
	TokensBefore int64
	// EstimatedTokensAfter is the estimated context size after
	// compaction; zero when not estimated.
	EstimatedTokensAfter int64
	// Details carries extension-specific data, when provided.
	Details any
	// Usage reports the tokens of the summarizer LLM call, when
	// available.
	Usage *Usage
	// FromHook marks the compaction as produced by an extension.
	FromHook bool
}

// SessionView is the read-only view of the current session exposed to
// extensions. It mirrors the subset of Pi's ReadonlySessionManager that
// smidja v0 can back with its JSONL session store: identity, location,
// display name, and the branch messages. Entry-level access (labels,
// trees, per-entry metadata) is deferred to the sessions wave.
type SessionView interface {
	// ID returns the session identifier.
	ID() string
	// Path returns the session file path; empty for an ephemeral
	// session.
	Path() string
	// Cwd returns the working directory the session was started in.
	Cwd() string
	// Name returns the session display name; empty when unset.
	Name() string
	// Messages returns the session messages of the current branch in
	// chronological order.
	Messages() []Message
}

// ModelRegistry is the read side of the model catalogue. v0 backs a
// single registered model (the configured one); Find and Available
// consult that catalogue.
type ModelRegistry interface {
	// Model returns the currently active model; nil when none is set.
	Model() *Model
	// Available lists the registered models in registration order.
	Available() []Model
	// Find looks up a model by provider and id; ok is false when
	// unknown.
	Find(provider, id string) (m Model, ok bool)
}

// NewSessionOptions configures CommandContext.NewSession.
type NewSessionOptions struct {
	// ParentSession is the parent session file recorded in the new
	// session header.
	ParentSession string
	// Setup mutates the new session before WithSession runs.
	Setup func(sm SessionView) error
	// WithSession runs after the switch, against the replacement
	// session context. Do not use the old context inside it.
	WithSession func(ctx CommandContext) error
}

// ForkOptions configures CommandContext.Fork.
type ForkOptions struct {
	// Position is "before" (default) to fork before the selected entry,
	// or "at" to duplicate the active path through the entry.
	Position string
	// WithSession runs after the fork, against the replacement session
	// context.
	WithSession func(ctx CommandContext) error
}

// TreeOptions configures CommandContext.NavigateTree.
type TreeOptions struct {
	// Summarize generates a summary of the abandoned branch.
	Summarize bool
	// CustomInstructions guides the summarizer.
	CustomInstructions string
	// ReplaceInstructions, when true, replaces the default summary
	// prompt entirely instead of appending.
	ReplaceInstructions bool
	// Label attaches to the branch summary entry (or the target entry
	// when not summarizing).
	Label string
}

// SwitchOptions configures CommandContext.SwitchSession.
type SwitchOptions struct {
	// WithSession runs after the switch, against the replacement
	// session context.
	WithSession func(ctx CommandContext) error
}

// SessionSwitchResult is the outcome of a session-control command. The
// harness reports Cancelled when an extension cancelled the operation.
type SessionSwitchResult struct {
	// Cancelled reports whether the operation was cancelled.
	Cancelled bool
}
