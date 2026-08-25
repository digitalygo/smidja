package openrouter

import (
	"context"
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

func TestNewDefaultClient(t *testing.T) {
	c := New("https://openrouter.ai/api/v1/chat/completions/", "sk-test", nil)
	if c.baseURL != "https://openrouter.ai/api/v1/chat/completions" {
		t.Errorf("baseURL = %q, want trailing slash trimmed", c.baseURL)
	}
	if c.apiKey != "sk-test" {
		t.Errorf("apiKey = %q, want %q", c.apiKey, "sk-test")
	}
	if c.http == nil {
		t.Fatal("default http client is nil")
	}
	if c.http.Timeout != 0 {
		t.Errorf("http.Timeout = %v, want 0 (cancellation via request context)", c.http.Timeout)
	}
	tr, ok := c.http.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", c.http.Transport)
	}
	if tr.DialContext == nil {
		t.Error("default transport has no DialContext")
	}
	if tr.TLSHandshakeTimeout == 0 {
		t.Error("default transport has no TLS handshake timeout")
	}
}

func TestNewGivenClient(t *testing.T) {
	given := &http.Client{}
	c := New("https://example.com", "k", given)
	if c.http != given {
		t.Error("provided http client not used as-is")
	}
}

// TestStreamTurnRequestShape drives one full turn and verifies the exact
// request the client sends: method, path, headers, and body shape.
func TestStreamTurnRequestShape(t *testing.T) {
	events := []string{
		`{"id":"gen_1","choices":[{"index":0,"delta":{"content":"hi"}}]}`,
		`{"id":"gen_1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	srv, captured := captureServer(t, events...)
	defer srv.Close()

	req := &agent.TurnRequest{
		Model:  "anthropic/claude-sonnet-4.5",
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

	c := New(srv.URL, "sk-test", nil)
	msg, err := c.StreamTurn(context.Background(), req, nil, nil)
	if err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	if msg == nil || len(msg.Content) != 1 || msg.Content[0].Text != "hi" {
		t.Fatalf("msg = %+v, want single text block %q", msg, "hi")
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
	if got := captured.header.Get("HTTP-Referer"); got != "https://github.com/digitalygo/smidja" {
		t.Errorf("HTTP-Referer = %q", got)
	}
	if got := captured.header.Get("X-Title"); got != "smidja" {
		t.Errorf("X-Title = %q", got)
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
	if got.Messages[0].Role != "system" || string(got.Messages[0].Content) != `"Be helpful"` {
		t.Errorf("messages[0] = %+v, want system prompt first", got.Messages[0])
	}
	if got.Messages[1].Role != "user" || string(got.Messages[1].Content) != `"hello"` {
		t.Errorf("messages[1] = %+v, want user string content", got.Messages[1])
	}
	asst := got.Messages[2]
	if asst.Role != "assistant" || string(asst.Content) != `"Hi!"` {
		t.Errorf("messages[2] = %+v, want assistant with text", asst)
	}
	if len(asst.ToolCalls) != 1 {
		t.Fatalf("assistant tool_calls = %d, want 1", len(asst.ToolCalls))
	}
	tc := asst.ToolCalls[0]
	if tc.ID != "call_1" || tc.Type != "function" || tc.Function.Name != "read" ||
		tc.Function.Arguments != `{"path":"a.go"}` {
		t.Errorf("tool call = %+v, want id/type/function wired", tc)
	}
	tool := got.Messages[3]
	if tool.Role != "tool" || tool.ToolCallID != "call_1" || string(tool.Content) != `"file body"` {
		t.Errorf("messages[3] = %+v, want tool result", tool)
	}

	if len(got.Tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(got.Tools))
	}
	tw := got.Tools[0]
	if tw.Type != "function" || tw.Function.Name != "read" || tw.Function.Description != "Reads a file" ||
		string(tw.Function.Parameters) != `{"type":"object"}` {
		t.Errorf("tool = %+v, want function wiring", tw)
	}
}

// TestStreamTurnRequestPath uses a base URL with a path and verifies the
// client POSTs to it unchanged.
func TestStreamTurnRequestPath(t *testing.T) {
	events := []string{`[DONE]`}
	srv, captured := captureServer(t, events...)
	defer srv.Close()

	c := New(srv.URL+"/api/v1/chat/completions", "k", nil)
	if _, err := c.StreamTurn(context.Background(), baseTurnReq(), nil, nil); err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	if captured.path != "/api/v1/chat/completions" {
		t.Errorf("path = %q, want %q", captured.path, "/api/v1/chat/completions")
	}
}

// TestBuildMessages verifies the wire conversion of every message variant.
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
			got, err := json.Marshal(buildMessages(tc.system, tc.messages))
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("messages = %s\nwant         %s", got, tc.want)
			}
		})
	}
}

// TestBuildTools verifies the tool wire conversion, including the empty
// case which must omit the field entirely.
func TestBuildTools(t *testing.T) {
	empty := buildTools(nil)
	if empty != nil {
		t.Errorf("buildTools(nil) = %v, want nil", empty)
	}

	tools := buildTools([]agent.Tool{
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

// TestStreamTurnNilArguments guards the nil contract of StreamTurn.
func TestStreamTurnNilArguments(t *testing.T) {
	srv, _ := captureServer(t, `[DONE]`)
	defer srv.Close()
	c := New(srv.URL, "k", nil)

	if _, err := c.StreamTurn(context.Background(), nil, nil, nil); err == nil {
		t.Error("nil request: want error")
	}
	req := baseTurnReq()
	req.Messages = nil
	if _, err := c.StreamTurn(context.Background(), req, nil, nil); err == nil {
		t.Error("nil messages: want error")
	}
}

// TestStreamTurnNon200 verifies the HTTP error envelope is parsed and the
// status code and message surface in the error.
func TestStreamTurnNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"code":"401","message":"Invalid API key","metadata":{"provider_name":"OpenAI"}}}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "bad-key", nil)
	msg, err := c.StreamTurn(context.Background(), baseTurnReq(), nil, nil)
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

// TestStreamTurnNon200NonJSON verifies the fallback for error bodies that
// are not the OpenRouter envelope.
func TestStreamTurnNon200NonJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, "upstream exploded")
	}))
	defer srv.Close()

	c := New(srv.URL, "k", nil)
	_, err := c.StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "502") || !strings.Contains(err.Error(), "upstream exploded") {
		t.Errorf("error = %q, want status and body", err)
	}
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
	header http.Header
	body   []byte
}
