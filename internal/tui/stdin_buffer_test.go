package tui

import (
	"sync"
	"testing"
)

func collectSequences(t *testing.T, chunks [][]byte) []string {
	t.Helper()
	var got []string
	buffer := newStdinBuffer(defaultEscapeTimeout)
	buffer.onSequence = func(sequence string) { got = append(got, sequence) }
	for _, chunk := range chunks {
		buffer.process(chunk)
	}
	return got
}

func TestStdinBufferCompleteSequences(t *testing.T) {
	tests := []struct {
		name   string
		chunks [][]byte
		want   []string
	}{
		{
			name:   "plain text",
			chunks: [][]byte{[]byte("ab")},
			want:   []string{"a", "b"},
		},
		{
			name:   "csi",
			chunks: [][]byte{[]byte("\x1b[A")},
			want:   []string{"\x1b[A"},
		},
		{
			name:   "osc bel",
			chunks: [][]byte{[]byte("\x1b]11;rgb:00/00/00\x07")},
			want:   []string{"\x1b]11;rgb:00/00/00\x07"},
		},
		{
			name:   "osc st",
			chunks: [][]byte{[]byte("\x1b]11;rgb:00/00/00\x1b\\")},
			want:   []string{"\x1b]11;rgb:00/00/00\x1b\\"},
		},
		{
			name:   "sgr mouse",
			chunks: [][]byte{[]byte("\x1b[<0;12;5M")},
			want:   []string{"\x1b[<0;12;5M"},
		},
		{
			name:   "ss3",
			chunks: [][]byte{[]byte("\x1bOA")},
			want:   []string{"\x1bOA"},
		},
		{
			name:   "esc key alone",
			chunks: [][]byte{[]byte("\x1b")},
			want:   nil,
		},
		{
			name:   "esc then bracket",
			chunks: [][]byte{[]byte("\x1b"), []byte("[Z")},
			want:   []string{"\x1b[Z"},
		},
		{
			name:   "double esc prefix",
			chunks: [][]byte{[]byte("\x1b\x1b[A")},
			want:   []string{"\x1b", "\x1b[A"},
		},
		{
			name:   "utf8 text",
			chunks: [][]byte{[]byte("你好")},
			want:   []string{"你", "好"},
		},
		{
			name:   "paste whole",
			chunks: [][]byte{[]byte("\x1b[200~hi there\x1b[201~")},
			want:   []string{BracketedPaste("hi there")},
		},
		{
			name:   "paste split across chunks",
			chunks: [][]byte{[]byte("\x1b[200~hi"), []byte(" there\x1b[201~")},
			want:   []string{BracketedPaste("hi there")},
		},
		{
			name:   "paste with escape-like content",
			chunks: [][]byte{[]byte("\x1b[200~a\x1b[Ab\x1b[201~")},
			want:   []string{BracketedPaste("a\x1b[Ab")},
		},
		{
			name:   "text then paste then text",
			chunks: [][]byte{[]byte("x\x1b[200~p\x1b[201~y")},
			want:   []string{"x", BracketedPaste("p"), "y"},
		},
		{
			name:   "old mouse encoding",
			chunks: [][]byte{[]byte("\x1b[M !!")},
			want:   []string{"\x1b[M !!"},
		},
		{
			name:   "kitty printable then duplicate char",
			chunks: [][]byte{[]byte("\x1b[97ua")},
			want:   []string{"\x1b[97u"},
		},
		{
			name:   "kitty printable then other char",
			chunks: [][]byte{[]byte("\x1b[97ub")},
			want:   []string{"\x1b[97u", "b"},
		},
		{
			name:   "enter",
			chunks: [][]byte{[]byte("\r")},
			want:   []string{"\r"},
		},
		{
			name:   "modify other keys",
			chunks: [][]byte{[]byte("\x1b[27;5;99~")},
			want:   []string{"\x1b[27;5;99~"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := collectSequences(t, test.chunks)
			if len(got) != len(test.want) {
				t.Fatalf("sequences = %q, want %q", got, test.want)
			}
			for i := range got {
				if got[i] != test.want[i] {
					t.Fatalf("sequences[%d] = %q, want %q (all %q)", i, got[i], test.want[i], test.want)
				}
			}
		})
	}
}

