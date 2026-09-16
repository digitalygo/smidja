package interactive

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/tui"
)

func TestUserMessageRender(t *testing.T) {
	theme := mustTheme(t)
	message := NewUserMessage("hello **world**", theme, false)
	lines := message.Render(30)
	assertWidthBound(t, lines, 30)
	assertContains(t, plainLines(lines), "hello world")
	background, ok := theme.GetBgAnsi("userMessageBg")
	if !ok || !strings.Contains(strings.Join(lines, "\n"), background) {
		t.Fatal("user message background missing")
	}
	message.SetText("replaced")
	if message.Text() != "replaced" {
		t.Fatalf("Text() = %q", message.Text())
	}
	message.Invalidate()
	if got := plainLines(message.Render(30)); !strings.Contains(strings.Join(got, "\n"), "replaced") {
		t.Fatalf("render after SetText = %#v", got)
	}
}

func TestUserMessageNarrowWidth(t *testing.T) {
	theme := mustTheme(t)
	message := NewUserMessage(strings.Repeat("word ", 40), theme, false)
	for _, width := range []int{1, 2, 5, 12} {
		assertWidthBound(t, message.Render(width), width)
	}
}

func TestAssistantMessageStreamingSegments(t *testing.T) {
	theme := mustTheme(t)
	assistant := NewAssistantMessage(theme, false)
	assistant.SetThinkingKeyDisplay("ctrl+t")
	assistant.AppendText("Hello ")
	assistant.AppendText("world")
	assistant.AppendThinking("thinking hard")
	assistant.AppendText(" after")
	lines := plainLines(assistant.Render(40))
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Hello world") {
		t.Fatalf("streamed text missing: %#v", lines)
	}
	if !strings.Contains(joined, "after") {
		t.Fatalf("text after thinking missing: %#v", lines)
	}
	if !strings.Contains(joined, "Thinking...") || !strings.Contains(joined, "ctrl+t to expand") {
		t.Fatalf("collapsed thinking hint missing: %#v", lines)
	}
	if strings.Contains(joined, "thinking hard") {
		t.Fatalf("collapsed thinking leaked content: %#v", lines)
	}
	if assistant.Text() != "Hello world after" {
		t.Fatalf("assistant text = %q", assistant.Text())
	}
}

func TestAssistantMessageThinkingExpanded(t *testing.T) {
	theme := mustTheme(t)
	assistant := NewAssistantMessage(theme, false)
	assistant.SetThinkingKeyDisplay("ctrl+t")
	assistant.AppendThinking("reasoning here")
	assistant.SetThinkingExpanded(true)
	lines := plainLines(assistant.Render(40))
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "reasoning here") {
		t.Fatalf("expanded thinking missing content: %#v", lines)
	}
	if !strings.Contains(joined, "ctrl+t to collapse") {
		t.Fatalf("expanded thinking hint missing: %#v", lines)
	}
	assistant.SetThinkingLevel("high")
	assistant.SetThinkingExpanded(false)
	collapsed := strings.Join(plainLines(assistant.Render(40)), "\n")
	if !strings.Contains(collapsed, "Thinking...") {
		t.Fatalf("collapse after expand failed: %q", collapsed)
	}
}

func TestAssistantMessageStopReasons(t *testing.T) {
	theme := mustTheme(t)
	cases := []struct {
		reason  string
		message string
		want    string
		absent  bool
	}{
		{reason: "", want: "", absent: true},
		{reason: "length", want: "Response was truncated before completion."},
		{reason: "aborted", message: "Request was aborted", want: "Operation aborted"},
		{reason: "aborted", message: "custom abort", want: "custom abort"},
		{reason: "error", message: "boom", want: "Error: boom"},
		{reason: "error", want: "Error: Unknown error"},
	}
	for _, testCase := range cases {
		assistant := NewAssistantMessage(theme, false)
		assistant.AppendText("body")
		assistant.SetStopReason(testCase.reason, testCase.message)
		lines := plainLines(assistant.Render(80))
		joined := strings.Join(lines, "\n")
		if testCase.absent {
			if testCase.want != "" && strings.Contains(joined, testCase.want) {
				t.Fatalf("unexpected stop line for %q", testCase.reason)
			}
			continue
		}
		if !strings.Contains(joined, testCase.want) {
			t.Fatalf("stop reason %q missing %q: %#v", testCase.reason, testCase.want, lines)
		}
	}
}

