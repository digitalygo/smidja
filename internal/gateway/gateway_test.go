package gateway

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestGatewaySubmitDeliversToRegisteredSink(t *testing.T) {
	sink := newDeliverySpy(4)
	runner := newRecordingRunner()
	g := newTestGateway(t, Options{
		Runner:   runner,
		Resolver: fixedResolver("ws1", "/hint/s1.jsonl"),
	})
	g.RegisterSink("telegram", sink)
	cancel := startGateway(t, g)
	defer cancel()
	msg := sampleMessage("m1")
	receipt, err := g.Submit(context.Background(), msg)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if receipt.ID != "m1" {
		t.Fatalf("receipt id = %s", receipt.ID)
	}
	d := sink.next(t)
	if d.Err != nil {
		t.Fatalf("delivery error: %v", d.Err)
	}
	if d.Result.Text != "reply:hello m1" {
		t.Fatalf("result = %+v", d.Result)
	}
	if d.Transport != "telegram" || d.ExternalChatKey != msg.ExternalChatKey {
		t.Fatalf("delivery context = %+v", d)
	}
	rec := mustRecord(t, g.Journal(), "m1")
	if rec.Status != StatusCompleted {
		t.Fatalf("record status = %s", rec.Status)
	}
}

func TestGatewayDuplicateIDsRejected(t *testing.T) {
	g := newTestGateway(t, Options{
		Runner:   instantRunner{},
		Resolver: fixedResolver("ws1", ""),
	})
	cancel := startGateway(t, g)
	defer cancel()
	msg := sampleMessage("m1")
	if _, err := g.Submit(context.Background(), msg); err != nil {
		t.Fatalf("first submit: %v", err)
	}
	_, err := g.Submit(context.Background(), msg)
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate submit = %v, want ErrDuplicate", err)
	}
	if g.Journal().Len() != 1 {
		t.Fatalf("journal len = %d, want 1", g.Journal().Len())
	}
}

func TestGatewayDuplicateAcrossStatuses(t *testing.T) {
	g := newTestGateway(t, Options{
		Runner:   instantRunner{},
		Resolver: fixedResolver("ws1", ""),
	})
	cancel := startGateway(t, g)
	defer cancel()
	msg := sampleMessage("m1")
	if _, err := g.Submit(context.Background(), msg); err != nil {
		t.Fatalf("first submit: %v", err)
	}
	waitFor(t, func() bool {
		rec, ok := g.Journal().Get("m1")
		return ok && rec.Status == StatusCompleted
	}, "first turn completes")
	_, err := g.Submit(context.Background(), msg)
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate after completion = %v, want ErrDuplicate", err)
	}
}

func TestGatewayReplayPendingOnStart(t *testing.T) {
	dir := t.TempDir()
	j, err := OpenJournal(dir, 100)
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	rec := sampleRecord("m1")
	if err := j.Append(rec); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := j.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	runner := newRecordingRunner()
	sink := newDeliverySpy(4)
	g := newTestGateway(t, Options{
		Dir:      dir,
		Runner:   runner,
		Resolver: fixedResolver("ws1", ""),
	})
	g.RegisterSink("telegram", sink)
	cancel := startGateway(t, g)
	defer cancel()
	d := sink.next(t)
	if d.ID != "m1" {
		t.Fatalf("replayed delivery id = %s", d.ID)
	}
	rec = mustRecord(t, g.Journal(), "m1")
	if rec.Status != StatusCompleted {
		t.Fatalf("replayed record status = %s", rec.Status)
	}
	texts := runner.texts()
	if len(texts) != 1 || texts[0] != "hello m1" {
		t.Fatalf("runner texts = %v", texts)
	}
}

func TestGatewayReplaySkipsCompleted(t *testing.T) {
	dir := t.TempDir()
	j, err := OpenJournal(dir, 100)
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	done := sampleRecord("done")
	done.Status = StatusCompleted
	if err := j.Append(done); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := j.Append(sampleRecord("pending")); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := j.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	runner := newRecordingRunner()
	g := newTestGateway(t, Options{Dir: dir, Runner: runner, Resolver: fixedResolver("ws1", "")})
	cancel := startGateway(t, g)
	defer cancel()
	waitFor(t, func() bool { return runner.count() == 1 }, "only pending replay runs")
	if texts := runner.texts(); len(texts) != 1 || texts[0] != "hello pending" {
		t.Fatalf("runner texts = %v", texts)
	}
}

