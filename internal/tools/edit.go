package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/workspace"
	"os"
)

type editArgs struct {
	Path       *string `json:"path"`
	OldText    *string `json:"oldText"`
	NewText    *string `json:"newText"`
	ReplaceAll *bool   `json:"replaceAll"`
}

type editTool struct {
	ws *workspace.Workspace
}

func (t *editTool) Name() string { return "edit" }

func (t *editTool) Description() string {
	return "Replaces literal text in a file inside the workspace, atomically; it errors when the search text matches more than once unless replaceAll is true."
}

func (t *editTool) Schema() json.RawMessage {
	return schema(
		map[string]any{
			"path":       strProp("Path to the file to edit, relative to the workspace root."),
			"oldText":    strProp("Literal text to find. Must match at least once."),
			"newText":    strProp("Replacement text."),
			"replaceAll": boolProp("Replace every occurrence instead of exactly one. Defaults to false."),
		},
		"path", "oldText", "newText",
	)
}

func (t *editTool) Exec(ctx context.Context, args json.RawMessage) Result {
	var a editArgs
	if res := decodeArgs("edit", args, &a); res.IsError {
		return res
	}
	if a.Path == nil {
		return agent.ErrorResult("edit: missing required argument 'path'")
	}
	if a.OldText == nil {
		return agent.ErrorResult("edit: missing required argument 'oldText'")
	}
	if a.NewText == nil {
		return agent.ErrorResult("edit: missing required argument 'newText'")
	}
	if *a.OldText == "" {
		return agent.ErrorResult("edit: oldText must not be empty")
	}
	full, res := contain(t.ws, *a.Path)
	if res.IsError {
		return res
	}

	data, err := os.ReadFile(full)
	if err != nil {
		return agent.ErrorResult(fmt.Sprintf("edit %q: %v", *a.Path, err))
	}
	old, new := []byte(*a.OldText), []byte(*a.NewText)
	count := bytes.Count(data, old)
	if count == 0 {
		return agent.ErrorResult(fmt.Sprintf("edit %q: oldText not found in file", *a.Path))
	}
	replaceAll := a.ReplaceAll != nil && *a.ReplaceAll
	if count > 1 && !replaceAll {
		return agent.ErrorResult(fmt.Sprintf("edit %q: oldText matches %d times; pass replaceAll=true or use a more specific match", *a.Path, count))
	}
	if replaceAll {
		data = bytes.ReplaceAll(data, old, new)
	} else {
		data = bytes.Replace(data, old, new, 1)
	}
	if err := atomicWrite(full, data); err != nil {
		return agent.ErrorResult(fmt.Sprintf("edit %q: %v", *a.Path, err))
	}
	return agent.TextResult(fmt.Sprintf("Replaced %d occurrence(s) of the given text in %s", count, *a.Path))
}
