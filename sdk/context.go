package sdk

import "context"

// API is the extension-facing action surface of the harness. It mirrors
// Pi's ExtensionAPI method groups one for one: tool registration and
// activation, command registration, message injection, session entry
// persistence, model and thinking-level control, provider registration,
// flags, shell execution, and the inter-extension event bus.
//
// The harness implements API and passes it to SetupHook.Setup; the same
// implementation is embedded in every HandlerContext, so handlers can call
// these methods directly.
type API interface {
	// RegisterTool registers a tool the model may call. Registering a
	// name that already exists replaces the previous tool, mirroring
	// Pi's tool-override behavior. Returns an error only for invalid
	// registrations (empty name).
	RegisterTool(t Tool) error

	// UnregisterTool removes a previously registered tool by name. It
	// has no effect when the name is not registered. Pi has no direct
	// equivalent; this is the symmetric removal added by smidja.
	UnregisterTool(name string) error

	// ActiveTools returns the names of the currently active tools.
	ActiveTools() []string

	// SetActiveTools replaces the active tool set by name. Names that
	// are not registered are ignored. Returns an error for an empty
	// active set when the harness requires at least one tool.
	SetActiveTools(names []string) error

	// AllTools returns metadata for every configured tool, built-in and
	// extension-registered.
	AllTools() []ToolInfo

	// RegisterCommand registers a slash command. Multiple extensions
	// registering the same name are kept with numeric invocation
	// suffixes, mirroring Pi.
	RegisterCommand(name string, cmd Command) error

	// Commands lists the invokable commands of the current session.
	Commands() []CommandInfo

	// SendMessage injects a custom message into the session. Custom
	// messages participate in LLM context.
	SendMessage(msg CustomMessage, opts SendOptions) error

	// SendUserMessage sends a user message to the agent, as if typed by
	// the user, and triggers a turn. When the agent is streaming,
	// SendOptions.DeliverAs selects the queueing mode.
	SendUserMessage(text string, opts SendOptions) error

	// AppendEntry persists extension state as a custom session entry.
	// Custom entries do not participate in LLM context; restore them on
	// the next session start through HandlerContext.SessionManager.
	AppendEntry(customType string, data any) error

	// SetSessionName sets the session display name. An empty name
	// clears it.
	SetSessionName(name string) error

	// LabelEntry sets a label on a session entry, or clears it when
	// label is empty. Labels are user-defined markers for bookmarking
	// and navigation.
	LabelEntry(entryID, label string) error

	// SetModel switches the active model. It returns an error when the
	// model is unknown or unusable (Pi returns false instead).
	SetModel(m Model) error

	// SetThinkingLevel sets the thinking level. Level clamping to model
	// capabilities is deferred to the model-registry wave; v0 stores and
	// passes the level through.
	SetThinkingLevel(level ThinkingLevel) error

	// RegisterProvider registers or overrides a provider. v0 backs the
	// OpenRouter-completions dialect; other dialects are deferred.
	RegisterProvider(name string, cfg ProviderConfig) error

	// RemoveProvider removes a previously registered provider and its
	// models. It has no effect when the provider is not registered.
	RemoveProvider(name string) error

	// RegisterFlag registers a CLI flag with its default value.
	RegisterFlag(name string, opts FlagOptions) error

	// Flags returns the current values of the registered flags, keyed
	// by name. Values are bool or string.
	Flags() map[string]any

	// Exec runs a shell command and returns its captured output.
	Exec(command string, args []string, opts ExecOptions) (*ExecResult, error)

	// EmitCustomEvent broadcasts an event on the inter-extension event
	// bus. Other extensions can observe it through their own event-bus
	// subscriptions.
	EmitCustomEvent(name string, data any) error
}

// HandlerContext is passed to every hook handler and tool execution. It
// embeds the API and adds per-handler access to the current run state:
// the UI, the run mode, the session, the model catalogue, the abort
// signal, and the context-management actions.
type HandlerContext interface {
	API

	// UI returns the user-interaction surface. In print mode the dialog
	// methods return ErrModeUnsupported and the fire-and-forget methods
	// are no-ops; check HasUI() before prompting.
	UI() UI

	// Mode returns the current run mode.
	Mode() Mode

	// HasUI reports whether dialog-capable UI is available. It is true
	// in interactive mode and false in print mode.
	HasUI() bool

	// Cwd returns the current working directory.
	Cwd() string

	// SessionManager returns the read-only view of the current session.
	SessionManager() SessionView

	// ModelRegistry returns the model catalogue.
	ModelRegistry() ModelRegistry

	// Model returns the active model; nil when none is set.
	Model() *Model

	// ThinkingLevel returns the current effective thinking level.
	ThinkingLevel() ThinkingLevel

	// Signal returns the abort context of the current agent operation:
	// a context cancelled when the user aborts the operation. It may be
	// nil when no agent turn is active, mirroring Pi's undefined
	// signal.
	Signal() context.Context

	// Abort cancels the current agent operation.
	Abort()

	// Shutdown requests a graceful shutdown of the harness. It emits
	// the session_shutdown event before exiting.
	Shutdown()

	// ContextUsage returns the estimated context usage for the active
	// model; nil when unknown.
	ContextUsage() *ContextUsage

	// Compact triggers compaction without blocking. Follow up through
	// CompactOptions.OnComplete and OnError.
	Compact(opts CompactOptions)

	// SystemPrompt returns the current effective system prompt.
	SystemPrompt() string
}

// CommandContext extends HandlerContext with session-control methods that
// are only safe to call from user-initiated commands: they can deadlock
// from event handlers, mirroring Pi's ExtensionCommandContext.
type CommandContext interface {
	HandlerContext

	// WaitForIdle blocks until the agent fully settles, including
	// automatic retries, auto-compaction retries, and queued
	// continuations.
	WaitForIdle() error

	// NewSession starts a new session, optionally recording a parent
	// session, initializing the new session, and running post-switch
	// work.
	NewSession(opts NewSessionOptions) (*SessionSwitchResult, error)

	// Fork forks from a specific session entry, creating a new session
	// file. Position "before" forks before the selected entry, "at"
	// duplicates the active path through it.
	Fork(entryID string, opts ForkOptions) (*SessionSwitchResult, error)

	// NavigateTree navigates to a different point in the session tree,
	// optionally summarizing the abandoned branch.
	NavigateTree(targetID string, opts TreeOptions) (*SessionSwitchResult, error)

	// SwitchSession switches to a different session file, optionally
	// running post-switch work.
	SwitchSession(path string, opts SwitchOptions) (*SessionSwitchResult, error)

	// Reload reloads extensions, skills, prompts, themes, and context
	// files. After it returns, treat the current handler as terminal.
	Reload() error
}
