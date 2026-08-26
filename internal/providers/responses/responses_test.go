package responses

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
)

// stubTool is a minimal agent.Tool for request-shape tests.
type stubTool struct {
	name   string
	desc   string
	schema json.RawMessage
}

func (s stubTool) Name() string                                       { return s.name }
func (s stubTool) Description() string                                { return s.desc }
func (s stubTool) Schema() json.RawMessage                            { return s.schema }
func (s stubTool) Exec(context.Context, json.RawMessage) agent.Result { return agent.Result{} }

// baseTurnReq returns a minimal turn request with one user message.
func baseTurnReq() *agent.TurnRequest {
	return &agent.TurnRequest{
		Model: "gpt-5",
		Messages: []*agent.Message{
			{User: &agent.UserMessage{Role: string(agent.RoleUser), Content: json.RawMessage(`"hi"`)}},
		},
	}
}

// testDriver returns a plain-mode driver pointed at the given base URL
// with a fixed credential.
func testDriver(t *testing.T, baseURL string) *Responses {
	t.Helper()
	return New(Config{
		BaseURL:    baseURL,
		ProviderID: "openai",
		API:        "openai-responses",
		Auth: func(context.Context) (string, error) {
			return "sk-test", nil
		},
	}, nil)
}

// codexToken returns a JWT-shaped ChatGPT access token whose payload
// carries the given account id.
func codexToken(accountID string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(
		fmt.Sprintf(`{"https://api.openai.com/auth":{"chatgpt_account_id":%q}}`, accountID)))
	return header + "." + payload + ".signature"
}

// captureServer serves the given SSE events and records the request it
// received. Events are flushed one by one so the client reads them
// incrementally.
func captureServer(t *testing.T, events ...string) (*httptest.Server, *capturedRequest) {
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
			for _, e := range events {
				fmt.Fprintf(w, "data: %s\n\n", e)
				fl.Flush()
			}
		}
	}))
	return srv, captured
}

// capturedRequest holds what captureServer recorded about a request.
type capturedRequest struct {
	method string
	path   string
	query  string
	header http.Header
	body   []byte
}

// equalStrings compares two string slices.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestNewDefaultClient checks URL defaulting per variant.
func TestNewDefaultClient(t *testing.T) {
	d := New(Config{ProviderID: "openai"}, nil)
	if d.baseURL != defaultBaseURL {
		t.Errorf("baseURL = %q, want %q", d.baseURL, defaultBaseURL)
	}
	if d.prefix != "openai" {
		t.Errorf("prefix = %q, want provider id", d.prefix)
	}
	if d.http == nil {
		t.Fatal("default http client is nil")
	}
	d2 := New(Config{ProviderID: "openai", Codex: true}, nil)
	if d2.baseURL != defaultCodexBaseURL {
		t.Errorf("codex baseURL = %q, want %q", d2.baseURL, defaultCodexBaseURL)
	}
	d3 := New(Config{ProviderID: "openai", Azure: true}, nil)
	if d3.apiVersion != "v1" {
		t.Errorf("azure apiVersion = %q, want default v1", d3.apiVersion)
	}
}