func TestAssistantMessageErrorSanitized(t *testing.T) {
	theme := mustTheme(t)
	assistant := NewAssistantMessage(theme, false)
	assistant.AppendText("body")
	assistant.SetStopReason("error", "boom\nsecond\x1b[31m")
	joined := strings.Join(plainLines(assistant.Render(60)), "\n")
	if !strings.Contains(joined, "Error: boom second") {
		t.Fatalf("sanitized error missing: %q", joined)
	}
}

func TestNoticeKinds(t *testing.T) {
	theme := mustTheme(t)
	cases := []struct {
		kind  NoticeKind
		token tui.ThemeColor
	}{
		{kind: NoticeInfo, token: "muted"},
		{kind: NoticeWarning, token: "warning"},
		{kind: NoticeError, token: "error"},
	}
	for _, testCase := range cases {
		notice := NewNotice(testCase.kind, "message", theme)
		lines := notice.Render(30)
		notice.Invalidate()
		token, _ := theme.GetFgAnsi(testCase.token)
		if !strings.Contains(lines[1], token) {
			t.Fatalf("notice kind %d missing token %s: %q", testCase.kind, testCase.token, lines[1])
		}
	}
}

func TestNoticeSanitizedAndWrapped(t *testing.T) {
	theme := mustTheme(t)
	notice := NewNotice(NoticeError, "boom\x1b[31m "+strings.Repeat("word ", 30), theme)
	lines := notice.Render(20)
	assertWidthBound(t, lines, 20)
	if strings.Contains(strings.Join(lines, ""), "\x1b[31m") {
		t.Fatal("notice retained raw escape")
	}
}

func TestSkillBlockExpansion(t *testing.T) {
	theme := mustTheme(t)
	skill := NewSkillBlock("skill\x1b[31mname", "content body", theme, false, "ctrl+o")
	collapsed := plainLines(skill.Render(40))
	if !strings.Contains(strings.Join(collapsed, "\n"), "[skill] skillname (ctrl+o to expand)") {
		t.Fatalf("collapsed skill = %#v", collapsed)
	}
	skill.SetExpanded(true)
	if !skill.IsExpanded() {
		t.Fatal("SetExpanded did not stick")
	}
	expanded := plainLines(skill.Render(40))
	if !strings.Contains(strings.Join(expanded, "\n"), "content body") {
		t.Fatalf("expanded skill missing content: %#v", expanded)
	}
	assertWidthBound(t, skill.Render(12), 12)
}

func TestCompactionBlockExpansion(t *testing.T) {
	theme := mustTheme(t)
	block := NewCompactionBlock("summary text", 12345, theme, false, "ctrl+o")
	collapsed := plainLines(block.Render(50))
	if !strings.Contains(strings.Join(collapsed, "\n"), "Compacted from 12,345 tokens") {
		t.Fatalf("collapsed compaction = %#v", collapsed)
	}
	if !strings.Contains(strings.Join(collapsed, "\n"), "ctrl+o to expand") {
		t.Fatalf("collapsed compaction hint missing: %#v", collapsed)
	}
	block.SetExpanded(true)
	if !block.IsExpanded() {
		t.Fatal("SetExpanded did not stick")
	}
	expanded := plainLines(block.Render(50))
	if !strings.Contains(strings.Join(expanded, "\n"), "summary text") {
		t.Fatalf("expanded compaction missing summary: %#v", expanded)
	}
	if !strings.Contains(strings.Join(expanded, "\n"), "ctrl+o to collapse") {
		t.Fatalf("expanded compaction hint missing: %#v", expanded)
	}
	block.Invalidate()
	assertWidthBound(t, block.Render(10), 10)
}

func TestFormatToolArgs(t *testing.T) {
	formatted := formatToolArgs(json.RawMessage(`{"path":"a.txt","count":2}`))
	if !strings.Contains(formatted, "\"path\": \"a.txt\"") {
		t.Fatalf("indented args = %q", formatted)
	}
	if got := formatToolArgs(json.RawMessage("not json\x1b[31m")); got != "not json" {
		t.Fatalf("invalid args = %q", got)
	}
	if got := formatToolArgs(nil); got != "" {
		t.Fatalf("empty args = %q", got)
	}
}

