package cli

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/tui"
)

const (
	metaHostileOSCTitle   = "\x1b]0;pwned-title\x07"
	metaHostileCSIPrivate = "\x1b[?9001h"
	metaHostileDCS        = "\x1bP1$pwned-dcs\x1b\\"
	metaHostileAPC        = "\x1b_pwned-apc\x07"
	metaHostileC1CSI      = "\u009b38;5;196m"
	metaHostileC0Run      = "\x07\x0b\x1c\x07"

	metaHostileLabel      = "safe" + metaHostileCSIPrivate + "red" + metaHostileDCS + "\nline " + metaHostileC0Run + "tail"
	metaHostileKind       = "op" + metaHostileC1CSI + "aque"
	metaHostileTimestamp  = "2012" + metaHostileOSCTitle + "-10" + metaHostileAPC + "-10"
	metaHostileName       = "renamed " + metaHostileOSCTitle + "hostile"
	metaHostileCustomType = "note" + metaHostileCSIPrivate + "kind"
	metaHostileHeaderTime = "2030" + metaHostileOSCTitle + "-05" + metaHostileAPC + "-05"
	metaCycleIDOne        = "cy1" + metaHostileC1CSI + "a"
	metaCycleIDTwo        = "cy2" + metaHostileC1CSI + "b"
)

var metaHostileRawPayloads = []string{
	"\x1b]0;pwned-title",
	"\x1b[?9001",
	"\x1bP1$pwned-dcs",
	"\x1b_pwned-apc",
	"\u009b38;5;196m",
	"\x0b",
	"\x1c",
}

var metaOSC52Pattern = regexp.MustCompile(`\x1b]52;c;([A-Za-z0-9+/=]+)`)

func metaJSONValue(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal hostile metadata value: %v", err)
	}
	return string(encoded)
}

func assertNoInjectedMetadata(t *testing.T, output string) {
	t.Helper()
	for _, payload := range metaHostileRawPayloads {
		if strings.Contains(output, payload) {
			t.Fatalf("terminal output carries the injected payload %q:\n%q", payload, output)
		}
	}
}

func assertMetadataResidue(t *testing.T, output string, want []string) {
	t.Helper()
	plain := tui.StripTerminalSequences(output)
	for _, fragment := range want {
		if !strings.Contains(plain, fragment) {
			t.Errorf("terminal output is missing the sanitized residue %q:\n%s", fragment, plain)
		}
	}
}

