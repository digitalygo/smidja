package tui

import (
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"
)

var ErrNotATerminal = errors.New("tui: stdin or stdout is not a terminal")

type Terminal interface {
	Start(onInput func(string), onResize func()) error
	Stop()
	Write(data string)
	Columns() int
	Rows() int
	KittyProtocolActive() bool
	ModifyOtherKeysActive() bool
	MoveBy(lines int)
	HideCursor()
	ShowCursor()
	ClearLine()
	ClearFromCursor()
	ClearScreen()
	SetTitle(title string)
	DrainInput(maxMs, idleMs int)
	OnEOF(func())
}

type rawState struct {
	value any
}

var lastInputTime atomic.Int64

type terminalOps interface {
	name() string
	isTerminal(file *os.File) bool
	makeRaw(file *os.File) (*rawState, error)
	restoreState(file *os.File, state *rawState) error
	windowSize(file *os.File) (cols, rows int, err error)
}

var (
	defaultTerminalOpsMu  sync.RWMutex
	defaultTerminalOpsRef terminalOps
)

func setDefaultTerminalOps(ops terminalOps) {
	defaultTerminalOpsMu.Lock()
	defaultTerminalOpsRef = ops
	defaultTerminalOpsMu.Unlock()
}

type noTerminalOps struct{}

func (noTerminalOps) name() string { return "none" }

func (noTerminalOps) isTerminal(*os.File) bool { return false }

func (noTerminalOps) makeRaw(*os.File) (*rawState, error) {
	return nil, errUnsupportedPlatform
}

func (noTerminalOps) restoreState(*os.File, *rawState) error {
	return errUnsupportedPlatform
}

func (noTerminalOps) windowSize(*os.File) (int, int, error) {
	return 0, 0, errUnsupportedPlatform
}

var errUnsupportedPlatform = errors.New("tui: no terminal operations available")

func currentTerminalOps() terminalOps {
	defaultTerminalOpsMu.RLock()
	ops := defaultTerminalOpsRef
	defaultTerminalOpsMu.RUnlock()
	if ops != nil {
		return ops
	}
	return noTerminalOps{}
}

const (
	defaultColumns = 80
	defaultRows    = 24
	readBufferSize = 4096
)

var kittyQuery = "\x1b[>" + itoa(KittyQueryFlags) + "u\x1b[?u" + CursorDeviceAttrs

type pendingQuery struct {
	matcher func(sequence string) bool
	reply   chan string
}

type ProcessTerminal struct {
	mu sync.Mutex

	stdin  io.Reader
	stdout io.Writer

	stdinFile  *os.File
	stdoutFile *os.File

	ops terminalOps

	running      bool
	stopped      bool
	wasRaw       *rawState
	savedSizeOK  bool
	pasteEnabled bool

	kittyActive      atomic.Bool
	modifyOtherKeys  atomic.Bool
	kittyQueryPushed bool

	escapeTimeoutMs int

	inputHandler  func(string)
	resizeHandler func()
	eofHandler    func()
	eofDelivered  atomic.Bool

	draining atomic.Bool

	buffer      *stdinBuffer
	flushTimer  *time.Timer
	queryWaiter *pendingQuery

	stopReading chan struct{}
	readerDone  chan struct{}

	winchStop chan struct{}
	winchDone chan struct{}

	stopResizeSignals func()

	columns int
	rows    int
}

func NewProcessTerminal(stdin io.Reader, stdout io.Writer) *ProcessTerminal {
	terminal := &ProcessTerminal{
		stdin:           stdin,
		stdout:          stdout,
		ops:             currentTerminalOps(),
		escapeTimeoutMs: resolveEscapeTimeoutMs(os.Getenv),
		stopReading:     make(chan struct{}),
		readerDone:      make(chan struct{}),
		columns:         defaultColumns,
		rows:            defaultRows,
	}
	terminal.buffer = newStdinBuffer(terminal.escapeTimeoutMs)
	terminal.buffer.onSequence = terminal.dispatchSequence
	if file, ok := stdin.(*os.File); ok {
		terminal.stdinFile = file
	}
	if file, ok := stdout.(*os.File); ok {
		terminal.stdoutFile = file
	}
	terminal.refreshSize()
	return terminal
}

