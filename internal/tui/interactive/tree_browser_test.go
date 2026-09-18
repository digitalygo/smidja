package interactive

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/tui"
)

func sampleTreeNodes() []TreeBrowserNode {
	return []TreeBrowserNode{
		{ID: "u1", Depth: 0, Kind: "user", Preview: "hello", Leaf: false},
		{ID: "a1", ParentID: "u1", Depth: 1, Kind: "assistant", Preview: "answer one"},
		{ID: "t1", ParentID: "a1", Depth: 2, Kind: "tool", Preview: "tool one"},
		{ID: "a2", ParentID: "u1", Depth: 1, Kind: "assistant", Label: "keep", Preview: "answer two", Leaf: true},
		{ID: "t2", ParentID: "a2", Depth: 2, Kind: "tool", Preview: "tool two"},
	}
}

func newTestTreeBrowser(t *testing.T, nodes []TreeBrowserNode, actions *[]TreeBrowserAction) *TreeBrowser {
	t.Helper()
	return NewTreeBrowser(TreeBrowserOptions{Title: "Tree", Nodes: nodes, ActiveEntryID: "a2"}, NewDialogTheme(nil), func(action TreeBrowserAction) {
		if actions != nil {
			*actions = append(*actions, action)
		}
	})
}

func visibleSet(browser *TreeBrowser) map[string]bool {
	out := map[string]bool{}
	for _, id := range browser.VisibleIDs() {
		out[id] = true
	}
	return out
}

func TestTreeBrowserFilters(t *testing.T) {
	browser := newTestTreeBrowser(t, sampleTreeNodes(), nil)
	all := visibleSet(browser)
	for _, id := range []string{"u1", "a1", "t1", "a2", "t2"} {
		if !all[id] {
			t.Fatalf("default filter missing %s", id)
		}
	}
	browser.HandleInput("\t")
	if browser.Filter() != TreeFilterNoTools {
		t.Fatalf("filter after tab = %s, want no-tools", browser.Filter())
	}
	noTools := visibleSet(browser)
	if noTools["t1"] || noTools["t2"] {
		t.Error("no-tools filter must hide tool entries")
	}
	if !noTools["u1"] || !noTools["a1"] || !noTools["a2"] {
		t.Error("no-tools filter must keep ancestors")
	}
	browser.HandleInput("\t")
	if userOnly := visibleSet(browser); len(userOnly) != 1 || !userOnly["u1"] {
		t.Errorf("user filter visible = %v, want only u1", userOnly)
	}
	browser.HandleInput("\t")
	if labelled := visibleSet(browser); len(labelled) != 1 || !labelled["a2"] {
		t.Errorf("labelled filter visible = %v, want only a2", labelled)
	}
	browser.HandleInput("\t")
	if all := visibleSet(browser); len(all) != 5 {
		t.Errorf("all filter visible = %v, want every entry", all)
	}
	browser.HandleInput("\t")
	if browser.Filter() != TreeFilterDefault {
		t.Errorf("filter cycle ended at %s, want default", browser.Filter())
	}
}

func TestTreeBrowserFoldAndUnfold(t *testing.T) {
	browser := newTestTreeBrowser(t, sampleTreeNodes(), nil)
	browser.HandleInput("\x1b[A")
	browser.HandleInput("\x1b[A")
	browser.HandleInput("\x1b[A")
	if browser.SelectedID() != "u1" {
		t.Fatalf("selected = %s, want u1 after three up moves", browser.SelectedID())
	}
	browser.HandleInput("\x1b[D")
	if browser.Folded() != 1 {
		t.Fatalf("folded count = %d, want 1", browser.Folded())
	}
	if visible := visibleSet(browser); len(visible) != 1 || !visible["u1"] {
		t.Errorf("folded visible = %v, want only the root", visible)
	}
	browser.HandleInput("\x1b[C")
	if browser.Folded() != 0 {
		t.Fatalf("folded count after unfold = %d, want 0", browser.Folded())
	}
	if len(browser.VisibleIDs()) != 5 {
		t.Errorf("unfolded visible = %d, want 5", len(browser.VisibleIDs()))
	}
}

