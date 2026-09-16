package interactive

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/tui"
)

func TestSurfaceLayoutOrderAndPrimaryRegion(t *testing.T) {
	theme := mustTheme(t)
	surface, _ := newTestSurface(t, SurfaceOptions{Theme: theme, Home: "/home/tester"})
	surface.SetModel("claude-x")
	surface.SetThinkingLevel("medium")
	surface.SetWorkspace("/home/tester/proj")
	surface.SetSessionName("main")
	surface.AddUserMessage("hello")
	surface.SetWorking(true)
	frame := surface.RenderFrame(40, 18)
	if frame.PrimaryScrollView != surface.Transcript() {
		t.Fatal("transcript is not the primary scroll view")
	}
	lines := plainLines(frame.Lines)
	assertWidthBound(t, frame.Lines, 40)
	transcriptRow := indexOfFragment(t, lines, "hello")
	editorBorder := 0
	for index, line := range lines {
		if strings.HasPrefix(line, "─") {
			editorBorder = index
			break
		}
	}
	statusRow := indexOfFragment(t, lines, "Working")
	footerRow := indexOfFragment(t, lines, "~/proj")
	editorInputRow := indexOfFragment(t, lines, "claude-x")
	if !(transcriptRow < editorBorder && editorBorder < statusRow) {
		t.Fatalf("layout order wrong: transcript=%d editorBorder=%d status=%d\n%s", transcriptRow, editorBorder, statusRow, strings.Join(lines, "\n"))
	}
	if statusRow > editorInputRow || editorInputRow < editorBorder {
		t.Fatalf("editor input not between transcript and footer: %d %d", statusRow, editorInputRow)
	}
	if footerRow <= statusRow {
		t.Fatalf("footer should follow the status region: status=%d footer=%d", statusRow, footerRow)
	}
}

func indexOfFragment(t *testing.T, lines []string, fragment string) int {
	t.Helper()
	for index, line := range lines {
		if strings.Contains(line, fragment) {
			return index
		}
	}
	t.Fatalf("frame does not contain %q:\n%s", fragment, strings.Join(lines, "\n"))
	return -1
}

func TestSurfaceStreamingAssistantTurnFrame(t *testing.T) {
	theme := mustTheme(t)
	surface, _ := newTestSurface(t, SurfaceOptions{Theme: theme, Hyperlinks: true, Home: "/home/tester"})
	surface.SetModel("claude-x")
	surface.SetThinkingLevel("medium")
	surface.SetWorkspace("/home/tester/proj")
	surface.SetSessionName("main")
	surface.SetUsage(UsageSummary{Input: 1200, Output: 340, Cost: 0.0123})
	surface.AddUserMessage("hi")
	assistant := surface.StartAssistantTurn()
	assistant.AppendThinking("let me think")
	surface.AppendAssistantText("# Title\n")
	assertFrameLines(t, renderPlainFrame(surface, 40, 16), []string{
		"",
		" hi",
		"",
		"",
		" Thinking... (ctrl+t to expand)",
		" Title",
		"",
		"",
		"",
		"",
		strings.Repeat("─", 40),
		"",
		strings.Repeat("─", 40),
		"~/proj • main",
		"↑1.2k ↓340 $0.012      claude-x • medium",
		"ctrl+o tools · shift+tab thinking · c...",
	})
	surface.AppendAssistantText("Some **bold** and `code` here")
	assertFrameLines(t, renderPlainFrame(surface, 40, 16), []string{
		"",
		" hi",
		"",
		"",
		" Thinking... (ctrl+t to expand)",
		" Title",
		"",
		" Some bold and code here",
		"",
		"",
		strings.Repeat("─", 40),
		"",
		strings.Repeat("─", 40),
		"~/proj • main",
		"↑1.2k ↓340 $0.012      claude-x • medium",
		"ctrl+o tools · shift+tab thinking · c...",
	})
}

