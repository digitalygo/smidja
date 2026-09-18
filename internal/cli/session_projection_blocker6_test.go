package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/internal/tui/interactive"
)

func blocker6LeafID(t *testing.T, path string) string {
	t.Helper()
	loader, err := session.LoadWithOptions(path, session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	return session.EntryID(loader.Leaf())
}

func blocker6BuildFull(t *testing.T) (string, map[string]string) {
	t.Helper()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"first question"`), Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	ids["user1"] = blocker6LeafID(t, sess.Path())
	if err := sess.AppendAssistant(&agent.AssistantMessage{
		Role: "assistant",
		Content: []agent.ContentBlock{
			{Type: agent.BlockTypeText, Text: "working"},
			{Type: agent.BlockTypeToolCall, ID: "call_b6", Name: "probe", Arguments: json.RawMessage(`{}`)},
		},
		StopReason: "toolUse",
		Timestamp:  2,
	}); err != nil {
		t.Fatal(err)
	}
	ids["assistant"] = blocker6LeafID(t, sess.Path())
	if err := sess.AppendToolResult(&agent.ToolResultMessage{
		Role: "toolResult", ToolCallID: "call_b6", ToolName: "probe",
		Content:   []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "probe output"}},
		Timestamp: 3,
	}); err != nil {
		t.Fatal(err)
	}
	ids["tool"] = blocker6LeafID(t, sess.Path())
	if err := sess.AppendEntry(&session.CustomEntry{CustomType: sessionProvenanceCustomType, Data: json.RawMessage(`{"origin":"created"}`)}); err != nil {
		t.Fatal(err)
	}
	ids["provenance"] = blocker6LeafID(t, sess.Path())
	if err := sess.AppendEntry(&session.CustomEntry{CustomType: session.RuntimeProfileCustomType, Data: json.RawMessage(`{"providerID":"test"}`)}); err != nil {
		t.Fatal(err)
	}
	ids["profile"] = blocker6LeafID(t, sess.Path())
	if err := sess.AppendEntry(&session.CustomEntry{CustomType: "bookkeeping", Data: json.RawMessage(`{"x":1}`)}); err != nil {
		t.Fatal(err)
	}
	ids["generic"] = blocker6LeafID(t, sess.Path())
	if err := sess.AppendEntry(&session.CustomMessageEntry{CustomType: "notice", Content: json.RawMessage(`"visible payload"`), Display: true}); err != nil {
		t.Fatal(err)
	}
	ids["visibleCustom"] = blocker6LeafID(t, sess.Path())
	if err := sess.AppendEntry(&session.CustomMessageEntry{CustomType: "hidden-note", Content: json.RawMessage(`"should stay out"`), Display: false}); err != nil {
		t.Fatal(err)
	}
	ids["hiddenCustom"] = blocker6LeafID(t, sess.Path())
	label := "keep"
	if err := sess.AppendEntry(&session.LabelEntry{TargetID: ids["user1"], Label: &label}); err != nil {
		t.Fatal(err)
	}
	ids["labelEntry"] = blocker6LeafID(t, sess.Path())
	name := "renamed session"
	if err := sess.AppendEntry(&session.SessionInfoEntry{Name: &name}); err != nil {
		t.Fatal(err)
	}
	ids["info"] = blocker6LeafID(t, sess.Path())
	if err := sess.AppendEntry(&session.ModelChangeEntry{Provider: "openrouter", ModelID: "test/model"}); err != nil {
		t.Fatal(err)
	}
	ids["model"] = blocker6LeafID(t, sess.Path())
	if err := sess.AppendEntry(&session.ThinkingLevelChangeEntry{ThinkingLevel: "high"}); err != nil {
		t.Fatal(err)
	}
	ids["thinking"] = blocker6LeafID(t, sess.Path())
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"follow up"`), Timestamp: 4}); err != nil {
		t.Fatal(err)
	}
	ids["user2"] = blocker6LeafID(t, sess.Path())
	path := sess.Path()
	sess.Close()
	return path, ids
}

func blocker6NodesByID(nodes []interactive.TreeBrowserNode) map[string]interactive.TreeBrowserNode {
	out := map[string]interactive.TreeBrowserNode{}
	for _, n := range nodes {
		out[n.ID] = n
	}
	return out
}

func blocker6VisibleSet(browser *interactive.TreeBrowser) map[string]bool {
	out := map[string]bool{}
	for _, id := range browser.VisibleIDs() {
		out[id] = true
	}
	return out
}

