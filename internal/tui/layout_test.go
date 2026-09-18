package tui

import (
	"strings"
	"testing"
	"time"
)

type plainComponent struct {
	lines []string
}

func (p *plainComponent) Render(width int) []string {
	result := make([]string, len(p.lines))
	for i, line := range p.lines {
		if VisibleWidth(line) > width {
			result[i] = SliceByColumn(line, 0, width, true)
		} else {
			result[i] = line
		}
	}
	return result
}

func (p *plainComponent) Invalidate() {}

type fakeStack struct {
	spec StackLayoutSpec
}

func (f *fakeStack) StackLayout() StackLayoutSpec { return f.spec }

func (f *fakeStack) Render(width int) []string {
	entries := visibleStackEntries(f.spec.Entries, Viewport{Width: maxInt(1, width), Height: 1 << 30})
	if len(entries) == 0 {
		return nil
	}
	if f.spec.Vertical {
		lines := make([]string, 0, 16)
		for index, entry := range entries {
			if index > 0 {
				for gap := 0; gap < f.spec.Gap; gap++ {
					lines = append(lines, "")
				}
			}
			var childLines []string
			if entry.Options.Basis != nil {
				if size := *entry.Options.Basis; size > 0 {
					childLines = make([]string, size)
				}
			} else {
				childLines = entry.Component.Render(width)
			}
			lines = append(lines, childLines...)
		}
		return lines
	}
	return nil
}

func (f *fakeStack) Invalidate() {}

type fakeScroll struct {
	spec ScrollLayoutSpec
}

func (f *fakeScroll) ScrollLayout() ScrollLayoutSpec { return f.spec }
func (f *fakeScroll) Render(width int) []string      { return nil }
func (f *fakeScroll) Invalidate()                    {}

func lines(count int, filler byte) []string {
	result := make([]string, count)
	for i := range result {
		result[i] = strings.Repeat(string(filler), 10) + " " + itoa(i)
	}
	return result
}

func TestAllocateStackSizesGrow(t *testing.T) {
	entries := []StackLayoutEntry{
		{Component: &staticComponent{}, Options: StackEntryOptions{Grow: 1}},
		{Component: &staticComponent{}, Options: StackEntryOptions{Grow: 3}},
	}
	sizes := AllocateStackSizes(entries, []int{1, 1}, 100, true, 0)
	if sizes[0]+sizes[1] != 100 {
		t.Fatalf("sizes = %v, want total 100", sizes)
	}
	if sizes[1] <= sizes[0] {
		t.Fatalf("grow 3 should dominate grow 1: %v", sizes)
	}
}

func TestAllocateStackSizesShrink(t *testing.T) {
	entries := []StackLayoutEntry{
		{Component: &staticComponent{}, Options: StackEntryOptions{Shrink: ShrinkNone, MinSize: 1}},
		{Component: &staticComponent{}, Options: StackEntryOptions{Shrink: 1, MinSize: 1}},
	}
	sizes := AllocateStackSizes(entries, []int{40, 40}, 50, true, 0)
	if sizes[0] != 40 {
		t.Fatalf("non-shrinkable kept: %v", sizes)
	}
	if sizes[1] != 10 {
		t.Fatalf("shrinkable reduced to %d, want 10", sizes[1])
	}

	defaultShrink := []StackLayoutEntry{
		{Component: &staticComponent{}},
		{Component: &staticComponent{}},
	}
	sizes = AllocateStackSizes(defaultShrink, []int{40, 40}, 50, true, 0)
	if sizes[0]+sizes[1] != 50 {
		t.Fatalf("default shrink should reduce total: %v", sizes)
	}
}

