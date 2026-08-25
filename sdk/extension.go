// Package sdk defines the public extension contract of the smidja harness:
// the Extension interface, the optional capability groups extensions opt
// into, the phase 1 hook registries, the handler context, the UI surface,
// and the bundle/entry types used by packaged builds.
//
// The contract mirrors Pi 0.84.2 (the installed @earendil-works/
// pi-coding-agent) capability for capability, with event names kept
// identical to Pi (context, message_end, auto_retry_start, tool_call,
// session_start, ...) so extensions port over with minimal renaming. The
// precise parity target is frozen in docs/sdk-parity-matrix.md; the
// dispositions there decide what v0 backs today versus what a later phase
// backs.
//
// The package is the public contract of the harness: it imports nothing
// outside the standard library, never references internal packages, and
// every exported symbol is documented. Extension authors compile against
// this package only.
package sdk

// Extension is the base contract every smidja extension implements. The
// ID must be a stable identifier unique across the extensions loaded into
// one harness run; the harness uses it for diagnostics, error attribution,
// and tool/command source metadata.
type Extension interface {
	// ID returns the extension's stable identifier.
	ID() string
}

// SetupHook is implemented by extensions that need one-time initialization
// against the harness API. Setup runs once before any session starts, in
// extension load order; api is the extension API used for registration
// (RegisterTool, RegisterCommand, RegisterFlag, RegisterProvider, ...).
//
// Returning an error fails the extension load. The harness logs the error
// and skips the extension, then continues with the remaining extensions,
// matching Pi's per-extension error isolation.
type SetupHook interface {
	// Setup initializes the extension with the harness API.
	Setup(api API) error
}

// LLMHook is implemented by extensions that hook the LLM turn cycle:
// context assembly (context event), finalized messages (message_end), and
// the automatic retry lifecycle (auto_retry_start, auto_retry_end).
type LLMHook interface {
	// RegisterLLMHooks registers the extension's LLM-cycle handlers.
	RegisterLLMHooks(r LLMHookRegistry)
}

// ToolHook is implemented by extensions that hook tool execution:
// pre-execution gating (tool_call, which can block or patch arguments) and
// result patching (tool_result).
type ToolHook interface {
	// RegisterToolHooks registers the extension's tool hooks.
	RegisterToolHooks(r ToolHookRegistry)
}

// SessionHook is implemented by extensions that hook the session
// lifecycle: session start and shutdown, for setup and cleanup work.
type SessionHook interface {
	// RegisterSessionHooks registers the extension's session hooks.
	RegisterSessionHooks(r SessionHookRegistry)
}
