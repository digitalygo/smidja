package gemini

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

// baseTurnReq returns a minimal turn request with one user message.
func baseTurnReq() *agent.TurnRequest {
	return &agent.TurnRequest{
		Model: "gemini-2.5-pro",
		Messages: []*agent.Message{
			{User: &agent.UserMessage{Role: string(agent.RoleUser), Content: json.RawMessage(`"hi"`)}},
		},
	}
}

// testDriver returns a driver pointed at the given base URL with a fixed
// credential and identity.
func testDriver(t *testing.T, baseURL string) *Gemini {
	t.Helper()
	return New(Config{
		BaseURL:    baseURL,
		ProviderID: "gemini",
		API:        "google-generative-ai",
		APIKey: func(context.Context) (string, error) {
			return "key-test", nil
		},
	}, nil)
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

// TestNewDefaultClient checks the default http client shape and URL
// defaulting.
func TestNewDefaultClient(t *testing.T) {
	d := New(Config{BaseURL: "https://example.com/v1beta/", ProviderID: "gemini"}, nil)
	if d.baseURL != "https://example.com/v1beta" {
		t.Errorf("baseURL = %q, want trailing slash trimmed", d.baseURL)
	}
	if d.prefix != "gemini" {
		t.Errorf("prefix = %q, want provider id", d.prefix)
	}
	if d.http == nil {
		t.Fatal("default http client is nil")
	}
	d2 := New(Config{ProviderID: "gemini"}, nil)
	if d2.baseURL != defaultBaseURL {
		t.Errorf("baseURL = %q, want default %q", d2.baseURL, defaultBaseURL)
	}
}

// TestStreamTurnRequestShape drives one full turn and verifies the exact
// request the driver sends: method, path, query, headers, and body shape.
func TestStreamTurnRequestShape(t *testing.T) {
	events := []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]},"finishReason":"STOP"}],"responseId":"resp_1"}`,
	}
	srv, captured := captureServer(t, events...)
	defer srv.Close()

	d := New(Config{
		BaseURL:    srv.URL,
		ProviderID: "gemini",
		API:        "google-generative-ai",
		DefaultHeaders: map[string]string{
			"X-Custom": "yes",
		},
		APIKey: func(context.Context) (string, error) { return "key-test", nil },
	}, nil)

	req := &agent.TurnRequest{
		Model:  "gemini-2.5-pro",
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
			stubTool{name: "read", desc: "Reads a file", schema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)},
		},
	}

	msg, err := d.StreamTurn(context.Background(), req, nil, nil)
	if err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	if msg == nil || len(msg.Content) != 1 || msg.Content[0].Text != "hi" {
		t.Fatalf("msg = %+v, want single text block %q", msg, "hi")
	}
	if msg.API != "google-generative-ai" || msg.Provider != "gemini" {
		t.Errorf("identity = api %q provider %q", msg.API, msg.Provider)
	}

	if captured.method != http.MethodPost {
		t.Errorf("method = %q, want POST", captured.method)
	}
	if captured.path != "/models/gemini-2.5-pro:streamGenerateContent" {
		t.Errorf("path = %q, want streamGenerateContent endpoint", captured.path)
	}
	if captured.query != "alt=sse" {
		t.Errorf("query = %q, want alt=sse", captured.query)
	}
	if got := captured.header.Get("x-goog-api-key"); got != "key-test" {
		t.Errorf("x-goog-api-key = %q, want key-test", got)
	}
	if got := captured.header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := captured.header.Get("X-Custom"); got != "yes" {
		t.Errorf("X-Custom = %q, want default header applied", got)
	}

	var got struct {
		Contents []struct {
			Role  string `json:"role"`
			Parts []struct {
				Text             *string         `json:"text"`
				Thought          bool            `json:"thought"`
				FunctionCall     json.RawMessage `json:"functionCall"`
				FunctionResponse json.RawMessage `json:"functionResponse"`
			} `json:"parts"`
		} `json:"contents"`
		SystemInstruction struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"systemInstruction"`
		Tools []struct {
			FunctionDeclarations []struct {
				Name                 string          `json:"name"`
				Description          string          `json:"description"`
				ParametersJsonSchema json.RawMessage `json:"parametersJsonSchema"`
			} `json:"functionDeclarations"`
		} `json:"tools"`
		ThinkingConfig struct {
			IncludeThoughts bool `json:"includeThoughts"`
		} `json:"thinkingConfig"`
	}
	if err := json.Unmarshal(captured.body, &got); err != nil {
		t.Fatalf("unmarshal captured body: %v", err)
	}

	if len(got.Contents) != 3 {
		t.Fatalf("contents = %d, want 3", len(got.Contents))
	}
	if got.Contents[0].Role != "user" || got.Contents[0].Parts[0].Text == nil || *got.Contents[0].Parts[0].Text != "hello" {
		t.Errorf("contents[0] = %+v, want user text", got.Contents[0])
	}
	if got.Contents[1].Role != "model" {
		t.Errorf("contents[1] role = %q, want model", got.Contents[1].Role)
	}
	if got.Contents[2].Role != "user" || got.Contents[2].Parts[0].FunctionResponse == nil {
		t.Errorf("contents[2] = %+v, want user functionResponse", got.Contents[2])
	}
	if got.SystemInstruction.Parts[0].Text != "Be helpful" {
		t.Errorf("systemInstruction = %+v", got.SystemInstruction)
	}
	if len(got.Tools) != 1 || len(got.Tools[0].FunctionDeclarations) != 1 {
		t.Fatalf("tools = %+v, want one declaration", got.Tools)
	}
	decl := got.Tools[0].FunctionDeclarations[0]
	if decl.Name != "read" || decl.Description != "Reads a file" {
		t.Errorf("declaration = %+v", decl)
	}
	if !json.Valid(decl.ParametersJsonSchema) {
		t.Errorf("parametersJsonSchema = %s, want valid JSON schema", decl.ParametersJsonSchema)
	}
	if !got.ThinkingConfig.IncludeThoughts {
		t.Error("thinkingConfig.includeThoughts = false, want true")
	}
}

// TestStreamTurnAuthError verifies that a failing APIKey func aborts the
// turn before any request is sent.
func TestStreamTurnAuthError(t *testing.T) {
	authCalled := false
	d := New(Config{
		BaseURL:    "https://example.com",
		ProviderID: "gemini",
		APIKey: func(context.Context) (string, error) {
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
	d := New(Config{BaseURL: "https://example.com", ProviderID: "gemini"}, nil)
	_, err := d.StreamTurn(context.Background(), nil, nil, nil)
	if err == nil || !strings.HasPrefix(err.Error(), "gemini: ") {
		t.Errorf("error = %q, want gemini: prefix", err)
	}
}

// TestStreamTurnNon200 verifies the HTTP error envelope is parsed and the
// status code and message surface in the error.
func TestStreamTurnNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"error":{"code":403,"message":"API key not valid. Please pass a valid API key.","status":"PERMISSION_DENIED"}}`)
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
	if !strings.Contains(err.Error(), "403 Forbidden") {
		t.Errorf("error %q missing status", err)
	}
	if !strings.Contains(err.Error(), "API key not valid") {
		t.Errorf("error %q missing message", err)
	}
	if !strings.Contains(err.Error(), "code 403") {
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
