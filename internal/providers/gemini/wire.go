package gemini

import (
	"encoding/json"
)

// GenerateContentRequest is the body of the streamGenerateContent
// request. Every optional field is omitted when empty, mirroring pi-ai's
// buildParams: the driver sends no generationConfig because smidja's
// TurnRequest carries no sampling knobs.
type GenerateContentRequest struct {
	Contents          []Content         `json:"contents"`
	SystemInstruction *Content          `json:"systemInstruction,omitempty"`
	Tools             []Tool            `json:"tools,omitempty"`
	ThinkingConfig    *ThinkingConfig   `json:"thinkingConfig,omitempty"`
	GenerationConfig  *GenerationConfig `json:"generationConfig,omitempty"`
}

// Content is one turn of the conversation: a role plus its parts.
type Content struct {
	Role  string `json:"role,omitempty"`
	Parts []Part `json:"parts"`
}

// Part is one element of a content turn: a text part (optionally a
// thinking part marked with Thought and carrying a ThoughtSignature), a
// function call, or a function response. Text is a pointer so the wire
// distinguishes an absent field from an empty string, exactly like
// pi-ai's `part.text !== undefined` check.
type Part struct {
	Text             *string           `json:"text,omitempty"`
	Thought          bool              `json:"thought,omitempty"`
	ThoughtSignature string            `json:"thoughtSignature,omitempty"`
	FunctionCall     *FunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *FunctionResponse `json:"functionResponse,omitempty"`
}

// FunctionCall is one tool invocation requested by the model. Args is the
// raw JSON object of the arguments; ID is only populated for models that
// require explicit tool call ids (Gemini 3+).
type FunctionCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
	ID   string          `json:"id,omitempty"`
}

// FunctionResponse is the harness's report of one tool execution: the
// response object is {"output": ...} on success and {"error": ...} on
// failure, per the Gemini SDK documentation.
type FunctionResponse struct {
	Name     string          `json:"name"`
	Response json.RawMessage `json:"response"`
	ID       string          `json:"id,omitempty"`
}

// Tool is one Gemini tool: a list of function declarations.
type Tool struct {
	FunctionDeclarations []FunctionDeclaration `json:"functionDeclarations,omitempty"`
}

// FunctionDeclaration is the metadata of one callable function.
type FunctionDeclaration struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parametersJsonSchema,omitempty"`
}

// ThinkingConfig requests thinking parts from the model.
type ThinkingConfig struct {
	IncludeThoughts bool `json:"includeThoughts"`
}

// GenerationConfig carries the sampling knobs smidja does not currently
// set; it exists so hosts can extend the wire shape without restructuring
// the request type.
type GenerationConfig struct {
	Temperature     *float64 `json:"temperature,omitempty"`
	MaxOutputTokens *int     `json:"maxOutputTokens,omitempty"`
}

// StreamChunk is one SSE data payload of a streaming generateContent
// response. Each data line is a full GenerateContentResponse; usage
// metadata is cumulative across chunks, so the driver takes the last one.
type StreamChunk struct {
	Candidates     []Candidate     `json:"candidates"`
	UsageMetadata  *UsageMetadata  `json:"usageMetadata"`
	PromptFeedback *PromptFeedback `json:"promptFeedback"`
	ResponseID     string          `json:"responseId"`
}

// Candidate is one response candidate; smidja always reads the first.
type Candidate struct {
	Content      *Content `json:"content"`
	FinishReason string   `json:"finishReason"`
}

// UsageMetadata is the cumulative token accounting of the stream. The
// counts are totals, not deltas: each chunk repeats the whole accounting,
// so a driver must overwrite rather than sum.
type UsageMetadata struct {
	PromptTokenCount        int64 `json:"promptTokenCount"`
	CandidatesTokenCount    int64 `json:"candidatesTokenCount"`
	TotalTokenCount         int64 `json:"totalTokenCount"`
	CachedContentTokenCount int64 `json:"cachedContentTokenCount"`
	ThoughtsTokenCount      int64 `json:"thoughtsTokenCount"`
}

// PromptFeedback reports a prompt-level block. When BlockReason is
// non-empty the turn is rejected without candidates.
type PromptFeedback struct {
	BlockReason        string `json:"blockReason"`
	BlockReasonMessage string `json:"blockReasonMessage"`
}

// ErrorEnvelope is the provider error body shape.
type ErrorEnvelope struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}
