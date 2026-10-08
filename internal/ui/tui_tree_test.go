package ui

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/tui"
	"github.com/digitalygo/smidja/internal/tui/interactive"
	"github.com/digitalygo/smidja/sdk"
)

func TestShowTreeBrowserCopiesAndCloses(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	result := make(chan TreeBrowserResult, 1)
	failure := make(chan error, 1)
	go func() {
		value, err := runner.ShowTreeBrowser(context.Background(), interactive.TreeBrowserOptions{
			Title: "Tree",
			Nodes: []interactive.TreeBrowserNode{
				{ID: "a", Kind: "user", Preview: "copy me"},
				{ID: "b", Kind: "assistant", Preview: "second"},
			},
		})
		if err != nil {
			failure <- err
			return
		}
		result <- value
	}()
	waitForDialog(t, runner)
	runner.view.RenderNow(true)
	terminal.SendInput("c")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(terminal.Output(), tui.OSC52Clipboard(base64.StdEncoding.EncodeToString([]byte("copy me")))) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !strings.Contains(terminal.Output(), tui.OSC52Clipboard(base64.StdEncoding.EncodeToString([]byte("copy me")))) {
		t.Fatalf("copy action did not emit an OSC52 payload:\n%q", terminal.Output())
	}
	if runner.dialogs.Active() == false {
		t.Fatal("copy must not close the tree browser")
	}
	terminal.SendInput("\x1b")
	select {
	case err := <-failure:
		t.Fatalf("ShowTreeBrowser error = %v", err)
	case value := <-result:
		if value.Action != interactive.TreeActionClose {
			t.Fatalf("action = %q, want close", value.Action)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ShowTreeBrowser did not resolve")
	}
}

func TestShowTreeBrowserRequiresActiveRunner(t *testing.T) {
	terminal := newFakeUITerminal(80, 24)
	opts := fakeUIRunnerOptions(terminal)
	opts.Home = t.TempDir()
	runner := NewRunner(opts)
	if _, err := runner.ShowTreeBrowser(context.Background(), interactive.TreeBrowserOptions{}); err != sdk.ErrModeUnsupported {
		t.Fatalf("ShowTreeBrowser inactive = %v, want ErrModeUnsupported", err)
	}
	if err := runner.CopyToClipboard("text"); err != sdk.ErrModeUnsupported {
		t.Fatalf("CopyToClipboard inactive = %v, want ErrModeUnsupported", err)
	}
}

func TestCopyToClipboardWritesOSC52(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	if err := runner.CopyToClipboard("clipboard payload"); err != nil {
		t.Fatalf("CopyToClipboard = %v", err)
	}
	want := tui.OSC52Clipboard(base64.StdEncoding.EncodeToString([]byte("clipboard payload")))
	if !strings.Contains(terminal.Output(), want) {
		t.Fatalf("terminal output missing OSC52 payload:\n%q", terminal.Output())
	}
}

func treeBrowserTargetNodes() []interactive.TreeBrowserNode {
	return []interactive.TreeBrowserNode{
		{ID: "hidden", Kind: "notice", Preview: "hidden body"},
		{ID: "keep", Kind: "user", Preview: "keep body"},
		{ID: "target", Kind: "assistant", Label: "target", Preview: "target body", Deletable: true, Renamable: true},
	}
}

func sendSearchedChars(terminal *fakeUITerminal, query string) {
	terminal.SendInput("/")
	time.Sleep(20 * time.Millisecond)
	for _, r := range query {
		terminal.SendInput(string(r))
		time.Sleep(5 * time.Millisecond)
	}
	terminal.SendInput("\r")
	time.Sleep(20 * time.Millisecond)
}

func TestShowTreeBrowserReturnsEmittedVisibleTarget(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	result := make(chan TreeBrowserResult, 1)
	failure := make(chan error, 1)
	go func() {
		value, err := runner.ShowTreeBrowser(context.Background(), interactive.TreeBrowserOptions{
			Title: "Tree",
			Nodes: treeBrowserTargetNodes(),
		})
		if err != nil {
			failure <- err
			return
		}
		result <- value
	}()
	waitForDialog(t, runner)
	runner.view.RenderNow(true)
	sendSearchedChars(terminal, "target")
	terminal.SendInput("\x1b[B")
	time.Sleep(20 * time.Millisecond)
	terminal.SendInput("\r")
	select {
	case err := <-failure:
		t.Fatalf("ShowTreeBrowser error = %v", err)
	case value := <-result:
		if value.Action != interactive.TreeActionSelect || value.EntryID != "target" {
			t.Fatalf("result = %+v, want a select for the searched target", value)
		}
		if value.Node.Preview != "target body" {
			t.Fatalf("result node preview = %q, want the emitted target payload", value.Node.Preview)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ShowTreeBrowser did not resolve")
	}
}

func TestShowTreeBrowserReconcilesHiddenActiveEntry(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	result := make(chan TreeBrowserResult, 1)
	failure := make(chan error, 1)
	go func() {
		value, err := runner.ShowTreeBrowser(context.Background(), interactive.TreeBrowserOptions{
			Title:         "Tree",
			Nodes:         treeBrowserTargetNodes(),
			ActiveEntryID: "hidden",
		})
		if err != nil {
			failure <- err
			return
		}
		result <- value
	}()
	waitForDialog(t, runner)
	runner.view.RenderNow(true)
	terminal.SendInput("\r")
	select {
	case err := <-failure:
		t.Fatalf("ShowTreeBrowser error = %v", err)
	case value := <-result:
		if value.Action != interactive.TreeActionSelect || value.EntryID != "keep" {
			t.Fatalf("result = %+v, want a select for the reconciled visible row keep", value)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ShowTreeBrowser did not resolve")
	}
}