func TestTreeBrowserSelectionStableAcrossFoldAndFilter(t *testing.T) {
	browser := newTestTreeBrowser(t, sampleTreeNodes(), nil)
	if browser.SelectedID() != "a2" {
		t.Fatalf("initial selected = %s, want a2", browser.SelectedID())
	}
	browser.HandleInput("\x1b[D")
	browser.HandleInput("\x1b[C")
	if browser.SelectedID() != "a2" {
		t.Fatalf("selected after fold cycle = %s, want a2", browser.SelectedID())
	}
	browser.HandleInput("\t")
	if browser.SelectedID() != "a2" {
		t.Fatalf("selected under no-tools = %s, want the still visible a2", browser.SelectedID())
	}
	browser.HandleInput("\t")
	if browser.SelectedID() != "u1" {
		t.Fatalf("selected under user filter = %s, want the reconciled u1", browser.SelectedID())
	}
	browser.HandleInput("\t")
	browser.HandleInput("\t")
	browser.HandleInput("\t")
	if browser.SelectedID() != "a2" {
		t.Fatalf("selected after filter cycle = %s, want the visible a2", browser.SelectedID())
	}
	if visible := visibleSet(browser); len(visible) != 5 {
		t.Fatalf("visible after filter cycle = %v, want every entry", visible)
	}
}

func TestTreeBrowserSelectOnLeafAndCopyAction(t *testing.T) {
	var actions []TreeBrowserAction
	browser := newTestTreeBrowser(t, sampleTreeNodes(), &actions)
	browser.HandleInput("c")
	if len(actions) != 1 || actions[0].Kind != TreeActionCopy || actions[0].EntryID != "a2" {
		t.Fatalf("copy actions = %+v, want a copy action for a2", actions)
	}
	if browser.SelectedPreview() != "answer two" {
		t.Errorf("preview = %q, want answer two", browser.SelectedPreview())
	}
	browser.HandleInput("\x1b[B")
	browser.HandleInput("\r")
	if len(actions) != 2 || actions[1].Kind != TreeActionSelect || actions[1].EntryID != "t2" {
		t.Fatalf("actions = %+v, want a select action for the leaf t2", actions)
	}
	actions = actions[:0]
	browser.HandleInput("\x1b[A")
	browser.HandleInput("\r")
	if len(actions) != 0 {
		t.Fatalf("enter on a parent must fold, not select: %+v", actions)
	}
	if browser.Folded() != 1 {
		t.Errorf("folded count = %d, want 1 after enter on the parent", browser.Folded())
	}
}

func TestTreeBrowserSearchAndUnicodeResize(t *testing.T) {
	nodes := []TreeBrowserNode{
		{ID: "cjk", Depth: 0, Kind: "user", Label: "設計", Preview: strings.Repeat("ワイド", 40)},
		{ID: "emoji", ParentID: "cjk", Depth: 1, Kind: "assistant", Label: "done ✅", Preview: "🙂🙂"},
	}
	browser := newTestTreeBrowser(t, nodes, nil)
	for _, width := range []int{8, 20, 120} {
		lines := browser.Render(width)
		if len(lines) == 0 {
			t.Fatalf("render width %d produced no lines", width)
		}
		for _, line := range lines {
			if strings.Contains(line, "\n") {
				t.Fatalf("render width %d produced an embedded newline", width)
			}
		}
	}
	browser.HandleInput("/")
	browser.HandleInput("d")
	browser.HandleInput("o")
	browser.HandleInput("n")
	browser.HandleInput("e")
	if browser.Search() != "done" {
		t.Fatalf("search = %q, want done", browser.Search())
	}
	if visible := visibleSet(browser); !visible["emoji"] || !visible["cjk"] {
		t.Errorf("search visible = %v, want the match and its ancestor", visible)
	}
	browser.HandleInput("\x7f")
	if browser.Search() != "don" {
		t.Fatalf("search after backspace = %q, want don", browser.Search())
	}
}

