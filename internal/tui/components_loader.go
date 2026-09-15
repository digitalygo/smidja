package tui

import (
	"context"
	"sync"
	"time"
)

var defaultLoaderFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const defaultLoaderInterval = 80 * time.Millisecond

type LoaderIndicator struct {
	Frames     []string
	IntervalMs int
}

type Loader struct {
	*Text

	mu                sync.Mutex
	frames            []string
	interval          time.Duration
	currentFrame      int
	verbatimIndicator bool
	spinnerColor      func(string) string
	messageColor      func(string) string
	message           string
	tui               TUIController
	timer             *time.Timer
	stopped           bool
}

type TUIController interface {
	RequestRender(force bool)
}

func NewLoader(tui TUIController, spinnerColor, messageColor func(string) string, message string, indicator *LoaderIndicator) *Loader {
	loader := &Loader{
		Text:         NewText("", 1, 0, nil),
		spinnerColor: spinnerColor,
		messageColor: messageColor,
		message:      message,
		tui:          tui,
	}
	loader.SetIndicator(indicator)
	return loader
}

func (l *Loader) Render(width int) []string {
	return append([]string{""}, l.Text.Render(width)...)
}

func (l *Loader) Start() {
	l.mu.Lock()
	l.stopped = false
	l.updateDisplayLocked()
	l.restartAnimationLocked()
	l.mu.Unlock()
}

func (l *Loader) Stop() {
	l.mu.Lock()
	l.stopped = true
	if l.timer != nil {
		l.timer.Stop()
		l.timer = nil
	}
	l.mu.Unlock()
}

func (l *Loader) SetMessage(message string) {
	l.mu.Lock()
	l.message = message
	l.updateDisplayLocked()
	l.mu.Unlock()
}

func (l *Loader) Invalidate() {
	l.Text.Invalidate()
	l.mu.Lock()
	l.updateDisplayLocked()
	l.mu.Unlock()
}

func (l *Loader) SetIndicator(indicator *LoaderIndicator) {
	l.mu.Lock()
	l.verbatimIndicator = indicator != nil
	if indicator != nil && indicator.Frames != nil {
		l.frames = append([]string(nil), indicator.Frames...)
	} else {
		l.frames = append([]string(nil), defaultLoaderFrames...)
	}
	interval := defaultLoaderInterval
	if indicator != nil && indicator.IntervalMs > 0 {
		interval = time.Duration(indicator.IntervalMs) * time.Millisecond
	}
	l.interval = interval
	l.currentFrame = 0
	l.updateDisplayLocked()
	l.restartAnimationLocked()
	l.mu.Unlock()
}

func (l *Loader) restartAnimationLocked() {
	if l.timer != nil {
		l.timer.Stop()
		l.timer = nil
	}
	if l.stopped || len(l.frames) <= 1 {
		return
	}
	interval := l.interval
	l.timer = time.AfterFunc(interval, l.advanceFrame)
}

func (l *Loader) advanceFrame() {
	l.mu.Lock()
	if l.stopped {
		l.mu.Unlock()
		return
	}
	if len(l.frames) > 0 {
		l.currentFrame = (l.currentFrame + 1) % len(l.frames)
	}
	l.updateDisplayLocked()
	timer := time.AfterFunc(l.interval, l.advanceFrame)
	l.timer = timer
	l.mu.Unlock()
}

func (l *Loader) renderedIndicatorLocked() string {
	frame := ""
	if len(l.frames) > 0 {
		frame = l.frames[l.currentFrame%len(l.frames)]
	}
	if l.verbatimIndicator {
		return frame
	}
	if l.spinnerColor != nil {
		return l.spinnerColor(frame)
	}
	return frame
}

func (l *Loader) updateDisplayLocked() {
	indicator := l.renderedIndicatorLocked()
	if indicator != "" {
		indicator += " "
	}
	message := l.message
	if l.messageColor != nil {
		message = l.messageColor(message)
	}
	l.Text.SetText(indicator + message)
	if l.tui != nil {
		l.tui.RequestRender(false)
	}
}

type CancellableLoader struct {
	*Loader

	mu      sync.Mutex
	cancel  context.CancelFunc
	ctx     context.Context
	onAbort func()
}

func NewCancellableLoader(tui TUIController, spinnerColor, messageColor func(string) string, message string) *CancellableLoader {
	ctx, cancel := context.WithCancel(context.Background())
	loader := &CancellableLoader{
		Loader: NewLoader(tui, spinnerColor, messageColor, message, nil),
		cancel: cancel,
		ctx:    ctx,
	}
	return loader
}

func (l *CancellableLoader) Context() context.Context { return l.ctx }

func (l *CancellableLoader) Aborted() bool {
	return l.ctx.Err() != nil
}

func (l *CancellableLoader) SetOnAbort(callback func()) {
	l.mu.Lock()
	l.onAbort = callback
	l.mu.Unlock()
}

func (l *CancellableLoader) HandleInput(data string) {
	keybindings := GlobalKeybindings()
	if keybindings.Matches(data, "tui.select.cancel") {
		l.mu.Lock()
		callback := l.onAbort
		l.mu.Unlock()
		l.cancel()
		if callback != nil {
			callback()
		}
	}
}

func (l *CancellableLoader) WantsKeyRelease() bool { return false }

func (l *CancellableLoader) Dispose() {
	l.Stop()
	l.cancel()
}
