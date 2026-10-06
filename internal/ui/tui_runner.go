package ui

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/digitalygo/smidja/internal/tui"
	"github.com/digitalygo/smidja/internal/tui/interactive"
	"github.com/digitalygo/smidja/sdk"
)

var errRunnerStopped = errors.New("ui: tui runner is stopped")

const graphicsProbeTimeout = 150 * time.Millisecond

var (
	notifySignals = signal.Notify
	stopSignals   = signal.Stop
)

type documentComponent struct {
	surface *interactive.Surface
}

func (d *documentComponent) Render(width int) []string {
	return d.surface.RenderDocument(width)
}

func (d *documentComponent) RenderRich(width int) tui.RichRender {
	return d.surface.RenderRichDocument(width)
}

func (d *documentComponent) Invalidate() {
	d.surface.Root().Invalidate()
}

type resizeNotifyingTerminal struct {
	tui.Terminal
	onResize func()
}

func (t *resizeNotifyingTerminal) Start(onInput func(string), onResize func()) error {
	return t.Terminal.Start(onInput, func() {
		if t.onResize != nil {
			t.onResize()
		}
		if onResize != nil {
			onResize()
		}
	})
}

func runEditorCommand(command, filePath string, stdin io.Reader, stdout io.Writer) error {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return errors.New("ui: external editor command is empty")
	}
	cmd := exec.Command(fields[0], append(fields[1:], filePath)...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stdout
	return cmd.Run()
}

type screen interface {
	tui.Component
	Start() error
	Stop(tui.StopOptions)
	SetFocus(tui.Component)
	FocusedComponent() tui.Component
	ShowOverlay(tui.Component, tui.OverlayOptions) tui.OverlayHandle
	HideOverlay()
	HasOverlay() bool
	SetModalCapture(bool)
	ModalCapture() bool
	AddInputListener(tui.InputListener) func()
	RequestRender(bool)
	RenderNow(bool)
	Terminal() tui.Terminal
	SuspendScreen()
	ResumeScreen()
}

type rawSuspender interface {
	SuspendRaw() error
	ResumeRaw() error
}

var _ rawSuspender = (*tui.ProcessTerminal)(nil)

type RunnerOptions struct {
	Stdin  io.Reader
	Stdout io.Writer

	Mode  TUIMode
	Title string

	Home          string
	WorkspaceRoot string
	ProjectPath   string

	ExternalCommand string
	ExternalRunner  tui.ExternalRunner

	NewTerminal func(stdin io.Reader, stdout io.Writer) tui.Terminal

	Env           func(string) string
	ImagesEnabled bool
	LinkOpener    tui.LinkOpener

	OnSubmit    func(string)
	OnInterrupt func()
}

type Runner struct {
	terminal tui.Terminal
	view     screen
	keys     *tui.KeybindingsManager
	surface  *interactive.Surface
	themes   *tui.ThemeRegistry
	dialogs  *modalService
	title    string
	stdin    io.Reader
	stdout   io.Writer
	mode     TUIMode

	lifecycleCtx    context.Context
	lifecycleCancel context.CancelFunc

	working       atomic.Bool
	started       atomic.Bool
	stopped       atomic.Bool
	editorActive  atomic.Bool
	editorRunning atomic.Bool

	startStopMu sync.Mutex

	stopOnce   sync.Once
	exitOnce   sync.Once
	done       chan struct{}
	exitC      chan struct{}
	signalStop func()

	onSignalsRegistered func()
	loginStartHook      func(*LoginOperation)

	actionsMu      sync.Mutex
	actionsCh      chan func()
	actionsStop    chan struct{}
	actionsStopped bool
	actionsDone    chan struct{}

	mu          sync.Mutex
	onSubmit    func(string)
	onInterrupt func()

	env                     func(string) string
	hyperlinks              bool
	imagesEnabled           bool
	imageLoader             *tui.ImageLoader
	graphics                tui.GraphicsCapability
	graphicsTracker         *tui.GraphicsReplyTracker
	linkOpener              tui.LinkOpener
	graphicsProber          tui.GraphicsProber
	graphicsProbeProtocol   tui.GraphicsProtocol
	graphicsProbeGeneration uint64
}

