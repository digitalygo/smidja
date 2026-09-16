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
	SuspendRaw() error
	ResumeRaw() error
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

	stdinPollInterval = 25 * time.Millisecond
	fdSetLimit        = 1024
	readerParkTimeout = time.Second
)

type readerSession struct {
	generation int
	stop       <-chan struct{}
	done       chan struct{}
	fd         uintptr
	gated      bool
	readable   func(uintptr, time.Duration) (bool, error)
}

type deliveryBatch struct {
	sequences  []string
	eof        bool
	generation int
}

type deliveryPump struct {
	mu      sync.Mutex
	cond    *sync.Cond
	queue   []deliveryBatch
	stopped bool
	handle  func(deliveryBatch)
}

func newDeliveryPump(handle func(deliveryBatch)) *deliveryPump {
	pump := &deliveryPump{handle: handle}
	pump.cond = sync.NewCond(&pump.mu)
	return pump
}

func (p *deliveryPump) enqueue(batch deliveryBatch) {
	if len(batch.sequences) == 0 && !batch.eof {
		return
	}
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return
	}
	p.queue = append(p.queue, batch)
	p.cond.Signal()
	p.mu.Unlock()
}

func (p *deliveryPump) stop() {
	p.mu.Lock()
	p.stopped = true
	p.queue = nil
	p.cond.Broadcast()
	p.mu.Unlock()
}

func (p *deliveryPump) run() {
	for {
		p.mu.Lock()
		for len(p.queue) == 0 && !p.stopped {
			p.cond.Wait()
		}
		if len(p.queue) == 0 {
			p.mu.Unlock()
			return
		}
		batch := p.queue[0]
		p.queue = p.queue[1:]
		p.mu.Unlock()
		p.handle(batch)
	}
}

var errReaderParkTimeout = errors.New("tui: input reader did not park in time")

var errReaderNotExited = errors.New("tui: input reader has not exited")

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
	suspended    bool
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

	consumeGate atomic.Pointer[func()]
	publishGate atomic.Pointer[func()]

	bufferMu             sync.Mutex
	buffer               *stdinBuffer
	flushMu              sync.Mutex
	flushTimer           *time.Timer
	flushArmed           bool
	flushArmedGeneration int
	flushArmedEpoch      uint64
	flushNextEpoch       uint64

	queryWaiter *pendingQuery

	stopReading chan struct{}
	readerDone  chan struct{}

	readerPaused       bool
	readerGeneration   int
	deliveryGeneration int
	readerParkWait     time.Duration

	pump *deliveryPump

	stdinReadableFn func(uintptr, time.Duration) (bool, error)

	stdinFD    uintptr
	stdinGated bool

	pasteWasEnabled          bool
	kittyWasPushed           bool
	modifyOtherKeysSuspended bool

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
		readerParkWait:  readerParkTimeout,
		stdinReadableFn: stdinReadableSelect,
		columns:         defaultColumns,
		rows:            defaultRows,
	}
	terminal.buffer = newStdinBuffer(terminal.escapeTimeoutMs)
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
	t.mu.Lock()
	defer t.mu.Unlock()
	t.refreshSizeLocked()
}

func (t *ProcessTerminal) refreshSizeLocked() {
	cols, rows, err := 0, 0, errors.New("no file")
	if t.stdoutFile != nil {
		cols, rows, err = t.ops.windowSize(t.stdoutFile)
	}
	if err != nil {
		cols = envSize(os.Getenv, "COLUMNS", defaultColumns)
		rows = envSize(os.Getenv, "LINES", defaultRows)
	}
	t.columns = cols
	t.rows = rows
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
	defer t.mu.Unlock()
	if t.running {
		return errors.New("tui: terminal already started")
	}
	if t.stdinFile == nil || t.stdoutFile == nil || !t.ops.isTerminal(t.stdinFile) || !t.ops.isTerminal(t.stdoutFile) {
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
		t.stopped = true
		return err
	}
	t.wasRaw = state

	t.suspended = false
	t.readerPaused = false
	t.readerGeneration = 0
	t.pasteWasEnabled = false
	t.kittyWasPushed = false
	t.modifyOtherKeysSuspended = false
	t.winchStop = make(chan struct{})
	t.winchDone = make(chan struct{})
	t.stdinFD, t.stdinGated = stdinFileDescriptor(t.stdinFile)

	t.pasteEnabled = true
	t.stdout.Write([]byte(BracketedPasteOn))
	t.refreshSizeLocked()
	t.kittyQueryPushed = true
	t.stdout.Write([]byte(kittyQuery))

	t.startResizeWatcher()

	pump := newDeliveryPump(t.deliverBatch)
	t.pump = pump
	go pump.run()

	t.spawnReaderLocked()
	return nil
}