func TestTreeBrowserHandlesCorruptGraph(t *testing.T) {
	nodes := []TreeBrowserNode{
		{ID: "n1", ParentID: "n2", Depth: 0, Kind: "user"},
		{ID: "n2", ParentID: "n1", Depth: 1, Kind: "user"},
		{ID: "orphan", ParentID: "ghost", Depth: 0, Kind: "user", Corrupt: true},
	}
	browser := newTestTreeBrowser(t, nodes, nil)
	visible := browser.VisibleIDs()
	if len(visible) != 3 {
		t.Fatalf("visible = %v, want every corrupt node without hanging", visible)
	}
	if lines := browser.Render(40); len(lines) == 0 {
		t.Fatal("corrupt graph produced no render")
	}
	browser.HandleInput("\x1b[D")
	browser.HandleInput("\x1b[C")
}

func TestTreeBrowserCloseAction(t *testing.T) {
	var actions []TreeBrowserAction
	browser := newTestTreeBrowser(t, sampleTreeNodes(), &actions)
	browser.HandleInput("\x1b")
	if len(actions) != 1 || actions[0].Kind != TreeActionClose {
		t.Fatalf("actions = %+v, want close", actions)
	}
	browser.HandleInput("c")
	if len(actions) != 1 {
		t.Fatalf("a settled browser must ignore further input: %+v", actions)
	}
}

func TestTreeBrowserTruncatesLongLabels(t *testing.T) {
	nodes := []TreeBrowserNode{
		{ID: "long", Depth: 0, Kind: "user", Label: strings.Repeat("x", 200), Timestamp: "2026-01-01T00:00:00.000Z"},
	}
	browser := newTestTreeBrowser(t, nodes, nil)
	lines := browser.Render(30)
	if len(lines) == 0 {
		t.Fatal("no render output")
	}
	for _, line := range lines {
		if tui.VisibleWidth(line) > 30 {
			t.Errorf("line width %d exceeds 30: %q", tui.VisibleWidth(line), line)
		}
	}
}

func actionableTreeNodes() []TreeBrowserNode {
	return []TreeBrowserNode{
		{ID: "hidden", Kind: "notice", Preview: "hidden body"},
		{ID: "keep", Kind: "user", Preview: "keep body"},
		{ID: "target", Kind: "assistant", Label: "target", Preview: "target body", Deletable: true, Renamable: true},
	}
}

func searchTreeBrowser(browser *TreeBrowser, query string) {
	browser.HandleInput("/")
	for _, r := range query {
		browser.HandleInput(string(r))
	}
	browser.HandleInput("\r")
}

func TestTreeBrowserReconcilesHiddenPriorSelection(t *testing.T) {
	browser := NewTreeBrowser(TreeBrowserOptions{Title: "Tree", Nodes: actionableTreeNodes(), ActiveEntryID: "hidden"}, NewDialogTheme(nil), nil)
	if got := browser.SelectedID(); got != "keep" {
		t.Fatalf("selected = %q, want the nearest visible row keep", got)
	}
	if visible := visibleSet(browser); !visible["keep"] || visible["hidden"] {
		t.Fatalf("visible = %v, want keep without the filtered notice", visible)
	}
}

func TestTreeBrowserReconcilesToNearestPreviousVisible(t *testing.T) {
	nodes := []TreeBrowserNode{
		{ID: "u1", Kind: "user"},
		{ID: "a1", Kind: "assistant", Label: "one"},
		{ID: "a2", Kind: "assistant"},
		{ID: "a3", Kind: "assistant", Label: "three"},
	}
	browser := NewTreeBrowser(TreeBrowserOptions{Title: "Tree", Nodes: nodes, ActiveEntryID: "a2", Filter: TreeFilterLabelled}, NewDialogTheme(nil), nil)
	if got := browser.SelectedID(); got != "a1" {
		t.Fatalf("selected = %q, want the nearest previous visible row a1", got)
	}
}

