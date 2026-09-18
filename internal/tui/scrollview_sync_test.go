package tui

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestScrollViewScrollbarLifecycle(t *testing.T) {
	var renders atomic.Int64
	view := NewScrollView(&staticComponent{lines: lines(50, 'c')}, ScrollViewOptions{
		Scrollbar:          ScrollbarAuto,
		ScrollbarHideDelay: 5 * time.Millisecond,
	})
	if view.Primary() {
		t.Fatal("scroll view should not be primary by default")
	}
	if view.Overscroll() != "chain" {
		t.Fatalf("overscroll = %q", view.Overscroll())
	}
	if view.Scrollbar() != ScrollbarAuto {
		t.Fatalf("scrollbar = %v", view.Scrollbar())
	}
	view.UpdateLayout(50, 10, func() { renders.Add(1) })

	view.SetScrollbarActive(true)
	view.SetScrollbarActive(true)
	if !view.IsScrollbarActive() {
		t.Fatal("scrollbar should be active")
	}
	if !view.IsScrollbarVisible() {
		t.Fatal("active auto scrollbar should be visible")
	}

	view.SetScrollbarActive(false)
	deadline := time.Now().Add(2 * time.Second)
	for view.IsScrollbarVisible() {
		if time.Now().After(deadline) {
			t.Fatal("auto scrollbar did not expire")
		}
		time.Sleep(time.Millisecond)
	}
	if renders.Load() == 0 {
		t.Fatal("scrollbar activity should request a render")
	}

	view.SetScrollbar(ScrollbarAlways)
	if view.Scrollbar() != ScrollbarAlways || !view.IsScrollbarVisible() {
		t.Fatal("always scrollbar should stay visible")
	}
	view.SetScrollbarActive(false)
	view.SetScrollbar(ScrollbarAuto)
	view.SetScrollbar(ScrollbarAuto)

	view.UpdateLayout(5, 10, func() {})
	view.SetScrollbarActive(true)
	view.SetScrollbarActive(false)
	if view.IsScrollbarVisible() {
		t.Fatal("scrollbar hidden when content fits")
	}
	view.ScrollBy(3)
	if view.ScrollTop() != 0 {
		t.Fatalf("scroll top = %d, want 0", view.ScrollTop())
	}
}

func TestScrollViewScrollbarActivityDuringScroll(t *testing.T) {
	var renders atomic.Int64
	view := NewScrollView(&staticComponent{lines: lines(50, 'c')}, ScrollViewOptions{
		Follow:             "end",
		Scrollbar:          ScrollbarAuto,
		ScrollbarHideDelay: time.Hour,
	})
	view.UpdateLayout(50, 10, func() { renders.Add(1) })
	view.SetScrollbarActive(true)
	view.SetScrollbarActive(false)
	if !view.IsScrollbarVisible() {
		t.Fatal("scrollbar should be transiently visible")
	}
	view.ScrollBy(5)
	if !view.IsScrollbarVisible() {
		t.Fatal("scrolling should keep the scrollbar visible")
	}
	view.ScrollTo(0, ScrollToOptions{})
	if view.ScrollTop() != 0 {
		t.Fatalf("scroll top = %d", view.ScrollTop())
	}
	view.ScrollToStart()
	view.ScrollToEnd()
	if !view.IsFollowingEnd() {
		t.Fatal("scroll to end should follow end")
	}
	if renders.Load() == 0 {
		t.Fatal("scroll operations should request renders")
	}
}

func TestScrollViewScrollbarTransitionsStopPendingTimer(t *testing.T) {
	view := NewScrollView(&staticComponent{lines: lines(50, 'c')}, ScrollViewOptions{
		Scrollbar:          ScrollbarAuto,
		ScrollbarHideDelay: time.Hour,
	})
	view.UpdateLayout(50, 10, func() {})
	view.SetScrollbarActive(true)
	view.SetScrollbar(ScrollbarAuto)
	if !view.IsScrollbarVisible() {
		t.Fatal("active auto scrollbar should be visible")
	}
	view.SetScrollbarActive(false)
	view.SetScrollbar(ScrollbarAlways)
	if view.Scrollbar() != ScrollbarAlways || !view.IsScrollbarVisible() {
		t.Fatal("always scrollbar should stay visible")
	}
}

func TestScrollViewRenderPadsWhenScrollbarReservesColumn(t *testing.T) {
	view := NewScrollView(&staticComponent{lines: []string{"hello"}}, ScrollViewOptions{Scrollbar: ScrollbarAlways})
	view.UpdateLayout(1, 10, func() {})
	rendered := view.Render(20)
	if len(rendered) != 1 {
		t.Fatalf("rendered lines = %d", len(rendered))
	}
	if got := VisibleWidth(rendered[0]); got != 20 {
		t.Fatalf("padded width = %d, want 20", got)
	}
}

func TestScrollViewDispatchMouseOffsetsByScrollTop(t *testing.T) {
	target := &mouseTargetComponent{}
	view := NewScrollView(target, ScrollViewOptions{})
	view.UpdateLayout(50, 10, func() {})
	view.ScrollTo(7, ScrollToOptions{})
	if result := view.dispatchMouse(MouseEvent{Type: MousePress, X: 1, Y: 2, Width: 10, Height: 10}); result == nil {
		t.Fatal("mouse dispatch should reach the child")
	}
}
