package gemini

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
)

// doneEvent is the SSE event some proxies append to terminate a stream;
// Gemini itself closes the connection instead.
const doneEvent = "[DONE]"

// maxSSELine is the largest accepted single SSE data line (8 MiB). It is
// a parser bound for one bufio.Scanner token, not a stream cap, matching
// the openai-completions driver.
const maxSSELine = 8 * 1024 * 1024

// readStream reads and parses the SSE stream of one streaming
// generateContent response, delivering text and thinking deltas to the
// callbacks and accumulating content blocks, tool calls, usage, and the
// stop reason. It returns the completed assistant message, or nil and an
// error when the stream aborts or ends prematurely. Each data line is a
// full GenerateContentResponse; usage metadata is cumulative, so the last
// chunk wins.
func (d *Gemini) readStream(ctx context.Context, resp *http.Response, model string, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	state := newStreamState(d.prefix, d.providerID, d.api, model, onText, onThinking)

	// The stream may stall with the connection open; closing the body
	// when the context is cancelled unblocks the reader below.
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
				return d.finish(ctx, scanner, state)
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
	return d.finish(ctx, scanner, state)
}

// finish applies the end-of-stream checks, mirroring pi-ai's post-loop
// validation order: cancellation, then "pending" (no finish reason), then
// an error or aborted stop reason.
func (d *Gemini) finish(ctx context.Context, scanner *bufio.Scanner, state *streamState) (*agent.AssistantMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", d.prefix, err)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%s: read stream: %w", d.prefix, err)
	}
	if state.stopReason == "error" {
		msg := state.rawStopReason
		if msg == "" {
			msg = "an unknown error occurred"
		}
		return nil, fmt.Errorf("%s: provider stopped with: %s", d.prefix, msg)
	}
	if !state.gotFinishReason {
		return nil, errors.New(d.prefix + ": stream ended without a finish reason")
	}
	return state.result(), nil
}

// streamState accumulates the parsed output of one streaming response.
type streamState struct {
	prefix          string
	providerID      string
	api             string
	model           string
	blocks          []agent.ContentBlock
	builders        []*strings.Builder // aligned with blocks; holds Text or Thinking content
	openText        int                // position of the open text block, -1 when closed
	openThinking    int                // position of the open thinking block, -1 when closed
	usedIDs         map[string]bool    // tool call ids already emitted, for duplicate detection
	responseID      string
	usage           agent.Usage
	stopReason      string
	rawStopReason   string
	gotFinishReason bool
	onText          func(string)
	onThinking      func(string)
}

// newStreamState returns an empty state for the given provider identity,
// model, and callbacks. prefix is the driver's error-message prefix.
func newStreamState(prefix, providerID, api, model string, onText func(string), onThinking func(string)) *streamState {
	return &streamState{
		prefix:       prefix,
		providerID:   providerID,
		api:          api,
		model:        model,
		openText:     -1,
		openThinking: -1,
		usedIDs:      make(map[string]bool),
		onText:       onText,
		onThinking:   onThinking,
	}
}

// apply parses one SSE data payload (a full GenerateContentResponse) and
// folds it into the state, mirroring pi-ai's per-chunk processing order:
// response id, candidates (parts then finishReason), then usage.
func (s *streamState) apply(data string) error {
	var chunk StreamChunk
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return fmt.Errorf("%s: decode stream chunk: %w", s.prefix, err)
	}
	if chunk.ResponseID != "" && s.responseID == "" {
		s.responseID = chunk.ResponseID
	}
	// A prompt block arrives with no candidates and no finish reason;
	// surface it as an error result (pi-ai relies on the SDK for this).
	if fb := chunk.PromptFeedback; fb != nil && fb.BlockReason != "" {
		msg := fb.BlockReason
		if fb.BlockReasonMessage != "" {
			msg += ": " + fb.BlockReasonMessage
		}
		return fmt.Errorf("%s: prompt blocked: %s", s.prefix, msg)
	}
	if len(chunk.Candidates) > 0 {
		c := &chunk.Candidates[0]
		if c.Content != nil {
			for i := range c.Content.Parts {
				if err := s.applyPart(&c.Content.Parts[i]); err != nil {
					return err
				}
			}
		}
		if c.FinishReason != "" {
			s.gotFinishReason = true
			s.rawStopReason = c.FinishReason
			s.stopReason = mapStopReason(c.FinishReason)
			// A STOP finish reason still means toolUse when the turn
			// produced tool calls, mirroring pi-ai's override.
			if s.stopReason == "stop" && hasToolCall(s.blocks) {
				s.stopReason = "toolUse"
			}
		}
	}
	// Usage metadata is cumulative: the last chunk wins, never summed.
	if chunk.UsageMetadata != nil {
		s.usage = chunk.UsageMetadata.toUsage()
	}
	return nil
}

