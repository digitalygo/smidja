package cli

import (
	"context"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/openrouter"
)

type hostClient struct {
	base agent.Client
	host *hostRuntime
	seam bool
}

var _ agent.Client = (*hostClient)(nil)

func newHostClient(base agent.Client, host *hostRuntime, seam bool) agent.Client {
	if base == nil {
		return nil
	}
	return &hostClient{base: base, host: host, seam: seam}
}

func (c *hostClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	if c.seam && c.host != nil {
		if directive, ok := c.host.reasoningDirective(); ok {
			ctx = openrouter.WithReasoning(ctx, directive)
		}
	}
	return c.base.StreamTurn(ctx, req, onText, onThinking)
}

func isOpenRouterClient(client agent.Client) bool {
	_, ok := client.(*openrouter.Client)
	return ok
}
