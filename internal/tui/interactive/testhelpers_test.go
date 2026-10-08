package interactive

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/tui"
)

func mustTheme(t *testing.T) *tui.Theme {
	t.Helper()
	registry := tui.NewThemeRegistry("", "", tui.ColorModeTrueColor)
	theme, err := registry.SetTheme("dark")
	if err != nil {
		t.Fatalf("load dark theme: %v", err)
	}
	return theme
}

func mustKeys(t *testing.T) *tui.KeybindingsManager {
	t.Helper()
	return tui.NewDefaultKeybindingsManager(nil)
}

type manualTask struct {
	id       int
	deadline time.Time
	fn       func()
	stopped  bool
}

type manualTimer struct {
	clock *manualClock
	id    int
}

func (t *manualTimer) Stop() bool {
	return t.clock.stop(t.id)
}

type manualClock struct {
	mu     sync.Mutex
	now    time.Time
	nextID int
	tasks  map[int]*manualTask
}

func newManualClock(start time.Time) *manualClock {
	return &manualClock{now: start, tasks: map[int]*manualTask{}}
}

func (c *manualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *manualClock) AfterFunc(d time.Duration, fn func()) tui.Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	task := &manualTask{id: c.nextID, deadline: c.now.Add(d), fn: fn}
	c.tasks[task.id] = task
	return &manualTimer{clock: c, id: task.id}
}

func (c *manualClock) stop(id int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	task, ok := c.tasks[id]
	if !ok || task.stopped {
		return false
	}
	delete(c.tasks, id)
	return true
}

func (c *manualClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	for {
		var chosen *manualTask
		for _, task := range c.tasks {
			if task.stopped || task.deadline.After(c.now) {
				continue
			}
			if chosen == nil || task.deadline.Before(chosen.deadline) || (task.deadline.Equal(chosen.deadline) && task.id < chosen.id) {
				chosen = task
			}
		}
		if chosen == nil {
			break
		}
		delete(c.tasks, chosen.id)
		c.mu.Unlock()
		chosen.fn()
		c.mu.Lock()
	}
	c.mu.Unlock()
}

func (c *manualClock) Pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.tasks)
}

type recordingController struct {
	mu      sync.Mutex
	renders int
}

func (r *recordingController) RequestRender(bool) {
	r.mu.Lock()
	r.renders++
	r.mu.Unlock()
}

func (r *recordingController) Renders() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.renders
}

func newTestSurface(t *testing.T, options SurfaceOptions) (*Surface, *manualClock) {
	t.Helper()
	theme := options.Theme
	if theme == nil {
		theme = mustTheme(t)
	}
	clock := newManualClock(time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC))
	options.Theme = theme
	options.Clock = clock
	if options.Keybindings == nil {
		options.Keybindings = mustKeys(t)
	}
	if options.Editor == nil {
		options.Editor = tui.NewEditor(tui.EditorOptions{Theme: theme, TerminalRows: 24})
	}
	surface := NewSurface(options)
	t.Cleanup(surface.Close)
	return surface, clock
}

func normalizeFrame(lines []string) []string {
	normalized := make([]string, len(lines))
	for i, line := range lines {
		line = strings.ReplaceAll(line, tui.CursorMarker, "")
		normalized[i] = strings.TrimRight(line, " ")
	}
	return normalized
}

func renderFrame(surface *Surface, width, height int) []string {
	return normalizeFrame(surface.RenderFrame(width, height).Lines)
}

func renderPlainFrame(surface *Surface, width, height int) []string {
	return plainLines(renderFrame(surface, width, height))
}

func assertFrameLines(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("frame height mismatch: got %d lines, want %d\ngot:\n%s", len(got), len(want), strings.Join(got, "\n"))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("frame line %d mismatch:\n got: %q\nwant: %q", i, got[i], want[i])
		}
	}
}

func assertContains(t *testing.T, lines []string, fragment string) {
	t.Helper()
	for _, line := range lines {
		if strings.Contains(line, fragment) {
			return
		}
	}
	t.Fatalf("frame does not contain %q:\n%s", fragment, strings.Join(lines, "\n"))
}

func assertNotContains(t *testing.T, lines []string, fragment string) {
	t.Helper()
	for _, line := range lines {
		if strings.Contains(line, fragment) {
			t.Fatalf("frame unexpectedly contains %q:\n%s", fragment, strings.Join(lines, "\n"))
		}
	}
}

func assertWidthBound(t *testing.T, lines []string, width int) {
	t.Helper()
	for index, line := range lines {
		if visible := tui.VisibleWidth(line); visible > width {
			t.Fatalf("line %d exceeds width %d: visible %d %q", index, width, visible, line)
		}
	}
}

func plainLines(lines []string) []string {
	result := make([]string, len(lines))
	for index, line := range lines {
		result[index] = strings.TrimRight(tui.StripTerminalSequences(line), " ")
	}
	return result
}
