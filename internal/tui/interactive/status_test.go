package interactive

import (
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/tui"
)

func newStatus(t *testing.T, clock *manualClock, controller tui.TUIController, indicator *tui.LoaderIndicator) (*StatusIndicator, *tui.Runtime) {
	t.Helper()
	if clock == nil {
		clock = newManualClock(time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC))
	}
	runtime := tui.NewRuntime(clock)
	status := NewStatusIndicator(clock, runtime, controller, indicator, mustKeys(t), mustTheme(t))
	return status, runtime
}

func TestStatusIndicatorIdle(t *testing.T) {
	status, runtime := newStatus(t, nil, nil, nil)
	defer runtime.Stop()
	if status.Kind() != StatusIdle {
		t.Fatalf("initial kind = %v", status.Kind())
	}
	if lines := status.Render(40); lines != nil {
		t.Fatalf("idle render = %#v", lines)
	}
	if status.Working() {
		t.Fatal("idle reports working")
	}
}

func TestStatusIndicatorWorkingElapsed(t *testing.T) {
	clock := newManualClock(time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC))
	status, runtime := newStatus(t, clock, nil, nil)
	defer runtime.Stop()
	status.SetWorking(true)
	clock.Advance(5 * time.Second)
	lines := plainLines(status.Render(40))
	if !strings.Contains(lines[0], "Working (5s)") {
		t.Fatalf("working line = %q", lines[0])
	}
	status.SetWorkingMessage("Thinking hard")
	lines = plainLines(status.Render(40))
	if !strings.Contains(lines[0], "Thinking hard (5s)") {
		t.Fatalf("working message line = %q", lines[0])
	}
	status.SetWorking(false)
	if status.Working() {
		t.Fatal("SetWorking(false) did not clear working")
	}
	if lines := status.Render(40); lines != nil {
		t.Fatalf("render after stop = %#v", lines)
	}
}

func TestStatusIndicatorRetryCountdown(t *testing.T) {
	clock := newManualClock(time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC))
	status, runtime := newStatus(t, clock, nil, nil)
	defer runtime.Stop()
	status.SetRetry(2, 5, 10*time.Second)
	if status.Kind() != StatusRetry {
		t.Fatalf("kind = %v", status.Kind())
	}
	lines := plainLines(status.Render(60))
	if !strings.Contains(lines[0], "Retrying (2/5) in 10s... (escape to cancel)") {
		t.Fatalf("retry line = %q", lines[0])
	}
	clock.Advance(4 * time.Second)
	lines = plainLines(status.Render(60))
	if !strings.Contains(lines[0], "in 6s") {
		t.Fatalf("retry countdown = %q", lines[0])
	}
	clock.Advance(time.Hour)
	lines = plainLines(status.Render(60))
	if !strings.Contains(lines[0], "in 0s") {
		t.Fatalf("retry countdown floor = %q", lines[0])
	}
	status.ClearRetry()
	if status.Kind() != StatusIdle {
		t.Fatalf("kind after clear = %v", status.Kind())
	}
}

func TestStatusIndicatorCompacting(t *testing.T) {
	status, runtime := newStatus(t, nil, nil, nil)
	defer runtime.Stop()
	status.SetCompacting(true)
	if status.Kind() != StatusCompaction {
		t.Fatalf("kind = %v", status.Kind())
	}
	lines := plainLines(status.Render(60))
	if !strings.Contains(lines[0], "Compacting context... (escape to cancel)") {
		t.Fatalf("compacting line = %q", lines[0])
	}
	status.SetCompacting(false)
	if status.Kind() != StatusIdle {
		t.Fatalf("kind = %v", status.Kind())
	}
}

