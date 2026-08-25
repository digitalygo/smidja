package agent

import "context"

// TurnRequest describes one assistant turn: the model to call, the system
// prompt, the full conversation so far, and the tools the model may use.
type TurnRequest struct {
	// Model is the provider model identifier for this turn, for example
	// "anthropic/claude-sonnet-4.5".
	Model string

	// System is the system prompt. May be empty when the request carries
	// no system prompt.
	System string

	// Messages is the full conversation so far: user, assistant, and
	// toolResult messages in chronological order. It must not be nil and
	// must not be mutated by the client.
	Messages []*Message

	// Tools lists the tools the model may call during this turn. The
	// client serializes them into the provider's tool format.
	Tools []Tool
}

// StreamHandler is the callback seam for incremental assistant output. It
// is the canonical callback shape for stream consumers and is reserved for
// future expansion (for example tool-call delta callbacks in a later wave).
// The operative contract for this wave is Client.StreamTurn, which takes
// plain callbacks instead; StreamHandler is defined here so consumers and
// implementations share one documented shape when the expansion lands.
type StreamHandler interface {
	// OnText receives an incremental piece of text output. Deltas are
	// delivered sequentially, in arrival order; concatenating them
	// reproduces the full text.
	OnText(delta string)

	// OnThinking receives an incremental piece of thinking output, with
	// the same delivery guarantees as OnText.
	OnThinking(delta string)
}

// Client streams assistant turns from a provider. The loop, session, and
// CLI packages depend on this seam; keep the interface narrow and stable.
type Client interface {
	// StreamTurn performs one assistant turn against the provider.
	//
	// The request carries the model, system prompt, conversation, and
	// tools. onText and onThinking receive incremental output as it
	// arrives, sequentially and in arrival order; both may be nil to
	// discard that kind of output. The provider's tool-call deltas are
	// accumulated internally, never surfaced through callbacks in this
	// wave.
	//
	// On success it returns the completed AssistantMessage: the
	// accumulated content blocks (text, thinking, and toolCall blocks),
	// the usage accounting, and a StopReason of "stop" or "toolUse".
	// On failure it returns nil and an error describing the transport or
	// protocol problem; deltas already delivered to the callbacks remain
	// delivered.
	StreamTurn(ctx context.Context, req *TurnRequest, onText func(string), onThinking func(string)) (*AssistantMessage, error)
}
