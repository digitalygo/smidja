package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

type Recorder interface {
	AppendUser(*UserMessage) error
	AppendAssistant(*AssistantMessage) error
	AppendToolResult(*ToolResultMessage) error
}

type LoopDeps struct {
	Client Client

	Tools []Tool

	Catalog ToolCatalog

	Recorder Recorder

	Stdout io.Writer

	OnThinking func(string)

	Preparer ContextPreparer

	Hooks HookDispatcher

	Detector LoopDetector

	SessionEntryIDs []string

	RetryPolicy RetryPolicy

	RetryPolicySet bool

	Retry func(ctx context.Context, produce func(context.Context) (*AssistantMessage, error), policy RetryPolicy, callbacks *RetryCallbacks) (*AssistantMessage, error)

	IsContextOverflow func(errorMessage string) bool

	OnRetryScheduled func(attempt, maxAttempts int, delayMs int64, errorMessage string)

	OnRetryFinished func(success bool, attempt int, finalError string)
}

var (
	ErrContextOverflow = errors.New("agent: context overflow")

	ErrLoopDetected = errors.New("agent: loop detected")
)

type ContextOverflowError struct {
	Assistant *AssistantMessage

	Err error
}

func (e *ContextOverflowError) Error() string {
	if e == nil {
		return ErrContextOverflow.Error()
	}
	if e.Assistant != nil && e.Assistant.ErrorMessage != "" {
		return "agent: context overflow: " + e.Assistant.ErrorMessage
	}
	if e.Err != nil {
		return "agent: context overflow: " + e.Err.Error()
	}
	return ErrContextOverflow.Error()
}

func (e *ContextOverflowError) Unwrap() error { return e.Err }

type LoopDetectedError struct {
	Outcome Outcome

	Call ContentBlock

	Err error
}

