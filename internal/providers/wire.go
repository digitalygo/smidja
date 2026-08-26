package providers

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// ChatRequest is the OpenAI chat completions request body shared by every
// openai-completions dialect provider.
type ChatRequest struct {
	Model         string        `json:"model"`
	Messages      []WireMessage `json:"messages"`
	Tools         []WireTool    `json:"tools,omitempty"`
	ToolChoice    string        `json:"tool_choice"`
	Stream        bool          `json:"stream"`
	StreamOptions StreamOptions `json:"stream_options"`
}

// StreamOptions requests per-chunk usage on the final stream event.
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// WireMessage is one conversation message. Content is a JSON string or an
// array of content parts; ToolCalls and ToolCallID are set for assistant
// and tool messages respectively.
type WireMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content,omitempty"`
	ToolCalls  []WireToolCall  `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

// WirePart is one content part of a user message.
type WirePart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// WireToolCall is a function invocation requested by the model.
type WireToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function WireFunctionCall `json:"function"`
}

// WireFunctionCall carries the tool name and JSON-encoded arguments.
type WireFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// WireTool is a tool exposed to the model.
type WireTool struct {
	Type     string       `json:"type"`
	Function WireFunction `json:"function"`
}

// WireFunction is the tool metadata sent to the model.
type WireFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// WireErrorEnvelope is the provider error body shape.
type WireErrorEnvelope struct {
	Error WireError `json:"error"`
}

// WireError describes a provider error. It appears both in HTTP error
// bodies and as an SSE data event after a 200 status.
type WireError struct {
	Code     string          `json:"code"`
	Message  string          `json:"message"`
	Metadata json.RawMessage `json:"metadata"`
}

// WireChunk is one SSE data payload of a streaming completion.
type WireChunk struct {
	ID      string       `json:"id"`
	Choices []WireChoice `json:"choices"`
	Usage   *WireUsage   `json:"usage"`
	Error   *WireError   `json:"error"`
}

// WireChoice is one completion choice of a stream chunk.
type WireChoice struct {
	Index        int       `json:"index"`
	Delta        WireDelta `json:"delta"`
	FinishReason string    `json:"finish_reason"`
}

// WireDelta is the incremental output of one chunk. Content and Reasoning
// may be null; ToolCalls carries tool-call fragments keyed by index.
type WireDelta struct {
	Content   *string             `json:"content"`
	Reasoning *string             `json:"reasoning"`
	ToolCalls []WireDeltaToolCall `json:"tool_calls"`
}

// WireDeltaToolCall is one tool-call fragment. Index identifies the tool
// call across fragments; ID and the function name arrive on the first
// fragment; Arguments accumulates in pieces.
type WireDeltaToolCall struct {
	Index    *int   `json:"index"`
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// WireUsage carries the token accounting. With stream_options.include_usage
// it arrives on a final chunk with an empty choices array. The detail and
// cost objects are optional and every shape is tolerated, so a variant
// usage payload never aborts a healthy stream.
type WireUsage struct {
	PromptTokens            int64                        `json:"prompt_tokens"`
	CompletionTokens        int64                        `json:"completion_tokens"`
	TotalTokens             int64                        `json:"total_tokens"`
	PromptTokensDetails     *WirePromptTokensDetails     `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *WireCompletionTokensDetails `json:"completion_tokens_details,omitempty"`
	Cost                    *WireCost                    `json:"cost,omitempty"`
}

// WirePromptTokensDetails breaks down prompt tokens. OpenRouter reports
// cache reads both flat (cached_tokens) and nested under cache_read, and
// cache writes under cache_creation; the custom unmarshaler accepts every
// shape so a usage chunk never aborts a healthy stream.
type WirePromptTokensDetails struct {
	CachedTokens int64 // top-level cached_tokens
	CacheRead    int64 // cache_read.cached_tokens
	CacheWrite   int64 // cache_creation.cached_tokens
}