// TestStreamTurnRequestShape drives one full turn in plain mode and
// verifies the exact request the driver sends: method, path, headers,
// and body shape.
func TestStreamTurnRequestShape(t *testing.T) {
	events := []string{
		`{"type":"response.created","response":{"id":"resp_1"}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"","annotations":[]}],"status":"completed"}}`,
		`{"type":"response.output_text.delta","output_index":0,"delta":"hi"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"hi","annotations":[]}],"status":"completed"}}`,
		`{"type":"response.completed","response":{"id":"resp_1","status":"completed","usage":{"input_tokens":20,"output_tokens":5,"total_tokens":25,"input_tokens_details":{"cached_tokens":4}},"output":[]}}`,
	}
	srv, captured := captureServer(t, events...)
	defer srv.Close()

	d := New(Config{
		BaseURL:    srv.URL,
		ProviderID: "openai",
		API:        "openai-responses",
		DefaultHeaders: map[string]string{
			"X-Custom": "yes",
		},
		Auth: func(context.Context) (string, error) { return "sk-test", nil },
	}, nil)

	req := &agent.TurnRequest{
		Model:  "gpt-5",
		System: "Be helpful",
		Messages: []*agent.Message{
			{User: &agent.UserMessage{Role: string(agent.RoleUser), Content: json.RawMessage(`"hello"`)}},
			{Assistant: &agent.AssistantMessage{Role: string(agent.RoleAssistant), Content: []agent.ContentBlock{
				{Type: agent.BlockTypeText, Text: "Hi!"},
				{Type: agent.BlockTypeToolCall, ID: "call_1|fc_item_1", Name: "read", Arguments: json.RawMessage(`{"path":"a.go"}`)},
			}}},
			{ToolResult: &agent.ToolResultMessage{
				Role:       string(agent.RoleToolResult),
				ToolCallID: "call_1|fc_item_1",
				ToolName:   "read",
				Content:    []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "file body"}},
			}},
		},
		Tools: []agent.Tool{
			stubTool{name: "read", desc: "Reads a file", schema: json.RawMessage(`{"type":"object"}`)},
		},
	}

	msg, err := d.StreamTurn(context.Background(), req, nil, nil)
	if err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	if msg == nil || len(msg.Content) != 1 || msg.Content[0].Text != "hi" {
		t.Fatalf("msg = %+v, want single text block %q", msg, "hi")
	}
	if msg.API != "openai-responses" || msg.Provider != "openai" {
		t.Errorf("identity = api %q provider %q", msg.API, msg.Provider)
	}
	if msg.ResponseID != "resp_1" {
		t.Errorf("responseId = %q, want resp_1", msg.ResponseID)
	}

	if captured.method != http.MethodPost {
		t.Errorf("method = %q, want POST", captured.method)
	}
	if captured.path != "/" {
		t.Errorf("path = %q, want base endpoint", captured.path)
	}
	if got := captured.header.Get("Authorization"); got != "Bearer sk-test" {
		t.Errorf("Authorization = %q", got)
	}
	if got := captured.header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := captured.header.Get("Accept"); got != "text/event-stream" {
		t.Errorf("Accept = %q", got)
	}
	if got := captured.header.Get("X-Custom"); got != "yes" {
		t.Errorf("X-Custom = %q, want default header applied", got)
	}
	if got := captured.header.Get("chatgpt-account-id"); got != "" {
		t.Errorf("chatgpt-account-id = %q, want absent in plain mode", got)
	}
	if got := captured.header.Get("OpenAI-Beta"); got != "" {
		t.Errorf("OpenAI-Beta = %q, want absent in plain mode", got)
	}
	if got := captured.header.Get("session-id"); got != "" {
		t.Errorf("session-id = %q, want absent without session", got)
	}

	var got struct {
		Model        string `json:"model"`
		Instructions string `json:"instructions"`
		Input        []struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			ID      string `json:"id"`
			CallID  string `json:"call_id"`
			Name    string `json:"name"`
			Output  string `json:"output"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"input"`
		Tools []struct {
			Type       string          `json:"type"`
			Name       string          `json:"name"`
			Strict     *bool           `json:"strict"`
			Parameters json.RawMessage `json:"parameters"`
		} `json:"tools"`
		Stream  bool     `json:"stream"`
		Store   bool     `json:"store"`
		Text    any      `json:"text"`
		Include []string `json:"include"`
	}
	if err := json.Unmarshal(captured.body, &got); err != nil {
		t.Fatalf("unmarshal captured body: %v", err)
	}

	if got.Model != "gpt-5" {
		t.Errorf("model = %q, want gpt-5", got.Model)
	}
	if got.Instructions != "Be helpful" {
		t.Errorf("instructions = %q, want system prompt", got.Instructions)
	}
	if !got.Stream {
		t.Error("stream = false, want true")
	}
	if got.Store {
		t.Error("store = true, want false")
	}
	if len(got.Include) != 0 {
		t.Errorf("include = %v, want empty by default", got.Include)
	}
	if got.Text != nil {
		t.Errorf("text = %v, want absent in plain mode", got.Text)
	}
	if len(got.Input) != 4 {
		t.Fatalf("input = %d items, want 4", len(got.Input))
	}
	if got.Input[0].Type != "message" || got.Input[0].Role != "user" || got.Input[0].Content[0].Text != "hello" {
		t.Errorf("input[0] = %+v, want user message", got.Input[0])
	}
	if got.Input[1].Type != "message" || got.Input[1].Role != "assistant" {
		t.Errorf("input[1] = %+v, want assistant message", got.Input[1])
	}
	if got.Input[2].Type != "function_call" || got.Input[2].CallID != "call_1" || got.Input[2].ID != "fc_item_1" || got.Input[2].Name != "read" {
		t.Errorf("input[2] = %+v, want function_call split ids", got.Input[2])
	}
	if got.Input[3].Type != "function_call_output" || got.Input[3].CallID != "call_1" || got.Input[3].Output != "file body" {
		t.Errorf("input[3] = %+v, want function_call_output", got.Input[3])
	}
	if len(got.Tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(got.Tools))
	}
	if got.Tools[0].Type != "function" || got.Tools[0].Name != "read" || got.Tools[0].Strict == nil || *got.Tools[0].Strict {
		t.Errorf("tool = %+v, want strict false", got.Tools[0])
	}
	if !json.Valid(got.Tools[0].Parameters) {
		t.Errorf("parameters = %s, want valid schema", got.Tools[0].Parameters)
	}
}

