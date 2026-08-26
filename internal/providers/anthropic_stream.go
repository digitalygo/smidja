package providers

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

var anthropicMessageEvents = map[string]bool{
	"message_start":       true,
	"message_delta":       true,
	"message_stop":        true,
	"content_block_start": true,
	"content_block_delta": true,
	"content_block_stop":  true,
}

type anthropicEvent struct {
	Type         string                 `json:"type"`
	Message      *anthropicMessage      `json:"message,omitempty"`
	Index        int                    `json:"index,omitempty"`
	ContentBlock *anthropicContentBlock `json:"content_block,omitempty"`
	Delta        json.RawMessage        `json:"delta,omitempty"`
	Usage        *anthropicUsage        `json:"usage,omitempty"`
}

type anthropicMessage struct {
	ID    string          `json:"id"`
	Usage *anthropicUsage `json:"usage"`
}

type anthropicContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	Signature string          `json:"signature,omitempty"`
	Data      string          `json:"data,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
}

type anthropicDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	Thinking    string `json:"thinking,omitempty"`
	Signature   string `json:"signature,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
}

type anthropicMessageDelta struct {
	StopReason  string `json:"stop_reason"`
	StopDetails struct {
		Explanation string `json:"explanation"`
	} `json:"stop_details"`
}

type anthropicUsage struct {
	InputTokens              *int64                  `json:"input_tokens"`
	OutputTokens             *int64                  `json:"output_tokens"`
	CacheReadInputTokens     *int64                  `json:"cache_read_input_tokens"`
	CacheCreationInputTokens *int64                  `json:"cache_creation_input_tokens"`
	OutputTokensDetails      *anthropicOutputDetails `json:"output_tokens_details,omitempty"`
}

type anthropicOutputDetails struct {
	ThinkingTokens *int64 `json:"thinking_tokens"`
}

func (d *Anthropic) readStream(ctx context.Context, resp *http.Response, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	state := newAnthropicStreamState(d.prefix, d.providerID, req.Model, d.oauth, req.Tools, onText, onThinking)

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

	var eventName string
	var dataLines []string
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		switch {
		case line == "":
			if len(dataLines) == 0 {
				continue
			}
			data := strings.Join(dataLines, "\n")
			dataLines = dataLines[:0]
			if err := state.handleEvent(eventName, data); err != nil {
				return nil, err
			}
			eventName = ""
		case strings.HasPrefix(line, ":"):
		case strings.HasPrefix(line, "event:"):
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimPrefix(line, "data:"))
		}
	}
	if len(dataLines) > 0 {
		if err := state.handleEvent(eventName, strings.Join(dataLines, "\n")); err != nil {
			return nil, err
		}
	}

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", d.prefix, err)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%s: read stream: %w", d.prefix, err)
	}
	return state.finish()
}

type anthropicStreamState struct {
	prefix     string
	providerID string
	model      string
	oauth      bool
	tools      []agent.Tool
	blocks     []agent.ContentBlock
	args       []*strings.Builder
	byIndex    map[int]int
	sawStart   bool
	sawEnd     bool
	sawStop    bool
	responseID string
	usage      agent.Usage
	stopReason string
	errorMsg   string
	onText     func(string)
	onThinking func(string)
}

func newAnthropicStreamState(prefix, providerID, model string, oauth bool, tools []agent.Tool, onText func(string), onThinking func(string)) *anthropicStreamState {
	return &anthropicStreamState{
		prefix:     prefix,
		providerID: providerID,
		model:      model,
		oauth:      oauth,
		tools:      tools,
		byIndex:    make(map[int]int),
		onText:     onText,
		onThinking: onThinking,
	}
}

func (s *anthropicStreamState) handleEvent(name, data string) error {
	if name == "error" {
		return fmt.Errorf("%s: provider stream error: %s", s.prefix, data)
	}
	if name == "" {
		var probe struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(data), &probe); err != nil || !anthropicMessageEvents[probe.Type] {
			return nil
		}
		name = probe.Type
	} else if !anthropicMessageEvents[name] {
		return nil
	}

	var ev anthropicEvent
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		return fmt.Errorf("%s: decode anthropic SSE event %s: %v", s.prefix, name, err)
	}
	if ev.Type == "" {
		ev.Type = name
	}
	return s.apply(&ev)
}

func (s *anthropicStreamState) apply(ev *anthropicEvent) error {
	switch ev.Type {
	case "message_start":
		s.sawStart = true
		if m := ev.Message; m != nil {
			s.responseID = m.ID
			if u := m.Usage; u != nil {
				s.applyUsage(u, true)
			}
		}
	case "content_block_start":
		if ev.ContentBlock != nil {
			s.startBlock(ev.Index, ev.ContentBlock)
		}
	case "content_block_delta":
		if len(ev.Delta) == 0 {
			return nil
		}
		var d anthropicDelta
		if err := json.Unmarshal(ev.Delta, &d); err != nil {
			return fmt.Errorf("%s: decode content_block_delta: %v", s.prefix, err)
		}
		s.applyDelta(ev.Index, &d)
	case "content_block_stop":
		s.stopBlock(ev.Index)
	case "message_delta":
		if len(ev.Delta) > 0 {
			var md anthropicMessageDelta
			if err := json.Unmarshal(ev.Delta, &md); err != nil {
				return fmt.Errorf("%s: decode message_delta: %v", s.prefix, err)
			}
			if md.StopReason != "" {
				stop, errMsg, err := s.mapStopReason(md.StopReason, md.StopDetails.Explanation)
				if err != nil {
					return err
				}
				s.stopReason = stop
				s.errorMsg = errMsg
				s.sawStop = true
			}
		}
		if ev.Usage != nil {
			s.applyUsage(ev.Usage, false)
		}
	case "message_stop":
		s.sawEnd = true
	}
	return nil
}

