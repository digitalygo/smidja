package ui

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/tui/interactive"
)

type TurnScope struct {
	surface *interactive.Surface
	active  func() bool

	open         bool
	opened       bool
	attemptValid bool
	finalized    bool
}

func NewTurnScope(surface *interactive.Surface, active func() bool) *TurnScope {
	return &TurnScope{surface: surface, active: active}
}

func (s *TurnScope) Surface() *interactive.Surface { return s.surface }

func (s *TurnScope) live() bool {
	if s == nil || s.surface == nil {
		return false
	}
	return s.active == nil || s.active()
}

func (s *TurnScope) ensureAssistant() {
	if s == nil || s.open || !s.live() {
		return
	}
	s.surface.StartAssistantTurn()
	s.open = true
	s.opened = true
	s.attemptValid = true
}

func (s *TurnScope) appendText(delta string) {
	if delta == "" {
		return
	}
	s.ensureAssistant()
	if s.open {
		s.surface.AppendAssistantText(delta)
	}
}

func (s *TurnScope) appendThinking(delta string) {
	if delta == "" {
		return
	}
	s.ensureAssistant()
	if s.open {
		s.surface.AppendAssistantThinking(delta)
	}
}

func (s *TurnScope) reconcileMessage(parts []interactive.AssistantMessagePart, stopReason string) {
	if s == nil || !s.live() || s.finalized {
		return
	}
	switch {
	case s.open:
		if s.surface.ReconcileAssistantContent(parts) {
			s.attemptValid = true
		}
	case len(parts) > 0:
		s.surface.StartAssistantTurn()
		s.surface.ReconcileAssistantContent(parts)
		s.opened = true
		s.attemptValid = true
	}
	s.open = false
	if stopReason == "toolUse" {
		s.attemptValid = false
	}
}

func (s *TurnScope) toolBoundary() {
	if s == nil || s.finalized {
		return
	}
	s.open = false
	s.attemptValid = false
}

func (s *TurnScope) endAssistant(reason, message string) {
	if s == nil || !s.live() || s.finalized {
		return
	}
	s.finalized = true
	s.open = false
	if !s.attemptValid {
		if !shouldCreateFinalBlock(reason, s.opened) {
			return
		}
		s.surface.StartAssistantTurn()
		s.opened = true
		s.attemptValid = true
	}
	s.surface.EndAssistantTurn(reason, message)
}

func shouldCreateFinalBlock(stopReason string, opened bool) bool {
	switch stopReason {
	case "length":
		return true
	case "error", "aborted":
		return opened
	default:
		return false
	}
}

func AssistantParts(m *agent.Message) []interactive.AssistantMessagePart {
	return assistantParts(m)
}

func hasAuthoritativeContent(parts []interactive.AssistantMessagePart) bool {
	for _, part := range parts {
		if part.Text != "" {
			return true
		}
	}
	return false
}

func (s *TurnScope) FinalizeAuthoritative(hasAuthoritative bool, parts []interactive.AssistantMessagePart, stopReason, errorMessage string) {
	if s == nil || !s.live() || s.finalized {
		return
	}
	if hasAuthoritative {
		if stopReason == "toolUse" {
			if hasAuthoritativeContent(parts) {
				if s.surface.ReconcileAssistantContent(parts) {
					s.opened = true
				} else {
					s.surface.StartAssistantTurn()
					s.surface.ReconcileAssistantContent(parts)
					s.opened = true
				}
			}
			s.open = false
			s.attemptValid = false
		} else {
			switch {
			case s.open:
				if s.surface.ReconcileAssistantContent(parts) {
					s.attemptValid = true
				}
				s.open = false
			case s.attemptValid:
				if s.surface.ReconcileAssistantContent(parts) {
					s.attemptValid = true
				}
				s.open = false
			default:
				if hasAuthoritativeContent(parts) {
					s.surface.StartAssistantTurn()
					s.surface.ReconcileAssistantContent(parts)
					s.opened = true
					s.attemptValid = true
				}
				s.open = false
			}
		}
	}
	s.endAssistant(stopReason, errorMessage)
}

func (s *TurnScope) TextWriter() io.Writer {
	return &assistantTextWriter{scope: s}
}

func (s *TurnScope) ThinkingCallback() func(string) {
	return func(delta string) { s.appendThinking(delta) }
}

func (s *TurnScope) Finish(stopReason, errorMessage string) {
	s.endAssistant(stopReason, errorMessage)
}

func (s *TurnScope) Opened() bool { return s != nil && s.opened }

type assistantTextWriter struct {
	scope *TurnScope
}

func (w *assistantTextWriter) Write(p []byte) (int, error) {
	w.scope.appendText(string(p))
	return len(p), nil
}

type HookDecorator struct {
	next  agent.HookDispatcher
	scope *TurnScope

	mu    sync.Mutex
	tools map[string]*interactive.ToolExecution
}

var _ agent.HookDispatcher = (*HookDecorator)(nil)

func NewHookDecorator(next agent.HookDispatcher, scope *TurnScope) *HookDecorator {
	return &HookDecorator{next: next, scope: scope}
}

func (d *HookDecorator) surface() *interactive.Surface {
	if d == nil || d.scope == nil {
		return nil
	}
	return d.scope.Surface()
}

func (d *HookDecorator) live() bool {
	if d == nil || d.scope == nil {
		return false
	}
	return d.scope.live()
}

func (d *HookDecorator) Context(ctx context.Context, req agent.ContextRequest) (agent.ContextResult, error) {
	if d.next == nil {
		return agent.ContextResult{Messages: req.Messages, System: req.System}, nil
	}
	return d.next.Context(ctx, req)
}