// TestStreamTurnSessionHeaders verifies the session affinity headers in
// plain and Codex modes.
func TestStreamTurnSessionHeaders(t *testing.T) {
	events := []string{
		`{"type":"response.created","response":{"id":"resp_1"}}`,
		`{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[]}}`,
	}
	srv, captured := captureServer(t, events...)
	defer srv.Close()

	d := New(Config{
		BaseURL:    srv.URL,
		ProviderID: "openai",
		API:        "openai-responses",
		SessionID:  "sess_123",
		Auth:       func(context.Context) (string, error) { return "sk-test", nil },
	}, nil)
	if _, err := d.StreamTurn(context.Background(), baseTurnReq(), nil, nil); err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	if got := captured.header.Get("session_id"); got != "sess_123" {
		t.Errorf("session_id = %q, want sess_123", got)
	}
	if got := captured.header.Get("x-client-request-id"); got != "sess_123" {
		t.Errorf("x-client-request-id = %q, want sess_123", got)
	}

	srv2, captured2 := captureServer(t, events...)
	defer srv2.Close()
	d2 := New(Config{
		BaseURL:    srv2.URL,
		ProviderID: "openai",
		API:        "openai-codex-responses",
		Codex:      true,
		SessionID:  "sess_123",
		Auth:       func(context.Context) (string, error) { return codexToken("acct_1"), nil },
	}, nil)
	if _, err := d2.StreamTurn(context.Background(), baseTurnReq(), nil, nil); err != nil {
		t.Fatalf("StreamTurn codex: %v", err)
	}
	if got := captured2.header.Get("session-id"); got != "sess_123" {
		t.Errorf("codex session-id = %q, want sess_123", got)
	}
	if got := captured2.header.Get("x-client-request-id"); got != "sess_123" {
		t.Errorf("codex x-client-request-id = %q, want sess_123", got)
	}
	if got := captured2.header.Get("session_id"); got != "" {
		t.Errorf("codex session_id = %q, want absent", got)
	}
}

