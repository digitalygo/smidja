package interactive

import (
	"encoding/json"

	"github.com/digitalygo/smidja/internal/tui"
)

const (
	ReplayUser       = "user"
	ReplayAssistant  = "assistant"
	ReplayTool       = "tool"
	ReplayCompaction = "compaction"
	ReplayCustom     = "custom"
	ReplayNotice     = "notice"
)

const (
	CustomKindMessage = "message"
	CustomKindEntry   = "entry"
)

type TranscriptEntry struct {
	Kind         string
	Text         string
	Parts        []AssistantMessagePart
	StopReason   string
	ErrorMessage string
	ToolName     string
	ToolArgs     json.RawMessage
	ToolOutput   string
	ToolError    bool
	Pending      bool
	Summary      string
	TokensBefore int64
	CustomType   string
	CustomLabel  string
	CustomKind   string
	CustomData   any
	Timestamp    string
}

func (s *Surface) ReplaceTranscript(entries []TranscriptEntry) {
	prepared := make([]tui.Component, len(entries))
	for index, entry := range entries {
		if entry.Kind != ReplayCustom {
			continue
		}
		customKind := entry.CustomKind
		if customKind == "" {
			customKind = CustomKindEntry
		}
		view := CustomEntryView{CustomType: entry.CustomType, Text: entry.Text, Data: entry.CustomData, Label: entry.CustomLabel}
		prepared[index] = s.prepareCustomComponent(customKind, view)
	}
	var detachedSlots []*customRenderSlot
	s.runtime.Run(func() {
		for _, child := range s.chat.Children() {
			s.ext.untrackPrepared(child)
		}
		detachedSlots = s.ext.takeCustomSlots()
		s.chat.Clear()
		s.pending.Clear()
		s.assistants = nil
		s.collapsibles = nil
		s.status.Reset()
		s.footer.SetUsage(UsageSummary{})
		s.refreshPendingLocked()
		for index, entry := range entries {
			s.replayEntryLocked(entry, prepared[index])
		}
		if s.transcript != nil {
			s.transcript.ScrollToEnd()
		}
	})
	for _, slot := range detachedSlots {
		slot.Dispose()
	}
	s.invalidateChat()
	s.stateMu.RLock()
	controller := s.controller
	s.stateMu.RUnlock()
	if closer, ok := controller.(interface{ CloseSearch() }); ok {
		closer.CloseSearch()
	}
}

func (s *Surface) replayEntryLocked(entry TranscriptEntry, customComponent tui.Component) {
	switch entry.Kind {
	case ReplayUser:
		block := NewUserMessage(entry.Text, s.theme, s.hyperlinks)
		s.applyUserTransformer(block)
		s.chat.AddChild(block)
		s.ext.trackPrepared(block)
	case ReplayAssistant:
		assistant := NewAssistantMessage(s.theme, s.hyperlinks)
		assistant.SetThinkingLevel(s.footer.ThinkingLevel())
		assistant.SetThinkingExpanded(s.thinkingExpanded)
		assistant.SetThinkingKeyDisplay(s.thinkingKey)
		s.applyAssistantTransformer(assistant)
		assistant.ReconcileContent(entry.Parts)
		if entry.StopReason != "" || entry.ErrorMessage != "" {
			assistant.SetStopReason(entry.StopReason, entry.ErrorMessage)
		}
		s.assistants = append(s.assistants, assistant)
		s.chat.AddChild(assistant)
		s.ext.trackPrepared(assistant)
	case ReplayTool:
		block := NewToolExecution(entry.ToolName, entry.ToolArgs, s.theme, s.expandKey, s.requestRender)
		block.SetExpanded(s.toolsExpanded)
		status := ToolSuccess
		switch {
		case entry.Pending:
			status = ToolPending
		case entry.ToolError:
			status = ToolError
		}
		block.SetResult(entry.ToolOutput, status)
		s.collapsibles = append(s.collapsibles, block)
		s.chat.AddChild(block)
	case ReplayCompaction:
		block := NewCompactionBlock(entry.Summary, entry.TokensBefore, s.theme, s.hyperlinks, s.expandKey)
		block.SetExpanded(s.toolsExpanded)
		s.collapsibles = append(s.collapsibles, block)
		s.chat.AddChild(block)
	case ReplayCustom:
		view := CustomEntryView{CustomType: entry.CustomType, Text: entry.Text, Data: entry.CustomData, Label: entry.CustomLabel}
		kind := entry.CustomKind
		if kind == "" {
			kind = CustomKindEntry
		}
		slot := newCustomRenderSlot(kind, view, customComponent)
		if customComponent == nil {
			text := entry.Text
			if kind == CustomKindEntry {
				text = fallbackCustomText(view)
			}
			block := s.newCustomBlock(view, text)
			block.SetExpanded(s.toolsExpanded)
			slot.swap(block)
			s.collapsibles = append(s.collapsibles, block)
		}
		s.chat.AddChild(slot)
		s.ext.addCustomSlot(slot)
		s.ext.trackPrepared(slot)
	default:
		s.chat.AddChild(NewNotice(NoticeInfo, entry.Text, s.theme))
	}
}

func (s *Surface) refreshPendingLocked() {
	messages := s.editor.QueuedMessages()
	if len(messages) == 0 {
		return
	}
	dequeueKey := keyDisplayFor(s.keybindings, "app.message.dequeue")
	s.pending.AddChild(tui.NewSpacer(1))
	for _, message := range messages {
		s.pending.AddChild(tui.NewTruncatedText(s.theme.Fg("dim", "Follow-up: "+SanitizeSingleLine(message)), 1, 0))
	}
	s.pending.AddChild(tui.NewTruncatedText(s.theme.Fg("dim", "↳ "+dequeueKey+" to edit all queued messages"), 1, 0))
}
