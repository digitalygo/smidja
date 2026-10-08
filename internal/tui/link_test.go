package tui

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestDetectGraphicsProtocolConservative(t *testing.T) {
	env := func(values map[string]string) func(string) string {
		return func(key string) string { return values[key] }
	}
	if got := DetectGraphicsProtocol(env(map[string]string{"TERM": "xterm-256color"})); got != GraphicsNone {
		t.Fatalf("unknown terminal should be conservative, got %v", got)
	}
	if got := DetectGraphicsProtocol(env(map[string]string{"TERM": "xterm-kitty"})); got != GraphicsKitty {
		t.Fatalf("kitty term should be detected, got %v", got)
	}
	if got := DetectGraphicsProtocol(env(map[string]string{"TERM_PROGRAM": "iTerm.app"})); got != GraphicsITerm2 {
		t.Fatalf("iterm should be detected, got %v", got)
	}
	if got := DetectGraphicsProtocol(env(map[string]string{"TERM": "xterm-kitty", "TMUX": "1"})); got != GraphicsNone {
		t.Fatalf("multiplexer should suppress graphics, got %v", got)
	}
	if got := DetectGraphicsProtocol(env(map[string]string{"TERM": "xterm-256color"})); got != GraphicsNone {
		t.Fatalf("keyboard support must not imply graphics, got %v", got)
	}
	if got := DetectGraphicsProtocol(env(map[string]string{"KITTY_WINDOW_ID": "1"})); got != GraphicsKitty {
		t.Fatal("KITTY_WINDOW_ID should imply kitty")
	}
}

func TestKittyGraphicsReplyParsing(t *testing.T) {
	if !ParseKittyGraphicsReply("\x1b_Gi=31;OK\x1b\\") {
		t.Fatal("valid reply not parsed")
	}
	if ParseKittyGraphicsReply("\x1b_Gi=31;ENOENT\x1b\\") {
		t.Fatal("error reply should not count as support")
	}
	for _, malformed := range []string{"", "OK", "\x1b_G", "\x1b_G\x1b\\"} {
		if ParseKittyGraphicsReply(malformed) {
			t.Fatalf("malformed reply %q accepted", malformed)
		}
	}
}

func TestGraphicsCapabilityDisableWins(t *testing.T) {
	capability := GraphicsCapability{Protocol: GraphicsKitty, Enabled: true}
	if !capability.Available() {
		t.Fatal("kitty should be available")
	}
	if capability.Disable().Available() {
		t.Fatal("disable should win over protocol")
	}
	if (GraphicsCapability{Protocol: GraphicsNone, Enabled: true}).Available() {
		t.Fatal("none protocol should not be available")
	}
}

func TestKittyProtocolStrings(t *testing.T) {
	transmit := KittyTransmit(7, 2, 10, 5, []byte("payload"))
	if !strings.Contains(transmit, "a=T") || !strings.Contains(transmit, "i=7") || !strings.Contains(transmit, "c=10") {
		t.Fatalf("transmit malformed: %q", transmit)
	}
	chunked := KittyTransmit(1, 1, 1, 1, make([]byte, 8192))
	if strings.Count(chunked, "\x1b_G") < 2 {
		t.Fatal("large payload should be chunked")
	}
	if !strings.Contains(chunked, "\x1b_Gm=1;") || !strings.Contains(chunked, "\x1b_Gm=0;") {
		t.Fatalf("chunk continuation flags missing: %q", chunked)
	}
	if strings.Contains(chunked, "\x1b_G,") {
		t.Fatalf("chunk continuation must not start with a comma: %q", chunked)
	}
	if !strings.HasPrefix(KittyDeleteImage(4), "\x1b_Ga=d,d=i,i=4") {
		t.Fatal("delete image malformed")
	}
	if !strings.HasPrefix(KittyDeletePlacement(4, 5), "\x1b_Ga=d,d=i,i=4,p=5") {
		t.Fatal("delete placement malformed")
	}
	if KittyDeleteAll() != "\x1b_Ga=d\x1b\\" {
		t.Fatal("delete all malformed")
	}
	iterm := ITerm2Inline("a.png", []byte("data"), 3, 4)
	if !strings.HasPrefix(iterm, "\x1b]1337;File=") || !strings.HasSuffix(iterm, "\x07") {
		t.Fatalf("iterm malformed: %q", iterm)
	}
	if !strings.Contains(iterm, "inline=1:") {
		t.Fatal("iterm inline flag missing")
	}
}