func TestAllocateStackSizesMinMax(t *testing.T) {
	entries := []StackLayoutEntry{
		{Component: &staticComponent{}, Options: StackEntryOptions{Grow: 1, MinSize: 10, MaxSize: 30}},
		{Component: &staticComponent{}, Options: StackEntryOptions{Grow: 1, MinSize: 5}},
	}
	sizes := AllocateStackSizes(entries, []int{5, 5}, 60, true, 0)
	if sizes[0] != 30 {
		t.Fatalf("maxSize cap violated: %v", sizes)
	}
	if sizes[1] != 30 {
		t.Fatalf("grow remainder = %d, want 30", sizes[1])
	}

	sizes = AllocateStackSizes(entries, []int{50, 50}, 20, true, 0)
	if sizes[0] != 10 {
		t.Fatalf("minSize floor violated: %v", sizes)
	}
}

func TestAllocateStackSizesGap(t *testing.T) {
	entries := []StackLayoutEntry{
		{Component: &staticComponent{}, Options: StackEntryOptions{Grow: 1}},
		{Component: &staticComponent{}, Options: StackEntryOptions{Grow: 1}},
	}
	sizes := AllocateStackSizes(entries, []int{1, 1}, 100, true, 10)
	if sizes[0]+sizes[1]+10 != 100 {
		t.Fatalf("gap not accounted: %v", sizes)
	}
}

func TestAllocateStackSizesBasis(t *testing.T) {
	basis := 25
	entries := []StackLayoutEntry{
		{Component: &staticComponent{}, Options: StackEntryOptions{Basis: &basis, Grow: 0, Shrink: 0}},
	}
	sizes := AllocateStackSizes(entries, []int{5}, 100, true, 0)
	if sizes[0] != 25 {
		t.Fatalf("basis ignored: %v", sizes)
	}
}

func TestVStackLayout(t *testing.T) {
	first := &staticComponent{lines: lines(3, 'a')}
	second := &staticComponent{lines: lines(2, 'b')}
	stack := &fakeStack{spec: StackLayoutSpec{Vertical: true, Entries: []StackLayoutEntry{
		{Component: first}, {Component: second},
	}, Gap: 1}}

	frame := RenderLayoutFrame(stack, 20, 10, func() {})
	if len(frame.Lines) != 10 {
		t.Fatalf("frame height = %d", len(frame.Lines))
	}
	if !strings.Contains(StripTerminalSequences(frame.Lines[0]), "a 0") {
		t.Fatalf("first child not painted first: %q", frame.Lines[0])
	}
	if len(frame.Root.Children) != 2 {
		t.Fatalf("children = %d", len(frame.Root.Children))
	}
	if frame.Root.Children[0].Rect.Y != 0 || frame.Root.Children[1].Rect.Y != 4 {
		t.Fatalf("child positions = %d, %d with gap 1", frame.Root.Children[0].Rect.Y, frame.Root.Children[1].Rect.Y)
	}
}

func TestStackVisibility(t *testing.T) {
	visible := &staticComponent{lines: lines(2, 'a')}
	hidden := &staticComponent{lines: lines(2, 'b')}
	stack := &fakeStack{spec: StackLayoutSpec{Vertical: true, Entries: []StackLayoutEntry{
		{Component: visible},
		{Component: hidden, Options: StackEntryOptions{Visible: func(Viewport) bool { return false }}},
	}}}
	frame := RenderLayoutFrame(stack, 20, 10, func() {})
	if strings.Contains(frame.Lines[0], "b") {
		t.Fatalf("hidden child painted: %q", frame.Lines[0])
	}
	if len(frame.Root.Children) != 1 {
		t.Fatalf("hidden child laid out: %d children", len(frame.Root.Children))
	}
}

func TestHStackLayout(t *testing.T) {
	left := &plainComponent{lines: []string{"ab", "cd"}}
	right := &plainComponent{lines: []string{"ef"}}
	stack := &fakeStack{spec: StackLayoutSpec{Entries: []StackLayoutEntry{
		{Component: left}, {Component: right},
	}}}
	frame := RenderLayoutFrame(stack, 20, 5, func() {})
	first := StripTerminalSequences(frame.Lines[0])
	if !strings.HasPrefix(first, "abef") {
		t.Fatalf("hstack line = %q", first)
	}
	if len(frame.Root.Children) != 2 {
		t.Fatalf("children = %d", len(frame.Root.Children))
	}
	if frame.Root.Children[1].Rect.X != 2 {
		t.Fatalf("second child X = %d, want 2", frame.Root.Children[1].Rect.X)
	}
}