func (e *LoopDetectedError) Error() string {
	if e == nil {
		return ErrLoopDetected.Error()
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return ErrLoopDetected.Error()
}

func (e *LoopDetectedError) Unwrap() error { return e.Err }

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
	for _, t := range catalogTools(deps) {
		if t != nil {
			toolsByName[t.Name()] = t
		}
	}

	policy := DefaultRetryPolicy()
	if deps.RetryPolicySet || deps.RetryPolicy != (RetryPolicy{}) {
		policy = deps.RetryPolicy
	}
	overflow := deps.IsContextOverflow

	var lastUsageInput int64
	turnIndex := 0

	for {
		if err := ctx.Err(); err != nil {
			return history, fmt.Errorf("agent: %w", err)
		}

		req := ContextRequest{
			Messages:       append([]*Message(nil), history...),
			System:         system,
			LastUsageInput: lastUsageInput,
			EntryIDs:       deps.SessionEntryIDs,
		}
		cres := ContextResult{Messages: req.Messages, System: req.System}
		if deps.Preparer != nil {
			if cres, err = deps.Preparer.Prepare(ctx, req); err != nil {
				return history, fmt.Errorf("agent: prepare context: %w", err)
			}
		}
		if deps.Hooks != nil {
			hres, herr := deps.Hooks.Context(ctx, ContextRequest{
				Messages: cres.Messages,
				System:   cres.System,
				EntryIDs: req.EntryIDs,
			})
			if herr != nil {
				return history, fmt.Errorf("agent: context hook: %w", herr)
			}
			if hres.Messages != nil {
				cres.Messages = hres.Messages
				cres.System = hres.System
			}
		}

		turnReq := &TurnRequest{
			Model:    model,
			System:   cres.System,
			Messages: cres.Messages,
			Tools:    catalogTools(deps),
		}
		var lastMsg *AssistantMessage
		produce := func(pctx context.Context) (*AssistantMessage, error) {
			if deps.Preparer != nil {
				deps.Preparer.ObserveRequest(time.Now())
			}
			msg, perr := deps.Client.StreamTurn(pctx, turnReq, onText, onThinking)
			if perr != nil {
				return nil, perr
			}
			if msg != nil {
				lastMsg = msg
				if deps.Preparer != nil {
					deps.Preparer.ObserveResponse(msg)
				}
			}
			return msg, nil
		}

		var asst *AssistantMessage
		if deps.Retry != nil {
			asst, err = deps.Retry(ctx, produce, policy, retryCallbacks(deps, ctx))
		} else {
			asst, err = produce(ctx)
		}
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return history, fmt.Errorf("agent: %w", ctxErr)
			}
			if overflow != nil && overflow(err.Error()) {
				return history, &ContextOverflowError{
					Assistant: lastMsg,
					Err:       fmt.Errorf("%w: %v", ErrContextOverflow, err),
				}
			}
			return history, fmt.Errorf("agent: stream turn: %w", err)
		}
		if asst == nil {
			return history, errors.New("agent: client returned a nil assistant message")
		}
		lastUsageInput = asst.Usage.Input

		finalMsg := &Message{Assistant: asst}
		if asst.StopReason != "error" && deps.Hooks != nil {
			replaced, merr := deps.Hooks.MessageEnd(ctx, finalMsg)
			if merr != nil {
				return history, fmt.Errorf("agent: message_end hook: %w", merr)
			}
			if replaced != nil {
				if replaced.Assistant == nil {
					return history, errors.New("agent: message_end hook replaced the assistant message with another role")
				}
				finalMsg = replaced
			}
		}
		calls := toolCallBlocks(finalMsg.Assistant.Content)
		denied := make([]bool, len(calls))
		denyReason := make([]string, len(calls))
		if finalMsg.Assistant.StopReason == "toolUse" && deps.Hooks != nil {
			for i := range calls {
				call := &calls[i]
				dec, derr := deps.Hooks.ToolCall(ctx, call.Name, call.ID, call.Arguments)
				if derr != nil {
					return history, fmt.Errorf("agent: tool_call hook: %w", derr)
				}
				if dec.Block {
					denied[i] = true
					denyReason[i] = dec.Reason
				}
				if dec.FinalArgs != nil {
					call.Arguments = dec.FinalArgs
					applyFinalArgs(finalMsg.Assistant, call.ID, call.Arguments)
				}
			}
		}

		if deps.Recorder != nil {
			if err := deps.Recorder.AppendAssistant(finalMsg.Assistant); err != nil {
				return history, fmt.Errorf("agent: record assistant message: %w", err)
			}
		}
		history = append(history, finalMsg)

		if finalMsg.Assistant.StopReason == "error" {
			if overflow != nil && overflow(finalMsg.Assistant.ErrorMessage) {
				return history, &ContextOverflowError{
					Assistant: finalMsg.Assistant,
					Err:       fmt.Errorf("%w: %s", ErrContextOverflow, finalMsg.Assistant.ErrorMessage),
				}
			}
			return history, fmt.Errorf("agent: provider error: %s", finalMsg.Assistant.ErrorMessage)
		}
		if finalMsg.Assistant.StopReason != "toolUse" {
			return history, nil
		}
		if len(calls) == 0 {
			return history, nil
		}
		turnIndex++

		var steerMsgs []*Message
		for i, call := range calls {
			res := Result{}
			if denied[i] {
				res = ErrorResult(denyReason[i])
			} else {
				var xerr error
				if res, xerr = revalidateAndExecute(ctx, deps, &call, finalMsg.Assistant, toolsByName); xerr != nil {
					return history, xerr
				}
			}

			if deps.Detector != nil {
				outcome := deps.Detector.Observe(observationTurn(finalMsg.Assistant, call, res, turnIndex, i == 0))
				switch outcome.Verdict {
				case VerdictWarn:
					if outcome.SteerText != "" {
						steerMsgs = append(steerMsgs, &Message{User: &UserMessage{
							Role:      string(RoleUser),
							Content:   mustMarshalString(outcome.SteerText),
							Timestamp: NowMillis(),
						}})
					}
				case VerdictBlock:
					res = ErrorResult("loop detected: " + loopFindingText(outcome))
					blocked, rerr := recordToolResult(deps, ctx, call, res)
					if rerr != nil {
						return history, rerr
					}
					history = append(history, &Message{ToolResult: blocked})
					return history, &LoopDetectedError{
						Outcome: outcome,
						Call:    call,
						Err:     fmt.Errorf("%w: %s", ErrLoopDetected, loopFindingText(outcome)),
					}
				}
			}

			msg, rerr := recordToolResult(deps, ctx, call, res)
			if rerr != nil {
				return history, rerr
			}
			history = append(history, &Message{ToolResult: msg})
		}

		for _, sm := range steerMsgs {
			if deps.Recorder != nil {
				if err := deps.Recorder.AppendUser(sm.User); err != nil {
					return history, fmt.Errorf("agent: record steering message: %w", err)
				}
			}
			history = append(history, sm)
		}
	}
}

