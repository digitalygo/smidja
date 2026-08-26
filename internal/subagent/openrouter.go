package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
)

const selectionSystemPrompt = `You are the context compaction selector of the smidja harness. You receive older conversation messages, each labeled with an entry ref, and you choose which ones to keep so the conversation continues working with a smaller context.

Keep what still matters: the task and its constraints, decisions and their rationale, and tool outputs that later messages refer back to. Keep tool-call/result pairs together. Drop what is stale: superseded attempts, obsolete tool outputs, and messages no later message references.

Reply with strict JSON only, exactly this shape, with no prose, no markdown fences, and no trailing text:
{"kept":["<ref>", ...]}

Rules:
- Return only refs present in the message list, never a ref twice.
- Keep at least one ref.
- Keep the set small enough to fit the stated token budget.`

type OpenRouterSelector struct {
	client agent.Client
}

func NewOpenRouterSelector(client agent.Client) *OpenRouterSelector {
	return &OpenRouterSelector{client: client}
}

func (s *OpenRouterSelector) Select(ctx context.Context, req SelectionRequest) (Selection, error) {
	if s.client == nil {
		return Selection{}, errors.New("subagent: nil selector client")
	}
	if req.Model == "" {
		return Selection{}, errors.New("subagent: empty selection model")
	}
	if len(req.Candidates) == 0 {
		return Selection{}, errors.New("subagent: no candidates")
	}
	if req.BudgetTokens <= 0 {
		return Selection{}, errors.New("subagent: non-positive selection budget")
	}

	cost := make(map[string]int64, len(req.Candidates))
	for _, c := range req.Candidates {
		cost[c.Ref] = estimateTokens(c.Message)
	}

	chunks := req.Chunks
	if len(chunks) == 0 {
		chunks = [][]Candidate{req.Candidates}
	}

	var kept []string
	for _, chunk := range chunks {
		answer, err := s.askChunk(ctx, req, chunk)
		if err != nil {
			return Selection{}, err
		}
		ids, err := parseKeptIDs(answer)
		if err != nil {
			return Selection{}, err
		}
		kept = append(kept, ids...)
	}
	if err := validateSelection(kept, cost, req.BudgetTokens); err != nil {
		return Selection{}, err
	}
	return Selection{KeptIDs: kept}, nil
}

func (s *OpenRouterSelector) askChunk(ctx context.Context, req SelectionRequest, chunk []Candidate) (string, error) {
	asst, err := s.turnWithRetry(ctx, req.Model, renderChunk(req, chunk))
	if err != nil {
		return "", err
	}
	return textOf(asst), nil
}

func (s *OpenRouterSelector) turnWithRetry(ctx context.Context, model, prompt string) (*agent.AssistantMessage, error) {
	const maxAttempts = 3
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		asst, err := s.client.StreamTurn(ctx, &agent.TurnRequest{
			Model:  model,
			System: selectionSystemPrompt,
			Messages: []*agent.Message{{
				User: &agent.UserMessage{Role: string(agent.RoleUser), Content: jsonString(prompt)},
			}},
		}, nil, nil)
		if err == nil {
			return asst, nil
		}
		lastErr = err
		if attempt < maxAttempts {
			if err := sleepCtx(ctx, time.Duration(attempt)*200*time.Millisecond); err != nil {
				return nil, err
			}
		}
	}
	return nil, fmt.Errorf("subagent: selection turn failed: %w", lastErr)
}

func renderChunk(req SelectionRequest, chunk []Candidate) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Context window: %d tokens. Keep at most %d tokens of the messages below.\n\n", req.WindowTokens, req.BudgetTokens)
	for _, c := range chunk {
		fmt.Fprintf(&b, "[ref %s]\n%s\n\n", c.Ref, renderMessage(c.Message))
	}
	return b.String()
}

func renderMessage(m *agent.Message) string {
	if m == nil {
		return "(unknown message)"
	}
	switch {
	case m.User != nil:
		return "user: " + userContentText(m.User.Content)
	case m.Assistant != nil:
		var b strings.Builder
		for _, blk := range m.Assistant.Content {
			switch blk.Type {
			case agent.BlockTypeText:
				b.WriteString(blk.Text)
			case agent.BlockTypeToolCall:
				fmt.Fprintf(&b, "tool call %s(%s) [id %s]", blk.Name, string(blk.Arguments), blk.ID)
			}
		}
		if b.Len() == 0 {
			return "assistant: (empty)"
		}
		return "assistant: " + b.String()
	case m.ToolResult != nil:
		flag := ""
		if m.ToolResult.IsError {
			flag = " (error)"
		}
		return fmt.Sprintf("tool result for %s (%s)%s: %s", m.ToolResult.ToolCallID, m.ToolResult.ToolName, flag, blockText(m.ToolResult.Content))
	default:
		return "(unknown message)"
	}
}

func userContentText(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var b strings.Builder
		for _, blk := range blocks {
			if blk.Type == agent.BlockTypeText {
				b.WriteString(blk.Text)
				b.WriteString(" ")
			}
		}
		return strings.TrimSpace(b.String())
	}
	return string(raw)
}

func blockText(blocks []agent.ContentBlock) string {
	var b strings.Builder
	for _, blk := range blocks {
		if blk.Type == agent.BlockTypeText {
			b.WriteString(blk.Text)
		}
	}
	return b.String()
}

func textOf(a *agent.AssistantMessage) string {
	if a == nil {
		return ""
	}
	return strings.TrimSpace(blockText(a.Content))
}

func parseKeptIDs(answer string) ([]string, error) {
	body := strings.TrimSpace(answer)
	body = strings.TrimPrefix(body, "```json")
	body = strings.TrimPrefix(body, "```")
	body = strings.TrimSuffix(body, "```")
	body = strings.TrimSpace(body)
	var out struct {
		Kept []string `json:"kept"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		return nil, fmt.Errorf("%w: model answer is not valid JSON: %v", ErrInvalidSelection, err)
	}
	return out.Kept, nil
}

func validateSelection(kept []string, cost map[string]int64, budget int64) error {
	if len(kept) == 0 {
		return fmt.Errorf("%w: kept set is empty", ErrInvalidSelection)
	}
	seen := make(map[string]struct{}, len(kept))
	var used int64
	for _, id := range kept {
		if _, ok := cost[id]; !ok {
			return fmt.Errorf("%w: unknown ref %q", ErrInvalidSelection, id)
		}
		if _, dup := seen[id]; dup {
			return fmt.Errorf("%w: duplicate ref %q", ErrInvalidSelection, id)
		}
		seen[id] = struct{}{}
		used += cost[id]
	}
	if used > budget {
		return fmt.Errorf("%w: kept set exceeds budget: %d > %d tokens", ErrInvalidSelection, used, budget)
	}
	return nil
}

func jsonString(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
