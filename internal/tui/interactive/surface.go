package interactive

import (
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/digitalygo/smidja/internal/tui"
)

type SurfaceOptions struct {
	Theme            *tui.Theme
	Editor           *tui.Editor
	Keybindings      *tui.KeybindingsManager
	Clock            Clock
	Hyperlinks       bool
	Home             string
	Scrollbar        tui.ScrollbarMode
	SpinnerIndicator *tui.LoaderIndicator
	Controller       tui.TUIController
	OnEditorChange   func(string)
	OnSubmit         func(string)
}

type collapsible interface {
	SetExpanded(expanded bool)
	IsExpanded() bool
}

type runningCollapsible interface {
	collapsible
	Running() bool
	Cancel() bool
}

type ownedRoot struct {
	surface *Surface
}

func (o *ownedRoot) Render(width int) []string {
	var lines []string
	o.surface.runtime.Run(func() {
		lines = o.surface.root.Render(width)
	})
	return lines
}

func (o *ownedRoot) RenderDocument(width int) []string {
	return o.surface.RenderDocument(width)
}

func (o *ownedRoot) StackLayout() tui.StackLayoutSpec {
	return o.surface.root.StackLayout()
}

func (o *ownedRoot) RenderLayoutFrame(width, height int, requestRender func()) *tui.LayoutFrame {
	var frame *tui.LayoutFrame
	o.surface.runtime.Run(func() {
		frame = tui.RenderLayoutFrame(o.surface.root, width, height, requestRender)
	})
	return frame
}

func (o *ownedRoot) Invalidate() {
	o.surface.runtime.Run(func() {
		o.surface.root.Invalidate()
	})
}

type Surface struct {
	runtime     *tui.Runtime
	theme       *tui.Theme
	keybindings *tui.KeybindingsManager
	clock       Clock
	hyperlinks  bool
	editor      *tui.Editor
	root        *tui.Stack
	owned       *ownedRoot

	stateMu    sync.RWMutex
	controller tui.TUIController
	onSubmit   func(string)
	onChange   func(string)
	closed     atomic.Bool

	chat         *tui.Container
	transcript   *tui.ScrollView
	pending      *tui.Container
	statusRegion *tui.Container
	widgets      *WidgetPanel
	footer       *Footer
	status       *StatusIndicator
	spinner      *tui.LoaderIndicator

	assistants       []*AssistantMessage
	collapsibles     []collapsible
	thinkingExpanded bool
	toolsExpanded    bool
	expandKey        string
	thinkingKey      string
}

func NewSurface(options SurfaceOptions) *Surface {
	theme := options.Theme
	if theme == nil {
		registry := tui.NewThemeRegistry("", "", tui.ColorModeUnset)
		if loaded, err := registry.SetTheme("dark"); err == nil {
			theme = loaded
		} else {
			theme = registry.Active()
		}
	}
	keybindings := options.Keybindings
	if keybindings == nil {
		keybindings = tui.NewDefaultKeybindingsManager(nil)
	}
	clock := options.Clock
	if clock == nil {
		clock = SystemClock{}
	}
	editor := options.Editor
	if editor == nil {
		editor = tui.NewEditor(tui.EditorOptions{Theme: theme, TerminalRows: 24})
	}
	surface := &Surface{
		runtime:      tui.NewRuntime(clock),
		theme:        theme,
		keybindings:  keybindings,
		clock:        clock,
		hyperlinks:   options.Hyperlinks,
		controller:   options.Controller,
		onChange:     options.OnEditorChange,
		onSubmit:     options.OnSubmit,
		editor:       editor,
		chat:         &tui.Container{},
		pending:      &tui.Container{},
		statusRegion: &tui.Container{},
		widgets:      NewWidgetPanel(),
		footer:       NewFooter(theme, keybindings, options.Home),
		spinner:      options.SpinnerIndicator,
		expandKey:    keyDisplayFor(keybindings, "app.tools.expand"),
		thinkingKey:  keyDisplayFor(keybindings, "app.thinking.toggle"),
	}
	surface.status = NewStatusIndicator(clock, surface.runtime, options.Controller, options.SpinnerIndicator, keybindings, theme)
	surface.owned = &ownedRoot{surface: surface}
	surface.statusRegion.AddChild(surface.status)
	surface.buildLayout(options)
	surface.footer.SetQueuedSource(editor.QueuedCount)
	editor.SetOnChange(surface.handleEditorChange)
	editor.SetOnSubmit(surface.handleEditorSubmit)
	return surface
}

