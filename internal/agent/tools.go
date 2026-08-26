package agent

import (
	"context"
	"encoding/json"
)

type Result struct {
	Content []ContentBlock
	IsError bool
}

func TextResult(text string) Result {
	return Result{Content: []ContentBlock{{Type: BlockTypeText, Text: text}}}
}

func ErrorResult(text string) Result {
	return Result{Content: []ContentBlock{{Type: BlockTypeText, Text: text}}, IsError: true}
}

type Tool interface {
	Name() string

	Description() string

	Schema() json.RawMessage

	Exec(ctx context.Context, args json.RawMessage) Result
}
