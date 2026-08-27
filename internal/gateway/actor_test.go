package gateway

import (
	"context"
	"errors"
	"testing"
	"time"
)

func newActorWithJournal(t *testing.T, cfg ActorConfig) (*Actor, *Journal, chan Delivery) {
	t.Helper()
	dir := t.TempDir()
	j, err := OpenJournal(dir, 100)
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	deliveries := make(chan Delivery, 64)
	cfg.Marker = j
	cfg.Deliver = func(d Delivery) { deliveries <- d }
	if cfg.Runner == nil {
		cfg.Runner = instantRunner{}
	}
	a := NewActor(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	a.start(ctx)
	t.Cleanup(func() {
		cancel()
		a.stop()
		j.Close()
	})
	return a, j, deliveries
}

func TestActorFIFOOrder(t *testing.T) {
	runner := newSleepRunner(5 * time.Millisecond)
	a, j, deliveries := newActorWithJournal(t, ActorConfig{
		Key:         "k",
		Workspace:   "ws",
		SessionHint: "/sessions/hint.jsonl",
		Runner:      runner,
		Scheduler:   NewScheduler(4),
	})
	seedRecords(t, j, "a", "b", "c", "d", "e")
	for i := 0; i < 5; i++ {
		msg := sampleMessage(string(rune('a' + i)))
		mustSubmit(t, a, msg)
	}
	got := deliveriesToIDs(t, deliveries, 5)
	want := []string{"a", "b", "c", "d", "e"}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func deliveriesToIDs(t *testing.T, ch chan Delivery, n int) []string {
	t.Helper()
	out := make([]string, 0, n)
	deadline := time.After(3 * time.Second)
	for len(out) < n {
		select {
		case d := <-ch:
			out = append(out, d.ID)
		case <-deadline:
			t.Fatalf("timed out waiting for %d deliveries, got %d", n, len(out))
		}
	}
	return out
}

func TestActorQueueOverflowRejection(t *testing.T) {
	blocker := newBlockingRunner()
	a, j, _ := newActorWithJournal(t, ActorConfig{
		Key:         "k",
		Workspace:   "ws",
		MailboxSize: 4,
		Runner:      blocker,
		Scheduler:   NewScheduler(4),
	})
	seedRecords(t, j, "a", "b", "c", "d", "e")
	mustSubmit(t, a, sampleMessage("a"))
	<-blocker.started
	for i := 0; i < 4; i++ {
		msg := sampleMessage(string(rune('b' + i)))
		mustSubmit(t, a, msg)
	}
	rejected := 0
	for i := 0; i < 4; i++ {
		msg := sampleMessage(string(rune('f' + i)))
		_, err := a.Submit(msg)
		if errors.Is(err, ErrMailboxFull) {
			rejected++
		}
	}
	if rejected != 4 {
		t.Fatalf("rejected = %d, want 4", rejected)
	}
	close(blocker.release)
	waitFor(t, func() bool { return blocker.calls() == 5 }, "all queued turns run")
}

func TestActorBusyState(t *testing.T) {
	blocker := newBlockingRunner()
	a, j, _ := newActorWithJournal(t, ActorConfig{
		Key:       "k",
		Workspace: "ws",
		Runner:    blocker,
		Scheduler: NewScheduler(4),
	})
	seedRecords(t, j, "m1")
	if a.Busy() {
		t.Fatalf("actor should be idle")
	}
	mustSubmit(t, a, sampleMessage("m1"))
	<-blocker.started
	if !a.Busy() {
		t.Fatalf("actor should be busy")
	}
	close(blocker.release)
	waitFor(t, func() bool { return !a.Busy() }, "actor idle again")
}

func TestActorCancelCurrentMarksOutcomeUnknown(t *testing.T) {
	blocker := newBlockingRunner()
	a, j, deliveries := newActorWithJournal(t, ActorConfig{
		Key:       "k",
		Workspace: "ws",
		Runner:    blocker,
		Scheduler: NewScheduler(4),
	})
	seedRecords(t, j, "m1")
	msg := sampleMessage("m1")
	mustSubmit(t, a, msg)
	<-blocker.started
	a.CancelCurrent()
	d := (<-deliveries)
	if d.Err == nil {
		t.Fatalf("expected cancellation error delivery")
	}
	waitFor(t, func() bool {
		rec, ok := j.Get("m1")
		return ok && rec.Status == StatusOutcomeUnknown
	}, "outcome_unknown mark")
	close(blocker.release)
}

func TestActorRunnerFailureMarksFailed(t *testing.T) {
	runner := failingRunner{err: boomError{}}
	a, j, deliveries := newActorWithJournal(t, ActorConfig{
		Key:       "k",
		Workspace: "ws",
		Runner:    runner,
		Scheduler: NewScheduler(4),
	})
	seedRecords(t, j, "m1")
	mustSubmit(t, a, sampleMessage("m1"))
	d := (<-deliveries)
	if d.Err == nil {
		t.Fatalf("expected failure delivery")
	}
	if d.Result.Text != "" {
		t.Fatalf("no result expected on failure")
	}
	rec := mustRecord(t, j, "m1")
	if rec.Status != StatusFailed || rec.ErrorClass != "boomError" {
		t.Fatalf("record = %+v", rec)
	}
}

func TestActorCompletionMarksAndDelivers(t *testing.T) {
	runner := newRecordingRunner()
	a, j, deliveries := newActorWithJournal(t, ActorConfig{
		Key:       "k",
		Workspace: "ws",
		Runner:    runner,
		Scheduler: NewScheduler(4),
	})
	seedRecords(t, j, "m1")
	msg := sampleMessage("m1")
	mustSubmit(t, a, msg)
	d := (<-deliveries)
	if d.Err != nil {
		t.Fatalf("unexpected error: %v", d.Err)
	}
	if d.Result.Text != "reply:hello m1" {
		t.Fatalf("result text = %q", d.Result.Text)
	}
	if d.ExternalChatKey != msg.ExternalChatKey || d.UserIDHash != msg.UserIDHash {
		t.Fatalf("delivery context mismatch: %+v", d)
	}
	rec := mustRecord(t, j, "m1")
	if rec.Status != StatusCompleted {
		t.Fatalf("record status = %s", rec.Status)
	}
}

func TestActorWorkItemCarriesSessionHintTextAndEntriesDone(t *testing.T) {
	spy := newEntriesSpy()
	var captured WorkItem
	done := make(chan struct{})
	runner := runnerFunc(func(ctx context.Context, work WorkItem) (RunResult, error) {
		captured = work
		close(done)
		return RunResult{}, nil
	})
	a, j, _ := newActorWithJournal(t, ActorConfig{
		Key:         "k",
		Workspace:   "ws",
		SessionHint: "/hint/session.jsonl",
		Runner:      runner,
		Scheduler:   NewScheduler(4),
		EntriesDone: spy.fn(),
	})
	seedRecords(t, j, "m1")
	mustSubmit(t, a, sampleMessage("m1"))
	<-done
	if captured.SessionPath != "/hint/session.jsonl" {
		t.Fatalf("session path = %q", captured.SessionPath)
	}
	if captured.Text != "hello m1" {
		t.Fatalf("text = %q", captured.Text)
	}
	if captured.EntriesDone == nil {
		t.Fatalf("entries done callback missing")
	}
	captured.EntriesDone()
	waitFor(t, func() bool { return spy.calls == 1 }, "entries done call")
}

type runnerFunc func(ctx context.Context, work WorkItem) (RunResult, error)

func (f runnerFunc) Run(ctx context.Context, work WorkItem) (RunResult, error) {
	return f(ctx, work)
}

func TestActorStopCancelsQueuedMessages(t *testing.T) {
	blocker := newBlockingRunner()
	a, j, deliveries := newActorWithJournal(t, ActorConfig{
		Key:         "k",
		Workspace:   "ws",
		MailboxSize: 8,
		Runner:      blocker,
		Scheduler:   NewScheduler(4),
	})
	seedRecords(t, j, "inflight", "queued1", "queued2")
	mustSubmit(t, a, sampleMessage("inflight"))
	mustSubmit(t, a, sampleMessage("queued1"))
	mustSubmit(t, a, sampleMessage("queued2"))
	<-blocker.started
	a.stop()
	rec := mustRecord(t, j, "inflight")
	if rec.Status != StatusOutcomeUnknown {
		t.Fatalf("in-flight status = %s, want outcome_unknown", rec.Status)
	}
	for _, id := range []string{"queued1", "queued2"} {
		rec := mustRecord(t, j, id)
		if rec.Status != StatusCancelled {
			t.Fatalf("queued %s status = %s, want cancelled", id, rec.Status)
		}
	}
	got := deliveriesToIDs(t, deliveries, 3)
	if len(got) != 3 {
		t.Fatalf("deliveries = %v", got)
	}
	_, err := a.Submit(sampleMessage("after"))
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("submit after stop = %v, want ErrClosed", err)
	}
}

func TestActorReceiptQueuePosition(t *testing.T) {
	blocker := newBlockingRunner()
	a, j, _ := newActorWithJournal(t, ActorConfig{
		Key:         "k",
		Workspace:   "ws",
		MailboxSize: 4,
		Runner:      blocker,
		Scheduler:   NewScheduler(4),
	})
	seedRecords(t, j, "a", "b", "c")
	first := mustSubmit(t, a, sampleMessage("a"))
	if first.QueuePosition != 0 {
		t.Fatalf("first position = %d, want 0", first.QueuePosition)
	}
	<-blocker.started
	second := mustSubmit(t, a, sampleMessage("b"))
	third := mustSubmit(t, a, sampleMessage("c"))
	if second.QueuePosition != 0 || third.QueuePosition != 1 {
		t.Fatalf("positions = %d, %d", second.QueuePosition, third.QueuePosition)
	}
	close(blocker.release)
}

func TestActorWaitsOnSchedulerGate(t *testing.T) {
	sched := NewScheduler(1)
	blocker := newBlockingRunner()
	a, j, deliveries := newActorWithJournal(t, ActorConfig{
		Key:       "k",
		Workspace: "ws",
		Runner:    blocker,
		Scheduler: sched,
	})
	seedRecords(t, j, "m1")
	if err := sched.Acquire(context.Background(), "ws"); err != nil {
		t.Fatalf("test acquire: %v", err)
	}
	mustSubmit(t, a, sampleMessage("m1"))
	time.Sleep(20 * time.Millisecond)
	rec, ok := j.Get("m1")
	if !ok || rec.Status != StatusAccepted {
		t.Fatalf("record should still be accepted while waiting on gate: %+v", rec)
	}
	sched.Release("ws")
	waitFor(t, func() bool { return blocker.calls() == 1 }, "turn starts after gate release")
	close(blocker.release)
	<-deliveries
	waitFor(t, func() bool {
		rec, ok := j.Get("m1")
		return ok && rec.Status == StatusCompleted
	}, "turn completes")
}
