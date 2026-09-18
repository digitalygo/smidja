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
	Timestamp    string
}

func (s *Surface) ReplaceTranscript(entries []TranscriptEntry) {
	s.runtime.Run(func() {
		s.chat.Clear()
		s.pending.Clear()
		s.assistants = nil
		s.collapsibles = nil
		s.status.Reset()
		s.footer.SetUsage(UsageSummary{})
		s.refreshPendingLocked()
		for _, entry := range entries {
			s.replayEntryLocked(entry)
		}
		if s.transcript != nil {
			s.transcript.ScrollToEnd()
		}
	})
	s.invalidateChat()
}

func (s *Surface) replayEntryLocked(entry TranscriptEntry) {
	switch entry.Kind {
	case ReplayUser:
		s.chat.AddChild(NewUserMessage(entry.Text, s.theme, s.hyperlinks))
	case ReplayAssistant:
		assistant := NewAssistantMessage(s.theme, s.hyperlinks)
		assistant.SetThinkingLevel(s.footer.ThinkingLevel())
		assistant.SetThinkingExpanded(s.thinkingExpanded)
		assistant.SetThinkingKeyDisplay(s.thinkingKey)
		assistant.ReconcileContent(entry.Parts)
		if entry.StopReason != "" || entry.ErrorMessage != "" {
			assistant.SetStopReason(entry.StopReason, entry.ErrorMessage)
		}
		s.assistants = append(s.assistants, assistant)
		s.chat.AddChild(assistant)
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
		block := NewCustomBlock(entry.CustomLabel, entry.Text, s.theme, s.hyperlinks, s.expandKey)
		block.SetExpanded(s.toolsExpanded)
		s.collapsibles = append(s.collapsibles, block)
		s.chat.AddChild(block)
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
