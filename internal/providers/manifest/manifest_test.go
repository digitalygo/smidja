package manifest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/authstore"
	"github.com/digitalygo/smidja/internal/providers"
	"github.com/digitalygo/smidja/internal/providers/gemini"
	"github.com/digitalygo/smidja/internal/providers/responses"
)

func envOf(m map[string]string) func(string) string {
	return func(name string) string { return m[name] }
}

func baseTurnReq(model string) *agent.TurnRequest {
	return &agent.TurnRequest{
		Model: model,
		Messages: []*agent.Message{
			{User: &agent.UserMessage{Role: string(agent.RoleUser), Content: json.RawMessage(`"hi"`)}},
		},
	}
}

func captureServer(t *testing.T, frames ...string) (*httptest.Server, *capturedRequest) {
	t.Helper()
	captured := &capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		captured.method = r.Method
		captured.path = r.URL.Path
		captured.query = r.URL.RawQuery
		captured.header = r.Header.Clone()
		captured.body = body

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if fl, ok := w.(http.Flusher); ok {
			for _, f := range frames {
				fmt.Fprint(w, f)
				fl.Flush()
			}
		}
	}))
	return srv, captured
}

type capturedRequest struct {
	method string
	path   string
	query  string
	header http.Header
	body   []byte
}

type rewriteTransport struct {
	target *url.URL
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL = &url.URL{Scheme: t.target.Scheme, Host: t.target.Host, Path: req.URL.Path, RawQuery: req.URL.RawQuery}
	return http.DefaultTransport.RoundTrip(clone)
}

func clientFor(srv *httptest.Server) *http.Client {
	target, err := url.Parse(srv.URL)
	if err != nil {
		panic(err)
	}
	return &http.Client{Transport: &rewriteTransport{target: target}}
}

var (
	completionsFrames = []string{
		`data: {"id":"gen_1","choices":[{"index":0,"delta":{"content":"hi"}}]}` + "\n\n",
		`data: {"id":"gen_1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n",
		"data: [DONE]\n\n",
	}
	anthropicFrames = []string{
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_01\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-test\",\"stop_reason\":null,\"usage\":{\"input_tokens\":25,\"output_tokens\":1}}}\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null}}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	}
	geminiFrames = []string{
		`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]},"finishReason":"STOP"}],"responseId":"resp_1"}` + "\n\n",
	}
	responsesFrames = []string{
		`data: {"type":"response.created","response":{"id":"resp_1"}}` + "\n\n",
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"","annotations":[]}],"status":"completed"}}` + "\n\n",
		`data: {"type":"response.output_text.delta","output_index":0,"delta":"hi"}` + "\n\n",
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"hi","annotations":[]}],"status":"completed"}}` + "\n\n",
		`data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","usage":{"input_tokens":20,"output_tokens":5,"total_tokens":25,"input_tokens_details":{"cached_tokens":4}},"output":[]}}` + "\n\n",
	}
)

func TestAllSpecsValid(t *testing.T) {
	validDialects := map[string]bool{
		DialectOpenAICompletions: true,
		DialectAnthropicMessages: true,
		DialectGemini:            true,
		DialectOpenAIResponses:   true,
	}
	seen := make(map[string]bool, len(All))
	for _, spec := range All {
		if spec.ID == "" {
			t.Error("spec with empty ID")
		}
		if spec.EnvVar == "" {
			t.Errorf("%s: empty EnvVar", spec.ID)
		}
		if !validDialects[spec.Dialect] {
			t.Errorf("%s: unsupported dialect %q", spec.ID, spec.Dialect)
		}
		if spec.DefaultModel == "" {
			t.Errorf("%s: empty DefaultModel", spec.ID)
		}
		if seen[spec.ID] {
			t.Errorf("duplicate provider id %q", spec.ID)
		}
		seen[spec.ID] = true
		if spec.ID == "azure-openai-responses" {
			if spec.BaseURL != "" {
				t.Errorf("azure base URL = %q, want empty (env-driven)", spec.BaseURL)
			}
			continue
		}
		u, err := url.Parse(spec.BaseURL)
		if err != nil {
			t.Errorf("%s: base URL %q: %v", spec.ID, spec.BaseURL, err)
			continue
		}
		if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			t.Errorf("%s: base URL %q is not a valid endpoint", spec.ID, spec.BaseURL)
		}
	}
	if len(seen) != 32 {
		t.Errorf("manifest holds %d providers, want 32", len(seen))
	}
}