func catalogTools(deps *LoopDeps) []Tool {
	if deps != nil && deps.Catalog != nil {
		return deps.Catalog.Tools()
	}
	if deps != nil {
		return deps.Tools
	}
	return nil
}

func retryCallbacks(deps *LoopDeps, ctx context.Context) *RetryCallbacks {
	return &RetryCallbacks{
		Scheduled: func(attempt, maxAttempts int, delayMs int64, errorMessage string) {
			if deps.Hooks != nil {
				deps.Hooks.AutoRetryStart(ctx, attempt, maxAttempts, delayMs, errorMessage)
			}
			if deps.OnRetryScheduled != nil {
				deps.OnRetryScheduled(attempt, maxAttempts, delayMs, errorMessage)
			}
		},
		Finished: func(success bool, attempt int, finalError string) {
			if deps.Hooks != nil {
				deps.Hooks.AutoRetryEnd(ctx, success, attempt, finalError)
			}
			if deps.OnRetryFinished != nil {
				deps.OnRetryFinished(success, attempt, finalError)
			}
		},
	}
}

func recordToolResult(deps *LoopDeps, ctx context.Context, call ContentBlock, res Result) (*ToolResultMessage, error) {
	if deps.Hooks != nil {
		var err error
		if res, err = deps.Hooks.ToolResult(ctx, call.Name, call.ID, call.Arguments, res); err != nil {
			return nil, fmt.Errorf("agent: tool_result hook: %w", err)
		}
	}
	msg := resultToToolResult(call, res)
	if deps.Recorder != nil {
		if err := deps.Recorder.AppendToolResult(msg); err != nil {
			return nil, fmt.Errorf("agent: record tool result: %w", err)
		}
	}
	return msg, nil
}

func observationTurn(asst *AssistantMessage, call ContentBlock, res Result, turnIndex int, withText bool) Turn {
	obs := Turn{
		TurnIndex: turnIndex,
		ToolCalls: []ToolCallObs{{
			ToolCallID: call.ID,
			Name:       call.Name,
			Arguments:  call.Arguments,
			Result:     resultToToolResult(call, res),
		}},
	}
	if withText {
		var thinking, text strings.Builder
		for _, b := range asst.Content {
			switch b.Type {
			case BlockTypeThinking:
				thinking.WriteString(b.Thinking)
			case BlockTypeText:
				text.WriteString(b.Text)
			}
		}
		obs.ThinkingText = strings.TrimSpace(thinking.String())
		obs.TextContent = strings.TrimSpace(text.String())
	}
	return obs
}

func loopFindingText(o Outcome) string {
	if len(o.Findings) > 0 {
		return o.Findings[0].Message
	}
	return ""
}

func mustMarshalString(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

func revalidateAndExecute(ctx context.Context, deps *LoopDeps, call *ContentBlock, asst *AssistantMessage, toolsByName map[string]Tool) (Result, error) {
	if deps.Hooks != nil {
		dec, derr := deps.Hooks.ToolCall(ctx, call.Name, call.ID, call.Arguments)
		if derr != nil {
			return Result{}, fmt.Errorf("agent: tool_call hook: %w", derr)
		}
		if dec.FinalArgs != nil {
			call.Arguments = dec.FinalArgs
			applyFinalArgs(asst, call.ID, call.Arguments)
		}
		if dec.Block {
			return ErrorResult("blocked on re-validation: " + dec.Reason), nil
		}
	}
	return executeCall(ctx, *call, toolsByName), nil
}

func executeCall(ctx context.Context, call ContentBlock, toolsByName map[string]Tool) Result {
	tool, ok := toolsByName[call.Name]
	if !ok {
		return ErrorResult(fmt.Sprintf("unknown tool %q", call.Name))
	}
	if !json.Valid(call.Arguments) {
		return ErrorResult(fmt.Sprintf("tool %q: invalid arguments: not valid JSON", call.Name))
	}
	return tool.Exec(ctx, call.Arguments)
}

func toolCallBlocks(content []ContentBlock) []ContentBlock {
	var out []ContentBlock
	for _, b := range content {
		if b.Type == BlockTypeToolCall {
			out = append(out, b)
		}
	}
	return out
}

func applyFinalArgs(asst *AssistantMessage, callID string, args json.RawMessage) {
	if asst == nil {
		return
	}
	for i := range asst.Content {
		b := &asst.Content[i]
		if b.Type == BlockTypeToolCall && b.ID == callID {
			b.Arguments = args
			return
		}
	}
}

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