func keyDisplayFor(keybindings *tui.KeybindingsManager, action string) string {
	return SanitizeSingleLine(strings.Join(keybindings.Keys(action), "/"))
}

func (s *Surface) buildLayout(options SurfaceOptions) {
	scrollbar := options.Scrollbar
	if scrollbar == tui.ScrollbarHidden {
		scrollbar = tui.ScrollbarAuto
	}
	s.transcript = tui.NewScrollView(s.chat, tui.ScrollViewOptions{
		Follow:              "end",
		Primary:             true,
		Overscroll:          "chain",
		Scrollbar:           scrollbar,
		ScrollbarTrackStyle: func(text string) string { return s.theme.Fg("scrollbarTrack", text) },
		ScrollbarThumbStyle: func(text string) string { return s.theme.Fg("scrollbarThumb", text) },
	})
	editorContainer := &tui.Container{}
	editorContainer.AddChild(s.editor)
	dock := tui.NewVStack(0, tui.AlignStretch)
	dock.AddChildWithOptions(editorContainer, tui.StackEntryOptions{Shrink: 1, MinSize: 3})
	dock.AddChildWithOptions(s.pending, tui.StackEntryOptions{Shrink: 1})
	dock.AddChildWithOptions(s.statusRegion, tui.StackEntryOptions{Shrink: 1})
	dock.AddChildWithOptions(s.widgets, tui.StackEntryOptions{Shrink: 1})
	dock.AddChildWithOptions(s.footer, tui.StackEntryOptions{Shrink: 1})
	s.root = tui.NewVStack(0, tui.AlignStretch)
	zero := 0
	s.root.AddChildWithOptions(s.transcript, tui.StackEntryOptions{Basis: &zero, Grow: 1, Shrink: 1, MinSize: 1})
	s.root.AddChildWithOptions(dock, tui.StackEntryOptions{Shrink: 1, MinSize: 1})
}

func (s *Surface) Root() tui.Component         { return s.owned }
func (s *Surface) Transcript() *tui.ScrollView { return s.transcript }
func (s *Surface) Editor() *tui.Editor         { return s.editor }
func (s *Surface) Footer() *Footer             { return s.footer }
func (s *Surface) Status() *StatusIndicator    { return s.status }
func (s *Surface) WidgetPanel() *WidgetPanel   { return s.widgets }
func (s *Surface) Theme() *tui.Theme {
	var theme *tui.Theme
	s.runtime.Run(func() { theme = s.theme })
	return theme
}

func (s *Surface) SetTheme(theme *tui.Theme) {
	if theme == nil {
		return
	}
	s.runtime.Run(func() {
		s.theme = theme
		for _, child := range s.chat.Children() {
			applyComponentTheme(child, theme)
		}
		s.editor.SetTheme(theme)
		s.footer.SetTheme(theme)
		s.status.SetTheme(theme)
		s.widgets.SetTheme(theme)
	})
	s.invalidateChat()
	s.requestRender()
}

func (s *Surface) SetController(controller tui.TUIController) {
	s.stateMu.Lock()
	s.controller = controller
	s.stateMu.Unlock()
	s.status.SetController(controller)
}

func (s *Surface) SetOnSubmit(fn func(string)) {
	s.stateMu.Lock()
	s.onSubmit = fn
	s.stateMu.Unlock()
}

func (s *Surface) Submit() func(string) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.onSubmit
}

func (s *Surface) RequestRender() {
	s.requestRender()
}