func TestLookup(t *testing.T) {
	spec, ok := Lookup("deepseek")
	if !ok || spec.EnvVar != "DEEPSEEK_API_KEY" {
		t.Errorf("Lookup(deepseek) = %+v, %v; want the deepseek spec", spec, ok)
	}
	if _, ok := Lookup("does-not-exist"); ok {
		t.Error("Lookup on an unknown provider returned ok=true")
	}
}

func TestBuildDialectDrivers(t *testing.T) {
	tests := []struct {
		id       string
		model    string
		frames   []string
		wantPath string
		check    func(*testing.T, providers.Driver)
	}{
		{
			id: "deepseek", model: "deepseek-v4-pro", frames: completionsFrames, wantPath: "/chat/completions",
			check: func(t *testing.T, d providers.Driver) {
				if _, ok := d.(*providers.OpenAICompletions); !ok {
					t.Errorf("deepseek driver = %T, want *providers.OpenAICompletions", d)
				}
			},
		},
		{
			id: "anthropic", model: "claude-sonnet-4-6", frames: anthropicFrames, wantPath: "/v1/messages",
			check: func(t *testing.T, d providers.Driver) {
				if _, ok := d.(*providers.Anthropic); !ok {
					t.Errorf("anthropic driver = %T, want *providers.Anthropic", d)
				}
			},
		},
		{
			id: "google", model: "gemini-2.5-pro", frames: geminiFrames, wantPath: "/v1beta/models/gemini-2.5-pro:streamGenerateContent",
			check: func(t *testing.T, d providers.Driver) {
				if _, ok := d.(*gemini.Gemini); !ok {
					t.Errorf("google driver = %T, want *gemini.Gemini", d)
				}
			},
		},
		{
			id: "openai", model: "gpt-5.2", frames: responsesFrames, wantPath: "/v1/responses",
			check: func(t *testing.T, d providers.Driver) {
				if _, ok := d.(*responses.Responses); !ok {
					t.Errorf("openai driver = %T, want *responses.Responses", d)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			spec, ok := Lookup(tt.id)
			if !ok {
				t.Fatalf("no spec for %s", tt.id)
			}
			env := map[string]string{spec.EnvVar: "sk-" + tt.id}
			srv, captured := captureServer(t, tt.frames...)
			defer srv.Close()
			deps := Deps{Env: envOf(env), HTTP: clientFor(srv)}
			d, err := Build(tt.id, deps)
			if err != nil {
				t.Fatalf("Build(%s): %v", tt.id, err)
			}
			tt.check(t, d)
			msg, err := d.StreamTurn(context.Background(), baseTurnReq(tt.model), nil, nil)
			if err != nil {
				t.Fatalf("StreamTurn: %v", err)
			}
			if msg.Provider != tt.id {
				t.Errorf("provider = %q, want %q", msg.Provider, tt.id)
			}
			if captured.path != tt.wantPath {
				t.Errorf("path = %q, want %q", captured.path, tt.wantPath)
			}
		})
	}
}

func TestBuildCredentialResolution(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	store, err := authstore.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := store.Set("deepseek", authstore.Entry{Type: "api_key", Key: "sk-store"}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	env := map[string]string{"DEEPSEEK_API_KEY": "sk-env"}
	srv, captured := captureServer(t, completionsFrames...)
	defer srv.Close()
	deps := Deps{Env: envOf(env), Store: store, HTTP: clientFor(srv)}
	d, err := Build("deepseek", deps)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := d.StreamTurn(context.Background(), baseTurnReq("deepseek-v4-pro"), nil, nil); err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	if got := captured.header.Get("Authorization"); got != "Bearer sk-env" {
		t.Errorf("Authorization = %q, want the env key", got)
	}

	srv2, captured2 := captureServer(t, completionsFrames...)
	defer srv2.Close()
	deps.Env = envOf(map[string]string{})
	deps.HTTP = clientFor(srv2)
	d, err = Build("deepseek", deps)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := d.StreamTurn(context.Background(), baseTurnReq("deepseek-v4-pro"), nil, nil); err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	if got := captured2.header.Get("Authorization"); got != "Bearer sk-store" {
		t.Errorf("Authorization = %q, want the store key", got)
	}
}

func TestBuildMissingCredential(t *testing.T) {
	d, err := Build("groq", Deps{Env: envOf(map[string]string{})})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	_, err = d.StreamTurn(context.Background(), baseTurnReq("llama-3.3-70b-versatile"), nil, nil)
	if err == nil {
		t.Fatal("StreamTurn with no credential succeeded, want error")
	}
	if !strings.Contains(err.Error(), "GROQ_API_KEY") {
		t.Errorf("error = %q, want the env var named", err)
	}
}

func TestBuildUnknown(t *testing.T) {
	if _, err := Build("no-such-provider", Deps{}); err == nil {
		t.Fatal("Build on an unknown provider returned no error")
	}
}

func TestBuildCloudflareWorkersAI(t *testing.T) {
	if _, err := Build("cloudflare-workers-ai", Deps{Env: envOf(map[string]string{})}); err == nil {
		t.Fatal("Build without CLOUDFLARE_ACCOUNT_ID returned no error")
	}
	if _, err := Build("cloudflare-workers-ai", Deps{}); err == nil {
		t.Fatal("Build with nil env returned no error")
	}
	env := map[string]string{
		"CLOUDFLARE_ACCOUNT_ID": "acct-123",
		"CLOUDFLARE_API_KEY":    "cf-key",
	}
	srv, captured := captureServer(t, completionsFrames...)
	defer srv.Close()
	d, err := Build("cloudflare-workers-ai", Deps{Env: envOf(env), HTTP: clientFor(srv)})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := d.StreamTurn(context.Background(), baseTurnReq("@cf/moonshotai/kimi-k2.6"), nil, nil); err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	if captured.path != "/client/v4/accounts/acct-123/ai/v1/chat/completions" {
		t.Errorf("path = %q, want account id materialized", captured.path)
	}
	if got := captured.header.Get("Authorization"); got != "Bearer cf-key" {
		t.Errorf("Authorization = %q, want the cloudflare key", got)
	}
}

func TestBuildCloudflareAIGateway(t *testing.T) {
	if _, err := Build("cloudflare-ai-gateway", Deps{Env: envOf(map[string]string{})}); err == nil {
		t.Fatal("Build without cloudflare env returned no error")
	}
	if _, err := Build("cloudflare-ai-gateway", Deps{Env: envOf(map[string]string{
		"CLOUDFLARE_ACCOUNT_ID": "acct-123",
		"CLOUDFLARE_GATEWAY_ID": "gw-456",
	})}); err == nil {
		t.Fatal("Build without the cloudflare key returned no error")
	}
	env := map[string]string{
		"CLOUDFLARE_ACCOUNT_ID": "acct-123",
		"CLOUDFLARE_GATEWAY_ID": "gw-456",
		"CLOUDFLARE_API_KEY":    "cf-key",
	}
	srv, captured := captureServer(t, completionsFrames...)
	defer srv.Close()
	d, err := Build("cloudflare-ai-gateway", Deps{Env: envOf(env), HTTP: clientFor(srv)})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := d.StreamTurn(context.Background(), baseTurnReq("workers-ai/@cf/moonshotai/kimi-k2.6"), nil, nil); err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	if captured.path != "/v1/acct-123/gw-456/compat/chat/completions" {
		t.Errorf("path = %q, want account and gateway id materialized", captured.path)
	}
	if got := captured.header.Get("cf-aig-authorization"); got != "Bearer cf-key" {
		t.Errorf("cf-aig-authorization = %q, want the cloudflare key", got)
	}
}

func TestBuildAzure(t *testing.T) {
	if _, err := Build("azure-openai-responses", Deps{Env: envOf(map[string]string{})}); err == nil {
		t.Fatal("Build without azure endpoint env returned no error")
	}
	env := map[string]string{
		"AZURE_OPENAI_RESOURCE_NAME":       "my-resource",
		"AZURE_OPENAI_API_VERSION":         "2024-02-01",
		"AZURE_OPENAI_DEPLOYMENT_NAME_MAP": "gpt-5.2=my-gpt52",
		"AZURE_OPENAI_API_KEY":             "az-key",
	}
	srv, captured := captureServer(t, responsesFrames...)
	defer srv.Close()
	d, err := Build("azure-openai-responses", Deps{Env: envOf(env), HTTP: clientFor(srv)})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, ok := d.(*responses.Responses); !ok {
		t.Fatalf("azure driver = %T, want *responses.Responses", d)
	}
	if _, err := d.StreamTurn(context.Background(), baseTurnReq("gpt-5.2"), nil, nil); err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	wantPath := "/openai/deployments/my-gpt52/responses"
	if captured.path != wantPath {
		t.Errorf("path = %q, want %q", captured.path, wantPath)
	}
	if captured.query != "api-version=2024-02-01" {
		t.Errorf("query = %q, want the api version", captured.query)
	}
	if got := captured.header.Get("api-key"); got != "az-key" {
		t.Errorf("api-key = %q, want the azure key", got)
	}
}

func TestBuildAzureDefaults(t *testing.T) {
	env := map[string]string{
		"AZURE_OPENAI_BASE_URL": "https://my-resource.openai.azure.com",
		"AZURE_OPENAI_API_KEY":  "az-key",
	}
	srv, captured := captureServer(t, responsesFrames...)
	defer srv.Close()
	d, err := Build("azure-openai-responses", Deps{Env: envOf(env), HTTP: clientFor(srv)})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := d.StreamTurn(context.Background(), baseTurnReq("gpt-5.2"), nil, nil); err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	if captured.path != "/openai/deployments/gpt-5.2/responses" {
		t.Errorf("path = %q, want the default model as deployment", captured.path)
	}
	if captured.query != "api-version=v1" {
		t.Errorf("query = %q, want the default api version", captured.query)
	}
}

func TestBuildAnthropicAuth(t *testing.T) {
	srv, captured := captureServer(t, anthropicFrames...)
	defer srv.Close()
	d, err := Build("anthropic", Deps{Env: envOf(map[string]string{"ANTHROPIC_API_KEY": "sk-ant"}), HTTP: clientFor(srv)})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := d.StreamTurn(context.Background(), baseTurnReq("claude-sonnet-4-6"), nil, nil); err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	if got := captured.header.Get("x-api-key"); got != "sk-ant" {
		t.Errorf("x-api-key = %q, want the api key", got)
	}
	if got := captured.header.Get("anthropic-version"); got == "" {
		t.Error("anthropic-version header missing")
	}
}

func TestParseDeploymentMap(t *testing.T) {
	m := parseDeploymentMap("gpt-4=my-gpt4, gpt-4o=my-gpt4o, broken, =x, y=")
	if len(m) != 2 || m["gpt-4"] != "my-gpt4" || m["gpt-4o"] != "my-gpt4o" {
		t.Errorf("parseDeploymentMap = %v, want the two valid entries", m)
	}
	if got := parseDeploymentMap(""); len(got) != 0 {
		t.Errorf("empty map = %v, want empty", got)
	}
}
