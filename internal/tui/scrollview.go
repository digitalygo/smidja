package tui

import "time"

type ScrollbarMode int

const (
	ScrollbarHidden ScrollbarMode = iota
	ScrollbarAuto
	ScrollbarAlways
)

type ScrollViewOptions struct {
	Follow              string
	Primary             bool
	Overscroll          string
	Scrollbar           ScrollbarMode
	ScrollbarTrackStyle func(string) string
	ScrollbarThumbStyle func(string) string
	ScrollbarHideDelay  time.Duration
}

type ScrollView struct {
	Container

	child               Component
	followEnd           bool
	primary             bool
	overscroll          string
	scrollbar           ScrollbarMode
	scrollbarTrackStyle func(string) string
	scrollbarThumbStyle func(string) string
	scrollbarHideDelay  time.Duration

	scrollTop         int
	contentHeight     int
	viewportHeight    int
	followingEnd      bool
	followSuppressed  bool
	requestRender     func()
	transientVisible  bool
	isScrollbarActive bool
	hideTimer         *time.Timer
}

func NewScrollView(child Component, options ScrollViewOptions) *ScrollView {
	view := &ScrollView{
		child:               child,
		followEnd:           options.Follow == "end",
		primary:             options.Primary,
		overscroll:          options.Overscroll,
		scrollbar:           options.Scrollbar,
		scrollbarTrackStyle: options.ScrollbarTrackStyle,
		scrollbarThumbStyle: options.ScrollbarThumbStyle,
		scrollbarHideDelay:  options.ScrollbarHideDelay,
	}
	if view.overscroll == "" {
		view.overscroll = "chain"
	}
	if view.scrollbarTrackStyle == nil {
		view.scrollbarTrackStyle = func(text string) string { return "\x1b[90m" + text + "\x1b[39m" }
	}
	if view.scrollbarThumbStyle == nil {
		view.scrollbarThumbStyle = func(text string) string { return "\x1b[37m" + text + "\x1b[39m" }
	}
	if view.scrollbarHideDelay <= 0 {
		view.scrollbarHideDelay = time.Second
	}
	view.followingEnd = view.followEnd
	view.children = []Component{child}
	return view
}

func (s *ScrollView) ScrollTop() int           { return s.scrollTop }
func (s *ScrollView) IsFollowingEnd() bool     { return s.followingEnd }
func (s *ScrollView) ViewportHeight() int      { return s.viewportHeight }
func (s *ScrollView) Primary() bool            { return s.primary }
func (s *ScrollView) Overscroll() string       { return s.overscroll }
func (s *ScrollView) Scrollbar() ScrollbarMode { return s.scrollbar }

func (s *ScrollView) IsScrollbarVisible() bool {
	if s.scrollbar == ScrollbarAlways {
		return s.viewportHeight > 0
	}
	return s.scrollbar == ScrollbarAuto && s.contentHeight > s.viewportHeight && s.transientVisible
}

func (s *ScrollView) SetScrollbar(mode ScrollbarMode) {
	if mode == s.scrollbar {
		return
	}
	s.scrollbar = mode
	if mode != ScrollbarAuto {
		s.hideTransientScrollbar()
	} else if s.isScrollbarActive {
		s.markScrollbarActivity()
	}
	s.notifyRender()
}

func (s *ScrollView) ContentWidth(width int) int {
	if s.scrollbar == ScrollbarAlways && width > 1 {
		return width - 1
	}
	return width
}

func (s *ScrollView) markScrollbarActivity() {
	if s.scrollbar != ScrollbarAuto || s.contentHeight <= s.viewportHeight {
		return
	}
	s.transientVisible = true
	if s.hideTimer != nil {
		s.hideTimer.Stop()
		s.hideTimer = nil
	}
	if s.isScrollbarActive {
		return
	}
	s.hideTimer = time.AfterFunc(s.scrollbarHideDelay, func() {
		s.transientVisible = false
		s.hideTimer = nil
		s.notifyRender()
	})
}

func (s *ScrollView) hideTransientScrollbar() {
	s.transientVisible = false
	if s.hideTimer != nil {
		s.hideTimer.Stop()
		s.hideTimer = nil
	}
}

