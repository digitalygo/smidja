package tui

import (
	"strings"
	"unicode/utf8"
)

const (
	defaultSequenceTimeout = 50
	defaultEscapeTimeout   = 10
	sshEscapeTimeout       = 100
)

type stdinBuffer struct {
	buffer      string
	pasteMode   bool
	pasteBuffer string
	pendingKP   int

	sequenceTimeoutMs int
	escapeTimeoutMs   int

	onSequence func(sequence string)
}

func newStdinBuffer(escapeTimeoutMs int) *stdinBuffer {
	return &stdinBuffer{
		sequenceTimeoutMs: defaultSequenceTimeout,
		escapeTimeoutMs:   escapeTimeoutMs,
		pendingKP:         -1,
	}
}

func completeSequenceState(data string) sequenceState {
	if !strings.HasPrefix(data, ANSIESC) {
		return sequenceNotEscape
	}
	if len(data) == 1 {
		return sequenceIncomplete
	}
	afterEsc := data[1:]
	switch {
	case strings.HasPrefix(afterEsc, "["):
		if strings.HasPrefix(afterEsc, "[M") {
			if len(data) >= 6 {
				return sequenceComplete
			}
			return sequenceIncomplete
		}
		return completeCSIState(data)
	case strings.HasPrefix(afterEsc, "]"):
		return completeStringState(data, "]")
	case strings.HasPrefix(afterEsc, "P"), strings.HasPrefix(afterEsc, "_"):
		return completeStringState(data, afterEsc[:1])
	case strings.HasPrefix(afterEsc, "O"):
		if len(afterEsc) >= 2 {
			return sequenceComplete
		}
		return sequenceIncomplete
	case len(afterEsc) == 1:
		return sequenceComplete
	}
	return sequenceComplete
}

type sequenceState int

const (
	sequenceComplete sequenceState = iota
	sequenceIncomplete
	sequenceNotEscape
)

func completeCSIState(data string) sequenceState {
	if len(data) < 3 {
		return sequenceIncomplete
	}
	payload := data[2:]
	last := payload[len(payload)-1]
	if last < 0x40 || last > 0x7e {
		return sequenceIncomplete
	}
	if !strings.HasPrefix(payload, "<") {
		return sequenceComplete
	}
	if sgrMouseRegex(payload) {
		return sequenceComplete
	}
	return sequenceIncomplete
}

func sgrMouseRegex(payload string) bool {
	if len(payload) < 7 || payload[0] != '<' || (payload[len(payload)-1] != 'M' && payload[len(payload)-1] != 'm') {
		return false
	}
	parts := strings.Split(payload[1:len(payload)-1], ";")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		for i := 0; i < len(p); i++ {
			if p[i] < '0' || p[i] > '9' {
				return false
			}
		}
	}
	return true
}

func completeStringState(data, kind string) sequenceState {
	if strings.HasSuffix(data, ANSIST) || strings.HasSuffix(data, ANSIBEL) {
		return sequenceComplete
	}
	return sequenceIncomplete
}

func extractCompleteSequences(buffer string) (sequences []string, remainder string) {
	pos := 0
	for pos < len(buffer) {
		remaining := buffer[pos:]
		if strings.HasPrefix(remaining, ANSIESC) {
			seqEnd := 1
			emitted := false
			for seqEnd <= len(remaining) {
				candidate := remaining[:seqEnd]
				state := completeSequenceState(candidate)
				if state == sequenceComplete {
					if candidate == ANSIESC+ANSIESC {
						next := byte(0)
						if seqEnd < len(remaining) {
							next = remaining[seqEnd]
						}
						if next == '[' || next == ']' || next == 'O' || next == 'P' || next == '_' {
							sequences = append(sequences, ANSIESC)
							pos++
							emitted = true
							break
						}
					}
					sequences = append(sequences, candidate)
					pos += seqEnd
					emitted = true
					break
				}
				if state == sequenceIncomplete {
					seqEnd++
					continue
				}
				break
			}
			if emitted {
				continue
			}
			if seqEnd > len(remaining) {
				return sequences, remaining
			}
			return sequences, remaining
		}
		_, size := utf8.DecodeRuneInString(remaining)
		if size == 0 {
			size = 1
		}
		sequences = append(sequences, remaining[:size])
		pos += size
	}
	return sequences, ""
}

