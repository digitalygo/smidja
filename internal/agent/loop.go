package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Recorder is the persistence seam RunTurn writes every message to as the
// turn progresses. The session package's *session.Session satisfies this
// interface directly; the CLI wraps it in an adapter so the loop stays
// decoupled from the session format. Implementations must accept
// sequential calls; RunTurn never calls them concurrently.
type Recorder interface {
	AppendUser(*UserMessage) error
	AppendAssistant(*AssistantMessage) error
	AppendToolResult(*ToolResultMessage) error
}

// LoopDeps carries the dependencies of one RunTurn invocation.
type LoopDeps struct {
	// Client is the provider seam used for every assistant turn. It is
	// required.
	Client Client

	// Tools is the registry of tools the model may call, looked up by
	// Name on each toolCall block. May be nil or empty; tool calls then
	// resolve to unknown-tool error results.
	Tools []Tool

	// Recorder persists every message produced by the turn, in order:
	// the user message, each assistant message, and each tool result.
	// May be nil to disable persistence.
	Recorder Recorder

	// Stdout is the streaming output target for text deltas. May be nil
	// to discard the streamed output.
	Stdout io.Writer

	// OnThinking receives thinking deltas for display, when the caller
	// wants them. It may be nil to discard thinking output; the loop
	// itself never decides what to show, it always forwards both text
	// and thinking deltas to the caller-provided sinks. The CLI wires
	// this only when SMIDJA_SHOW_THINKING is set.
	OnThinking func(string)
}

// RunTurn runs one user turn of the agentic loop: it records the user
// message, then alternates assistant turns with tool executions until the
// model stops requesting tools or the context is cancelled.
//
// The user message is built from userInput as a JSON string (so quoting is
// correct for the provider), recorded through the Recorder, and appended to
// history. Each assistant turn streams its text deltas to deps.Stdout and
// its thinking deltas to deps.OnThinking as they arrive, and the completed
// message is recorded and appended. When the model requests tool calls
// (StopReason "toolUse"), the loop executes them in block order, records
// each result immediately, and starts the next round with the full history.
// Unknown tools and tool calls whose arguments are not valid JSON produce
// error results without executing anything.
//
// The loop has no round or tool-call budget: it keeps alternating assistant
// turns with tool executions until the model stops on its own ("stop",
// "error", or "aborted", or "toolUse" without tool calls), the client
// fails, or the context is cancelled (wrapped so errors.Is matches
// context.Canceled/DeadlineExceeded). RunTurn returns the accumulated
// history and nil when the model stopped on its own, and the history plus
// an error otherwise.
func RunTurn(ctx context.Context, deps *LoopDeps, model string, system string, history []*Message, userInput string) ([]*Message, error) {
	if deps == nil {
		return history, errors.New("agent: nil loop deps")
	}
	if deps.Client == nil {
		return history, errors.New("agent: nil client")
	}

	userContent, err := json.Marshal(userInput)
	if err != nil {
		return history, fmt.Errorf("agent: marshal user input: %w", err)
	}
	userMsg := &UserMessage{Role: string(RoleUser), Content: userContent, Timestamp: NowMillis()}
	if deps.Recorder != nil {
		if err := deps.Recorder.AppendUser(userMsg); err != nil {
			return history, fmt.Errorf("agent: record user message: %w", err)
		}
	}
	history = append(history, &Message{User: userMsg})

	onText := func(delta string) {
		if deps.Stdout != nil {
			io.WriteString(deps.Stdout, delta)
		}
	}
	onThinking := func(delta string) {
		if deps.OnThinking != nil {
			deps.OnThinking(delta)
		}
	}
	toolsByName := make(map[string]Tool, len(deps.Tools))
	for _, t := range deps.Tools {
		if t != nil {
			toolsByName[t.Name()] = t
		}
	}

	for {
		if err := ctx.Err(); err != nil {
			return history, fmt.Errorf("agent: %w", err)
		}
		asst, err := deps.Client.StreamTurn(ctx, &TurnRequest{
			Model:    model,
			System:   system,
			Messages: history,
			Tools:    deps.Tools,
		}, onText, onThinking)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return history, fmt.Errorf("agent: %w", ctxErr)
			}
			return history, fmt.Errorf("agent: stream turn: %w", err)
		}
		if asst == nil {
			return history, errors.New("agent: client returned a nil assistant message")
		}
		if deps.Recorder != nil {
			if err := deps.Recorder.AppendAssistant(asst); err != nil {
				return history, fmt.Errorf("agent: record assistant message: %w", err)
			}
		}
		history = append(history, &Message{Assistant: asst})

		if asst.StopReason != "toolUse" {
			return history, nil
		}
		calls := toolCallBlocks(asst.Content)
		if len(calls) == 0 {
			return history, nil
		}
		for _, call := range calls {
			res := executeCall(ctx, call, toolsByName)
			if deps.Recorder != nil {
				if err := deps.Recorder.AppendToolResult(res); err != nil {
					return history, fmt.Errorf("agent: record tool result: %w", err)
				}
			}
			history = append(history, &Message{ToolResult: res})
		}
	}
}

// executeCall runs one toolCall block and converts the outcome into the
// tool result message fed back to the model. Unknown tools and arguments
// that are not valid JSON produce error results without executing
// anything.
func executeCall(ctx context.Context, call ContentBlock, toolsByName map[string]Tool) *ToolResultMessage {
	tool, ok := toolsByName[call.Name]
	if !ok {
		return errorToolResult(call, fmt.Sprintf("unknown tool %q", call.Name))
	}
	if !json.Valid(call.Arguments) {
		return errorToolResult(call, fmt.Sprintf("tool %q: invalid arguments: not valid JSON", call.Name))
	}
	return resultToToolResult(call, tool.Exec(ctx, call.Arguments))
}

// toolCallBlocks returns the content blocks of the given assistant content
// whose type is toolCall, in order.
func toolCallBlocks(content []ContentBlock) []ContentBlock {
	var out []ContentBlock
	for _, b := range content {
		if b.Type == BlockTypeToolCall {
			out = append(out, b)
		}
	}
	return out
}

// errorToolResult builds a failing tool result message whose single text
// block describes the problem.
func errorToolResult(call ContentBlock, text string) *ToolResultMessage {
	return &ToolResultMessage{
		Role:       string(RoleToolResult),
		ToolCallID: call.ID,
		ToolName:   call.Name,
		Content:    []ContentBlock{{Type: BlockTypeText, Text: text}},
		IsError:    true,
		Timestamp:  NowMillis(),
	}
}

// resultToToolResult converts a tool's Result into the tool result message
// fed back to the model. Content blocks pass through as-is; when the
// result carries no text block at all, an empty text block is appended so
// the message always has a readable content for the provider.
func resultToToolResult(call ContentBlock, res Result) *ToolResultMessage {
	content := res.Content
	hasText := false
	for _, b := range content {
		if b.Type == BlockTypeText {
			hasText = true
			break
		}
	}
	if !hasText {
		content = append(append([]ContentBlock(nil), content...), ContentBlock{Type: BlockTypeText, Text: ""})
	}
	return &ToolResultMessage{
		Role:       string(RoleToolResult),
		ToolCallID: call.ID,
		ToolName:   call.Name,
		Content:    content,
		IsError:    res.IsError,
		Timestamp:  NowMillis(),
	}
}