func (s *ScrollView) SetScrollbarActive(active bool) {
	if active == s.isScrollbarActive {
		return
	}
	s.isScrollbarActive = active
	s.markScrollbarActivity()
	s.notifyRender()
}

type ScrollToOptions struct {
	DisableFollow bool
}

func (s *ScrollView) ScrollTo(scrollTop int, options ScrollToOptions) {
	maxScrollTop := maxInt(0, s.contentHeight-s.viewportHeight)
	next := maxInt(0, minInt(maxScrollTop, scrollTop))
	suppressed := options.DisableFollow && next == maxScrollTop
	followingEnd := !suppressed && s.followEnd && next == maxScrollTop
	if next == s.scrollTop && followingEnd == s.followingEnd && suppressed == s.followSuppressed {
		return
	}
	moved := next != s.scrollTop
	s.scrollTop = next
	s.followingEnd = followingEnd
	s.followSuppressed = suppressed
	if moved {
		s.markScrollbarActivity()
	}
	s.notifyRender()
}

func (s *ScrollView) ScrollBy(lines int) int {
	if lines == 0 {
		return 0
	}
	maxScrollTop := maxInt(0, s.contentHeight-s.viewportHeight)
	start := s.scrollTop
	if s.followingEnd {
		start = maxScrollTop
	}
	next := maxInt(0, minInt(maxScrollTop, start+lines))
	moved := next - start
	wasFollowingEnd := s.followingEnd
	s.scrollTop = next
	s.followingEnd = s.followEnd && next == maxScrollTop
	s.followSuppressed = false
	if moved != 0 {
		s.markScrollbarActivity()
	}
	if moved != 0 || s.followingEnd != wasFollowingEnd {
		s.notifyRender()
	}
	return lines - moved
}

func (s *ScrollView) ScrollToStart() {
	changed := s.scrollTop != 0 ||
		s.followingEnd != (s.followEnd && s.contentHeight <= s.viewportHeight)
	s.scrollTop = 0
	s.followingEnd = s.followEnd && s.contentHeight <= s.viewportHeight
	s.followSuppressed = false
	if changed {
		s.markScrollbarActivity()
		s.notifyRender()
	}
}

func (s *ScrollView) ScrollToEnd() {
	next := maxInt(0, s.contentHeight-s.viewportHeight)
	changed := s.scrollTop != next || s.followingEnd != s.followEnd
	s.scrollTop = next
	s.followingEnd = s.followEnd
	s.followSuppressed = false
	if changed {
		s.markScrollbarActivity()
		s.notifyRender()
	}
}

func (s *ScrollView) UpdateLayout(contentHeight, viewportHeight int, requestRender func()) {
	s.contentHeight = maxInt(0, contentHeight)
	s.viewportHeight = maxInt(0, viewportHeight)
	s.requestRender = requestRender
	maxScrollTop := maxInt(0, s.contentHeight-s.viewportHeight)
	if s.followingEnd {
		s.scrollTop = maxScrollTop
	} else {
		s.scrollTop = maxInt(0, minInt(s.scrollTop, maxScrollTop))
	}
	if s.scrollTop < maxScrollTop {
		s.followSuppressed = false
	}
	if s.followEnd && s.scrollTop == maxScrollTop && !s.followSuppressed {
		s.followingEnd = true
	}
	if s.contentHeight <= s.viewportHeight {
		s.hideTransientScrollbar()
	}
}

func (s *ScrollView) notifyRender() {
	if s.requestRender != nil {
		s.requestRender()
	}
}

func (s *ScrollView) ScrollLayout() ScrollLayoutSpec {
	return ScrollLayoutSpec{Child: s.child, State: s}
}

func (s *ScrollView) Render(width int) []string {
	contentWidth := s.ContentWidth(width)
	lines := s.child.Render(contentWidth)
	if contentWidth == width {
		return lines
	}
	result := make([]string, len(lines))
	for i, line := range lines {
		result[i] = line + " "
	}
	return result
}

func (s *ScrollView) dispatchMouse(event MouseEvent) *mouseDispatchResult {
	childEvent := event.WithPosition(event.X, event.Y+s.scrollTop)
	return dispatchMouseEvent(s.child, childEvent)
}
