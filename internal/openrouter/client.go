// Package openrouter is the compatibility facade for the OpenRouter chat
// completions API. It keeps the historical constructor, client fields,
// identity headers, error prefixes, and api/provider identity while
// delegating all internals to the OpenAICompletions driver in
// internal/providers: request building, SSE parsing, tool-call
// accumulation, tolerant usage decoders, and error envelopes live in the
// driver. New provider wiring goes through internal/providers, not here.
package openrouter

import (
	"context"
	"net/http"
	"strings"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/providers"
)

// Headers that identify the smidja harness to OpenRouter.
const (
	referer  = "https://github.com/digitalygo/smidja"
	appTitle = "smidja"
	apiField = "openai-completions"
	provider = "openrouter"
)

// Compile-time assertion that Client implements agent.Client.
var _ agent.Client = (*Client)(nil)

// Client streams assistant turns from OpenRouter. It implements
// agent.Client, is safe for concurrent use, and delegates to an
// OpenAICompletions driver parameterized with the OpenRouter endpoint,
// identity headers, and api key.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
	driver  *providers.OpenAICompletions
}

// New returns a Client for the given base URL and API key. When httpClient
// is nil a default client is built whose transport carries generous dial
// and TLS handshake timeouts. The default client has no overall timeout:
// per-request cancellation is driven by the request context.
func New(baseURL, apiKey string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = providers.DefaultHTTPClient()
	}
	baseURL = strings.TrimRight(baseURL, "/")
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		http:    httpClient,
		driver: providers.NewOpenAICompletions(providers.Config{
			BaseURL: baseURL,
			DefaultHeaders: map[string]string{
				"HTTP-Referer": referer,
				"X-Title":      appTitle,
			},
			Auth: func(context.Context) (string, error) {
				return apiKey, nil
			},
			ProviderID: provider,
			API:        apiField,
		}, httpClient),
	}
}

// StreamTurn performs one assistant turn against OpenRouter: it POSTs the
// request, streams the SSE response, delivers text and thinking deltas to
// the callbacks, and returns the completed assistant message. On failure
// it returns nil and an error; deltas already delivered to the callbacks
// stay delivered. The driver prefixes every error with "openrouter:".
func (c *Client) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	return c.driver.StreamTurn(ctx, req, onText, onThinking)
}