func writeHostileMetadataSessions(t *testing.T, fixture *bridgeFixture) (resumable, statusOnly string) {
	t.Helper()
	dir, err := fixture.store.DirForCwd(fixture.cwd)
	if err != nil {
		t.Fatal(err)
	}
	resumable = filepath.Join(dir, "hostile-resume.jsonl")
	statusOnly = filepath.Join(dir, "hostile-status.jsonl")
	message := func(id, parent, timestamp, text string) string {
		line := `{"type":"message","id":` + metaJSONValue(t, id)
		if parent != "" {
			line += `,"parentId":` + metaJSONValue(t, parent)
		}
		return line + `,"timestamp":"` + timestamp + `","message":{"role":"user","content":` + metaJSONValue(t, text) + `}}`
	}
	resumableLines := []string{
		`{"type":"session","version":3,"id":"01a0acc5-0487-793d-8a82-f3a26b3f089f","timestamp":"2026-01-01T00:00:00.000Z","cwd":"` + fixture.cwd + `"}`,
		message("m1", "", "2026-01-01T00:00:01.000Z", "root question"),
		`{"type":"label","id":"lb1","parentId":"m1","timestamp":"2026-01-01T00:00:02.000Z","targetId":"m1","label":` + metaJSONValue(t, metaHostileLabel) + `}`,
		`{"type":` + metaJSONValue(t, metaHostileKind) + `,"id":"op1","parentId":"m1","timestamp":` + metaJSONValue(t, metaHostileTimestamp) + `,"data":"opaque payload"}`,
		`{"type":"custom","id":"cu1","parentId":"m1","timestamp":"2026-01-01T00:00:03.000Z","customType":` + metaJSONValue(t, metaHostileCustomType) + `,"data":"custom payload"}`,
		message(metaCycleIDOne, metaCycleIDTwo, "2026-01-01T00:00:04.000Z", "cycle a"),
		message(metaCycleIDTwo, metaCycleIDOne, "2026-01-01T00:00:05.000Z", "cycle b"),
		message("m2", "cu1", "2026-01-01T00:00:06.000Z", "active leaf"),
		`{"type":"session_info","id":"si1","parentId":"m2","timestamp":"2026-01-01T00:00:07.000Z","name":` + metaJSONValue(t, metaHostileName) + `}`,
	}
	statusOnlyLines := []string{
		`{"type":"session","version":3,"id":"02a0acc5-0487-793d-8a82-f3a26b3f089f","timestamp":` + metaJSONValue(t, metaHostileHeaderTime) + `,"cwd":"` + fixture.cwd + `"}`,
		message("s1", "", "2026-01-02T00:00:01.000Z", "status only"),
	}
	if err := os.WriteFile(resumable, []byte(strings.Join(resumableLines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statusOnly, []byte(strings.Join(statusOnlyLines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return resumable, statusOnly
}

func TestBridgeTreeCommandSanitizesHostileSessionMetadata(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	path, _ := writeHostileMetadataSessions(t, fixture)
	fixture.bridge.handle("/resume " + path)
	if fixture.bridge.rd.sessionPath != path {
		t.Fatalf("resumed path = %s, want %s", fixture.bridge.rd.sessionPath, path)
	}
	waitForOutputSettled(t, fixture.terminal, "session renamed to", 3*time.Second)
	done := make(chan struct{})
	go func() {
		defer close(done)
		fixture.bridge.handle("/tree")
	}()
	waitForOutputSettled(t, fixture.terminal, "Session tree", 3*time.Second)
	assertNoInjectedMetadata(t, fixture.terminal.Output())
	assertMetadataResidue(t, fixture.terminal.Output(), []string{"opaque", "safered line tail", "2012-10-10", "renamed hostile"})
	fixture.terminal.SendInput("\x1b")
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("tree browser did not close")
	}
	settled := waitForOutputSettled(t, fixture.terminal, "unreachable entry", 3*time.Second)
	assertNoInjectedMetadata(t, settled)
	if !strings.Contains(tui.StripTerminalSequences(settled), "unreachable entry cy1a") {
		t.Errorf("the corruption warning lost the sanitized entry id:\n%s", tui.StripTerminalSequences(settled))
	}
}

func TestBridgeSessionsBrowserSanitizesMetadataAndKeepsRawIdentity(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	resumable, _ := writeHostileMetadataSessions(t, fixture)
	done := make(chan error, 1)
	go func() { done <- fixture.bridge.sessionsBrowser() }()
	waitForOutputSettled(t, fixture.terminal, "Sessions", 3*time.Second)
	output := fixture.terminal.Output()
	assertNoInjectedMetadata(t, output)
	assertMetadataResidue(t, output, []string{"renamed hostile", "2030-05-05"})
	if strings.Contains(output, "\x1b]52;") {
		t.Fatalf("clipboard was written before the user-triggered copy:\n%q", output)
	}
	fixture.terminal.SendInput("/")
	fixture.terminal.SendInput("hostile-resume")
	fixture.terminal.SendInput("\r")
	time.Sleep(80 * time.Millisecond)
	fixture.terminal.SendInput("\x1b[B")
	time.Sleep(80 * time.Millisecond)
	fixture.terminal.SendInput("c")
	want := tui.OSC52Clipboard(base64.StdEncoding.EncodeToString([]byte(resumable)))
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(fixture.terminal.Output(), want) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	output = fixture.terminal.Output()
	if !strings.Contains(output, want) {
		t.Fatalf("the sessions copy did not emit the exact raw session path through OSC52:\n%q", output)
	}
	matches := metaOSC52Pattern.FindAllStringSubmatch(output, -1)
	if len(matches) != 1 {
		t.Fatalf("output carries %d OSC52 payloads, want exactly the user-triggered one:\n%q", len(matches), output)
	}
	decoded, err := base64.StdEncoding.DecodeString(matches[0][1])
	if err != nil {
		t.Fatalf("decode OSC52 payload: %v", err)
	}
	if string(decoded) != resumable {
		t.Fatalf("copied payload = %q, want the exact raw session path %q", decoded, resumable)
	}
	fixture.terminal.SendInput("\r")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("sessionsBrowser = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("sessions browser did not close after resume")
	}
	if fixture.bridge.rd.sessionPath != resumable {
		t.Errorf("resumed path = %s, want the exact raw selection %s", fixture.bridge.rd.sessionPath, resumable)
	}
	waitForOutputSettled(t, fixture.terminal, "resumed hostile-resume.jsonl", 3*time.Second)
	assertNoInjectedMetadata(t, fixture.terminal.Output())
	assertMetadataResidue(t, fixture.terminal.Output(), []string{"notekind", "renamed hostile"})
}
