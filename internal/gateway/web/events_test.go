package web

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/gateway"
)

func TestEventRingAppendAndReplay(t *testing.T) {
	ring := newEventRing()
	for i := 1; i <= 3; i++ {
		ring.append(deliveryEvent{key: "u", ID: "m" + string(rune('a'+i-1))})
	}
	snap := ring.snapshot(0)
	if snap.latest != 3 {
		t.Errorf("latest = %d, want 3", snap.latest)
	}
	if len(snap.events) != 3 {
		t.Fatalf("events = %d, want 3", len(snap.events))
	}
	if snap.events[0].Seq != 1 || snap.events[2].Seq != 3 {
		t.Errorf("seq order = %d..%d", snap.events[0].Seq, snap.events[2].Seq)
	}
	snap = ring.snapshot(1)
	if len(snap.events) != 2 {
		t.Errorf("events after 1 = %d, want 2", len(snap.events))
	}
	if snap.events[0].Seq != 2 {
		t.Errorf("first after 1 = %d, want 2", snap.events[0].Seq)
	}
	snap = ring.snapshot(3)
	if len(snap.events) != 0 {
		t.Errorf("events after 3 = %d, want 0", len(snap.events))
	}
}

func TestEventRingCapacity(t *testing.T) {
	ring := newEventRing()
	for i := 0; i < 250; i++ {
		ring.append(deliveryEvent{key: "u", ID: "m"})
	}
	snap := ring.snapshot(0)
	if snap.latest != 250 {
		t.Errorf("latest = %d, want 250", snap.latest)
	}
	if len(snap.events) != eventRingSize {
		t.Errorf("buffered = %d, want %d", len(snap.events), eventRingSize)
	}
	if snap.oldest != 51 {
		t.Errorf("oldest = %d, want 51", snap.oldest)
	}
	if snap.events[0].Seq != 51 {
		t.Errorf("first seq = %d, want 51", snap.events[0].Seq)
	}
}

func TestEventRingSubscriberReceives(t *testing.T) {
	ring := newEventRing()
	ch := ring.subscribe()
	defer ring.unsubscribe(ch)
	ring.append(deliveryEvent{key: "u", ID: "m1"})
	select {
	case ev := <-ch:
		if ev.ID != "m1" || ev.Seq != 1 {
			t.Errorf("event = %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive event")
	}
}

func TestEventRingSlowSubscriberDrops(t *testing.T) {
	ring := newEventRing()
	ch := ring.subscribe()
	defer ring.unsubscribe(ch)
	for i := 0; i < 250; i++ {
		ring.append(deliveryEvent{key: "u", ID: "m"})
	}
	select {
	case <-ch:
	default:
		t.Fatal("expected at least one buffered event")
	}
	ring.mu.Lock()
	buffered := len(ch)
	ring.mu.Unlock()
	if buffered > subscriberBuffer {
		t.Errorf("slow subscriber buffered %d, want at most %d", buffered, subscriberBuffer)
	}
	snap := ring.snapshot(0)
	if len(snap.events) != eventRingSize {
		t.Errorf("ring lost events for fast subscribers: %d", len(snap.events))
	}
}

func TestEventRingUnsubscribeStopsDelivery(t *testing.T) {
	ring := newEventRing()
	ch := ring.subscribe()
	ring.unsubscribe(ch)
	ring.append(deliveryEvent{key: "u", ID: "m1"})
	select {
	case ev := <-ch:
		t.Errorf("received after unsubscribe: %+v", ev)
	default:
	}
	if ring.snapshot(0).latest != 1 {
		t.Error("ring lost the event")
	}
}

func TestEventRingEmptySnapshot(t *testing.T) {
	ring := newEventRing()
	snap := ring.snapshot(0)
	if snap.latest != 0 || snap.oldest != 0 || len(snap.events) != 0 {
		t.Errorf("empty snapshot = %+v", snap)
	}
}

func TestEventRingSeqPersistsAcrossSubscribers(t *testing.T) {
	ring := newEventRing()
	ring.append(deliveryEvent{key: "u"})
	ch := ring.subscribe()
	defer ring.unsubscribe(ch)
	seq := ring.append(deliveryEvent{key: "u"})
	if seq != 2 {
		t.Errorf("seq = %d, want 2", seq)
	}
	select {
	case ev := <-ch:
		if ev.Seq != 2 {
			t.Errorf("subscriber seq = %d, want 2", ev.Seq)
		}
	case <-time.After(time.Second):
		t.Fatal("no event")
	}
}

func TestSSEBasicDelivery(t *testing.T) {
	_, fw, ts := startTestServer(t, nil)
	client := jarClient(t, ts)
	loginAs(t, client, ts, "secret-token")
	userID := cookieValue(t, client, ts)

	resp, err := client.Get(ts.URL + "/api/events?since=0")
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("content-type = %q", ct)
	}
	lines := sseLines(t, resp.Body)
	waitSSELine(t, lines, "event: hello")

	if err := fw.sink.Deliver(context.Background(), gateway.Delivery{
		ID:              "m1",
		Transport:       transportWeb,
		ExternalChatKey: userID,
		Text:            "hi",
		Result:          gateway.RunResult{Text: "reply", SessionID: "sess-1"},
	}); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	waitSSELine(t, lines, "event: delivery")
	dataLine := waitSSELine(t, lines, "data: ")
	if !strings.Contains(dataLine, `"sessionID":"sess-1"`) || !strings.Contains(dataLine, `"result":"reply"`) {
		t.Errorf("data = %q", dataLine)
	}
}