func TestSurfaceToolBlockDiffAndOutputFrame(t *testing.T) {
	theme := mustTheme(t)
	surface, _ := newTestSurface(t, SurfaceOptions{Theme: theme, Hyperlinks: true, Home: "/home/tester"})
	surface.SetModel("claude-x")
	tool := surface.AddToolExecution("edit", json.RawMessage(`{"path":"a.txt"}`))
	tool.SetDiff("-1 old line\n+1 new line\n 2 ctx")
	tool.SetResult("Replaced 1 occurrence", ToolSuccess)
	frame := renderPlainFrame(surface, 40, 22)
	assertWidthBound(t, frame, 40)
	assertFrameLines(t, frame, []string{
		"",
		"",
		" edit",
		"",
		" {",
		`   "path": "a.txt"`,
		" }",
		"",
		" -1 old line",
		" +1 new line",
		"  2 ctx",
		"",
		" Replaced 1 occurrence",
		"",
		"",
		"",
		strings.Repeat("─", 40),
		"",
		strings.Repeat("─", 40),
		"",
		"                          claude-x • off",
		"ctrl+o tools · shift+tab thinking · c...",
	})
}

func TestSurfaceToolExpansionIsSurfaceWide(t *testing.T) {
	theme := mustTheme(t)
	surface, _ := newTestSurface(t, SurfaceOptions{Theme: theme})
	first := surface.AddToolExecution("read", nil)
	first.SetResult(strings.Join(linesOf(30, "first"), "\n"), ToolSuccess)
	second := surface.AddToolExecution("bash", nil)
	second.SetResult(strings.Join(linesOf(30, "second"), "\n"), ToolSuccess)
	if !strings.Contains(strings.Join(plainLines(second.Render(60)), "\n"), "to expand") {
		t.Fatal("collapsed hint should offer expanding")
	}
	if !surface.ToggleToolExpansion() {
		t.Fatal("toggle reported no change")
	}
	if !first.IsExpanded() || !second.IsExpanded() {
		t.Fatal("toggle did not expand every block")
	}
	expandedFrame := strings.Join(plainLines(surface.RenderFrame(60, 40).Lines), "\n")
	if !strings.Contains(expandedFrame, "to collapse") || strings.Contains(expandedFrame, "to expand") {
		t.Fatalf("expanded hints not truthful:\n%s", expandedFrame)
	}
	surface.SetWidget("hint", nil)
	third := surface.AddToolExecution("grep", nil)
	if !third.IsExpanded() {
		t.Fatal("new block did not inherit the surface expansion state")
	}
	surface.ToggleToolExpansion()
	if first.IsExpanded() || second.IsExpanded() || third.IsExpanded() {
		t.Fatal("second toggle did not collapse every block")
	}
	collapsedFrame := strings.Join(plainLines(surface.RenderFrame(60, 40).Lines), "\n")
	if !strings.Contains(collapsedFrame, "to expand") || strings.Contains(collapsedFrame, "to collapse") {
		t.Fatalf("collapsed hints not truthful:\n%s", collapsedFrame)
	}
}

func TestSurfaceThinkingToggleIsSurfaceWide(t *testing.T) {
	theme := mustTheme(t)
	surface, _ := newTestSurface(t, SurfaceOptions{Theme: theme})
	first := surface.StartAssistantTurn()
	first.AppendThinking("first reasoning")
	second := surface.StartAssistantTurn()
	second.AppendThinking("second reasoning")
	collapsed := strings.Join(plainLines(surface.RenderFrame(60, 20).Lines), "\n")
	if strings.Contains(collapsed, "reasoning") {
		t.Fatalf("collapsed thinking leaked: %s", collapsed)
	}
	if !surface.ToggleThinking() {
		t.Fatal("toggle reported no change")
	}
	expanded := strings.Join(plainLines(surface.RenderFrame(60, 20).Lines), "\n")
	if !strings.Contains(expanded, "first reasoning") || !strings.Contains(expanded, "second reasoning") {
		t.Fatalf("thinking not expanded surface wide: %s", expanded)
	}
	surface.ToggleThinking()
	recollapsed := strings.Join(plainLines(surface.RenderFrame(60, 20).Lines), "\n")
	if strings.Contains(recollapsed, "reasoning") {
		t.Fatalf("thinking not recollapsed: %s", recollapsed)
	}
}

