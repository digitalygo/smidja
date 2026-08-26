package providers

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

type ChatRequest struct {
	Model         string        `json:"model"`
	Messages      []WireMessage `json:"messages"`
	Tools         []WireTool    `json:"tools,omitempty"`
	ToolChoice    string        `json:"tool_choice"`
	Stream        bool          `json:"stream"`
	StreamOptions StreamOptions `json:"stream_options"`
}

type StreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type WireMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content,omitempty"`
	ToolCalls  []WireToolCall  `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

type WirePart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type WireToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function WireFunctionCall `json:"function"`
}

type WireFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type WireTool struct {
	Type     string       `json:"type"`
	Function WireFunction `json:"function"`
}

type WireFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type WireErrorEnvelope struct {
	Error WireError `json:"error"`
}

type WireError struct {
	Code     string          `json:"code"`
	Message  string          `json:"message"`
	Metadata json.RawMessage `json:"metadata"`
}

type WireChunk struct {
	ID      string       `json:"id"`
	Choices []WireChoice `json:"choices"`
	Usage   *WireUsage   `json:"usage"`
	Error   *WireError   `json:"error"`
}

type WireChoice struct {
	Index        int       `json:"index"`
	Delta        WireDelta `json:"delta"`
	FinishReason string    `json:"finish_reason"`
}

type WireDelta struct {
	Content   *string             `json:"content"`
	Reasoning *string             `json:"reasoning"`
	ToolCalls []WireDeltaToolCall `json:"tool_calls"`
}

type WireDeltaToolCall struct {
	Index    *int   `json:"index"`
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type WireUsage struct {
	PromptTokens            int64                        `json:"prompt_tokens"`
	CompletionTokens        int64                        `json:"completion_tokens"`
	TotalTokens             int64                        `json:"total_tokens"`
	PromptTokensDetails     *WirePromptTokensDetails     `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *WireCompletionTokensDetails `json:"completion_tokens_details,omitempty"`
	Cost                    *WireCost                    `json:"cost,omitempty"`
}

type WirePromptTokensDetails struct {
	CachedTokens int64
	CacheRead    int64
	CacheWrite   int64
}

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

type WireCompletionTokensDetails struct {
	ReasoningTokens int64
}

func (d *WireCompletionTokensDetails) UnmarshalJSON(data []byte) error {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil
	}
	d.ReasoningTokens = rawInt(obj, "reasoning", "reasoning_tokens")
	return nil
}

type WireCost struct {
	Input      float64
	Output     float64
	CacheRead  float64
	CacheWrite float64
	Total      float64
}

func (c *WireCost) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	var total float64
	if err := json.Unmarshal(trimmed, &total); err == nil {
		c.Total = total
		return nil
	}
	var s string
	if err := json.Unmarshal(trimmed, &s); err == nil {
		if f, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
			c.Total = f
		}
		return nil
	}
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
