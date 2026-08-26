package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
)

func TestNewOpenAICompletionsDefaultClient(t *testing.T) {
	d := NewOpenAICompletions(Config{BaseURL: "https://api.example.com/v1/chat/completions/", ProviderID: "p"}, nil)
	if d.baseURL != "https://api.example.com/v1/chat/completions" {
		t.Errorf("baseURL = %q, want trailing slash trimmed", d.baseURL)
	}
	if d.prefix != "p" {
		t.Errorf("prefix = %q, want provider id", d.prefix)
	}
	if d.http == nil {
		t.Fatal("default http client is nil")
	}
	if d.http.Timeout != 0 {
		t.Errorf("http.Timeout = %v, want 0 (cancellation via request context)", d.http.Timeout)
	}
	tr, ok := d.http.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", d.http.Transport)
	}
	if tr.DialContext == nil {
		t.Error("default transport has no DialContext")
	}
	if tr.TLSHandshakeTimeout == 0 {
		t.Error("default transport has no TLS handshake timeout")
	}
}

func TestNewOpenAICompletionsGivenClient(t *testing.T) {
	given := &http.Client{}
	d := NewOpenAICompletions(Config{BaseURL: "https://example.com", ProviderID: "p"}, given)
	if d.http != given {
		t.Error("provided http client not used as-is")
	}
}

func TestNewOpenAICompletionsEmptyProvider(t *testing.T) {
	d := NewOpenAICompletions(Config{BaseURL: "https://example.com"}, nil)
	if d.prefix != "provider" {
		t.Errorf("prefix = %q, want fallback %q", d.prefix, "provider")
	}
}

func TestStreamTurnRequestShape(t *testing.T) {
	events := []string{
		`{"id":"gen_1","choices":[{"index":0,"delta":{"content":"hi"}}]}`,
		`{"id":"gen_1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	srv, captured := captureServer(t, events...)
	defer srv.Close()

	d := NewOpenAICompletions(Config{
		BaseURL:    srv.URL,
		ProviderID: "test-provider",
		API:        "openai-completions",
		DefaultHeaders: map[string]string{
			"X-Custom": "yes",
		},
		Auth: func(context.Context) (string, error) { return "sk-test", nil },
	}, nil)

	req := &agent.TurnRequest{
		Model:  "provider/model-1",
		System: "Be helpful",
		Messages: []*agent.Message{
			{User: &agent.UserMessage{Role: string(agent.RoleUser), Content: json.RawMessage(`"hello"`)}},
			{Assistant: &agent.AssistantMessage{Role: string(agent.RoleAssistant), Content: []agent.ContentBlock{
				{Type: agent.BlockTypeText, Text: "Hi!"},
				{Type: agent.BlockTypeToolCall, ID: "call_1", Name: "read", Arguments: json.RawMessage(`{"path":"a.go"}`)},
			}}},
			{ToolResult: &agent.ToolResultMessage{
				Role:       string(agent.RoleToolResult),
				ToolCallID: "call_1",
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
	if msg.API != "openai-completions" || msg.Provider != "test-provider" {
		t.Errorf("identity = api %q provider %q", msg.API, msg.Provider)
	}

	if captured.method != http.MethodPost {
		t.Errorf("method = %q, want POST", captured.method)
	}
	if captured.path != "/" {
		t.Errorf("path = %q, want %q", captured.path, "/")
	}
	if got := captured.header.Get("Authorization"); got != "Bearer sk-test" {
		t.Errorf("Authorization = %q", got)
	}
	if got := captured.header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := captured.header.Get("X-Custom"); got != "yes" {
		t.Errorf("X-Custom = %q, want default header applied", got)
	}

	var got struct {
		Model    string `json:"model"`
		Messages []struct {
			Role      string          `json:"role"`
			Content   json.RawMessage `json:"content"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
			ToolCallID string `json:"tool_call_id"`
		} `json:"messages"`
		Tools []struct {
			Type     string `json:"type"`
			Function struct {
				Name        string          `json:"name"`
				Description string          `json:"description"`
				Parameters  json.RawMessage `json:"parameters"`
			} `json:"function"`
		} `json:"tools"`
		ToolChoice    string `json:"tool_choice"`
		Stream        bool   `json:"stream"`
		StreamOptions struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
	}
	if err := json.Unmarshal(captured.body, &got); err != nil {
		t.Fatalf("unmarshal captured body: %v", err)
	}

	if got.Model != req.Model {
		t.Errorf("model = %q, want %q", got.Model, req.Model)
	}
	if got.ToolChoice != "auto" {
		t.Errorf("tool_choice = %q, want auto", got.ToolChoice)
	}
	if !got.Stream {
		t.Error("stream = false, want true")
	}
	if !got.StreamOptions.IncludeUsage {
		t.Error("stream_options.include_usage = false, want true")
	}
	if len(got.Messages) != 4 {
		t.Fatalf("messages = %d, want 4", len(got.Messages))
	}
	if got.Messages[1].Role != "user" || string(got.Messages[1].Content) != `"hello"` {
		t.Errorf("messages[1] = %+v, want user string content", got.Messages[1])
	}
	if len(got.Tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(got.Tools))
	}
}

