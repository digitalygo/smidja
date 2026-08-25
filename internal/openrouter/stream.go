package openrouter

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/digitalygo/smidja/internal/agent"
)

// doneEvent is the SSE event that ends a completion stream.
const doneEvent = "[DONE]"

// maxSSELine is the largest accepted single SSE data line (8 MiB). It is a
// parser bound for one bufio.Scanner token, not a stream cap: SSE parsing
// requires a fixed token ceiling, and no well-formed data line approaches
// it.
const maxSSELine = 8 * 1024 * 1024

// readStream reads and parses the SSE stream of one completion response,
// delivering text and thinking deltas to the callbacks and accumulating
// content blocks, tool calls, usage, and the stop reason. It returns the
// completed assistant message, or nil and an error when the stream aborts
// or ends prematurely.
func readStream(ctx context.Context, resp *http.Response, model string, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	state := newStreamState(model, onText, onThinking)

	// The stream may stall with the connection open; closing the body when
	// the context is cancelled unblocks the reader below.
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			resp.Body.Close()
		case <-done:
		}
	}()
	defer close(done)

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), maxSSELine)

	var dataLines []string
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			if len(dataLines) == 0 {
				continue
			}
			data := strings.Join(dataLines, "\n")
			dataLines = dataLines[:0]
			if strings.TrimSpace(data) == doneEvent {
				return state.result(), nil
			}
			if err := state.apply(data); err != nil {
				return nil, err
			}
		case strings.HasPrefix(line, ":"):
			// SSE comment line; ignore.
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimPrefix(line, "data:"))
		}
	}

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("openrouter: %w", err)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("openrouter: read stream: %w", err)
	}
	if state.gotFinishReason {
		return state.result(), nil
	}
	return nil, errors.New("openrouter: stream ended prematurely without [DONE] or finish_reason")
}

// streamState accumulates the parsed output of one streaming completion.
type streamState struct {
	model           string
	blocks          []agent.ContentBlock
	builders        []*strings.Builder       // aligned with blocks; holds Text or Thinking content
	toolCalls       map[int]int              // tool-call delta index -> block position
	toolArgs        map[int]*strings.Builder // tool-call delta index -> arguments builder
	openText        int                      // position of the open text block, -1 when closed
	openThinking    int                      // position of the open thinking block, -1 when closed
	responseID      string
	usage           agent.Usage
	stopReason      string
	gotFinishReason bool
	onText          func(string)
	onThinking      func(string)
}

// newStreamState returns an empty state for the given model and callbacks.
func newStreamState(model string, onText func(string), onThinking func(string)) *streamState {
	return &streamState{
		model:        model,
		toolCalls:    make(map[int]int),
		toolArgs:     make(map[int]*strings.Builder),
		openText:     -1,
		openThinking: -1,
		onText:       onText,
		onThinking:   onThinking,
	}
}

// apply parses one SSE data payload and folds it into the state.
func (s *streamState) apply(data string) error {
	var ch wireChunk
	if err := json.Unmarshal([]byte(data), &ch); err != nil {
		return fmt.Errorf("openrouter: decode stream chunk: %w", err)
	}
	if ch.Error != nil {
		return &providerError{*ch.Error}
	}
	if s.responseID == "" && ch.ID != "" {
		s.responseID = ch.ID
	}
	for i := range ch.Choices {
		choice := &ch.Choices[i]
		if choice.FinishReason != "" {
			s.gotFinishReason = true
			s.stopReason = finishReasonToStopReason(choice.FinishReason)
		}
		if choice.Delta.Content != nil {
			if text := *choice.Delta.Content; text != "" {
				if err := s.addText(text); err != nil {
					return err
				}
				if s.onText != nil {
					s.onText(text)
				}
			}
		}
		if choice.Delta.Reasoning != nil {
			if text := *choice.Delta.Reasoning; text != "" {
				if err := s.addThinking(text); err != nil {
					return err
				}
				if s.onThinking != nil {
					s.onThinking(text)
				}
			}
		}
		for _, tc := range choice.Delta.ToolCalls {
			if err := s.addToolCall(tc); err != nil {
				return err
			}
		}
	}
	if ch.Usage != nil {
		s.usage = ch.Usage.toUsage()
	}
	return nil
}

// finishReasonToStopReason maps provider finish reasons onto agent stop
// reasons; unknown reasons pass through verbatim.
func finishReasonToStopReason(reason string) string {
	switch reason {
	case "tool_calls":
		return "toolUse"
	case "stop":
		return "stop"
	default:
		return reason
	}
}