func resolveEscapeTimeoutMs(getenv func(string) string) int {
	if configured := getenv("SMIDJA_TUI_ESC_TIMEOUT"); configured != "" {
		if value, valid := parseIntString(configured); valid && value > 0 {
			return value
		}
	}
	if getenv("SSH_CONNECTION") != "" || getenv("SSH_TTY") != "" {
		return sshEscapeTimeout
	}
	return defaultEscapeTimeout
}

func (t *ProcessTerminal) refreshSize() {
	cols, rows, err := 0, 0, errors.New("no file")
	if t.stdoutFile != nil {
		cols, rows, err = t.ops.windowSize(t.stdoutFile)
	}
	if err != nil {
		cols = envSize(os.Getenv, "COLUMNS", defaultColumns)
		rows = envSize(os.Getenv, "LINES", defaultRows)
	}
	t.mu.Lock()
	t.columns = cols
	t.rows = rows
	t.mu.Unlock()
}

func envSize(getenv func(string) string, name string, fallback int) int {
	if value := getenv(name); value != "" {
		if parsed, valid := parseIntString(value); valid && parsed > 0 {
			return parsed
		}
	}
	return fallback
}

func (t *ProcessTerminal) Start(onInput func(string), onResize func()) error {
	t.mu.Lock()
	if t.running {
		t.mu.Unlock()
		return errors.New("tui: terminal already started")
	}
	if t.stdinFile == nil || t.stdoutFile == nil || !t.ops.isTerminal(t.stdinFile) || !t.ops.isTerminal(t.stdoutFile) {
		t.mu.Unlock()
		return ErrNotATerminal
	}
	t.inputHandler = onInput
	t.resizeHandler = onResize
	t.running = true
	t.stopped = false
	t.eofDelivered.Store(false)

	state, err := t.ops.makeRaw(t.stdinFile)
	if err != nil {
		t.inputHandler = nil
		t.resizeHandler = nil
		t.running = false
		t.mu.Unlock()
		return err
	}
	t.wasRaw = state

	t.stopReading = make(chan struct{})
	t.readerDone = make(chan struct{})
	t.winchStop = make(chan struct{})
	t.winchDone = make(chan struct{})
	t.mu.Unlock()

	t.startResizeWatcher()

	t.mu.Lock()
	t.pasteEnabled = true
	t.mu.Unlock()
	t.stdout.Write([]byte(BracketedPasteOn))

	t.refreshSize()
	go t.readLoop()

	t.kittyQueryPushed = true
	t.stdout.Write([]byte(kittyQuery))
	return nil
}

func (t *ProcessTerminal) startResizeWatcher() {
	notify, stop, supported := resizeSignalChannel()
	if !supported {
		t.stopResizeSignals = nil
		close(t.winchDone)
		return
	}
	t.stopResizeSignals = stop
	go func() {
		defer close(t.winchDone)
		for {
			select {
			case <-t.winchStop:
				return
			case <-notify:
				t.refreshSize()
				t.mu.Lock()
				handler := t.resizeHandler
				t.mu.Unlock()
				if handler != nil {
					handler()
				}
			}
		}
	}()
	refreshTerminalDimensions()
}

func (t *ProcessTerminal) readLoop() {
	defer close(t.readerDone)
	defer t.deliverEOF()

	var utf8Hold []byte
	readBuffer := make([]byte, readBufferSize)
	for {
		select {
		case <-t.stopReading:
			return
		default:
		}
		n, err := t.stdin.Read(readBuffer)
		if n > 0 {
			lastInputTime.Store(time.Now().UnixNano())
			chunk := readBuffer[:n]
			if len(utf8Hold) > 0 {
				chunk = append(append([]byte(nil), utf8Hold...), chunk...)
				utf8Hold = utf8Hold[:0]
			}
			if trailing := incompleteUTF8Suffix(chunk); trailing > 0 {
				utf8Hold = append(utf8Hold[:0], chunk[len(chunk)-trailing:]...)
				chunk = chunk[:len(chunk)-trailing]
			}
			t.buffer.process(chunk)
		}
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, os.ErrClosed) {
				return
			}
			if !temporaryReadError(err) {
				return
			}
		}
		if n == 0 && err == nil {
			select {
			case <-t.stopReading:
				return
			case <-time.After(time.Millisecond):
			}
		}
	}
}