// applyPart folds one response part into the state, mirroring pi-ai's
// block switching: text and thinking parts accumulate into the open block
// of the matching type (starting a new one when the type changes), and
// functionCall parts close the open block and append a toolCall block.
func (s *streamState) applyPart(p *Part) error {
	if p.Text != nil {
		isThinking := p.Thought
		open := (s.openText >= 0) || (s.openThinking >= 0)
		if !open || (isThinking && s.openText >= 0) || (!isThinking && s.openThinking >= 0) {
			s.closeBlocks()
			if isThinking {
				b := &strings.Builder{}
				s.blocks = append(s.blocks, agent.ContentBlock{Type: agent.BlockTypeThinking})
				s.builders = append(s.builders, b)
				s.openThinking = len(s.blocks) - 1
			} else {
				b := &strings.Builder{}
				s.blocks = append(s.blocks, agent.ContentBlock{Type: agent.BlockTypeText})
				s.builders = append(s.builders, b)
				s.openText = len(s.blocks) - 1
			}
		}
		if isThinking {
			s.builders[s.openThinking].WriteString(*p.Text)
			// Some backends only send the signature on the first delta;
			// keep the last non-empty one.
			s.blocks[s.openThinking].ThinkingSignature = retainThoughtSignature(s.blocks[s.openThinking].ThinkingSignature, p.ThoughtSignature)
			if *p.Text != "" && s.onThinking != nil {
				s.onThinking(*p.Text)
			}
		} else {
			s.builders[s.openText].WriteString(*p.Text)
			if *p.Text != "" && s.onText != nil {
				s.onText(*p.Text)
			}
		}
	}
	if p.FunctionCall != nil {
		s.closeBlocks()
		id := p.FunctionCall.ID
		// Gemini function calls have no reliable id: synthesize one of
		// the pi-ai shape name_timestamp_counter when absent or when the
		// provider repeats an id already used in this turn.
		if id == "" || s.usedIDs[id] {
			id = fmt.Sprintf("%s_%d_%d", p.FunctionCall.Name, time.Now().UnixMilli(), toolCallCounter.Add(1))
		}
		s.usedIDs[id] = true
		args := p.FunctionCall.Args
		if len(args) == 0 {
			args = json.RawMessage("{}")
		}
		s.blocks = append(s.blocks, agent.ContentBlock{
			Type:      agent.BlockTypeToolCall,
			ID:        id,
			Name:      p.FunctionCall.Name,
			Arguments: args,
		})
		s.builders = append(s.builders, nil)
	}
	return nil
}

// closeBlocks marks every open content block as closed.
func (s *streamState) closeBlocks() {
	s.openText = -1
	s.openThinking = -1
}

// result assembles the final assistant message from the accumulated
// state, materializing the builder-held text and thinking content.
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

// toUsage maps the cumulative wire usage onto agent.Usage, mirroring
// pi-ai: cached tokens are excluded from the input count, reasoning
// tokens ride inside the output count, and cost stays zero.
func (u *UsageMetadata) toUsage() agent.Usage {
	return agent.Usage{
		Input:       u.PromptTokenCount - u.CachedContentTokenCount,
		Output:      u.CandidatesTokenCount + u.ThoughtsTokenCount,
		CacheRead:   u.CachedContentTokenCount,
		Reasoning:   u.ThoughtsTokenCount,
		TotalTokens: u.TotalTokenCount,
	}
}

// hasToolCall reports whether any accumulated block is a toolCall block.
func hasToolCall(blocks []agent.ContentBlock) bool {
	for _, b := range blocks {
		if b.Type == agent.BlockTypeToolCall {
			return true
		}
	}
	return false
}
