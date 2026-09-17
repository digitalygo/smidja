package interactive

import (
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/tui"
)

func p4ChainNodes() []TreeBrowserNode {
	return []TreeBrowserNode{
		{ID: "u1", Depth: 0, Kind: "user", Preview: "first question", Timestamp: "2026-01-01T00:00:01.000Z"},
		{ID: "a1", ParentID: "u1", Depth: 1, Kind: "assistant", Preview: "thinking answer", Timestamp: "2026-01-01T00:00:02.000Z"},
		{ID: "t1", ParentID: "a1", Depth: 2, Kind: "tool", Preview: "probe output", Timestamp: "2026-01-01T00:00:03.000Z"},
		{ID: "u2", ParentID: "a1", Depth: 2, Kind: "user", Preview: "follow up", Timestamp: "2026-01-01T00:00:04.000Z"},
		{ID: "a2", ParentID: "u1", Depth: 1, Kind: "assistant", Label: "keep", LabelTimestamp: "2026-01-01T00:00:05.000Z", Preview: "branched answer", Timestamp: "2026-01-01T00:00:05.000Z", Leaf: true},
		{ID: "t2", ParentID: "a2", Depth: 2, Kind: "tool", Preview: "second tool", Timestamp: "2026-01-01T00:00:06.000Z"},
		{ID: "lbl", Depth: 3, Kind: "label", Preview: "bookkeeping label"},
		{ID: "inf", Depth: 3, Kind: "info", Preview: "bookkeeping info"},
		{ID: "mdl", Depth: 3, Kind: "model", Preview: "bookkeeping model"},
		{ID: "thk", Depth: 3, Kind: "thinking", Preview: "bookkeeping thinking"},
	}
}

func p4VisibleSet(b *TreeBrowser) map[string]bool {
	out := map[string]bool{}
	for _, id := range b.VisibleIDs() {
		out[id] = true
	}
	return out
}

func TestP4ExactFiltersOnActualEntries(t *testing.T) {
	browser := NewTreeBrowser(TreeBrowserOptions{Title: "Tree", Nodes: p4ChainNodes(), ActiveEntryID: "a2"}, NewDialogTheme(nil), nil)
	def := p4VisibleSet(browser)
	for _, id := range []string{"u1", "a1", "t1", "u2", "a2", "t2"} {
		if !def[id] {
			t.Fatalf("default missing conversation %s: %v", id, def)
		}
	}
	for _, id := range []string{"lbl", "inf", "mdl", "thk"} {
		if def[id] {
			t.Fatalf("default leaked bookkeeping %s", id)
		}
	}
	browser.HandleInput("\t")
	if browser.Filter() != TreeFilterNoTools {
		t.Fatalf("filter = %s, want no-tools", browser.Filter())
	}
	noTools := p4VisibleSet(browser)
	if noTools["t1"] || noTools["t2"] {
		t.Fatalf("no-tools shows tools: %v", noTools)
	}
	for _, id := range []string{"u1", "a1", "u2", "a2"} {
		if !noTools[id] {
			t.Fatalf("no-tools missing %s", id)
		}
	}
	browser.HandleInput("\t")
	userOnly := p4VisibleSet(browser)
	if len(userOnly) != 2 || !userOnly["u1"] || !userOnly["u2"] {
		t.Fatalf("user visible = %v, want u1 u2", userOnly)
	}
	if d := browser.DisplayDepth("u2"); d != 1 {
		t.Fatalf("display depth u2 = %d, want 1 via nearest visible u1", d)
	}
	browser.HandleInput("\t")
	labelled := p4VisibleSet(browser)
	if len(labelled) != 1 || !labelled["a2"] {
		t.Fatalf("labelled visible = %v, want only a2", labelled)
	}
	browser.HandleInput("\t")
	all := p4VisibleSet(browser)
	if len(all) != 10 {
		t.Fatalf("all visible = %v, want every projected node", all)
	}
}

