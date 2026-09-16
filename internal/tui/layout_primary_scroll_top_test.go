package tui

import (
	"strings"
	"testing"
)

type renderMutatingComponent struct {
	lines    []string
	onRender func()
}

func (m *renderMutatingComponent) Render(int) []string {
	if m.onRender != nil {
		m.onRender()
	}
	return m.lines
}

func (m *renderMutatingComponent) Invalidate() {}

func TestBindCursorToFrameClampsToCapturedBounds(t *testing.T) {
	if got := bindCursorToFrame(nil, 5); got != 5 {
		t.Fatalf("nil frame cursor = %d, want 5", got)
	}
	if got := bindCursorToFrame(&LayoutFrame{}, 7); got != 7 {
		t.Fatalf("frame without primary cursor = %d, want 7", got)
	}
	view := NewScrollView(&staticComponent{lines: lines(10, 'c')}, ScrollViewOptions{Primary: true})
	if got := bindCursorToFrame(&LayoutFrame{Root: &LayoutBox{}, PrimaryScrollView: view}, 9); got != 9 {
		t.Fatalf("frame without box cursor = %d, want 9", got)
	}
	frame := RenderLayoutFrame(view, 20, 4, func() {})
	if got := bindCursorToFrame(frame, 99); got != 6 {
		t.Fatalf("clamped cursor = %d, want 6", got)
	}
	if got := bindCursorToFrame(frame, -3); got != 0 {
		t.Fatalf("floor cursor = %d, want 0", got)
	}
}

func TestLayoutFramePrimaryScrollTopMatchesVisibleSlice(t *testing.T) {
	content := lines(50, 'c')
	view := NewScrollView(&staticComponent{lines: content}, ScrollViewOptions{Follow: "end", Primary: true})
	mutator := &renderMutatingComponent{lines: []string{"mutator"}}
	mutator.onRender = func() { view.ScrollTo(0, ScrollToOptions{}) }
	root := &fakeStack{spec: StackLayoutSpec{Vertical: true, Entries: []StackLayoutEntry{
		{Component: view},
		{Component: mutator, Options: StackEntryOptions{Basis: intPtr(1)}},
	}}}

	frame := RenderLayoutFrame(root, 20, 10, func() {})
	if frame.PrimaryScrollView != view {
		t.Fatal("primary scroll view not captured")
	}
	box := GetScrollViewBox(frame, view)
	if box == nil || len(box.Children) == 0 {
		t.Fatal("scroll view box not found")
	}
	sliceOffset := box.Rect.Y - box.Children[0].Rect.Y
	if sliceOffset != 40 {
		t.Fatalf("visible slice offset = %d, want 40", sliceOffset)
	}
	if frame.PrimaryScrollTop != sliceOffset {
		t.Fatalf("primary scroll top = %d, visible slice offset = %d", frame.PrimaryScrollTop, sliceOffset)
	}
	if view.ScrollTop() != 0 {
		t.Fatalf("live scroll top = %d, want 0 after render mutation", view.ScrollTop())
	}
	if strings.TrimRight(frame.Lines[0], " ") != content[sliceOffset] {
		t.Fatalf("first visible line = %q, want %q", frame.Lines[0], content[sliceOffset])
	}
}