func TestToolExecutionRendersDiffAndOutput(t *testing.T) {
	theme := mustTheme(t)
	changes := 0
	tool := NewToolExecution("edit\x1b[31m", json.RawMessage(`{"path":"a.txt"}`), theme, "ctrl+o", func() { changes++ })
	tool.SetDiff("-1 old line\n+1 new line")
	tool.SetResult("Replaced 1 occurrence", ToolSuccess)
	if changes != 2 {
		t.Fatalf("onChange calls = %d, want 2", changes)
	}
	lines := plainLines(tool.Render(50))
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, " edit") {
		t.Fatalf("tool title missing: %#v", lines)
	}
	if !strings.Contains(joined, "old line") || !strings.Contains(joined, "new line") {
		t.Fatalf("diff missing: %#v", lines)
	}
	if !strings.Contains(joined, "Replaced 1 occurrence") {
		t.Fatalf("tool output hidden when diff present: %#v", lines)
	}
	if tool.Status() != ToolSuccess {
		t.Fatalf("status = %v", tool.Status())
	}
	if tool.Output() != "Replaced 1 occurrence" {
		t.Fatalf("output = %q", tool.Output())
	}
	assertWidthBound(t, tool.Render(20), 20)
}

func TestToolExecutionPreviewCounterAndHints(t *testing.T) {
	theme := mustTheme(t)
	tool := NewToolExecution("bash", nil, theme, "ctrl+o", nil)
	tool.SetResult(strings.Join(linesOf(40, "line"), "\n"), ToolPending)
	preview := strings.Join(plainLines(tool.Render(40)), "\n")
	if !strings.Contains(preview, "30 more lines") || !strings.Contains(preview, "ctrl+o to expand") {
		t.Fatalf("preview counter missing: %q", preview)
	}
	tool.SetExpanded(true)
	expanded := strings.Join(plainLines(tool.Render(40)), "\n")
	if !strings.Contains(expanded, "line 39") {
		t.Fatalf("expanded output missing: %q", expanded)
	}
	if !strings.Contains(expanded, "ctrl+o to collapse") {
		t.Fatalf("collapse hint missing: %q", expanded)
	}
}

func linesOf(count int, prefix string) []string {
	lines := make([]string, count)
	for index := range lines {
		lines[index] = prefix + " " + itoa(index)
	}
	return lines
}

func TestToolExecutionStatusBackgrounds(t *testing.T) {
	theme := mustTheme(t)
	cases := []struct {
		status ToolStatus
		token  tui.ThemeColor
	}{
		{status: ToolPending, token: "toolPendingBg"},
		{status: ToolSuccess, token: "toolSuccessBg"},
		{status: ToolError, token: "toolErrorBg"},
	}
	for _, testCase := range cases {
		tool := NewToolExecution("read", nil, theme, "ctrl+o", nil)
		tool.SetResult("out", testCase.status)
		token, _ := theme.GetBgAnsi(testCase.token)
		lines := tool.Render(30)
		if !strings.Contains(lines[1], token) {
			t.Fatalf("status %d missing background %s", testCase.status, testCase.token)
		}
	}
}

func TestToolExecutionCacheInvalidation(t *testing.T) {
	theme := mustTheme(t)
	tool := NewToolExecution("read", nil, theme, "ctrl+o", nil)
	first := tool.Render(30)
	tool.SetResult("changed", ToolSuccess)
	second := tool.Render(30)
	if strings.Join(first, "|") == strings.Join(second, "|") {
		t.Fatal("cache not invalidated after SetResult")
	}
	tool.Invalidate()
	tool.Render(30)
}

func TestSubagentBlockRender(t *testing.T) {
	theme := mustTheme(t)
	changes := 0
	block := NewSubagentBlock("worker\x1b[31m", theme, "ctrl+o", func() { changes++ })
	block.SetOutput("result output")
	block.SetStatus(ToolSuccess)
	if changes != 2 {
		t.Fatalf("onChange calls = %d, want 2", changes)
	}
	joined := strings.Join(plainLines(block.Render(40)), "\n")
	if !strings.Contains(joined, "[subagent] worker") || !strings.Contains(joined, "result output") {
		t.Fatalf("subagent render = %q", joined)
	}
	block.SetExpanded(true)
	if !block.IsExpanded() {
		t.Fatal("SetExpanded did not stick")
	}
	block.Invalidate()
	assertWidthBound(t, block.Render(8), 8)
}

func newBashExecution(t *testing.T, clock *manualClock, command string) *BashExecution {
	t.Helper()
	runtime := tui.NewRuntime(clock)
	return NewBashExecution(command, mustTheme(t), "ctrl+o", "esc", nil, runtime, nil)
}

