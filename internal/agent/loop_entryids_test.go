package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type capturingPreparer struct {
	requests []ContextRequest
}

func (p *capturingPreparer) Prepare(ctx context.Context, req ContextRequest) (ContextResult, error) {
	p.requests = append(p.requests, req)
	return ContextResult{Messages: req.Messages, System: req.System}, nil
}

func (p *capturingPreparer) ObserveRequest(t time.Time) {}

func (p *capturingPreparer) ObserveResponse(m *AssistantMessage) {}

func TestRunTurnRefreshesSessionEntryIDsPerRequest(t *testing.T) {
	tool := &fakeTool{name: "probe", result: TextResult("probe result")}
	client := &fakeClient{script: []*AssistantMessage{
		toolUseMsg(toolCallBlock("call_1", "probe", `{}`)),
		textStop("done"),
	}}
	hooks := &fakeHooks{}
	var hookSeen [][]string
	hooks.contextFn = func(ctx context.Context, req ContextRequest) (ContextResult, error) {
		hookSeen = append(hookSeen, append([]string(nil), req.EntryIDs...))
		return ContextResult{Messages: req.Messages, System: req.System}, nil
	}
	preparer := &capturingPreparer{}
	var refreshSeen [][]string
	deps := &LoopDeps{
		Client:   client,
		Tools:    []Tool{tool},
		Hooks:    hooks,
		Preparer: preparer,
		RefreshSessionEntryIDs: func(history []*Message) ([]string, error) {
			ids := make([]string, len(history))
			for i := range ids {
				ids[i] = fmt.Sprintf("entry-%d", i)
			}
			refreshSeen = append(refreshSeen, ids)
			return ids, nil
		},
	}
	history, err := RunTurn(context.Background(), deps, "test/model", "", nil, "hello")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if len(history) != 4 {
		t.Fatalf("history length = %d, want 4", len(history))
	}
	if len(refreshSeen) != 2 {
		t.Fatalf("refresh calls = %d, want one per context request", len(refreshSeen))
	}
	if len(refreshSeen[0]) != 1 || len(refreshSeen[1]) != 3 {
		t.Fatalf("refresh alignments = %d,%d, want 1,3", len(refreshSeen[0]), len(refreshSeen[1]))
	}
	if len(preparer.requests) != len(refreshSeen) {
		t.Fatalf("prepare requests = %d, want %d", len(preparer.requests), len(refreshSeen))
	}
	for i, request := range preparer.requests {
		if len(request.EntryIDs) != len(request.Messages) {
			t.Fatalf("request %d entry ids %d messages %d must align", i, len(request.EntryIDs), len(request.Messages))
		}
		if strings.Join(request.EntryIDs, ",") != strings.Join(refreshSeen[i], ",") {
			t.Fatalf("request %d entry ids = %v, want %v", i, request.EntryIDs, refreshSeen[i])
		}
	}
	if len(hookSeen) != len(refreshSeen) {
		t.Fatalf("hook requests = %d, want %d", len(hookSeen), len(refreshSeen))
	}
	for i := range hookSeen {
		if strings.Join(hookSeen[i], ",") != strings.Join(refreshSeen[i], ",") {
			t.Fatalf("request %d entry ids = %v, want %v", i, hookSeen[i], refreshSeen[i])
		}
	}
}

func TestRunTurnRefreshSessionEntryIDsErrorSurfaces(t *testing.T) {
	client := &fakeClient{script: []*AssistantMessage{textStop("done")}}
	deps := &LoopDeps{
		Client: client,
		RefreshSessionEntryIDs: func(history []*Message) ([]string, error) {
			return nil, errors.New("reload boom")
		},
	}
	_, err := RunTurn(context.Background(), deps, "test/model", "", nil, "hello")
	if err == nil || !strings.Contains(err.Error(), "refresh session entry ids") {
		t.Fatalf("RunTurn error = %v, want refresh failure", err)
	}
	if client.calls != 0 {
		t.Fatalf("client calls = %d, want none after a refresh failure", client.calls)
	}
}

func TestRunTurnWithoutRefreshKeepsStaticEntryIDs(t *testing.T) {
	client := &fakeClient{script: []*AssistantMessage{textStop("done")}}
	hooks := &fakeHooks{}
	var hookSeen []string
	hooks.contextFn = func(ctx context.Context, req ContextRequest) (ContextResult, error) {
		hookSeen = append([]string(nil), req.EntryIDs...)
		return ContextResult{Messages: req.Messages, System: req.System}, nil
	}
	deps := &LoopDeps{
		Client:          client,
		Hooks:           hooks,
		SessionEntryIDs: []string{"static-0"},
	}
	if _, err := RunTurn(context.Background(), deps, "test/model", "", nil, "hello"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if len(hookSeen) != 1 || hookSeen[0] != "static-0" {
		t.Fatalf("hook entry ids = %v, want the static list", hookSeen)
	}
}
