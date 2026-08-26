// Package gemini implements the smidja provider driver for the Google
// Generative Language API (the google-generative-ai protocol): streaming
// generateContent over SSE with thinking parts, thought signatures, and
// function calling. It ports the reference behavior of pi-ai's
// google-generative-ai adapter (dist/api/google-generative-ai.js and
// dist/api/google-shared.js) faithfully, restricted to the smidja
// agent.ContentBlock surface.
//
// Adaptations from pi-ai, all documented where they occur:
//   - The driver always requests thinking parts
//     (thinkingConfig.includeThoughts: true): smidja's TurnRequest carries
//     no reasoning knob, and thinking blocks surface whenever the model
//     produces them, like the openai-completions driver does for reasoning
//     deltas. Pi disables thoughts unless reasoning is requested.
//   - agent.ContentBlock has no text-signature field, so thoughtSignature
//     values attached to text parts are not persisted on replay; thinking
//     block signatures (the common case) are preserved. Tool-call parts
//     likewise cannot carry a signature in smidja.
//   - promptFeedback.blockReason is surfaced as an explicit error; pi-ai
//     relies on the SDK to throw for blocked prompts.
//   - Cost accounting stays zero: smidja has no per-model pricing table.
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

// defaultBaseURL is the Google Generative Language API endpoint root.
const defaultBaseURL = "https://generativelanguage.googleapis.com/v1beta"

// toolCallCounter is the package-level counter Pi uses to synthesize
// unique tool call ids of the shape name_timestamp_counter when the API
// omits or repeats an id.
var toolCallCounter atomic.Int64

// Config parameterizes a Gemini driver. Every field is optional; the zero
// value yields a driver that fails with clear errors instead of panicking.
type Config struct {
	// APIKey resolves the credential for one request; it is sent in the
	// x-goog-api-key header. It is called once per turn and may be nil,
	// in which case no key header is sent and the provider rejects the
	// request.
	APIKey func(ctx context.Context) (string, error)

	// BaseURL is the API root. It defaults to
	// https://generativelanguage.googleapis.com/v1beta; a trailing
	// slash is trimmed.
	BaseURL string

	// ProviderID is the canonical provider identifier, for example
	// "gemini". It lands in agent.AssistantMessage.Provider and
	// prefixes every error message the driver produces.
	ProviderID string

	// API is the API dialect identifier, for example
	// "google-generative-ai". It lands in agent.AssistantMessage.API.
	API string

	// DefaultHeaders are extra headers sent on every request, for
	// example a custom User-Agent.
	DefaultHeaders map[string]string
}

// Gemini streams assistant turns from the Google Generative Language API.
// It implements providers.Driver (and therefore agent.Client) and is safe
// for concurrent use: all mutable state lives per turn.
type Gemini struct {
	baseURL    string
	apiKey     func(ctx context.Context) (string, error)
	headers    map[string]string
	providerID string
	api        string
	prefix     string
	http       *http.Client
}

// Compile-time assertion that Gemini satisfies the provider driver seam.
var _ providers.Driver = (*Gemini)(nil)

// New returns a driver for the given configuration. When httpClient is
// nil a default client is built whose transport carries generous dial and
// TLS handshake timeouts; the client has no overall timeout, so
// per-request cancellation is driven by the request context.
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

// StreamTurn performs one assistant turn against the Google Generative
// Language API: it POSTs the generateContent request, streams the SSE
// response, delivers text and thinking deltas to the callbacks, and
// returns the completed assistant message. On failure it returns nil and
// an error; deltas already delivered to the callbacks stay delivered. It
// implements agent.Client.
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
	// The resolved credential wins over any default header, so a driver
	// never sends a stale key.
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

// responseError builds an error from a non-2xx response, parsing the
// provider {error:{code,message,status}} envelope when present.
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