func TestTreeBrowserActionTargetsSingleSearchResult(t *testing.T) {
	cases := []struct {
		name string
		key  string
		kind string
	}{
		{name: "rename", key: "r", kind: TreeActionRename},
		{name: "delete", key: "d", kind: TreeActionDelete},
		{name: "select", key: "\r", kind: TreeActionSelect},
		{name: "copy", key: "c", kind: TreeActionCopy},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var actions []TreeBrowserAction
			browser := NewTreeBrowser(TreeBrowserOptions{Title: "Tree", Nodes: actionableTreeNodes(), ActiveEntryID: "hidden"}, NewDialogTheme(nil), func(action TreeBrowserAction) {
				actions = append(actions, action)
			})
			searchTreeBrowser(browser, "target")
			if browser.SelectedID() != "target" {
				t.Fatalf("selected after search = %q, want target", browser.SelectedID())
			}
			if visible := browser.VisibleIDs(); len(visible) != 1 || visible[0] != "target" {
				t.Fatalf("visible = %v, want only target", visible)
			}
			browser.HandleInput(tc.key)
			if len(actions) != 1 || actions[0].Kind != tc.kind || actions[0].EntryID != "target" {
				t.Fatalf("actions = %+v, want one %s for target", actions, tc.kind)
			}
			if actions[0].Node.Preview != "target body" {
				t.Fatalf("action preview = %q, want the raw target payload", actions[0].Node.Preview)
			}
		})
	}
}

func TestTreeBrowserFilterWithNoVisibleRowsRejectsActions(t *testing.T) {
	var actions []TreeBrowserAction
	nodes := []TreeBrowserNode{{ID: "tools", Kind: "tool", Deletable: true, Renamable: true}}
	browser := NewTreeBrowser(TreeBrowserOptions{Title: "Tree", Nodes: nodes}, NewDialogTheme(nil), func(action TreeBrowserAction) {
		actions = append(actions, action)
	})
	browser.HandleInput("\t")
	if browser.Filter() != TreeFilterNoTools {
		t.Fatalf("filter = %s, want no-tools", browser.Filter())
	}
	if browser.SelectedID() != "" {
		t.Fatalf("selected = %q, want empty when nothing is visible", browser.SelectedID())
	}
	for _, key := range []string{"c", "d", "r", "\r"} {
		browser.HandleInput(key)
	}
	if len(actions) != 0 {
		t.Fatalf("actions = %+v, want none without a visible selection", actions)
	}
	browser.HandleInput("\x1b")
	if len(actions) != 1 || actions[0].Kind != TreeActionClose {
		t.Fatalf("actions = %+v, want a close after the empty filter", actions)
	}
}

func TestTreeBrowserTerminalActionBurstCannotRetarget(t *testing.T) {
	var actions []TreeBrowserAction
	browser := NewTreeBrowser(TreeBrowserOptions{Title: "Tree", Nodes: actionableTreeNodes(), ActiveEntryID: "target"}, NewDialogTheme(nil), func(action TreeBrowserAction) {
		actions = append(actions, action)
	})
	browser.HandleInput("\r")
	browser.HandleInput("\x1b[A")
	browser.HandleInput("\x1b[B")
	browser.HandleInput("c")
	if len(actions) != 1 || actions[0].Kind != TreeActionSelect || actions[0].EntryID != "target" {
		t.Fatalf("burst actions = %+v, want a single select for target", actions)
	}
	if browser.SelectedID() != "target" {
		t.Fatalf("selection retargeted to %q after a terminal action", browser.SelectedID())
	}
}

func TestTreeBrowserCopySnapshotSurvivesArrowRetarget(t *testing.T) {
	var actions []TreeBrowserAction
	browser := NewTreeBrowser(TreeBrowserOptions{Title: "Tree", Nodes: actionableTreeNodes(), ActiveEntryID: "target"}, NewDialogTheme(nil), func(action TreeBrowserAction) {
		actions = append(actions, action)
	})
	browser.HandleInput("c")
	browser.HandleInput("\x1b[A")
	if len(actions) != 1 || actions[0].Kind != TreeActionCopy || actions[0].EntryID != "target" {
		t.Fatalf("actions = %+v, want a single copy for target", actions)
	}
	if actions[0].Node.Preview != "target body" {
		t.Fatalf("copy preview = %q, want the snapshotted target payload", actions[0].Node.Preview)
	}
	if browser.SelectedID() != "keep" {
		t.Fatalf("selected = %q, want keep after the arrow", browser.SelectedID())
	}
}

