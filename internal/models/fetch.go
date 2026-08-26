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

const OpenRouterModelsURL = "https://openrouter.ai/api/v1/models"

const maxModelsBody = 8 << 20

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

type wireModel struct {
	ID            string          `json:"id"`
	ContextLength json.RawMessage `json:"context_length"`
}

func (w wireModel) info() ModelInfo {
	return ModelInfo{
		ID:            w.ID,
		ContextWindow: lenientInt64(w.ContextLength),
		Provider:      providerOf(w.ID),
	}
}

func providerOf(id string) string {
	if i := strings.IndexByte(id, '/'); i > 0 {
		return id[:i]
	}
	return ""
}

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
