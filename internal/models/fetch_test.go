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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func fixtureClient() *http.Client {
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.String() != OpenRouterModelsURL {
			return nil, fmt.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		return jsonResponse(http.StatusOK, modelsFixture), nil
	})}
}

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
	m, ok = r.Get("anthropic/claude-opus-5")
	if !ok || m.ContextWindow != 1_000_000 {
		t.Errorf("fallback entry after merge = %+v ok=%v, want window 1000000", m, ok)
	}
}

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

func TestFetchOpenRouterModelsRequestError(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("connection refused")
	})}
	if _, err := FetchOpenRouterModels(context.Background(), client); err == nil {
		t.Fatal("expected an error for a failed request")
	}
}

func TestFetchOpenRouterModelsBadJSON(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, "not json at all"), nil
	})}
	if _, err := FetchOpenRouterModels(context.Background(), client); err == nil {
		t.Fatal("expected an error for an undecodable body")
	}
}

func TestFetchOpenRouterModelsMissingData(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"models": []}`), nil
	})}
	if _, err := FetchOpenRouterModels(context.Background(), client); err == nil {
		t.Fatal("expected an error for a missing data array")
	}
}

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
