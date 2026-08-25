package agent

import (
	"context"
	"encoding/json"
)

// Result is the outcome of one tool execution. Content holds the output as
// text blocks only; IsError marks a failed execution, in which case the
// content carries the error description.
type Result struct {
	// Content holds the tool output as text blocks. Empty when the tool
	// produced no output.
	Content []ContentBlock
	// IsError marks the execution as failed.
	IsError bool
}

// TextResult returns a successful Result whose only content block is a text
// block with the given text.
func TextResult(text string) Result {
	return Result{Content: []ContentBlock{{Type: BlockTypeText, Text: text}}}
}

// ErrorResult returns a failed Result whose only content block carries the
// error text.
func ErrorResult(text string) Result {
	return Result{Content: []ContentBlock{{Type: BlockTypeText, Text: text}}, IsError: true}
}

// Tool is the contract every smidja tool implements. Tools are registered
// by the tools package and exposed to the model through the provider seam.
type Tool interface {
	// Name returns the tool's canonical name as used in toolCall blocks
	// and in the provider's tool list. Names must be stable identifiers,
	// for example "read" or "exec".
	Name() string

	// Description returns a human-readable description shown to the model
	// so it can decide when and how to call the tool.
	Description() string

	// Schema returns the JSON schema of the tool's parameters as a raw
	// JSON object, in the provider's expected schema dialect.
	Schema() json.RawMessage

	// Exec runs the tool with the given arguments and returns its result.
	// The context carries cancellation and timeouts; Exec must respect
	// ctx.Done() in long-running work.
	Exec(ctx context.Context, args json.RawMessage) Result
}
