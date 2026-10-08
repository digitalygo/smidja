package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type mailboxCaptureClient struct {
	fake     *fakeClient
	requests []*TurnRequest
}

type mailboxEntryPreparer struct {
	entryCounts []int
}

func (p *mailboxEntryPreparer) Prepare(_ context.Context, req ContextRequest) (ContextResult, error) {
	p.entryCounts = append(p.entryCounts, len(req.EntryIDs))
	return ContextResult{Messages: req.Messages, System: req.System}, nil
}

func (*mailboxEntryPreparer) ObserveRequest(time.Time) {}

func (*mailboxEntryPreparer) ObserveResponse(*AssistantMessage) {}

func (c *mailboxCaptureClient) StreamTurn(ctx context.Context, request *TurnRequest, onText func(string), onThinking func(string)) (*AssistantMessage, error) {
	c.requests = append(c.requests, request)
	return c.fake.StreamTurn(ctx, request, onText, onThinking)
}

func TestRunTurnMailboxPollsNextTurnBeforeRequestAndFollowUpAtStop(t *testing.T) {
	client := &mailboxCaptureClient{fake: &fakeClient{script: []*AssistantMessage{
		textMessage("first reply", "stop"),
		textMessage("follow-up reply", "stop"),
	}}}
	var calls []string
	nextTurnDelivered := false
	preparer := &mailboxEntryPreparer{}
	deps := &LoopDeps{
		Client:   client,
		Preparer: preparer,
		RefreshSessionEntryIDs: func(history []*Message) ([]string, error) {
			return make([]string, len(history)), nil
		},
		Mailbox: func(_ context.Context, history []*Message, stopping, external bool) (MailboxResult, error) {
			calls = append(calls, mailboxCallName(stopping, external))
			if !stopping && external && !nextTurnDelivered {
				nextTurnDelivered = true
				next := append(append([]*Message(nil), history...), persistedUserMessage("next-turn"))
				return MailboxResult{Messages: next, PollAgain: true}, nil
			}
			if stopping && client.fake.calls == 1 {
				followUp := append(append([]*Message(nil), history...), persistedUserMessage("follow-up"))
				return MailboxResult{Messages: followUp, Continue: true}, nil
			}
			return MailboxResult{}, nil
		},
	}
	history, err := RunTurn(context.Background(), deps, "test/model", "", nil, "external")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if client.fake.calls != 2 {
		t.Fatalf("model calls = %d, want 2", client.fake.calls)
	}
	if got := mailboxUserTexts(client.requests[0]); !strings.Contains(got, "external") || !strings.Contains(got, "next-turn") {
		t.Fatalf("first request history = %q", got)
	}
	if got := mailboxUserTexts(client.requests[1]); !strings.Contains(got, "follow-up") {
		t.Fatalf("second request history = %q", got)
	}
	if len(history) != 5 {
		t.Fatalf("history length = %d, want external, next-turn, assistant, follow-up, assistant", len(history))
	}
	for i, req := range client.requests {
		if preparer.entryCounts[i] != len(req.Messages) {
			t.Errorf("request %d entry ids = %d, messages = %d", i, preparer.entryCounts[i], len(req.Messages))
		}
	}
	wantCalls := []string{"request-external", "request-external", "stop", "request", "stop"}
	if len(calls) != len(wantCalls) {
		t.Fatalf("mailbox boundaries = %v, want %v", calls, wantCalls)
	}
	for i := range wantCalls {
		if calls[i] != wantCalls[i] {
			t.Fatalf("mailbox boundaries = %v, want %v", calls, wantCalls)
		}
	}
}

func TestRunTurnMailboxErrorIsReturned(t *testing.T) {
	client := &fakeClient{script: []*AssistantMessage{textMessage("unused", "stop")}}
	deps := &LoopDeps{
		Client: client,
		Mailbox: func(context.Context, []*Message, bool, bool) (MailboxResult, error) {
			return MailboxResult{}, context.Canceled
		},
	}
	_, err := RunTurn(context.Background(), deps, "test/model", "", nil, "external")
	if err == nil || !strings.Contains(err.Error(), "deliver mailbox message: context canceled") {
		t.Fatalf("RunTurn error = %v", err)
	}
	if client.calls != 0 {
		t.Fatalf("model calls = %d, want 0", client.calls)
	}
}

func textMessage(text, stopReason string) *AssistantMessage {
	return &AssistantMessage{
		Role:       string(RoleAssistant),
		Content:    []ContentBlock{{Type: BlockTypeText, Text: text}},
		StopReason: stopReason,
		Usage:      Usage{},
	}
}

func mailboxCallName(stopping, external bool) string {
	if stopping {
		return "stop"
	}
	if external {
		return "request-external"
	}
	return "request"
}

func mailboxUserTexts(req *TurnRequest) string {
	var texts []string
	for _, message := range req.Messages {
		if message == nil || message.User == nil {
			continue
		}
		var text string
		if err := json.Unmarshal(message.User.Content, &text); err != nil {
			text = string(message.User.Content)
		}
		texts = append(texts, text)
	}
	return strings.Join(texts, "\n")
}
