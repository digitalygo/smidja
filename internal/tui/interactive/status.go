package interactive

import (
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/digitalygo/smidja/internal/tui"
)

var defaultSpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const defaultSpinnerInterval = 80 * time.Millisecond

type Clock interface {
	Now() time.Time
	AfterFunc(d time.Duration, fn func()) tui.Timer
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }

func (SystemClock) AfterFunc(d time.Duration, fn func()) tui.Timer {
	return time.AfterFunc(d, fn)
}

type StatusKind int

const (
	StatusIdle StatusKind = iota
	StatusWorking
	StatusRetry
	StatusCompaction
)

type StatusIndicator struct {
	mu              sync.Mutex
	working         bool
	compacting      bool
	retryAttempt    int
	retryMax        int
	deadline        time.Time
	workingSince    time.Time
	message         string
	frames          []string
	interval        time.Duration
	frame           int
	runtime         *tui.Runtime
	controller      tui.TUIController
	cancelAnimation func() bool
	clock           Clock
	keybindings     *tui.KeybindingsManager
	theme           *tui.Theme
}

func NewStatusIndicator(clock Clock, runtime *tui.Runtime, controller tui.TUIController, indicator *tui.LoaderIndicator, keybindings *tui.KeybindingsManager, theme *tui.Theme) *StatusIndicator {
	frames := defaultSpinnerFrames
	interval := defaultSpinnerInterval
	if indicator != nil {
		if len(indicator.Frames) > 0 {
			frames = sanitizeFrames(indicator.Frames)
		}
		if indicator.IntervalMs > 0 {
			interval = time.Duration(indicator.IntervalMs) * time.Millisecond
		}
	}
	return &StatusIndicator{
		frames:      frames,
		interval:    interval,
		runtime:     runtime,
		controller:  controller,
		clock:       clock,
		keybindings: keybindings,
		theme:       theme,
	}
}

func (s *StatusIndicator) SetWorking(working bool) {
	s.mu.Lock()
	if working && !s.working {
		s.workingSince = s.clock.Now()
	}
	s.working = working
	s.syncAnimationLocked()
	s.mu.Unlock()
}

func (s *StatusIndicator) Working() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.working
}

func (s *StatusIndicator) SetWorkingMessage(message string) {
	s.mu.Lock()
	s.message = SanitizeSingleLine(message)
	s.mu.Unlock()
}

func (s *StatusIndicator) SetRetry(attempt, maxAttempts int, delay time.Duration) {
	s.mu.Lock()
	s.retryAttempt = attempt
	s.retryMax = maxAttempts
	s.deadline = s.clock.Now().Add(delay)
	s.syncAnimationLocked()
	s.mu.Unlock()
}

func (s *StatusIndicator) ClearRetry() {
	s.mu.Lock()
	if s.retryAttempt == 0 && s.retryMax == 0 {
		s.mu.Unlock()
		return
	}
	s.retryAttempt = 0
	s.retryMax = 0
	s.syncAnimationLocked()
	s.mu.Unlock()
}

func (s *StatusIndicator) SetCompacting(active bool) {
	s.mu.Lock()
	s.compacting = active
	s.syncAnimationLocked()
	s.mu.Unlock()
}

func (s *StatusIndicator) StopAnimation() {
	s.mu.Lock()
	s.stopAnimationLocked()
	s.mu.Unlock()
}

func (s *StatusIndicator) SetController(controller tui.TUIController) {
	s.mu.Lock()
	s.controller = controller
	s.mu.Unlock()
}

func (s *StatusIndicator) Kind() StatusKind {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resolveKindLocked()
}

func (s *StatusIndicator) resolveKindLocked() StatusKind {
	switch {
	case s.retryAttempt > 0:
		return StatusRetry
	case s.compacting:
		return StatusCompaction
	case s.working:
		return StatusWorking
	default:
		return StatusIdle
	}
}

func (s *StatusIndicator) syncAnimationLocked() {
	if s.runtime == nil || s.resolveKindLocked() == StatusIdle {
		s.stopAnimationLocked()
		return
	}
	if s.cancelAnimation == nil {
		s.cancelAnimation = s.runtime.Every(s.interval, s.advance)
	}
}

func (s *StatusIndicator) stopAnimationLocked() {
	if s.cancelAnimation != nil {
		s.cancelAnimation()
		s.cancelAnimation = nil
	}
}

func (s *StatusIndicator) advance() {
	s.mu.Lock()
	if s.resolveKindLocked() == StatusIdle {
		s.stopAnimationLocked()
		s.mu.Unlock()
		return
	}
	if len(s.frames) > 0 {
		s.frame = (s.frame + 1) % len(s.frames)
	}
	controller := s.controller
	s.mu.Unlock()
	if controller != nil {
		controller.RequestRender(false)
	}
}

func (s *StatusIndicator) Invalidate() {}

