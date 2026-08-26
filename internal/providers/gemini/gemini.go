package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/providers"
)

const defaultBaseURL = "https://generativelanguage.googleapis.com/v1beta"

var toolCallCounter atomic.Int64

type Config struct {
	APIKey func(ctx context.Context) (string, error)

	BaseURL string

	ProviderID string

	API string

	DefaultHeaders map[string]string
}

type Gemini struct {
	baseURL    string
	apiKey     func(ctx context.Context) (string, error)
	headers    map[string]string
	providerID string
	api        string
	prefix     string
	http       *http.Client
}

var _ providers.Driver = (*Gemini)(nil)

func New(cfg Config, httpClient *http.Client) *Gemini {
	if httpClient == nil {
		httpClient = providers.DefaultHTTPClient()
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	prefix := cfg.ProviderID
	if prefix == "" {
		prefix = "provider"
	}
	return &Gemini{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     cfg.APIKey,
		headers:    cfg.DefaultHeaders,
		providerID: cfg.ProviderID,
		api:        cfg.API,
		prefix:     prefix,
		http:       httpClient,
	}
}

func (d *Gemini) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	if req == nil {
		return nil, errors.New(d.prefix + ": nil turn request")
	}
	if req.Messages == nil {
		return nil, errors.New(d.prefix + ": nil messages")
	}

	contents, system := BuildContents(req.System, req.Messages, d.providerID, req.Model)
	payload, err := json.Marshal(GenerateContentRequest{
		Contents:          contents,
		SystemInstruction: system,
		Tools:             BuildTools(req.Tools),
		ThinkingConfig:    &ThinkingConfig{IncludeThoughts: true},
	})
	if err != nil {
		return nil, fmt.Errorf("%s: encode request: %w", d.prefix, err)
	}

	endpoint := d.baseURL + "/models/" + url.PathEscape(req.Model) + ":streamGenerateContent?alt=sse"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("%s: build request: %w", d.prefix, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	for k, v := range d.headers {
		httpReq.Header.Set(k, v)
	}
	if d.apiKey != nil {
		key, err := d.apiKey(ctx)
		if err != nil {
			return nil, fmt.Errorf("%s: resolve credential: %w", d.prefix, err)
		}
		httpReq.Header.Set("x-goog-api-key", key)
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

func (d *Gemini) responseError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var env ErrorEnvelope
	if err := json.Unmarshal(body, &env); err == nil && env.Error.Message != "" {
		code := ""
		if env.Error.Code != 0 {
			code = fmt.Sprintf(" (code %d)", env.Error.Code)
		}
		return fmt.Errorf("%s: %s: %s%s", d.prefix, resp.Status, env.Error.Message, code)
	}
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		msg = resp.Status
	}
	return fmt.Errorf("%s: %s: %s", d.prefix, resp.Status, msg)
}