func TestP4BranchedHiddenAncestorsRecomputeParent(t *testing.T) {
	nodes := []TreeBrowserNode{
		{ID: "u1", Depth: 0, Kind: "user", Preview: "root"},
		{ID: "m1", ParentID: "u1", Depth: 1, Kind: "model", Preview: "hidden model"},
		{ID: "a1", ParentID: "m1", Depth: 2, Kind: "assistant", Preview: "after hidden"},
		{ID: "t1", ParentID: "a1", Depth: 3, Kind: "tool", Preview: "tool"},
		{ID: "u2", ParentID: "t1", Depth: 4, Kind: "user", Preview: "deep user"},
	}
	browser := NewTreeBrowser(TreeBrowserOptions{Title: "Tree", Nodes: nodes, ActiveEntryID: "u2", Filter: TreeFilterUser}, NewDialogTheme(nil), nil)
	vis := p4VisibleSet(browser)
	if len(vis) != 2 || !vis["u1"] || !vis["u2"] {
		t.Fatalf("user visible = %v, want u1 u2 without hidden ancestors", vis)
	}
	if d := browser.DisplayDepth("u2"); d != 1 {
		t.Fatalf("display depth u2 = %d, want 1", d)
	}
	if d := browser.DisplayDepth("u1"); d != 0 {
		t.Fatalf("display depth u1 = %d, want 0", d)
	}
	ids := browser.VisibleIDs()
	if len(ids) != 2 || ids[0] != "u1" || ids[1] != "u2" {
		t.Fatalf("branch order = %v, want u1 u2", ids)
	}
	browser.HandleInput("\x1b[A")
	if browser.SelectedID() != "u1" {
		t.Fatalf("selected = %s, want u1", browser.SelectedID())
	}
	browser.HandleInput("\x1b[D")
	if browser.Folded() != 1 {
		t.Fatalf("folded = %d, want 1", browser.Folded())
	}
	if vis := p4VisibleSet(browser); len(vis) != 1 || !vis["u1"] {
		t.Fatalf("folded visible = %v, want only u1", vis)
	}
	browser.HandleInput("\x1b[C")
	if browser.Folded() != 0 {
		t.Fatalf("folded after unfold = %d", browser.Folded())
	}
}

func TestP4DisplayDepthBounded(t *testing.T) {
	var nodes []TreeBrowserNode
	prev := ""
	for i := 0; i < 15; i++ {
		id := strings.Repeat("n", i+1) + "x"
		if i == 0 {
			id = "n0"
		} else {
			id = "n" + string(rune('0'+i/10)) + string(rune('0'+i%10))
		}
		node := TreeBrowserNode{ID: id, Kind: "user", Preview: "row"}
		if prev != "" {
			node.ParentID = prev
			node.Depth = i
		}
		nodes = append(nodes, node)
		prev = id
	}
	browser := NewTreeBrowser(TreeBrowserOptions{Title: "Tree", Nodes: nodes, ActiveEntryID: nodes[len(nodes)-1].ID, Filter: TreeFilterUser}, NewDialogTheme(nil), nil)
	for _, id := range browser.VisibleIDs() {
		if d := browser.DisplayDepth(id); d > treeMaxDisplayDepth {
			t.Fatalf("display depth %d exceeds bound for %s", d, id)
		}
	}
	last := nodes[len(nodes)-1].ID
	if d := browser.DisplayDepth(last); d != treeMaxDisplayDepth {
		t.Fatalf("deepest depth = %d, want bound %d", d, treeMaxDisplayDepth)
	}
	for _, width := range []int{20, 40, 80, 120} {
		for _, line := range browser.Render(width) {
			if tui.VisibleWidth(line) > width {
				t.Fatalf("width %d line exceeds: %q", width, line)
			}
		}
	}
}