func (s *StatusIndicator) Render(width int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	kind := s.resolveKindLocked()
	var line string
	switch kind {
	case StatusWorking:
		line = s.spinner("accent") + " " + s.themeMessage(s.workingLabel()) + elapsedSuffix(s.clock.Now().Sub(s.workingSince))
	case StatusRetry:
		remaining := int(math.Ceil(s.deadline.Sub(s.clock.Now()).Seconds()))
		if remaining < 0 {
			remaining = 0
		}
		message := fmt.Sprintf("Retrying (%d/%d) in %ds... (%s to cancel)", s.retryAttempt, s.retryMax, remaining, s.interruptKey())
		line = s.spinner("warning") + " " + s.themeMessage(message)
	case StatusCompaction:
		message := "Compacting context... (" + s.interruptKey() + " to cancel)"
		line = s.spinner("accent") + " " + s.themeMessage(message)
	default:
		return nil
	}
	return []string{padLineToWidth(tui.TruncateToWidth(line, maxInt(0, width), "", false), width)}
}

func elapsedSuffix(elapsed time.Duration) string {
	seconds := int(elapsed.Seconds())
	if seconds < 0 {
		seconds = 0
	}
	return " (" + itoa(seconds) + "s)"
}

func (s *StatusIndicator) interruptKey() string {
	if s.keybindings == nil {
		return "esc"
	}
	keys := s.keybindings.Keys("app.interrupt")
	if len(keys) == 0 {
		return "esc"
	}
	return SanitizeSingleLine(strings.Join(keys, "/"))
}

func (s *StatusIndicator) workingLabel() string {
	if s.message != "" {
		return s.message
	}
	return "Working"
}

func (s *StatusIndicator) themeMessage(message string) string {
	return s.theme.Fg("muted", message)
}

func (s *StatusIndicator) spinner(token string) string {
	frame := ""
	if len(s.frames) > 0 {
		frame = s.frames[s.frame%len(s.frames)]
	}
	return s.theme.Fg(tui.ThemeColor(token), frame)
}

type UsageSummary struct {
	Input      int64
	Output     int64
	CacheRead  int64
	CacheWrite int64
	Cost       float64
}

func FormatTokens(count int64) string {
	if count < 0 {
		count = 0
	}
	switch {
	case count < 1000:
		return strconv.FormatInt(count, 10)
	case count < 10000:
		return fmt.Sprintf("%.1fk", float64(count)/1000)
	case count < 1000000:
		return strconv.FormatInt(int64(math.Round(float64(count)/1000)), 10) + "k"
	case count < 10000000:
		return fmt.Sprintf("%.1fM", float64(count)/1000000)
	default:
		return strconv.FormatInt(int64(math.Round(float64(count)/1000000)), 10) + "M"
	}
}

func FormatWorkspace(workspace, home string) string {
	if home == "" || workspace == "" {
		return workspace
	}
	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return workspace
	}
	absHome, err := filepath.Abs(home)
	if err != nil {
		return workspace
	}
	relative, err := filepath.Rel(absHome, absWorkspace)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return workspace
	}
	if relative == "." {
		return "~"
	}
	return "~" + string(filepath.Separator) + relative
}

type keyHintEntry struct {
	action string
	label  string
}

var footerKeyHints = []keyHintEntry{
	{action: "app.tools.expand", label: "tools"},
	{action: "app.thinking.cycle", label: "thinking"},
	{action: "app.model.select", label: "model"},
}

type Footer struct {
	mu            sync.Mutex
	theme         *tui.Theme
	keybindings   *tui.KeybindingsManager
	model         string
	thinkingLevel string
	workspace     string
	home          string
	sessionName   string
	usage         UsageSummary
	queuedSource  func() int
	statuses      map[string]string
}

func NewFooter(theme *tui.Theme, keybindings *tui.KeybindingsManager, home string) *Footer {
	return &Footer{
		theme:         theme,
		keybindings:   keybindings,
		thinkingLevel: "off",
		home:          home,
		statuses:      map[string]string{},
	}
}

func (f *Footer) SetModel(model string) {
	f.mu.Lock()
	f.model = SanitizeSingleLine(model)
	f.mu.Unlock()
}

func (f *Footer) SetThinkingLevel(level string) {
	f.mu.Lock()
	if level == "" {
		level = "off"
	}
	f.thinkingLevel = SanitizeSingleLine(level)
	f.mu.Unlock()
}

func (f *Footer) ThinkingLevel() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.thinkingLevel
}

func (f *Footer) SetWorkspace(workspace string) {
	f.mu.Lock()
	f.workspace = SanitizeSingleLine(workspace)
	f.mu.Unlock()
}

func (f *Footer) SetSessionName(name string) {
	f.mu.Lock()
	f.sessionName = SanitizeSingleLine(name)
	f.mu.Unlock()
}

func (f *Footer) SetUsage(usage UsageSummary) {
	f.mu.Lock()
	f.usage = usage
	f.mu.Unlock()
}

func (f *Footer) SetQueuedSource(source func() int) {
	f.mu.Lock()
	f.queuedSource = source
	f.mu.Unlock()
}

func (f *Footer) SetStatus(key, text string) {
	f.mu.Lock()
	f.statuses[SanitizeSingleLine(key)] = SanitizeSingleLine(text)
	f.mu.Unlock()
}

func (f *Footer) ClearStatus(key string) {
	f.mu.Lock()
	delete(f.statuses, SanitizeSingleLine(key))
	f.mu.Unlock()
}