func TestBashExecutionRunningAndComplete(t *testing.T) {
	clock := newManualClock(time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC))
	runtime := tui.NewRuntime(clock)
	changes := 0
	block := NewBashExecution("ls\x1b[31m", mustTheme(t), "ctrl+o", "esc", nil, runtime, func() { changes++ })
	running := strings.Join(plainLines(block.Render(40)), "\n")
	if !strings.Contains(running, "$ ls") {
		t.Fatalf("bash command missing: %q", running)
	}
	if !strings.Contains(running, "Running... (esc to cancel)") {
		t.Fatalf("bash cancel hint missing: %q", running)
	}
	block.AppendOutput("one\r\ntwo\n")
	block.AppendOutput("three")
	if changes != 2 {
		t.Fatalf("onChange calls = %d, want 2", changes)
	}
	block.SetComplete(0, true, false)
	if changes != 3 {
		t.Fatalf("onChange calls after complete = %d, want 3", changes)
	}
	completed := strings.Join(plainLines(block.Render(40)), "\n")
	if !strings.Contains(completed, "one") || !strings.Contains(completed, "two") || !strings.Contains(completed, "three") {
		t.Fatalf("bash output missing: %q", completed)
	}
	if strings.Contains(completed, "Running...") {
		t.Fatalf("completed bash still shows spinner: %q", completed)
	}
	if block.Running() {
		t.Fatal("completed bash reports running")
	}
}

func TestBashExecutionErrorAndCancelStatuses(t *testing.T) {
	clock := newManualClock(time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC))
	failed := newBashExecution(t, clock, "false")
	failed.SetComplete(2, true, false)
	if joined := strings.Join(plainLines(failed.Render(40)), "\n"); !strings.Contains(joined, "(exit 2)") {
		t.Fatalf("exit code missing: %q", joined)
	}
	cancelled := newBashExecution(t, clock, "sleep")
	cancelled.SetComplete(0, false, true)
	if joined := strings.Join(plainLines(cancelled.Render(40)), "\n"); !strings.Contains(joined, "(cancelled)") {
		t.Fatalf("cancel marker missing: %q", joined)
	}
}

func TestBashExecutionCancelAction(t *testing.T) {
	clock := newManualClock(time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC))
	cancelled := false
	runtime := tui.NewRuntime(clock)
	block := NewBashExecution("sleep 1", mustTheme(t), "ctrl+o", "esc", nil, runtime, nil)
	block.SetOnCancel(func() { cancelled = true })
	if !block.Cancel() {
		t.Fatal("Cancel should report an action")
	}
	if !cancelled {
		t.Fatal("cancel callback not invoked")
	}
	block.SetComplete(0, true, false)
	if block.Cancel() {
		t.Fatal("Cancel on a finished block should be a no-op")
	}
	block.SetOnCancel(nil)
	block2 := NewBashExecution("sleep 2", mustTheme(t), "ctrl+o", "esc", nil, runtime, nil)
	if block2.Cancel() {
		t.Fatal("Cancel without callback should be a no-op")
	}
}

func TestBashExecutionPreviewAndExpansion(t *testing.T) {
	clock := newManualClock(time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC))
	block := newBashExecution(t, clock, "seq")
	block.AppendOutput(strings.Join(linesOf(30, "row"), "\n"))
	block.SetComplete(0, true, false)
	collapsed := strings.Join(plainLines(block.Render(40)), "\n")
	if !strings.Contains(collapsed, "more lines") || !strings.Contains(collapsed, "ctrl+o to expand") {
		t.Fatalf("bash hidden counter missing: %q", collapsed)
	}
	block.SetExpanded(true)
	expanded := strings.Join(plainLines(block.Render(40)), "\n")
	if !strings.Contains(expanded, "row 29") {
		t.Fatalf("expanded bash output missing: %q", expanded)
	}
	if !strings.Contains(expanded, "ctrl+o to collapse") {
		t.Fatalf("bash collapse hint missing: %q", expanded)
	}
	assertWidthBound(t, block.Render(16), 16)
}

func TestBashExecutionAnimationIsDeterministic(t *testing.T) {
	clock := newManualClock(time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC))
	runtime := tui.NewRuntime(clock)
	renders := 0
	block := NewBashExecution("sleep", mustTheme(t), "ctrl+o", "esc", &tui.LoaderIndicator{Frames: []string{"a", "b", "c"}, IntervalMs: 100}, runtime, func() { renders++ })
	if clock.Pending() != 1 {
		t.Fatalf("expected one animation timer, got %d", clock.Pending())
	}
	clock.Advance(100 * time.Millisecond)
	if renders != 1 {
		t.Fatalf("animation renders = %d, want 1", renders)
	}
	clock.Advance(200 * time.Millisecond)
	if renders != 2 {
		t.Fatalf("animation renders = %d, want 2", renders)
	}
	block.SetComplete(0, true, false)
	clock.Advance(time.Second)
	if renders != 3 || clock.Pending() != 0 {
		t.Fatalf("animation not stopped on completion: renders=%d pending=%d", renders, clock.Pending())
	}
}

