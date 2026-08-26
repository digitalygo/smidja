package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/workspace"
)

type writeArgs struct {
	Path    *string `json:"path"`
	Content *string `json:"content"`
}

type writeTool struct {
	ws *workspace.Workspace
}

func (t *writeTool) Name() string { return "write" }

func (t *writeTool) Description() string {
	return "Writes a file inside the workspace, creating parent directories as needed and replacing any existing file atomically."
}

func (t *writeTool) Schema() json.RawMessage {
	return schema(
		map[string]any{
			"path":    strProp("Path to the file to write, relative to the workspace root."),
			"content": strProp("Full text content to write."),
		},
		"path", "content",
	)
}

func (t *writeTool) Exec(ctx context.Context, args json.RawMessage) Result {
	var a writeArgs
	if res := decodeArgs("write", args, &a); res.IsError {
		return res
	}
	if a.Path == nil {
		return agent.ErrorResult("write: missing required argument 'path'")
	}
	if a.Content == nil {
		return agent.ErrorResult("write: missing required argument 'content'")
	}
	full, res := contain(t.ws, *a.Path)
	if res.IsError {
		return res
	}
	if err := atomicWrite(full, []byte(*a.Content)); err != nil {
		return agent.ErrorResult(fmt.Sprintf("write %q: %v", *a.Path, err))
	}
	return agent.TextResult(fmt.Sprintf("Wrote %d bytes to %s", len(*a.Content), *a.Path))
}