func TestCommandLinkOpenerRejectsUnsafeTarget(t *testing.T) {
	calls := 0
	opener := &CommandLinkOpener{Command: "echo"}
	openerOpen := func(target string) error {
		calls++
		return opener.Open(context.Background(), target)
	}
	for _, target := range []string{"javascript:alert(1)", "file:///etc/passwd", "data:text/html,x", "", "ftp://x"} {
		if err := openerOpen(target); !errors.Is(err, ErrUnsafeLink) {
			t.Fatalf("target %q should be rejected, got %v", target, err)
		}
	}
	if calls != 5 {
		t.Fatalf("unexpected calls %d", calls)
	}
}

func TestCommandLinkOpenerRunsWithoutShell(t *testing.T) {
	dir := t.TempDir()
	script := dir + "/open.sh"
	if err := writeExecutable(script, "#!/bin/sh\nexit 0\n"); err != nil {
		t.Fatal(err)
	}
	opener := &CommandLinkOpener{Command: script, Timeout: time.Second}
	if err := opener.Open(context.Background(), "https://example.com"); err != nil {
		t.Fatalf("opener failed: %v", err)
	}
	missing := &CommandLinkOpener{Command: dir + "/missing", Timeout: time.Second}
	if err := missing.Open(context.Background(), "https://example.com"); err == nil {
		t.Fatal("missing command should fail silently with an error")
	}
}

func TestLinkOpenerFuncInjection(t *testing.T) {
	opened := ""
	opener := LinkOpenerFunc(func(ctx context.Context, target string) error {
		opened = target
		return nil
	})
	if err := opener.Open(context.Background(), "https://example.com"); err != nil {
		t.Fatal(err)
	}
	if opened != "https://example.com" {
		t.Fatalf("injected opener not used: %q", opened)
	}
}

func TestExtractLinkHits(t *testing.T) {
	line := "see " + OSC8Hyperlink("", "https://example.com") + "site" + OSC8Close + " now"
	hits := ExtractLinkHits(line, 3)
	if len(hits) != 1 {
		t.Fatalf("expected one hit, got %+v", hits)
	}
	hit := hits[0]
	if hit.URL != "https://example.com" || hit.Start != 4 || hit.End != 8 || hit.Row != 3 {
		t.Fatalf("unexpected hit %+v", hit)
	}
	if url, ok := LinkAt(line, 3, 5); !ok || url != "https://example.com" {
		t.Fatalf("LinkAt missed: %q %v", url, ok)
	}
	if _, ok := LinkAt(line, 3, 0); ok {
		t.Fatal("LinkAt should miss outside the link")
	}
}

func TestLinkAtRejectsUnsafeTargets(t *testing.T) {
	line := OSC8Hyperlink("", "javascript:alert(1)") + "bad" + OSC8Close
	if _, ok := LinkAt(line, 0, 0); ok {
		t.Fatal("unsafe OSC8 target should not be clickable")
	}
	line = OSC8Hyperlink("", "mailto:a@b.c") + "mail" + OSC8Close
	if url, ok := LinkAt(line, 0, 0); !ok || url != "mailto:a@b.c" {
		t.Fatalf("mailto should be clickable: %q %v", url, ok)
	}
}

func TestExtractLinkHitsWideRunes(t *testing.T) {
	line := "世界" + OSC8Hyperlink("", "https://x.test") + "リンク" + OSC8Close
	hits := ExtractLinkHits(line, 0)
	if len(hits) != 1 || hits[0].Start != 4 || hits[0].End != 10 {
		t.Fatalf("wide rune columns wrong: %+v", hits)
	}
}

func TestIsSafeLinkTargetBounds(t *testing.T) {
	if !IsSafeLinkTarget("https://example.com/a") {
		t.Fatal("valid https rejected")
	}
	if IsSafeLinkTarget("HTTPS://") {
		t.Fatal("empty authority should be rejected")
	}
	long := "https://example.com/" + strings.Repeat("a", maxLinkTargetLength)
	if IsSafeLinkTarget(long) {
		t.Fatal("oversized target should be rejected")
	}
	if IsSafeLinkTarget("https://example.com/\x1b]0;x\x07") {
		t.Fatal("control characters should be rejected")
	}
}

func writeExecutable(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o755)
}