func (s *Surface) RenderDocument(width int) []string {
	var lines []string
	s.runtime.Run(func() {
		lines = append(lines, s.chat.Render(width)...)
		lines = append(lines, s.editor.Render(width)...)
		lines = append(lines, s.pending.Render(width)...)
		lines = append(lines, s.statusRegion.Render(width)...)
		lines = append(lines, s.widgets.Render(width)...)
		lines = append(lines, s.footer.Render(width)...)
	})
	return lines
}

func (s *Surface) RenderFrame(width, height int) *tui.LayoutFrame {
	return s.owned.RenderLayoutFrame(width, height, s.requestRender)
}

func (s *Surface) requestRender() {
	if s.closed.Load() {
		return
	}
	s.stateMu.RLock()
	controller := s.controller
	s.stateMu.RUnlock()
	if controller != nil {
		controller.RequestRender(false)
	}
}

func (s *Surface) invalidateChat() {
	s.runtime.Run(func() {
		s.chat.Invalidate()
		s.requestRender()
	})
}

func (s *Surface) handleEditorChange(text string) {
	s.refreshPending()
	s.stateMu.RLock()
	callback := s.onChange
	s.stateMu.RUnlock()
	if callback != nil {
		callback(text)
	}
}

func (s *Surface) handleEditorSubmit(text string) {
	s.stateMu.RLock()
	callback := s.onSubmit
	s.stateMu.RUnlock()
	if callback != nil {
		callback(text)
	}
}

func (s *Surface) refreshPending() {
	s.runtime.Run(func() {
		s.pending.Clear()
		messages := s.editor.QueuedMessages()
		if len(messages) > 0 {
			s.pending.AddChild(tui.NewSpacer(1))
			dequeueKey := keyDisplayFor(s.keybindings, "app.message.dequeue")
			for _, message := range messages {
				s.pending.AddChild(tui.NewTruncatedText(s.theme.Fg("dim", "Follow-up: "+SanitizeSingleLine(message)), 1, 0))
			}
			s.pending.AddChild(tui.NewTruncatedText(s.theme.Fg("dim", "↳ "+dequeueKey+" to edit all queued messages"), 1, 0))
		}
	})
	s.requestRender()
}

func (s *Surface) AddUserMessage(text string) *UserMessage {
	var block *UserMessage
	s.runtime.Run(func() {
		block = NewUserMessage(text, s.theme, s.hyperlinks)
		s.chat.AddChild(block)
	})
	s.invalidateChat()
	return block
}

func (s *Surface) StartAssistantTurn() *AssistantMessage {
	var assistant *AssistantMessage
	s.runtime.Run(func() {
		assistant = NewAssistantMessage(s.theme, s.hyperlinks)
		assistant.SetThinkingLevel(s.footer.ThinkingLevel())
		assistant.SetThinkingExpanded(s.thinkingExpanded)
		assistant.SetThinkingKeyDisplay(s.thinkingKey)
		s.assistants = append(s.assistants, assistant)
		s.chat.AddChild(assistant)
	})
	s.invalidateChat()
	return assistant
}

func (s *Surface) currentAssistant() *AssistantMessage {
	var assistant *AssistantMessage
	s.runtime.Run(func() {
		if len(s.assistants) > 0 {
			assistant = s.assistants[len(s.assistants)-1]
		}
	})
	return assistant
}

func (s *Surface) AppendAssistantText(delta string) {
	assistant := s.currentAssistant()
	if assistant == nil {
		return
	}
	assistant.AppendText(delta)
	s.invalidateChat()
}

func (s *Surface) AppendAssistantThinking(delta string) {
	assistant := s.currentAssistant()
	if assistant == nil {
		return
	}
	assistant.AppendThinking(delta)
	s.invalidateChat()
}

func (s *Surface) ReconcileAssistantContent(parts []AssistantMessagePart) bool {
	assistant := s.currentAssistant()
	if assistant == nil {
		return false
	}
	assistant.ReconcileContent(parts)
	s.invalidateChat()
	return true
}

