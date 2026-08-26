package responses

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

const maxSSELine = 8 * 1024 * 1024

func (d *Responses) readStream(ctx context.Context, resp *http.Response, model string, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	state := newStreamState(d.prefix, d.providerID, d.api, model, onText, onThinking)

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
			if err := state.apply(data); err != nil {
				return nil, err
			}
		case strings.HasPrefix(line, ":"):
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimPrefix(line, "data:"))
		}
	}

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", d.prefix, err)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%s: read stream: %w", d.prefix, err)
	}
	if state.stopReason == "error" {
		msg := state.errorMessage
		if msg == "" {
			msg = "an unknown error occurred"
		}
		return nil, errors.New(d.prefix + ": " + msg)
	}
	if !state.sawTerminal {
		return nil, errors.New(d.prefix + ": stream ended before a terminal response event")
	}
	return state.result(), nil
}

type slot struct {
	kind     string
	blockPos int
	partial  *strings.Builder
}

type streamState struct {
	prefix          string
	providerID      string
	api             string
	model           string
	blocks          []agent.ContentBlock
	builders        []*strings.Builder
	slots           map[int]*slot
	reasoningBlocks map[string]int
	responseID      string
	usage           agent.Usage
	stopReason      string
	errorMessage    string
	sawTerminal     bool
	onText          func(string)
	onThinking      func(string)
}

func newStreamState(prefix, providerID, api, model string, onText func(string), onThinking func(string)) *streamState {
	return &streamState{
		prefix:          prefix,
		providerID:      providerID,
		api:             api,
		model:           model,
		slots:           make(map[int]*slot),
		reasoningBlocks: make(map[string]int),
		onText:          onText,
		onThinking:      onThinking,
	}
}

func (s *streamState) apply(data string) error {
	if strings.TrimSpace(data) == "[DONE]" {
		return nil
	}
	var env streamEvent
	if err := json.Unmarshal([]byte(data), &env); err != nil {
		return fmt.Errorf("%s: decode stream event: %w", s.prefix, err)
	}
	switch env.Type {
	case "response.created":
		var e struct {
			Response struct {
				ID string `json:"id"`
			} `json:"response"`
		}
		if err := json.Unmarshal([]byte(data), &e); err != nil {
			return s.decodeErr(err)
		}
		if s.responseID == "" {
			s.responseID = e.Response.ID
		}
	case "response.output_item.added":
		return s.outputItemAdded(data)
	case "response.output_item.done":
		return s.outputItemDone(data)
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		var e deltaEvent
		if err := json.Unmarshal([]byte(data), &e); err != nil {
			return s.decodeErr(err)
		}
		s.addThinkingDelta(e.OutputIndex, e.Delta)
	case "response.reasoning_summary_part.done":
		var e struct {
			OutputIndex int `json:"output_index"`
		}
		if err := json.Unmarshal([]byte(data), &e); err != nil {
			return s.decodeErr(err)
		}
		s.addThinkingDelta(e.OutputIndex, "\n\n")
	case "response.output_text.delta", "response.refusal.delta":
		var e deltaEvent
		if err := json.Unmarshal([]byte(data), &e); err != nil {
			return s.decodeErr(err)
		}
		s.addTextDelta(e.OutputIndex, e.Delta)
	case "response.function_call_arguments.delta":
		var e deltaEvent
		if err := json.Unmarshal([]byte(data), &e); err != nil {
			return s.decodeErr(err)
		}
		s.addArgsDelta(e.OutputIndex, e.Delta)
	case "response.function_call_arguments.done":
		var e argumentsEvent
		if err := json.Unmarshal([]byte(data), &e); err != nil {
			return s.decodeErr(err)
		}
		s.finalizeArgs(e.OutputIndex, e.Arguments)
	case "response.completed", "response.incomplete", "response.done":
		var e responseEvent
		if err := json.Unmarshal([]byte(data), &e); err != nil {
			return s.decodeErr(err)
		}
		s.finalizeResponse(&e.Response)
	case "response.failed":
		var e failedEvent
		if err := json.Unmarshal([]byte(data), &e); err != nil {
			return s.decodeErr(err)
		}
		return s.failed(&e)
	case "error":
		var e errorEvent
		if err := json.Unmarshal([]byte(data), &e); err != nil {
			return s.decodeErr(err)
		}
		code, message := e.Code, e.Message
		if message == "" {
			message = "unknown error"
		}
		if code == "" {
			return fmt.Errorf("%s: %s", s.prefix, message)
		}
		return fmt.Errorf("%s: error code %s: %s", s.prefix, code, message)
	}
	return nil
}

func (s *streamState) decodeErr(err error) error {
	return fmt.Errorf("%s: decode stream event: %w", s.prefix, err)
}

