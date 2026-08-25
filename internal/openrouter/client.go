// Package openrouter implements the agent.Client seam against the
// OpenRouter chat completions API: an OpenAI-compatible streaming endpoint
// reached through SSE. It converts the smidja conversation model into wire
// messages, streams deltas back into content blocks, and accumulates usage
// and stop reasons.
package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
)

// Headers that identify the smidja harness to OpenRouter.
const (
	referer  = "https://github.com/digitalygo/smidja"
	appTitle = "smidja"
	apiField = "openai-completions"
	provider = "openrouter"
)

// jsonNull is the JSON literal used for assistant content with no text.
var jsonNull = json.RawMessage("null")

// Compile-time assertion that Client implements agent.Client.
var _ agent.Client = (*Client)(nil)

// Client streams assistant turns from OpenRouter. It implements
// agent.Client and is safe for concurrent use.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// New returns a Client for the given base URL and API key. When httpClient
// is nil a default client is built whose transport carries generous dial
// and TLS handshake timeouts. The default client has no overall timeout:
// per-request cancellation is driven by the request context.
func New(baseURL, apiKey string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Transport: defaultTransport()}
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http:    httpClient,
	}
}

// defaultTransport clones the standard transport and replaces the dial and
// TLS handshake timeouts with generous values, so a stalled upstream never
// holds a turn open forever while long streams still work as intended.
func defaultTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DialContext = (&net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext
	t.TLSHandshakeTimeout = 10 * time.Second
	return t
}

// StreamTurn performs one assistant turn against OpenRouter: it POSTs the
// request, streams the SSE response, delivers text and thinking deltas to
// the callbacks, and returns the completed assistant message. On failure it
// returns nil and an error; deltas already delivered to the callbacks stay
// delivered.
func (c *Client) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	if req == nil {
		return nil, errors.New("openrouter: nil turn request")
	}
	if req.Messages == nil {
		return nil, errors.New("openrouter: nil messages")
	}

	payload, err := json.Marshal(chatRequest{
		Model:         req.Model,
		Messages:      buildMessages(req.System, req.Messages),
		Tools:         buildTools(req.Tools),
		ToolChoice:    "auto",
		Stream:        true,
		StreamOptions: streamOptions{IncludeUsage: true},
	})
	if err != nil {
		return nil, fmt.Errorf("openrouter: encode request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("openrouter: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("HTTP-Referer", referer)
	httpReq.Header.Set("X-Title", appTitle)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openrouter: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, responseError(resp)
	}

	return readStream(ctx, resp, req.Model, onText, onThinking)
}

// buildMessages converts the conversation into wire messages, prepending
// the system prompt as the first message when it is non-empty.
func buildMessages(system string, messages []*agent.Message) []wireMessage {
	out := make([]wireMessage, 0, len(messages)+1)
	if system != "" {
		out = append(out, wireMessage{Role: "system", Content: jsonString(system)})
	}
	for _, m := range messages {
		switch {
		case m.User != nil:
			out = append(out, wireMessage{Role: "user", Content: userContent(m.User.Content)})
		case m.Assistant != nil:
			out = append(out, assistantContent(m.Assistant))
		case m.ToolResult != nil:
			out = append(out, toolResultContent(m.ToolResult))
		}
	}
	return out
}

// userContent renders a user message's raw content: JSON strings pass
// through verbatim, content-block arrays are flattened into text parts.
func userContent(raw json.RawMessage) json.RawMessage {
	if isJSONString(raw) {
		return raw
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil || blocks == nil {
		// Neither a string nor a block array: send it verbatim and let
		// the provider surface the problem.
		return raw
	}
	parts := make([]wirePart, 0, len(blocks))
	for _, b := range blocks {
		parts = append(parts, wirePart{Type: "text", Text: b.Text})
	}
	if b, err := json.Marshal(parts); err == nil {
		return b
	}
	return raw
}

// isJSONString reports whether raw is a JSON string literal.
func isJSONString(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) > 0 && t[0] == '"'
}

// assistantContent renders an assistant message: the joined text of its
// text blocks (or null) and its tool calls as OpenAI function-call wire
// objects.
func assistantContent(a *agent.AssistantMessage) wireMessage {
	w := wireMessage{Role: "assistant"}
	var text strings.Builder
	for _, b := range a.Content {
		switch b.Type {
		case agent.BlockTypeText:
			text.WriteString(b.Text)
		case agent.BlockTypeToolCall:
			w.ToolCalls = append(w.ToolCalls, wireToolCall{
				ID:   b.ID,
				Type: "function",
				Function: wireFunctionCall{
					Name:      b.Name,
					Arguments: string(b.Arguments),
				},
			})
		}
	}
	if text.Len() > 0 {
		w.Content = jsonString(text.String())
	} else {
		w.Content = jsonNull
	}
	return w
}

// toolResultContent renders a tool result message with the concatenated
// text of its content blocks.
func toolResultContent(t *agent.ToolResultMessage) wireMessage {
	w := wireMessage{Role: "tool", ToolCallID: t.ToolCallID}
	var text strings.Builder
	for _, b := range t.Content {
		if b.Type == agent.BlockTypeText {
			text.WriteString(b.Text)
		}
	}
	w.Content = jsonString(text.String())
	return w
}

// buildTools converts agent tools into OpenAI function-tool wire objects.
// It returns nil for an empty tool list so the field is omitted.
func buildTools(tools []agent.Tool) []wireTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]wireTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, wireTool{
			Type: "function",
			Function: wireFunction{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  t.Schema(),
			},
		})
	}
	return out
}

// responseError builds an error from a non-2xx response, parsing the
// OpenRouter {error:{code,message,metadata}} envelope when present.
func responseError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var env wireErrorEnvelope
	if err := json.Unmarshal(body, &env); err == nil && env.Error.Message != "" {
		code := ""
		if env.Error.Code != "" {
			code = fmt.Sprintf(" (code %s)", env.Error.Code)
		}
		return fmt.Errorf("openrouter: %s: %s%s", resp.Status, env.Error.Message, code)
	}
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		msg = resp.Status
	}
	return fmt.Errorf("openrouter: %s: %s", resp.Status, msg)
}

// jsonString returns v as a JSON string literal.
func jsonString(v string) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
