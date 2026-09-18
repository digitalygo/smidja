package interactive

import (
	"testing"
)

func blocker6FullNodes() []TreeBrowserNode {
	return []TreeBrowserNode{
		{ID: "user1", Depth: 0, Kind: "user", Preview: "first", Timestamp: "2026-01-01T00:00:01.000Z"},
		{ID: "assistant1", ParentID: "user1", Depth: 1, Kind: "assistant", Preview: "work"},
		{ID: "tool1", ParentID: "assistant1", Depth: 2, Kind: "tool", Preview: "out"},
		{ID: "prov1", ParentID: "tool1", Depth: 3, Kind: "provenance", CustomType: "smidja.session.provenance", Preview: "prov"},
		{ID: "prof1", ParentID: "prov1", Depth: 4, Kind: "profile", CustomType: "smidja.runtime.profile", Preview: "prof"},
		{ID: "gen1", ParentID: "prof1", Depth: 5, Kind: "custom", CustomType: "bookkeeping", Preview: "gen"},
		{ID: "vis1", ParentID: "gen1", Depth: 6, Kind: "custom-message", CustomType: "notice", Display: true, Preview: "visible"},
		{ID: "hid1", ParentID: "vis1", Depth: 7, Kind: "custom-hidden", CustomType: "hidden-note", Display: false, Preview: "hidden"},
		{ID: "lbl1", ParentID: "hid1", Depth: 8, Kind: "label", Preview: "label"},
		{ID: "inf1", ParentID: "lbl1", Depth: 9, Kind: "info", Preview: "info"},
		{ID: "mod1", ParentID: "inf1", Depth: 10, Kind: "model", Preview: "model"},
		{ID: "thk1", ParentID: "mod1", Depth: 11, Kind: "thinking", Preview: "thinking"},
		{ID: "comp1", ParentID: "thk1", Depth: 12, Kind: "compaction", Preview: "compact"},
		{ID: "sum1", ParentID: "comp1", Depth: 13, Kind: "summary", Preview: "summary"},
		{ID: "user2", ParentID: "sum1", Depth: 14, Kind: "user", Label: "keep", Preview: "follow"},
	}
}

func blocker6Set(b *TreeBrowser) map[string]bool {
	out := map[string]bool{}
	for _, id := range b.VisibleIDs() {
		out[id] = true
	}
	return out
}

func TestBlocker6BookkeepingFilters(t *testing.T) {
	nodes := blocker6FullNodes()
	if nodes[6].Kind == nodes[5].Kind {
		t.Fatalf("generic %q must differ from displayable %q", nodes[5].Kind, nodes[6].Kind)
	}
	if nodes[6].Kind == nodes[7].Kind {
		t.Fatalf("displayable must differ from hidden")
	}
	browser := NewTreeBrowser(TreeBrowserOptions{Title: "Tree", Nodes: nodes, ActiveEntryID: "user2"}, NewDialogTheme(nil), nil)
	def := blocker6Set(browser)
	for _, id := range []string{"user1", "assistant1", "tool1", "vis1", "comp1", "sum1", "user2"} {
		if !def[id] {
			t.Fatalf("default missing %s: %v", id, def)
		}
	}
	for _, id := range []string{"prov1", "prof1", "gen1", "hid1", "lbl1", "inf1", "mod1", "thk1"} {
		if def[id] {
			t.Fatalf("default leaked %s", id)
		}
	}
	ids := browser.VisibleIDs()
	wantDef := []string{"user1", "assistant1", "tool1", "vis1", "comp1", "sum1", "user2"}
	if len(ids) != len(wantDef) {
		t.Fatalf("default visible = %v, want %v", ids, wantDef)
	}
	for i, id := range wantDef {
		if ids[i] != id {
			t.Fatalf("default order %d = %s, want %s", i, ids[i], id)
		}
	}
	if d := browser.DisplayDepth("user2"); d != 6 {
		t.Fatalf("default depth user2 = %d, want 6", d)
	}
	noTools := NewTreeBrowser(TreeBrowserOptions{Title: "Tree", Nodes: nodes, ActiveEntryID: "user2", Filter: TreeFilterNoTools}, NewDialogTheme(nil), nil)
	noSet := blocker6Set(noTools)
	for _, id := range []string{"user1", "assistant1", "vis1", "comp1", "sum1", "user2"} {
		if !noSet[id] {
			t.Fatalf("no-tools missing %s", id)
		}
	}
	if noSet["tool1"] {
		t.Fatalf("no-tools leaked tool")
	}
	for _, id := range []string{"prov1", "prof1", "gen1", "hid1", "lbl1", "inf1", "mod1", "thk1"} {
		if noSet[id] {
			t.Fatalf("no-tools leaked %s", id)
		}
	}
	userBrowser := NewTreeBrowser(TreeBrowserOptions{Title: "Tree", Nodes: nodes, ActiveEntryID: "user2", Filter: TreeFilterUser}, NewDialogTheme(nil), nil)
	userIDs := userBrowser.VisibleIDs()
	if len(userIDs) != 2 || userIDs[0] != "user1" || userIDs[1] != "user2" {
		t.Fatalf("user visible = %v, want user1 user2", userIDs)
	}
	labelled := NewTreeBrowser(TreeBrowserOptions{Title: "Tree", Nodes: nodes, Filter: TreeFilterLabelled}, NewDialogTheme(nil), nil)
	labelledIDs := labelled.VisibleIDs()
	if len(labelledIDs) != 1 || labelledIDs[0] != "user2" {
		t.Fatalf("labelled visible = %v, want user2", labelledIDs)
	}
	all := NewTreeBrowser(TreeBrowserOptions{Title: "Tree", Nodes: nodes, Filter: TreeFilterAll}, NewDialogTheme(nil), nil)
	allIDs := all.VisibleIDs()
	if len(allIDs) != len(nodes) {
		t.Fatalf("all visible = %d, want %d", len(allIDs), len(nodes))
	}
	for i, n := range nodes {
		if allIDs[i] != n.ID {
			t.Fatalf("all order %d = %s, want %s", i, allIDs[i], n.ID)
		}
	}
	if !isTreeBookkeepingKind("custom") || !isTreeBookkeepingKind("provenance") || !isTreeBookkeepingKind("profile") || !isTreeBookkeepingKind("custom-hidden") {
		t.Fatal("bookkeeping kinds must include custom provenance profile custom-hidden")
	}
	if isTreeBookkeepingKind("custom-message") {
		t.Fatal("custom-message must be conversation visible")
	}
	if !isTreeConversationKind("custom-message") || isTreeConversationKind("custom") {
		t.Fatal("conversation classification mismatch")
	}
}