func TestStreamTurnAuthError(t *testing.T) {
	authCalled := false
	d := NewOpenAICompletions(Config{
		BaseURL:    "https://example.com",
		ProviderID: "p",
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

func TestStreamTurnErrorPrefix(t *testing.T) {
	d := NewOpenAICompletions(Config{BaseURL: "https://example.com", ProviderID: "deepseek"}, nil)
	_, err := d.StreamTurn(context.Background(), nil, nil, nil)
	if err == nil || !strings.HasPrefix(err.Error(), "deepseek: ") {
		t.Errorf("error = %q, want deepseek: prefix", err)
	}
}

func TestStreamTurnNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"code":"401","message":"Invalid API key","metadata":{"provider_name":"OpenAI"}}}`)
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
	if !strings.Contains(err.Error(), "Invalid API key") {
		t.Errorf("error %q missing message", err)
	}
	if !strings.Contains(err.Error(), "code 401") {
		t.Errorf("error %q missing code", err)
	}
}

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

func TestBuildMessages(t *testing.T) {
	cases := []struct {
		name     string
		system   string
		messages []*agent.Message
		want     string
	}{
		{
			name:     "system and user string",
			system:   "Be helpful",
			messages: []*agent.Message{{User: &agent.UserMessage{Content: json.RawMessage(`"hi"`)}}},
			want:     `[{"role":"system","content":"Be helpful"},{"role":"user","content":"hi"}]`,
		},
		{
			name:   "user block array flattened",
			system: "",
			messages: []*agent.Message{{User: &agent.UserMessage{Content: json.RawMessage(
				`[{"type":"text","text":"part one"},{"type":"text","text":"part two"}]`)}}},
			want: `[{"role":"user","content":[{"type":"text","text":"part one"},{"type":"text","text":"part two"}]}]`,
		},
		{
			name:   "assistant text and tool call",
			system: "",
			messages: []*agent.Message{{Assistant: &agent.AssistantMessage{Content: []agent.ContentBlock{
				{Type: agent.BlockTypeText, Text: "Sure, "},
				{Type: agent.BlockTypeText, Text: "reading now"},
				{Type: agent.BlockTypeToolCall, ID: "call_1", Name: "read", Arguments: json.RawMessage(`{"path":"a.go"}`)},
			}}}},
			want: `[{"role":"assistant","content":"Sure, reading now","tool_calls":[` +
				`{"id":"call_1","type":"function","function":{"name":"read","arguments":"{\"path\":\"a.go\"}"}}]}]`,
		},
		{
			name:   "assistant tool call without text content is null",
			system: "",
			messages: []*agent.Message{{Assistant: &agent.AssistantMessage{Content: []agent.ContentBlock{
				{Type: agent.BlockTypeToolCall, ID: "c", Name: "exec", Arguments: json.RawMessage(`{}`)},
			}}}},
			want: `[{"role":"assistant","content":null,"tool_calls":[` +
				`{"id":"c","type":"function","function":{"name":"exec","arguments":"{}"}}]}]`,
		},
		{
			name:     "assistant without content is null",
			system:   "",
			messages: []*agent.Message{{Assistant: &agent.AssistantMessage{Content: nil}}},
			want:     `[{"role":"assistant","content":null}]`,
		},
		{
			name:   "tool result concatenates text blocks",
			system: "",
			messages: []*agent.Message{{ToolResult: &agent.ToolResultMessage{
				ToolCallID: "call_1",
				Content:    []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "a"}, {Type: agent.BlockTypeText, Text: "b"}},
			}}},
			want: `[{"role":"tool","content":"ab","tool_call_id":"call_1"}]`,
		},
		{
			name:     "empty system omits system message",
			system:   "",
			messages: []*agent.Message{{User: &agent.UserMessage{Content: json.RawMessage(`"hi"`)}}},
			want:     `[{"role":"user","content":"hi"}]`,
		},
		{
			name:     "empty conversation is empty array",
			system:   "",
			messages: []*agent.Message{},
			want:     `[]`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.Marshal(BuildMessages(tc.system, tc.messages))
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("messages = %s\nwant         %s", got, tc.want)
			}
		})
	}
}

func TestBuildTools(t *testing.T) {
	if empty := BuildTools(nil); empty != nil {
		t.Errorf("BuildTools(nil) = %v, want nil", empty)
	}
	tools := BuildTools([]agent.Tool{
		stubTool{name: "read", desc: "Reads a file", schema: json.RawMessage(`{"type":"object"}`)},
	})
	got, err := json.Marshal(tools)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `[{"type":"function","function":{"name":"read","description":"Reads a file","parameters":{"type":"object"}}}]`
	if string(got) != want {
		t.Errorf("tools = %s\nwant    %s", got, want)
	}
}

func TestDefaultHTTPClientShared(t *testing.T) {
	c := DefaultHTTPClient()
	if c.Timeout != 0 {
		t.Errorf("Timeout = %v, want 0", c.Timeout)
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", c.Transport)
	}
	if tr.DialContext == nil || tr.TLSHandshakeTimeout == 0 {
		t.Error("default transport missing generous timeouts")
	}
}