func (t *ProcessTerminal) deliverBatch(batch deliveryBatch) {
	for _, sequence := range batch.sequences {
		if !t.deliveryActive(batch.generation) {
			return
		}
		t.dispatchSequenceTagged(sequence, batch.generation)
	}
	if batch.eof {
		if !t.deliveryActive(batch.generation) {
			return
		}
		t.deliverEOF(batch.generation)
	}
}

func (t *ProcessTerminal) deliveryActive(generation int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.deliveryActiveLocked(generation)
}

func (t *ProcessTerminal) deliveryActiveLocked(generation int) bool {
	return !t.stopped && !t.suspended && t.deliveryGeneration == generation
}

func (t *ProcessTerminal) currentDeliveryGeneration() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.deliveryGeneration
}

func (t *ProcessTerminal) stopDelivery() {
	t.mu.Lock()
	t.deliveryGeneration++
	pump := t.pump
	t.pump = nil
	t.mu.Unlock()
	t.flushMu.Lock()
	t.invalidateFlushLocked()
	t.flushMu.Unlock()
	if pump != nil {
		pump.stop()
	}
}

func stdinFileDescriptor(file *os.File) (uintptr, bool) {
	if file == nil {
		return 0, false
	}
	conn, err := file.SyscallConn()
	if err != nil {
		return 0, false
	}
	var descriptor uintptr
	if err := conn.Control(func(fd uintptr) { descriptor = fd }); err != nil {
		return 0, false
	}
	return descriptor, descriptor < fdSetLimit
}

func (t *ProcessTerminal) spawnReaderLocked() {
	t.readerGeneration++
	t.stopReading = make(chan struct{})
	t.readerDone = make(chan struct{})
	session := readerSession{
		generation: t.readerGeneration,
		stop:       t.stopReading,
		done:       t.readerDone,
		fd:         t.stdinFD,
		gated:      t.stdinGated,
		readable:   t.stdinReadableFn,
	}
	go t.readLoop(session)
}

func (t *ProcessTerminal) closeReaderChannelsLocked() {
	if t.stopReading != nil {
		close(t.stopReading)
		t.stopReading = nil
	}
}

func (t *ProcessTerminal) parkReaderForSuspend() error {
	t.mu.Lock()
	if t.stopped {
		t.mu.Unlock()
		return errors.New("tui: terminal is stopped")
	}
	if !t.running || t.readerPaused {
		t.mu.Unlock()
		return nil
	}
	t.readerPaused = true
	readerDone := t.readerDone
	generation := t.readerGeneration
	parkWait := t.readerParkWait
	t.closeReaderChannelsLocked()
	t.mu.Unlock()
	if readerDone == nil {
		return nil
	}
	select {
	case <-readerDone:
		return nil
	case <-time.After(parkWait):
		t.armReaderRecovery(generation, readerDone)
		return errReaderParkTimeout
	}
}

func (t *ProcessTerminal) armReaderRecovery(generation int, done chan struct{}) {
	go func() {
		<-done
		t.mu.Lock()
		defer t.mu.Unlock()
		if !t.running || t.stopped || t.suspended || !t.readerPaused {
			return
		}
		if t.readerGeneration != generation || t.readerDone != done {
			return
		}
		t.readerPaused = false
		t.spawnReaderLocked()
	}()
}

