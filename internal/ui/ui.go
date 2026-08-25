// Package ui implements the line-oriented user interface of the smidja
// harness: the sdk.UI surface backed by a terminal-friendly REPL on
// stdin, stdout, and stderr.
//
// LineUI owns the buffered stdin reader. New wraps the provided stdin in
// a bufio.Reader, and every dialog reads through that single reader, so
// REPL prompts and extension dialogs share one buffering stream. When
// the CLI integrates LineUI it must stop creating its own bufio.Reader
// and hand its raw stdin to ui.New; the CLI keeps its own reader for
// now.
//
// Routing: dialogs and notifications render to stderr, keeping stdout
// clean for model output. In print mode (sdk.ModePrint) the dialog
// methods return sdk.ErrModeUnsupported without reading stdin, and the
// fire-and-forget methods are no-ops, per the sdk.UI contract. The
// state-carrying methods (SetStatus, SetWidget, SetWorkingMessage,
// SetTitle) render minimally as single stderr lines at the next prompt
// boundary, that is, at the start of the next dialog call. All dialog
// and render work is serialized by a mutex so concurrent extension
// calls never interleave.
package ui

import (
	"bufio"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/digitalygo/smidja/sdk"
)

// LineUI is the line-oriented sdk.UI implementation. Construct it with
// New; the zero value is not usable.
type LineUI struct {
	mu     sync.Mutex
	mode   sdk.Mode
	in     *bufio.Reader
	stdout io.Writer
	stderr io.Writer

	// Dialog state carried between prompt boundaries.
	statuses map[string]string
	widgets  map[string][]string
	working  string
	title    string
	dirty    bool // state changed since the last flush
}

// New builds a LineUI around the given streams and mode. LineUI owns the
// buffered stdin reader: stdin is wrapped in a bufio.Reader that every
// dialog shares, so the CLI must hand its raw stdin here and stop
// creating its own reader. Nil streams are tolerated: a nil stdin
// behaves as immediate EOF (dialogs cancel), and nil writers discard
// output. Modes other than sdk.ModeInteractive behave like
// sdk.ModePrint.
func New(stdin io.Reader, stdout, stderr io.Writer, mode sdk.Mode) *LineUI {
	if stdin == nil {
		stdin = strings.NewReader("")
	}
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	return &LineUI{
		mode:     mode,
		in:       bufio.NewReader(stdin),
		stdout:   stdout,
		stderr:   stderr,
		statuses: make(map[string]string),
		widgets:  make(map[string][]string),
	}
}

// Compile-time assertion that LineUI satisfies the UI contract.
var _ sdk.UI = (*LineUI)(nil)

// interactive reports whether the UI runs in interactive mode, the only
// mode with dialog support.
func (l *LineUI) interactive() bool { return l.mode == sdk.ModeInteractive }

// Notify prints a fire-and-forget notification line to stderr in
// interactive mode; it is a no-op in print mode. stdout is never used,
// so model output stays clean.
func (l *LineUI) Notify(message string, kind sdk.NotifyKind) {
	if !l.interactive() {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.stderr, "smidja: [%s] %s\n", kind, message)
}

