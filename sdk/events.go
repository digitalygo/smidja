package sdk

import "encoding/json"

const (
	EventContext         = "context"
	EventMessageEnd      = "message_end"
	EventAutoRetryStart  = "auto_retry_start"
	EventAutoRetryEnd    = "auto_retry_end"
	EventToolCall        = "tool_call"
	EventToolResult      = "tool_result"
	EventSessionStart    = "session_start"
	EventSessionShutdown = "session_shutdown"
)

type ContextEvent struct {
	Messages []Message
}

type ContextEventResult struct {
	Messages []Message
}

type MessageEndEvent struct {
	Message Message
}

type MessageEndEventResult struct {
	Message Message
}

type AutoRetryStartEvent struct {
	Attempt      int
	MaxAttempts  int
	DelayMs      int64
	ErrorMessage string
}

type AutoRetryEndEvent struct {
	Success    bool
	Attempt    int
	FinalError string
}

type ToolCallEvent struct {
	ToolCallID string
	Name       string
	Args       json.RawMessage
}

type ToolCallDecision struct {
	Block     bool
	Reason    string
	FinalArgs json.RawMessage
}

type ToolResultEvent struct {
	ToolCallID string
	Name       string
	Args       json.RawMessage
	Content    []Block
	Details    any
	IsError    bool
	Usage      *Usage
}

type ToolResultEventResult struct {
	Content []Block
	Details any
	IsError *bool
	Usage   *Usage
}

type SessionStartReason string

const (
	SessionStartStartup SessionStartReason = "startup"
	SessionStartReload  SessionStartReason = "reload"
	SessionStartNew     SessionStartReason = "new"
	SessionStartResume  SessionStartReason = "resume"
	SessionStartFork    SessionStartReason = "fork"
)

type SessionStartEvent struct {
	Reason              SessionStartReason
	PreviousSessionFile string
}

type SessionShutdownReason string

const (
	SessionShutdownQuit   SessionShutdownReason = "quit"
	SessionShutdownReload SessionShutdownReason = "reload"
	SessionShutdownNew    SessionShutdownReason = "new"
	SessionShutdownResume SessionShutdownReason = "resume"
	SessionShutdownFork   SessionShutdownReason = "fork"
)

type SessionShutdownEvent struct {
	Reason            SessionShutdownReason
	TargetSessionFile string
}
