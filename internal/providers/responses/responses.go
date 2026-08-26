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

const (
	defaultBaseURL         = "https://api.openai.com/v1/responses"
	defaultCodexBaseURL    = "https://chatgpt.com/backend-api"
	defaultAzureAPIVersion = "v1"
	codexBetaHeader        = "responses=experimental"
	codexAccountClaimKey   = "https://api.openai.com/auth"
)

type Config struct {
	BaseURL string

	Auth func(ctx context.Context) (string, error)

	ProviderID string

	API string

	Codex bool

	Azure bool

	Deployment string

	APIVersion string

	SessionID string

	Originator string

	UserAgent string

	Include []string

	DefaultHeaders map[string]string
}

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

var _ providers.Driver = (*Responses)(nil)

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

func (d *Responses) buildRequest(req *agent.TurnRequest, input []json.RawMessage) request {
	instructions := req.System
	if d.codex && instructions == "" {
		instructions = "You are a helpful assistant."
	}
	include := d.include
	if d.codex && len(include) == 0 {
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

func (d *Responses) modelName(model string) string {
	if d.azure {
		return d.deployment
	}
	return model
}

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

func extractAccountID(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", errors.New("failed to extract accountId from token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
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