var _ tui.TUIController = (*Runner)(nil)

var _ sdk.UI = (*Runner)(nil)

func NewRunner(opts RunnerOptions) *Runner {
	stdin := opts.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	stdout := opts.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	newTerminal := opts.NewTerminal
	if newTerminal == nil {
		newTerminal = func(in io.Reader, out io.Writer) tui.Terminal {
			return tui.NewProcessTerminal(in, out)
		}
	}
	terminal := newTerminal(stdin, stdout)
	var graphicsProber tui.GraphicsProber
	if prober, ok := terminal.(tui.GraphicsProber); ok {
		graphicsProber = prober
	}
	keys := tui.NewDefaultKeybindingsManager(nil)
	themes := tui.NewThemeRegistry("", "", tui.ColorModeUnset)
	theme := themes.Active()
	if loaded, err := themes.SetTheme("dark"); err == nil {
		theme = loaded
	}
	externalRunner := opts.ExternalRunner
	if externalRunner == nil {
		externalRunner = tui.RunnerFunc(func(command, filePath string) error {
			return runEditorCommand(command, filePath, stdin, stdout)
		})
	}
	editor := tui.NewEditor(tui.EditorOptions{
		Theme:           theme,
		WorkspaceRoot:   opts.WorkspaceRoot,
		SmidjaHome:      opts.Home,
		ProjectPath:     opts.ProjectPath,
		ExternalCommand: opts.ExternalCommand,
		ExternalRunner:  externalRunner,
		TerminalRows:    24,
	})
	surface := interactive.NewSurface(interactive.SurfaceOptions{
		Theme:       theme,
		Editor:      editor,
		Keybindings: keys,
		Home:        opts.Home,
		Hyperlinks:  true,
	})
	title := opts.Title
	if strings.TrimSpace(title) == "" {
		title = "smidja"
	}
	lifecycleCtx, lifecycleCancel := context.WithCancel(context.Background())
	env := opts.Env
	if env == nil {
		env = os.Getenv
	}
	linkOpener := opts.LinkOpener
	if linkOpener == nil {
		linkOpener = &tui.CommandLinkOpener{}
	}
	runner := &Runner{
		terminal:        terminal,
		keys:            keys,
		surface:         surface,
		themes:          themes,
		title:           title,
		done:            make(chan struct{}),
		exitC:           make(chan struct{}),
		onSubmit:        opts.OnSubmit,
		onInterrupt:     opts.OnInterrupt,
		stdin:           stdin,
		stdout:          stdout,
		mode:            opts.Mode,
		lifecycleCtx:    lifecycleCtx,
		lifecycleCancel: lifecycleCancel,
		env:             env,
		hyperlinks:      true,
		imagesEnabled:   opts.ImagesEnabled,
		graphicsTracker: tui.NewGraphicsReplyTracker(0),
		linkOpener:      linkOpener,
		imageLoader:     tui.NewImageLoader(opts.WorkspaceRoot, opts.ImagesEnabled),
		graphicsProber:  graphicsProber,
	}
	interactive.SetDefaultImageResolver(runner.resolveImage)
	rawTerminal := terminal
	wrapped := &resizeNotifyingTerminal{Terminal: rawTerminal, onResize: func() { editor.SetTerminalRows(rawTerminal.Rows()) }}
	terminal = wrapped
	runner.terminal = wrapped
	var view screen
	if opts.Mode.Fullscreen() {
		alt := tui.NewAltScreen(terminal, true, tui.AltScreenOptions{})
		alt.SetKeybindings(keys)
		alt.SetLayoutRoot(surface.Root())
		view = alt
	} else {
		main := tui.NewMainScreen(terminal, true)
		main.AddChild(&documentComponent{surface: surface})
		view = main
	}
	runner.view = view
	runner.dialogs = newModalService(view)
	runner.configureScreen(view, theme)
	surface.SetController(runner)
	surface.SetOnSubmit(runner.handleSubmit)
	view.SetFocus(editor)
	view.AddInputListener(runner.handleInput)
	terminal.OnEOF(runner.RequestExit)
	return runner
}