// TestCodexModeDifferences drives a turn in Codex mode and verifies the
// header and body differences versus plain mode.
func TestCodexModeDifferences(t *testing.T) {
	events := []string{
		`{"type":"response.created","response":{"id":"resp_1"}}`,
		`{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[]}}`,
	}
	srv, captured := captureServer(t, events...)
	defer srv.Close()

	d := New(Config{
		BaseURL:    srv.URL + "/backend-api",
		ProviderID: "openai",
		API:        "openai-codex-responses",
		Codex:      true,
		Include:    []string{"reasoning.encrypted_content"},
		Auth:       func(context.Context) (string, error) { return codexToken("acct_42"), nil },
	}, nil)

	req := &agent.TurnRequest{
		Model: "gpt-5-codex",
		Messages: []*agent.Message{
			{User: &agent.UserMessage{Role: string(agent.RoleUser), Content: json.RawMessage(`"hi"`)}},
		},
		Tools: []agent.Tool{
			stubTool{name: "read", desc: "Reads", schema: json.RawMessage(`{"type":"object"}`)},
		},
	}
	msg, err := d.StreamTurn(context.Background(), req, nil, nil)
	if err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	if msg.API != "openai-codex-responses" {
		t.Errorf("api = %q, want openai-codex-responses", msg.API)
	}

	if captured.path != "/backend-api/codex/responses" {
		t.Errorf("path = %q, want /backend-api/codex/responses", captured.path)
	}
	if got := captured.header.Get("Authorization"); got != "Bearer "+codexToken("acct_42") {
		t.Errorf("Authorization = %q", got)
	}
	if got := captured.header.Get("chatgpt-account-id"); got != "acct_42" {
		t.Errorf("chatgpt-account-id = %q, want acct_42", got)
	}
	if got := captured.header.Get("originator"); got != "pi" {
		t.Errorf("originator = %q, want pi default", got)
	}
	if got := captured.header.Get("OpenAI-Beta"); got != "responses=experimental" {
		t.Errorf("OpenAI-Beta = %q", got)
	}
	if got := captured.header.Get("User-Agent"); got != "smidja" {
		t.Errorf("User-Agent = %q, want smidja default", got)
	}

	var got struct {
		Model        string `json:"model"`
		Instructions string `json:"instructions"`
		Text         struct {
			Verbosity string `json:"verbosity"`
		} `json:"text"`
		Include           []string `json:"include"`
		ToolChoice        string   `json:"tool_choice"`
		ParallelToolCalls bool     `json:"parallel_tool_calls"`
		Tools             []struct {
			Strict *bool `json:"strict"`
		} `json:"tools"`
		PromptCacheKey any `json:"prompt_cache_key"`
	}
	if err := json.Unmarshal(captured.body, &got); err != nil {
		t.Fatalf("unmarshal captured body: %v", err)
	}
	if got.Text.Verbosity != "low" {
		t.Errorf("text.verbosity = %q, want low", got.Text.Verbosity)
	}
	if len(got.Include) != 1 || got.Include[0] != "reasoning.encrypted_content" {
		t.Errorf("include = %v, want reasoning.encrypted_content", got.Include)
	}
	if got.ToolChoice != "auto" {
		t.Errorf("tool_choice = %q, want auto", got.ToolChoice)
	}
	if !got.ParallelToolCalls {
		t.Error("parallel_tool_calls = false, want true")
	}
	if len(got.Tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(got.Tools))
	}
	if got.Tools[0].Strict != nil {
		t.Errorf("codex tool strict = %v, want null", got.Tools[0].Strict)
	}
	if got.PromptCacheKey != nil {
		t.Errorf("prompt_cache_key = %v, want absent without session", got.PromptCacheKey)
	}
}

