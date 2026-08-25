package models

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// modelsFixture is a realistic OpenRouter catalogue response: it carries
// unknown fields the decoder must ignore, context_length values encoded
// as integers, floats, and strings, and entries that must be skipped
// (empty id, zero and negative windows, undecodable values, and a model
// id without a provider prefix).
const modelsFixture = `{
  "data": [
    {"id": "anthropic/claude-sonnet-4.5", "name": "Claude Sonnet 4.5", "context_length": 200000, "pricing": {"prompt": "3", "completion": "15"}, "top_provider": {"context_length": 200000}},
    {"id": "openai/gpt-5", "context_length": 400000},
    {"id": "google/gemini-2.5-pro", "context_length": 1048576.0},
    {"id": "deepseek/deepseek-chat", "context_length": "163840"},
    {"id": "no-provider-model", "context_length": 1000},
    {"id": "", "context_length": 99999},
    {"id": "broken/zero", "context_length": 0},
    {"id": "broken/negative", "context_length": -1},
    {"id": "broken/text", "context_length": "NaN"},
    {"id": "broken/bool", "context_length": true}
  ]
}`

// roundTripFunc adapts a function to http.RoundTripper so fetch tests
// serve fixtures with no network and no server.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// jsonResponse builds a canned HTTP response with the given status and
// body.
func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// fixtureClient returns a client whose transport serves the catalogue
// fixture and asserts the request shape: GET on the catalogue endpoint.
func fixtureClient() *http.Client {
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.String() != OpenRouterModelsURL {
			return nil, fmt.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		return jsonResponse(http.StatusOK, modelsFixture), nil
	})}
}

// TestFetchOpenRouterModels checks tolerant decoding of the fixture:
// valid entries are parsed with the provider derived from the id prefix,
// unknown fields are ignored, and every invalid entry is skipped.
func TestFetchOpenRouterModels(t *testing.T) {
	got, err := FetchOpenRouterModels(context.Background(), fixtureClient())
	if err != nil {
		t.Fatalf("FetchOpenRouterModels: %v", err)
	}

	want := []ModelInfo{
		{ID: "anthropic/claude-sonnet-4.5", ContextWindow: 200_000, Provider: "anthropic"},
		{ID: "openai/gpt-5", ContextWindow: 400_000, Provider: "openai"},
		{ID: "google/gemini-2.5-pro", ContextWindow: 1_048_576, Provider: "google"},
		{ID: "deepseek/deepseek-chat", ContextWindow: 163_840, Provider: "deepseek"},
		{ID: "no-provider-model", ContextWindow: 1_000, Provider: ""},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], w)
		}
	}
}

// TestFetchOpenRouterModelsMerges checks that a fetched catalogue layers
// on top of the fallback registry: live values replace fallback entries,
// new entries are added, and fallback-only entries keep their values.
func TestFetchOpenRouterModelsMerges(t *testing.T) {
	fetched, err := FetchOpenRouterModels(context.Background(), fixtureClient())
	if err != nil {
		t.Fatalf("FetchOpenRouterModels: %v", err)
	}
	r := NewRegistry()
	r.Merge(fetched)

	m, ok := r.Get("anthropic/claude-sonnet-4.5")
	if !ok || m.ContextWindow != 200_000 {
		t.Errorf("merged default = %+v ok=%v, want window 200000", m, ok)
	}
	m, ok = r.Get("openai/gpt-5")
	if !ok || m.ContextWindow != 400_000 {
		t.Errorf("merged gpt-5 = %+v ok=%v, want window 400000", m, ok)
	}
	m, ok = r.Get("no-provider-model")
	if !ok || m.ContextWindow != 1_000 {
		t.Errorf("added entry = %+v ok=%v, want window 1000", m, ok)
	}
	// Fallback-only entries survive the merge.
	m, ok = r.Get("anthropic/claude-opus-5")
	if !ok || m.ContextWindow != 1_000_000 {
		t.Errorf("fallback entry after merge = %+v ok=%v, want window 1000000", m, ok)
	}
}

// TestFetchOpenRouterModelsServerError checks that a non-2xx status is
// reported as an error.
func TestFetchOpenRouterModelsServerError(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusInternalServerError, "boom"), nil
	})}
	_, err := FetchOpenRouterModels(context.Background(), client)
	if err == nil {
		t.Fatal("expected an error for a 500 response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error %q does not mention the status", err)
	}
}

// TestFetchOpenRouterModelsRequestError checks that a transport failure
// is reported as an error.
func TestFetchOpenRouterModelsRequestError(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("connection refused")
	})}
	if _, err := FetchOpenRouterModels(context.Background(), client); err == nil {
		t.Fatal("expected an error for a failed request")
	}
}

// TestFetchOpenRouterModelsBadJSON checks that an undecodable body is an
// error.
func TestFetchOpenRouterModelsBadJSON(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, "not json at all"), nil
	})}
	if _, err := FetchOpenRouterModels(context.Background(), client); err == nil {
		t.Fatal("expected an error for an undecodable body")
	}
}

// TestFetchOpenRouterModelsMissingData checks that a JSON body without a
// data array is an error.
func TestFetchOpenRouterModelsMissingData(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"models": []}`), nil
	})}
	if _, err := FetchOpenRouterModels(context.Background(), client); err == nil {
		t.Fatal("expected an error for a missing data array")
	}
}

// TestFetchOpenRouterModelsEmptyData checks that an empty catalogue is a
// valid, empty result.
func TestFetchOpenRouterModelsEmptyData(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"data": []}`), nil
	})}
	got, err := FetchOpenRouterModels(context.Background(), client)
	if err != nil {
		t.Fatalf("FetchOpenRouterModels: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d entries, want 0", len(got))
	}
}

// TestFetchOpenRouterModelsCancelled checks that the caller context is
// bound to the HTTP request, so a ctx-aware transport aborts the fetch
// when the context is cancelled.
func TestFetchOpenRouterModelsCancelled(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := r.Context().Err(); err != nil {
			return nil, err
		}
		return jsonResponse(http.StatusOK, `{"data": []}`), nil
	})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := FetchOpenRouterModels(ctx, client)
	if err == nil {
		t.Fatal("expected an error for a cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}

// TestFetchOpenRouterModelsNilClient checks that a nil client falls back
// to http.DefaultClient. It mutates the global DefaultClient, so it must
// not run in parallel with other fetch tests.
func TestFetchOpenRouterModelsNilClient(t *testing.T) {
	orig := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"data": [{"id": "x/y", "context_length": 7}]}`), nil
	})}
	t.Cleanup(func() { http.DefaultClient = orig })

	got, err := FetchOpenRouterModels(context.Background(), nil)
	if err != nil {
		t.Fatalf("FetchOpenRouterModels: %v", err)
	}
	if len(got) != 1 || got[0].ID != "x/y" || got[0].ContextWindow != 7 {
		t.Errorf("got %+v, want one entry x/y with window 7", got)
	}
}