// UnmarshalJSON reads the flat and nested breakdown shapes without error;
// null, missing, and non-object values leave the breakdown zero.
func (d *WirePromptTokensDetails) UnmarshalJSON(data []byte) error {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil
	}
	d.CachedTokens = rawInt(obj, "cached_tokens")
	d.CacheRead = nestedCachedTokens(obj, "cache_read")
	d.CacheWrite = nestedCachedTokens(obj, "cache_creation")
	return nil
}

// nestedCachedTokens decodes cached_tokens from a nested breakdown object
// named by key, returning zero when the key is absent, null, or not an
// object.
func nestedCachedTokens(obj map[string]json.RawMessage, key string) int64 {
	raw, ok := obj[key]
	if !ok {
		return 0
	}
	var nested map[string]json.RawMessage
	if err := json.Unmarshal(raw, &nested); err != nil {
		return 0
	}
	return rawInt(nested, "cached_tokens")
}

// WireCompletionTokensDetails breaks down completion tokens, including
// reasoning tokens. Providers disagree on the key (reasoning vs
// reasoning_tokens); the custom unmarshaler accepts both and tolerates
// null, missing, and non-object shapes without error.
type WireCompletionTokensDetails struct {
	ReasoningTokens int64
}

// UnmarshalJSON reads either common spelling of the reasoning token count;
// any other shape leaves the breakdown zero rather than aborting the
// stream.
func (d *WireCompletionTokensDetails) UnmarshalJSON(data []byte) error {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil
	}
	d.ReasoningTokens = rawInt(obj, "reasoning", "reasoning_tokens")
	return nil
}

// WireCost is the monetary accounting of one turn in USD. OpenRouter
// normally reports an object with per-field totals, but some providers
// send a bare number (the total) or nothing at all; the custom unmarshaler
// accepts every shape so a usage chunk never aborts a healthy stream.
type WireCost struct {
	Input      float64
	Output     float64
	CacheRead  float64
	CacheWrite float64
	Total      float64
}

// UnmarshalJSON accepts an object {input, output, cache_read, cache_write,
// total}, a bare number treated as the total, a string-encoded number, or
// null/missing, all without error.
func (c *WireCost) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	// A bare number is the total cost.
	var total float64
	if err := json.Unmarshal(trimmed, &total); err == nil {
		c.Total = total
		return nil
	}
	// Tolerate a string-encoded number as the total.
	var s string
	if err := json.Unmarshal(trimmed, &s); err == nil {
		if f, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
			c.Total = f
		}
		return nil
	}
	// Object shape with per-field accounting; unknown shapes are ignored.
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return nil
	}
	c.Input = rawFloat(obj, "input")
	c.Output = rawFloat(obj, "output")
	c.CacheRead = rawFloat(obj, "cache_read", "cacheRead")
	c.CacheWrite = rawFloat(obj, "cache_write", "cacheWrite")
	c.Total = rawFloat(obj, "total")
	return nil
}

// rawFloat decodes the first present key of names in obj as a float64,
// tolerating string-encoded numbers; it returns zero when the key is
// absent or not numeric.
func rawFloat(obj map[string]json.RawMessage, names ...string) float64 {
	for _, name := range names {
		raw, ok := obj[name]
		if !ok {
			continue
		}
		var f float64
		if err := json.Unmarshal(raw, &f); err == nil {
			return f
		}
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			if f, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
				return f
			}
		}
		return 0
	}
	return 0
}

// rawInt decodes the first present key of names in obj as an int64,
// tolerating float-encoded integers; it returns zero when the key is
// absent or not numeric.
func rawInt(obj map[string]json.RawMessage, names ...string) int64 {
	for _, name := range names {
		raw, ok := obj[name]
		if !ok {
			continue
		}
		var i int64
		if err := json.Unmarshal(raw, &i); err == nil {
			return i
		}
		var f float64
		if err := json.Unmarshal(raw, &f); err == nil {
			return int64(f)
		}
		return 0
	}
	return 0
}
