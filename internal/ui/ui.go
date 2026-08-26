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

type LineUI struct {
	mu     sync.Mutex
	mode   sdk.Mode
	in     *bufio.Reader
	stdout io.Writer
	stderr io.Writer

	statuses map[string]string
	widgets  map[string][]string
	working  string
	title    string
	dirty    bool
}

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

var _ sdk.UI = (*LineUI)(nil)

func (l *LineUI) interactive() bool { return l.mode == sdk.ModeInteractive }

func (l *LineUI) Notify(message string, kind sdk.NotifyKind) {
	if !l.interactive() {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.stderr, "smidja: [%s] %s\n", kind, message)
}

func (l *LineUI) Confirm(title, message string) (bool, error) {
	if !l.interactive() {
		return false, sdk.ErrModeUnsupported
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.flushState()
	fmt.Fprintf(l.stderr, "%s: %s [y/N] ", title, message)
	line, err := l.readLine()
	if err != nil {
		return false, nil
	}
	return isYes(line), nil
}

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
		if err != nil {
			return "", nil
		}
		if n, ok := parseChoice(line, len(options)); ok {
			return options[n], nil
		}
		fmt.Fprintln(l.stderr, "invalid choice, try again")
	}
}

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
	if err != nil {
		return "", nil
	}
	return line, nil
}

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

func (l *LineUI) SetWorkingMessage(message string) {
	if !l.interactive() {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.working = message
	l.dirty = true
}

func (l *LineUI) SetTitle(title string) {
	if !l.interactive() {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.title = title
	l.dirty = true
}

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

func (l *LineUI) readLine() (string, error) {
	line, err := l.in.ReadString('\n')
	line = strings.TrimRight(line, "\r\n")
	if err != nil && line == "" {
		return "", err
	}
	return line, nil
}

func isYes(line string) bool {
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

func parseChoice(line string, n int) (int, bool) {
	v, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || v < 1 || v > n {
		return 0, false
	}
	return v - 1, true
}