func TestGatewayMailboxOverflowMarksCancelled(t *testing.T) {
	blocker := newBlockingRunner()
	g := newTestGateway(t, Options{
		MailboxSize: 2,
		Burst:       100,
		Runner:      blocker,
		Resolver:    fixedResolver("ws1", ""),
	})
	cancel := startGateway(t, g)
	defer cancel()
	ctx := context.Background()
	if _, err := g.Submit(ctx, sameChatMessage("chat", "m1")); err != nil {
		t.Fatalf("submit m1: %v", err)
	}
	<-blocker.started
	for _, id := range []string{"m2", "m3"} {
		if _, err := g.Submit(ctx, sameChatMessage("chat", id)); err != nil {
			t.Fatalf("submit %s: %v", id, err)
		}
	}
	_, err := g.Submit(ctx, sameChatMessage("chat", "overflow"))
	if !errors.Is(err, ErrMailboxFull) {
		t.Fatalf("overflow submit = %v, want ErrMailboxFull", err)
	}
	rec := mustRecord(t, g.Journal(), "overflow")
	if rec.Status != StatusCancelled {
		t.Fatalf("overflow record status = %s, want cancelled", rec.Status)
	}
	close(blocker.release)
}

func TestGatewayValidationRejects(t *testing.T) {
	g := newTestGateway(t, Options{Runner: instantRunner{}, Resolver: fixedResolver("ws1", "")})
	cancel := startGateway(t, g)
	defer cancel()
	valid := sampleMessage("m1")
	cases := []struct {
		name string
		msg  InboundMessage
	}{
		{"empty id", InboundMessage{Transport: "t", ExternalChatKey: "c", UserIDHash: "u", Text: "x"}},
		{"empty transport", InboundMessage{ID: "i", ExternalChatKey: "c", UserIDHash: "u", Text: "x"}},
		{"empty chat key", InboundMessage{ID: "i", Transport: "t", UserIDHash: "u", Text: "x"}},
		{"empty user hash", InboundMessage{ID: "i", Transport: "t", ExternalChatKey: "c", Text: "x"}},
	}
	for _, tc := range cases {
		_, err := g.Submit(context.Background(), tc.msg)
		if !errors.Is(err, ErrInvalidMessage) {
			t.Fatalf("%s: got %v, want ErrInvalidMessage", tc.name, err)
		}
	}
	_, err := g.Submit(context.Background(), valid)
	if err != nil {
		t.Fatalf("valid submit failed: %v", err)
	}
}

func TestGatewayRateLimitRejects(t *testing.T) {
	g := newTestGateway(t, Options{
		Burst:           3,
		RefillPerMinute: 0.0001,
		Runner:          instantRunner{},
		Resolver:        fixedResolver("ws1", ""),
	})
	cancel := startGateway(t, g)
	defer cancel()
	for i := 0; i < 3; i++ {
		m := sampleMessage(string(rune('a' + i)))
		m.UserIDHash = HashUserIdentity("single-user")
		if _, err := g.Submit(context.Background(), m); err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
	}
	m := sampleMessage("d")
	m.UserIDHash = HashUserIdentity("single-user")
	_, err := g.Submit(context.Background(), m)
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("rate limited submit = %v, want ErrRateLimited", err)
	}
}

func TestGatewaySizeCapRejects(t *testing.T) {
	g := newTestGateway(t, Options{
		MaxInboundBytes: 8,
		Runner:          instantRunner{},
		Resolver:        fixedResolver("ws1", ""),
	})
	cancel := startGateway(t, g)
	defer cancel()
	msg := sampleMessage("big")
	msg.Text = "this text is way too long"
	_, err := g.Submit(context.Background(), msg)
	if !errors.Is(err, ErrInboundTooLarge) {
		t.Fatalf("oversize submit = %v, want ErrInboundTooLarge", err)
	}
}

func TestGatewaySinksRoutedByTransport(t *testing.T) {
	telegramSink := newDeliverySpy(4)
	discordSink := newDeliverySpy(4)
	g := newTestGateway(t, Options{
		Runner:   instantRunner{},
		Resolver: fixedResolver("ws1", ""),
	})
	g.RegisterSink("telegram", telegramSink)
	g.RegisterSink("discord", discordSink)
	cancel := startGateway(t, g)
	defer cancel()
	tg := sampleMessage("tg1")
	tg.Transport = "telegram"
	dc := sampleMessage("dc1")
	dc.Transport = "discord"
	if _, err := g.Submit(context.Background(), tg); err != nil {
		t.Fatalf("telegram submit: %v", err)
	}
	if _, err := g.Submit(context.Background(), dc); err != nil {
		t.Fatalf("discord submit: %v", err)
	}
	if d := telegramSink.next(t); d.ID != "tg1" {
		t.Fatalf("telegram sink got %s", d.ID)
	}
	if d := discordSink.next(t); d.ID != "dc1" {
		t.Fatalf("discord sink got %s", d.ID)
	}
}

