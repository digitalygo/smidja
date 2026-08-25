package extensions

import (
	"context"

	"github.com/digitalygo/smidja/sdk"
)

// defaultContext is the v0 handler context the runtime builds around the
// host API when the host does not supply its own through SetContext. It
// exposes the API and the dispatch signal; the remaining run-state
// accessors return zero values until the wave that implements
// sdk.HandlerContext lands (UI, session, model registry, compaction).
//
// The default context behaves like a print-mode context: HasUI is false,
// Mode is ModePrint, and the UI dialogs return sdk.ErrModeUnsupported
// while the fire-and-forget UI methods are no-ops. Hosts that back the
// full surface should install their own context through SetContext.
type defaultContext struct {
	sdk.API
	signal context.Context
}

// Compile-time assertion that the default context satisfies the handler
// context contract.
var _ sdk.HandlerContext = (*defaultContext)(nil)

// UI returns a no-op UI with print-mode semantics.
func (c *defaultContext) UI() sdk.UI { return noopUI{} }

// Mode returns ModePrint: the default context cannot prompt the user.
func (c *defaultContext) Mode() sdk.Mode { return sdk.ModePrint }

// HasUI reports false: the default context has no dialog-capable UI.
func (c *defaultContext) HasUI() bool { return false }

// Cwd returns an empty working directory; the harness supplies the real
// value through its own context.
func (c *defaultContext) Cwd() string { return "" }

// SessionManager returns nil until the sessions wave lands.
func (c *defaultContext) SessionManager() sdk.SessionView { return nil }

// ModelRegistry returns nil until the model-registry wave lands.
func (c *defaultContext) ModelRegistry() sdk.ModelRegistry { return nil }

// Model returns nil until the model-registry wave lands.
func (c *defaultContext) Model() *sdk.Model { return nil }

// ThinkingLevel returns ThinkingOff until the model-registry wave lands.
func (c *defaultContext) ThinkingLevel() sdk.ThinkingLevel { return sdk.ThinkingOff }

// Signal returns the abort context of the current dispatch.
func (c *defaultContext) Signal() context.Context { return c.signal }

// Abort is a no-op in the default context.
func (c *defaultContext) Abort() {}

// Shutdown is a no-op in the default context.
func (c *defaultContext) Shutdown() {}

// ContextUsage returns nil until the context-management wave lands.
func (c *defaultContext) ContextUsage() *sdk.ContextUsage { return nil }

// Compact is a no-op until the context-management wave lands.
func (c *defaultContext) Compact(sdk.CompactOptions) {}

// SystemPrompt returns an empty string until the prompt wave lands.
func (c *defaultContext) SystemPrompt() string { return "" }

// noopUI is the UI of the default context, implementing the print-mode
// semantics the SDK freezes: dialogs return sdk.ErrModeUnsupported and
// the fire-and-forget methods are no-ops.
type noopUI struct{}

// Compile-time assertion that noopUI satisfies the UI contract.
var _ sdk.UI = noopUI{}

// Notify is a no-op.
func (noopUI) Notify(string, sdk.NotifyKind) {}

// Confirm reports that the operation is unsupported in this mode.
func (noopUI) Confirm(string, string) (bool, error) { return false, sdk.ErrModeUnsupported }

// Select reports that the operation is unsupported in this mode.
func (noopUI) Select(string, []string) (string, error) { return "", sdk.ErrModeUnsupported }

// Input reports that the operation is unsupported in this mode.
func (noopUI) Input(string, string) (string, error) { return "", sdk.ErrModeUnsupported }

// Editor reports that the operation is unsupported in this mode.
func (noopUI) Editor(string, string) (string, error) { return "", sdk.ErrModeUnsupported }

// SetStatus is a no-op.
func (noopUI) SetStatus(string, string) {}

// SetWidget is a no-op.
func (noopUI) SetWidget(string, []string) {}

// SetWorkingMessage is a no-op.
func (noopUI) SetWorkingMessage(string) {}

// SetTitle is a no-op.
func (noopUI) SetTitle(string) {}