// Confirm shows a "title: message [y/N]" prompt on stderr and reads one
// line from stdin. It returns true for y/Y/yes/YES (case-insensitive,
// trimmed), false for anything else, and (false, nil) on EOF, which
// counts as a cancel. In print mode it returns sdk.ErrModeUnsupported
// without touching stdin.
func (l *LineUI) Confirm(title, message string) (bool, error) {
	if !l.interactive() {
		return false, sdk.ErrModeUnsupported
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.flushState()
	fmt.Fprintf(l.stderr, "%s: %s [y/N] ", title, message)
	line, err := l.readLine()
	if err != nil { // EOF with no input cancels
		return false, nil
	}
	return isYes(line), nil
}

// Select prints a numbered list on stderr and reads a 1-based choice
// from stdin. Invalid input (non-numeric or out of range) re-prompts;
// EOF cancels with ("", nil). An empty option list returns ("", nil)
// without prompting. In print mode it returns sdk.ErrModeUnsupported
// without touching stdin.
func (l *LineUI) Select(title string, options []string) (string, error) {
	if !l.interactive() {
		return "", sdk.ErrModeUnsupported
	}
	if len(options) == 0 {
		return "", nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.flushState()
	fmt.Fprintf(l.stderr, "%s:\n", title)
	for i, opt := range options {
		fmt.Fprintf(l.stderr, "  %d. %s\n", i+1, opt)
	}
	for {
		fmt.Fprintf(l.stderr, "choose 1-%d: ", len(options))
		line, err := l.readLine()
		if err != nil { // EOF cancels
			return "", nil
		}
		if n, ok := parseChoice(line, len(options)); ok {
			return options[n], nil
		}
		fmt.Fprintln(l.stderr, "invalid choice, try again")
	}
}

// Input shows a one-line prompt on stderr and returns the entered line
// with its line terminator stripped. EOF cancels with ("", nil). In
// print mode it returns sdk.ErrModeUnsupported without touching stdin.
func (l *LineUI) Input(title, placeholder string) (string, error) {
	if !l.interactive() {
		return "", sdk.ErrModeUnsupported
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.flushState()
	if placeholder != "" {
		fmt.Fprintf(l.stderr, "%s [%s]: ", title, placeholder)
	} else {
		fmt.Fprintf(l.stderr, "%s: ", title)
	}
	line, err := l.readLine()
	if err != nil { // EOF cancels
		return "", nil
	}
	return line, nil
}

// Editor opens $EDITOR on a temp file prefilled with prefill and returns
// the resulting text. It returns sdk.ErrModeUnsupported in print mode
// and when $EDITOR is unset: the line UI has no multiline terminator, so
// there is no way to finish a document typed inline, and the built-in
// multiline editor is deferred to the TUI phase. The editor command is
// split on whitespace, so values such as "code -w" work; quoted
// arguments are not supported. The dialog mutex is held for the whole
// editing session, so concurrent UI calls wait until the editor exits.
// A non-zero editor exit is reported as an error.
func (l *LineUI) Editor(title, prefill string) (string, error) {
	if !l.interactive() {
		return "", sdk.ErrModeUnsupported
	}
	editor := os.Getenv("EDITOR")
	if strings.TrimSpace(editor) == "" {
		return "", fmt.Errorf("%w: $EDITOR is not set; the built-in multiline editor is deferred to the TUI phase", sdk.ErrModeUnsupported)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.flushState()
	return l.runEditor(editor, prefill)
}

// runEditor writes prefill to a temp file, runs the editor command on
// it, and returns the file contents. The temp file is removed
// afterwards. Callers must hold l.mu.
func (l *LineUI) runEditor(editor, prefill string) (string, error) {
	f, err := os.CreateTemp("", "smidja-editor-*")
	if err != nil {
		return "", fmt.Errorf("ui: create temp file: %w", err)
	}
	path := f.Name()
	defer os.Remove(path)
	if _, err := f.WriteString(prefill); err != nil {
		f.Close()
		return "", fmt.Errorf("ui: write prefill: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("ui: close prefill: %w", err)
	}

	parts := strings.Fields(editor)
	cmd := exec.Command(parts[0], append(parts[1:], path)...)
	// The editor inherits the UI streams. A full terminal-editor
	// passthrough needs the raw tty fd, which the buffered reader does
	// not expose; that arrives with the TUI phase. Non-tty editors such
	// as "code -w" and scripts work today.
	cmd.Stdin = l.in
	cmd.Stdout = l.stdout
	cmd.Stderr = l.stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("ui: editor %q: %w", editor, err)
	}
	out, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("ui: read editor output: %w", err)
	}
	return string(out), nil
}

// SetStatus records a status line for key, rendered as a single stderr
// line at the next prompt boundary. An empty text clears the key. It is
// a no-op in print mode.
func (l *LineUI) SetStatus(key, text string) {
	if !l.interactive() {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if text == "" {
		delete(l.statuses, key)
	} else {
		l.statuses[key] = text
	}
	l.dirty = true
}

// SetWidget records a widget for key, rendered as a single stderr line
// at the next prompt boundary with the content joined by " | ". A nil
// content clears the widget. It is a no-op in print mode.
func (l *LineUI) SetWidget(key string, content []string) {
	if !l.interactive() {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if content == nil {
		delete(l.widgets, key)
	} else {
		l.widgets[key] = append([]string(nil), content...)
	}
	l.dirty = true
}

// SetWorkingMessage records the message shown while the model streams,
// rendered as a single stderr line at the next prompt boundary. An empty
// message restores the default and renders nothing. It is a no-op in
// print mode.
func (l *LineUI) SetWorkingMessage(message string) {
	if !l.interactive() {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.working = message
	l.dirty = true
}

// SetTitle records the terminal window title, rendered as a single
// stderr line at the next prompt boundary. It is a no-op in print mode.
func (l *LineUI) SetTitle(title string) {
	if !l.interactive() {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.title = title
	l.dirty = true
}

// flushState renders the recorded state as single stderr lines at a
// prompt boundary, in a fixed order: title, working message, then
// statuses and widgets each sorted by key. Callers must hold l.mu.
func (l *LineUI) flushState() {
	if !l.dirty {
		return
	}
	if l.title != "" {
		fmt.Fprintf(l.stderr, "title: %s\n", l.title)
	}
	if l.working != "" {
		fmt.Fprintf(l.stderr, "working: %s\n", l.working)
	}
	for _, k := range slices.Sorted(maps.Keys(l.statuses)) {
		fmt.Fprintf(l.stderr, "status: %s: %s\n", k, l.statuses[k])
	}
	for _, k := range slices.Sorted(maps.Keys(l.widgets)) {
		fmt.Fprintf(l.stderr, "widget: %s: %s\n", k, strings.Join(l.widgets[k], " | "))
	}
	l.dirty = false
}

// readLine reads one line from the buffered stdin, stripping the line
// terminator (including a trailing carriage return). A partial line at
// EOF is returned with a nil error, since it is still a valid answer; a
// clean EOF returns io.EOF so callers treat it as a cancel. Callers must
// hold l.mu.
func (l *LineUI) readLine() (string, error) {
	line, err := l.in.ReadString('\n')
	line = strings.TrimRight(line, "\r\n")
	if err != nil && line == "" {
		return "", err
	}
	return line, nil
}

// isYes reports whether a trimmed line is an affirmative answer.
func isYes(line string) bool {
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

// parseChoice parses a 1-based choice against a list of size n,
// returning the 0-based index when the line is a number in range.
func parseChoice(line string, n int) (int, bool) {
	v, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || v < 1 || v > n {
		return 0, false
	}
	return v - 1, true
}