func TestGatewaySubmitBeforeStart(t *testing.T) {
	g := newTestGateway(t, Options{Runner: instantRunner{}, Resolver: fixedResolver("ws1", "")})
	_, err := g.Submit(context.Background(), sampleMessage("m1"))
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("submit before start = %v, want ErrClosed", err)
	}
	if err := g.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := g.Start(context.Background()); !errors.Is(err, ErrAlreadyStarted) {
		t.Fatalf("second start = %v, want ErrAlreadyStarted", err)
	}
	if err := g.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if err := g.Shutdown(context.Background()); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("second shutdown = %v, want ErrNotStarted", err)
	}
}

func TestGatewayShutdownNoGoroutineLeak(t *testing.T) {
	time.Sleep(50 * time.Millisecond)
	baseline := runtime.NumGoroutine()
	blocker := newBlockingRunner()
	sink := newDeliverySpy(16)
	g := newTestGateway(t, Options{
		MailboxSize: 4,
		Burst:       100,
		Runner:      blocker,
		Resolver:    fixedResolver("ws1", ""),
	})
	g.RegisterSink("telegram", sink)
	cancel := startGateway(t, g)
	for i := 0; i < 3; i++ {
		if _, err := g.Submit(context.Background(), sampleMessage(string(rune('a'+i)))); err != nil {
			t.Fatalf("submit: %v", err)
		}
	}
	<-blocker.started
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := g.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	cancel()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= baseline+1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("goroutines leaked: baseline=%d now=%d", baseline, runtime.NumGoroutine())
}

func TestGatewayShutdownMarksCancelledAndOutcomeUnknown(t *testing.T) {
	blocker := newBlockingRunner()
	g := newTestGateway(t, Options{
		MailboxSize: 8,
		Burst:       100,
		Runner:      blocker,
		Resolver:    fixedResolver("ws1", ""),
	})
	cancel := startGateway(t, g)
	if _, err := g.Submit(context.Background(), sameChatMessage("chat", "inflight")); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := g.Submit(context.Background(), sameChatMessage("chat", "queued")); err != nil {
		t.Fatalf("submit: %v", err)
	}
	<-blocker.started
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := g.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	cancel()
	inflight := mustRecord(t, g.Journal(), "inflight")
	if inflight.Status != StatusOutcomeUnknown {
		t.Fatalf("in-flight status = %s, want outcome_unknown", inflight.Status)
	}
	queued := mustRecord(t, g.Journal(), "queued")
	if queued.Status != StatusCancelled {
		t.Fatalf("queued status = %s, want cancelled", queued.Status)
	}
}

func TestGatewayDefaultDirUnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	g, err := New(Options{Runner: instantRunner{}, Resolver: fixedResolver("ws1", "")})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer g.Journal().Close()
	if _, err := os.Stat(filepath.Join(home, ".smidja", "gateway", journalFileName)); err != nil {
		t.Fatalf("default journal dir not created: %v", err)
	}
}

func TestGatewayPerWorkspaceSerializationEndToEnd(t *testing.T) {
	runner := newSleepRunner(30 * time.Millisecond)
	g := newTestGateway(t, Options{
		Burst:          100,
		Runner:         runner,
		Resolver:       func(key string) (string, string) { return "ws:" + key, "" },
		MaxActiveTurns: 4,
	})
	sink := newDeliverySpy(16)
	g.RegisterSink("telegram", sink)
	cancel := startGateway(t, g)
	defer cancel()
	msgA := sampleMessage("a")
	msgA.ExternalChatKey = "chatA"
	msgB := sampleMessage("b")
	msgB.ExternalChatKey = "chatB"
	begin := time.Now()
	if _, err := g.Submit(context.Background(), msgA); err != nil {
		t.Fatalf("submit a: %v", err)
	}
	if _, err := g.Submit(context.Background(), msgB); err != nil {
		t.Fatalf("submit b: %v", err)
	}
	sink.wait(t, 2)
	elapsed := time.Since(begin)
	if elapsed >= 55*time.Millisecond {
		t.Fatalf("different chat keys took %v, want parallel", elapsed)
	}
}

func TestGatewaySameWorkspaceSerializedEndToEnd(t *testing.T) {
	runner := newSleepRunner(30 * time.Millisecond)
	g := newTestGateway(t, Options{
		Burst:          100,
		Runner:         runner,
		Resolver:       func(key string) (string, string) { return "shared-ws", "" },
		MaxActiveTurns: 4,
	})
	sink := newDeliverySpy(16)
	g.RegisterSink("telegram", sink)
	cancel := startGateway(t, g)
	defer cancel()
	msgA := sampleMessage("a")
	msgA.ExternalChatKey = "chatA"
	msgB := sampleMessage("b")
	msgB.ExternalChatKey = "chatB"
	begin := time.Now()
	if _, err := g.Submit(context.Background(), msgA); err != nil {
		t.Fatalf("submit a: %v", err)
	}
	if _, err := g.Submit(context.Background(), msgB); err != nil {
		t.Fatalf("submit b: %v", err)
	}
	sink.wait(t, 2)
	elapsed := time.Since(begin)
	if elapsed < 55*time.Millisecond {
		t.Fatalf("shared workspace took %v, want serialized", elapsed)
	}
}