func (r *Runner) ThemeRegistry() *tui.ThemeRegistry { return r.themes }

func (r *Runner) configureScreen(view screen, theme *tui.Theme) {
	switch typed := view.(type) {
	case *tui.AltScreen:
		typed.SetLinkOpener(r.linkOpener)
		typed.SetImageBytesProvider(r.loadImage)
		if theme != nil {
			typed.SetTheme(theme)
		}
	case *tui.MainScreen:
		typed.SetImageBytesProvider(r.loadImage)
	}
	if r.imageLoader != nil {
		r.imageLoader.SetOnEvict(func(cacheKey string) {
			switch typed := view.(type) {
			case *tui.AltScreen:
				if sequence := typed.ImagePlacements().FreeCacheKey(cacheKey); sequence != "" {
					typed.Terminal().Write(sequence)
				}
			case *tui.MainScreen:
				if sequence := typed.ImagePlacements().FreeCacheKey(cacheKey); sequence != "" {
					typed.Terminal().Write(sequence)
				}
			}
		})
	}
}

func (r *Runner) loadImage(source, cacheKey string) (*tui.LoadedImage, bool) {
	r.mu.Lock()
	loader := r.imageLoader
	enabled := r.imagesEnabled
	r.mu.Unlock()
	if !enabled || loader == nil {
		return nil, false
	}
	loaded, err := loader.Load(source)
	if err != nil {
		return nil, false
	}
	return loaded, true
}

func (r *Runner) applyGraphics(view screen) {
	r.mu.Lock()
	enabled := r.imagesEnabled
	loader := r.imageLoader
	started := r.started.Load()
	prober := r.graphicsProber
	probeGeneration := r.graphicsProbeGeneration
	probeProtocol := r.graphicsProbeProtocol
	r.mu.Unlock()
	detected := tui.DetectGraphicsProtocol(r.env)
	protocol := detected
	if detected == tui.GraphicsKitty && started && prober != nil {
		if probeGeneration == 0 {
			ok := prober.QueryKittyGraphics(graphicsProbeTimeout)
			r.mu.Lock()
			if r.graphicsProbeGeneration == 0 {
				r.graphicsProbeGeneration = 1
				if ok {
					r.graphicsProbeProtocol = tui.GraphicsKitty
				} else {
					r.graphicsProbeProtocol = tui.GraphicsNone
				}
			}
			probeProtocol = r.graphicsProbeProtocol
			r.mu.Unlock()
		}
		protocol = probeProtocol
	}
	capability := tui.GraphicsCapability{
		Protocol: protocol,
		Enabled:  enabled && loader != nil && loader.Enabled(),
	}
	r.mu.Lock()
	r.graphics = capability
	r.mu.Unlock()
	switch typed := view.(type) {
	case *tui.AltScreen:
		typed.SetGraphics(capability)
	case *tui.MainScreen:
		typed.SetGraphics(capability)
	}
}

func (r *Runner) SetImagesEnabled(enabled bool) {
	r.mu.Lock()
	r.imagesEnabled = enabled
	loader := r.imageLoader
	r.mu.Unlock()
	if loader != nil {
		loader.SetEnabled(enabled)
	}
	r.applyGraphics(r.view)
	r.view.Invalidate()
	r.view.RequestRender(true)
}

