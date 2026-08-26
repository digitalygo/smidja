package openrouter

import (
	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/providers"
)

func buildMessages(system string, messages []*agent.Message) []providers.WireMessage {
	return providers.BuildMessages(system, messages)
}

func buildTools(tools []agent.Tool) []providers.WireTool {
	return providers.BuildTools(tools)
}
