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

func p4BlockerChain(t *testing.T) (*session.Loader, string, string, string) {
	t.Helper()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"first question"`), Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendAssistant(&agent.AssistantMessage{
		Role: "assistant",
		Content: []agent.ContentBlock{
			{Type: agent.BlockTypeText, Text: "working"},
			{Type: agent.BlockTypeToolCall, ID: "call_1", Name: "probe", Arguments: json.RawMessage(`{}`)},
		},
		StopReason: "toolUse",
		Timestamp:  2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendToolResult(&agent.ToolResultMessage{
		Role: "toolResult", ToolCallID: "call_1", ToolName: "probe",
		Content:   []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "probe output"}},
		Timestamp: 3,
	}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"follow up"`), Timestamp: 4}); err != nil {
		t.Fatal(err)
	}
	path := sess.Path()
	sess.Close()
	loader, err := session.LoadWithOptions(path, session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	branch, err := loader.ActiveBranch()
	if err != nil {
		t.Fatal(err)
	}
	if len(branch) != 4 {
		t.Fatalf("branch = %d, want 4", len(branch))
	}
	userID := session.EntryID(branch[0])
	assistantID := session.EntryID(branch[1])
	toolID := session.EntryID(branch[2])
	return loader, userID, assistantID, toolID
}

func p4NodeByID(nodes []interactive.TreeBrowserNode, id string) interactive.TreeBrowserNode {
	for _, n := range nodes {
		if n.ID == id {
			return n
		}
	}
	return interactive.TreeBrowserNode{}
}

func TestP4BlockerRealisticChainFilters(t *testing.T) {
	loader, userID, assistantID, toolID := p4BlockerChain(t)
	nodes, _ := buildTreeNodes(loader, false, false)
	if len(nodes) < 4 {
		t.Fatalf("nodes = %d, want at least 4", len(nodes))
	}
	byKind := map[string]string{}
	for _, n := range nodes {
		byKind[n.ID] = n.Kind
	}
	if byKind[userID] != "user" {
		t.Fatalf("user kind = %s", byKind[userID])
	}
	if byKind[assistantID] != "assistant" {
		t.Fatalf("assistant kind = %s", byKind[assistantID])
	}
	if byKind[toolID] != "tool" {
		t.Fatalf("tool kind = %s", byKind[toolID])
	}
	userBrowser := interactive.NewTreeBrowser(interactive.TreeBrowserOptions{Title: "Tree", Nodes: nodes, ActiveEntryID: toolID, Filter: interactive.TreeFilterUser}, interactive.NewDialogTheme(nil), nil)
	for _, id := range userBrowser.VisibleIDs() {
		n := p4NodeByID(nodes, id)
		if n.Kind != "user" {
			t.Fatalf("user filter leaked %s kind %s", id, n.Kind)
		}
	}
	if got := userBrowser.SelectedID(); got != userID {
		t.Fatalf("user selection = %s, want nearest previous %s", got, userID)
	}
	noTools := interactive.NewTreeBrowser(interactive.TreeBrowserOptions{Title: "Tree", Nodes: nodes, ActiveEntryID: toolID, Filter: interactive.TreeFilterNoTools}, interactive.NewDialogTheme(nil), nil)
	for _, id := range noTools.VisibleIDs() {
		if id == toolID {
			t.Fatalf("no-tools leaked tool %s", id)
		}
	}
	def := interactive.NewTreeBrowser(interactive.TreeBrowserOptions{Title: "Tree", Nodes: nodes}, interactive.NewDialogTheme(nil), nil)
	if len(def.VisibleIDs()) < 4 {
		t.Fatalf("default visible = %v, want chain", def.VisibleIDs())
	}
}