func TestStatusIndicatorAnimationDeterministic(t *testing.T) {
	clock := newManualClock(time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC))
	controller := &recordingController{}
	status, runtime := newStatus(t, clock, controller, &tui.LoaderIndicator{Frames: []string{"a", "b", "c"}, IntervalMs: 100})
	status.SetWorking(true)
	if clock.Pending() != 1 {
		t.Fatalf("pending timers = %d, want 1", clock.Pending())
	}
	if lines := plainLines(status.Render(40)); !strings.HasPrefix(lines[0], "a ") {
		t.Fatalf("first frame = %q", lines[0])
	}
	clock.Advance(100 * time.Millisecond)
	if controller.Renders() != 1 {
		t.Fatalf("renders after one tick = %d", controller.Renders())
	}
	if lines := plainLines(status.Render(40)); !strings.HasPrefix(lines[0], "b ") {
		t.Fatalf("second frame = %q", lines[0])
	}
	clock.Advance(100 * time.Millisecond)
	if lines := plainLines(status.Render(40)); !strings.HasPrefix(lines[0], "c ") {
		t.Fatalf("third frame = %q", lines[0])
	}
	status.SetWorking(false)
	clock.Advance(time.Second)
	if clock.Pending() != 0 || controller.Renders() != 2 {
		t.Fatalf("animation not stopped: pending=%d renders=%d", clock.Pending(), controller.Renders())
	}
	runtime.Stop()
}

func TestStatusIndicatorStopAnimation(t *testing.T) {
	clock := newManualClock(time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC))
	status, runtime := newStatus(t, clock, nil, nil)
	status.SetCompacting(true)
	status.StopAnimation()
	if clock.Pending() != 0 {
		t.Fatalf("pending timers after StopAnimation = %d", clock.Pending())
	}
	runtime.Stop()
	status.SetWorking(true)
	if clock.Pending() != 0 {
		t.Fatalf("pending timers after runtime stop = %d", clock.Pending())
	}
}

func TestStatusIndicatorRenderWidthBound(t *testing.T) {
	status, runtime := newStatus(t, nil, nil, nil)
	defer runtime.Stop()
	status.SetWorking(true)
	status.SetWorkingMessage(strings.Repeat("long message ", 20))
	for _, width := range []int{1, 5, 20, 80} {
		lines := status.Render(width)
		assertWidthBound(t, lines, width)
	}
}

func TestFormatTokens(t *testing.T) {
	cases := []struct {
		count int64
		want  string
	}{
		{count: -5, want: "0"},
		{count: 999, want: "999"},
		{count: 1500, want: "1.5k"},
		{count: 15000, want: "15k"},
		{count: 1500000, want: "1.5M"},
		{count: 15000000, want: "15M"},
	}
	for _, testCase := range cases {
		if got := FormatTokens(testCase.count); got != testCase.want {
			t.Fatalf("FormatTokens(%d) = %q, want %q", testCase.count, got, testCase.want)
		}
	}
}

func TestFormatWorkspace(t *testing.T) {
	cases := []struct {
		name      string
		workspace string
		home      string
		want      string
	}{
		{name: "empty home", workspace: "/tmp/x", home: "", want: "/tmp/x"},
		{name: "empty workspace", workspace: "", home: "/home/x", want: ""},
		{name: "under home", workspace: "/home/x/proj", home: "/home/x", want: "~/proj"},
		{name: "equal home", workspace: "/home/x", home: "/home/x", want: "~"},
		{name: "outside home", workspace: "/tmp/x", home: "/home/x", want: "/tmp/x"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := FormatWorkspace(testCase.workspace, testCase.home); got != testCase.want {
				t.Fatalf("FormatWorkspace(%q, %q) = %q, want %q", testCase.workspace, testCase.home, got, testCase.want)
			}
		})
	}
}

