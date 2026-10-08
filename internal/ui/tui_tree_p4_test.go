package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/tui"
	"github.com/digitalygo/smidja/internal/tui/interactive"
)

func p4TreeNodes() []interactive.TreeBrowserNode {
	return []interactive.TreeBrowserNode{
		{ID: "u1", Kind: "user", Preview: "first", Timestamp: "2026-01-01T00:00:01.000Z"},
		{ID: "a1", ParentID: "u1", Kind: "assistant", Preview: "answer", Timestamp: "2026-01-01T00:00:02.000Z"},
		{ID: "t1", ParentID: "a1", Kind: "tool", Preview: "tool out", Timestamp: "2026-01-01T00:00:03.000Z"},
		{ID: "u2", ParentID: "a1", Kind: "user", Preview: "follow", Timestamp: "2026-01-01T00:00:04.000Z", Label: "keep", LabelTimestamp: "2026-01-01T00:00:05.000Z", Leaf: true},
	}
}

func TestP4ShowTreeBrowserLabelSnapshot(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	result := make(chan TreeBrowserResult, 1)
	failure := make(chan error, 1)
	go func() {
		value, err := runner.ShowTreeBrowser(context.Background(), interactive.TreeBrowserOptions{
			Title: "Tree",
			Nodes: p4TreeNodes(),
		})
		if err != nil {
			failure <- err
			return
		}
		result <- value
	}()
	waitForDialog(t, runner)
	runner.view.RenderNow(true)
	time.Sleep(30 * time.Millisecond)
	terminal.SendInput("l")
	select {
	case err := <-failure:
		t.Fatalf("ShowTreeBrowser error = %v", err)
	case value := <-result:
		if value.Action != interactive.TreeActionLabel {
			t.Fatalf("action = %q, want label", value.Action)
		}
		if value.EntryID != "u2" {
			t.Fatalf("entry = %s, want u2 snapshot", value.EntryID)
		}
		if value.Node.Preview != "follow" || value.Node.Label != "keep" {
			t.Fatalf("node snapshot = %+v, want target payload", value.Node)
		}
		if value.Filter != interactive.TreeFilterDefault {
			t.Fatalf("filter = %q, want default", value.Filter)
		}
		if value.HideTimestamps {
			t.Fatalf("hide timestamps = true, want false by default")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ShowTreeBrowser did not resolve label")
	}
}

func TestP4ShowTreeBrowserTimestampToggleAndFilterPreserved(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	result := make(chan TreeBrowserResult, 1)
	failure := make(chan error, 1)
	go func() {
		value, err := runner.ShowTreeBrowser(context.Background(), interactive.TreeBrowserOptions{
			Title: "Tree",
			Nodes: p4TreeNodes(),
		})
		if err != nil {
			failure <- err
			return
		}
		result <- value
	}()
	waitForDialog(t, runner)
	runner.view.RenderNow(true)
	time.Sleep(30 * time.Millisecond)
	terminal.SendInput("t")
	time.Sleep(30 * time.Millisecond)
	terminal.SendInput("\t")
	time.Sleep(30 * time.Millisecond)
	terminal.SendInput("l")
	select {
	case err := <-failure:
		t.Fatalf("ShowTreeBrowser error = %v", err)
	case value := <-result:
		if !value.HideTimestamps {
			t.Fatalf("hide timestamps = false, want true after toggle")
		}
		if value.Filter != interactive.TreeFilterNoTools {
			t.Fatalf("filter = %q, want no-tools after tab", value.Filter)
		}
		if value.EntryID != "u2" && value.EntryID != "a1" && value.EntryID != "u1" {
			t.Fatalf("entry = %s, want visible target under no-tools", value.EntryID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ShowTreeBrowser did not resolve")
	}
}

func p4DialogFrameWidth(rendered string) int {
	width := -1
	for _, line := range strings.Split(rendered, "\n") {
		start := strings.Index(line, "╭")
		if start < 0 {
			continue
		}
		end := strings.LastIndex(line, "╮")
		if end < start {
			continue
		}
		width = tui.VisibleWidth(line[start : end+len("╮")])
	}
	return width
}

func p4RenderedSelectedRow(rendered, id string) bool {
	for _, line := range strings.Split(rendered, "\n") {
		if strings.Contains(line, "›") && strings.Contains(line, "("+id+")") {
			return true
		}
	}
	return false
}

func p4WaitForRenderAfterResize(t *testing.T, runner *Runner, terminal *fakeUITerminal, mark int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if terminal.WriteCount() > mark {
			time.Sleep(30 * time.Millisecond)
			runner.view.RenderNow(true)
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("resize render never reached the terminal")
}

func TestP4ShowTreeBrowserResizeKeepsSelectionVisible(t *testing.T) {
	var nodes []interactive.TreeBrowserNode
	for i := 0; i < 20; i++ {
		id := "row" + string(rune('a'+i%26)) + string(rune('0'+i%10)) + string(rune('0'+i/10))
		nodes = append(nodes, interactive.TreeBrowserNode{ID: id, Kind: "user", Label: id, Preview: "body " + id})
	}
	last := nodes[len(nodes)-1].ID
	aboveWindow := nodes[0].ID
	firstWindowed := nodes[len(nodes)-12].ID
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	result := make(chan TreeBrowserResult, 1)
	failure := make(chan error, 1)
	go func() {
		value, err := runner.ShowTreeBrowser(context.Background(), interactive.TreeBrowserOptions{
			Title:         "Tree",
			Nodes:         nodes,
			ActiveEntryID: last,
		})
		if err != nil {
			failure <- err
			return
		}
		result <- value
	}()
	waitForDialog(t, runner)
	runner.view.RenderNow(true)
	time.Sleep(30 * time.Millisecond)
	initial := strippedOutput(terminal.Output())
	if !strings.Contains(initial, "Tree [default]") {
		t.Fatalf("initial render missing the tree dialog:\n%s", initial)
	}
	if width := p4DialogFrameWidth(initial); width != 56 {
		t.Fatalf("initial dialog width = %d, want 56 at 80 columns:\n%s", width, initial)
	}
	if !p4RenderedSelectedRow(initial, last) {
		t.Fatalf("initial render missing selected row %s:\n%s", last, initial)
	}
	if !strings.Contains(initial, "body "+last) {
		t.Fatalf("initial render missing active preview %q:\n%s", "body "+last, initial)
	}
	if strings.Contains(initial, "("+aboveWindow+")") {
		t.Fatalf("initial render shows %s above the selection window:\n%s", aboveWindow, initial)
	}

	mark := terminal.WriteCount()
	terminal.SetSize(120, 40)
	p4WaitForRenderAfterResize(t, runner, terminal, mark)
	raw := terminal.OutputSince(mark)
	resized := strippedOutput(raw)
	if !strings.Contains(raw, tui.CursorEraseScreen) || !strings.Contains(raw, tui.CursorHome) {
		t.Fatalf("resize render did not redraw the viewport bounds:\n%s", raw)
	}
	if !strings.Contains(resized, "Tree [default]") {
		t.Fatalf("resized render missing the tree dialog:\n%s", resized)
	}
	if width := p4DialogFrameWidth(resized); width != 84 {
		t.Fatalf("resized dialog width = %d, want 84 at 120 columns:\n%s", width, resized)
	}
	if !p4RenderedSelectedRow(resized, last) {
		t.Fatalf("resized render lost selected row %s:\n%s", last, resized)
	}
	if !strings.Contains(resized, "body "+last) {
		t.Fatalf("resized render missing active preview %q:\n%s", "body "+last, resized)
	}
	if strings.Contains(resized, "("+aboveWindow+")") || !strings.Contains(resized, "("+firstWindowed+")") {
		t.Fatalf("resized viewport window drifted:\n%s", resized)
	}
	terminal.SendInput("\r")
	select {
	case err := <-failure:
		t.Fatalf("ShowTreeBrowser error = %v", err)
	case value := <-result:
		if value.EntryID != last {
			t.Fatalf("entry = %s, want initial selection %s beyond viewport", value.EntryID, last)
		}
		if value.Node.Preview != "body "+last {
			t.Fatalf("node preview = %q, want %q", value.Node.Preview, "body "+last)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ShowTreeBrowser did not resolve")
	}
}
