package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	eventRingSize    = 200
	subscriberBuffer = 16
	writeDeadline    = time.Second
	heartbeatEvery   = 15 * time.Second
)

type deliveryEvent struct {
	key       string `json:"-"`
	Seq       int64  `json:"seq"`
	ID        string `json:"id"`
	SessionID string `json:"sessionID,omitempty"`
	Text      string `json:"text,omitempty"`
	Result    string `json:"result,omitempty"`
	Error     string `json:"error,omitempty"`
}

type eventRing struct {
	mu   sync.Mutex
	seq  int64
	buf  []deliveryEvent
	subs map[chan deliveryEvent]struct{}
}

func newEventRing() *eventRing {
	return &eventRing{subs: make(map[chan deliveryEvent]struct{})}
}

func (r *eventRing) append(ev deliveryEvent) int64 {
	r.mu.Lock()
	r.seq++
	ev.Seq = r.seq
	if len(r.buf) == eventRingSize {
		copy(r.buf, r.buf[1:])
		r.buf = r.buf[:eventRingSize-1]
	}
	r.buf = append(r.buf, ev)
	subs := make([]chan deliveryEvent, 0, len(r.subs))
	for ch := range r.subs {
		subs = append(subs, ch)
	}
	r.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- ev:
		default:
		}
	}
	return ev.Seq
}

func (r *eventRing) subscribe() chan deliveryEvent {
	ch := make(chan deliveryEvent, subscriberBuffer)
	r.mu.Lock()
	r.subs[ch] = struct{}{}
	r.mu.Unlock()
	return ch
}

func (r *eventRing) unsubscribe(ch chan deliveryEvent) {
	r.mu.Lock()
	delete(r.subs, ch)
	r.mu.Unlock()
}

type ringSnapshot struct {
	oldest int64
	latest int64
	events []deliveryEvent
}

func (r *eventRing) snapshot(after int64) ringSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	snap := ringSnapshot{latest: r.seq}
	if len(r.buf) == 0 {
		return snap
	}
	snap.oldest = r.buf[0].Seq
	for _, ev := range r.buf {
		if ev.Seq > after {
			snap.events = append(snap.events, ev)
		}
	}
	return snap
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request, a authCtx) {
	since := int64(-1)
	if raw := r.URL.Query().Get("since"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid since")
			return
		}
		since = n
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}
	flusher.Flush()
	rw := http.NewResponseController(w)
	sub := s.events.subscribe()
	defer s.events.unsubscribe(sub)
	snap := s.events.snapshot(since)
	if !writeSSE(rw, w, "hello", map[string]int64{"oldest": snap.oldest, "latest": snap.latest}) {
		return
	}
	if since >= 0 && snap.oldest > 0 && since < snap.oldest-1 {
		if !writeSSE(rw, w, "gap", map[string]int64{"since": snap.latest}) {
			return
		}
	} else {
		for _, ev := range snap.events {
			if ev.key != a.userID {
				continue
			}
			if !writeSSE(rw, w, "delivery", ev) {
				return
			}
		}
	}
	ticker := time.NewTicker(heartbeatEvery)
	defer ticker.Stop()
	for {
		select {
		case ev := <-sub:
			if ev.key != a.userID {
				continue
			}
			if !writeSSE(rw, w, "delivery", ev) {
				return
			}
		case <-ticker.C:
			if !writeSSE(rw, w, "", nil) {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}

func writeSSE(rw *http.ResponseController, w http.ResponseWriter, event string, payload any) bool {
	_ = rw.SetWriteDeadline(time.Now().Add(writeDeadline))
	if event != "" {
		if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
			return false
		}
	}
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return false
		}
	} else {
		if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
			return false
		}
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	return true
}
