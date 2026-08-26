package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
)

func postJSON(ctx context.Context, endpoint string, body any) (*http.Response, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	request.Header.Set("accept", "application/json")
	request.Header.Set("content-type", "application/json")
	return http.DefaultClient.Do(request)
}

func postForm(ctx context.Context, endpoint string, form url.Values) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	request.Header.Set("accept", "application/json")
	request.Header.Set("content-type", "application/x-www-form-urlencoded")
	return http.DefaultClient.Do(request)
}

func readBody(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func decodeMap(body []byte) map[string]any {
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil
	}
	return parsed
}

func errorDetail(body map[string]any) string {
	if detail, ok := body["error_description"].(string); ok && detail != "" {
		return detail
	}
	if detail, ok := body["message"].(string); ok && detail != "" {
		return detail
	}
	switch detail := body["error"].(type) {
	case string:
		return detail
	case map[string]any:
		if message, ok := detail["message"].(string); ok {
			return message
		}
	}
	return ""
}

func stringField(body map[string]any, field string) string {
	value, _ := body[field].(string)
	return value
}

func isFinite(value float64) bool {
	return !math.IsInf(value, 0) && !math.IsNaN(value)
}
