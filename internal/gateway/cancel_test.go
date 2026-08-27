package gateway

import (
	"context"
	"testing"
	"time"
)

func TestGatewayCancelUnknownKeyReturnsFalse(t *testing.T) {
	g := newTestGateway(t, Options{
		Runner:   instantRunner{},
		Resolver: fixedResolver("ws1", ""),
	})
	cancel := startGateway(t, g)
	defer cancel()
	if g.Cancel("telegram", "no-such-chat") {
		t.Fatal("cancel on an unknown chat key returned true")
	}
}

func TestGatewayCancelCurrentTurnEndToEnd(t *testing.T) {
	blocker := newBlockingRunner()
	sink := newDeliverySpy(4)
	g := newTestGateway(t, Options{
		MailboxSize: 4,
		Burst:       100,
		Runner:      blocker,
		Resolver:    fixedResolver("ws1", ""),
	})
	g.RegisterSink("telegram", sink)
	cancel := startGateway(t, g)
	defer cancel()
	msg := sampleMessage("m1")
	if _, err := g.Submit(context.Background(), msg); err != nil {
		t.Fatalf("submit: %v", err)
	}
	<-blocker.started
	if !g.Cancel(msg.Transport, msg.ExternalChatKey) {
		t.Fatal("cancel of the active turn returned false")
	}
	d := sink.next(t)
	if d.Err == nil {
		t.Fatalf("cancel delivery err = nil, want a context error")
	}
	if d.Result.Text != "" {
		t.Fatalf("cancel delivery result = %q, want empty", d.Result.Text)
	}
	rec := mustRecord(t, g.Journal(), "m1")
	if rec.Status != StatusOutcomeUnknown {
		t.Fatalf("record status = %s, want outcome_unknown", rec.Status)
	}
	waitFor(t, func() bool { return g.RateLimiter().ActiveTurns() == 0 }, "turn slot released after cancel")
}

func TestGatewayCancelIdempotentAfterTurnEnds(t *testing.T) {
	blocker := newBlockingRunner()
	sink := newDeliverySpy(4)
	g := newTestGateway(t, Options{
		Burst:    100,
		Runner:   blocker,
		Resolver: fixedResolver("ws1", ""),
	})
	g.RegisterSink("telegram", sink)
	cancel := startGateway(t, g)
	defer cancel()
	msg := sampleMessage("m1")
	if _, err := g.Submit(context.Background(), msg); err != nil {
		t.Fatalf("submit: %v", err)
	}
	<-blocker.started
	close(blocker.release)
	waitFor(t, func() bool {
		rec, ok := g.Journal().Get("m1")
		return ok && rec.Status == StatusCompleted
	}, "turn completes")
	sink.next(t)
	if g.Cancel(msg.Transport, msg.ExternalChatKey) {
		t.Fatal("cancel after the turn completed returned true")
	}
	select {
	case d := <-sink.ch:
		t.Fatalf("unexpected extra delivery after completed turn: %+v", d)
	case <-time.After(30 * time.Millisecond):
	}
}