func (r *Runner) resolveImage(source, alt string, maxColumns int) (tui.ResolvedImage, bool) {
	trimmed := strings.TrimSpace(source)
	if trimmed == "" {
		return tui.ResolvedImage{}, false
	}
	r.mu.Lock()
	loader := r.imageLoader
	enabled := r.imagesEnabled
	capability := r.graphics
	r.mu.Unlock()
	if !enabled || loader == nil || capability.Protocol == tui.GraphicsNone {
		return tui.ResolvedImage{}, false
	}
	if capability.Protocol == tui.GraphicsITerm2 && r.mode.Fullscreen() {
		return tui.ResolvedImage{}, false
	}
	loaded, err := loader.Load(trimmed)
	if err != nil {
		return tui.ResolvedImage{}, false
	}
	if loaded.WebPPassthrough && capability.Protocol == tui.GraphicsKitty {
		return tui.ResolvedImage{}, false
	}
	columns := maxColumns
	if columns > 40 {
		columns = 40
	}
	if columns < 1 {
		columns = 1
	}
	denominator := loaded.Width
	if denominator < 1 {
		denominator = 1
	}
	rows := columns * loaded.Height / denominator / 2
	if rows < 1 {
		rows = 1
	}
	if rows > 20 {
		rows = 20
	}
	cacheKey, err := loader.ResolvePath(trimmed)
	if err != nil {
		return tui.ResolvedImage{}, false
	}
	return tui.ResolvedImage{
		CacheKey: cacheKey,
		Columns:  columns,
		Rows:     rows,
		Label:    "[image: " + interactive.SanitizeSingleLine(alt) + "]",
		Protocol: capability.Protocol,
	}, true
}

func (r *Runner) ImagesEnabled() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.imagesEnabled
}

func (r *Runner) GraphicsCapability() tui.GraphicsCapability {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.graphics
}

func (r *Runner) Surface() *interactive.Surface { return r.surface }

func (r *Runner) Terminal() tui.Terminal {
	if wrapped, ok := r.terminal.(*resizeNotifyingTerminal); ok {
		return wrapped.Terminal
	}
	return r.terminal
}

func (r *Runner) Active() bool { return r.started.Load() && !r.stopped.Load() }

func (r *Runner) Done() <-chan struct{} { return r.exitC }

func (r *Runner) RequestExit() {
	r.exitOnce.Do(func() {
		r.CloseSearch()
		r.cancelDialogs()
		r.endActions()
		close(r.exitC)
	})
}

func (r *Runner) CloseSearch() {
	if alt, ok := r.view.(*tui.AltScreen); ok {
		alt.CloseSearch()
	}
}

func (r *Runner) cancelDialogs() {
	if r.dialogs != nil {
		r.dialogs.shutdown()
	}
}

func (r *Runner) CancelDialogs() { r.cancelDialogs() }

func (r *Runner) SetOnSubmit(fn func(string)) {
	r.mu.Lock()
	r.onSubmit = fn
	r.mu.Unlock()
}

func (r *Runner) SetOnInterrupt(fn func()) {
	r.mu.Lock()
	r.onInterrupt = fn
	r.mu.Unlock()
}

func (r *Runner) Start() error {
	r.startStopMu.Lock()
	defer r.startStopMu.Unlock()
	if r.stopped.Load() {
		return errRunnerStopped
	}
	if r.started.Load() {
		return errors.New("ui: tui runner already started")
	}
	signals := make(chan os.Signal, 2)
	notifySignals(signals, os.Interrupt, syscall.SIGTERM)
	r.mu.Lock()
	r.signalStop = func() { stopSignals(signals) }
	r.mu.Unlock()
	go r.signalLoop(signals)

	if r.onSignalsRegistered != nil {
		r.onSignalsRegistered()
	}

	r.beginActions()
	if err := r.view.Start(); err != nil {
		r.endActions()
		r.view.Stop(tui.StopOptions{})
		r.unregisterSignals()
		r.RequestExit()
		return err
	}
	r.started.Store(true)
	r.applyGraphics(r.view)
	r.surface.Editor().SetTerminalRows(r.terminal.Rows())
	r.terminal.SetTitle(r.title)
	return nil
}