func TestFooterContent(t *testing.T) {
	theme := mustTheme(t)
	footer := NewFooter(theme, mustKeys(t), "/home/tester")
	footer.SetModel("llama\x1b[31m")
	footer.SetThinkingLevel("medium\nlevel")
	footer.SetWorkspace("/home/tester/proj\x07")
	footer.SetSessionName("session\x1b[31m")
	footer.SetUsage(UsageSummary{Input: 1500, Output: 25, CacheRead: 100, CacheWrite: 50, Cost: 0.125})
	queued := 2
	footer.SetQueuedSource(func() int { return queued })
	footer.SetStatus("b", "second")
	footer.SetStatus("a", "first")
	footer.Invalidate()
	lines := plainLines(footer.Render(80))
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "~/proj") || !strings.Contains(joined, "session") {
		t.Fatalf("workspace line = %#v", lines)
	}
	if !strings.Contains(joined, "2 queued") {
		t.Fatalf("queued count missing: %#v", lines)
	}
	if !strings.Contains(joined, "llama") || strings.Contains(joined, "\x1b") {
		t.Fatalf("model not sanitized: %#v", lines)
	}
	if !strings.Contains(joined, "medium level") {
		t.Fatalf("thinking level not sanitized: %#v", lines)
	}
	if !strings.Contains(joined, "↑1.5k") || !strings.Contains(joined, "↓25") || !strings.Contains(joined, "R100") || !strings.Contains(joined, "W50") || !strings.Contains(joined, "$0.125") {
		t.Fatalf("usage line missing stats: %#v", lines)
	}
	if !strings.Contains(joined, "first second") {
		t.Fatalf("statuses not sorted: %#v", lines)
	}
	footer.ClearStatus("a")
	footer.ClearStatus("b")
	if got := footer.Render(80); strings.Contains(strings.Join(got, "\n"), "first") {
		t.Fatalf("cleared statuses still rendered: %#v", got)
	}
	if footer.ThinkingLevel() != "medium level" {
		t.Fatalf("ThinkingLevel() = %q", footer.ThinkingLevel())
	}
}

func TestFooterWidthBounds(t *testing.T) {
	theme := mustTheme(t)
	footer := NewFooter(theme, nil, "/home/tester")
	footer.SetModel(strings.Repeat("model", 20))
	footer.SetWorkspace(strings.Repeat("deep/path", 10))
	footer.SetSessionName(strings.Repeat("session", 10))
	footer.SetUsage(UsageSummary{Input: 1234567, Output: 7654321, Cost: 12.5})
	footer.SetStatus("k", strings.Repeat("status ", 20))
	for _, width := range []int{1, 4, 10, 30} {
		lines := footer.Render(width)
		assertWidthBound(t, lines, width)
	}
}

func TestFooterEmptyModel(t *testing.T) {
	theme := mustTheme(t)
	footer := NewFooter(theme, nil, "")
	lines := plainLines(footer.Render(40))
	if !strings.Contains(strings.Join(lines, "\n"), "no-model") {
		t.Fatalf("placeholder model missing: %#v", lines)
	}
}

func TestWidgetPanelLifecycle(t *testing.T) {
	panel := NewWidgetPanel()
	if lines := panel.Render(20); lines != nil {
		t.Fatalf("empty panel rendered %#v", lines)
	}
	panel.SetWidget("z", []string{"z line\x1b[31m", strings.Repeat("long ", 20)})
	panel.SetWidget("a", []string{"a line"})
	panel.Invalidate()
	if !panel.HasWidget("a") || panel.HasWidget("missing") {
		t.Fatal("HasWidget mismatch")
	}
	lines := plainLines(panel.Render(12))
	if len(lines) != 3 {
		t.Fatalf("widget lines = %d, want 3: %#v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "a line") {
		t.Fatalf("widgets not sorted: %#v", lines)
	}
	if strings.Contains(strings.Join(lines, ""), "\x1b[31m") {
		t.Fatal("widget content not sanitized")
	}
	for _, line := range panel.Render(12) {
		if tui.VisibleWidth(line) > 12 {
			t.Fatalf("widget line exceeds width: %q", line)
		}
	}
	panel.ClearWidget("a")
	if panel.HasWidget("a") {
		t.Fatal("ClearWidget did not remove the widget")
	}
	panel.ClearWidget("a")
	if lines := panel.Render(0); lines != nil {
		t.Fatalf("zero width render = %#v", lines)
	}
}

func TestSystemClock(t *testing.T) {
	clock := SystemClock{}
	if clock.Now().IsZero() {
		t.Fatal("system clock returned zero time")
	}
	done := make(chan struct{})
	timer := clock.AfterFunc(time.Millisecond, func() { close(done) })
	defer timer.Stop()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("system clock timer did not fire")
	}
}

