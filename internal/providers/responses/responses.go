// Package responses implements the smidja provider driver for the OpenAI
// Responses protocol: streaming typed input items and named SSE events
// over /v1/responses. It is the base for the Azure OpenAI Responses and
// ChatGPT Codex variants, which the driver selects through Config knobs.
// It ports the reference behavior of pi-ai's openai-responses,
// openai-codex-responses, and azure-openai-responses adapters
// (dist/api/openai-responses*.js) faithfully, restricted to the smidja
// agent.ContentBlock surface.
//
// Adaptations from pi-ai, all documented where they occur:
//   - The system prompt is sent in the top-level instructions field for
//     every variant; pi-ai's plain adapter pushes a developer/system
//     input item instead (the Codex adapter already uses instructions).
//   - The driver sends no reasoning parameters: smidja's TurnRequest
//     carries no reasoning knob. Hosts that want encrypted reasoning
//     items (and therefore reasoning replay) pass the include list via
//     Config.Include, for example ["reasoning.encrypted_content"].
//   - agent.ContentBlock has no text-signature field, so assistant text
//     message ids fall back to the deterministic msg_pi_<i> shape on
//     replay instead of round-tripping the provider id.
//   - Cost accounting stays zero: smidja has no per-model pricing table.
package responses

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/providers"
)

// Endpoint defaults for the three variants.
const (
	defaultBaseURL         = "https://api.openai.com/v1/responses"
	defaultCodexBaseURL    = "https://chatgpt.com/backend-api"
	defaultAzureAPIVersion = "v1"
	codexBetaHeader        = "responses=experimental"
	codexAccountClaimKey   = "https://api.openai.com/auth"
)

// Config parameterizes a Responses driver. Every field is optional; the
// zero value yields a driver that fails with clear errors instead of
// panicking.
type Config struct {
	// BaseURL is the responses endpoint. In plain mode it defaults to
	// https://api.openai.com/v1/responses and is POSTed to as-is. In
	// Codex mode it is the ChatGPT backend root (default
	// https://chatgpt.com/backend-api) and the driver appends
	// /codex/responses. In Azure mode it is the Azure resource endpoint
	// root (for example https://my-resource.openai.azure.com) and the
	// driver appends /openai/deployments/{deployment}/responses with the
	// api-version query parameter. A trailing slash is trimmed.
	BaseURL string

	// Auth resolves the credential for one request. Plain and Codex
	// modes send it as a Bearer token; Azure mode sends it in the
	// api-key header. Codex mode additionally derives the
	// chatgpt-account-id header from the JWT payload, mirroring pi-ai's
	// extractAccountId, so Codex requires a JWT-shaped credential. Auth
	// may be nil, in which case the credential is empty.
	Auth func(ctx context.Context) (string, error)

	// ProviderID is the canonical provider identifier, for example
	// "openai". It lands in agent.AssistantMessage.Provider and
	// prefixes every error message the driver produces.
	ProviderID string

	// API is the API dialect identifier, for example "openai-responses"
	// or "openai-codex-responses". It lands in agent.AssistantMessage.API.
	API string

	// Codex switches the wire details to the ChatGPT Codex backend: the
	// chatgpt-account-id, originator, session-id, and OpenAI-Beta
	// headers, the /codex/responses URL, and the Codex request body
	// (text.verbosity, parallel_tool_calls, tool_choice auto, and the
	// reasoning.encrypted_content include). Codex credentials are the
	// ChatGPT access token; the account id is extracted from its JWT
	// payload.
	Codex bool

	// Azure switches to the Azure OpenAI Responses endpoint: the
	// api-key auth header, the deployment URL shape above, and the model
	// field replaced by the deployment name.
	Azure bool

	// Deployment is the Azure deployment name. It is required in Azure
	// mode: the driver sends it as the model field and in the URL path.
	Deployment string

	// APIVersion is the Azure api-version query parameter. It defaults
	// to "v1", the version pi-ai's SDK defaults to; hosts serving a
	// concrete API version should set it explicitly.
	APIVersion string

	// SessionID, when non-empty, is sent in the session-affinity
	// headers (session_id and x-client-request-id in plain mode,
	// session-id and x-client-request-id in Codex mode) and as the
	// prompt_cache_key request field. It mirrors pi-ai's sessionId
	// plumbing.
	SessionID string

	// Originator is the Codex originator header value. It defaults to
	// "pi", the value the ChatGPT backend recognizes for its own
	// clients; smidja reuses it so the backend accepts the traffic.
	Originator string

	// UserAgent is the Codex User-Agent header value; defaults to
	// "smidja".
	UserAgent string

	// Include lists the response detail fields to request; it lands in
	// the request include field. Hosts wanting encrypted reasoning item
	// replay pass ["reasoning.encrypted_content"].
	Include []string

	// DefaultHeaders are extra headers sent on every request. The
	// mode-specific auth headers are set after these and win.
	DefaultHeaders map[string]string
}