// TestCodexModeDefaultInstructions verifies the Codex fallback
// instructions when the turn carries no system prompt.
func TestCodexModeDefaultInstructions(t *testing.T) {
	events := []string{
		`{"type":"response.created","response":{"id":"r"}}`,
		`{"type":"response.completed","response":{"id":"r","status":"completed","output":[]}}`,
	}
	srv, captured := captureServer(t, events...)
	defer srv.Close()

	d := New(Config{
		BaseURL:    srv.URL,
		ProviderID: "openai",
		API:        "openai-codex-responses",
		Codex:      true,
		Auth:       func(context.Context) (string, error) { return codexToken("acct_1"), nil },
	}, nil)
	if _, err := d.StreamTurn(context.Background(), baseTurnReq(), nil, nil); err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	var got struct {
		Instructions string `json:"instructions"`
	}
	if err := json.Unmarshal(captured.body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Instructions != "You are a helpful assistant." {
		t.Errorf("instructions = %q, want fallback", got.Instructions)
	}
}

// TestCodexModeBadToken verifies that a non-JWT credential in Codex mode
// aborts the turn before any request is sent.
func TestCodexModeBadToken(t *testing.T) {
	d := New(Config{
		BaseURL:    "https://example.com",
		ProviderID: "openai",
		API:        "openai-codex-responses",
		Codex:      true,
		Auth:       func(context.Context) (string, error) { return "not-a-jwt", nil },
	}, nil)
	_, err := d.StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "accountId") {
		t.Errorf("error = %v, want accountId extraction failure", err)
	}
}

// TestAzureMode verifies the Azure knobs: deployment URL shape,
// api-key auth header, and the deployment as model field.
func TestAzureMode(t *testing.T) {
	events := []string{
		`{"type":"response.created","response":{"id":"r"}}`,
		`{"type":"response.completed","response":{"id":"r","status":"completed","output":[]}}`,
	}
	srv, captured := captureServer(t, events...)
	defer srv.Close()

	d := New(Config{
		BaseURL:    srv.URL,
		ProviderID: "azure",
		API:        "azure-openai-responses",
		Azure:      true,
		Deployment: "my-deployment",
		APIVersion: "2025-03-01-preview",
		SessionID:  "sess_1",
		Auth:       func(context.Context) (string, error) { return "az-key", nil },
	}, nil)
	if _, err := d.StreamTurn(context.Background(), baseTurnReq(), nil, nil); err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	if captured.path != "/openai/deployments/my-deployment/responses" {
		t.Errorf("path = %q, want /openai/deployments/my-deployment/responses", captured.path)
	}
	if captured.query != "api-version=2025-03-01-preview" {
		t.Errorf("query = %q, want api-version", captured.query)
	}
	if got := captured.header.Get("api-key"); got != "az-key" {
		t.Errorf("api-key = %q, want az-key", got)
	}
	if got := captured.header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, want absent in azure mode", got)
	}
	var got struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(captured.body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Model != "my-deployment" {
		t.Errorf("model = %q, want deployment name", got.Model)
	}
}

// TestAzureModeRequiresDeployment verifies the deployment guard.
func TestAzureModeRequiresDeployment(t *testing.T) {
	d := New(Config{
		BaseURL:    "https://example.com",
		ProviderID: "azure",
		API:        "azure-openai-responses",
		Azure:      true,
		Auth:       func(context.Context) (string, error) { return "az-key", nil },
	}, nil)
	_, err := d.StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "deployment") {
		t.Errorf("error = %v, want deployment required", err)
	}
}

// TestExtractAccountID pins the JWT account id extraction.
func TestExtractAccountID(t *testing.T) {
	got, err := extractAccountID(codexToken("acct_7"))
	if err != nil || got != "acct_7" {
		t.Errorf("extractAccountID = %q, %v; want acct_7", got, err)
	}
	for _, bad := range []string{"", "no-dots", "a.b", "a.b.c", codexToken("")} {
		if _, err := extractAccountID(bad); err == nil {
			t.Errorf("extractAccountID(%q): want error", bad)
		}
	}
}