func (f *Footer) Invalidate() {}

func (f *Footer) keyDisplay(action string) string {
	if f.keybindings == nil {
		return ""
	}
	keys := f.keybindings.Keys(action)
	return SanitizeSingleLine(strings.Join(keys, "/"))
}

func (f *Footer) hintLine() string {
	parts := make([]string, 0, len(footerKeyHints))
	for _, hint := range footerKeyHints {
		key := f.keyDisplay(hint.action)
		if key == "" {
			continue
		}
		parts = append(parts, key+" "+hint.label)
	}
	return strings.Join(parts, " · ")
}

func (f *Footer) Render(width int) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if width <= 0 {
		return nil
	}
	lines := []string{f.workspaceLine(width), f.statsLine(width)}
	hints := f.hintLine()
	if hints != "" {
		lines = append(lines, f.theme.Fg("dim", tui.TruncateToWidth(hints, width, "...", false)))
	}
	if statuses := f.statusLine(width); statuses != "" {
		lines = append(lines, tui.TruncateToWidth(statuses, width, f.theme.Fg("dim", "..."), false))
	}
	return lines
}

func (f *Footer) workspaceLine(width int) string {
	display := FormatWorkspace(f.workspace, f.home)
	segments := []string{display}
	if f.sessionName != "" {
		segments = append(segments, f.sessionName)
	}
	if f.queuedSource != nil {
		if queued := f.queuedSource(); queued > 0 {
			segments = append(segments, itoa(queued)+" queued")
		}
	}
	line := strings.Join(segments, " • ")
	return f.theme.Fg("dim", tui.TruncateToWidth(line, width, f.theme.Fg("dim", "..."), false))
}

func (f *Footer) statsLine(width int) string {
	parts := make([]string, 0, 6)
	if f.usage.Input > 0 {
		parts = append(parts, "↑"+FormatTokens(f.usage.Input))
	}
	if f.usage.Output > 0 {
		parts = append(parts, "↓"+FormatTokens(f.usage.Output))
	}
	if f.usage.CacheRead > 0 {
		parts = append(parts, "R"+FormatTokens(f.usage.CacheRead))
	}
	if f.usage.CacheWrite > 0 {
		parts = append(parts, "W"+FormatTokens(f.usage.CacheWrite))
	}
	if f.usage.Cost > 0 {
		parts = append(parts, fmt.Sprintf("$%.3f", f.usage.Cost))
	}
	statsLeft := strings.Join(parts, " ")
	right := f.model
	if right == "" {
		right = "no-model"
	}
	right += " • " + f.thinkingLevel
	statsLeft = tui.TruncateToWidth(statsLeft, width, "...", false)
	leftWidth := tui.VisibleWidth(statsLeft)
	minPadding := 2
	line := ""
	if rightWidth := tui.VisibleWidth(right); leftWidth+minPadding+rightWidth <= width {
		padding := strings.Repeat(" ", width-leftWidth-rightWidth)
		line = statsLeft + padding + right
	} else if available := width - leftWidth - minPadding; available > 0 {
		truncated := tui.TruncateToWidth(right, available, "", false)
		padding := strings.Repeat(" ", maxInt(0, width-leftWidth-tui.VisibleWidth(truncated)))
		line = statsLeft + padding + truncated
	} else {
		line = statsLeft
	}
	return f.theme.Fg("dim", line)
}

func (f *Footer) statusLine(width int) string {
	if len(f.statuses) == 0 {
		return ""
	}
	keys := make([]string, 0, len(f.statuses))
	for key := range f.statuses {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, f.statuses[key])
	}
	line := strings.Join(parts, " ")
	if tui.VisibleWidth(line) > width {
		return tui.TruncateToWidth(line, width, f.theme.Fg("dim", "..."), false)
	}
	return line
}

type WidgetPanel struct {
	mu      sync.Mutex
	widgets map[string][]string
}

func NewWidgetPanel() *WidgetPanel {
	return &WidgetPanel{widgets: map[string][]string{}}
}

func (w *WidgetPanel) SetWidget(key string, content []string) {
	w.mu.Lock()
	cleaned := make([]string, 0, len(content))
	for _, line := range content {
		cleaned = append(cleaned, SanitizeSingleLine(line))
	}
	w.widgets[SanitizeSingleLine(key)] = cleaned
	w.mu.Unlock()
}

func (w *WidgetPanel) ClearWidget(key string) {
	w.mu.Lock()
	delete(w.widgets, SanitizeSingleLine(key))
	w.mu.Unlock()
}

func (w *WidgetPanel) HasWidget(key string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, ok := w.widgets[key]
	return ok
}

func (w *WidgetPanel) Invalidate() {}

func (w *WidgetPanel) Render(width int) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.widgets) == 0 || width <= 0 {
		return nil
	}
	keys := make([]string, 0, len(w.widgets))
	for key := range w.widgets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var lines []string
	for _, key := range keys {
		for _, line := range w.widgets[key] {
			lines = append(lines, padLineToWidth(tui.TruncateToWidth(line, width, "...", false), width))
		}
	}
	return lines
}