func (t *ProcessTerminal) readerExitedLocked() bool {
	if t.readerDone == nil {
		return true
	}
	select {
	case <-t.readerDone:
		return true
	default:
		return false
	}
}

func (t *ProcessTerminal) resumeReaderLocked() error {
	if !t.readerPaused {
		return nil
	}
	if !t.readerExitedLocked() {
		return errReaderNotExited
	}
	t.readerPaused = false
	if t.running && !t.stopped {
		t.spawnReaderLocked()
	}
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
	winchStop := t.winchStop
	winchDone := t.winchDone
	go func() {
		defer close(winchDone)
		for {
			select {
			case <-winchStop:
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

func (t *ProcessTerminal) readLoop(session readerSession) {
	defer close(session.done)
	defer t.deliverEOFForSession(session)

	var utf8Hold []byte
	readBuffer := make([]byte, readBufferSize)
	for {
		select {
		case <-session.stop:
			return
		default:
		}
		if session.gated {
			readable, err := session.readable(session.fd, stdinPollInterval)
			if err != nil {
				return
			}
			if !readable {
				continue
			}
		}
		n, err := t.stdin.Read(readBuffer)
		if n > 0 {
			t.mu.Lock()
			retired := t.readerGeneration != session.generation
			quiesced := t.readerPaused || t.stopped
			generation := t.deliveryGeneration
			pump := t.pump
			t.mu.Unlock()
			if retired {
				return
			}
			if !quiesced {
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
				t.ingestInputChunk(chunk, generation, pump)
			}
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
			case <-session.stop:
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
	t.dispatchSequenceTagged(sequence, t.currentDeliveryGeneration())
}

func (t *ProcessTerminal) dispatchSequenceTagged(sequence string, generation int) {
	t.mu.Lock()
	if !t.deliveryActiveLocked(generation) {
		t.mu.Unlock()
		return
	}
	waiter := t.queryWaiter
	if waiter != nil && waiter.matcher(sequence) {
		t.queryWaiter = nil
		t.mu.Unlock()
		waiter.reply <- sequence
		close(waiter.reply)
		return
	}
	if t.consumeNegotiationLocked(sequence) {
		t.mu.Unlock()
		return
	}
	handler := t.inputHandler
	t.mu.Unlock()

	if handler != nil {
		handler(sequence)
	}
}

func (t *ProcessTerminal) consumeNegotiationLocked(sequence string) bool {
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

func (t *ProcessTerminal) enterConsumeGate() {
	if gate := t.consumeGate.Load(); gate != nil {
		(*gate)()
	}
}

func (t *ProcessTerminal) enterPublishGate() {
	if gate := t.publishGate.Load(); gate != nil {
		(*gate)()
	}
}

func (t *ProcessTerminal) consumePendingForGeneration(generation int, requireRunning bool) ([]string, *deliveryPump, bool) {
	t.bufferMu.Lock()
	t.mu.Lock()
	pump := t.pump
	active := t.deliveryActiveLocked(generation)
	if requireRunning && (!t.running || t.readerPaused) {
		active = false
	}
	if !active || pump == nil {
		t.mu.Unlock()
		t.bufferMu.Unlock()
		return nil, nil, false
	}
	t.flushMu.Lock()
	t.invalidateFlushLocked()
	sequences := t.buffer.flush()
	t.flushMu.Unlock()
	t.mu.Unlock()
	t.bufferMu.Unlock()
	return sequences, pump, true
}

func (t *ProcessTerminal) ingestInputChunk(chunk []byte, generation int, pump *deliveryPump) {
	t.bufferMu.Lock()
	t.flushMu.Lock()
	t.invalidateFlushLocked()
	t.flushMu.Unlock()
	t.buffer.process(chunk)
	sequences := t.buffer.takePending()
	if pump != nil && len(sequences) > 0 {
		pump.enqueue(deliveryBatch{sequences: sequences, generation: generation})
	}
	t.flushMu.Lock()
	t.armFlushLocked(generation)
	t.flushMu.Unlock()
	t.bufferMu.Unlock()
}

func (t *ProcessTerminal) invalidateFlushLocked() {
	if t.flushTimer != nil {
		t.flushTimer.Stop()
		t.flushTimer = nil
	}
	t.flushNextEpoch++
	t.flushArmed = false
}

func (t *ProcessTerminal) armFlushLocked(generation int) {
	delayMs, pending := t.buffer.flushDelay()
	if !pending || delayMs <= 0 {
		t.flushArmed = false
		return
	}
	epoch := t.flushNextEpoch
	t.flushArmed = true
	t.flushArmedGeneration = generation
	t.flushArmedEpoch = epoch
	t.flushTimer = time.AfterFunc(time.Duration(delayMs)*time.Millisecond, func() {
		t.flushPending(generation, epoch)
	})
}

func (t *ProcessTerminal) scheduleFlush() {
	t.bufferMu.Lock()
	t.mu.Lock()
	t.flushMu.Lock()
	t.invalidateFlushLocked()
	generation := t.deliveryGeneration
	t.armFlushLocked(generation)
	t.flushMu.Unlock()
	t.mu.Unlock()
	t.bufferMu.Unlock()
}

func (t *ProcessTerminal) flushPending(generation int, epoch uint64) {
	t.enterConsumeGate()
	t.bufferMu.Lock()
	t.mu.Lock()
	t.flushMu.Lock()
	if !t.flushArmed || t.flushArmedEpoch != epoch || t.flushArmedGeneration != generation {
		t.flushMu.Unlock()
		t.mu.Unlock()
		t.bufferMu.Unlock()
		return
	}
	pump := t.pump
	active := t.deliveryActiveLocked(generation)
	if !t.running || t.readerPaused {
		active = false
	}
	if !active || pump == nil {
		t.flushArmed = false
		t.flushTimer = nil
		t.flushMu.Unlock()
		t.mu.Unlock()
		t.bufferMu.Unlock()
		return
	}
	sequences := t.buffer.flush()
	t.flushArmed = false
	t.flushTimer = nil
	if len(sequences) == 0 {
		t.flushMu.Unlock()
		t.mu.Unlock()
		t.bufferMu.Unlock()
		return
	}
	t.enterPublishGate()
	pump.enqueue(deliveryBatch{sequences: sequences, generation: generation})
	t.flushMu.Unlock()
	t.mu.Unlock()
	t.bufferMu.Unlock()
}

func (t *ProcessTerminal) cancelFlushTimer() {
	t.flushMu.Lock()
	t.invalidateFlushLocked()
	t.flushMu.Unlock()
}

func (t *ProcessTerminal) deliverEOFForSession(session readerSession) {
	t.bufferMu.Lock()
	t.mu.Lock()
	retired := t.readerGeneration != session.generation
	quiesced := t.readerPaused || t.stopped || !t.running
	pump := t.pump
	generation := t.deliveryGeneration
	if retired || quiesced || pump == nil {
		t.mu.Unlock()
		t.bufferMu.Unlock()
		return
	}
	t.flushMu.Lock()
	t.invalidateFlushLocked()
	sequences := t.buffer.flush()
	t.enterPublishGate()
	pump.enqueue(deliveryBatch{sequences: sequences, eof: true, generation: generation})
	t.flushMu.Unlock()
	t.mu.Unlock()
	t.bufferMu.Unlock()
}

func (t *ProcessTerminal) deliverEOFHandler(generation int) {
	t.mu.Lock()
	if !t.deliveryActiveLocked(generation) {
		t.mu.Unlock()
		return
	}
	handler := t.eofHandler
	handled := t.eofDelivered.Swap(true)
	t.mu.Unlock()
	if handler != nil && !handled {
		handler()
	}
}

func (t *ProcessTerminal) deliverEOF(generation int) {
	t.enterConsumeGate()
	sequences, _, active := t.consumePendingForGeneration(generation, false)
	if !active {
		return
	}
	for _, sequence := range sequences {
		if !t.deliveryActive(generation) {
			return
		}
		t.dispatchSequenceTagged(sequence, generation)
	}
	t.deliverEOFHandler(generation)
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
	t.suspended = false
	t.readerPaused = false
	t.pasteWasEnabled = false
	t.kittyWasPushed = false
	t.modifyOtherKeysSuspended = false
}

func (t *ProcessTerminal) disableInteractiveProtocolsLocked() {
	if t.kittyQueryPushed {
		t.kittyWasPushed = true
		t.kittyQueryPushed = false
		t.stdout.Write([]byte(KittyProtocolPop))
	}
	t.kittyActive.Store(false)
	SetKittyProtocolActive(false)
	if t.modifyOtherKeys.Swap(false) {
		t.modifyOtherKeysSuspended = true
		t.stdout.Write([]byte(ModifyOtherKeysDisable))
	}
	if t.pasteEnabled {
		t.pasteWasEnabled = true
		t.pasteEnabled = false
		t.stdout.Write([]byte(BracketedPasteOff))
	}
}

func (t *ProcessTerminal) enableInteractiveProtocolsLocked() {
	if t.pasteWasEnabled {
		t.pasteWasEnabled = false
		t.pasteEnabled = true
		t.stdout.Write([]byte(BracketedPasteOn))
	}
	if t.modifyOtherKeysSuspended {
		t.modifyOtherKeysSuspended = false
		t.enableModifyOtherKeys()
	}
	if t.kittyWasPushed {
		t.kittyWasPushed = false
		t.kittyQueryPushed = true
		t.kittyActive.Store(false)
		SetKittyProtocolActive(false)
		t.stdout.Write([]byte(kittyQuery))
	}
}

func (t *ProcessTerminal) SuspendRaw() error {
	t.mu.Lock()
	stopped, suspended := t.stopped, t.suspended
	t.mu.Unlock()
	if stopped {
		return errors.New("tui: terminal is stopped")
	}
	if suspended {
		return nil
	}
	if err := t.parkReaderForSuspend(); err != nil {
		return err
	}

	if err := t.quiesceBufferForSuspend(); err != nil {
		return err
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped {
		return errors.New("tui: terminal is stopped")
	}
	if t.suspended {
		return nil
	}
	t.disableInteractiveProtocolsLocked()
	if t.wasRaw != nil {
		if err := t.ops.restoreState(t.stdinFile, t.wasRaw); err != nil {
			t.enableInteractiveProtocolsLocked()
			_ = t.resumeReaderLocked()
			return err
		}
	}
	t.suspended = true
	return nil
}

func (t *ProcessTerminal) quiesceBufferForSuspend() error {
	t.cancelFlushTimer()
	t.bufferMu.Lock()
	t.mu.Lock()
	if t.stopped {
		t.mu.Unlock()
		t.bufferMu.Unlock()
		return errors.New("tui: terminal is stopped")
	}
	t.deliveryGeneration++
	t.buffer.clear()
	t.mu.Unlock()
	t.bufferMu.Unlock()
	return nil
}

func (t *ProcessTerminal) ResumeRaw() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.suspended {
		return nil
	}
	if t.readerPaused && !t.readerExitedLocked() {
		return errReaderNotExited
	}
	if t.wasRaw != nil {
		state, err := t.ops.makeRaw(t.stdinFile)
		if err != nil {
			return err
		}
		t.wasRaw = state
	}
	t.deliveryGeneration++
	t.suspended = false
	t.enableInteractiveProtocolsLocked()
	return t.resumeReaderLocked()
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
	t.mu.Unlock()

	t.cancelFlushTimer()
	t.bufferMu.Lock()
	t.buffer.clear()
	t.bufferMu.Unlock()
	t.stopDelivery()

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
