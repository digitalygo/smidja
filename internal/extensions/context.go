package extensions

import (
	"context"

	"github.com/digitalygo/smidja/sdk"
)

type defaultContext struct {
	sdk.API
	signal context.Context
}

var _ sdk.HandlerContext = (*defaultContext)(nil)

func (c *defaultContext) UI() sdk.UI { return noopUI{} }

func (c *defaultContext) Mode() sdk.Mode { return sdk.ModePrint }

func (c *defaultContext) HasUI() bool { return false }

func (c *defaultContext) Cwd() string { return "" }

func (c *defaultContext) SessionManager() sdk.SessionView { return nil }

func (c *defaultContext) ModelRegistry() sdk.ModelRegistry { return nil }

func (c *defaultContext) Model() *sdk.Model { return nil }

func (c *defaultContext) ThinkingLevel() sdk.ThinkingLevel { return sdk.ThinkingOff }

func (c *defaultContext) Signal() context.Context { return c.signal }

func (c *defaultContext) Abort() {}

func (c *defaultContext) Shutdown() {}

func (c *defaultContext) ContextUsage() *sdk.ContextUsage { return nil }

func (c *defaultContext) Compact(sdk.CompactOptions) {}

func (c *defaultContext) SystemPrompt() string { return "" }

type noopUI struct{}

var _ sdk.UI = noopUI{}

func (noopUI) Notify(string, sdk.NotifyKind) {}

func (noopUI) Confirm(string, string) (bool, error) { return false, sdk.ErrModeUnsupported }

func (noopUI) Select(string, []string) (string, error) { return "", sdk.ErrModeUnsupported }

func (noopUI) Input(string, string) (string, error) { return "", sdk.ErrModeUnsupported }

func (noopUI) Editor(string, string) (string, error) { return "", sdk.ErrModeUnsupported }

func (noopUI) SetStatus(string, string) {}

func (noopUI) SetWidget(string, []string) {}

func (noopUI) SetWorkingMessage(string) {}

func (noopUI) SetTitle(string) {}
