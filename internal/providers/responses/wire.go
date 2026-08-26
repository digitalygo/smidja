package responses

import (
	"encoding/json"
)

// request is the wire body of a Responses API turn. Mode-specific fields
// (Text, ToolChoice, ParallelToolCalls, PromptCacheKey) are only set by
// the Codex variant, mirroring pi-ai's buildRequestBody.
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

// textOptions carries the Codex verbosity knob.
type textOptions struct {
	Verbosity string `json:"verbosity"`
}

// tool is one Responses function tool. Strict is a pointer so the wire
// distinguishes the plain mode value (false) from the Codex value (null),
// mirroring pi-ai's convertResponsesTools for both adapters.
type tool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	Strict      *bool           `json:"strict"`
}

// inputItem is a typed input item the driver constructs: a user message,
// an assistant message, a function call, or a function call output.
// Reasoning items are replayed verbatim as raw JSON instead and never go
// through this struct. Content carries the per-role part structs
// (inputContent for user messages, outputContent for assistant messages)
// through their dynamic types.
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

// inputContent is one content part of an input item: input_text on user
// messages, output_text on assistant messages.
type inputContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// outputContent is one content part of an assistant message item. The
// annotations array is always present on output parts, mirroring pi-ai;
// input parts never carry it.
type outputContent struct {
	Type        string `json:"type"`
	Text        string `json:"text"`
	Annotations []any  `json:"annotations"`
}

// streamEvent is the common envelope of every SSE data payload: the
// named event type rides inside the data, exactly like the openai SDK
// parses it.
type streamEvent struct {
	Type string `json:"type"`
}

// outputItemEvent is the shape shared by response.output_item.added and
// response.output_item.done. The Item bytes are kept raw so a reasoning
// item can be persisted verbatim as the thinking signature.
type outputItemEvent struct {
	OutputIndex int             `json:"output_index"`
	Item        json.RawMessage `json:"item"`
}

// outputItem is the decoded view of one output item.
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

// deltaEvent is the shape of the output delta events.
type deltaEvent struct {
	OutputIndex int    `json:"output_index"`
	Delta       string `json:"delta"`
}

// argumentsEvent is the shape of response.function_call_arguments.done.
type argumentsEvent struct {
	OutputIndex int    `json:"output_index"`
	Arguments   string `json:"arguments"`
}

// responseEvent carries the terminal response object of
// response.completed, response.incomplete, response.done, and
// response.failed.
type responseEvent struct {
	Response response `json:"response"`
}

// response is the terminal response object: identity, status, usage, and
// the final output items (used to backfill reasoning signatures Azure
// omits from the done events).
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

// usage is the token accounting of a completed response. OpenAI includes
// cached and cache-write tokens inside input_tokens, so the driver
// subtracts both, mirroring pi-ai's finalizeResponse.
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

// failedEvent carries the error details of a response.failed event.
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

// errorEvent is the shape of the top-level error SSE event.
type errorEvent struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// errorEnvelope is the provider error body shape of a non-2xx response.
type errorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}
