package openrouter

import (
	"context"
	"net/http"
	"strings"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/providers"
)

const (
	referer  = "https://github.com/digitalygo/smidja"
	appTitle = "smidja"
	apiField = "openai-completions"
	provider = "openrouter"
)

var _ agent.Client = (*Client)(nil)

type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
	driver  *providers.OpenAICompletions
}

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

func (c *Client) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	return c.driver.StreamTurn(ctx, req, onText, onThinking)
}
