package openrouter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func captureReasoningBody(t *testing.T, ctx context.Context) map[string]json.RawMessage {
	t.Helper()
	events := []string{
		`{"id":"gen_1","choices":[{"index":0,"delta":{"content":"ok"}}]}`,
		`{"id":"gen_1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	srv, captured := captureServer(t, events...)
	defer srv.Close()
	client := New(srv.URL, "sk-test", nil)
	if _, err := client.StreamTurn(ctx, baseTurnReq(), nil, nil); err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(captured.body, &body); err != nil {
		t.Fatalf("decode body %s: %v", captured.body, err)
	}
	return body
}

func TestStreamTurnInjectsReasoningEffort(t *testing.T) {
	ctx := WithReasoning(context.Background(), ReasoningDirective{Effort: "high"})
	body := captureReasoningBody(t, ctx)
	if got := string(body["reasoning"]); got != `{"effort":"high"}` {
		t.Fatalf("reasoning = %s", got)
	}
	if string(body["model"]) != `"test/model"` {
		t.Fatalf("model = %s", body["model"])
	}
}

func TestStreamTurnInjectsDistinctMaxEffort(t *testing.T) {
	ctx := WithReasoning(context.Background(), ReasoningDirective{Effort: "max"})
	body := captureReasoningBody(t, ctx)
	if got := string(body["reasoning"]); got != `{"effort":"max"}` {
		t.Fatalf("reasoning = %s", got)
	}
}

func TestStreamTurnInjectsDisabledReasoning(t *testing.T) {
	ctx := WithReasoning(context.Background(), ReasoningDirective{Disable: true})
	body := captureReasoningBody(t, ctx)
	if got := string(body["reasoning"]); got != `{"enabled":false,"effort":"none"}` {
		t.Fatalf("reasoning = %s", got)
	}
}

func TestStreamTurnOmitsReasoningWhenUnset(t *testing.T) {
	body := captureReasoningBody(t, context.Background())
	if _, ok := body["reasoning"]; ok {
		t.Fatalf("unset request must omit reasoning: %s", body["reasoning"])
	}
}

func TestStreamTurnReasoningIsPerRequest(t *testing.T) {
	high := captureReasoningBody(t, WithReasoning(context.Background(), ReasoningDirective{Effort: "high"}))
	low := captureReasoningBody(t, WithReasoning(context.Background(), ReasoningDirective{Effort: "low"}))
	plain := captureReasoningBody(t, context.Background())
	if string(high["reasoning"]) == string(low["reasoning"]) {
		t.Fatal("reasoning directives leaked across requests")
	}
	if _, ok := plain["reasoning"]; ok {
		t.Fatal("a plain request inherited another request's reasoning")
	}
}

func TestInjectReasoningRejectsNonObjectBodies(t *testing.T) {
	for _, body := range []string{``, `[]`, `"text"`, `{`, `not json`} {
		patched, ok := injectReasoning([]byte(body), []byte(`{"effort":"high"}`))
		if ok || patched != nil {
			t.Fatalf("injectReasoning(%q) = %q, %v; want failure", body, patched, ok)
		}
	}
	patched, ok := injectReasoning([]byte(`{"model":"m"}`), []byte(`{"effort":"high"}`))
	if !ok {
		t.Fatalf("patched = %q, want success", patched)
	}
	var patchedObject map[string]json.RawMessage
	if err := json.Unmarshal(patched, &patchedObject); err != nil {
		t.Fatalf("patched JSON = %s: %v", patched, err)
	}
	if string(patchedObject["reasoning"]) != `{"effort":"high"}` || string(patchedObject["model"]) != `"m"` {
		t.Fatalf("patched = %s", patched)
	}
	patched, ok = injectReasoning([]byte(`{}`), []byte(`{"effort":"high"}`))
	if !ok || string(patched) != `{"reasoning":{"effort":"high"}}` {
		t.Fatalf("empty object patched = %s ok=%v", patched, ok)
	}
	patched, ok = injectReasoning([]byte(`{"reasoning":{"effort":"low"},"a":1}`), []byte(`{"effort":"high"}`))
	if !ok {
		t.Fatal("existing reasoning field must be replaceable")
	}
	if strings.Count(string(patched), `"reasoning"`) != 1 {
		t.Fatalf("duplicate reasoning field: %s", patched)
	}
	if err := json.Unmarshal(patched, &patchedObject); err != nil {
		t.Fatal(err)
	}
	if string(patchedObject["reasoning"]) != `{"effort":"high"}` {
		t.Fatalf("replaced reasoning = %s", patchedObject["reasoning"])
	}
}

func TestReasoningTransportPreservesGivenClient(t *testing.T) {
	calls := 0
	base := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, http.ErrSkipAltProtocol
	})
	given := &http.Client{Transport: base}
	client := New("https://example.com", "k", given)
	if client.http != given {
		t.Fatal("constructor must keep the provided client identity")
	}
	if given.Transport == nil {
		t.Fatal("constructor must not mutate the provided transport")
	}
	if _, ok := given.Transport.(roundTripperFunc); !ok {
		t.Fatalf("provided transport = %T, want the original decorator", given.Transport)
	}
	_, err := client.StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if !errors.Is(err, http.ErrSkipAltProtocol) {
		t.Fatalf("StreamTurn error = %v, want the provided transport error", err)
	}
	if calls != 1 {
		t.Fatalf("provided transport calls = %d, want 1", calls)
	}
}

func TestReasoningTransportLeavesPlainRequestsUntouched(t *testing.T) {
	var seen string
	base := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		seen = req.Method
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
	})
	transport := &reasoningTransport{base: base}
	req, err := http.NewRequest(http.MethodGet, "https://example.com", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transport.RoundTrip(req); err != nil {
		t.Fatal(err)
	}
	if seen != http.MethodGet {
		t.Fatalf("method = %s", seen)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type failingBody struct{}

func (failingBody) Read([]byte) (int, error) { return 0, errors.New("body read failed") }
func (failingBody) Close() error             { return nil }

type failingCloseBody struct{}

func (failingCloseBody) Read(p []byte) (int, error) { return copy(p, `{"a":1}`), io.EOF }
func (failingCloseBody) Close() error               { return errors.New("body close failed") }

func TestReasoningTransportDoesNotMutateCallerRequest(t *testing.T) {
	var received *http.Request
	base := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		received = req
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
	})
	transport := &reasoningTransport{base: base}
	original := strings.NewReader(`{"model":"m"}`)
	req, err := http.NewRequest(http.MethodPost, "https://example.com/chat/completions", original)
	if err != nil {
		t.Fatal(err)
	}
	length := req.ContentLength
	req = req.WithContext(WithReasoning(req.Context(), ReasoningDirective{Effort: "high"}))
	if _, err := transport.RoundTrip(req); err != nil {
		t.Fatal(err)
	}
	if received == nil {
		t.Fatal("base transport saw no request")
	}
	if received == req {
		t.Fatal("caller request must not be mutated by the transport")
	}
	if req.ContentLength != length {
		t.Fatalf("caller ContentLength = %d, want %d", req.ContentLength, length)
	}
	if req.GetBody == nil {
		t.Fatal("caller GetBody must stay available")
	}
	originalReplay, err := req.GetBody()
	if err != nil {
		t.Fatal(err)
	}
	originalData, _ := io.ReadAll(originalReplay)
	if string(originalData) != `{"model":"m"}` {
		t.Fatalf("caller request body was rewritten: %s", originalData)
	}
	if !strings.Contains(string(receivedBody(t, received)), `"reasoning"`) {
		t.Fatalf("forwarded body = %s", receivedBody(t, received))
	}
}

func receivedBody(t *testing.T, req *http.Request) []byte {
	t.Helper()
	if req.GetBody == nil {
		t.Fatal("forwarded request must expose GetBody")
	}
	reader, err := req.GetBody()
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestReasoningTransportScopesToConfiguredEndpoint(t *testing.T) {
	endpoint, err := url.Parse("https://example.com/chat/completions")
	if err != nil {
		t.Fatal(err)
	}
	var bodies []string
	base := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		bodies = append(bodies, string(body))
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
	})
	transport := &reasoningTransport{base: base, endpoint: endpoint}
	other, err := http.NewRequest(http.MethodPost, "https://example.com/other", strings.NewReader(`{"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	other = other.WithContext(WithReasoning(other.Context(), ReasoningDirective{Effort: "high"}))
	if _, err := transport.RoundTrip(other); err != nil {
		t.Fatal(err)
	}
	target, err := http.NewRequest(http.MethodPost, "https://example.com/chat/completions", strings.NewReader(`{"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	target = target.WithContext(WithReasoning(target.Context(), ReasoningDirective{Effort: "high"}))
	if _, err := transport.RoundTrip(target); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 {
		t.Fatalf("bodies = %v", bodies)
	}
	if bodies[0] != `{"a":1}` {
		t.Fatalf("off-endpoint POST was rewritten: %s", bodies[0])
	}
	if !strings.Contains(bodies[1], `"reasoning":{"effort":"high"}`) {
		t.Fatalf("on-endpoint POST was not rewritten: %s", bodies[1])
	}
}

func TestWithReasoningHandlesNilContext(t *testing.T) {
	ctx := WithReasoning(nil, ReasoningDirective{Effort: "low"})
	if ctx == nil {
		t.Fatal("nil context must be replaced")
	}
	directive, ok := reasoningFromContext(ctx)
	if !ok || directive.Effort != "low" {
		t.Fatalf("directive = %+v ok=%v", directive, ok)
	}
	if _, ok := reasoningFromContext(nil); ok {
		t.Fatal("nil context must not carry a directive")
	}
}

func TestReasoningTransportMethodAndBodyEdgeCases(t *testing.T) {
	type record struct {
		method string
		body   string
		clone  string
		replay string
	}
	var records []record
	base := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		recorded := record{method: req.Method, body: string(body), clone: req.URL.Path}
		if req.GetBody != nil {
			reader, err := req.GetBody()
			if err == nil {
				data, _ := io.ReadAll(reader)
				recorded.replay = string(data)
			}
		}
		records = append(records, recorded)
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
	})
	transport := &reasoningTransport{base: base}

	getReq, err := http.NewRequest(http.MethodGet, "https://example.com", strings.NewReader(`{"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	getReq = getReq.WithContext(WithReasoning(getReq.Context(), ReasoningDirective{Effort: "high"}))
	if _, err := transport.RoundTrip(getReq); err != nil {
		t.Fatal(err)
	}

	invalid, err := http.NewRequest(http.MethodPost, "https://example.com", strings.NewReader("not json"))
	if err != nil {
		t.Fatal(err)
	}
	invalid = invalid.WithContext(WithReasoning(invalid.Context(), ReasoningDirective{Effort: "high"}))
	if _, err := transport.RoundTrip(invalid); err != nil {
		t.Fatal(err)
	}

	valid, err := http.NewRequest(http.MethodPost, "https://example.com", strings.NewReader(`{"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	valid = valid.WithContext(WithReasoning(valid.Context(), ReasoningDirective{Effort: "high"}))
	if _, err := transport.RoundTrip(valid); err != nil {
		t.Fatal(err)
	}

	if len(records) != 3 {
		t.Fatalf("records = %+v", records)
	}
	if records[0].method != http.MethodGet || records[0].body != `{"a":1}` {
		t.Fatalf("GET record = %+v", records[0])
	}
	if records[1].body != "not json" {
		t.Fatalf("invalid body was rewritten: %s", records[1].body)
	}
	if !strings.Contains(records[2].body, `"reasoning":{"effort":"high"}`) {
		t.Fatalf("patched body = %s", records[2].body)
	}
	if !strings.Contains(records[2].replay, `"reasoning":{"effort":"high"}`) {
		t.Fatalf("GetBody replay = %s", records[2].replay)
	}
}

func TestReasoningTransportReadAndCloseErrors(t *testing.T) {
	transport := &reasoningTransport{base: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
	})}
	readReq, _ := http.NewRequest(http.MethodPost, "https://example.com", failingBody{})
	readReq = readReq.WithContext(WithReasoning(readReq.Context(), ReasoningDirective{Effort: "high"}))
	if _, err := transport.RoundTrip(readReq); err == nil || !strings.Contains(err.Error(), "body read failed") {
		t.Fatalf("read error = %v", err)
	}
	closeReq, _ := http.NewRequest(http.MethodPost, "https://example.com", failingCloseBody{})
	closeReq = closeReq.WithContext(WithReasoning(closeReq.Context(), ReasoningDirective{Effort: "high"}))
	if _, err := transport.RoundTrip(closeReq); err == nil || !strings.Contains(err.Error(), "body close failed") {
		t.Fatalf("close error = %v", err)
	}
}

func TestReasoningTransportFallsBackToDefaultTransport(t *testing.T) {
	events := []string{
		`{"id":"gen_1","choices":[{"index":0,"delta":{"content":"ok"}}]}`,
		`{"id":"gen_1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	srv, captured := captureServer(t, events...)
	defer srv.Close()
	client := New(srv.URL, "sk-test", &http.Client{})
	if _, err := client.StreamTurn(WithReasoning(context.Background(), ReasoningDirective{Effort: "high"}), baseTurnReq(), nil, nil); err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	if !strings.Contains(string(captured.body), `"reasoning":{"effort":"high"}`) {
		t.Fatalf("body = %s", captured.body)
	}
}

func TestParseReasoningEndpointRejectsMalformedURL(t *testing.T) {
	if endpoint := parseReasoningEndpoint("://missing-scheme"); endpoint != nil {
		t.Fatalf("malformed endpoint = %+v, want nil", endpoint)
	}
	if endpoint := parseReasoningEndpoint("https://user:secret@example.com/chat/completions"); endpoint == nil || endpoint.Host != "example.com" {
		t.Fatalf("valid endpoint = %+v", endpoint)
	}
}
