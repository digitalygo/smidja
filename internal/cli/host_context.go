package cli

import (
	"context"
	"encoding/json"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/models"
	"github.com/digitalygo/smidja/sdk"
)

type hostTapRecorder struct {
	agent.Recorder
	host *hostRuntime
}

func (r *hostTapRecorder) AppendUser(m *agent.UserMessage) error {
	if r.Recorder == nil {
		return nil
	}
	if err := r.Recorder.AppendUser(m); err != nil {
		return err
	}
	r.host.appendMessage(&agent.Message{User: m})
	return nil
}

func (r *hostTapRecorder) AppendAssistant(m *agent.AssistantMessage) error {
	if r.Recorder == nil {
		return nil
	}
	if err := r.Recorder.AppendAssistant(m); err != nil {
		return err
	}
	r.host.appendMessage(&agent.Message{Assistant: m})
	return nil
}

func (r *hostTapRecorder) AppendToolResult(m *agent.ToolResultMessage) error {
	if r.Recorder == nil {
		return nil
	}
	if err := r.Recorder.AppendToolResult(m); err != nil {
		return err
	}
	r.host.appendMessage(&agent.Message{ToolResult: m})
	return nil
}

func hostRecorder(d *runDeps, recorder agent.Recorder) agent.Recorder {
	if d == nil || d.host == nil || recorder == nil {
		return recorder
	}
	return &hostTapRecorder{Recorder: recorder, host: d.host}
}

func projectMessages(messages []*agent.Message) []sdk.Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]sdk.Message, 0, len(messages))
	for _, message := range messages {
		out = append(out, extensions.MessageToSDK(message))
	}
	return out
}

func (h *hostRuntime) projectedMessages() []sdk.Message {
	h.mu.Lock()
	messages := h.messages
	h.mu.Unlock()
	return projectMessages(messages)
}

func (h *hostRuntime) modelRegistry() sdk.ModelRegistry {
	h.mu.Lock()
	reg := h.modelReg
	modelID := h.modelID
	wireModel := h.wireModel
	provider := h.provider
	h.mu.Unlock()
	if reg == nil {
		return nil
	}
	return &hostModelRegistry{reg: reg, modelID: modelID, wireModel: wireModel, provider: provider}
}

func sdkModel(modelID, wireModel, provider string) *sdk.Model {
	if modelID == "" || wireModel == "" {
		return nil
	}
	model := sdk.Model{ID: modelID, Name: modelID, Provider: provider}
	if wireModel != modelID {
		model.Name = wireModel
	}
	return &model
}

func (h *hostRuntime) currentModel() *sdk.Model {
	h.mu.Lock()
	modelID := h.modelID
	wireModel := h.wireModel
	provider := h.provider
	h.mu.Unlock()
	return sdkModel(modelID, wireModel, provider)
}

func contextUsageOf(messages []*agent.Message, window int64) *sdk.ContextUsage {
	usage := &sdk.ContextUsage{ContextWindow: window}
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if message == nil || message.Assistant == nil {
			continue
		}
		tokens := message.Assistant.Usage.Input
		if tokens <= 0 {
			continue
		}
		percent := 0.0
		if window > 0 {
			percent = float64(tokens) / float64(window) * 100
		}
		usage.Tokens = &tokens
		usage.Percent = &percent
		return usage
	}
	return usage
}

func (h *hostRuntime) contextUsage() *sdk.ContextUsage {
	h.mu.Lock()
	messages := h.messages
	window := h.window
	h.mu.Unlock()
	return contextUsageOf(messages, window)
}

func (h *hostRuntime) systemPrompt() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.system
}

type hostContextState struct {
	handle   *hostSessionHandle
	messages []sdk.Message
	modelReg sdk.ModelRegistry
	model    *sdk.Model
	system   string
	usage    *sdk.ContextUsage
}

func (h *hostRuntime) captureContextState() *hostContextState {
	h.mu.Lock()
	defer h.mu.Unlock()
	state := &hostContextState{
		handle:   copyHostHandle(h.handle),
		messages: projectMessages(h.messages),
		system:   h.system,
		usage:    contextUsageOf(h.messages, h.window),
	}
	state.model = sdkModel(h.modelID, h.wireModel, h.provider)
	if h.modelReg != nil {
		state.modelReg = &hostModelRegistry{reg: h.modelReg, modelID: h.modelID, wireModel: h.wireModel, provider: h.provider}
	}
	return state
}

type hostSessionView struct {
	handle   *hostSessionHandle
	messages []sdk.Message
}

var _ sdk.SessionView = (*hostSessionView)(nil)

func (v *hostSessionView) ID() string   { return v.handle.id }
func (v *hostSessionView) Path() string { return v.handle.path }
func (v *hostSessionView) Cwd() string  { return v.handle.cwd }
func (v *hostSessionView) Name() string { return v.handle.name }

func (v *hostSessionView) Messages() []sdk.Message {
	out := make([]sdk.Message, len(v.messages))
	for i, message := range v.messages {
		out[i] = cloneSDKMessage(message)
	}
	return out
}

func cloneSDKMessage(message sdk.Message) sdk.Message {
	out := sdk.Message{Role: message.Role}
	if message.Content != nil {
		out.Content = make([]sdk.Block, len(message.Content))
		for i, block := range message.Content {
			out.Content[i] = sdk.Block{
				Type:      block.Type,
				Text:      block.Text,
				Thinking:  block.Thinking,
				ID:        block.ID,
				Name:      block.Name,
				Arguments: cloneSDKRaw(block.Arguments),
			}
		}
	}
	if message.Usage != nil {
		usage := *message.Usage
		out.Usage = &usage
	}
	return out
}