func (s *anthropicStreamState) applyUsage(u *anthropicUsage, overwrite bool) {
	if overwrite {
		s.usage.Input = ptrOrZero(u.InputTokens)
		s.usage.Output = ptrOrZero(u.OutputTokens)
		s.usage.CacheRead = ptrOrZero(u.CacheReadInputTokens)
		s.usage.CacheWrite = ptrOrZero(u.CacheCreationInputTokens)
	} else {
		if u.InputTokens != nil {
			s.usage.Input = *u.InputTokens
		}
		if u.OutputTokens != nil {
			s.usage.Output = *u.OutputTokens
		}
		if u.CacheReadInputTokens != nil {
			s.usage.CacheRead = *u.CacheReadInputTokens
		}
		if u.CacheCreationInputTokens != nil {
			s.usage.CacheWrite = *u.CacheCreationInputTokens
		}
	}
	if d := u.OutputTokensDetails; d != nil && d.ThinkingTokens != nil {
		s.usage.Reasoning = *d.ThinkingTokens
	}
	s.usage.TotalTokens = s.usage.Input + s.usage.Output + s.usage.CacheRead + s.usage.CacheWrite
}

func ptrOrZero(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func (s *anthropicStreamState) startBlock(index int, cb *anthropicContentBlock) {
	block := agent.ContentBlock{}
	switch cb.Type {
	case "text":
		block = agent.ContentBlock{Type: agent.BlockTypeText, Text: cb.Text}
	case "thinking":
		block = agent.ContentBlock{Type: agent.BlockTypeThinking, Thinking: cb.Thinking, ThinkingSignature: cb.Signature}
	case "redacted_thinking":
		block = agent.ContentBlock{Type: agent.BlockTypeThinking, Thinking: "[Reasoning redacted]", ThinkingSignature: cb.Data, Redacted: true}
	case "tool_use":
		block = agent.ContentBlock{Type: agent.BlockTypeToolCall, ID: cb.ID, Name: toolNameFromWire(cb.Name, s.oauth, s.tools), Arguments: toolInput(cb.Input)}
	default:
		return
	}
	s.blocks = append(s.blocks, block)
	pos := len(s.blocks) - 1
	s.byIndex[index] = pos
	if block.Type == agent.BlockTypeToolCall {
		s.args = append(s.args, &strings.Builder{})
	} else {
		s.args = append(s.args, nil)
	}
}

func (s *anthropicStreamState) applyDelta(index int, d *anthropicDelta) {
	pos, ok := s.byIndex[index]
	if !ok {
		return
	}
	switch d.Type {
	case "text_delta":
		if d.Text == "" {
			return
		}
		s.blocks[pos].Text += d.Text
		if s.onText != nil {
			s.onText(d.Text)
		}
	case "thinking_delta":
		if d.Thinking == "" {
			return
		}
		s.blocks[pos].Thinking += d.Thinking
		if s.onThinking != nil {
			s.onThinking(d.Thinking)
		}
	case "signature_delta":
		s.blocks[pos].ThinkingSignature += d.Signature
	case "input_json_delta":
		if b := s.args[pos]; b != nil {
			b.WriteString(d.PartialJSON)
		}
	}
}

func (s *anthropicStreamState) stopBlock(index int) {
	pos, ok := s.byIndex[index]
	if !ok {
		return
	}
	b := s.args[pos]
	if b == nil || b.Len() == 0 {
		return
	}
	raw := b.String()
	if json.Valid([]byte(raw)) {
		s.blocks[pos].Arguments = json.RawMessage(raw)
	}
}

func (s *anthropicStreamState) mapStopReason(reason, explanation string) (string, string, error) {
	switch reason {
	case "end_turn":
		return "stop", "", nil
	case "max_tokens":
		return "length", "", nil
	case "tool_use":
		return "toolUse", "", nil
	case "refusal":
		errMsg := explanation
		if errMsg == "" {
			errMsg = "The model refused to complete the request"
		}
		return "error", errMsg, nil
	case "pause_turn":
		return "stop", "", nil
	case "stop_sequence":
		return "stop", "", nil
	case "sensitive":
		return "error", "Provider stopped with: sensitive", nil
	default:
		return "", "", fmt.Errorf("%s: unhandled stop reason: %s", s.prefix, reason)
	}
}

func (s *anthropicStreamState) finish() (*agent.AssistantMessage, error) {
	if !s.sawEnd {
		if !s.sawStart {
			return nil, errors.New(s.prefix + ": stream ended prematurely before message_start")
		}
		return nil, errors.New(s.prefix + ": stream ended prematurely before message_stop")
	}
	if !s.sawStop {
		return nil, errors.New(s.prefix + ": stream ended without a stop reason")
	}
	if s.stopReason == "error" {
		msg := s.errorMsg
		if msg == "" {
			msg = "an unknown error occurred"
		}
		return nil, errors.New(s.prefix + ": " + msg)
	}
	return &agent.AssistantMessage{
		Role:       string(agent.RoleAssistant),
		Content:    s.blocks,
		API:        AnthropicAPI,
		Provider:   s.providerID,
		Model:      s.model,
		ResponseID: s.responseID,
		Usage:      s.usage,
		StopReason: s.stopReason,
		Timestamp:  agent.NowMillis(),
	}, nil
}