func (d *HookDecorator) MessageEnd(ctx context.Context, m *agent.Message) (*agent.Message, error) {
	if d.next == nil {
		d.applyMessageEnd(m)
		return m, nil
	}
	replaced, err := d.next.MessageEnd(ctx, m)
	if err != nil {
		return replaced, err
	}
	effective := m
	if replaced != nil {
		effective = replaced
	}
	d.applyMessageEnd(effective)
	return replaced, nil
}

func (d *HookDecorator) applyMessageEnd(m *agent.Message) {
	if d == nil || d.scope == nil || m == nil || m.Assistant == nil {
		return
	}
	d.scope.reconcileMessage(assistantParts(m), m.Assistant.StopReason)
}

func assistantParts(m *agent.Message) []interactive.AssistantMessagePart {
	if m == nil || m.Assistant == nil {
		return nil
	}
	parts := make([]interactive.AssistantMessagePart, 0, len(m.Assistant.Content))
	for _, block := range m.Assistant.Content {
		switch block.Type {
		case agent.BlockTypeThinking:
			parts = append(parts, interactive.AssistantMessagePart{Thinking: true, Text: block.Thinking})
		case agent.BlockTypeText:
			parts = append(parts, interactive.AssistantMessagePart{Text: block.Text})
		}
	}
	return parts
}

func (d *HookDecorator) AutoRetryStart(ctx context.Context, attempt int, maxAttempts int, delayMs int64, errorMessage string) error {
	if d.next != nil {
		if err := d.next.AutoRetryStart(ctx, attempt, maxAttempts, delayMs, errorMessage); err != nil {
			return err
		}
	}
	if surface := d.surface(); surface != nil && d.live() {
		surface.SetRetry(attempt, maxAttempts, time.Duration(delayMs)*time.Millisecond)
	}
	return nil
}

func (d *HookDecorator) AutoRetryEnd(ctx context.Context, success bool, attempt int, finalError string) error {
	if d.next != nil {
		if err := d.next.AutoRetryEnd(ctx, success, attempt, finalError); err != nil {
			return err
		}
	}
	if surface := d.surface(); surface != nil && d.live() {
		surface.ClearRetry()
	}
	return nil
}

func (d *HookDecorator) ToolCall(ctx context.Context, name string, callID string, args json.RawMessage) (agent.ToolCallDecision, error) {
	decision := agent.ToolCallDecision{FinalArgs: args}
	if d.scope != nil {
		d.scope.toolBoundary()
	}
	if d.next != nil {
		forwarded, err := d.next.ToolCall(ctx, name, callID, args)
		if err != nil {
			return forwarded, err
		}
		decision = forwarded
	}
	shown := decision.FinalArgs
	if len(shown) == 0 {
		shown = args
	}
	d.mu.Lock()
	if d.tools == nil {
		d.tools = make(map[string]*interactive.ToolExecution)
	}
	if _, seen := d.tools[callID]; !seen {
		if surface := d.surface(); surface != nil && d.live() {
			d.tools[callID] = surface.AddToolExecution(name, shown)
		}
	}
	d.mu.Unlock()
	return decision, nil
}

func (d *HookDecorator) ToolResult(ctx context.Context, name string, callID string, args json.RawMessage, res agent.Result) (agent.Result, error) {
	out := res
	if d.scope != nil {
		d.scope.toolBoundary()
	}
	if d.next != nil {
		forwarded, err := d.next.ToolResult(ctx, name, callID, args, res)
		if err != nil {
			return forwarded, err
		}
		out = forwarded
	}
	status := interactive.ToolSuccess
	if out.IsError {
		status = interactive.ToolError
	}
	d.mu.Lock()
	if d.tools == nil {
		d.tools = make(map[string]*interactive.ToolExecution)
	}
	block := d.tools[callID]
	if block == nil {
		if surface := d.surface(); surface != nil && d.live() {
			block = surface.AddToolExecution(name, args)
			d.tools[callID] = block
		}
	}
	d.mu.Unlock()
	if block != nil {
		block.SetResult(toolResultText(out), status)
	}
	return out, nil
}

func (d *HookDecorator) SessionStart(ctx context.Context, reason string) error {
	if d.next == nil {
		return nil
	}
	return d.next.SessionStart(ctx, reason)
}

func (d *HookDecorator) SessionStartWithFiles(ctx context.Context, reason, previousPath string) error {
	if d.next == nil {
		return nil
	}
	type starter interface {
		SessionStartWithFiles(context.Context, string, string) error
	}
	if withFiles, ok := d.next.(starter); ok {
		return withFiles.SessionStartWithFiles(ctx, reason, previousPath)
	}
	return d.next.SessionStart(ctx, reason)
}

func (d *HookDecorator) SessionShutdown(ctx context.Context, reason string) error {
	if d.next == nil {
		return nil
	}
	return d.next.SessionShutdown(ctx, reason)
}

func (d *HookDecorator) SessionShutdownWithFiles(ctx context.Context, reason, targetPath string) error {
	if d.next == nil {
		return nil
	}
	type stopper interface {
		SessionShutdownWithFiles(context.Context, string, string) error
	}
	if withFiles, ok := d.next.(stopper); ok {
		return withFiles.SessionShutdownWithFiles(ctx, reason, targetPath)
	}
	return d.next.SessionShutdown(ctx, reason)
}

func toolResultText(res agent.Result) string {
	var builder strings.Builder
	for _, block := range res.Content {
		if block.Type == agent.BlockTypeText {
			builder.WriteString(block.Text)
		}
	}
	return builder.String()
}