func TestTreeBrowserMouseAndActionBurstKeepsTarget(t *testing.T) {
	const press = "\x1b[<0;10;5M"
	const release = "\x1b[<0;10;5m"
	var copyActions []TreeBrowserAction
	copyBrowser := NewTreeBrowser(TreeBrowserOptions{Title: "Tree", Nodes: actionableTreeNodes(), ActiveEntryID: "target"}, NewDialogTheme(nil), func(action TreeBrowserAction) {
		copyActions = append(copyActions, action)
	})
	copyBrowser.HandleInput(press)
	copyBrowser.HandleInput(release)
	copyBrowser.HandleInput("c")
	copyBrowser.HandleInput(press)
	copyBrowser.HandleInput("\x1b[A")
	copyBrowser.HandleInput("d")
	copyBrowser.HandleInput(release)
	if len(copyActions) != 1 || copyActions[0].Kind != TreeActionCopy || copyActions[0].EntryID != "target" {
		t.Fatalf("mouse burst actions = %+v, want a single copy for target", copyActions)
	}
	if copyBrowser.SelectedID() != "keep" {
		t.Fatalf("selected = %q, want keep after the mouse burst", copyBrowser.SelectedID())
	}

	var deleteActions []TreeBrowserAction
	deleteBrowser := NewTreeBrowser(TreeBrowserOptions{Title: "Tree", Nodes: actionableTreeNodes(), ActiveEntryID: "keep"}, NewDialogTheme(nil), func(action TreeBrowserAction) {
		deleteActions = append(deleteActions, action)
	})
	deleteBrowser.HandleInput(press)
	deleteBrowser.HandleInput("d")
	deleteBrowser.HandleInput("\x1b[B")
	deleteBrowser.HandleInput(release)
	deleteBrowser.HandleInput("d")
	deleteBrowser.HandleInput("\x1b[B")
	if len(deleteActions) != 1 || deleteActions[0].Kind != TreeActionDelete || deleteActions[0].EntryID != "target" {
		t.Fatalf("mouse burst actions = %+v, want a single delete for target", deleteActions)
	}
}

func TestTreeBrowserReconcileKeepsViewportWhenSelectionVisible(t *testing.T) {
	nodes := []TreeBrowserNode{
		{ID: "first", Kind: "user", Label: "first-row"},
		{ID: "second", Kind: "user", Label: "second-row"},
		{ID: "last", Kind: "user", Label: "last-row", Leaf: true},
	}
	browser := NewTreeBrowser(TreeBrowserOptions{Title: "Tree", Nodes: nodes, ActiveEntryID: "last"}, NewDialogTheme(nil), nil)
	joined := strings.Join(browser.Render(60), "\n")
	if !strings.Contains(joined, "first-row") {
		t.Fatalf("initial reconcile scrolled away from the first row:\n%s", joined)
	}
}

func TestTreeBrowserConcurrentRenderFilterInput(t *testing.T) {
	var mu sync.Mutex
	var actions []TreeBrowserAction
	browser := NewTreeBrowser(TreeBrowserOptions{Title: "Tree", Nodes: actionableTreeNodes(), ActiveEntryID: "target"}, NewDialogTheme(nil), func(action TreeBrowserAction) {
		mu.Lock()
		actions = append(actions, action)
		mu.Unlock()
	})
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(5)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				browser.Render(60)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				browser.HandleInput("\t")
			}
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				browser.HandleInput("\x1b[A")
				browser.HandleInput("\x1b[B")
			}
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				browser.VisibleIDs()
				browser.SelectedID()
				browser.SelectedPreview()
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			searchTreeBrowser(browser, "target")
		}
	}()
	time.Sleep(100 * time.Millisecond)
	close(stop)
	wg.Wait()
}