func TestP4TimestampToggle(t *testing.T) {
	nodes := []TreeBrowserNode{
		{ID: "u1", Kind: "user", Preview: "hi", Timestamp: "2026-01-01T00:00:01.000Z", Label: "keep", LabelTimestamp: "2026-01-01T00:00:02.000Z", Leaf: true},
	}
	browser := NewTreeBrowser(TreeBrowserOptions{Title: "Tree", Nodes: nodes}, NewDialogTheme(nil), nil)
	if !browser.TimestampsVisible() {
		t.Fatal("timestamps must be visible by default")
	}
	shown := strings.Join(browser.Render(80), "\n")
	if !strings.Contains(shown, "2026-01-01T00:00:01.000Z") {
		t.Fatalf("render missing entry timestamp:\n%s", shown)
	}
	if !strings.Contains(shown, "2026-01-01T00:00:02.000Z") {
		t.Fatalf("render missing label timestamp:\n%s", shown)
	}
	browser.HandleInput("t")
	if browser.TimestampsVisible() {
		t.Fatal("timestamps must hide after toggle")
	}
	hidden := strings.Join(browser.Render(80), "\n")
	if strings.Contains(hidden, "2026-01-01T00:00:01.000Z") || strings.Contains(hidden, "2026-01-01T00:00:02.000Z") {
		t.Fatalf("render leaked timestamps after toggle:\n%s", hidden)
	}
	if !strings.Contains(hidden, "keep") {
		t.Fatalf("label must remain without timestamps:\n%s", hidden)
	}
	browser.HandleInput("t")
	if !browser.TimestampsVisible() {
		t.Fatal("timestamps must return after second toggle")
	}
}

func TestP4LabelActionSnapshot(t *testing.T) {
	var actions []TreeBrowserAction
	nodes := p4ChainNodes()
	browser := NewTreeBrowser(TreeBrowserOptions{Title: "Tree", Nodes: nodes, ActiveEntryID: "a2"}, NewDialogTheme(nil), func(a TreeBrowserAction) {
		actions = append(actions, a)
	})
	browser.HandleInput("l")
	if len(actions) != 1 || actions[0].Kind != TreeActionLabel {
		t.Fatalf("actions = %+v, want one label", actions)
	}
	if actions[0].EntryID != "a2" {
		t.Fatalf("label target = %s, want a2", actions[0].EntryID)
	}
	if actions[0].Node.Preview != "branched answer" || actions[0].Node.Label != "keep" {
		t.Fatalf("label snapshot = %+v, want target payload", actions[0].Node)
	}
	if actions[0].Filter != TreeFilterDefault {
		t.Fatalf("label filter = %s, want default", actions[0].Filter)
	}
	browser.HandleInput("\x1b[A")
	browser.HandleInput("l")
	if len(actions) != 1 {
		t.Fatalf("settled browser must ignore second label: %+v", actions)
	}
}

func TestP4InitialSelectionBeyondViewportStaysVisible(t *testing.T) {
	var nodes []TreeBrowserNode
	for i := 0; i < 20; i++ {
		id := "r" + string(rune('a'+i/10)) + string(rune('0'+i%10))
		nodes = append(nodes, TreeBrowserNode{ID: id, Kind: "user", Preview: "row " + id, Label: "row"})
	}
	last := nodes[len(nodes)-1].ID
	browser := NewTreeBrowser(TreeBrowserOptions{Title: "Tree", Nodes: nodes, ActiveEntryID: last}, NewDialogTheme(nil), nil)
	if browser.SelectedID() != last {
		t.Fatalf("selected = %s, want %s", browser.SelectedID(), last)
	}
	found := false
	selectedRows := 0
	for _, line := range browser.Render(60) {
		if strings.Contains(line, "›") {
			selectedRows++
		}
		if strings.Contains(line, last) || strings.Contains(line, "row "+last) {
			found = true
		}
	}
	if !found {
		t.Fatalf("render at width 60 missing the initial selection %s", last)
	}
	if selectedRows != 1 {
		t.Fatalf("render at width 60 shows %d cursor rows, want exactly the selected row in the viewport", selectedRows)
	}
	ids := browser.VisibleIDs()
	pos := -1
	for i, id := range ids {
		if id == last {
			pos = i
		}
	}
	if pos < 0 {
		t.Fatalf("selection %s not in visible %v", last, ids)
	}
	if pos < 8 {
		t.Fatalf("position %d should be beyond first viewport for 20 rows", pos)
	}
	for _, width := range []int{20, 60, 120} {
		lines := browser.Render(width)
		if len(lines) == 0 {
			t.Fatalf("width %d produced no lines", width)
		}
		for _, line := range lines {
			if tui.VisibleWidth(line) > width {
				t.Fatalf("width %d line exceeds: %q", width, line)
			}
		}
	}
	browser.HandleInput("\x1b[A")
	if browser.SelectedID() == last {
		t.Fatalf("up move did not leave %s", last)
	}
}
