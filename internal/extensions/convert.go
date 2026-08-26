package extensions

import (
	"encoding/json"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/sdk"
)

func messageToSDK(m *agent.Message) sdk.Message {
	if m == nil {
		return sdk.Message{}
	}
	out := sdk.Message{Role: m.Role()}
	switch {
	case m.User != nil:
		out.Content = rawContentToBlocks(m.User.Content)
	case m.Assistant != nil:
		out.Content = blocksToSDK(m.Assistant.Content)
		u := usageToSDK(m.Assistant.Usage)
		out.Usage = &u
	case m.ToolResult != nil:
		out.Content = blocksToSDK(m.ToolResult.Content)
	}
	return out
}

func messageFromSDK(m *sdk.Message) *agent.Message {
	if m == nil {
		return nil
	}
	switch m.Role {
	case string(agent.RoleUser):
		return &agent.Message{
			User: &agent.UserMessage{
				Role:      string(agent.RoleUser),
				Content:   blocksToRawContent(m.Content),
				Timestamp: agent.NowMillis(),
			},
		}
	case string(agent.RoleAssistant):
		return &agent.Message{
			Assistant: &agent.AssistantMessage{
				Role:      string(agent.RoleAssistant),
				Content:   blocksFromSDK(m.Content),
				Usage:     usageFromSDK(m.Usage),
				Timestamp: agent.NowMillis(),
			},
		}
	case string(agent.RoleToolResult):
		return &agent.Message{
			ToolResult: &agent.ToolResultMessage{
				Role:      string(agent.RoleToolResult),
				Content:   blocksFromSDK(m.Content),
				Timestamp: agent.NowMillis(),
			},
		}
	default:
		return &agent.Message{}
	}
}

func blocksToSDK(blocks []agent.ContentBlock) []sdk.Block {
	if blocks == nil {
		return nil
	}
	out := make([]sdk.Block, len(blocks))
	for i, b := range blocks {
		out[i] = sdk.Block{
			Type:      b.Type,
			Text:      b.Text,
			Thinking:  b.Thinking,
			ID:        b.ID,
			Name:      b.Name,
			Arguments: cloneRaw(b.Arguments),
		}
	}
	return out
}

func blocksFromSDK(blocks []sdk.Block) []agent.ContentBlock {
	if blocks == nil {
		return nil
	}
	out := make([]agent.ContentBlock, len(blocks))
	for i, b := range blocks {
		out[i] = agent.ContentBlock{
			Type:      b.Type,
			Text:      b.Text,
			Thinking:  b.Thinking,
			ID:        b.ID,
			Name:      b.Name,
			Arguments: cloneRaw(b.Arguments),
		}
	}
	return out
}

func rawContentToBlocks(raw json.RawMessage) []sdk.Block {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var arr []sdk.Block
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []sdk.Block{{Type: agent.BlockTypeText, Text: s}}
	}
	return nil
}

func blocksToRawContent(blocks []sdk.Block) json.RawMessage {
	if len(blocks) == 0 {
		return nil
	}
	if len(blocks) == 1 && blocks[0].Type == agent.BlockTypeText &&
		blocks[0].Thinking == "" && blocks[0].ID == "" && blocks[0].Name == "" {
		if b, err := json.Marshal(blocks[0].Text); err == nil {
			return b
		}
		return nil
	}
	if b, err := json.Marshal(blocks); err == nil {
		return b
	}
	return nil
}

func usageToSDK(u agent.Usage) sdk.Usage {
	return sdk.Usage{
		Input:       u.Input,
		Output:      u.Output,
		CacheRead:   u.CacheRead,
		CacheWrite:  u.CacheWrite,
		Reasoning:   u.Reasoning,
		TotalTokens: u.TotalTokens,
		Cost: sdk.Cost{
			Input:      u.Cost.Input,
			Output:     u.Cost.Output,
			CacheRead:  u.Cost.CacheRead,
			CacheWrite: u.Cost.CacheWrite,
			Total:      u.Cost.Total,
		},
	}
}

func usageFromSDK(u *sdk.Usage) agent.Usage {
	if u == nil {
		return agent.Usage{}
	}
	return agent.Usage{
		Input:       u.Input,
		Output:      u.Output,
		CacheRead:   u.CacheRead,
		CacheWrite:  u.CacheWrite,
		Reasoning:   u.Reasoning,
		TotalTokens: u.TotalTokens,
		Cost: agent.Cost{
			Input:      u.Cost.Input,
			Output:     u.Cost.Output,
			CacheRead:  u.Cost.CacheRead,
			CacheWrite: u.Cost.CacheWrite,
			Total:      u.Cost.Total,
		},
	}
}

func cloneRaw(r json.RawMessage) json.RawMessage {
	if r == nil {
		return nil
	}
	return append(json.RawMessage(nil), r...)
}