func TestStatusIndicatorFallbackInterruptKey(t *testing.T) {
	clock := newManualClock(time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC))
	runtime := tui.NewRuntime(clock)
	status := NewStatusIndicator(clock, runtime, nil, nil, nil, mustTheme(t))
	status.SetRetry(1, 2, 5*time.Second)
	lines := plainLines(status.Render(60))
	if !strings.Contains(lines[0], "(esc to cancel)") {
		t.Fatalf("fallback interrupt key missing: %q", lines[0])
	}
	status.Invalidate()
	runtime.Stop()
}

func TestStatusIndicatorWithoutRuntime(t *testing.T) {
	clock := newManualClock(time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC))
	status := NewStatusIndicator(clock, nil, nil, &tui.LoaderIndicator{}, nil, mustTheme(t))
	status.SetWorking(true)
	status.SetCompacting(true)
	status.StopAnimation()
	status.advance()
	if lines := plainLines(status.Render(40)); len(lines) == 0 {
		t.Fatal("indicator without runtime produced no output")
	}
}

func TestStatusIndicatorAdvanceIdleStops(t *testing.T) {
	clock := newManualClock(time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC))
	status, runtime := newStatus(t, clock, nil, nil)
	defer runtime.Stop()
	status.SetWorking(true)
	status.SetWorking(false)
	status.advance()
	if clock.Pending() != 0 {
		t.Fatalf("advance left %d timers", clock.Pending())
	}
}

func TestStatusIndicatorOverlapTransitions(t *testing.T) {
	clock := newManualClock(time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC))
	status, runtime := newStatus(t, clock, nil, nil)
	defer runtime.Stop()

	status.SetWorking(true)
	status.SetRetry(1, 3, 5*time.Second)
	if status.Kind() != StatusRetry {
		t.Fatalf("kind with retry and working = %v", status.Kind())
	}
	status.ClearRetry()
	if status.Kind() != StatusWorking {
		t.Fatalf("kind after retry cleared = %v", status.Kind())
	}
	if clock.Pending() != 1 {
		t.Fatalf("animation stopped while working after retry: pending=%d", clock.Pending())
	}

	status.SetRetry(1, 3, 5*time.Second)
	status.SetWorking(false)
	if status.Kind() != StatusRetry {
		t.Fatalf("kind with working cleared during retry = %v", status.Kind())
	}
	if clock.Pending() != 1 {
		t.Fatalf("animation stopped while retrying: pending=%d", clock.Pending())
	}

	status.ClearRetry()
	if status.Kind() != StatusIdle {
		t.Fatalf("kind after retry cleared with no other state = %v", status.Kind())
	}
	if clock.Pending() != 0 {
		t.Fatalf("animation not stopped when idle: pending=%d", clock.Pending())
	}

	status.SetWorking(true)
	status.SetCompacting(true)
	status.SetWorking(false)
	if status.Kind() != StatusCompaction {
		t.Fatalf("kind with working cleared during compaction = %v", status.Kind())
	}
	if clock.Pending() != 1 {
		t.Fatalf("animation stopped while compacting: pending=%d", clock.Pending())
	}

	status.SetCompacting(false)
	if status.Kind() != StatusIdle {
		t.Fatalf("kind after all states cleared = %v", status.Kind())
	}
	if clock.Pending() != 0 {
		t.Fatalf("animation not stopped when idle: pending=%d", clock.Pending())
	}
}