func TestHStackAlignments(t *testing.T) {
	tall := &plainComponent{lines: lines(4, 'x')}
	short := &plainComponent{lines: []string{"s"}}
	alignments := map[StackAlign]int{
		AlignStart:   0,
		AlignCenter:  1,
		AlignEnd:     3,
		AlignStretch: 0,
	}
	for align, wantY := range alignments {
		t.Run(align.String(), func(t *testing.T) {
			stack := &fakeStack{spec: StackLayoutSpec{Entries: []StackLayoutEntry{
				{Component: tall}, {Component: short},
			}, Align: align}}
			frame := RenderLayoutFrame(stack, 20, 4, func() {})
			if got := frame.Root.Children[1].Rect.Y; got != wantY {
				t.Fatalf("align %v child Y = %d, want %d", align, got, wantY)
			}
			if align == AlignStretch {
				if got := frame.Root.Children[1].Rect.Height; got != 4 {
					t.Fatalf("stretch height = %d, want 4", got)
				}
			}
		})
	}
}

func (a StackAlign) String() string {
	switch a {
	case AlignStart:
		return "start"
	case AlignCenter:
		return "center"
	case AlignEnd:
		return "end"
	}
	return "stretch"
}

func TestScrollViewFollowEnd(t *testing.T) {
	child := &staticComponent{lines: lines(50, 'c')}
	view := NewScrollView(child, ScrollViewOptions{Follow: "end"})
	frame := RenderLayoutFrame(view, 20, 10, func() {})
	if view.ScrollTop() != 40 {
		t.Fatalf("scrollTop = %d, want 40", view.ScrollTop())
	}
	if !view.IsFollowingEnd() {
		t.Fatal("should follow end")
	}
	last := StripTerminalSequences(frame.Lines[len(frame.Lines)-1])
	if !strings.Contains(last, "49") {
		t.Fatalf("last line = %q, want content line 49", last)
	}

	view.ScrollToStart()
	if view.ScrollTop() != 0 {
		t.Fatalf("scrollTop after start = %d", view.ScrollTop())
	}
	view.ScrollToEnd()
	if view.ScrollTop() != 40 || !view.IsFollowingEnd() {
		t.Fatalf("scrollToEnd = %d", view.ScrollTop())
	}
}

func TestScrollViewScrollByAndRemainder(t *testing.T) {
	child := &staticComponent{lines: lines(50, 'c')}
	view := NewScrollView(child, ScrollViewOptions{})
	view.UpdateLayout(50, 10, func() {})
	remainder := view.ScrollBy(-5)
	if remainder != -5 || view.ScrollTop() != 0 {
		t.Fatalf("scroll above start: remainder=%d top=%d", remainder, view.ScrollTop())
	}
	remainder = view.ScrollBy(3)
	if remainder != 0 || view.ScrollTop() != 3 {
		t.Fatalf("scroll down: remainder=%d top=%d", remainder, view.ScrollTop())
	}
	remainder = view.ScrollBy(7)
	if remainder != 0 || view.ScrollTop() != 10 {
		t.Fatalf("scroll down: remainder=%d top=%d", remainder, view.ScrollTop())
	}
	remainder = view.ScrollBy(100)
	if remainder != 70 || view.ScrollTop() != 40 {
		t.Fatalf("scroll past end: remainder=%d top=%d", remainder, view.ScrollTop())
	}
}

