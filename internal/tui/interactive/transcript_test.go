package interactive

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/tui"
)

func TestReplaceTranscriptResetsAndRebuilds(t *testing.T) {
	surface, _ := newTestSurface(t, SurfaceOptions{})
	surface.AddUserMessage("stale question")
	surface.StartAssistantTurn()
	surface.AppendAssistantText("stale answer")
	surface.EndAssistantTurn("stop", "")
	surface.Status().SetWorking(true)

	surface.ReplaceTranscript([]TranscriptEntry{
		{Kind: ReplayUser, Text: "replayed question"},
		{Kind: ReplayAssistant, Parts: []AssistantMessagePart{{Thinking: true, Text: "replayed thinking"}, {Text: "replayed answer"}}, StopReason: "stop"},
		{Kind: ReplayTool, ToolName: "probe", ToolArgs: json.RawMessage(`{"x":1}`), ToolOutput: "tool output"},
		{Kind: ReplayTool, ToolName: "pending", ToolArgs: json.RawMessage(`{}`), Pending: true},
		{Kind: ReplayCompaction, Summary: "compacted history", TokensBefore: 42},
		{Kind: ReplayNotice, Text: "replay notice"},
	})

	frame := strings.Join(renderPlainFrame(surface, 100, 40), "\n")
	for _, want := range []string{"replayed question", "replayed answer", "Compacted from 42 tokens", "replay notice"} {
		if !strings.Contains(frame, want) {
			t.Errorf("replayed frame is missing %q:\n%s", want, frame)
		}
	}
	if strings.Contains(frame, "stale question") || strings.Contains(frame, "stale answer") {
		t.Errorf("ReplaceTranscript did not clear the previous transcript:\n%s", frame)
	}
	if surface.Status().Working() {
		t.Error("ReplaceTranscript must reset the working indicator")
	}
	if surface.Status().Kind() != StatusIdle {
		t.Errorf("status kind = %v, want idle", surface.Status().Kind())
	}
}

func TestReplaceTranscriptIsSafeWithEmptyInput(t *testing.T) {
	surface, _ := newTestSurface(t, SurfaceOptions{})
	surface.AddUserMessage("something")
	surface.ReplaceTranscript(nil)
	frame := strings.Join(renderPlainFrame(surface, 80, 24), "\n")
	if strings.Contains(frame, "something") {
		t.Errorf("empty ReplaceTranscript did not clear the transcript:\n%s", frame)
	}
}

func TestReplaceTranscriptResetsScrollState(t *testing.T) {
	surface, _ := newTestSurface(t, SurfaceOptions{})
	for i := 0; i < 60; i++ {
		surface.AddUserMessage("historical entry")
	}
	surface.Transcript().ScrollBy(-20)
	surface.ReplaceTranscript([]TranscriptEntry{{Kind: ReplayNotice, Text: "single entry"}})
	if !surface.Transcript().IsFollowingEnd() {
		t.Fatal("ReplaceTranscript must reset the scroll view to follow the end")
	}
	_ = tui.CursorMarker
}