func TestP4BlockerTombstonesClearInFileOrder(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"head"`), Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	loader, err := session.LoadWithOptions(sess.Path(), session.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	target := session.EntryID(loader.Leaf())
	first := "first"
	if err := sess.AppendEntry(&session.LabelEntry{TargetID: target, Label: &first}); err != nil {
		t.Fatal(err)
	}
	second := "second"
	if err := sess.AppendEntry(&session.LabelEntry{TargetID: target, Label: &second}); err != nil {
		t.Fatal(err)
	}
	path := sess.Path()
	sess.Close()
	loader, err = session.LoadWithOptions(path, session.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	nodes, _ := buildTreeNodes(loader, false, false)
	if got := p4NodeByID(nodes, target).Label; got != "second" {
		t.Fatalf("label = %q, want second", got)
	}
	if ts := p4NodeByID(nodes, target).LabelTimestamp; ts == "" {
		t.Fatal("label timestamp must be set for latest label")
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	loader2, err := session.LoadWithOptions(path, session.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	leaf := session.EntryID(loader2.Leaf())
	nilLine := `{"type":"label","id":"tomb1","parentId":"` + leaf + `","timestamp":"2026-01-01T00:00:10.000Z","targetId":"` + target + `","label":null}` + "\n"
	if _, err := f.WriteString(nilLine); err != nil {
		t.Fatal(err)
	}
	f.Close()
	loader, err = session.LoadWithOptions(path, session.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	nodes, _ = buildTreeNodes(loader, false, false)
	if got := p4NodeByID(nodes, target).Label; got != "" {
		t.Fatalf("label after nil tombstone = %q, want cleared", got)
	}
	labelled := interactive.NewTreeBrowser(interactive.TreeBrowserOptions{Title: "Tree", Nodes: nodes, Filter: interactive.TreeFilterLabelled}, interactive.NewDialogTheme(nil), nil)
	for _, id := range labelled.VisibleIDs() {
		if id == target {
			t.Fatalf("labelled filter leaked tombstoned %s", id)
		}
	}
	emptyLine := `{"type":"label","id":"tomb2","parentId":"tomb1","timestamp":"2026-01-01T00:00:11.000Z","targetId":"` + target + `"}` + "\n"
	f, err = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(emptyLine); err != nil {
		t.Fatal(err)
	}
	f.Close()
	third := "third"
	f, err = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	thirdLine := `{"type":"label","id":"lbl3","parentId":"tomb2","timestamp":"2026-01-01T00:00:12.000Z","targetId":"` + target + `","label":"` + third + `"}` + "\n"
	if _, err := f.WriteString(thirdLine); err != nil {
		t.Fatal(err)
	}
	f.Close()
	loader, err = session.LoadWithOptions(path, session.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	nodes, _ = buildTreeNodes(loader, false, false)
	if got := p4NodeByID(nodes, target).Label; got != third {
		t.Fatalf("label after re-label = %q, want third", got)
	}
	emptyStr := ""
	_ = emptyStr
	blank := "   "
	blankLine := `{"type":"label","id":"tomb3","parentId":"lbl3","timestamp":"2026-01-01T00:00:13.000Z","targetId":"` + target + `","label":"` + blank + `"}` + "\n"
	f, err = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(blankLine); err != nil {
		t.Fatal(err)
	}
	f.Close()
	loader, err = session.LoadWithOptions(path, session.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	nodes, _ = buildTreeNodes(loader, false, false)
	if got := p4NodeByID(nodes, target).Label; got != "" {
		t.Fatalf("label after whitespace tombstone = %q, want cleared", got)
	}
}

func TestP4BlockerTwoEditsAndClearAppendWithoutRewrite(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"entry"`), Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	loader, err := session.LoadWithOptions(sess.Path(), session.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	target := session.EntryID(loader.Leaf())
	before, err := os.ReadFile(sess.Path())
	if err != nil {
		t.Fatal(err)
	}
	beforeLines := len(strings.Split(strings.TrimSpace(string(before)), "\n"))
	v1 := "alpha"
	if err := sess.AppendEntry(&session.LabelEntry{TargetID: target, Label: &v1}); err != nil {
		t.Fatal(err)
	}
	v2 := "beta"
	if err := sess.AppendEntry(&session.LabelEntry{TargetID: target, Label: &v2}); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendEntry(&session.LabelEntry{TargetID: target}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(sess.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(after), string(before)) {
		t.Fatal("label edits rewrote the session prefix")
	}
	afterLines := len(strings.Split(strings.TrimSpace(string(after)), "\n"))
	if afterLines != beforeLines+3 {
		t.Fatalf("lines = %d, want %d after two edits and clear", afterLines, beforeLines+3)
	}
	loader, err = session.LoadWithOptions(sess.Path(), session.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	nodes, _ := buildTreeNodes(loader, false, false)
	if got := p4NodeByID(nodes, target).Label; got != "" {
		t.Fatalf("final label = %q, want cleared after two edits and clear", got)
	}
	loader, err = session.LoadWithOptions(sess.Path(), session.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, e := range loader.Entries() {
		if _, ok := e.(*session.LabelEntry); ok {
			count++
		}
	}
	if count != 3 {
		t.Fatalf("label entries = %d, want 3 appended", count)
	}
}