func (s *Surface) EndAssistantTurn(stopReason, errorMessage string) {
	assistant := s.currentAssistant()
	if assistant == nil {
		return
	}
	assistant.SetStopReason(stopReason, errorMessage)
	s.invalidateChat()
}

func (s *Surface) AddToolExecution(name string, args json.RawMessage) *ToolExecution {
	var block *ToolExecution
	s.runtime.Run(func() {
		block = NewToolExecution(name, args, s.theme, s.expandKey, s.requestRender)
		block.SetExpanded(s.toolsExpanded)
		s.collapsibles = append(s.collapsibles, block)
		s.chat.AddChild(block)
	})
	s.invalidateChat()
	return block
}

func (s *Surface) AddBashExecution(command string) *BashExecution {
	return s.AddBashExecutionWithCancel(command, nil)
}

func (s *Surface) AddBashExecutionWithCancel(command string, onCancel func()) *BashExecution {
	var block *BashExecution
	s.runtime.Run(func() {
		cancelKey := keyDisplayFor(s.keybindings, "app.interrupt")
		block = NewBashExecution(command, s.theme, s.expandKey, cancelKey, s.spinner, s.runtime, s.requestRender)
		block.SetOnCancel(onCancel)
		block.SetExpanded(s.toolsExpanded)
		s.collapsibles = append(s.collapsibles, block)
		s.chat.AddChild(block)
	})
	s.invalidateChat()
	return block
}

func (s *Surface) AddSubagent(name string) *SubagentBlock {
	var block *SubagentBlock
	s.runtime.Run(func() {
		block = NewSubagentBlock(name, s.theme, s.expandKey, s.requestRender)
		block.SetExpanded(s.toolsExpanded)
		s.collapsibles = append(s.collapsibles, block)
		s.chat.AddChild(block)
	})
	s.invalidateChat()
	return block
}

func (s *Surface) AddSkillInvocation(name, content string) *SkillBlock {
	var block *SkillBlock
	s.runtime.Run(func() {
		block = NewSkillBlock(name, content, s.theme, s.hyperlinks, s.expandKey)
		block.SetExpanded(s.toolsExpanded)
		s.collapsibles = append(s.collapsibles, block)
		s.chat.AddChild(block)
	})
	s.invalidateChat()
	return block
}

func (s *Surface) AddCompactionSummary(summary string, tokensBefore int64) *CompactionBlock {
	var block *CompactionBlock
	s.runtime.Run(func() {
		block = NewCompactionBlock(summary, tokensBefore, s.theme, s.hyperlinks, s.expandKey)
		block.SetExpanded(s.toolsExpanded)
		s.collapsibles = append(s.collapsibles, block)
		s.chat.AddChild(block)
	})
	s.invalidateChat()
	return block
}

func (s *Surface) AddNotice(kind NoticeKind, text string) {
	s.runtime.Run(func() {
		s.chat.AddChild(NewNotice(kind, text, s.theme))
	})
	s.invalidateChat()
}

func (s *Surface) SetWorking(working bool) {
	s.runtime.Run(func() {
		s.status.SetWorking(working)
	})
	s.requestRender()
}

func (s *Surface) SetWorkingMessage(message string) {
	s.runtime.Run(func() {
		s.status.SetWorkingMessage(message)
	})
	s.requestRender()
}

func (s *Surface) SetRetry(attempt, maxAttempts int, delay time.Duration) {
	s.runtime.Run(func() {
		s.status.SetRetry(attempt, maxAttempts, delay)
	})
	s.requestRender()
}

func (s *Surface) ClearRetry() {
	s.runtime.Run(func() {
		s.status.ClearRetry()
	})
	s.requestRender()
}

func (s *Surface) SetCompacting(active bool) {
	s.runtime.Run(func() {
		s.status.SetCompacting(active)
	})
	s.requestRender()
}

func (s *Surface) SetModel(model string) {
	s.runtime.Run(func() {
		s.footer.SetModel(model)
	})
	s.requestRender()
}

