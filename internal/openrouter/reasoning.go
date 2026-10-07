package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type reasoningKey struct{}

type ReasoningDirective struct {
	Disable bool

	Effort string
}

func WithReasoning(ctx context.Context, directive ReasoningDirective) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, reasoningKey{}, directive)
}

func reasoningFromContext(ctx context.Context) (ReasoningDirective, bool) {
	if ctx == nil {
		return ReasoningDirective{}, false
	}
	directive, ok := ctx.Value(reasoningKey{}).(ReasoningDirective)
	return directive, ok
}

type wireReasoning struct {
	Enabled *bool  `json:"enabled,omitempty"`
	Effort  string `json:"effort"`
}

func reasoningJSON(directive ReasoningDirective) []byte {
	payload := wireReasoning{Effort: directive.Effort}
	if directive.Disable {
		disabled := false
		payload.Enabled = &disabled
		payload.Effort = "none"
	}
	raw, _ := json.Marshal(payload)
	return raw
}

type reasoningTransport struct {
	base     http.RoundTripper
	endpoint *url.URL
}

func (t *reasoningTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	directive, ok := reasoningFromContext(req.Context())
	if !ok || req.Method != http.MethodPost || req.Body == nil || !eligibleReasoningRequest(req.URL, t.endpoint) {
		return base.RoundTrip(req)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	if closeErr := req.Body.Close(); closeErr != nil {
		return nil, closeErr
	}
	patched, ok := injectReasoning(body, reasoningJSON(directive))
	if !ok {
		patched = body
	}
	clone := req.Clone(req.Context())
	clone.Body = io.NopCloser(bytes.NewReader(patched))
	clone.ContentLength = int64(len(patched))
	clone.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(patched)), nil
	}
	return base.RoundTrip(clone)
}

func eligibleReasoningRequest(target, endpoint *url.URL) bool {
	if target == nil {
		return false
	}
	if endpoint == nil {
		return true
	}
	return target.Scheme == endpoint.Scheme && target.Host == endpoint.Host &&
		target.RawQuery == endpoint.RawQuery && normalizedURLPath(target) == normalizedURLPath(endpoint)
}

func normalizedURLPath(target *url.URL) string {
	return strings.TrimRight(target.Path, "/")
}

func injectReasoning(body, rawReasoning []byte) ([]byte, bool) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil || object == nil {
		return nil, false
	}
	object["reasoning"] = append(json.RawMessage(nil), rawReasoning...)
	patched, err := json.Marshal(object)
	if err != nil {
		return nil, false
	}
	return patched, true
}
