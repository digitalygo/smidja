package tui

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestKittyChunkedTransmitSyntax(t *testing.T) {
	payload := make([]byte, 4000)
	for index := range payload {
		payload[index] = byte(index)
	}
	sequence := KittyTransmit(7, 3, 12, 6, payload)
	if strings.Contains(sequence, "\x1b_G,") {
		t.Fatalf("chunk continuation must not start with a comma: %q", sequence)
	}
	chunks := strings.Split(strings.TrimSuffix(sequence, ANSIST), ANSIST)
	if len(chunks) < 2 {
		t.Fatalf("expected chunked output, got %d chunks", len(chunks))
	}
	first := chunks[0]
	if !strings.HasPrefix(first, "\x1b_Ga=T,f=100,i=7,p=3,c=12,r=6,C=1,q=2,m=1;") {
		t.Fatalf("unexpected first chunk header: %q", first)
	}
	for index, chunk := range chunks[1:] {
		header, encoded, ok := strings.Cut(strings.TrimPrefix(chunk, "\x1b_G"), ";")
		if !ok {
			t.Fatalf("chunk %d missing payload separator: %q", index, chunk)
		}
		want := ",m=1"
		if index == len(chunks)-2 {
			want = ",m=0"
		}
		if header != strings.TrimPrefix(want, ",") {
			t.Fatalf("chunk %d header = %q, want %q", index, header, strings.TrimPrefix(want, ","))
		}
		if strings.Contains(header, "a=T") || strings.Contains(header, "f=100") {
			t.Fatalf("continuation chunk repeated control keys: %q", header)
		}
		if len(encoded) > kittyGraphicsPayloadChunk {
			t.Fatalf("chunk %d exceeds the payload limit: %d", index, len(encoded))
		}
		if index < len(chunks)-2 && len(encoded)%4 != 0 {
			t.Fatalf("non-final chunk %d is not a multiple of 4: %d", index, len(encoded))
		}
	}
}

func TestKittyDeleteAndFreeCommands(t *testing.T) {
	if got := KittyDeletePlacement(5, 7); got != "\x1b_Ga=d,d=i,i=5,p=7"+ANSIST {
		t.Fatalf("delete placement = %q", got)
	}
	if got := KittyFreeImage(5); got != "\x1b_Ga=d,d=I,i=5"+ANSIST {
		t.Fatalf("free image = %q", got)
	}
	if got := KittyFreeAll(); got != "\x1b_Ga=d,d=A"+ANSIST {
		t.Fatalf("free all = %q", got)
	}
}

func TestKittyGraphicsReplyMatching(t *testing.T) {
	if _, matched := parseKittyGraphicsReply("\x1b_Gi=31;OK\x1b\\", 31); !matched {
		t.Fatal("matching id should match")
	}
	if _, matched := parseKittyGraphicsReply("\x1b_Gi=31;OK\x1b\\", 32); matched {
		t.Fatal("wrong id must not match")
	}
	if ok, matched := parseKittyGraphicsReply("\x1b_Gi=31;ENOENT\x1b\\", 31); !matched || ok {
		t.Fatalf("error reply matched=%v ok=%v", matched, ok)
	}
	if _, matched := parseKittyGraphicsReply("\x1b_Gi=31;OK", 31); matched {
		t.Fatal("unterminated reply must not match")
	}
	if _, matched := parseKittyGraphicsReply("\x1b_G"+strings.Repeat("x", kittyGraphicsReplyMaxLen)+"\x1b\\", 31); matched {
		t.Fatal("oversized reply must not match")
	}
}

func TestGraphicsReplyTrackerGenerationScope(t *testing.T) {
	tracker := NewGraphicsReplyTracker(20 * time.Millisecond)
	tracker.Begin(4)
	if !tracker.ObserveGeneration("\x1b_Gi=31;OK\x1b\\", 4) {
		t.Fatal("graphics reply must be consumed")
	}
	if !tracker.SupportedFor(4) {
		t.Fatal("matching generation should mark support")
	}

	fresh := NewGraphicsReplyTracker(20 * time.Millisecond)
	fresh.Begin(9)
	if !fresh.ObserveGeneration("\x1b_Gi=31;OK\x1b\\", 8) {
		t.Fatal("stale generation reply must still be consumed")
	}
	if fresh.SupportedFor(9) || fresh.Supported() {
		t.Fatal("stale generation reply must not enable graphics")
	}
	if fresh.Observe("ordinary input") {
		t.Fatal("non-graphics input must not be consumed")
	}

	timed := NewGraphicsReplyTracker(time.Millisecond)
	timed.Begin(1)
	time.Sleep(5 * time.Millisecond)
	if !timed.TimedOut() {
		t.Fatal("expected probe timeout")
	}
	if timed.Supported() {
		t.Fatal("timeout must not enable graphics")
	}
}

func TestProcessTerminalQueryKittyGraphics(t *testing.T) {
	cases := []struct {
		name      string
		reply     string
		respond   bool
		timeout   time.Duration
		supported bool
	}{
		{name: "success", reply: "\x1b_Gi=31;OK\x1b\\", respond: true, timeout: 500 * time.Millisecond, supported: true},
		{name: "error", reply: "\x1b_Gi=31;ENOENT\x1b\\", respond: true, timeout: 500 * time.Millisecond, supported: false},
		{name: "wrong id", reply: "\x1b_Gi=99;OK\x1b\\", respond: true, timeout: 30 * time.Millisecond, supported: false},
		{name: "timeout", respond: false, timeout: 30 * time.Millisecond, supported: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			terminal, stdinWrite, _, drain := newTestTerminal(t)
			defer stdinWrite.Close()
			if err := terminal.Start(func(string) {}, func() {}); err != nil {
				t.Fatalf("Start() error = %v", err)
			}
			drain()
			if testCase.respond {
				go func() {
					time.Sleep(5 * time.Millisecond)
					stdinWrite.WriteString(testCase.reply)
				}()
			}
			supported := terminal.QueryKittyGraphics(testCase.timeout)
			if supported != testCase.supported {
				t.Fatalf("QueryKittyGraphics() = %v, want %v", supported, testCase.supported)
			}
			if !strings.Contains(drain(), KittyGraphicsProbe) {
				t.Fatalf("probe sequence was not written")
			}
		})
	}
}

func TestProcessTerminalConsumesGraphicsRepliesWithoutInput(t *testing.T) {
	terminal, stdinWrite, _, _ := newTestTerminal(t)
	defer stdinWrite.Close()
	var mu sync.Mutex
	var inputs []string
	if err := terminal.Start(func(data string) {
		mu.Lock()
		inputs = append(inputs, data)
		mu.Unlock()
	}, func() {}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	terminal.dispatchSequence("\x1b_Gi=31;OK\x1b\\")
	terminal.dispatchSequence("\x1b_Gi=31;ENOENT\x1b\\")
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(inputs) != 0 {
		t.Fatalf("graphics replies leaked into input: %v", inputs)
	}
}