func TestSSEReplayAfterSince(t *testing.T) {
	_, fw, ts := startTestServer(t, nil)
	client := jarClient(t, ts)
	loginAs(t, client, ts, "secret-token")
	userID := cookieValue(t, client, ts)

	for i := 1; i <= 2; i++ {
		if err := fw.sink.Deliver(context.Background(), gateway.Delivery{
			ID:              "m" + strconv.Itoa(i),
			Transport:       transportWeb,
			ExternalChatKey: userID,
			Text:            "hi",
			Result:          gateway.RunResult{Text: "r"},
		}); err != nil {
			t.Fatalf("Deliver: %v", err)
		}
	}
	resp, err := client.Get(ts.URL + "/api/events?since=0")
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	defer resp.Body.Close()
	lines := sseLines(t, resp.Body)
	waitSSELine(t, lines, "event: hello")
	waitSSELine(t, lines, "event: delivery")
	waitSSELine(t, lines, "event: delivery")
	dataLine := waitSSELine(t, lines, "data: ")
	if !strings.Contains(dataLine, `"id":"m2"`) {
		t.Errorf("second delivery = %q", dataLine)
	}
}

func TestSSEFiltersOtherUsers(t *testing.T) {
	_, fw, ts := startTestServer(t, nil)
	client := jarClient(t, ts)
	loginAs(t, client, ts, "secret-token")
	userID := cookieValue(t, client, ts)

	resp, err := client.Get(ts.URL + "/api/events?since=0")
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	defer resp.Body.Close()
	lines := sseLines(t, resp.Body)
	waitSSELine(t, lines, "event: hello")

	if err := fw.sink.Deliver(context.Background(), gateway.Delivery{
		ID:              "other",
		Transport:       transportWeb,
		ExternalChatKey: "some-other-user",
		Text:            "secret",
		Result:          gateway.RunResult{Text: "r"},
	}); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if err := fw.sink.Deliver(context.Background(), gateway.Delivery{
		ID:              "mine",
		Transport:       transportWeb,
		ExternalChatKey: userID,
		Text:            "mine",
		Result:          gateway.RunResult{Text: "r"},
	}); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	waitSSELine(t, lines, "event: delivery")
	dataLine := waitSSELine(t, lines, "data: ")
	if strings.Contains(dataLine, "secret") || !strings.Contains(dataLine, `"id":"mine"`) {
		t.Errorf("other user's delivery leaked: %q", dataLine)
	}
}