func (s *streamState) outputItemAdded(data string) error {
	var e outputItemEvent
	if err := json.Unmarshal([]byte(data), &e); err != nil {
		return s.decodeErr(err)
	}
	var it outputItem
	if err := json.Unmarshal(e.Item, &it); err != nil {
		return s.decodeErr(err)
	}
	switch it.Type {
	case "reasoning":
		b := &strings.Builder{}
		s.blocks = append(s.blocks, agent.ContentBlock{Type: agent.BlockTypeThinking})
		s.builders = append(s.builders, b)
		s.slots[e.OutputIndex] = &slot{kind: "thinking", blockPos: len(s.blocks) - 1}
		if it.ID != "" {
			s.reasoningBlocks[it.ID] = len(s.blocks) - 1
		}
	case "message":
		if it.Phase == "final_answer" {
			s.stopReason = "stop"
		}
		b := &strings.Builder{}
		s.blocks = append(s.blocks, agent.ContentBlock{Type: agent.BlockTypeText})
		s.builders = append(s.builders, b)
		s.slots[e.OutputIndex] = &slot{kind: "text", blockPos: len(s.blocks) - 1}
	case "function_call":
		partial := &strings.Builder{}
		partial.WriteString(it.Arguments)
		s.blocks = append(s.blocks, agent.ContentBlock{
			Type: agent.BlockTypeToolCall,
			ID:   it.CallID + "|" + it.ID,
			Name: it.Name,
		})
		s.builders = append(s.builders, nil)
		s.slots[e.OutputIndex] = &slot{kind: "toolCall", blockPos: len(s.blocks) - 1, partial: partial}
	}
	return nil
}

func (s *streamState) outputItemDone(data string) error {
	var e outputItemEvent
	if err := json.Unmarshal([]byte(data), &e); err != nil {
		return s.decodeErr(err)
	}
	var it outputItem
	if err := json.Unmarshal(e.Item, &it); err != nil {
		return s.decodeErr(err)
	}
	sl, ok := s.slots[e.OutputIndex]
	if !ok {
		return nil
	}
	switch {
	case it.Type == "reasoning" && sl.kind == "thinking":
		var summary, content strings.Builder
		for _, p := range it.Summary {
			summary.WriteString(p.Text)
			summary.WriteString("\n\n")
		}
		for _, p := range it.Content {
			content.WriteString(p.Text)
			content.WriteString("\n\n")
		}
		if text := strings.TrimSuffix(summary.String(), "\n\n"); text != "" {
			s.blocks[sl.blockPos].Thinking = text
		} else if text := strings.TrimSuffix(content.String(), "\n\n"); text != "" {
			s.blocks[sl.blockPos].Thinking = text
		} else if b := s.builders[sl.blockPos]; b != nil {
			s.blocks[sl.blockPos].Thinking = b.String()
		}
		s.blocks[sl.blockPos].ThinkingSignature = string(e.Item)
		delete(s.slots, e.OutputIndex)
	case it.Type == "message" && sl.kind == "text":
		var text strings.Builder
		for _, p := range it.Content {
			if p.Type == "output_text" {
				text.WriteString(p.Text)
			} else {
				text.WriteString(p.Refusal)
			}
		}
		if text.Len() > 0 {
			s.blocks[sl.blockPos].Text = text.String()
		} else if b := s.builders[sl.blockPos]; b != nil {
			s.blocks[sl.blockPos].Text = b.String()
		}
		delete(s.slots, e.OutputIndex)
	case it.Type == "function_call" && sl.kind == "toolCall":
		args := it.Arguments
		if args == "" && sl.partial != nil {
			args = sl.partial.String()
		}
		if args == "" {
			args = "{}"
		}
		if json.Valid([]byte(args)) {
			s.blocks[sl.blockPos].Arguments = json.RawMessage(args)
		} else {
			s.blocks[sl.blockPos].Arguments = json.RawMessage("{}")
		}
		delete(s.slots, e.OutputIndex)
	}
	return nil
}

func (s *streamState) addTextDelta(index int, delta string) {
	sl, ok := s.slots[index]
	if !ok || sl.kind != "text" {
		return
	}
	if b := s.builders[sl.blockPos]; b != nil {
		b.WriteString(delta)
	}
	if delta != "" && s.onText != nil {
		s.onText(delta)
	}
}

func (s *streamState) addThinkingDelta(index int, delta string) {
	sl, ok := s.slots[index]
	if !ok || sl.kind != "thinking" {
		return
	}
	if b := s.builders[sl.blockPos]; b != nil {
		b.WriteString(delta)
	}
	if delta != "" && s.onThinking != nil {
		s.onThinking(delta)
	}
}

func (s *streamState) addArgsDelta(index int, delta string) {
	sl, ok := s.slots[index]
	if !ok || sl.kind != "toolCall" || sl.partial == nil {
		return
	}
	sl.partial.WriteString(delta)
}