func cloneSDKRaw(raw json.RawMessage) json.RawMessage {
	if raw == nil {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

type hostModelRegistry struct {
	reg       *models.Registry
	modelID   string
	wireModel string
	provider  string
}

var _ sdk.ModelRegistry = (*hostModelRegistry)(nil)

func (r *hostModelRegistry) Model() *sdk.Model {
	return sdkModel(r.modelID, r.wireModel, r.provider)
}

func (r *hostModelRegistry) Available() []sdk.Model {
	seen := map[string]struct{}{}
	out := make([]sdk.Model, 0, 16)
	for _, key := range r.reg.Keys() {
		info, ok := r.reg.GetByKey(key)
		if !ok || info.ID == "" {
			continue
		}
		if _, dup := seen[info.ID]; dup {
			continue
		}
		seen[info.ID] = struct{}{}
		out = append(out, sdk.Model{ID: info.ID, Name: info.ID, Provider: info.Provider})
	}
	return out
}

func (r *hostModelRegistry) Find(provider, id string) (sdk.Model, bool) {
	if id == "" {
		return sdk.Model{}, false
	}
	var info models.ModelInfo
	var ok bool
	if provider == "" {
		info, ok = r.reg.Get(id)
	} else {
		info, ok = r.reg.Lookup(provider, id)
	}
	if !ok {
		return sdk.Model{}, false
	}
	return sdk.Model{ID: info.ID, Name: info.ID, Provider: info.Provider}, true
}

type hostHandlerContext struct {
	sdk.API
	host   *hostRuntime
	state  *hostContextState
	signal context.Context
}

var _ sdk.HandlerContext = (*hostHandlerContext)(nil)

func (c *hostHandlerContext) WithSignal(signal context.Context) sdk.HandlerContext {
	clone := *c
	clone.signal = signal
	clone.state = c.host.captureContextState()
	return &clone
}

func (c *hostHandlerContext) UI() sdk.UI              { return extensions.NoopUI }
func (c *hostHandlerContext) Mode() sdk.Mode          { return sdk.ModePrint }
func (c *hostHandlerContext) HasUI() bool             { return false }
func (c *hostHandlerContext) Signal() context.Context { return c.signal }

func (c *hostHandlerContext) Cwd() string {
	if c.state == nil || c.state.handle == nil {
		return ""
	}
	return c.state.handle.cwd
}

func (c *hostHandlerContext) SessionManager() sdk.SessionView {
	if c.state == nil || c.state.handle == nil {
		return nil
	}
	return &hostSessionView{handle: c.state.handle, messages: c.state.messages}
}

func (c *hostHandlerContext) ModelRegistry() sdk.ModelRegistry {
	if c.state == nil {
		return nil
	}
	return c.state.modelReg
}

func (c *hostHandlerContext) Model() *sdk.Model {
	if c.state == nil || c.state.model == nil {
		return nil
	}
	model := *c.state.model
	return &model
}

func (c *hostHandlerContext) ThinkingLevel() sdk.ThinkingLevel { return sdk.ThinkingOff }

func (c *hostHandlerContext) Abort() { c.host.abort(c.signal) }

func (c *hostHandlerContext) Shutdown() { c.host.shutdown() }

func (c *hostHandlerContext) ContextUsage() *sdk.ContextUsage {
	if c.state == nil || c.state.usage == nil {
		return nil
	}
	usage := *c.state.usage
	if c.state.usage.Tokens != nil {
		tokens := *c.state.usage.Tokens
		usage.Tokens = &tokens
	}
	if c.state.usage.Percent != nil {
		percent := *c.state.usage.Percent
		usage.Percent = &percent
	}
	return &usage
}

func (c *hostHandlerContext) Compact(opts sdk.CompactOptions) { c.host.requestCompact(c.signal, opts) }

func (c *hostHandlerContext) SystemPrompt() string {
	if c.state == nil {
		return ""
	}
	return c.state.system
}

func (c *hostHandlerContext) AppendEntry(customType string, data any) error {
	if c.state == nil {
		return errHostClosed
	}
	return c.host.appendEntry(c.state.handle, customType, data)
}

func (c *hostHandlerContext) SetSessionName(name string) error {
	if c.state == nil {
		return errHostClosed
	}
	return c.host.setSessionName(c.state.handle, name)
}

func (c *hostHandlerContext) LabelEntry(entryID, label string) error {
	if c.state == nil {
		return errHostClosed
	}
	return c.host.labelEntry(c.state.handle, entryID, label)
}

func (c *hostHandlerContext) SendMessage(msg sdk.CustomMessage, opts sdk.SendOptions) error {
	if c.state == nil {
		return errHostClosed
	}
	return c.host.sendMessage(c.state.handle, c.signal, msg, opts)
}

func (c *hostHandlerContext) SendUserMessage(text string, opts sdk.SendOptions) error {
	if c.state == nil {
		return errHostClosed
	}
	return c.host.sendUserMessage(c.state.handle, c.signal, text, opts)
}

func (c *hostHandlerContext) Exec(command string, args []string, opts sdk.ExecOptions) (*sdk.ExecResult, error) {
	return c.host.exec(c.signal, command, args, opts)
}
