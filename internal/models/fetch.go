package models

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// OpenRouterModelsURL is the public OpenRouter model catalogue endpoint.
// It requires no authentication.
const OpenRouterModelsURL = "https://openrouter.ai/api/v1/models"

// maxModelsBody caps the catalogue response at 8 MiB. The live catalogue
// is a few megabytes, so the cap only guards against a runaway endpoint.
const maxModelsBody = 8 << 20

// FetchOpenRouterModels downloads the live OpenRouter model catalogue and
// returns it as ModelInfo entries, ready to be merged into a Registry.
// A nil client uses http.DefaultClient; a nil context is replaced by
// context.Background().
//
// The response is decoded tolerantly: unknown fields are ignored, and
// context_length values encoded as JSON integers, floats, or strings are
// all accepted. Entries without an id or with a non-positive
// context_length are skipped, so unknown entries never reach a merge.
// The provider is derived from the id prefix, the part before the first
// "/". Fetch reports an error only for a failed request, a non-2xx
// status, or a body that is not a JSON object carrying a data array.
func FetchOpenRouterModels(ctx context.Context, client *http.Client) ([]ModelInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, OpenRouterModelsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("models: build request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("models: fetch %s: %w", OpenRouterModelsURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("models: fetch %s: status %s", OpenRouterModelsURL, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxModelsBody))
	if err != nil {
		return nil, fmt.Errorf("models: read body: %w", err)
	}
	var envelope struct {
		Data []wireModel `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("models: decode %s: %w", OpenRouterModelsURL, err)
	}
	if envelope.Data == nil {
		return nil, fmt.Errorf("models: decode %s: missing data array", OpenRouterModelsURL)
	}
	out := make([]ModelInfo, 0, len(envelope.Data))
	for _, w := range envelope.Data {
		m := w.info()
		if m.ID == "" || m.ContextWindow <= 0 {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

// wireModel is one entry of the OpenRouter catalogue. The full response
// carries far more fields (name, description, pricing, architecture,
// top_provider); decoding into this struct ignores them all.
type wireModel struct {
	ID            string          `json:"id"`
	ContextLength json.RawMessage `json:"context_length"`
}

// info converts a wire entry to a ModelInfo, deriving the provider from
// the id prefix. ContextLength is decoded leniently (JSON integer, float,
// or string-encoded number); undecodable values decode to zero so the
// entry is skipped by the caller.
func (w wireModel) info() ModelInfo {
	return ModelInfo{
		ID:            w.ID,
		ContextWindow: lenientInt64(w.ContextLength),
		Provider:      providerOf(w.ID),
	}
}

// providerOf returns the provider identifier embedded in an OpenRouter
// model id: the prefix before the first "/", or "" when absent.
func providerOf(id string) string {
	if i := strings.IndexByte(id, '/'); i > 0 {
		return id[:i]
	}
	return ""
}

// lenientInt64 decodes a JSON value as an int64, accepting integers,
// floats, and string-encoded numbers. Zero is returned for null, empty,
// and undecodable values.
func lenientInt64(raw json.RawMessage) int64 {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return 0
	}
	var i int64
	if err := json.Unmarshal(raw, &i); err == nil {
		return i
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return int64(f)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil {
			return v
		}
	}
	return 0
}