func incompleteUTF8Suffix(data []byte) int {
	for i := len(data) - 1; i >= 0 && i >= len(data)-utf8.UTFMax; i-- {
		if data[i] < 0x80 {
			return 0
		}
		if data[i]&0xC0 == 0xC0 {
			expected := utf8SequenceLength(data[i])
			if expected > 0 && len(data)-i < expected {
				return len(data) - i
			}
			return 0
		}
	}
	return 0
}

func utf8SequenceLength(first byte) int {
	switch {
	case first&0xE0 == 0xC0:
		return 2
	case first&0xF0 == 0xE0:
		return 3
	case first&0xF8 == 0xF0:
		return 4
	}
	return 0
}

func temporaryReadError(err error) bool {
	return errors.Is(err, syscall.EINTR) || errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK)
}

func (t *ProcessTerminal) dispatchSequence(sequence string) {
	t.mu.Lock()
	waiter := t.queryWaiter
	if waiter != nil && waiter.matcher(sequence) {
		t.queryWaiter = nil
		t.mu.Unlock()
		waiter.reply <- sequence
		close(waiter.reply)
		return
	}
	t.mu.Unlock()

	if t.consumeNegotiation(sequence) {
		return
	}

	t.mu.Lock()
	handler := t.inputHandler
	t.mu.Unlock()
	if handler != nil {
		handler(sequence)
	}
}

func (t *ProcessTerminal) consumeNegotiation(sequence string) bool {
	if flags, ok := parseKittyFlagsResponse(sequence); ok {
		if flags != 0 {
			t.disableModifyOtherKeys()
			t.kittyActive.Store(true)
			SetKittyProtocolActive(true)
		} else {
			t.kittyActive.Store(false)
			SetKittyProtocolActive(false)
			t.enableModifyOtherKeys()
		}
		return true
	}
	if isDeviceAttributesResponse(sequence) {
		if !t.kittyActive.Load() {
			t.enableModifyOtherKeys()
		}
		return true
	}
	return false
}

func parseKittyFlagsResponse(sequence string) (int, bool) {
	if !strings.HasPrefix(sequence, "\x1b[?") || !strings.HasSuffix(sequence, "u") {
		return 0, false
	}
	body := sequence[3 : len(sequence)-1]
	if body == "" {
		return 0, true
	}
	for i := 0; i < len(body); i++ {
		if body[i] < '0' || body[i] > '9' {
			return 0, false
		}
	}
	value := 0
	for i := 0; i < len(body); i++ {
		value = value*10 + int(body[i]-'0')
	}
	return value, true
}

func isDeviceAttributesResponse(sequence string) bool {
	if !strings.HasPrefix(sequence, "\x1b[?") || !strings.HasSuffix(sequence, "c") {
		return false
	}
	body := sequence[3 : len(sequence)-1]
	if body == "" {
		return true
	}
	for i := 0; i < len(body); i++ {
		if body[i] != ';' && (body[i] < '0' || body[i] > '9') {
			return false
		}
	}
	return true
}

func (t *ProcessTerminal) enableModifyOtherKeys() {
	if t.kittyActive.Load() || t.modifyOtherKeys.Swap(true) {
		return
	}
	t.stdout.Write([]byte(ModifyOtherKeysEnable))
}

func (t *ProcessTerminal) disableModifyOtherKeys() {
	if !t.modifyOtherKeys.Swap(false) {
		return
	}
	t.stdout.Write([]byte(ModifyOtherKeysDisable))
}