func parseUnmodifiedKittyPrintable(sequence string) int {
	if !strings.HasSuffix(sequence, "u") || !strings.HasPrefix(sequence, "\x1b[") {
		return -1
	}
	body := sequence[2 : len(sequence)-1]
	semi := strings.IndexByte(body, ';')
	if semi >= 0 {
		body = body[:semi]
	}
	if idx := strings.IndexByte(body, ':'); idx >= 0 {
		body = body[:idx]
	}
	if body == "" {
		return -1
	}
	cp := parseSGRInt(body)
	if cp < 32 {
		return -1
	}
	return cp
}

func (b *stdinBuffer) process(data []byte) {
	if len(b.buffer) == 0 && len(data) == 0 {
		b.emit("")
		return
	}
	str := decodeInputBytes(data)
	b.buffer += str

	if b.pasteMode {
		b.pasteBuffer += b.buffer
		b.buffer = ""
		b.finishPaste()
		return
	}

	if idx := strings.Index(b.buffer, BracketedPasteStart); idx != -1 {
		if idx > 0 {
			before := b.buffer[:idx]
			seqs, rem := extractCompleteSequences(before)
			for _, s := range seqs {
				b.emit(s)
			}
			if rem != "" {
				b.emit(rem)
			}
		}
		b.buffer = b.buffer[idx+len(BracketedPasteStart):]
		b.pasteMode = true
		b.pasteBuffer = b.buffer
		b.buffer = ""
		b.pendingKP = -1
		b.finishPaste()
		return
	}

	seqs, remainder := extractCompleteSequences(b.buffer)
	b.buffer = remainder
	for _, s := range seqs {
		b.emit(s)
	}
}

func (b *stdinBuffer) finishPaste() {
	endIndex := strings.Index(b.pasteBuffer, BracketedPasteEnd)
	if endIndex == -1 {
		return
	}
	content := b.pasteBuffer[:endIndex]
	remaining := b.pasteBuffer[endIndex+len(BracketedPasteEnd):]
	b.pasteMode = false
	b.pasteBuffer = ""
	b.pendingKP = -1
	b.emit(BracketedPaste(content))
	if remaining != "" {
		b.process([]byte(remaining))
	}
}

func (b *stdinBuffer) emit(sequence string) {
	if sequence != "" && utf8.RuneCountInString(sequence) == 1 && b.pendingKP >= 0 {
		r, _ := utf8.DecodeRuneInString(sequence)
		if int(r) == b.pendingKP {
			b.pendingKP = -1
			return
		}
	}
	if sequence != "" {
		b.pendingKP = parseUnmodifiedKittyPrintable(sequence)
	}
	if b.onSequence != nil {
		b.onSequence(sequence)
	}
}

func (b *stdinBuffer) flushDelay() (int, bool) {
	if b.buffer == "" {
		return 0, false
	}
	if b.buffer == ANSIESC {
		return b.escapeTimeoutMs, true
	}
	return b.sequenceTimeoutMs, true
}

func (b *stdinBuffer) flush() []string {
	if b.buffer == "" {
		return nil
	}
	sequences := []string{b.buffer}
	b.buffer = ""
	b.pendingKP = -1
	return sequences
}

func (b *stdinBuffer) clear() {
	b.buffer = ""
	b.pasteMode = false
	b.pasteBuffer = ""
	b.pendingKP = -1
}

func decodeInputBytes(data []byte) string {
	if len(data) == 1 && data[0] > 127 {
		return ANSIESC + string(rune(data[0]-128))
	}
	if utf8.Valid(data) {
		return string(data)
	}
	var b strings.Builder
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		if r == utf8.RuneError && size <= 1 {
			if data[0] > 127 {
				b.WriteString(ANSIESC + string(rune(data[0]-128)))
			} else {
				b.WriteByte(data[0])
			}
			data = data[1:]
			continue
		}
		b.WriteRune(r)
		data = data[size:]
	}
	return b.String()
}