func TestSurfaceSubmitSeam(t *testing.T) {
	theme := mustTheme(t)
	submitted := make([]string, 0, 2)
	surface, _ := newTestSurface(t, SurfaceOptions{Theme: theme, OnSubmit: func(text string) { submitted = append(submitted, text) }})
	surface.Editor().SetText("first")
	surface.Editor().HandleInput("\r")
	surface.SetOnSubmit(func(text string) { submitted = append(submitted, "replaced:"+text) })
	surface.Editor().SetText("second")
	surface.Editor().HandleInput("\r")
	if len(submitted) != 2 || submitted[0] != "first" || submitted[1] != "replaced:second" {
		t.Fatalf("submitted = %q", submitted)
	}
	if surface.Submit() == nil {
		t.Fatal("Submit seam returned nil")
	}
}

func TestSurfaceEditorChangeAndPending(t *testing.T) {
	theme := mustTheme(t)
	changes := make([]string, 0, 2)
	surface, _ := newTestSurface(t, SurfaceOptions{Theme: theme, OnEditorChange: func(text string) { changes = append(changes, text) }})
	surface.Editor().SetText("draft")
	if len(changes) == 0 || changes[len(changes)-1] != "draft" {
		t.Fatalf("editor change callbacks = %q", changes)
	}
	surface.Editor().HandleInput("\x1b\r")
	frame := strings.Join(plainLines(surface.RenderFrame(60, 20).Lines), "\n")
	if !strings.Contains(frame, "Follow-up: draft") {
		t.Fatalf("queued follow-up not rendered:\n%s", frame)
	}
	if !strings.Contains(frame, "to edit all queued messages") {
		t.Fatalf("queued hint missing:\n%s", frame)
	}
}

func TestSurfaceUsageStatusAndWidgetSeams(t *testing.T) {
	theme := mustTheme(t)
	controller := &recordingController{}
	surface, _ := newTestSurface(t, SurfaceOptions{Theme: theme, Controller: controller, Home: "/home/tester"})
	surface.SetUsage(UsageSummary{Input: 2500, Output: 42, Cost: 0.5})
	surface.SetStatus("mode", "plan\x1b[31m")
	surface.SetWidget("panel", []string{"widget line"})
	frame := strings.Join(plainLines(surface.RenderFrame(60, 20).Lines), "\n")
	if !strings.Contains(frame, "↑2.5k") || !strings.Contains(frame, "$0.500") {
		t.Fatalf("usage not rendered:\n%s", frame)
	}
	if !strings.Contains(frame, "plan") || strings.Contains(frame, "\x1b") {
		t.Fatalf("status not sanitized:\n%s", frame)
	}
	if !strings.Contains(frame, "widget line") {
		t.Fatalf("widget not rendered:\n%s", frame)
	}
	surface.ClearStatus("mode")
	surface.ClearWidget("panel")
	cleared := strings.Join(plainLines(surface.RenderFrame(60, 20).Lines), "\n")
	if strings.Contains(cleared, "plan") || strings.Contains(cleared, "widget line") {
		t.Fatalf("status or widget not cleared:\n%s", cleared)
	}
	if controller.Renders() == 0 {
		t.Fatal("render requests were not forwarded to the controller")
	}
}

func TestSurfaceRetryAndCompactionStatus(t *testing.T) {
	theme := mustTheme(t)
	surface, clock := newTestSurface(t, SurfaceOptions{Theme: theme})
	surface.SetRetry(1, 3, 30*time.Second)
	retry := strings.Join(plainLines(surface.RenderFrame(60, 20).Lines), "\n")
	if !strings.Contains(retry, "Retrying (1/3) in 30s") {
		t.Fatalf("retry status missing:\n%s", retry)
	}
	surface.ClearRetry()
	surface.SetCompacting(true)
	compacting := strings.Join(plainLines(surface.RenderFrame(60, 20).Lines), "\n")
	if !strings.Contains(compacting, "Compacting context") {
		t.Fatalf("compaction status missing:\n%s", compacting)
	}
	surface.SetCompacting(false)
	clock.Advance(time.Second)
}