// TestClampPromptCacheKey pins the prompt_cache_key normalization,
// mirroring pi-ai's clampOpenAIPromptCacheKey.
func TestClampPromptCacheKey(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"sess_123", "sess_123"},
		{"with spaces and/slashes", "with_spaces_and_slashes"},
		{strings.Repeat("a", 80), strings.Repeat("a", 64)},
		{"trailing___", "trailing"},
	}
	for _, tc := range cases {
		if got := clampPromptCacheKey(tc.in); got != tc.want {
			t.Errorf("clampPromptCacheKey(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestCodexModeDefaultInclude verifies the Codex body always requests
// encrypted reasoning items even without an explicit include list.
func TestCodexModeDefaultInclude(t *testing.T) {
	events := []string{
		`{"type":"response.created","response":{"id":"r"}}`,
		`{"type":"response.completed","response":{"id":"r","status":"completed","output":[]}}`,
	}
	srv, captured := captureServer(t, events...)
	defer srv.Close()

	d := New(Config{
		BaseURL:    srv.URL,
		ProviderID: "openai",
		API:        "openai-codex-responses",
		Codex:      true,
		Auth:       func(context.Context) (string, error) { return codexToken("acct_1"), nil },
	}, nil)
	if _, err := d.StreamTurn(context.Background(), baseTurnReq(), nil, nil); err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	var got struct {
		Include []string `json:"include"`
	}
	if err := json.Unmarshal(captured.body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Include) != 1 || got.Include[0] != "reasoning.encrypted_content" {
		t.Errorf("include = %v, want reasoning.encrypted_content default", got.Include)
	}
}

// TestStreamTurnAuthError verifies that a failing Auth func aborts the
// turn before any request is sent.
func TestStreamTurnAuthError(t *testing.T) {
	authCalled := false
	d := New(Config{
		BaseURL:    "https://example.com",
		ProviderID: "openai",
		Auth: func(context.Context) (string, error) {
			authCalled = true
			return "", fmt.Errorf("no credential")
		},
	}, nil)
	_, err := d.StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "resolve credential") || !strings.Contains(err.Error(), "no credential") {
		t.Errorf("error = %v, want resolve credential failure", err)
	}
	if !authCalled {
		t.Error("auth func not called")
	}
}

// TestStreamTurnNilArguments guards the nil contract of StreamTurn.
func TestStreamTurnNilArguments(t *testing.T) {
	d := testDriver(t, "https://example.com")
	if _, err := d.StreamTurn(context.Background(), nil, nil, nil); err == nil {
		t.Error("nil request: want error")
	}
	req := baseTurnReq()
	req.Messages = nil
	if _, err := d.StreamTurn(context.Background(), req, nil, nil); err == nil {
		t.Error("nil messages: want error")
	}
}

// TestStreamTurnErrorPrefix verifies every error is prefixed with the
// provider id.
func TestStreamTurnErrorPrefix(t *testing.T) {
	d := New(Config{BaseURL: "https://example.com", ProviderID: "openai"}, nil)
	_, err := d.StreamTurn(context.Background(), nil, nil, nil)
	if err == nil || !strings.HasPrefix(err.Error(), "openai: ") {
		t.Errorf("error = %q, want openai: prefix", err)
	}
}

// TestStreamTurnNon200 verifies the HTTP error envelope is parsed and the
// status code and message surface in the error.
func TestStreamTurnNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"code":"invalid_api_key","message":"Incorrect API key provided","type":"invalid_request_error"}}`)
	}))
	defer srv.Close()

	d := testDriver(t, srv.URL)
	msg, err := d.StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if msg != nil {
		t.Errorf("msg = %+v, want nil on error", msg)
	}
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "401 Unauthorized") {
		t.Errorf("error %q missing status", err)
	}
	if !strings.Contains(err.Error(), "Incorrect API key provided") {
		t.Errorf("error %q missing message", err)
	}
	if !strings.Contains(err.Error(), "code invalid_api_key") {
		t.Errorf("error %q missing code", err)
	}
}

// TestStreamTurnNon200NonJSON verifies the fallback for error bodies that
// are not the provider envelope.
func TestStreamTurnNon200NonJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, "upstream exploded")
	}))
	defer srv.Close()

	d := testDriver(t, srv.URL)
	_, err := d.StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "502") || !strings.Contains(err.Error(), "upstream exploded") {
		t.Errorf("error = %q, want status and body", err)
	}
}
