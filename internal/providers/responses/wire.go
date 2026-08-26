package responses

import (
	"encoding/json"
)

type request struct {
	Model             string            `json:"model"`
	Instructions      string            `json:"instructions,omitempty"`
	Input             []json.RawMessage `json:"input"`
	Tools             []tool            `json:"tools,omitempty"`
	Stream            bool              `json:"stream"`
	Store             bool              `json:"store"`
	Include           []string          `json:"include,omitempty"`
	Text              *textOptions      `json:"text,omitempty"`
	ToolChoice        string            `json:"tool_choice,omitempty"`
	ParallelToolCalls bool              `json:"parallel_tool_calls,omitempty"`
	PromptCacheKey    *string           `json:"prompt_cache_key,omitempty"`
}

type textOptions struct {
	Verbosity string `json:"verbosity"`
}

type tool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	Strict      *bool           `json:"strict"`
}

type inputItem struct {
	Type      string `json:"type"`
	Role      string `json:"role,omitempty"`
	Content   []any  `json:"content,omitempty"`
	Status    string `json:"status,omitempty"`
	ID        string `json:"id,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Output    string `json:"output,omitempty"`
}

type inputContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type outputContent struct {
	Type        string `json:"type"`
	Text        string `json:"text"`
	Annotations []any  `json:"annotations"`
}

type streamEvent struct {
	Type string `json:"type"`
}

type outputItemEvent struct {
	OutputIndex int             `json:"output_index"`
	Item        json.RawMessage `json:"item"`
}

type outputItem struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Status    string `json:"status"`
	Phase     string `json:"phase"`
	Content   []struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Refusal string `json:"refusal"`
	} `json:"content"`
	Summary []struct {
		Text string `json:"text"`
	} `json:"summary"`
}

type deltaEvent struct {
	OutputIndex int    `json:"output_index"`
	Delta       string `json:"delta"`
}

type argumentsEvent struct {
	OutputIndex int    `json:"output_index"`
	Arguments   string `json:"arguments"`
}

type responseEvent struct {
	Response response `json:"response"`
}

type response struct {
	ID                string `json:"id"`
	Status            string `json:"status"`
	Usage             *usage `json:"usage"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
	Output []struct {
		Type             string          `json:"type"`
		ID               string          `json:"id"`
		EncryptedContent json.RawMessage `json:"encrypted_content"`
	} `json:"output"`
}

type usage struct {
	InputTokens        int64 `json:"input_tokens"`
	OutputTokens       int64 `json:"output_tokens"`
	TotalTokens        int64 `json:"total_tokens"`
	InputTokensDetails *struct {
		CachedTokens     int64 `json:"cached_tokens"`
		CacheWriteTokens int64 `json:"cache_write_tokens"`
	} `json:"input_tokens_details"`
	OutputTokensDetails *struct {
		ReasoningTokens int64 `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
}

type failedEvent struct {
	Response struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Error  *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		IncompleteDetails *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
	} `json:"response"`
}

type errorEvent struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}