func TestStatusIndicatorClearRetryKeepsWorking(t *testing.T) {
	clock := newManualClock(time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC))
	status, runtime := newStatus(t, clock, nil, nil)
	defer runtime.Stop()
	status.SetWorking(true)
	status.SetRetry(1, 2, time.Second)
	status.ClearRetry()
	if status.Kind() != StatusWorking {
		t.Fatalf("kind after clearing retry = %v", status.Kind())
	}
	if clock.Pending() != 1 {
		t.Fatalf("animation stopped after clearing retry: pending=%d", clock.Pending())
	}
	status.SetWorking(false)
	if status.Kind() != StatusIdle {
		t.Fatalf("kind after clearing working = %v", status.Kind())
	}
	if clock.Pending() != 0 {
		t.Fatalf("animation not stopped when idle: pending=%d", clock.Pending())
	}
}

func TestStatusIndicatorClearRetryToIdle(t *testing.T) {
	clock := newManualClock(time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC))
	status, runtime := newStatus(t, clock, nil, nil)
	defer runtime.Stop()
	status.SetRetry(1, 2, time.Second)
	if clock.Pending() != 1 {
		t.Fatalf("animation did not start for retry: pending=%d", clock.Pending())
	}
	status.ClearRetry()
	if status.Kind() != StatusIdle {
		t.Fatalf("kind after clearing retry = %v", status.Kind())
	}
	if clock.Pending() != 0 {
		t.Fatalf("animation not stopped after clearing retry to idle: pending=%d", clock.Pending())
	}
	status.ClearRetry()
	if clock.Pending() != 0 {
		t.Fatalf("repeated ClearRetry changed timers: pending=%d", clock.Pending())
	}
}

func TestStatusIndicatorRepeatedWorkingPreservesElapsed(t *testing.T) {
	clock := newManualClock(time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC))
	status, runtime := newStatus(t, clock, nil, nil)
	defer runtime.Stop()
	status.SetWorking(true)
	clock.Advance(5 * time.Second)
	status.SetWorking(true)
	clock.Advance(2 * time.Second)
	lines := plainLines(status.Render(40))
	if !strings.Contains(lines[0], "Working (7s)") {
		t.Fatalf("repeated SetWorking reset elapsed time: %q", lines[0])
	}
	if clock.Pending() != 1 {
		t.Fatalf("repeated SetWorking restarted animation: pending=%d", clock.Pending())
	}
}

func TestStatusIndicatorSanitizesCustomFrames(t *testing.T) {
	clock := newManualClock(time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC))
	status, runtime := newStatus(t, clock, nil, &tui.LoaderIndicator{Frames: []string{"a\x1b[31m", "b"}, IntervalMs: 100})
	defer runtime.Stop()
	status.SetWorking(true)
	rendered := status.Render(40)[0]
	if strings.Contains(rendered, "\x1b[31m") {
		t.Fatalf("custom frame escape leaked into render: %q", rendered)
	}
	if plain := tui.StripTerminalSequences(rendered); !strings.HasPrefix(plain, "a ") {
		t.Fatalf("sanitized frame missing: %q", plain)
	}
}

func TestFooterClearStatusSanitizesKey(t *testing.T) {
	footer := NewFooter(mustTheme(t), nil, "")
	footer.SetStatus("s\x1b[31m", "visible")
	if !strings.Contains(strings.Join(footer.Render(40), "\n"), "visible") {
		t.Fatal("status was not stored")
	}
	footer.ClearStatus("s\x1b[31m")
	if strings.Contains(strings.Join(footer.Render(40), "\n"), "visible") {
		t.Fatal("ClearStatus did not sanitize its key")
	}
}

func TestWidgetPanelClearWidgetSanitizesKey(t *testing.T) {
	panel := NewWidgetPanel()
	panel.SetWidget("w\x1b[31m", []string{"content"})
	if !panel.HasWidget("w") {
		t.Fatal("SetWidget did not sanitize its key")
	}
	panel.ClearWidget("w\x1b[31m")
	if panel.HasWidget("w") {
		t.Fatal("ClearWidget did not sanitize its key")
	}
}