func (r *Runner) unregisterSignals() {
	r.mu.Lock()
	stop := r.signalStop
	r.signalStop = nil
	r.mu.Unlock()
	if stop != nil {
		stop()
	}
}

func (r *Runner) beginActions() {
	r.actionsMu.Lock()
	defer r.actionsMu.Unlock()
	select {
	case <-r.exitC:
		return
	default:
	}
	if r.actionsStopped || r.actionsCh != nil {
		return
	}
	r.actionsCh = make(chan func(), 1)
	r.actionsStop = make(chan struct{})
	r.actionsDone = make(chan struct{})
	ch := r.actionsCh
	stop := r.actionsStop
	done := r.actionsDone
	go func() {
		defer close(done)
		for {
			select {
			case action := <-ch:
				if action == nil || !r.admitAction() {
					continue
				}
				action()
			default:
				select {
				case <-stop:
					return
				case action := <-ch:
					if action == nil || !r.admitAction() {
						continue
					}
					action()
				}
			}
		}
	}()
}

func (r *Runner) admitAction() bool {
	r.actionsMu.Lock()
	defer r.actionsMu.Unlock()
	return !r.actionsStopped
}

func (r *Runner) enqueueAction(action func()) bool {
	r.actionsMu.Lock()
	defer r.actionsMu.Unlock()
	if r.actionsStopped || r.actionsCh == nil {
		return false
	}
	select {
	case r.actionsCh <- action:
		return true
	default:
		return false
	}
}

func (r *Runner) endActions() {
	r.actionsMu.Lock()
	defer r.actionsMu.Unlock()
	if r.actionsStopped {
		return
	}
	r.actionsStopped = true
	if r.actionsStop != nil {
		close(r.actionsStop)
		r.actionsStop = nil
	}
}

func (r *Runner) joinActions() {
	r.actionsMu.Lock()
	stopping, done := r.actionsStopped, r.actionsDone
	r.actionsMu.Unlock()
	if stopping && done != nil {
		<-done
	}
}

func (r *Runner) signalLoop(signals <-chan os.Signal) {
	select {
	case <-signals:
		r.RequestExit()
	case <-r.exitC:
	}
}

func (r *Runner) Stop() {
	r.stopOnce.Do(func() {
		r.startStopMu.Lock()
		defer r.startStopMu.Unlock()
		r.stopped.Store(true)
		r.cancelDialogs()
		r.endActions()
		r.joinActions()
		r.flushFinalDocument()
		r.view.Stop(tui.StopOptions{})
		r.unregisterSignals()
		r.surface.Close()
		r.RequestExit()
		r.lifecycleCancel()
		close(r.done)
	})
}

func (r *Runner) FlushFinalDocument() {
	if !r.started.Load() || r.stopped.Load() {
		return
	}
	r.flushFinalDocument()
}

func (r *Runner) flushFinalDocument() {
	if r.mode.Fullscreen() {
		return
	}
	r.view.RenderNow(false)
}

func (r *Runner) RequestRender(force bool) {
	if !r.Active() {
		return
	}
	r.view.RequestRender(force)
}

func (r *Runner) Working() bool { return r.working.Load() }

func (r *Runner) SetWorking(working bool) {
	r.working.Store(working)
	r.surface.SetWorking(working)
	r.surface.Editor().SetDisableSubmit(working)
}

func (r *Runner) Interrupt() {
	if !r.working.Load() {
		return
	}
	r.mu.Lock()
	callback := r.onInterrupt
	r.mu.Unlock()
	if callback != nil {
		callback()
	}
}

func (r *Runner) handleSubmit(text string) {
	r.mu.Lock()
	callback := r.onSubmit
	r.mu.Unlock()
	if callback != nil {
		callback(text)
	}
}

