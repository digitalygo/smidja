package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func persistedUserMessage(text string) *Message {
	raw, _ := json.Marshal(text)
	return &Message{User: &UserMessage{Role: string(RoleUser), Content: raw, Timestamp: 1}}
}

func TestContinueTurnDoesNotAppendUser(t *testing.T) {
	client := &fakeClient{script: []*AssistantMessage{textStop("continued answer")}}
	rec := &fakeRecorder{}
	history := []*Message{persistedUserMessage("already persisted")}

	result, err := ContinueTurn(context.Background(), &LoopDeps{
		Client:   client,
		Recorder: rec,
	}, "test/model", "", history)
	if err != nil {
		t.Fatalf("ContinueTurn: %v", err)
	}
	if got := rec.joinEvents(); got != "assistant" {
		t.Fatalf("recorder events = %q, want only the assistant message", got)
	}
	if len(rec.users) != 0 {
		t.Fatalf("recorded %d user messages, want none", len(rec.users))
	}
	if client.attempts != 1 {
		t.Fatalf("client calls = %d, want 1", client.attempts)
	}
	if len(result) != 2 {
		t.Fatalf("history length = %d, want the persisted user plus the assistant", len(result))
	}
	if result[0].User == nil || result[0].User.Content == nil {
		t.Fatalf("first message = %+v, want the persisted user message", result[0])
	}
	if result[1].Assistant == nil || result[1].Assistant.StopReason != "stop" {
		t.Fatalf("second message = %+v, want the assistant answer", result[1])
	}
}

func TestContinueTurnRejectsMissingDependencies(t *testing.T) {
	history := []*Message{persistedUserMessage("kept")}
	if _, err := ContinueTurn(context.Background(), nil, "m", "", history); err == nil {
		t.Fatal("nil deps must fail")
	}
	if _, err := ContinueTurn(context.Background(), &LoopDeps{}, "m", "", history); err == nil {
		t.Fatal("nil client must fail")
	}
}

func TestContinueTurnReportsContextOverflow(t *testing.T) {
	client := &fakeClient{err: errors.New("provider: prompt is too long for requested model")}
	result, err := ContinueTurn(context.Background(), &LoopDeps{
		Client:            client,
		IsContextOverflow: func(message string) bool { return strings.Contains(message, "too long") },
	}, "test/model", "", []*Message{persistedUserMessage("kept")})
	if !errors.Is(err, ErrContextOverflow) {
		t.Fatalf("ContinueTurn error = %v, want ErrContextOverflow", err)
	}
	if client.attempts != 1 {
		t.Fatalf("client calls = %d, want 1", client.attempts)
	}
	if len(result) != 1 || result[0].User == nil {
		t.Fatalf("history = %+v, want only the persisted user message", result)
	}
}

func TestContinueTurnRefreshesEntryIDsPerRequest(t *testing.T) {
	tool := &fakeTool{name: "probe", result: TextResult("probe result")}
	client := &fakeClient{script: []*AssistantMessage{
		toolUseMsg(toolCallBlock("call_1", "probe", `{}`)),
		textStop("done"),
	}}
	preparer := &capturingPreparer{}
	var refreshed [][]string
	deps := &LoopDeps{
		Client:   client,
		Tools:    []Tool{tool},
		Preparer: preparer,
		RefreshSessionEntryIDs: func(history []*Message) ([]string, error) {
			ids := make([]string, len(history))
			for i := range ids {
				ids[i] = fmt.Sprintf("entry-%d", i)
			}
			refreshed = append(refreshed, ids)
			return ids, nil
		},
	}
	if _, err := ContinueTurn(context.Background(), deps, "test/model", "", []*Message{persistedUserMessage("persisted")}); err != nil {
		t.Fatalf("ContinueTurn: %v", err)
	}
	if len(refreshed) != 2 {
		t.Fatalf("refresh calls = %d, want one per context request", len(refreshed))
	}
	if len(refreshed[0]) != 1 || len(refreshed[1]) != 3 {
		t.Fatalf("refresh alignments = %d,%d, want 1,3", len(refreshed[0]), len(refreshed[1]))
	}
	for i, request := range preparer.requests {
		if len(request.EntryIDs) != len(request.Messages) {
			t.Fatalf("request %d entry ids %d messages %d must align", i, len(request.EntryIDs), len(request.Messages))
		}
		if strings.Join(request.EntryIDs, ",") != strings.Join(refreshed[i], ",") {
			t.Fatalf("request %d entry ids = %v, want %v", i, request.EntryIDs, refreshed[i])
		}
	}
}
