package ui

import (
	"context"

	"github.com/digitalygo/smidja/sdk"
)

type execHandlerContext struct {
	sdk.HandlerContext
	signal context.Context
	ui     sdk.UI
	mode   sdk.Mode
}

var _ sdk.HandlerContext = (*execHandlerContext)(nil)

func NewHandlerContext(base sdk.HandlerContext, signal context.Context, ui sdk.UI, mode sdk.Mode) sdk.HandlerContext {
	if signal == nil {
		signal = context.Background()
	}
	return &execHandlerContext{HandlerContext: base, signal: signal, ui: ui, mode: mode}
}

func (c *execHandlerContext) UI() sdk.UI { return c.ui }

func (c *execHandlerContext) Mode() sdk.Mode { return c.mode }

func (c *execHandlerContext) HasUI() bool { return true }

func (c *execHandlerContext) Signal() context.Context {
	if c.signal != nil {
		return c.signal
	}
	if c.HandlerContext == nil {
		return context.Background()
	}
	if provided := c.HandlerContext.Signal(); provided != nil {
		return provided
	}
	return context.Background()
}

func (r *Runner) InteractiveHandlerContext(signal context.Context, base sdk.HandlerContext) sdk.HandlerContext {
	if signal == nil {
		if base != nil {
			if provided := base.Signal(); provided != nil {
				signal = provided
			}
		}
		if signal == nil {
			signal = context.Background()
		}
	}
	return NewHandlerContext(base, signal, r.BoundUI(signal), sdk.ModeInteractive)
}