// addText accumulates a text delta into a strings.Builder, extending the
// open text block or starting a new one in first-appearance order.
func (s *streamState) addText(text string) error {
	if s.openText >= 0 {
		s.builders[s.openText].WriteString(text)
	} else {
		b := &strings.Builder{}
		b.WriteString(text)
		s.blocks = append(s.blocks, agent.ContentBlock{Type: agent.BlockTypeText})
		s.builders = append(s.builders, b)
		s.openText = len(s.blocks) - 1
		s.openThinking = -1
	}
	return nil
}

// addThinking accumulates a reasoning delta the same way addText does for
// text, producing thinking content blocks.
func (s *streamState) addThinking(text string) error {
	if s.openThinking >= 0 {
		s.builders[s.openThinking].WriteString(text)
	} else {
		b := &strings.Builder{}
		b.WriteString(text)
		s.blocks = append(s.blocks, agent.ContentBlock{Type: agent.BlockTypeThinking})
		s.builders = append(s.builders, b)
		s.openThinking = len(s.blocks) - 1
		s.openText = -1
	}
	return nil
}

// addToolCall folds one tool-call fragment into the accumulated tool call
// for its delta index, creating the block on first appearance and keeping
// the blocks in first-appearance order.
func (s *streamState) addToolCall(tc wireDeltaToolCall) error {
	index := 0
	if tc.Index != nil {
		index = *tc.Index
	}
	pos, ok := s.toolCalls[index]
	if !ok {
		s.blocks = append(s.blocks, agent.ContentBlock{Type: agent.BlockTypeToolCall})
		s.builders = append(s.builders, nil)
		pos = len(s.blocks) - 1
		s.toolCalls[index] = pos
		s.toolArgs[index] = &strings.Builder{}
		s.openText = -1
		s.openThinking = -1
	}
	block := &s.blocks[pos]
	if tc.ID != "" && block.ID == "" {
		block.ID = tc.ID
	}
	if tc.Function.Name != "" && block.Name == "" {
		block.Name = tc.Function.Name
	}
	if tc.Function.Arguments != "" {
		s.toolArgs[index].WriteString(tc.Function.Arguments)
	}
	return nil
}

// result assembles the final assistant message from the accumulated state,
// materializing the builder-held text, thinking, and tool arguments.
func (s *streamState) result() *agent.AssistantMessage {
	for i := range s.blocks {
		switch s.blocks[i].Type {
		case agent.BlockTypeText:
			if b := s.builders[i]; b != nil {
				s.blocks[i].Text = b.String()
			}
		case agent.BlockTypeThinking:
			if b := s.builders[i]; b != nil {
				s.blocks[i].Thinking = b.String()
			}
		}
	}
	for idx, pos := range s.toolCalls {
		if b := s.toolArgs[idx]; b != nil && b.Len() > 0 {
			s.blocks[pos].Arguments = json.RawMessage(b.String())
		}
	}
	stopReason := s.stopReason
	if stopReason == "" {
		stopReason = "stop"
	}
	return &agent.AssistantMessage{
		Role:       string(agent.RoleAssistant),
		Content:    s.blocks,
		API:        apiField,
		Provider:   provider,
		Model:      s.model,
		ResponseID: s.responseID,
		Usage:      s.usage,
		StopReason: stopReason,
		Timestamp:  agent.NowMillis(),
	}
}

// providerError aborts a turn with a provider-reported error envelope,
// whether it arrived via the HTTP status or as an SSE event.
type providerError struct {
	wireError
}

// Error implements error.
func (e *providerError) Error() string {
	code := ""
	if e.Code != "" {
		code = fmt.Sprintf(" (code %s)", e.Code)
	}
	msg := e.Message
	if msg == "" {
		msg = "provider error"
	}
	return fmt.Sprintf("openrouter: %s%s", msg, code)
}

// toUsage maps the wire usage onto agent.Usage. Cost and the detail
// breakdowns stay zero when the provider reports none.
func (u *wireUsage) toUsage() agent.Usage {
	usage := agent.Usage{
		Input:  u.PromptTokens,
		Output: u.CompletionTokens,
	}
	if u.TotalTokens > 0 {
		usage.TotalTokens = u.TotalTokens
	} else {
		usage.TotalTokens = u.PromptTokens + u.CompletionTokens
	}
	if d := u.PromptTokensDetails; d != nil {
		// Prefer the nested cache_read breakdown; fall back to the flat
		// cached_tokens field some providers send.
		usage.CacheRead = d.CacheRead
		if usage.CacheRead == 0 {
			usage.CacheRead = d.CachedTokens
		}
		usage.CacheWrite = d.CacheWrite
	}
	if d := u.CompletionTokensDetails; d != nil {
		usage.Reasoning = d.ReasoningTokens
	}
	if c := u.Cost; c != nil {
		usage.Cost.Input = c.Input
		usage.Cost.Output = c.Output
		usage.Cost.CacheRead = c.CacheRead
		usage.Cost.CacheWrite = c.CacheWrite
		usage.Cost.Total = c.Total
	}
	return usage
}