func (s *streamState) finalizeArgs(index int, args string) {
	sl, ok := s.slots[index]
	if !ok || sl.kind != "toolCall" || sl.partial == nil {
		return
	}
	sl.partial.Reset()
	sl.partial.WriteString(args)
}

func (s *streamState) finalizeResponse(resp *response) {
	s.sawTerminal = true
	s.backfillReasoningSignatures(resp.Output)
	if resp.ID != "" {
		s.responseID = resp.ID
	}
	if resp.Usage != nil {
		s.usage = resp.Usage.toUsage()
	}
	reason := ""
	if resp.IncompleteDetails != nil {
		reason = resp.IncompleteDetails.Reason
	}
	stop, errMsg := mapStatus(resp.Status, reason)
	s.stopReason = stop
	s.errorMessage = errMsg
	if s.stopReason == "stop" && hasToolCall(s.blocks) {
		s.stopReason = "toolUse"
	}
}

func (s *streamState) backfillReasoningSignatures(output []struct {
	Type             string          `json:"type"`
	ID               string          `json:"id"`
	EncryptedContent json.RawMessage `json:"encrypted_content"`
}) {
	for _, item := range output {
		if item.Type != "reasoning" || len(item.EncryptedContent) == 0 {
			continue
		}
		blockPos, ok := s.reasoningBlocks[item.ID]
		if !ok {
			continue
		}
		block := &s.blocks[blockPos]
		if block.Type != agent.BlockTypeThinking || block.ThinkingSignature == "" {
			continue
		}
		var stored map[string]any
		if err := json.Unmarshal([]byte(block.ThinkingSignature), &stored); err != nil {
			continue
		}
		if _, ok := stored["encrypted_content"]; ok {
			continue
		}
		stored["encrypted_content"] = item.EncryptedContent
		if b, err := json.Marshal(stored); err == nil {
			block.ThinkingSignature = string(b)
		}
	}
}

func (s *streamState) failed(e *failedEvent) error {
	resp := &e.Response
	if err := resp.Error; err != nil {
		code := err.Code
		if code == "" {
			code = "unknown"
		}
		message := err.Message
		if message == "" {
			message = "no message"
		}
		return fmt.Errorf("%s: %s: %s", s.prefix, code, message)
	}
	if resp.IncompleteDetails != nil && resp.IncompleteDetails.Reason != "" {
		return fmt.Errorf("%s: incomplete: %s", s.prefix, resp.IncompleteDetails.Reason)
	}
	return errors.New(s.prefix + ": unknown error (no error details in response)")
}

func (s *streamState) result() *agent.AssistantMessage {
	for i := range s.blocks {
		switch s.blocks[i].Type {
		case agent.BlockTypeText:
			if b := s.builders[i]; b != nil && s.blocks[i].Text == "" {
				s.blocks[i].Text = b.String()
			}
		case agent.BlockTypeThinking:
			if b := s.builders[i]; b != nil && s.blocks[i].Thinking == "" {
				s.blocks[i].Thinking = b.String()
			}
		}
	}
	stopReason := s.stopReason
	if stopReason == "" {
		stopReason = "stop"
	}
	return &agent.AssistantMessage{
		Role:       string(agent.RoleAssistant),
		Content:    s.blocks,
		API:        s.api,
		Provider:   s.providerID,
		Model:      s.model,
		ResponseID: s.responseID,
		Usage:      s.usage,
		StopReason: stopReason,
		Timestamp:  agent.NowMillis(),
	}
}

func (u *usage) toUsage() agent.Usage {
	var cached, cacheWrite int64
	if d := u.InputTokensDetails; d != nil {
		cached = d.CachedTokens
		cacheWrite = d.CacheWriteTokens
	}
	var reasoning int64
	if d := u.OutputTokensDetails; d != nil {
		reasoning = d.ReasoningTokens
	}
	input := u.InputTokens - cached - cacheWrite
	if input < 0 {
		input = 0
	}
	return agent.Usage{
		Input:       input,
		Output:      u.OutputTokens,
		CacheRead:   cached,
		CacheWrite:  cacheWrite,
		Reasoning:   reasoning,
		TotalTokens: u.TotalTokens,
	}
}

func mapStatus(status, incompleteReason string) (stop, errMsg string) {
	switch status {
	case "", "completed", "in_progress", "queued":
		return "stop", ""
	case "incomplete":
		if incompleteReason == "max_output_tokens" {
			return "length", ""
		}
		if incompleteReason == "" {
			incompleteReason = "without a provider reason"
		}
		return "error", "Response incomplete: " + incompleteReason
	case "failed", "cancelled":
		return "error", ""
	default:
		return "error", "unknown response status: " + status
	}
}

func hasToolCall(blocks []agent.ContentBlock) bool {
	for _, b := range blocks {
		if b.Type == agent.BlockTypeToolCall {
			return true
		}
	}
	return false
}