// Responses streams assistant turns from any provider that speaks the
// OpenAI Responses protocol over SSE. It implements providers.Driver (and
// therefore agent.Client) and is safe for concurrent use: all mutable
// state lives per turn.
type Responses struct {
	baseURL    string
	auth       func(ctx context.Context) (string, error)
	headers    map[string]string
	providerID string
	api        string
	prefix     string
	codex      bool
	azure      bool
	deployment string
	apiVersion string
	sessionID  string
	originator string
	userAgent  string
	include    []string
	http       *http.Client
}

// Compile-time assertion that Responses satisfies the provider driver
// seam.
var _ providers.Driver = (*Responses)(nil)

// New returns a driver for the given configuration. When httpClient is
// nil a default client is built whose transport carries generous dial and
// TLS handshake timeouts; the client has no overall timeout, so
// per-request cancellation is driven by the request context.
func New(cfg Config, httpClient *http.Client) *Responses {
	if httpClient == nil {
		httpClient = providers.DefaultHTTPClient()
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		if cfg.Codex {
			baseURL = defaultCodexBaseURL
		} else {
			baseURL = defaultBaseURL
		}
	}
	apiVersion := cfg.APIVersion
	if apiVersion == "" {
		apiVersion = defaultAzureAPIVersion
	}
	originator := cfg.Originator
	if originator == "" {
		originator = "pi"
	}
	userAgent := cfg.UserAgent
	if userAgent == "" {
		userAgent = "smidja"
	}
	prefix := cfg.ProviderID
	if prefix == "" {
		prefix = "provider"
	}
	return &Responses{
		baseURL:    strings.TrimRight(baseURL, "/"),
		auth:       cfg.Auth,
		headers:    cfg.DefaultHeaders,
		providerID: cfg.ProviderID,
		api:        cfg.API,
		prefix:     prefix,
		codex:      cfg.Codex,
		azure:      cfg.Azure,
		deployment: cfg.Deployment,
		apiVersion: apiVersion,
		sessionID:  cfg.SessionID,
		originator: originator,
		userAgent:  userAgent,
		include:    cfg.Include,
		http:       httpClient,
	}
}

// StreamTurn performs one assistant turn against the provider: it POSTs
// the request, streams the named-event SSE response, delivers text and
// thinking deltas to the callbacks, and returns the completed assistant
// message. On failure it returns nil and an error; deltas already
// delivered to the callbacks stay delivered. It implements agent.Client.
func (d *Responses) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	if req == nil {
		return nil, errors.New(d.prefix + ": nil turn request")
	}
	if req.Messages == nil {
		return nil, errors.New(d.prefix + ": nil messages")
	}
	if d.azure && d.deployment == "" {
		return nil, errors.New(d.prefix + ": azure mode requires a deployment name")
	}

	input := BuildInput(req.Messages)
	payload, err := json.Marshal(d.buildRequest(req, input))
	if err != nil {
		return nil, fmt.Errorf("%s: encode request: %w", d.prefix, err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, d.resolveURL(), bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("%s: build request: %w", d.prefix, err)
	}
	if err := d.applyHeaders(httpReq, ctx); err != nil {
		return nil, err
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

// buildRequest assembles the wire request body for the configured
// variant.
func (d *Responses) buildRequest(req *agent.TurnRequest, input []json.RawMessage) request {
	instructions := req.System
	if d.codex && instructions == "" {
		// The Codex backend expects a non-empty instructions string,
		// mirroring pi-ai's fallback.
		instructions = "You are a helpful assistant."
	}
	include := d.include
	if d.codex && len(include) == 0 {
		// The Codex backend always returns encrypted reasoning items,
		// mirroring pi-ai's hardcoded include.
		include = []string{"reasoning.encrypted_content"}
	}
	b := request{
		Model:        d.modelName(req.Model),
		Instructions: instructions,
		Input:        input,
		Stream:       true,
		Store:        false,
		Include:      include,
	}
	if tools := BuildTools(req.Tools, d.codex); len(tools) > 0 {
		b.Tools = tools
	}
	if d.codex {
		b.Text = &textOptions{Verbosity: "low"}
		b.ToolChoice = "auto"
		b.ParallelToolCalls = true
	}
	if d.sessionID != "" {
		clamped := clampPromptCacheKey(d.sessionID)
		b.PromptCacheKey = &clamped
	}
	return b
}

// clampPromptCacheKey normalizes a session id for the prompt_cache_key
// field, mirroring pi-ai's clampOpenAIPromptCacheKey: non-alphanumeric
// characters become underscores, the result is capped at 64 characters,
// and trailing underscores are stripped.
func clampPromptCacheKey(id string) string {
	var sb strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			sb.WriteRune(r)
		} else {
			sb.WriteByte('_')
		}
	}
	s := sb.String()
	if len(s) > 64 {
		s = s[:64]
	}
	return strings.TrimRight(s, "_")
}

