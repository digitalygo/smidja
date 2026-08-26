package providers

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

// Config parameterizes an OpenAICompletions driver. Every field is
// required in practice; the zero value yields a driver that fails with
// clear errors instead of panicking.
type Config struct {
	// BaseURL is the full chat/completions endpoint, for example
	// "https://openrouter.ai/api/v1/chat/completions".
	BaseURL string

	// DefaultHeaders are extra headers sent on every request, for
	// example the OpenRouter HTTP-Referer and X-Title identity headers.
	DefaultHeaders map[string]string

	// Auth returns the Bearer token for one request, resolving the
	// credential from the environment, the auth store, or a refreshed
	// OAuth token as the wiring evolves. It is called once per turn and
	// may be nil, in which case the Authorization header carries an
	// empty token.
	Auth func(ctx context.Context) (string, error)

	// ProviderID is the canonical provider identifier, for example
	// "deepseek". It lands in agent.AssistantMessage.Provider and
	// prefixes every error message the driver produces.
	ProviderID string

	// API is the API dialect identifier, for example "openai-completions".
	// It lands in agent.AssistantMessage.API.
	API string
}

// OpenAICompletions streams assistant turns from any provider that speaks
// the OpenAI chat completions protocol over SSE. It implements Driver (and
// therefore agent.Client) and is safe for concurrent use: all mutable
// state lives per turn.
type OpenAICompletions struct {
	baseURL    string
	headers    map[string]string
	auth       func(ctx context.Context) (string, error)
	providerID string
	api        string
	prefix     string
	http       *http.Client
}

// NewOpenAICompletions returns a driver for the given configuration. When
// httpClient is nil a default client is built whose transport carries
// generous dial and TLS handshake timeouts. The default client has no
// overall timeout: per-request cancellation is driven by the request
// context. A trailing slash on cfg.BaseURL is trimmed.
func NewOpenAICompletions(cfg Config, httpClient *http.Client) *OpenAICompletions {
	if httpClient == nil {
		httpClient = DefaultHTTPClient()
	}
	prefix := cfg.ProviderID
	if prefix == "" {
		prefix = "provider"
	}
	return &OpenAICompletions{
		baseURL:    strings.TrimRight(cfg.BaseURL, "/"),
		headers:    cfg.DefaultHeaders,
		auth:       cfg.Auth,
		providerID: cfg.ProviderID,
		api:        cfg.API,
		prefix:     prefix,
		http:       httpClient,
	}
}

// DefaultHTTPClient returns a client whose transport carries generous
// dial and TLS handshake timeouts and no overall timeout, so a stalled
// upstream never holds a turn open forever while long streams still work
// as intended. Drivers and compatibility facades share this constructor
// so nil-client callers observe the same default behavior.
func DefaultHTTPClient() *http.Client {
	return &http.Client{Transport: defaultTransport()}
}

// defaultTransport clones the standard transport and replaces the dial and
// TLS handshake timeouts with generous values.
func defaultTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DialContext = (&net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext
	t.TLSHandshakeTimeout = 10 * time.Second
	return t
}

// StreamTurn performs one assistant turn against the provider: it POSTs
// the request, streams the SSE response, delivers text and thinking
// deltas to the callbacks, and returns the completed assistant message.
// On failure it returns nil and an error; deltas already delivered to the
// callbacks stay delivered. It implements agent.Client.
func (d *OpenAICompletions) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	if req == nil {
		return nil, errors.New(d.prefix + ": nil turn request")
	}
	if req.Messages == nil {
		return nil, errors.New(d.prefix + ": nil messages")
	}

	payload, err := json.Marshal(ChatRequest{
		Model:         req.Model,
		Messages:      BuildMessages(req.System, req.Messages),
		Tools:         BuildTools(req.Tools),
		ToolChoice:    "auto",
		Stream:        true,
		StreamOptions: StreamOptions{IncludeUsage: true},
	})
	if err != nil {
		return nil, fmt.Errorf("%s: encode request: %w", d.prefix, err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, d.baseURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("%s: build request: %w", d.prefix, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	for k, v := range d.headers {
		httpReq.Header.Set(k, v)
	}
	// The resolved credential wins over any default header, so a driver
	// never sends a stale token.
	if d.auth != nil {
		token, err := d.auth(ctx)
		if err != nil {
			return nil, fmt.Errorf("%s: resolve credential: %w", d.prefix, err)
		}
		httpReq.Header.Set("Authorization", "Bearer "+token)
	} else {
		httpReq.Header.Set("Authorization", "Bearer ")
	}

	resp, err := d.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", d.prefix, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, d.responseError(resp)
	}

	return d.readStream(ctx, resp, req.Model, onText, onThinking)
}

// responseError builds an error from a non-2xx response, parsing the
// provider {error:{code,message,metadata}} envelope when present.
func (d *OpenAICompletions) responseError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var env WireErrorEnvelope
	if err := json.Unmarshal(body, &env); err == nil && env.Error.Message != "" {
		code := ""
		if env.Error.Code != "" {
			code = fmt.Sprintf(" (code %s)", env.Error.Code)
		}
		return fmt.Errorf("%s: %s: %s%s", d.prefix, resp.Status, env.Error.Message, code)
	}
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		msg = resp.Status
	}
	return fmt.Errorf("%s: %s: %s", d.prefix, resp.Status, msg)
}
