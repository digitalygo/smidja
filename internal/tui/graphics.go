package tui

import (
	"encoding/base64"
	"strconv"
	"strings"
	"sync"
	"time"
)

type GraphicsProtocol int

const (
	GraphicsNone GraphicsProtocol = iota
	GraphicsKitty
	GraphicsITerm2
)

func (p GraphicsProtocol) String() string {
	switch p {
	case GraphicsKitty:
		return "kitty"
	case GraphicsITerm2:
		return "iterm2"
	}
	return "none"
}

const (
	KittyGraphicsProbe        = "\x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\"
	kittyGraphicsReplyMaxLen  = 256
	kittyGraphicsPayloadChunk = 4096
)

func DetectGraphicsProtocol(env func(string) string) GraphicsProtocol {
	if env == nil {
		return GraphicsNone
	}
	term := strings.ToLower(env("TERM"))
	if env("TMUX") != "" || env("ZELLIJ") != "" || env("STY") != "" ||
		strings.HasPrefix(term, "tmux") || strings.HasPrefix(term, "screen") {
		return GraphicsNone
	}
	if env("KITTY_WINDOW_ID") != "" || strings.Contains(term, "kitty") || strings.Contains(term, "ghostty") ||
		env("TERM_PROGRAM") == "ghostty" {
		return GraphicsKitty
	}
	if env("TERM_PROGRAM") == "iTerm.app" || env("TERM_PROGRAM") == "WezTerm" || env("LC_TERMINAL") == "iTerm2" {
		return GraphicsITerm2
	}
	return GraphicsNone
}

func isKittyGraphicsSequence(sequence string) bool {
	return strings.HasPrefix(sequence, "\x1b_G") && len(sequence) <= kittyGraphicsReplyMaxLen
}

func ParseKittyGraphicsReply(reply string) bool {
	ok, matched := parseKittyGraphicsReply(reply, -1)
	return matched && ok
}

func parseKittyGraphicsReply(reply string, imageID int) (ok bool, matched bool) {
	if !isKittyGraphicsSequence(reply) {
		return false, false
	}
	body, complete := trimKittyGraphicsTerminator(strings.TrimPrefix(reply, "\x1b_G"))
	if !complete {
		return false, false
	}
	control, message, hasMessage := strings.Cut(body, ";")
	if !hasMessage {
		control = body
	}
	if !kittyControlMatchesID(control, imageID) {
		return false, false
	}
	return strings.TrimSpace(message) == "OK", true
}

func trimKittyGraphicsTerminator(body string) (string, bool) {
	if strings.HasSuffix(body, ANSIST) {
		return strings.TrimSuffix(body, ANSIST), true
	}
	if strings.HasSuffix(body, "\x07") {
		return strings.TrimSuffix(body, "\x07"), true
	}
	return body, false
}

func kittyControlMatchesID(control string, imageID int) bool {
	if imageID < 0 {
		return true
	}
	want := "i=" + strconv.Itoa(imageID)
	for _, field := range strings.Split(control, ",") {
		if strings.TrimSpace(field) == want {
			return true
		}
	}
	return false
}

type GraphicsProber interface {
	QueryKittyGraphics(timeout time.Duration) bool
}

type GraphicsCapability struct {
	Protocol GraphicsProtocol
	Enabled  bool
}

func (c GraphicsCapability) Available() bool {
	return c.Enabled && c.Protocol != GraphicsNone
}

func (c GraphicsCapability) Disable() GraphicsCapability {
	c.Enabled = false
	return c
}

func KittyTransmit(imageID, placement, columns, rows int, payload []byte) string {
	encoded := base64.StdEncoding.EncodeToString(payload)
	chunks := kittyChunks(encoded, kittyGraphicsPayloadChunk)
	var builder strings.Builder
	for index, chunk := range chunks {
		builder.WriteString("\x1b_G")
		if index == 0 {
			builder.WriteString("a=T,f=100,i=")
			builder.WriteString(strconv.Itoa(imageID))
			builder.WriteString(",p=")
			builder.WriteString(strconv.Itoa(placement))
			builder.WriteString(",c=")
			builder.WriteString(strconv.Itoa(columns))
			builder.WriteString(",r=")
			builder.WriteString(strconv.Itoa(rows))
			builder.WriteString(",C=1,q=2")
		}
		if len(chunks) > 1 {
			if index == 0 {
				builder.WriteString(",")
			}
			if index < len(chunks)-1 {
				builder.WriteString("m=1")
			} else {
				builder.WriteString("m=0")
			}
		}
		builder.WriteString(";")
		builder.WriteString(chunk)
		builder.WriteString(ANSIST)
	}
	return builder.String()
}

