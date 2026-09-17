package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/session"
)

func TestBridgeShowTreeLabelTwoEditsAndClear(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	if err := fixture.sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"first question"`), Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.sess.AppendAssistant(&agent.AssistantMessage{
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
	if err := fixture.sess.AppendToolResult(&agent.ToolResultMessage{
		Role: "toolResult", ToolCallID: "call_1", ToolName: "probe",
		Content:   []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "probe output"}},
		Timestamp: 3,
	}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"follow up"`), Timestamp: 4}); err != nil {
		t.Fatal(err)
	}
	loader, err := session.LoadWithOptions(fixture.sess.Path(), session.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	target := session.EntryID(loader.Leaf())
	if target == "" {
		t.Fatal("missing leaf target")
	}
	done := make(chan error, 1)
	go func() { done <- fixture.bridge.showTree() }()
	waitForOutputSettled(t, fixture.terminal, "Session tree", 3*time.Second)
	fixture.terminal.SendInput("l")
	waitForOutputSettled(t, fixture.terminal, "Label entry", 3*time.Second)
	fixture.terminal.SendInput("alpha")
	fixture.terminal.SendInput("\r")
	time.Sleep(300 * time.Millisecond)
	waitForOutputSettled(t, fixture.terminal, "Session tree", 3*time.Second)
	fixture.terminal.SendInput("l")
	waitForOutputSettled(t, fixture.terminal, "Label entry", 3*time.Second)
	fixture.terminal.SendInput("beta")
	fixture.terminal.SendInput("\r")
	time.Sleep(300 * time.Millisecond)
	waitForOutputSettled(t, fixture.terminal, "Session tree", 3*time.Second)
	fixture.terminal.SendInput("l")
	waitForOutputSettled(t, fixture.terminal, "Label entry", 3*time.Second)
	fixture.terminal.SendInput("\r")
	time.Sleep(300 * time.Millisecond)
	fixture.terminal.SendInput("\x1b")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("showTree = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("showTree did not close after label edits")
	}
	after, err := session.LoadWithOptions(fixture.sess.Path(), session.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, e := range after.Entries() {
		label, ok := e.(*session.LabelEntry)
		if !ok {
			continue
		}
		count++
		if label.TargetID != target {
			t.Fatalf("label target = %s, want preserved selection %s", label.TargetID, target)
		}
	}
	if count != 3 {
		t.Fatalf("label entries = %d, want 3 after two edits and clear", count)
	}
	nodes, _ := buildTreeNodes(after, false, false)
	for _, n := range nodes {
		if n.ID == target && strings.TrimSpace(n.Label) != "" {
			t.Fatalf("final label = %q, want cleared", n.Label)
		}
	}
}

func TestBridgeEditTreeLabelGuards(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	if err := fixture.bridge.editTreeLabel(nil, "", ""); err == nil {
		t.Fatal("edit with nil session must fail")
	}
	if err := fixture.bridge.editTreeLabel(fixture.bridge.sessions.Current(), "", ""); err == nil {
		t.Fatal("edit with empty target must fail")
	}
}

func TestBridgeEditTreeLabelSameKeepsFile(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	seedTurn(t, fixture.sess, "same label")
	loader, err := session.LoadWithOptions(fixture.sess.Path(), session.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	target := session.EntryID(loader.Leaf())
	keep := "keep"
	if err := fixture.sess.AppendEntry(&session.LabelEntry{TargetID: target, Label: &keep}); err != nil {
		t.Fatal(err)
	}
	before, err := session.LoadWithOptions(fixture.sess.Path(), session.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	beforeCount := len(before.Entries())
	done := make(chan error, 1)
	go func() { done <- fixture.bridge.showTree() }()
	waitForOutputSettled(t, fixture.terminal, "Session tree", 3*time.Second)
	fixture.terminal.SendInput("l")
	waitForOutputSettled(t, fixture.terminal, "Label entry", 3*time.Second)
	fixture.terminal.SendInput("keep")
	fixture.terminal.SendInput("\r")
	time.Sleep(300 * time.Millisecond)
	fixture.terminal.SendInput("\x1b")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("showTree = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("showTree did not close")
	}
	after, err := session.LoadWithOptions(fixture.sess.Path(), session.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Entries()) != beforeCount {
		t.Fatalf("same label appended %d entries, want no new entry", len(after.Entries())-beforeCount)
	}
}
