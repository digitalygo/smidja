// Package agent defines the core conversation model and the narrow provider
// and tool contracts the rest of smidja builds on: message types with exact
// JSON shapes for session persistence, the tool interface, and the streaming
// client seam. Other packages (session, openrouter, tools, loop) depend on
// these contracts, so the JSON tags and signatures in this package are part
// of the public surface and must not change without a coordinated update.
package agent

import (
	"encoding/json"
	"time"
)

// Role identifies the author of a message in a session.
type Role string

// Message roles used across smidja. The UserMessage, AssistantMessage, and
// ToolResultMessage structs carry the same values as plain strings in their
// Role fields; these constants are the canonical spellings.
const (
	RoleUser       Role = "user"
	RoleAssistant  Role = "assistant"
	RoleToolResult Role = "toolResult"
)

// Content block type constants for ContentBlock.Type.
const (
	// BlockTypeText is a plain text block.
	BlockTypeText = "text"
	// BlockTypeThinking is a reasoning block (model thinking).
	BlockTypeThinking = "thinking"
	// BlockTypeToolCall is a tool invocation block.
	BlockTypeToolCall = "toolCall"
)

// ContentBlock is one element of a message's content. The type discriminates
// the block: text blocks carry Text, thinking blocks carry Thinking (and
// optionally ThinkingSignature and Redacted), and toolCall blocks carry ID,
// Name, and Arguments.
type ContentBlock struct {
	// Type is "text", "thinking", or "toolCall".
	Type string `json:"type"`
	// Text holds the block's text for text blocks.
	Text string `json:"text,omitempty"`
	// Thinking holds the block's reasoning for thinking blocks.
	Thinking string `json:"thinking,omitempty"`
	// ThinkingSignature is the provider's signature for the thinking
	// content, used for redacted-thinking verification. Usually empty.
	ThinkingSignature string `json:"thinkingSignature,omitempty"`
	// Redacted marks a thinking block whose content the provider withheld.
	Redacted bool `json:"redacted,omitempty"`
	// ID is the tool call identifier for toolCall blocks; the matching
	// ToolResultMessage references it via ToolCallID.
	ID string `json:"id,omitempty"`
	// Name is the tool name for toolCall blocks.
	Name string `json:"name,omitempty"`
	// Arguments is the raw JSON object of the tool arguments for toolCall
	// blocks.
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// Usage is the token accounting of one assistant turn.
type Usage struct {
	// Input is the number of input tokens.
	Input int64 `json:"input"`
	// Output is the number of output tokens.
	Output int64 `json:"output"`
	// CacheRead is the number of tokens read from the provider cache.
	CacheRead int64 `json:"cacheRead"`
	// CacheWrite is the number of tokens written to the provider cache.
	CacheWrite int64 `json:"cacheWrite"`
	// Reasoning is the number of reasoning tokens, when the provider
	// reports them separately. Zero when absent.
	Reasoning int64 `json:"reasoning,omitempty"`
	// TotalTokens is the total number of tokens consumed.
	TotalTokens int64 `json:"totalTokens"`
	// Cost is the monetary cost of the turn, when the provider reports it.
	Cost Cost `json:"cost"`
}

// Cost is the monetary cost of one assistant turn.
type Cost struct {
	// Input is the cost of input tokens in USD.
	Input float64 `json:"input"`
	// Output is the cost of output tokens in USD.
	Output float64 `json:"output"`
	// CacheRead is the cost of cache-read tokens in USD.
	CacheRead float64 `json:"cacheRead"`
	// CacheWrite is the cost of cache-write tokens in USD.
	CacheWrite float64 `json:"cacheWrite"`
	// Total is the total cost of the turn in USD.
	Total float64 `json:"total"`
}

// UserMessage is a message authored by the user (or by the harness acting
// on the user's behalf). Content is a raw JSON value: either a JSON string
// or an array of content blocks.
type UserMessage struct {
	// Role is always "user".
	Role string `json:"role"`
	// Content is a JSON string or an array of content blocks.
	Content json.RawMessage `json:"content"`
	// Timestamp is the message creation time in epoch milliseconds.
	Timestamp int64 `json:"timestamp"`
}

// AssistantMessage is a message authored by the model, produced by one
// assistant turn. It carries the full content block list (text, thinking,
// and toolCall blocks), the usage accounting, and the reason the turn
// stopped.
type AssistantMessage struct {
	// Role is always "assistant".
	Role string `json:"role"`
	// Content is the model's output blocks for the turn.
	Content []ContentBlock `json:"content"`
	// API is the API shape used, currently "openai-completions".
	API string `json:"api"`
	// Provider is the provider used, currently "openrouter".
	Provider string `json:"provider"`
	// Model is the provider model identifier that produced the turn.
	Model string `json:"model"`
	// ResponseID is the provider's response identifier, when available.
	ResponseID string `json:"responseId,omitempty"`
	// Usage is the token and cost accounting of the turn.
	Usage Usage `json:"usage"`
	// StopReason is "stop", "toolUse", "error", or "aborted".
	StopReason string `json:"stopReason"`
	// ErrorMessage describes the failure when StopReason is "error".
	ErrorMessage string `json:"errorMessage,omitempty"`
	// Timestamp is the message creation time in epoch milliseconds.
	Timestamp int64 `json:"timestamp"`
}

// ToolResultMessage is the harness's report of one tool execution, feeding
// the result back to the model.
type ToolResultMessage struct {
	// Role is always "toolResult".
	Role string `json:"role"`
	// ToolCallID references the toolCall block (ContentBlock.ID) of the
	// assistant message that requested the execution.
	ToolCallID string `json:"toolCallId"`
	// ToolName is the name of the executed tool.
	ToolName string `json:"toolName"`
	// Content holds the tool output as text blocks only.
	Content []ContentBlock `json:"content"`
	// IsError marks the execution as failed; Content then describes the
	// error.
	IsError bool `json:"isError"`
	// Timestamp is the message creation time in epoch milliseconds.
	Timestamp int64 `json:"timestamp"`
}

// Message is the tagged union persisted in sessions and converted for the
// provider. Exactly one of User, Assistant, or ToolResult is set.
type Message struct {
	// User is set for user messages.
	User *UserMessage
	// Assistant is set for assistant messages.
	Assistant *AssistantMessage
	// ToolResult is set for tool result messages.
	ToolResult *ToolResultMessage
}

// Role returns the message's role: "user", "assistant", or "toolResult".
// It returns "" when no variant is set; when more than one variant is set
// (a construction error), it returns the first non-nil role in the order
// User, Assistant, ToolResult.
func (m *Message) Role() string {
	switch {
	case m.User != nil:
		return string(RoleUser)
	case m.Assistant != nil:
		return string(RoleAssistant)
	case m.ToolResult != nil:
		return string(RoleToolResult)
	default:
		return ""
	}
}

// NowMillis returns the current time in epoch milliseconds. Use it
// everywhere a timestamp is recorded so all messages share one clock.
func NowMillis() int64 {
	return time.Now().UnixMilli()
}