func TestSSEGapOnEvictedEvents(t *testing.T) {
	_, fw, ts := startTestServer(t, nil)
	client := jarClient(t, ts)
	loginAs(t, client, ts, "secret-token")
	userID := cookieValue(t, client, ts)

	for i := 0; i < eventRingSize+10; i++ {
		if err := fw.sink.Deliver(context.Background(), gateway.Delivery{
			ID:              "m",
			Transport:       transportWeb,
			ExternalChatKey: userID,
			Text:            "hi",
		}); err != nil {
			t.Fatalf("Deliver: %v", err)
		}
	}
	resp, err := client.Get(ts.URL + "/api/events?since=0")
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	defer resp.Body.Close()
	lines := sseLines(t, resp.Body)
	waitSSELine(t, lines, "event: hello")
	gap := waitSSELine(t, lines, "event: gap")
	if !strings.Contains(gap, "gap") {
		t.Fatalf("expected gap event, got %q", gap)
	}
}

func TestSSEEventsInvalidSince(t *testing.T) {
	_, _, ts := startTestServer(t, nil)
	client := jarClient(t, ts)
	loginAs(t, client, ts, "secret-token")
	resp := doJSON(t, client, "GET", ts.URL+"/api/events?since=abc", "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestSSEEventsNegativeSinceRejected(t *testing.T) {
	_, _, ts := startTestServer(t, nil)
	client := jarClient(t, ts)
	loginAs(t, client, ts, "secret-token")
	resp := doJSON(t, client, "GET", ts.URL+"/api/events?since=-1", "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestSSEHandlerContextCancel(t *testing.T) {
	s, _ := newTestServer(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "/api/events?since=0", nil).WithContext(ctx)
	rr := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		s.handleEvents(rr, req, authCtx{userID: "u"})
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not return on context cancel")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("code = %d, want 200", rr.Code)
	}
}

func TestSSEHandlerWriteFailure(t *testing.T) {
	s, _ := newTestServer(t, nil)
	req := httptest.NewRequest("GET", "/api/events?since=0", nil)
	w := &failWriter{h: make(http.Header)}
	s.handleEvents(w, req, authCtx{userID: "u"})
	if w.code != http.StatusOK {
		t.Errorf("code = %d, want 200", w.code)
	}
}

func TestWriteSSEDeadlineAndPayload(t *testing.T) {
	rr := httptest.NewRecorder()
	rw := http.NewResponseController(rr)
	if !writeSSE(rw, rr, "delivery", deliveryEvent{Seq: 7, ID: "m"}) {
		t.Fatal("writeSSE failed")
	}
	body := rr.Body.String()
	if !strings.Contains(body, "event: delivery") || !strings.Contains(body, `"seq":7`) {
		t.Errorf("body = %q", body)
	}
	if !strings.Contains(body, "\n\n") {
		t.Error("missing event terminator")
	}
}

func TestWriteSSEHeartbeat(t *testing.T) {
	rr := httptest.NewRecorder()
	rw := http.NewResponseController(rr)
	if !writeSSE(rw, rr, "", nil) {
		t.Fatal("writeSSE failed")
	}
	if !strings.Contains(rr.Body.String(), ": ping") {
		t.Errorf("body = %q", rr.Body.String())
	}
}

type failWriter struct {
	h    http.Header
	code int
}

func (w *failWriter) Header() http.Header  { return w.h }
func (w *failWriter) WriteHeader(code int) { w.code = code }
func (w *failWriter) Write(p []byte) (int, error) {
	return 0, errors.New("write failed")
}
func (w *failWriter) Flush()                           {}
func (w *failWriter) SetWriteDeadline(time.Time) error { return nil }

func waitSSELine(t *testing.T, lines <-chan string, want string) string {
	t.Helper()
	for {
		line := nextSSELine(t, lines)
		if strings.Contains(line, want) {
			return line
		}
	}
}

func nextSSELine(t *testing.T, lines <-chan string) string {
	t.Helper()
	select {
	case line, ok := <-lines:
		if !ok {
			t.Fatal("sse stream closed")
		}
		return line
	case <-time.After(5 * time.Second):
		t.Fatal("timed out reading sse")
		return ""
	}
}

func sseLines(t *testing.T, body io.Reader) <-chan string {
	t.Helper()
	ch := make(chan string, 64)
	go func() {
		br := bufio.NewReader(body)
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				close(ch)
				return
			}
			ch <- line
		}
	}()
	return ch
}
