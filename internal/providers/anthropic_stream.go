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

// anthropicMessageEvents is the set of SSE event names the messages API
// streams. Every other event (ping, ...) is ignored, exactly as Pi's
// ANTHROPIC_MESSAGE_EVENTS. The messages API does not use a [DONE]
// sentinel; message_stop terminates the stream.
var anthropicMessageEvents = map[string]bool{
	"message_start":       true,
	"message_delta":       true,
	"message_stop":        true,
	"content_block_start": true,
	"content_block_delta": true,
	"content_block_stop":  true,
}

// anthropicEvent is the JSON envelope shared by every messages API SSE
// event. Delta holds the type-specific fragment for content_block_delta
// and message_delta and is decoded per event type.
type anthropicEvent struct {
	Type         string                 `json:"type"`
	Message      *anthropicMessage      `json:"message,omitempty"`
	Index        int                    `json:"index,omitempty"`
	ContentBlock *anthropicContentBlock `json:"content_block,omitempty"`
	Delta        json.RawMessage        `json:"delta,omitempty"`
	Usage        *anthropicUsage        `json:"usage,omitempty"`
}

// anthropicMessage is the message object of a message_start event; only
// the id and the initial usage are consumed.
type anthropicMessage struct {
	ID    string          `json:"id"`
	Usage *anthropicUsage `json:"usage"`
}

// anthropicContentBlock is a content_block_start payload: text, thinking,
// redacted_thinking, or tool_use.
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

// anthropicDelta is a content_block_delta payload; exactly one field is
// set per delta type.
type anthropicDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	Thinking    string `json:"thinking,omitempty"`
	Signature   string `json:"signature,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
}

// anthropicMessageDelta is a message_delta payload carrying the stop
// reason and its optional explanation.
type anthropicMessageDelta struct {
	StopReason  string `json:"stop_reason"`
	StopDetails struct {
		Explanation string `json:"explanation"`
	} `json:"stop_details"`
}

// anthropicUsage is the token accounting. Counts are pointers so null
// fields (which proxies send in message_delta) decode without error; nil
// means "not present", mirroring Pi's null checks.
type anthropicUsage struct {
	InputTokens              *int64                  `json:"input_tokens"`
	OutputTokens             *int64                  `json:"output_tokens"`
	CacheReadInputTokens     *int64                  `json:"cache_read_input_tokens"`
	CacheCreationInputTokens *int64                  `json:"cache_creation_input_tokens"`
	OutputTokensDetails      *anthropicOutputDetails `json:"output_tokens_details,omitempty"`
}

// anthropicOutputDetails breaks down output tokens; thinking_tokens is the
// reasoning subset reported on the final message_delta.
type anthropicOutputDetails struct {
	ThinkingTokens *int64 `json:"thinking_tokens"`
}

// readStream reads and parses the SSE stream of one messages response,
// delivering text and thinking deltas to the callbacks and accumulating
// content blocks, tool calls, usage, and the stop reason. It returns the
// completed assistant message, or nil and an error when the stream aborts
// or ends prematurely. Error messages are prefixed with the driver's
// provider prefix.
func (d *Anthropic) readStream(ctx context.Context, resp *http.Response, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	state := newAnthropicStreamState(d.prefix, d.providerID, req.Model, d.oauth, req.Tools, onText, onThinking)

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
			// SSE comment line; ignore.
		case strings.HasPrefix(line, "event:"):
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimPrefix(line, "data:"))
		}
	}
	// Flush a trailing event whose terminating blank line never arrived.
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

// anthropicStreamState accumulates the parsed output of one streaming
// messages response. Content blocks are keyed by their wire index
// (byIndex) so deltas and stops land on the right block regardless of
// interleaving.
type anthropicStreamState struct {
	prefix     string
	providerID string
	model      string
	oauth      bool
	tools      []agent.Tool
	blocks     []agent.ContentBlock
	args       []*strings.Builder // tool-call argument fragments, aligned with blocks
	byIndex    map[int]int        // wire content index -> block position
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

// newAnthropicStreamState returns an empty state for the given provider
// identity, model, and callbacks. prefix is the driver's error-message
// prefix.
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

// handleEvent routes one flushed SSE event. The error event aborts the
// stream with its raw payload, exactly as Pi throws the error data; named
// message events are parsed and applied; everything else is ignored.
func (s *anthropicStreamState) handleEvent(name, data string) error {
	if name == "error" {
		return fmt.Errorf("%s: provider stream error: %s", s.prefix, data)
	}
	if name == "" {
		// Data without an event name: dispatch only when the payload is a
		// recognized message event; anything else is ignored.
		var probe struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(data), &probe); err != nil || !anthropicMessageEvents[probe.Type] {
			return nil
		}
		name = probe.Type
	} else if !anthropicMessageEvents[name] {
		return nil // ping and other non-message events are ignored
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

// apply folds one parsed message event into the state.
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

// applyUsage folds a usage payload into the running accounting.
// message_start usage overwrites every field; message_delta usage only
// updates fields that are present, so a proxy that omits input counts does
// not wipe the values captured at start. TotalTokens is always recomputed
// from the components, since Anthropic does not report it.
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

// ptrOrZero dereferences a nullable token count.
func ptrOrZero(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// startBlock opens a content block at the wire index. Blocks are appended
// in first-appearance order and the index map keeps later deltas and stops
// on the right position even when blocks interleave.
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
		return // unknown block types are ignored, as in Pi
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

// applyDelta folds one content_block_delta fragment into the block at the
// wire index. Unknown delta types are ignored, as in Pi.
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

// stopBlock finalizes the block at the wire index: the accumulated
// input_json_delta fragments become the tool arguments when they assemble
// into valid JSON, otherwise the start input is kept.
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

// mapStopReason maps a messages API stop_reason onto agent stop reasons,
// ported from Pi's mapStopReason: end_turn, pause_turn, and stop_sequence
// become "stop", tool_use becomes "toolUse", max_tokens becomes "length",
// and refusal and sensitive stops become errors. Unknown stop reasons abort
// the turn, exactly as Pi throws.
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

// finish assembles the final assistant message, validating that the stream
// terminated cleanly: a message_stop event and a stop reason must have been
// observed, and a refused or otherwise failed turn surfaces as an error,
// mirroring Pi.
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