func (t *ProcessTerminal) runQuery(query string, matcher func(string) bool, timeout time.Duration) (string, bool) {
	t.mu.Lock()
	if t.queryWaiter != nil {
		t.mu.Unlock()
		return "", false
	}
	waiter := &pendingQuery{matcher: matcher, reply: make(chan string, 1)}
	t.queryWaiter = waiter
	t.mu.Unlock()

	t.stdout.Write([]byte(query))

	select {
	case response := <-waiter.reply:
		return response, true
	case <-time.After(timeout):
		t.mu.Lock()
		if t.queryWaiter == waiter {
			t.queryWaiter = nil
		}
		t.mu.Unlock()
		return "", false
	}
}

func (t *ProcessTerminal) QueryDeviceAttributes(timeout time.Duration) bool {
	response, found := t.runQuery(
		CursorDeviceAttrs,
		isDeviceAttributesResponse,
		timeout,
	)
	return found && response != ""
}

func (t *ProcessTerminal) QueryCursorPosition(timeout time.Duration) (row, col int, ok bool) {
	response, found := t.runQuery(
		CursorReportPos,
		func(sequence string) bool {
			return strings.HasPrefix(sequence, "\x1b[") && strings.HasSuffix(sequence, "R") &&
				strings.Contains(sequence, ";")
		},
		timeout,
	)
	if !found {
		return 0, 0, false
	}
	return parseCursorPosition(response)
}

func parseCursorPosition(response string) (row, col int, ok bool) {
	if !strings.HasPrefix(response, "\x1b[") || !strings.HasSuffix(response, "R") {
		return 0, 0, false
	}
	body := response[2 : len(response)-1]
	separator := strings.LastIndexByte(body, ';')
	if separator < 0 {
		return 0, 0, false
	}
	row, rowOK := parseIntString(body[:separator])
	col, colOK := parseIntString(body[separator+1:])
	if !rowOK || !colOK || row <= 0 || col <= 0 {
		return 0, 0, false
	}
	return row, col, true
}

type RGBColor struct {
	R, G, B int
}

func (t *ProcessTerminal) QueryBackgroundColor(timeout time.Duration) (*RGBColor, bool) {
	response, found := t.runQuery(
		OSCQueryBackground,
		isOSC11Response,
		timeout,
	)
	if !found {
		return nil, false
	}
	return parseOSC11Background(response)
}

func isOSC11Response(sequence string) bool {
	return strings.HasPrefix(sequence, "\x1b]11;") &&
		(strings.HasSuffix(sequence, ANSIBEL) || strings.HasSuffix(sequence, ANSIST))
}

func parseOSC11Background(response string) (*RGBColor, bool) {
	if !strings.HasPrefix(response, "\x1b]11;") {
		return nil, false
	}
	body := response
	body = strings.TrimPrefix(body, "\x1b]11;")
	body = strings.TrimSuffix(body, ANSIST)
	body = strings.TrimSuffix(body, ANSIBEL)
	body = strings.TrimSpace(body)
	if strings.HasPrefix(body, "#") {
		hex := body[1:]
		if len(hex) == 6 {
			r, g, b, err := hexToRGB(body)
			if err != nil {
				return nil, false
			}
			return &RGBColor{R: r, G: g, B: b}, true
		}
		if len(hex) == 12 {
			r := parseOSCHexChannel(hex[0:4])
			g := parseOSCHexChannel(hex[4:8])
			b := parseOSCHexChannel(hex[8:12])
			if r == nil || g == nil || b == nil {
				return nil, false
			}
			return &RGBColor{R: *r, G: *g, B: *b}, true
		}
		return nil, false
	}
	if idx := strings.Index(body, ":"); idx >= 0 {
		body = body[idx+1:]
	}
	parts := strings.Split(body, "/")
	if len(parts) == 4 {
		parts = parts[:3]
	}
	if len(parts) != 3 {
		return nil, false
	}
	channels := make([]*int, 3)
	for i, part := range parts {
		channels[i] = parseOSCHexChannel(part)
		if channels[i] == nil {
			return nil, false
		}
	}
	return &RGBColor{R: *channels[0], G: *channels[1], B: *channels[2]}, true
}

