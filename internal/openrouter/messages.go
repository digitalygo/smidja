package openrouter

import (
	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/providers"
)

// buildMessages converts the conversation into wire messages, prepending
// the system prompt as the first message when it is non-empty. It is the
// driver's conversion, kept here under the historical name.
func buildMessages(system string, messages []*agent.Message) []providers.WireMessage {
	return providers.BuildMessages(system, messages)
}

// buildTools converts agent tools into OpenAI function-tool wire objects.
// It returns nil for an empty tool list so the field is omitted. It is
// the driver's conversion, kept here under the historical name.
func buildTools(tools []agent.Tool) []providers.WireTool {
	return providers.BuildTools(tools)
}
