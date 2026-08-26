package agent

import "context"

type TurnRequest struct {
	Model string

	System string

	Messages []*Message

	Tools []Tool
}

type StreamHandler interface {
	OnText(delta string)

	OnThinking(delta string)
}

type Client interface {
	StreamTurn(ctx context.Context, req *TurnRequest, onText func(string), onThinking func(string)) (*AssistantMessage, error)
}
