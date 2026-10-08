package interactive

import (
	"strings"
	"testing"
)

const (
	hostileOSCTitle   = "\x1b]0;pwned-title\x07"
	hostileCSIPrivate = "\x1b[?9001h"
	hostileDCS        = "\x1bP1$pwned-dcs\x1b\\"
	hostileAPC        = "\x1b_pwned-apc\x07"
	hostileC1CSI      = "\u009b38;5;196m"
	hostileC0Run      = "\x07\x0b\x1c\x07"
)

var hostileRawPayloads = []string{
	"\x1b]0;pwned-title",
	"\x1b[?9001",
	"\x1bP1$pwned-dcs",
	"\x1b_pwned-apc",
	"\u009b38;5;196m",
	"\x0b",
	"\x1c",
}

func hostileTreeNodes() []TreeBrowserNode {
	return []TreeBrowserNode{
		{
			ID:        "raw" + hostileOSCTitle + "entry-id",
			Depth:     0,
			Kind:      "op" + hostileC1CSI + "aque",
			Label:     "safe" + hostileCSIPrivate + "red" + hostileDCS + "\nline " + hostileC0Run + "tail",
			Timestamp: "2026" + hostileOSCTitle + "-01" + hostileAPC + "-01",
			Preview:   "payload" + hostileC0Run + "keep" + hostileOSCTitle + "visible",
			Leaf:      true,
		},
	}
}

func assertFrameHasNoControlBytes(t *testing.T, lines []string) {
	t.Helper()
	for index, line := range lines {
		if strings.ContainsAny(line, "\x1b\n\r") {
			t.Errorf("line %d contains an escape, newline, or carriage return: %q", index, line)
		}
		for _, r := range line {
			if isControlRune(r) {
				t.Errorf("line %d contains control rune %U: %q", index, r, line)
				break
			}
		}
	}
	assertFrameHasNoInjectedPayload(t, lines)
}

func assertFrameHasNoInjectedPayload(t *testing.T, lines []string) {
	t.Helper()
	joined := strings.Join(lines, "\n")
	for _, payload := range hostileRawPayloads {
		if strings.Contains(joined, payload) {
			t.Errorf("frame leaks the hostile payload %q:\n%s", payload, joined)
		}
	}
}

func TestTreeBrowserSanitizesHostileMetadataBeforeStyling(t *testing.T) {
	browser := NewTreeBrowser(TreeBrowserOptions{
		Title: "Tree" + hostileOSCTitle,
		Nodes: hostileTreeNodes(),
	}, NewDialogTheme(nil), nil)
	lines := browser.Render(80)
	if len(lines) == 0 {
		t.Fatal("no render output")
	}
	assertFrameHasNoControlBytes(t, lines)
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"opaque", "safered line tail", "2026-01-01", "payloadkeepvisible"} {
		if !strings.Contains(joined, want) {
			t.Errorf("frame is missing the sanitized residue %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "pwned-title") || strings.Contains(joined, "pwned-dcs") || strings.Contains(joined, "pwned-apc") {
		t.Errorf("frame leaked hostile payload text:\n%s", joined)
	}
}

func TestTreeBrowserHostileMetadataStaysCleanUnderTrustedStyling(t *testing.T) {
	browser := NewTreeBrowser(TreeBrowserOptions{
		Title: "Tree",
		Nodes: hostileTreeNodes(),
	}, NewDialogTheme(mustTheme(t)), nil)
	lines := browser.Render(80)
	if len(lines) == 0 {
		t.Fatal("no render output")
	}
	assertFrameHasNoInjectedPayload(t, lines)
}

func TestTreeBrowserCollapsesHostileMetadataToSingleRow(t *testing.T) {
	browser := NewTreeBrowser(TreeBrowserOptions{
		Title: "Tree",
		Nodes: []TreeBrowserNode{{ID: "n1", Kind: "kind\ninjected", Label: "l1\nl2", Timestamp: "t1\nt2"}},
	}, NewDialogTheme(nil), nil)
	lines := browser.Render(80)
	assertFrameHasNoControlBytes(t, lines)
	if len(lines) != 5 {
		t.Fatalf("frame lines = %d, want 5 (top, row, blank, hint, bottom):\n%s", len(lines), strings.Join(lines, "\n"))
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"kind injected", "(l1 l2)", "t1 t2"} {
		if !strings.Contains(joined, want) {
			t.Errorf("collapsed row is missing %q:\n%s", want, joined)
		}
	}
}

func TestTreeBrowserPreservesRawIdentityForActions(t *testing.T) {
	rawID := "raw" + hostileOSCTitle + "entry-id"
	rawPreview := "raw" + hostileC0Run + "payload" + hostileDCS
	nodes := []TreeBrowserNode{{
		ID:        rawID,
		Kind:      "user" + hostileC1CSI,
		Preview:   rawPreview,
		Leaf:      true,
		Deletable: true,
		Renamable: true,
	}}
	kinds := []string{"c", "d", "r", "\r"}
	for _, key := range kinds {
		var actions []TreeBrowserAction
		browser := NewTreeBrowser(TreeBrowserOptions{Title: "Tree", Nodes: nodes}, NewDialogTheme(nil), func(action TreeBrowserAction) {
			actions = append(actions, action)
		})
		browser.HandleInput(key)
		if len(actions) != 1 {
			t.Fatalf("key %q actions = %+v, want exactly one action", key, actions)
		}
		if actions[0].EntryID != rawID {
			t.Errorf("key %q action entry id = %q, want the raw id %q", key, actions[0].EntryID, rawID)
		}
		if actions[0].Node.ID != rawID || actions[0].Node.Preview != rawPreview {
			t.Errorf("key %q action node = %+v, want the raw snapshot", key, actions[0].Node)
		}
	}
	browser := NewTreeBrowser(TreeBrowserOptions{Title: "Tree", Nodes: nodes}, NewDialogTheme(nil), nil)
	if got := browser.SelectedPreview(); got != rawPreview {
		t.Errorf("preview = %q, want the exact raw copy payload %q", got, rawPreview)
	}
	if got := browser.SelectedID(); got != rawID {
		t.Errorf("selected id = %q, want the exact raw id %q", got, rawID)
	}
	ids := browser.VisibleIDs()
	if len(ids) != 1 || ids[0] != rawID {
		t.Errorf("visible ids = %q, want only the exact raw id %q", ids, rawID)
	}
}

func TestTreeBrowserSearchStatusRejectsControlSequences(t *testing.T) {
	browser := NewTreeBrowser(TreeBrowserOptions{Title: "Tree", Nodes: hostileTreeNodes()}, NewDialogTheme(nil), nil)
	browser.HandleInput("/")
	browser.HandleInput(hostileOSCTitle)
	browser.HandleInput(hostileCSIPrivate)
	browser.HandleInput(hostileC1CSI)
	browser.HandleInput("x")
	if got := browser.Search(); got != "x" {
		t.Fatalf("search = %q, want x", got)
	}
	browser.HandleInput("\r")
	lines := browser.Render(80)
	assertFrameHasNoInjectedPayload(t, lines)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "search: x") {
		t.Errorf("browser status line is missing the sanitized search value:\n%s", joined)
	}
}