func (r *Runner) handleInput(data string) tui.InputListenerResult {
	if tui.IsKeyRelease(data) {
		return tui.InputListenerResult{}
	}
	if r.graphicsTracker != nil && r.graphicsTracker.Observe(data) {
		return tui.InputListenerResult{Consume: true}
	}
	if r.editorActive.Load() {
		return tui.InputListenerResult{Consume: true}
	}
	if r.keys.Matches(data, "app.editor.external") {
		r.requestExternalEditor()
		return tui.InputListenerResult{Consume: true}
	}
	if r.keys.Matches(data, "app.exit") {
		if strings.TrimSpace(r.surface.Editor().Text()) == "" {
			r.RequestExit()
			return tui.InputListenerResult{Consume: true}
		}
		return tui.InputListenerResult{}
	}
	if r.keys.Matches(data, "app.interrupt") && r.working.Load() {
		r.Interrupt()
		return tui.InputListenerResult{Consume: true}
	}
	if r.surface.HandleActionKey(data) {
		return tui.InputListenerResult{Consume: true}
	}
	return tui.InputListenerResult{}
}

func (r *Runner) requestExternalEditor() {
	if !r.Active() {
		return
	}
	if !r.editorActive.CompareAndSwap(false, true) {
		return
	}
	if r.enqueueAction(func() { r.runExternalEditor() }) {
		return
	}
	r.editorActive.Store(false)
}

func (r *Runner) runExternalEditor() {
	defer r.editorActive.Store(false)
	if !r.editorRunning.CompareAndSwap(false, true) {
		return
	}
	defer r.editorRunning.Store(false)
	if !r.Active() {
		return
	}

	if err := r.suspendForEditor(); err != nil {
		if !r.stopped.Load() {
			r.surface.AddNotice(interactive.NoticeWarning, err.Error())
		}
		return
	}
	var editErr error
	if !r.stopped.Load() {
		editErr = r.surface.Editor().OpenExternalEditor()
	}
	resumeErr := r.resumeAfterEditor()
	if resumeErr != nil {
		r.view.ResumeScreen()
	}
	if r.stopped.Load() {
		if resumeErr != nil {
			r.surface.AddNotice(interactive.NoticeError, resumeErr.Error())
		}
		return
	}
	if editErr != nil {
		r.surface.AddNotice(interactive.NoticeWarning, editErr.Error())
	}
	if resumeErr != nil {
		r.surface.AddNotice(interactive.NoticeError, resumeErr.Error())
		r.view.RenderNow(true)
		r.RequestExit()
	}
}

func (r *Runner) suspendForEditor() error {
	r.view.SuspendScreen()
	if err := r.terminal.SuspendRaw(); err != nil {
		r.view.ResumeScreen()
		return err
	}
	return nil
}

func (r *Runner) resumeAfterEditor() error {
	if err := r.terminal.ResumeRaw(); err != nil {
		return err
	}
	r.view.ResumeScreen()
	return nil
}

func (r *Runner) Notify(message string, kind sdk.NotifyKind) {
	if !r.Active() {
		return
	}
	notice := interactive.NoticeInfo
	switch kind {
	case sdk.NotifyWarning:
		notice = interactive.NoticeWarning
	case sdk.NotifyError:
		notice = interactive.NoticeError
	}
	r.surface.AddNotice(notice, message)
}

func (r *Runner) SetStatus(key, text string) {
	if !r.Active() {
		return
	}
	if text == "" {
		r.surface.ClearStatus(key)
		return
	}
	r.surface.SetStatus(key, text)
}

func (r *Runner) SetWidget(key string, content []string) {
	if !r.Active() {
		return
	}
	if content == nil {
		r.surface.ClearWidget(key)
		return
	}
	r.surface.SetWidget(key, content)
}

func (r *Runner) SetWorkingMessage(message string) {
	if !r.Active() {
		return
	}
	r.surface.SetWorkingMessage(message)
}

func (r *Runner) SetTitle(title string) {
	r.mu.Lock()
	r.title = title
	r.mu.Unlock()
	if r.Active() {
		r.terminal.SetTitle(title)
	}
}
