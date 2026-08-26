package sdk

type LLMHookRegistry interface {
	OnContext(handler ContextHandler)

	OnMessageEnd(handler MessageEndHandler)

	OnAutoRetryStart(handler AutoRetryStartHandler)

	OnAutoRetryEnd(handler AutoRetryEndHandler)
}

type ToolHookRegistry interface {
	OnToolCall(handler ToolCallHandler)

	OnToolResult(handler ToolResultHandler)
}

type SessionHookRegistry interface {
	OnSessionStart(handler SessionStartHandler)

	OnSessionShutdown(handler SessionShutdownHandler)
}

type ContextHandler func(ctx HandlerContext, event ContextEvent) (*ContextEventResult, error)

type MessageEndHandler func(ctx HandlerContext, event MessageEndEvent) (*MessageEndEventResult, error)

type AutoRetryStartHandler func(ctx HandlerContext, event AutoRetryStartEvent) error

type AutoRetryEndHandler func(ctx HandlerContext, event AutoRetryEndEvent) error

type ToolCallHandler func(ctx HandlerContext, event ToolCallEvent) (*ToolCallDecision, error)

type ToolResultHandler func(ctx HandlerContext, event ToolResultEvent) (*ToolResultEventResult, error)

type SessionStartHandler func(ctx HandlerContext, event SessionStartEvent) error

type SessionShutdownHandler func(ctx HandlerContext, event SessionShutdownEvent) error
