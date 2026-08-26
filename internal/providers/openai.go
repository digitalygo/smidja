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

type Config struct {
	BaseURL string

	DefaultHeaders map[string]string

	Auth func(ctx context.Context) (string, error)

	ProviderID string

	API string
}

type OpenAICompletions struct {
	baseURL    string
	headers    map[string]string
	auth       func(ctx context.Context) (string, error)
	providerID string
	api        string
	prefix     string
	http       *http.Client
}

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

func DefaultHTTPClient() *http.Client {
	return &http.Client{Transport: defaultTransport()}
}

func defaultTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DialContext = (&net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext
	t.TLSHandshakeTimeout = 10 * time.Second
	return t
}

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