func TestP4Blocker6BookkeepingFilters(t *testing.T) {
	path, ids := blocker6BuildFull(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	loader, err := session.LoadWithOptions(path, session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	nodes, warnings := buildTreeNodes(loader, false, false)
	_ = warnings
	if len(nodes) != 13 {
		t.Fatalf("nodes = %d, want 13", len(nodes))
	}
	byID := blocker6NodesByID(nodes)
	wantKinds := map[string]string{
		ids["user1"]:         "user",
		ids["assistant"]:     "assistant",
		ids["tool"]:          "tool",
		ids["provenance"]:    "provenance",
		ids["profile"]:       "profile",
		ids["generic"]:       "custom",
		ids["visibleCustom"]: "custom-message",
		ids["hiddenCustom"]:  "custom-hidden",
		ids["labelEntry"]:    "label",
		ids["info"]:          "info",
		ids["model"]:         "model",
		ids["thinking"]:      "thinking",
		ids["user2"]:         "user",
	}
	for id, want := range wantKinds {
		got, ok := byID[id]
		if !ok {
			t.Fatalf("missing node %s", id)
		}
		if got.Kind != want {
			t.Fatalf("node %s kind = %s, want %s", id, got.Kind, want)
		}
	}
	if byID[ids["generic"]].Kind == byID[ids["visibleCustom"]].Kind {
		t.Fatalf("generic kind %q must differ from displayable custom %q", byID[ids["generic"]].Kind, byID[ids["visibleCustom"]].Kind)
	}
	if byID[ids["visibleCustom"]].Kind == byID[ids["hiddenCustom"]].Kind {
		t.Fatalf("displayable kind must differ from hidden kind")
	}
	if byID[ids["provenance"]].CustomType != sessionProvenanceCustomType {
		t.Fatalf("provenance CustomType = %q", byID[ids["provenance"]].CustomType)
	}
	if byID[ids["profile"]].CustomType != session.RuntimeProfileCustomType {
		t.Fatalf("profile CustomType = %q", byID[ids["profile"]].CustomType)
	}
	if byID[ids["generic"]].CustomType != "bookkeeping" {
		t.Fatalf("generic CustomType = %q", byID[ids["generic"]].CustomType)
	}
	if byID[ids["visibleCustom"]].CustomType != "notice" || !byID[ids["visibleCustom"]].Display {
		t.Fatalf("visible custom metadata = %+v", byID[ids["visibleCustom"]])
	}
	if byID[ids["hiddenCustom"]].Display {
		t.Fatalf("hidden custom Display must be false")
	}
	if byID[ids["user1"]].Label != "keep" {
		t.Fatalf("user1 label = %q, want keep", byID[ids["user1"]].Label)
	}
	if byID[ids["user1"]].LabelTimestamp == "" {
		t.Fatal("user1 label timestamp must be set")
	}
	for i := 1; i < len(nodes); i++ {
		if nodes[i].Depth != nodes[i-1].Depth+1 {
			t.Fatalf("depth chain broken at %d: %d -> %d", i, nodes[i-1].Depth, nodes[i].Depth)
		}
	}
	ordered := []string{ids["user1"], ids["assistant"], ids["tool"], ids["provenance"], ids["profile"], ids["generic"], ids["visibleCustom"], ids["hiddenCustom"], ids["labelEntry"], ids["info"], ids["model"], ids["thinking"], ids["user2"]}
	for i, id := range ordered {
		if nodes[i].ID != id {
			t.Fatalf("order %d = %s, want %s", i, nodes[i].ID, id)
		}
	}
	defBrowser := interactive.NewTreeBrowser(interactive.TreeBrowserOptions{Title: "Tree", Nodes: nodes, ActiveEntryID: ids["user2"]}, interactive.NewDialogTheme(nil), nil)
	def := blocker6VisibleSet(defBrowser)
	for _, key := range []string{"user1", "assistant", "tool", "visibleCustom", "user2"} {
		if !def[ids[key]] {
			t.Fatalf("default missing %s (%s): %v", key, ids[key], def)
		}
	}
	for _, key := range []string{"provenance", "profile", "generic", "hiddenCustom", "labelEntry", "info", "model", "thinking"} {
		if def[ids[key]] {
			t.Fatalf("default leaked bookkeeping %s (%s)", key, ids[key])
		}
	}
	defIDs := defBrowser.VisibleIDs()
	wantDef := []string{ids["user1"], ids["assistant"], ids["tool"], ids["visibleCustom"], ids["user2"]}
	if len(defIDs) != len(wantDef) {
		t.Fatalf("default visible = %v, want %v", defIDs, wantDef)
	}
	for i, id := range wantDef {
		if defIDs[i] != id {
			t.Fatalf("default order %d = %s, want %s (%v)", i, defIDs[i], id, defIDs)
		}
	}
	if d := defBrowser.DisplayDepth(ids["user2"]); d != 4 {
		t.Fatalf("default display depth user2 = %d, want 4", d)
	}
	if d := defBrowser.DisplayDepth(ids["visibleCustom"]); d != 3 {
		t.Fatalf("default display depth visibleCustom = %d, want 3", d)
	}
	noToolsBrowser := interactive.NewTreeBrowser(interactive.TreeBrowserOptions{Title: "Tree", Nodes: nodes, ActiveEntryID: ids["user2"], Filter: interactive.TreeFilterNoTools}, interactive.NewDialogTheme(nil), nil)
	noTools := blocker6VisibleSet(noToolsBrowser)
	for _, key := range []string{"user1", "assistant", "visibleCustom", "user2"} {
		if !noTools[ids[key]] {
			t.Fatalf("no-tools missing %s", key)
		}
	}
	for _, key := range []string{"tool", "provenance", "profile", "generic", "hiddenCustom", "labelEntry", "info", "model", "thinking"} {
		if noTools[ids[key]] {
			t.Fatalf("no-tools leaked %s", key)
		}
	}
	noToolsIDs := noToolsBrowser.VisibleIDs()
	wantNoTools := []string{ids["user1"], ids["assistant"], ids["visibleCustom"], ids["user2"]}
	if len(noToolsIDs) != len(wantNoTools) {
		t.Fatalf("no-tools visible = %v, want %v", noToolsIDs, wantNoTools)
	}
	for i, id := range wantNoTools {
		if noToolsIDs[i] != id {
			t.Fatalf("no-tools order %d = %s, want %s", i, noToolsIDs[i], id)
		}
	}
	userBrowser := interactive.NewTreeBrowser(interactive.TreeBrowserOptions{Title: "Tree", Nodes: nodes, ActiveEntryID: ids["user2"], Filter: interactive.TreeFilterUser}, interactive.NewDialogTheme(nil), nil)
	userVisible := userBrowser.VisibleIDs()
	if len(userVisible) != 2 || userVisible[0] != ids["user1"] || userVisible[1] != ids["user2"] {
		t.Fatalf("user visible = %v, want [user1 user2]", userVisible)
	}
	for _, id := range userVisible {
		if byID[id].Kind != "user" {
			t.Fatalf("user filter leaked kind %s", byID[id].Kind)
		}
	}
	if d := userBrowser.DisplayDepth(ids["user2"]); d != 1 {
		t.Fatalf("user display depth user2 = %d, want 1", d)
	}
	labelledBrowser := interactive.NewTreeBrowser(interactive.TreeBrowserOptions{Title: "Tree", Nodes: nodes, ActiveEntryID: ids["user2"], Filter: interactive.TreeFilterLabelled}, interactive.NewDialogTheme(nil), nil)
	labelledVisible := labelledBrowser.VisibleIDs()
	if len(labelledVisible) != 1 || labelledVisible[0] != ids["user1"] {
		t.Fatalf("labelled visible = %v, want only user1", labelledVisible)
	}
	allBrowser := interactive.NewTreeBrowser(interactive.TreeBrowserOptions{Title: "Tree", Nodes: nodes, ActiveEntryID: ids["user2"], Filter: interactive.TreeFilterAll}, interactive.NewDialogTheme(nil), nil)
	allVisible := allBrowser.VisibleIDs()
	if len(allVisible) != len(nodes) {
		t.Fatalf("all visible = %d, want %d", len(allVisible), len(nodes))
	}
	for i, id := range ordered {
		if allVisible[i] != id {
			t.Fatalf("all order %d = %s, want %s", i, allVisible[i], id)
		}
	}
	for _, id := range allVisible {
		if d := allBrowser.DisplayDepth(id); d < 0 || d > 8 {
			t.Fatalf("all display depth %s = %d out of range", id, d)
		}
	}
	history, entryIDs, _, err := projectModelHistoryWithIDs(loader)
	if err != nil {
		t.Fatal(err)
	}
	text := p4HistoryText(history)
	if !strings.Contains(text, "visible payload") {
		t.Fatalf("history missing displayable custom:\n%s", text)
	}
	if !strings.Contains(text, "should stay out") {
		t.Fatalf("history missing hidden custom payload:\n%s", text)
	}
	if !strings.Contains(text, ids["hiddenCustom"]) {
		t.Fatalf("history missing the hidden custom real entry id:\n%s", text)
	}
	for _, bad := range []string{"bookkeeping", "renamed session", "keep"} {
		if strings.Contains(text, bad) {
			t.Fatalf("history leaked %q:\n%s", bad, text)
		}
	}
	if len(history) != len(entryIDs) {
		t.Fatalf("history %d entryIDs %d must align", len(history), len(entryIDs))
	}
	foundHiddenID := false
	for _, id := range entryIDs {
		if id == ids["hiddenCustom"] {
			foundHiddenID = true
		}
	}
	if !foundHiddenID {
		t.Fatalf("entry ids %v must include the hidden custom real id %s", entryIDs, ids["hiddenCustom"])
	}
	transcript, _ := projectTranscript(loader)
	foundVisible := false
	for _, item := range transcript {
		if item.Kind == interactive.ReplayCustom && item.CustomType == session.RuntimeProfileCustomType {
			t.Fatal("transcript leaked runtime profile")
		}
		if item.Kind == interactive.ReplayCustom && item.CustomType == sessionProvenanceCustomType {
			t.Fatal("transcript leaked provenance")
		}
		if item.Kind == interactive.ReplayCustom && item.CustomType == "hidden-note" {
			t.Fatal("transcript leaked hidden custom")
		}
		if item.Kind == interactive.ReplayCustom && item.CustomType == "notice" && strings.Contains(item.Text, "visible payload") {
			foundVisible = true
		}
	}
	if !foundVisible {
		t.Fatal("transcript missing displayable custom")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("projection changed the session file")
	}
}

func TestP4Blocker6RealNewSessionHidesProvenanceProfile(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	sess, _, err := createTrackedSession(store, cwd)
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"hello"`), Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	cur := session.CurrentProfile{ProviderID: "test", ModelID: "test/model", OrderingVersion: 1}
	if _, err := sess.PersistRuntimeProfile(cur, func() string { return "fp-b6" }); err != nil {
		t.Fatal(err)
	}
	path := sess.Path()
	sess.Close()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), sessionProvenanceCustomType) {
		t.Fatal("new session file missing provenance")
	}
	if !strings.Contains(string(content), session.RuntimeProfileCustomType) {
		t.Fatal("new session file missing runtime profile")
	}
	loader, err := session.LoadWithOptions(path, session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	nodes, _ := buildTreeNodes(loader, false, false)
	byID := blocker6NodesByID(nodes)
	provenanceCount := 0
	profileCount := 0
	for _, n := range nodes {
		switch n.Kind {
		case "provenance":
			provenanceCount++
			if n.CustomType != sessionProvenanceCustomType {
				t.Fatalf("provenance metadata = %q", n.CustomType)
			}
		case "profile":
			profileCount++
			if n.CustomType != session.RuntimeProfileCustomType {
				t.Fatalf("profile metadata = %q", n.CustomType)
			}
		}
		_ = byID
	}
	if provenanceCount != 1 {
		t.Fatalf("provenance nodes = %d, want 1", provenanceCount)
	}
	if profileCount != 1 {
		t.Fatalf("profile nodes = %d, want 1", profileCount)
	}
	def := blocker6VisibleSet(interactive.NewTreeBrowser(interactive.TreeBrowserOptions{Title: "Tree", Nodes: nodes}, interactive.NewDialogTheme(nil), nil))
	noTools := blocker6VisibleSet(interactive.NewTreeBrowser(interactive.TreeBrowserOptions{Title: "Tree", Nodes: nodes, Filter: interactive.TreeFilterNoTools}, interactive.NewDialogTheme(nil), nil))
	for _, n := range nodes {
		if n.Kind == "provenance" || n.Kind == "profile" {
			if def[n.ID] {
				t.Fatalf("default shows %s %s", n.Kind, n.ID)
			}
			if noTools[n.ID] {
				t.Fatalf("no-tools shows %s %s", n.Kind, n.ID)
			}
		}
	}
	all := blocker6VisibleSet(interactive.NewTreeBrowser(interactive.TreeBrowserOptions{Title: "Tree", Nodes: nodes, Filter: interactive.TreeFilterAll}, interactive.NewDialogTheme(nil), nil))
	for _, n := range nodes {
		if !all[n.ID] {
			t.Fatalf("all missing %s %s", n.Kind, n.ID)
		}
	}
}