func TestScrollViewOverscrollContain(t *testing.T) {
	inner := NewScrollView(&staticComponent{lines: lines(50, 'i')}, ScrollViewOptions{Overscroll: "contain"})
	inner.UpdateLayout(50, 10, func() {})
	remainder := inner.ScrollBy(100)
	if remainder != 60 || inner.ScrollTop() != 40 {
		t.Fatalf("contain scroll = remainder %d top %d", remainder, inner.ScrollTop())
	}
	remaining := remainder
	if inner.overscroll != "contain" {
		remaining = inner.ScrollBy(remaining)
	}
	if remaining != 60 {
		t.Fatalf("contain must stop chaining: %d", remaining)
	}

	chained := NewScrollView(&staticComponent{lines: lines(50, 'c')}, ScrollViewOptions{})
	chained.UpdateLayout(50, 10, func() {})
	passed := chained.ScrollBy(100)
	if passed != 60 || chained.overscroll != "chain" {
		t.Fatalf("chain default should pass remainder on: %d (%s)", passed, chained.overscroll)
	}
}

func TestNestedScrollChaining(t *testing.T) {
	inner := NewScrollView(&staticComponent{lines: lines(30, 'i')}, ScrollViewOptions{})
	innerHost := &fakeStack{spec: StackLayoutSpec{Vertical: true, Entries: []StackLayoutEntry{
		{Component: inner, Options: StackEntryOptions{Basis: intPtr(10), Shrink: ShrinkNone}},
	}}}
	trailer := &plainComponent{lines: lines(25, 't')}
	outerContent := &fakeStack{spec: StackLayoutSpec{Vertical: true, Entries: []StackLayoutEntry{
		{Component: innerHost},
		{Component: trailer},
	}}}
	outer := NewScrollView(outerContent, ScrollViewOptions{})

	frame := RenderLayoutFrame(outer, 20, 10, func() {})
	if frame.PrimaryScrollView == nil {
		t.Fatal("primary scroll view not detected")
	}

	box := GetScrollViewBox(frame, inner)
	if box == nil {
		t.Fatal("inner scroll view box not found")
	}
	if box.Rect.Height != 10 {
		t.Fatalf("inner viewport height = %d, want 10", box.Rect.Height)
	}
	inner.ScrollBy(100)
	if inner.ScrollTop() != 20 {
		t.Fatalf("inner scrollTop = %d, want 20", inner.ScrollTop())
	}
	remaining := inner.ScrollBy(5)
	if remaining != 5 {
		t.Fatalf("inner remainder = %d", remaining)
	}
	if outer.ScrollTop() != 0 {
		t.Fatalf("outer should not scroll yet: %d", outer.ScrollTop())
	}
	outerRemainder := outer.ScrollBy(remaining)
	if outerRemainder != 0 || outer.ScrollTop() != 5 {
		t.Fatalf("chained scroll outer = %d remainder %d", outer.ScrollTop(), outerRemainder)
	}
}

func intPtr(v int) *int { return &v }

func TestScrollViewPrimarySelection(t *testing.T) {
	primary := NewScrollView(&staticComponent{lines: lines(30, 'p')}, ScrollViewOptions{Primary: true})
	secondary := NewScrollView(&staticComponent{lines: lines(30, 's')}, ScrollViewOptions{})
	stack := &fakeStack{spec: StackLayoutSpec{Vertical: true, Entries: []StackLayoutEntry{
		{Component: secondary},
		{Component: primary},
	}}}
	frame := RenderLayoutFrame(stack, 20, 20, func() {})
	if frame.PrimaryScrollView != primary {
		t.Fatal("primary flag should select the primary scroll view")
	}
}

func TestGetScrollViewsAt(t *testing.T) {
	inner := NewScrollView(&staticComponent{lines: lines(30, 'i')}, ScrollViewOptions{})
	outer := NewScrollView(&fakeScroll{spec: ScrollLayoutSpec{Child: &staticComponent{lines: lines(30, 'o')}, State: inner}}, ScrollViewOptions{})
	frame := RenderLayoutFrame(outer, 20, 10, func() {})
	views := getScrollViewsAt(frame, 5, 5)
	if len(views) != 2 || views[0] != inner || views[1] != outer {
		t.Fatalf("scroll views at = %v", views)
	}
	if views := getScrollViewsAt(frame, 100, 100); len(views) != 0 {
		t.Fatalf("outside hits = %v", views)
	}
}