func TestSurfaceBashCancelSeam(t *testing.T) {
	theme := mustTheme(t)
	cancelled := 0
	surface, _ := newTestSurface(t, SurfaceOptions{Theme: theme})
	bash := surface.AddBashExecutionWithCancel("sleep 1", func() { cancelled++ })
	bash.AppendOutput("line one")
	if !bash.Running() {
		t.Fatal("bash block should start running")
	}
	if !surface.HandleActionKey("\x1b") {
		t.Fatal("interrupt key did not reach the running bash block")
	}
	if cancelled != 1 {
		t.Fatalf("cancel callback calls = %d, want 1", cancelled)
	}
	bash.SetComplete(0, false, true)
	if surface.HandleActionKey("\x1b") {
		t.Fatal("interrupt after completion should be a no-op")
	}
	plain := strings.Join(plainLines(surface.RenderFrame(60, 20).Lines), "\n")
	if !strings.Contains(plain, "(cancelled)") {
		t.Fatalf("bash cancel marker missing:\n%s", plain)
	}
}

func TestSurfaceActionKeyRouting(t *testing.T) {
	theme := mustTheme(t)
	surface, _ := newTestSurface(t, SurfaceOptions{Theme: theme})
	tool := surface.AddToolExecution("read", nil)
	tool.SetResult("out", ToolSuccess)
	if !surface.HandleActionKey("\x0f") {
		t.Fatal("tool expand key not routed")
	}
	if !tool.IsExpanded() {
		t.Fatal("tool expand key did not toggle")
	}
	assistant := surface.StartAssistantTurn()
	assistant.AppendThinking("reasoning")
	if !surface.HandleActionKey("\x14") {
		t.Fatal("thinking toggle key not routed")
	}
	if !strings.Contains(strings.Join(plainLines(surface.RenderFrame(60, 20).Lines), "\n"), "reasoning") {
		t.Fatal("thinking toggle key did not expand")
	}
	if surface.HandleActionKey("unbound-key") {
		t.Fatal("unbound key should not be handled")
	}
}

func TestSurfaceRenderRequestSeamThroughScrollView(t *testing.T) {
	theme := mustTheme(t)
	controller := &recordingController{}
	surface, _ := newTestSurface(t, SurfaceOptions{Theme: theme, Controller: controller})
	assistant := surface.StartAssistantTurn()
	for i := 0; i < 80; i++ {
		assistant.AppendText("line of text\n")
	}
	surface.RenderFrame(40, 12)
	surface.Transcript().ScrollBy(3)
	if controller.Renders() == 0 {
		t.Fatal("scroll view did not request a render")
	}
	if surface.Transcript().Scrollbar() == tui.ScrollbarHidden {
		t.Fatal("default scrollbar mode should not be hidden")
	}
}

func TestSurfaceCloseTeardown(t *testing.T) {
	theme := mustTheme(t)
	controller := &recordingController{}
	surface, clock := newTestSurface(t, SurfaceOptions{Theme: theme, Controller: controller})
	surface.SetWorking(true)
	if clock.Pending() != 1 {
		t.Fatalf("pending animation timers = %d, want 1", clock.Pending())
	}
	clock.Advance(80 * time.Millisecond)
	before := controller.Renders()
	surface.Close()
	if !surface.Closed() {
		t.Fatal("Close did not mark the surface closed")
	}
	if clock.Pending() != 0 {
		t.Fatalf("timers still pending after close: %d", clock.Pending())
	}
	clock.Advance(time.Second)
	surface.SetModel("ignored")
	surface.AddNotice(NoticeInfo, "ignored")
	surface.RequestRender()
	surface.SetWorking(false)
	if controller.Renders() != before {
		t.Fatalf("renders after close = %d, want %d", controller.Renders(), before)
	}
	surface.Close()
}

func TestSurfaceDefaults(t *testing.T) {
	surface := NewSurface(SurfaceOptions{})
	t.Cleanup(surface.Close)
	if surface.Root() == nil || surface.Transcript() == nil || surface.Editor() == nil || surface.Footer() == nil || surface.Status() == nil || surface.WidgetPanel() == nil {
		t.Fatal("default surface component missing")
	}
	if surface.Closed() {
		t.Fatal("fresh surface reports closed")
	}
	frame := surface.RenderFrame(30, 10)
	if frame == nil || frame.Width != 30 || frame.Height != 10 {
		t.Fatalf("frame = %+v", frame)
	}
	lines := plainLines(frame.Lines)
	if !strings.Contains(strings.Join(lines, "\n"), "no-model") {
		t.Fatalf("default footer missing:\n%s", strings.Join(lines, "\n"))
	}
}