func (s *Surface) SetThinkingLevel(level string) {
	s.runtime.Run(func() {
		s.footer.SetThinkingLevel(level)
		s.editor.SetThinkingLevel(level)
		for _, assistant := range s.assistants {
			assistant.SetThinkingLevel(level)
		}
	})
	s.requestRender()
}

func (s *Surface) SetWorkspace(workspace string) {
	s.runtime.Run(func() {
		s.footer.SetWorkspace(workspace)
	})
	s.requestRender()
}

func (s *Surface) SetSessionName(name string) {
	s.runtime.Run(func() {
		s.footer.SetSessionName(name)
	})
	s.requestRender()
}

func (s *Surface) SetUsage(usage UsageSummary) {
	s.runtime.Run(func() {
		s.footer.SetUsage(usage)
	})
	s.requestRender()
}

func (s *Surface) SetStatus(key, text string) {
	s.runtime.Run(func() {
		s.footer.SetStatus(key, text)
	})
	s.requestRender()
}

func (s *Surface) ClearStatus(key string) {
	s.runtime.Run(func() {
		s.footer.ClearStatus(key)
	})
	s.requestRender()
}

func (s *Surface) SetWidget(key string, content []string) {
	s.runtime.Run(func() {
		s.widgets.SetWidget(key, content)
	})
	s.requestRender()
}

func (s *Surface) ClearWidget(key string) {
	s.runtime.Run(func() {
		s.widgets.ClearWidget(key)
	})
	s.requestRender()
}

func (s *Surface) ToggleToolExpansion() bool {
	s.runtime.Run(func() {
		s.toolsExpanded = !s.toolsExpanded
		for _, block := range s.collapsibles {
			block.SetExpanded(s.toolsExpanded)
		}
	})
	s.invalidateChat()
	return true
}

func (s *Surface) SetToolsExpanded(expanded bool) {
	s.runtime.Run(func() {
		s.toolsExpanded = expanded
		for _, block := range s.collapsibles {
			block.SetExpanded(expanded)
		}
	})
	s.invalidateChat()
}

func (s *Surface) ToolsExpanded() bool {
	var expanded bool
	s.runtime.Run(func() { expanded = s.toolsExpanded })
	return expanded
}

func (s *Surface) SetThinkingExpanded(expanded bool) {
	s.runtime.Run(func() {
		s.thinkingExpanded = expanded
		for _, assistant := range s.assistants {
			assistant.SetThinkingExpanded(expanded)
		}
	})
	s.invalidateChat()
}

func (s *Surface) ThinkingExpanded() bool {
	var expanded bool
	s.runtime.Run(func() { expanded = s.thinkingExpanded })
	return expanded
}

func (s *Surface) ToggleThinking() bool {
	s.runtime.Run(func() {
		s.thinkingExpanded = !s.thinkingExpanded
		for _, assistant := range s.assistants {
			assistant.SetThinkingExpanded(s.thinkingExpanded)
		}
	})
	s.invalidateChat()
	return true
}

func (s *Surface) CancelBash() bool {
	var target runningCollapsible
	s.runtime.Run(func() {
		for index := len(s.collapsibles) - 1; index >= 0; index-- {
			candidate, ok := s.collapsibles[index].(runningCollapsible)
			if !ok || !candidate.Running() {
				continue
			}
			target = candidate
			break
		}
	})
	if target == nil {
		return false
	}
	return target.Cancel()
}

func (s *Surface) Close() {
	if s.closed.Swap(true) {
		return
	}
	s.runtime.Run(func() {
		s.status.StopAnimation()
	})
	s.runtime.Stop()
}

func (s *Surface) Closed() bool { return s.closed.Load() }

func (s *Surface) HandleActionKey(data string) bool {
	if s.keybindings.Matches(data, "app.tools.expand") {
		s.ToggleToolExpansion()
		return true
	}
	if s.keybindings.Matches(data, "app.thinking.toggle") {
		s.ToggleThinking()
		return true
	}
	if s.keybindings.Matches(data, "app.interrupt") {
		return s.CancelBash()
	}
	return false
}
