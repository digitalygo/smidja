package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/digitalygo/smidja/internal/agent"
)

// AnthropicAPI is the API dialect identifier for the Anthropic messages
// protocol. It lands in agent.AssistantMessage.API, mirroring
// "openai-completions" for the OpenAI dialect.
const AnthropicAPI = "anthropic-messages"

// DefaultAnthropicBaseURL is the default messages endpoint. Override it in
// AnthropicConfig.BaseURL for OAuth gateways and compatible providers.
const DefaultAnthropicBaseURL = "https://api.anthropic.com/v1/messages"

// anthropicVersion is the API version the driver speaks. The messages API
// requires this header on every request.
const anthropicVersion = "2023-06-01"

// defaultAnthropicMaxTokens is the output budget used when AnthropicConfig.MaxTokens
// is zero. The messages API requires max_tokens; Pi derives it from model
// metadata, which the driver does not have, so it falls back to a fixed
// budget.
const defaultAnthropicMaxTokens = 4096

// Claude Code identity markers for subscription (OAuth) auth, ported from
// Pi's createClient: the beta features subscription clients must advertise,
// the CLI user-agent, and the identity system prompt.
const (
	claudeCodeBeta          = "claude-code-20250219"
	oauthBeta               = "oauth-2025-04-20"
	interleavedThinkingBeta = "interleaved-thinking-2025-05-14"
	claudeCodeVersion       = "2.1.75"
	claudeCodeSystemPrefix  = "You are Claude Code, Anthropic's official CLI for Claude."
)

// AnthropicConfig parameterizes an Anthropic driver. Every field except BaseURL and
// MaxTokens is required in practice; the zero value yields a driver that
// fails with clear errors instead of panicking.
type AnthropicConfig struct {
	// BaseURL is the messages endpoint. Empty means the default
	// https://api.anthropic.com/v1/messages; override it for OAuth
	// gateways and compatible providers.
	BaseURL string

	// APIKey resolves the credential for one request: the API key sent as
	// x-api-key, or the OAuth bearer token when OAuth is set. It is
	// called once per turn and must not be nil.
	APIKey func(ctx context.Context) (string, error)

	// OAuth switches the driver to subscription auth: the credential is
	// sent as an Authorization Bearer token, the Claude Code identity
	// beta headers and user-agent/x-app markers are added, and the
	// system prompt is prefixed with the Claude Code identity block,
	// exactly as Pi does for subscription tokens.
	OAuth bool

	// ProviderID is the canonical provider identifier, for example
	// "anthropic". It lands in agent.AssistantMessage.Provider and
	// prefixes every error message the driver produces.
	ProviderID string

	// MaxTokens is the max_tokens output budget. The messages API
	// requires it; zero means the 4096 default.
	MaxTokens int64
}

// Anthropic streams assistant turns from any provider that speaks the
// Anthropic messages protocol over SSE. It implements Driver (and therefore
// agent.Client) and is safe for concurrent use: all mutable state lives per
// turn.
type Anthropic struct {
	baseURL    string
	apiKey     func(ctx context.Context) (string, error)
	oauth      bool
	providerID string
	maxTokens  int64
	prefix     string
	http       *http.Client
}

// NewAnthropic returns a driver for the given configuration. When
// httpClient is nil a default client is built (see DefaultHTTPClient). The
// base URL defaults to the public messages endpoint and a trailing slash is
// trimmed.
func NewAnthropic(cfg AnthropicConfig, httpClient *http.Client) *Anthropic {
	if httpClient == nil {
		httpClient = DefaultHTTPClient()
	}
	prefix := cfg.ProviderID
	if prefix == "" {
		prefix = "provider"
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = DefaultAnthropicBaseURL
	}
	maxTokens := cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultAnthropicMaxTokens
	}
	return &Anthropic{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     cfg.APIKey,
		oauth:      cfg.OAuth,
		providerID: cfg.ProviderID,
		maxTokens:  maxTokens,
		prefix:     prefix,
		http:       httpClient,
	}
}

// Compile-time assertion that Anthropic satisfies Driver.
var _ Driver = (*Anthropic)(nil)

// StreamTurn performs one assistant turn against the provider: it POSTs the
// messages request, streams the SSE response, delivers text and thinking
// deltas to the callbacks, and returns the completed assistant message. On
// failure it returns nil and an error; deltas already delivered to the
// callbacks stay delivered. It implements agent.Client.
func (d *Anthropic) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	if req == nil {
		return nil, errors.New(d.prefix + ": nil turn request")
	}
	if req.Messages == nil {
		return nil, errors.New(d.prefix + ": nil messages")
	}
	if d.apiKey == nil {
		return nil, fmt.Errorf("%s: no API key configured for provider", d.prefix)
	}

	payload, err := json.Marshal(buildAnthropicRequest(d, req))
	if err != nil {
		return nil, fmt.Errorf("%s: encode request: %w", d.prefix, err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, d.baseURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("%s: build request: %w", d.prefix, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("accept", "application/json")
	httpReq.Header.Set("anthropic-version", anthropicVersion)
	httpReq.Header.Set("anthropic-dangerous-direct-browser-access", "true")

	key, err := d.apiKey(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: resolve credential: %w", d.prefix, err)
	}
	if d.oauth {
		httpReq.Header.Set("Authorization", "Bearer "+key)
		httpReq.Header.Set("anthropic-beta", strings.Join([]string{claudeCodeBeta, oauthBeta, interleavedThinkingBeta}, ","))
		httpReq.Header.Set("user-agent", "claude-cli/"+claudeCodeVersion)
		httpReq.Header.Set("x-app", "cli")
	} else {
		httpReq.Header.Set("x-api-key", key)
		httpReq.Header.Set("anthropic-beta", interleavedThinkingBeta)
	}

	resp, err := d.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", d.prefix, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, d.responseError(resp)
	}

	return d.readStream(ctx, resp, req, onText, onThinking)
}

// responseError builds an error from a non-2xx response, parsing the
// Anthropic {type:"error", error:{type, message}} envelope when present.
// The error type is included so the retry classifier can recognize
// provider-specific failures such as overloaded_error (HTTP 529); retry
// classification itself lives in internal/retry.
func (d *Anthropic) responseError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var env struct {
		Type  string `json:"type"`
		Error *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err == nil && env.Error != nil && env.Error.Message != "" {
		typ := ""
		if env.Error.Type != "" {
			typ = fmt.Sprintf(" (type %s)", env.Error.Type)
		}
		return fmt.Errorf("%s: %s: %s%s", d.prefix, resp.Status, env.Error.Message, typ)
	}
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		msg = resp.Status
	}
	return fmt.Errorf("%s: %s: %s", d.prefix, resp.Status, msg)
}
