package sdk

import "context"

type API interface {
	RegisterTool(t Tool) error

	UnregisterTool(name string) error

	ActiveTools() []string

	SetActiveTools(names []string) error

	AllTools() []ToolInfo

	ConfigValue(key string) string

	RegisterCommand(name string, cmd Command) error

	Commands() []CommandInfo

	SendMessage(msg CustomMessage, opts SendOptions) error

	SendUserMessage(text string, opts SendOptions) error

	AppendEntry(customType string, data any) error

	SetSessionName(name string) error

	LabelEntry(entryID, label string) error

	SetModel(m Model) error

	SetThinkingLevel(level ThinkingLevel) error

	RegisterProvider(name string, cfg ProviderConfig) error

	RemoveProvider(name string) error

	RegisterFlag(name string, opts FlagOptions) error

	Flags() map[string]any

	Exec(command string, args []string, opts ExecOptions) (*ExecResult, error)

	EmitCustomEvent(name string, data any) error
}

type HandlerContext interface {
	API

	UI() UI

	Mode() Mode

	HasUI() bool

	Cwd() string

	SessionManager() SessionView

	ModelRegistry() ModelRegistry

	Model() *Model

	ThinkingLevel() ThinkingLevel

	Signal() context.Context

	Abort()

	Shutdown()

	ContextUsage() *ContextUsage

	Compact(opts CompactOptions)

	SystemPrompt() string
}

type CommandContext interface {
	HandlerContext

	WaitForIdle() error

	NewSession(opts NewSessionOptions) (*SessionSwitchResult, error)

	Fork(entryID string, opts ForkOptions) (*SessionSwitchResult, error)

	NavigateTree(targetID string, opts TreeOptions) (*SessionSwitchResult, error)

	SwitchSession(path string, opts SwitchOptions) (*SessionSwitchResult, error)

	Reload() error
}
