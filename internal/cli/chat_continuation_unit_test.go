package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/session"
)

func TestAttachProjectedEntryIDs(t *testing.T) {
	existing := func([]*agent.Message) ([]string, error) { return []string{"kept"}, nil }

	d := &runDeps{}
	d.attachProjectedEntryIDs(nil)

	preserved := &agent.LoopDeps{RefreshSessionEntryIDs: existing}
	d.attachProjectedEntryIDs(preserved)
	if preserved.RefreshSessionEntryIDs == nil {
		t.Fatal("the existing refresh function must be preserved")
	}
	ids, err := preserved.RefreshSessionEntryIDs(nil)
	if err != nil || len(ids) != 1 || ids[0] != "kept" {
		t.Fatalf("preserved refresh = %v, %v", ids, err)
	}

	withoutSource := &agent.LoopDeps{}
	d.attachProjectedEntryIDs(withoutSource)
	if withoutSource.RefreshSessionEntryIDs != nil {
		t.Fatal("a deps without a session source must not gain a refresh function")
	}

	withSource := &runDeps{sessionPath: "/tmp/whatever"}
	attached := &agent.LoopDeps{}
	withSource.attachProjectedEntryIDs(attached)
	if attached.RefreshSessionEntryIDs == nil {
		t.Fatal("a session path must install a refresh function")
	}
}

func TestLoadProjectedContextWithoutSource(t *testing.T) {
	d := &runDeps{}
	if _, _, err := d.loadProjectedContext(); err == nil {
		t.Fatal("a missing session source must fail")
	} else if !strings.Contains(err.Error(), "no active session") {
		t.Fatalf("error = %v, want the missing-session message", err)
	}
}

func TestLoadProjectedContextReloadFailure(t *testing.T) {
	d := &runDeps{sessionPath: "/no/such/session.jsonl"}
	if _, _, err := d.loadProjectedContext(); err == nil {
		t.Fatal("a load failure must surface")
	}
}

func TestLoadProjectedContextControllerFailure(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	seedContinuationHistory(t, sess, 1)
	controller := testController(t, store, cwd)
	controller.Hold(sess)
	t.Cleanup(func() { _ = controller.Close() })
	controller.reload = func(string) (*session.Loader, error) { return nil, errors.New("reload boom") }

	d := &runDeps{controller: controller}
	if _, _, err := d.loadProjectedContext(); err == nil {
		t.Fatal("a controller reload failure must surface")
	}
}

func TestRefreshProjectedEntryIDsRejectsMisalignment(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	if err := sess.AppendUser(&agent.UserMessage{Role: string(agent.RoleUser), Content: []byte(`"one"`), Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	d := &runDeps{sessionPath: sess.Path()}
	if _, err := d.refreshProjectedEntryIDs(nil); err == nil {
		t.Fatal("a misaligned history must be rejected")
	} else if !strings.Contains(err.Error(), "do not align") {
		t.Fatalf("error = %v, want the alignment message", err)
	}
	history := []*agent.Message{{User: &agent.UserMessage{Role: string(agent.RoleUser), Content: []byte(`"one"`), Timestamp: 1}}}
	ids, err := d.refreshProjectedEntryIDs(history)
	if err != nil {
		t.Fatalf("aligned history: %v", err)
	}
	if len(ids) != 1 || ids[0] == "" {
		t.Fatalf("entry ids = %v, want one real id", ids)
	}
}

func TestRefreshProjectedEntryIDsSurfacesLoadFailure(t *testing.T) {
	d := &runDeps{sessionPath: "/no/such/session.jsonl"}
	history := []*agent.Message{{User: &agent.UserMessage{Role: string(agent.RoleUser), Content: []byte(`"one"`), Timestamp: 1}}}
	if _, err := d.refreshProjectedEntryIDs(history); err == nil {
		t.Fatal("a load failure must surface")
	}
}

func TestIsProviderErrorAssistant(t *testing.T) {
	if isProviderErrorAssistant(nil) {
		t.Fatal("nil messages are not provider errors")
	}
	if isProviderErrorAssistant(&agent.Message{User: &agent.UserMessage{Role: string(agent.RoleUser)}}) {
		t.Fatal("user messages are not provider errors")
	}
	if isProviderErrorAssistant(&agent.Message{Assistant: &agent.AssistantMessage{StopReason: "stop"}}) {
		t.Fatal("successful assistants are not provider errors")
	}
	if !isProviderErrorAssistant(&agent.Message{Assistant: &agent.AssistantMessage{StopReason: "error"}}) {
		t.Fatal("a plain provider error assistant must be excluded")
	}
	withToolCall := &agent.Message{Assistant: &agent.AssistantMessage{StopReason: "error", Content: []agent.ContentBlock{{Type: agent.BlockTypeToolCall, ID: "call_1", Name: "probe"}}}}
	if isProviderErrorAssistant(withToolCall) {
		t.Fatal("a provider error assistant that carries tool calls must stay paired")
	}
}