func TestThinkingLevelTokens(t *testing.T) {
	cases := map[string]tui.ThemeColor{
		"minimal": "thinkingMinimal",
		"low":     "thinkingLow",
		"medium":  "thinkingMedium",
		"high":    "thinkingHigh",
		"xhigh":   "thinkingXhigh",
		"max":     "thinkingMax",
		"":        "thinkingOff",
	}
	for level, want := range cases {
		if got := thinkingLevelToken(level); got != want {
			t.Fatalf("thinkingLevelToken(%q) = %q, want %q", level, got, want)
		}
	}
}

func TestExpandHint(t *testing.T) {
	if got := expandHint("", "expand"); got != "" {
		t.Fatalf("empty key hint = %q", got)
	}
	if got := expandHint("ctrl+o", "collapse"); got != ", ctrl+o to collapse" {
		t.Fatalf("hint = %q", got)
	}
}

func TestBlockInvalidateNoops(t *testing.T) {
	theme := mustTheme(t)
	NewUserMessage("x", theme, false).Invalidate()
	NewAssistantMessage(theme, false).Invalidate()
	NewNotice(NoticeInfo, "x", theme).Invalidate()
	NewSkillBlock("n", "c", theme, false, "k").Invalidate()
	NewCompactionBlock("s", 1, theme, false, "k").Invalidate()
	NewToolExecution("t", nil, theme, "k", nil).Invalidate()
	NewSubagentBlock("s", theme, "k", nil).Invalidate()
	NewBashExecution("b", theme, "k", "e", nil, nil, nil).Invalidate()
}

func TestBashExecutionWithoutRuntime(t *testing.T) {
	block := NewBashExecution("echo", mustTheme(t), "ctrl+o", "esc", nil, nil, nil)
	block.SetComplete(0, true, false)
	if lines := block.Render(20); len(lines) == 0 {
		t.Fatal("bash without runtime produced no output")
	}
}

func TestBashExecutionSanitizesCommand(t *testing.T) {
	block := NewBashExecution("echo\x1b[31m\nnext", mustTheme(t), "ctrl+o", "esc", nil, nil, nil)
	joined := strings.Join(plainLines(block.Render(40)), "\n")
	if strings.Contains(joined, "\x1b") || !strings.Contains(joined, "$ echo next") {
		t.Fatalf("command not sanitized to a single line: %q", joined)
	}
}

func TestBashExecutionAnimationIdleStop(t *testing.T) {
	clock := newManualClock(time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC))
	runtime := tui.NewRuntime(clock)
	block := NewBashExecution("echo", mustTheme(t), "ctrl+o", "esc", nil, runtime, nil)
	block.mu.Lock()
	block.status = bashComplete
	block.mu.Unlock()
	block.advance()
	if clock.Pending() != 0 {
		t.Fatalf("advance on a finished block left %d timers", clock.Pending())
	}
	runtime.Stop()
}

func TestSubagentBlockNarrowWidth(t *testing.T) {
	block := NewSubagentBlock("worker", mustTheme(t), "ctrl+o", nil)
	block.SetOutput(strings.Repeat("word ", 30))
	for _, width := range []int{1, 4, 10} {
		assertWidthBound(t, block.Render(width), width)
	}
}

func TestBashExecutionTeardownViaRuntimeStop(t *testing.T) {
	clock := newManualClock(time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC))
	runtime := tui.NewRuntime(clock)
	renders := 0
	NewBashExecution("sleep", mustTheme(t), "ctrl+o", "esc", nil, runtime, func() { renders++ })
	runtime.Stop()
	clock.Advance(time.Second)
	if renders != 0 {
		t.Fatalf("renders after runtime stop = %d", renders)
	}
	if clock.Pending() != 0 {
		t.Fatalf("pending timers after stop = %d", clock.Pending())
	}
}

func TestBashExecutionSanitizesCustomFrames(t *testing.T) {
	clock := newManualClock(time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC))
	runtime := tui.NewRuntime(clock)
	defer runtime.Stop()
	block := NewBashExecution("sleep", mustTheme(t), "ctrl+o", "esc", &tui.LoaderIndicator{Frames: []string{"a\x1b[31m", "b"}, IntervalMs: 100}, runtime, nil)
	rendered := block.spinnerFrame()
	if strings.Contains(rendered, "\x1b[31m") {
		t.Fatalf("custom bash frame escape leaked: %q", rendered)
	}
	if got := tui.StripTerminalSequences(rendered); got != "a" {
		t.Fatalf("sanitized bash frame = %q", got)
	}
}
