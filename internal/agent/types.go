package agent

import (
	"encoding/json"
	"time"
)

type Role string

const (
	RoleUser       Role = "user"
	RoleAssistant  Role = "assistant"
	RoleToolResult Role = "toolResult"
)

const (
	BlockTypeText     = "text"
	BlockTypeThinking = "thinking"
	BlockTypeToolCall = "toolCall"
)

type ContentBlock struct {
	Type              string          `json:"type"`
	Text              string          `json:"text,omitempty"`
	Thinking          string          `json:"thinking,omitempty"`
	ThinkingSignature string          `json:"thinkingSignature,omitempty"`
	Redacted          bool            `json:"redacted,omitempty"`
	ID                string          `json:"id,omitempty"`
	Name              string          `json:"name,omitempty"`
	Arguments         json.RawMessage `json:"arguments,omitempty"`
}

type Usage struct {
	Input       int64 `json:"input"`
	Output      int64 `json:"output"`
	CacheRead   int64 `json:"cacheRead"`
	CacheWrite  int64 `json:"cacheWrite"`
	Reasoning   int64 `json:"reasoning,omitempty"`
	TotalTokens int64 `json:"totalTokens"`
	Cost        Cost  `json:"cost"`
}

type Cost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
	Total      float64 `json:"total"`
}

type UserMessage struct {
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	Timestamp int64           `json:"timestamp"`
}

type AssistantMessage struct {
	Role         string         `json:"role"`
	Content      []ContentBlock `json:"content"`
	API          string         `json:"api"`
	Provider     string         `json:"provider"`
	Model        string         `json:"model"`
	ResponseID   string         `json:"responseId,omitempty"`
	Usage        Usage          `json:"usage"`
	StopReason   string         `json:"stopReason"`
	ErrorMessage string         `json:"errorMessage,omitempty"`
	Timestamp    int64          `json:"timestamp"`
}

type ToolResultMessage struct {
	Role       string         `json:"role"`
	ToolCallID string         `json:"toolCallId"`
	ToolName   string         `json:"toolName"`
	Content    []ContentBlock `json:"content"`
	IsError    bool           `json:"isError"`
	Timestamp  int64          `json:"timestamp"`
}

type Message struct {
	User       *UserMessage
	Assistant  *AssistantMessage
	ToolResult *ToolResultMessage
}

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

func NowMillis() int64 {
	return time.Now().UnixMilli()
}