func kittyChunks(encoded string, size int) []string {
	if len(encoded) <= size {
		return []string{encoded}
	}
	var chunks []string
	for start := 0; start < len(encoded); start += size {
		end := start + size
		if end > len(encoded) {
			end = len(encoded)
		}
		chunks = append(chunks, encoded[start:end])
	}
	return chunks
}

func KittyDeletePlacement(imageID, placement int) string {
	return "\x1b_Ga=d,d=i,i=" + strconv.Itoa(imageID) + ",p=" + strconv.Itoa(placement) + ANSIST
}

func KittyDeleteImage(imageID int) string {
	return "\x1b_Ga=d,d=i,i=" + strconv.Itoa(imageID) + ANSIST
}

func KittyFreeImage(imageID int) string {
	return "\x1b_Ga=d,d=I,i=" + strconv.Itoa(imageID) + ANSIST
}

func KittyDeleteAll() string {
	return "\x1b_Ga=d" + ANSIST
}

func KittyFreeAll() string {
	return "\x1b_Ga=d,d=A" + ANSIST
}

func ITerm2Inline(name string, payload []byte, columns, rows int) string {
	encodedName := base64.StdEncoding.EncodeToString([]byte(name))
	var builder strings.Builder
	builder.WriteString("\x1b]1337;File=name=")
	builder.WriteString(encodedName)
	builder.WriteString(";size=")
	builder.WriteString(strconv.Itoa(len(payload)))
	builder.WriteString(";width=")
	builder.WriteString(strconv.Itoa(columns))
	builder.WriteString(";height=")
	builder.WriteString(strconv.Itoa(rows))
	builder.WriteString(";inline=1:")
	builder.WriteString(base64.StdEncoding.EncodeToString(payload))
	builder.WriteString("\x07")
	return builder.String()
}

type GraphicsReplyTracker struct {
	mu         sync.Mutex
	pending    bool
	replied    bool
	generation uint64
	deadline   time.Time
	timeout    time.Duration
}

func NewGraphicsReplyTracker(timeout time.Duration) *GraphicsReplyTracker {
	if timeout <= 0 {
		timeout = 150 * time.Millisecond
	}
	return &GraphicsReplyTracker{timeout: timeout}
}

func (t *GraphicsReplyTracker) Start() {
	t.Begin(t.generation)
}

func (t *GraphicsReplyTracker) Begin(generation uint64) {
	t.mu.Lock()
	t.pending = true
	t.replied = false
	t.generation = generation
	t.deadline = time.Now().Add(t.timeout)
	t.mu.Unlock()
}

func (t *GraphicsReplyTracker) Observe(sequence string) bool {
	t.mu.Lock()
	generation := t.generation
	t.mu.Unlock()
	return t.ObserveGeneration(sequence, generation)
}

func (t *GraphicsReplyTracker) ObserveGeneration(sequence string, generation uint64) bool {
	if !isKittyGraphicsSequence(sequence) {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if generation != t.generation {
		return true
	}
	ok, matched := parseKittyGraphicsReply(sequence, -1)
	if matched && ok {
		t.replied = true
		t.pending = false
	}
	return true
}

func (t *GraphicsReplyTracker) Expire() {
	t.mu.Lock()
	t.pending = false
	t.mu.Unlock()
}

func (t *GraphicsReplyTracker) Pending() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.pending
}

func (t *GraphicsReplyTracker) Replied() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.replied
}

func (t *GraphicsReplyTracker) Supported() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.replied
}

func (t *GraphicsReplyTracker) SupportedFor(generation uint64) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.replied && t.generation == generation
}

func (t *GraphicsReplyTracker) Generation() uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.generation
}

func (t *GraphicsReplyTracker) TimedOut() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.pending && time.Now().After(t.deadline)
}