func TestGatewayMaxActiveAdmission(t *testing.T) {
	blocker := newBlockingRunner()
	g := newTestGateway(t, Options{
		Burst:              100,
		MaxActiveAdmission: 1,
		Runner:             blocker,
		Resolver:           fixedResolver("ws1", ""),
	})
	cancel := startGateway(t, g)
	defer cancel()
	if _, err := g.Submit(context.Background(), sameChatMessage("chat", "m1")); err != nil {
		t.Fatalf("first submit: %v", err)
	}
	<-blocker.started
	_, err := g.Submit(context.Background(), sameChatMessage("chat", "m2"))
	if !errors.Is(err, ErrTooManyActive) {
		t.Fatalf("admission submit = %v, want ErrTooManyActive", err)
	}
	close(blocker.release)
	waitFor(t, func() bool { return g.RateLimiter().ActiveTurns() == 0 }, "active turns drained")
	if _, err := g.Submit(context.Background(), sameChatMessage("chat", "m3")); err != nil {
		t.Fatalf("submit after drain: %v", err)
	}
	waitFor(t, func() bool { return g.RateLimiter().ActiveTurns() == 0 }, "second turn drained")
}

func TestGatewaySchedulerConcurrencyCount(t *testing.T) {
	g := newTestGateway(t, Options{
		Runner:         instantRunner{},
		Resolver:       fixedResolver("ws1", ""),
		MaxActiveTurns: 2,
	})
	if g.Scheduler().MaxActive() != 2 {
		t.Fatalf("max active turns = %d, want 2", g.Scheduler().MaxActive())
	}
}

func TestGatewayHashUserIdentity(t *testing.T) {
	h1 := HashUserIdentity("luca")
	h2 := HashUserIdentity("luca")
	h3 := HashUserIdentity("someone-else")
	if h1 != h2 {
		t.Fatalf("hash not deterministic")
	}
	if h1 == h3 {
		t.Fatalf("different identities hashed equal")
	}
	if len(h1) != 64 {
		t.Fatalf("hash length = %d, want 64", len(h1))
	}
}

func TestGatewayUnregisterSinkDropsDeliveries(t *testing.T) {
	sink := newDeliverySpy(4)
	g := newTestGateway(t, Options{Runner: instantRunner{}, Resolver: fixedResolver("ws1", "")})
	g.RegisterSink("telegram", sink)
	cancel := startGateway(t, g)
	defer cancel()
	if _, err := g.Submit(context.Background(), sampleMessage("m1")); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if d := sink.next(t); d.ID != "m1" {
		t.Fatalf("sink got %s", d.ID)
	}
	g.RegisterSink("telegram", nil)
	if _, err := g.Submit(context.Background(), sampleMessage("m2")); err != nil {
		t.Fatalf("submit m2: %v", err)
	}
	waitFor(t, func() bool {
		rec, ok := g.Journal().Get("m2")
		return ok && rec.Status == StatusCompleted
	}, "m2 completes")
	select {
	case d := <-sink.ch:
		t.Fatalf("unexpected delivery after unregister: %+v", d)
	case <-time.After(30 * time.Millisecond):
	}
}

func TestJournalRecordsSnapshot(t *testing.T) {
	dir := t.TempDir()
	j, err := OpenJournal(dir, 100)
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	defer j.Close()
	if err := j.Append(sampleRecord("a")); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := j.Append(sampleRecord("b")); err != nil {
		t.Fatalf("append: %v", err)
	}
	recs := j.Records()
	if len(recs) != 2 {
		t.Fatalf("records = %d, want 2", len(recs))
	}
	recs[0].ID = "mutated"
	if got, _ := j.Get("a"); got.ID != "a" {
		t.Fatalf("snapshot must be a copy")
	}
}

func TestJournalAppendUniqueAfterClose(t *testing.T) {
	dir := t.TempDir()
	j, err := OpenJournal(dir, 100)
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	if err := j.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := j.AppendUnique(sampleRecord("a")); !errors.Is(err, ErrClosed) {
		t.Fatalf("append unique after close = %v", err)
	}
}

func TestJournalOpenEmptyDir(t *testing.T) {
	if _, err := OpenJournal("", 100); err == nil {
		t.Fatalf("empty dir should fail")
	}
}
