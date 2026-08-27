package gateway

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type instantRunner struct{}

func (instantRunner) Run(ctx context.Context, work WorkItem) (RunResult, error) {
	if work.EntriesDone != nil {
		work.EntriesDone()
	}
	return RunResult{Text: "ok"}, nil
}

type recordingRunner struct {
	mu      sync.Mutex
	items   []WorkItem
	results map[string]RunResult
}

func newRecordingRunner() *recordingRunner {
	return &recordingRunner{results: make(map[string]RunResult)}
}

func (r *recordingRunner) Run(ctx context.Context, work WorkItem) (RunResult, error) {
	if work.EntriesDone != nil {
		work.EntriesDone()
	}
	r.mu.Lock()
	r.items = append(r.items, work)
	r.results[work.Text] = RunResult{Text: "reply:" + work.Text}
	r.mu.Unlock()
	return r.results[work.Text], nil
}

func (r *recordingRunner) texts() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.items))
	for i, it := range r.items {
		out[i] = it.Text
	}
	return out
}

func (r *recordingRunner) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.items)
}

type sleepRunner struct {
	delay time.Duration
	mu    sync.Mutex
	texts []string
}

func newSleepRunner(delay time.Duration) *sleepRunner {
	return &sleepRunner{delay: delay}
}

func (r *sleepRunner) Run(ctx context.Context, work WorkItem) (RunResult, error) {
	r.mu.Lock()
	r.texts = append(r.texts, work.Text)
	r.mu.Unlock()
	select {
	case <-time.After(r.delay):
		return RunResult{Text: "ok:" + work.Text}, nil
	case <-ctx.Done():
		return RunResult{}, ctx.Err()
	}
}

func (r *sleepRunner) order() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.texts))
	copy(out, r.texts)
	return out
}

type blockingRunner struct {
	mu      sync.Mutex
	started chan struct{}
	release chan struct{}
	count   int
}

func newBlockingRunner() *blockingRunner {
	return &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
}

func (r *blockingRunner) Run(ctx context.Context, work WorkItem) (RunResult, error) {
	r.mu.Lock()
	r.count++
	count := r.count
	r.mu.Unlock()
	if count == 1 {
		close(r.started)
	}
	select {
	case <-r.release:
		return RunResult{Text: "released"}, nil
	case <-ctx.Done():
		return RunResult{}, ctx.Err()
	}
}

func (r *blockingRunner) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count
}

type failingRunner struct {
	err error
}

func (r failingRunner) Run(ctx context.Context, work WorkItem) (RunResult, error) {
	if work.EntriesDone != nil {
		work.EntriesDone()
	}
	return RunResult{}, r.err
}

type boomError struct{}

func (boomError) Error() string { return "boom" }

type entriesSpy struct {
	calls int
	done  chan struct{}
}

func newEntriesSpy() *entriesSpy {
	return &entriesSpy{done: make(chan struct{}, 16)}
}

func (s *entriesSpy) fn() func() {
	return func() {
		s.calls++
		s.done <- struct{}{}
	}
}

type deliverySpy struct {
	ch chan Delivery
}

func newDeliverySpy(buffer int) *deliverySpy {
	return &deliverySpy{ch: make(chan Delivery, buffer)}
}

func (s *deliverySpy) Deliver(ctx context.Context, d Delivery) error {
	s.ch <- d
	return nil
}

func (s *deliverySpy) next(t *testing.T) Delivery {
	t.Helper()
	select {
	case d := <-s.ch:
		return d
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for delivery")
		return Delivery{}
	}
}

func (s *deliverySpy) wait(t *testing.T, n int) []Delivery {
	t.Helper()
	out := make([]Delivery, 0, n)
	deadline := time.After(3 * time.Second)
	for len(out) < n {
		select {
		case d := <-s.ch:
			out = append(out, d)
		case <-deadline:
			t.Fatalf("timed out waiting for %d deliveries, got %d", n, len(out))
		}
	}
	return out
}

func mustReceipt(t *testing.T, r Receipt, err error) Receipt {
	t.Helper()
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}
	return r
}

func mustSubmit(t *testing.T, a *Actor, msg InboundMessage) Receipt {
	t.Helper()
	r, err := a.Submit(msg)
	if err != nil {
		t.Fatalf("actor submit failed: %v", err)
	}
	return r
}

func mustRecord(t *testing.T, j *Journal, id string) Record {
	t.Helper()
	rec, ok := j.Get(id)
	if !ok {
		t.Fatalf("record %q not found", id)
	}
	return rec
}

func sampleRecord(id string) Record {
	return Record{
		ID:              id,
		Ts:              time.Now().UTC(),
		Transport:       "telegram",
		ExternalChatKey: "chat-" + id,
		UserIDHash:      "user-" + id,
		Text:            "hello " + id,
		Status:          StatusAccepted,
	}
}

func sampleMessage(id string) InboundMessage {
	return InboundMessage{
		ID:              id,
		Transport:       "telegram",
		ExternalChatKey: "chat-" + id,
		UserIDHash:      HashUserIdentity("user-" + id),
		Text:            "hello " + id,
	}
}

func seedRecords(t *testing.T, j *Journal, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if err := j.Append(sampleRecord(id)); err != nil {
			t.Fatalf("seed record %s: %v", id, err)
		}
	}
}

func sameChatMessage(chatKey, id string) InboundMessage {
	m := sampleMessage(id)
	m.ExternalChatKey = chatKey
	return m
}

func fixedResolver(workspace, hint string) Resolver {
	return func(key string) (string, string) {
		return workspace, hint
	}
}

func newTestGateway(t *testing.T, opts Options) *Gateway {
	t.Helper()
	if opts.Dir == "" {
		opts.Dir = t.TempDir()
	}
	g, err := New(opts)
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}
	return g
}

func startGateway(t *testing.T, g *Gateway) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	if err := g.Start(ctx); err != nil {
		cancel()
		t.Fatalf("start gateway: %v", err)
	}
	return cancel
}

func assertErrorIs(t *testing.T, err, want error) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %v, got nil", want)
	}
	if !errors.Is(err, want) {
		t.Fatalf("expected error %v, got %v", want, err)
	}
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
