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

type stubTool struct {
	name   string
	desc   string
	schema json.RawMessage
}

func (s stubTool) Name() string                                       { return s.name }
func (s stubTool) Description() string                                { return s.desc }
func (s stubTool) Schema() json.RawMessage                            { return s.schema }
func (s stubTool) Exec(context.Context, json.RawMessage) agent.Result { return agent.Result{} }

func baseTurnReq() *agent.TurnRequest {
	return &agent.TurnRequest{
		Model: "test/model",
		Messages: []*agent.Message{
			{User: &agent.UserMessage{Role: string(agent.RoleUser), Content: json.RawMessage(`"hi"`)}},
		},
	}
}

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

type capturedRequest struct {
	method string
	path   string
	header http.Header
	body   []byte
}

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