func TestStdinBufferFlushPartial(t *testing.T) {
	var got []string
	buffer := newStdinBuffer(defaultEscapeTimeout)
	buffer.onSequence = func(sequence string) { got = append(got, sequence) }
	buffer.process([]byte("\x1b[1"))
	if len(got) != 0 {
		t.Fatalf("partial sequence emitted: %q", got)
	}
	delay, ok := buffer.flushDelay()
	if !ok || delay != defaultSequenceTimeout {
		t.Fatalf("flushDelay = (%d, %v), want (%d, true)", delay, ok, defaultSequenceTimeout)
	}
	flushed := buffer.flush()
	if len(flushed) != 1 || flushed[0] != "\x1b[1" {
		t.Fatalf("flush() = %q", flushed)
	}
	if buffer.buffer != "" {
		t.Fatalf("buffer not cleared after flush: %q", buffer.buffer)
	}
}

func TestStdinBufferEscapeTimeout(t *testing.T) {
	buffer := newStdinBuffer(defaultEscapeTimeout)
	buffer.process([]byte("\x1b"))
	delay, ok := buffer.flushDelay()
	if !ok || delay != defaultEscapeTimeout {
		t.Fatalf("escape flushDelay = (%d, %v), want (%d, true)", delay, ok, defaultEscapeTimeout)
	}
}

func TestStdinBufferSSHEscapeTimeout(t *testing.T) {
	if got := resolveEscapeTimeoutMs(func(name string) string {
		if name == "SSH_CONNECTION" {
			return "1 2 3 4"
		}
		return ""
	}); got != sshEscapeTimeout {
		t.Fatalf("SSH escape timeout = %d, want %d", got, sshEscapeTimeout)
	}
	if got := resolveEscapeTimeoutMs(func(string) string { return "" }); got != defaultEscapeTimeout {
		t.Fatalf("default escape timeout = %d, want %d", got, defaultEscapeTimeout)
	}
	if got := resolveEscapeTimeoutMs(func(name string) string {
		if name == "SMIDJA_TUI_ESC_TIMEOUT" {
			return "80"
		}
		return ""
	}); got != 80 {
		t.Fatalf("configured escape timeout = %d, want 80", got)
	}
}

func TestStdinBufferPasteUntilEnd(t *testing.T) {
	var got []string
	buffer := newStdinBuffer(defaultEscapeTimeout)
	buffer.onSequence = func(sequence string) { got = append(got, sequence) }
	buffer.process([]byte("\x1b[200~partial paste without end"))
	if len(got) != 0 {
		t.Fatalf("premature paste emission: %q", got)
	}
	if !buffer.pasteMode {
		t.Fatal("paste mode not active")
	}
	delay, ok := buffer.flushDelay()
	if ok {
		t.Fatalf("paste without end should not schedule flush, got %d", delay)
	}
	buffer.process([]byte(" more\x1b[201~"))
	if len(got) != 1 || got[0] != BracketedPaste("partial paste without end more") {
		t.Fatalf("paste emission = %q", got)
	}
}

func TestStdinBufferIncompleteUTF8HeldByTerminal(t *testing.T) {
	terminal, stdinWrite, _, _ := newTestTerminal(t)
	defer stdinWrite.Close()
	var mu sync.Mutex
	var inputs []string
	done := make(chan struct{}, 8)
	if err := terminal.Start(func(data string) {
		mu.Lock()
		inputs = append(inputs, data)
		mu.Unlock()
		done <- struct{}{}
	}, func() {}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	stdinWrite.Write([]byte{0xE4, 0xBD})
	stdinWrite.Write([]byte{0xA0})
	waitForInputCount(t, &mu, &inputs, 1, done)
	mu.Lock()
	defer mu.Unlock()
	if len(inputs) != 1 || inputs[0] != "你" {
		t.Fatalf("utf8 split input = %q, want [你]", inputs)
	}
}

func TestCompleteSequenceState(t *testing.T) {
	tests := []struct {
		data  string
		state sequenceState
	}{
		{"a", sequenceNotEscape},
		{"\x1b", sequenceIncomplete},
		{"\x1b[", sequenceIncomplete},
		{"\x1b[A", sequenceComplete},
		{"\x1b[1;5A", sequenceComplete},
		{"\x1b[<0;1;1M", sequenceComplete},
		{"\x1b[<0;1;1m", sequenceComplete},
		{"\x1b[<0;1;1X", sequenceIncomplete},
		{"\x1b[?1;2c", sequenceComplete},
		{"\x1b]11;rgb:00/00/00\x07", sequenceComplete},
		{"\x1b]11;rgb:00/00/00", sequenceIncomplete},
		{"\x1bP1$r\x07", sequenceComplete},
		{"\x1bP1$r", sequenceIncomplete},
		{"\x1bO", sequenceIncomplete},
		{"\x1bOA", sequenceComplete},
		{"\x1b\x1b", sequenceComplete},
	}
	for _, test := range tests {
		if got := completeSequenceState(test.data); got != test.state {
			t.Errorf("completeSequenceState(%q) = %v, want %v", test.data, got, test.state)
		}
	}
}