func parseOSCHexChannel(channel string) *int {
	if channel == "" {
		return nil
	}
	for i := 0; i < len(channel); i++ {
		c := channel[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return nil
		}
	}
	maxValue := 1
	for i := 0; i < len(channel); i++ {
		maxValue *= 16
	}
	value := 0
	for i := 0; i < len(channel); i++ {
		c := channel[i]
		var digit int
		switch {
		case c >= '0' && c <= '9':
			digit = int(c - '0')
		case c >= 'a' && c <= 'f':
			digit = int(c-'a') + 10
		default:
			digit = int(c-'A') + 10
		}
		value = value*16 + digit
	}
	result := (value*255 + maxValue/2) / maxValue
	return &result
}

func (t *ProcessTerminal) OnEOF(callback func()) {
	t.eofHandler = callback
}

func (t *ProcessTerminal) deliverEOF() {
	t.buffer.flush()
	t.mu.Lock()
	handler := t.eofHandler
	handled := t.eofDelivered.Swap(true)
	t.mu.Unlock()
	if handler != nil && !handled {
		handler()
	}
}

func (t *ProcessTerminal) DrainInput(maxMs, idleMs int) {
	if maxMs <= 0 {
		return
	}
	idle := idleMs
	if idle <= 0 {
		idle = 50
	}
	started := time.Now()
	deadline := started.Add(time.Duration(maxMs) * time.Millisecond)
	for {
		lastRead := time.Unix(0, lastInputTime.Load())
		if time.Since(lastRead) >= time.Duration(idle)*time.Millisecond {
			return
		}
		if !time.Now().Before(deadline) {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

func (t *ProcessTerminal) restoreAllLocked() {
	if t.kittyQueryPushed {
		t.stdout.Write([]byte(KittyProtocolPop))
		t.kittyQueryPushed = false
	}
	t.kittyActive.Store(false)
	SetKittyProtocolActive(false)
	t.disableModifyOtherKeys()
	if t.pasteEnabled {
		t.stdout.Write([]byte(BracketedPasteOff))
		t.pasteEnabled = false
	}
	if t.wasRaw != nil {
		_ = t.ops.restoreState(t.stdinFile, t.wasRaw)
		t.wasRaw = nil
	}
}

func (t *ProcessTerminal) Stop() {
	t.mu.Lock()
	if !t.running {
		t.mu.Unlock()
		return
	}
	t.running = false
	t.stopped = true
	t.restoreAllLocked()
	t.inputHandler = nil
	t.resizeHandler = nil
	winchStop, winchDone := t.winchStop, t.winchDone
	t.winchStop, t.winchDone = nil, nil
	stopResizeSignals := t.stopResizeSignals
	t.stopResizeSignals = nil
	stopReading := t.stopReading
	readerDone := t.readerDone
	t.buffer.clear()
	t.mu.Unlock()

	if winchStop != nil {
		close(winchStop)
	}
	if winchDone != nil {
		<-winchDone
	}
	if stopResizeSignals != nil {
		stopResizeSignals()
	}
	if stopReading != nil {
		close(stopReading)
	}
	if readerDone != nil {
		select {
		case <-readerDone:
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (t *ProcessTerminal) Write(data string) {
	_, _ = t.stdout.Write([]byte(data))
}

func (t *ProcessTerminal) Columns() int {
	t.refreshSize()
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.columns
}

func (t *ProcessTerminal) Rows() int {
	t.refreshSize()
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.rows
}

func (t *ProcessTerminal) KittyProtocolActive() bool { return t.kittyActive.Load() }

func (t *ProcessTerminal) ModifyOtherKeysActive() bool { return t.modifyOtherKeys.Load() }

func (t *ProcessTerminal) MoveBy(lines int) {
	t.Write(CursorMoveLines(lines))
}

func (t *ProcessTerminal) HideCursor()      { t.Write(CursorHide) }
func (t *ProcessTerminal) ShowCursor()      { t.Write(CursorShow) }
func (t *ProcessTerminal) ClearLine()       { t.Write(CursorEraseLine) }
func (t *ProcessTerminal) ClearFromCursor() { t.Write(CursorEraseBelow) }
func (t *ProcessTerminal) ClearScreen()     { t.Write(CursorEraseScreen + CursorHome) }

func (t *ProcessTerminal) SetTitle(title string) {
	t.Write(OSCTitle(title))
}