func TestSurfaceSetController(t *testing.T) {
	theme := mustTheme(t)
	surface, clock := newTestSurface(t, SurfaceOptions{Theme: theme})
	controller := &recordingController{}
	surface.SetController(controller)
	surface.SetModel("m")
	if controller.Renders() == 0 {
		t.Fatal("controller did not receive render requests")
	}
	before := controller.Renders()
	surface.SetWorking(true)
	clock.Advance(80 * time.Millisecond)
	if controller.Renders() <= before {
		t.Fatal("status animation did not use the updated controller")
	}
	surface.SetWorking(false)
}

func TestSurfaceConcurrentMutationAndRender(t *testing.T) {
	theme := mustTheme(t)
	surface, _ := newTestSurface(t, SurfaceOptions{Theme: theme})
	assistant := surface.StartAssistantTurn()
	assistant.AppendThinking("seed")
	tool := surface.AddToolExecution("read", nil)
	tool.SetResult("seed output", ToolPending)
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for iteration := 0; iteration < 40; iteration++ {
				surface.AppendAssistantText("delta ")
				surface.AppendAssistantThinking("thought ")
				surface.SetStatus("worker", "busy")
				surface.SetWidget("panel", []string{"widget"})
				surface.ToggleToolExpansion()
				surface.ToggleThinking()
				surface.AddNotice(NoticeInfo, "notice")
			}
		}(worker)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for iteration := 0; iteration < 40; iteration++ {
			_ = renderPlainFrame(surface, 50, 20)
		}
	}()
	wg.Wait()
	surface.Close()
}

func TestSurfaceConcurrentClose(t *testing.T) {
	theme := mustTheme(t)
	surface, _ := newTestSurface(t, SurfaceOptions{Theme: theme})
	surface.SetWorking(true)
	done := make(chan struct{})
	go func() {
		for iteration := 0; iteration < 100; iteration++ {
			surface.SetStatus("key", "value")
			surface.AppendAssistantText("delta")
			surface.SetWorkingMessage("working")
		}
		close(done)
	}()
	surface.Close()
	<-done
	if !surface.Closed() {
		t.Fatal("surface not closed")
	}
}

func TestSurfaceBlockFactoriesAndEndTurn(t *testing.T) {
	theme := mustTheme(t)
	surface, _ := newTestSurface(t, SurfaceOptions{Theme: theme})
	surface.StartAssistantTurn()
	surface.AppendAssistantText("assistant body")
	surface.AppendAssistantThinking("assistant thought")
	surface.EndAssistantTurn("length", "")
	bash := surface.AddBashExecution("echo hi")
	bash.AppendOutput("hi from bash")
	bash.SetComplete(0, true, false)
	subagent := surface.AddSubagent("worker")
	subagent.SetOutput("subagent output")
	subagent.SetStatus(ToolSuccess)
	surface.AddSkillInvocation("skillname", "skill instructions")
	surface.AddCompactionSummary("compaction summary", 999)
	frame := strings.Join(plainLines(surface.RenderFrame(70, 60).Lines), "\n")
	for _, want := range []string{
		"assistant body",
		"Response was truncated before completion.",
		"$ echo hi",
		"hi from bash",
		"[subagent] worker",
		"subagent output",
		"[skill] skillname",
		"[compaction]",
		"Compacted from 999 tokens",
	} {
		if !strings.Contains(frame, want) {
			t.Fatalf("frame missing %q:\n%s", want, frame)
		}
	}
	if strings.Contains(frame, "assistant thought") {
		t.Fatalf("collapsed thinking leaked into the frame:\n%s", frame)
	}
}

func TestSurfaceRootRenderConcurrentWithMutation(t *testing.T) {
	theme := mustTheme(t)
	surface, _ := newTestSurface(t, SurfaceOptions{Theme: theme})
	surface.StartAssistantTurn()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for iteration := 0; iteration < 60; iteration++ {
			surface.AppendAssistantText("delta ")
			surface.AddNotice(NoticeInfo, "notice")
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for iteration := 0; iteration < 60; iteration++ {
			_ = surface.Root().Render(50)
			surface.Root().Invalidate()
		}
	}()
	wg.Wait()
	surface.Close()
}