// modelName returns the model field of the request: the deployment name
// in Azure mode, the requested model otherwise, mirroring pi-ai's Azure
// adapter.
func (d *Responses) modelName(model string) string {
	if d.azure {
		return d.deployment
	}
	return model
}

// resolveURL returns the endpoint for the configured variant.
func (d *Responses) resolveURL() string {
	switch {
	case d.azure:
		base := d.baseURL
		if !strings.Contains(base, "/openai") {
			base += "/openai"
		}
		return fmt.Sprintf("%s/deployments/%s/responses?api-version=%s",
			base, url.PathEscape(d.deployment), url.QueryEscape(d.apiVersion))
	case d.codex:
		return codexURL(d.baseURL)
	default:
		return d.baseURL
	}
}

// codexURL resolves the Codex responses endpoint from a base URL,
// mirroring pi-ai's resolveCodexUrl: a /codex/responses suffix is kept,
// a /codex suffix gains /responses, anything else gets /codex/responses
// appended.
func codexURL(base string) string {
	raw := strings.TrimRight(base, "/")
	switch {
	case strings.HasSuffix(raw, "/codex/responses"):
		return raw
	case strings.HasSuffix(raw, "/codex"):
		return raw + "/responses"
	default:
		return raw + "/codex/responses"
	}
}

// applyHeaders sets the mode-specific request headers and resolves the
// credential.
func (d *Responses) applyHeaders(httpReq *http.Request, ctx context.Context) error {
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	for k, v := range d.headers {
		httpReq.Header.Set(k, v)
	}
	token := ""
	if d.auth != nil {
		var err error
		token, err = d.auth(ctx)
		if err != nil {
			return fmt.Errorf("%s: resolve credential: %w", d.prefix, err)
		}
	}
	switch {
	case d.azure:
		httpReq.Header.Set("api-key", token)
	case d.codex:
		accountID, err := extractAccountID(token)
		if err != nil {
			return fmt.Errorf("%s: %w", d.prefix, err)
		}
		httpReq.Header.Set("Authorization", "Bearer "+token)
		httpReq.Header.Set("chatgpt-account-id", accountID)
		httpReq.Header.Set("originator", d.originator)
		httpReq.Header.Set("User-Agent", d.userAgent)
		httpReq.Header.Set("OpenAI-Beta", codexBetaHeader)
		if d.sessionID != "" {
			httpReq.Header.Set("session-id", d.sessionID)
			httpReq.Header.Set("x-client-request-id", d.sessionID)
		}
	default:
		httpReq.Header.Set("Authorization", "Bearer "+token)
		if d.sessionID != "" {
			httpReq.Header.Set("session_id", d.sessionID)
			httpReq.Header.Set("x-client-request-id", d.sessionID)
		}
	}
	return nil
}

// extractAccountID derives the chatgpt-account-id from the JWT payload
// of a ChatGPT access token, mirroring pi-ai's extractAccountId.
func extractAccountID(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", errors.New("failed to extract accountId from token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// Tolerate padded standard base64.
		payload, err = base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			return "", errors.New("failed to extract accountId from token")
		}
	}
	var claims map[string]map[string]string
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", errors.New("failed to extract accountId from token")
	}
	accountID := claims[codexAccountClaimKey]["chatgpt_account_id"]
	if accountID == "" {
		return "", errors.New("failed to extract accountId from token")
	}
	return accountID, nil
}

// responseError builds an error from a non-2xx response, parsing the
// provider {error:{code,message,type}} envelope when present.
func (d *Responses) responseError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var env errorEnvelope
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
