package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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
		Model: "test/model",
		Messages: []*agent.Message{
			{User: &agent.UserMessage{Role: string(agent.RoleUser), Content: json.RawMessage(`"hi"`)}},
		},
	}
}

// testDriver returns a driver pointed at the given base URL with a fixed
// credential and identity.
func testDriver(t *testing.T, baseURL string) *OpenAICompletions {
	t.Helper()
	return NewOpenAICompletions(Config{
		BaseURL:    baseURL,
		ProviderID: "test-provider",
		API:        "openai-completions",
		Auth: func(context.Context) (string, error) {
			return "sk-test", nil
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