func TestGetLayoutBoxesAtOrdering(t *testing.T) {
	child := &staticComponent{lines: lines(3, 'c')}
	stack := &fakeStack{spec: StackLayoutSpec{Vertical: true, Entries: []StackLayoutEntry{{Component: child}}}}
	frame := RenderLayoutFrame(stack, 20, 5, func() {})
	boxes := getLayoutBoxesAt(frame, 0, 0)
	if len(boxes) < 2 {
		t.Fatalf("boxes = %d", len(boxes))
	}
	if boxes[0] == frame.Root {
		t.Fatal("deepest box should come first")
	}
}

func TestScrollbarGeometryAndPainting(t *testing.T) {
	child := &staticComponent{lines: lines(50, 'c')}
	view := NewScrollView(child, ScrollViewOptions{Scrollbar: ScrollbarAlways})
	frame := RenderLayoutFrame(view, 20, 10, func() {})
	box := GetScrollViewBox(frame, view)
	if box == nil {
		t.Fatal("scroll view box missing")
	}
	geometry := getScrollbarGeometry(box, false)
	if geometry == nil {
		t.Fatal("always-on scrollbar should have geometry")
	}
	if geometry.column != 19 {
		t.Fatalf("column = %d, want 19", geometry.column)
	}
	if geometry.trackHeight != 10 || geometry.thumbHeight < 2 {
		t.Fatalf("track=%d thumb=%d", geometry.trackHeight, geometry.thumbHeight)
	}
	lastLine := frame.Lines[9]
	if !strings.Contains(StripTerminalSequences(lastLine), "│") {
		t.Fatalf("track not painted: %q", lastLine)
	}
	joined := StripTerminalSequences(strings.Join(frame.Lines, ""))
	if !strings.Contains(joined, "┃") {
		t.Fatalf("inactive thumb not painted: %q", joined)
	}

	view.SetScrollbarActive(true)
	frame = RenderLayoutFrame(view, 20, 10, func() {})
	if !strings.Contains(StripTerminalSequences(strings.Join(frame.Lines, "")), "█") {
		t.Fatalf("active thumb not painted")
	}
}

func TestScrollbarAutoHidden(t *testing.T) {
	child := &staticComponent{lines: lines(50, 'c')}
	view := NewScrollView(child, ScrollViewOptions{Scrollbar: ScrollbarAuto})
	frame := RenderLayoutFrame(view, 20, 10, func() {})
	box := GetScrollViewBox(frame, view)
	if geometry := getScrollbarGeometry(box, false); geometry != nil {
		t.Fatal("inactive auto scrollbar should have no geometry")
	}
	if geometry := getScrollbarGeometry(box, true); geometry == nil {
		t.Fatal("inactive auto scrollbar should reveal with includeHiddenAuto")
	}
	view.SetScrollbarActive(true)
	view.UpdateLayout(50, 10, func() {})
	if !view.IsScrollbarVisible() {
		t.Fatal("active auto scrollbar should be visible")
	}
	time.Sleep(10 * time.Millisecond)
}

func TestScrollViewContentWidthReservesScrollbarColumn(t *testing.T) {
	view := NewScrollView(&staticComponent{lines: []string{"hello"}}, ScrollViewOptions{Scrollbar: ScrollbarAlways})
	if got := view.ContentWidth(20); got != 19 {
		t.Fatalf("contentWidth = %d, want 19", got)
	}
	view.SetScrollbar(ScrollbarHidden)
	if got := view.ContentWidth(20); got != 20 {
		t.Fatalf("contentWidth after hide = %d", got)
	}
	rendered := view.Render(20)
	if !strings.HasSuffix(rendered[0], " ") {
		t.Fatalf("hidden scrollbar should pad right column: %q", rendered[0])
	}
}

func TestOSC133ZoneTrimming(t *testing.T) {
	marked := OSC133PromptStart + "prompt line"
	if got := trimOSC133Zone(marked); got != "prompt line" {
		t.Fatalf("trimmed = %q", got)
	}
	if got := trimOSC133Zone("plain"); got != "plain" {
		t.Fatalf("plain = %q", got)
	}
}
