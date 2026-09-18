package ui

import (
	"context"
	"encoding/base64"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/tui"
	"github.com/digitalygo/smidja/internal/tui/interactive"
)

const (
	hostileClipboardOSCTitle = "\x1b]0;pwned-title\x07"
	hostileClipboardDCS      = "\x1bP1$pwned-dcs\x1b\\"
	hostileClipboardC0Run    = "\x07\x0b\x1c\x07"
)

var hostileClipboardPayloads = []string{
	"\x1b]0;pwned-title",
	"\x1bP1$pwned-dcs",
	"\x0b",
	"\x1c",
}

var osc52PayloadPattern = regexp.MustCompile(`\x1b]52;c;([A-Za-z0-9+/=]+)`)

func TestShowTreeBrowserCopyEmitsExactRawPayloadThroughOSC52(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	rawPreview := "raw" + hostileClipboardC0Run + "payload" + hostileClipboardOSCTitle + hostileClipboardDCS + "tail"
	done := make(chan error, 1)
	go func() {
		_, err := runner.ShowTreeBrowser(context.Background(), interactive.TreeBrowserOptions{
			Title: "Tree",
			Nodes: []interactive.TreeBrowserNode{{ID: "raw-entry", Kind: "user", Preview: rawPreview, Leaf: true}},
		})
		done <- err
	}()
	waitForDialog(t, runner)
	runner.view.RenderNow(true)
	time.Sleep(20 * time.Millisecond)
	if strings.Contains(terminal.Output(), "\x1b]52;") {
		t.Fatalf("clipboard was written before the user-triggered copy:\n%q", terminal.Output())
	}
	terminal.SendInput("c")
	want := tui.OSC52Clipboard(base64.StdEncoding.EncodeToString([]byte(rawPreview)))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(terminal.Output(), want) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	output := terminal.Output()
	if !strings.Contains(output, want) {
		t.Fatalf("copy did not emit the exact raw payload through OSC52:\n%q", output)
	}
	matches := osc52PayloadPattern.FindAllStringSubmatch(output, -1)
	if len(matches) != 1 {
		t.Fatalf("output carries %d OSC52 payloads, want exactly the user-triggered one:\n%q", len(matches), output)
	}
	decoded, err := base64.StdEncoding.DecodeString(matches[0][1])
	if err != nil {
		t.Fatalf("decode OSC52 payload: %v", err)
	}
	if string(decoded) != rawPreview {
		t.Fatalf("copied payload = %q, want the exact raw source %q", decoded, rawPreview)
	}
	for _, payload := range hostileClipboardPayloads {
		if strings.Contains(output, payload) {
			t.Errorf("terminal output carries the raw hostile payload %q outside the OSC52 encoding:\n%q", payload, output)
		}
	}
	terminal.SendInput("\x1b")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ShowTreeBrowser error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ShowTreeBrowser did not resolve")
	}
}
