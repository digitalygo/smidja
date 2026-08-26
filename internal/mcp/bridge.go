package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/digitalygo/smidja/internal/agent"
)

const (
	maxToolNameLen = 64
	maxSchemaBytes = 256 * 1024
	validNameChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-"
)

var (
	ErrInvalidName     = errors.New("mcp: invalid tool name")
	ErrNameCollision   = errors.New("mcp: tool name collision after sanitization")
	ErrSchemaInvalid   = errors.New("mcp: tool inputSchema is not a JSON object")
	ErrSchemaTooLarge  = errors.New("mcp: tool inputSchema exceeds size limit")
	ErrUnsupportedType = errors.New("mcp: unsupported content type")
)

type bridgeTool struct {
	client      *Client
	mcpName     string
	name        string
	description string
	schema      json.RawMessage
}

func ToAgentTools(ctx context.Context, c *Client, prefix string) ([]agent.Tool, error) {
	tools, err := c.ListTools(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]agent.Tool, 0, len(tools))
	seen := make(map[string]string, len(tools))
	for _, tool := range tools {
		name, err := SanitizeName(prefix, tool.Name)
		if err != nil {
			return nil, fmt.Errorf("mcp: tool %q: %w", tool.Name, err)
		}
		if prev, dup := seen[name]; dup {
			return nil, fmt.Errorf("%w: %q and %q both map to %q", ErrNameCollision, prev, tool.Name, name)
		}
		seen[name] = tool.Name
		if err := validateInputSchema(tool.InputSchema); err != nil {
			return nil, fmt.Errorf("mcp: tool %q: %w", tool.Name, err)
		}
		out = append(out, &bridgeTool{
			client:      c,
			mcpName:     tool.Name,
			name:        name,
			description: tool.Description,
			schema:      tool.InputSchema,
		})
	}
	return out, nil
}

func SanitizeName(prefix, mcpName string) (string, error) {
	base := prefix + "_" + mcpName
	var b strings.Builder
	for _, r := range base {
		if strings.ContainsRune(validNameChars, r) {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	name := b.String()
	if name == "" || len(name) > maxToolNameLen {
		return "", ErrInvalidName
	}
	return name, nil
}

func validateInputSchema(raw json.RawMessage) error {
	if len(raw) > maxSchemaBytes {
		return ErrSchemaTooLarge
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ErrSchemaInvalid
	}
	if obj == nil {
		return ErrSchemaInvalid
	}
	return nil
}

func (t *bridgeTool) Name() string {
	return t.name
}

func (t *bridgeTool) Description() string {
	return t.description
}

func (t *bridgeTool) Schema() json.RawMessage {
	return t.schema
}

func (t *bridgeTool) Exec(ctx context.Context, args json.RawMessage) agent.Result {
	res, err := t.client.CallTool(ctx, t.mcpName, args)
	if err != nil {
		return agent.ErrorResult(fmt.Sprintf("mcp %s: %v", t.mcpName, err))
	}
	blocks := make([]agent.ContentBlock, 0, len(res.Content))
	for _, item := range res.Content {
		switch item.Type {
		case "text":
			blocks = append(blocks, agent.ContentBlock{Type: agent.BlockTypeText, Text: item.Text})
		case "json":
			blocks = append(blocks, agent.ContentBlock{Type: agent.BlockTypeText, Text: string(item.JSON)})
		default:
			return agent.ErrorResult(fmt.Sprintf("mcp %s: %s %q", t.mcpName, ErrUnsupportedType, item.Type))
		}
	}
	return agent.Result{Content: blocks, IsError: res.IsError}
}
