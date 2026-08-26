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

const AnthropicAPI = "anthropic-messages"

const DefaultAnthropicBaseURL = "https://api.anthropic.com/v1/messages"

const anthropicVersion = "2023-06-01"

const defaultAnthropicMaxTokens = 4096

const (
	claudeCodeBeta          = "claude-code-20250219"
	oauthBeta               = "oauth-2025-04-20"
	interleavedThinkingBeta = "interleaved-thinking-2025-05-14"
	claudeCodeVersion       = "2.1.75"
	claudeCodeSystemPrefix  = "You are Claude Code, Anthropic's official CLI for Claude."
)

type AnthropicConfig struct {
	BaseURL string

	APIKey func(ctx context.Context) (string, error)

	OAuth bool

	ProviderID string

	MaxTokens int64
}

type Anthropic struct {
	baseURL    string
	apiKey     func(ctx context.Context) (string, error)
	oauth      bool
	providerID string
	maxTokens  int64
	prefix     string
	http       *http.Client
}

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

var _ Driver = (*Anthropic)(nil)

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
