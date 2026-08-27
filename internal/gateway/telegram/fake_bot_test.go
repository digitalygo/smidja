package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/gateway"
)

type fakeCall struct {
	method string
	params url.Values
}

func (c fakeCall) param(key string) string {
	return c.params.Get(key)
}

type fakeFailure struct {
	code        int
	description string
	retryAfter  int
	remaining   int
}

type fakeBot struct {
	t             *testing.T
	token         string
	mu            sync.Mutex
	server        *httptest.Server
	queue         []Update
	wake          chan struct{}
	calls         []fakeCall
	failures      map[string][]fakeFailure
	richSupported bool
	networkErrors int
	nextMessageID int64
}

func newFakeBot(t *testing.T, token string) *fakeBot {
	t.Helper()
	f := &fakeBot{
		t:             t,
		token:         token,
		wake:          make(chan struct{}, 1),
		failures:      make(map[string][]fakeFailure),
		richSupported: true,
	}
	f.server = httptest.NewServer(f.handler())
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeBot) url() string {
	return f.server.URL
}

func (f *fakeBot) enqueue(updates ...Update) {
	f.mu.Lock()
	f.queue = append(f.queue, updates...)
	f.mu.Unlock()
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

func (f *fakeBot) fail(method string, code, retryAfter int, description string, times int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures[method] = append(f.failures[method], fakeFailure{code: code, description: description, retryAfter: retryAfter, remaining: times})
}

func (f *fakeBot) callsFor(method string) []fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fakeCall
	for _, c := range f.calls {
		if c.method == method {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeBot) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/bot")
		token, method, ok := strings.Cut(rest, "/")
		if !ok || token != f.token {
			f.t.Errorf("unexpected request path %s", r.URL.Path)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if err := r.ParseForm(); err != nil {
			f.t.Errorf("parse form: %v", err)
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.calls = append(f.calls, fakeCall{method: method, params: r.PostForm})
		if method == "getUpdates" && f.networkErrors > 0 {
			f.networkErrors--
			f.mu.Unlock()
			panic(http.ErrAbortHandler)
		}
		if fl := f.failures[method]; len(fl) > 0 {
			fail := fl[0]
			fail.remaining--
			if fail.remaining <= 0 {
				f.failures[method] = fl[1:]
			} else {
				f.failures[method][0] = fail
			}
			f.mu.Unlock()
			f.writeError(w, fail)
			return
		}
		switch method {
		case "getMe":
			f.mu.Unlock()
			f.writeResult(w, User{ID: 111, Username: "testbot"})
		case "deleteWebhook":
			f.mu.Unlock()
			f.writeResult(w, true)
		case "getUpdates":
			offset, _ := strconv.ParseInt(r.PostForm.Get("offset"), 10, 64)
			f.mu.Unlock()
			f.serveUpdates(w, r, offset)
		case "sendChatAction":
			f.mu.Unlock()
			f.writeResult(w, true)
		case "sendRichMessage":
			if !f.richSupported {
				f.mu.Unlock()
				f.writeError(w, fakeFailure{code: 400, description: "Bad Request: method is not available", remaining: 1})
				return
			}
			f.mu.Unlock()
			f.writeMessage(w)
		case "sendRichMessageDraft", "sendMessage":
			f.mu.Unlock()
			f.writeMessage(w)
		default:
			f.mu.Unlock()
			f.t.Errorf("unexpected method %s", method)
			http.NotFound(w, r)
		}
	})
	return mux
}

func (f *fakeBot) serveUpdates(w http.ResponseWriter, r *http.Request, offset int64) {
	deadline := time.NewTimer(100 * time.Millisecond)
	defer deadline.Stop()
	for {
		f.mu.Lock()
		var batch []Update
		for _, u := range f.queue {
			if u.UpdateID >= offset {
				batch = append(batch, u)
			}
		}
		if len(batch) > 0 {
			f.queue = nil
			f.mu.Unlock()
			f.writeResult(w, batch)
			return
		}
		f.mu.Unlock()
		select {
		case <-f.wake:
		case <-deadline.C:
			f.writeResult(w, []Update{})
			return
		case <-r.Context().Done():
			return
		}
	}
}

func (f *fakeBot) writeResult(w http.ResponseWriter, result any) {
	w.Header().Set("Content-Type", "application/json")
	body, err := json.Marshal(map[string]any{"ok": true, "result": result})
	if err != nil {
		f.t.Errorf("marshal result: %v", err)
		return
	}
	_, _ = w.Write(body)
}

func (f *fakeBot) writeError(w http.ResponseWriter, fail fakeFailure) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(fail.code)
	body, err := json.Marshal(map[string]any{
		"ok":          false,
		"error_code":  fail.code,
		"description": fail.description,
		"parameters":  map[string]any{"retry_after": fail.retryAfter},
	})
	if err != nil {
		f.t.Errorf("marshal error: %v", err)
		return
	}
	_, _ = w.Write(body)
}

func (f *fakeBot) writeMessage(w http.ResponseWriter) {
	f.mu.Lock()
	f.nextMessageID++
	id := f.nextMessageID
	f.mu.Unlock()
	f.writeResult(w, ResponseMessage{MessageID: id})
}

func waitCalls(t *testing.T, f *fakeBot, method string, min int) []fakeCall {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		calls := f.callsFor(method)
		if len(calls) >= min {
			return calls
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d %s calls, got %d", min, method, len(calls))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func assertNoCalls(t *testing.T, f *fakeBot, method string, settle time.Duration) {
	t.Helper()
	time.Sleep(settle)
	if n := len(f.callsFor(method)); n != 0 {
		t.Fatalf("expected no %s calls, got %d", method, n)
	}
}

func waitForMessages(t *testing.T, rec *submitRecorder, min int) []gateway.InboundMessage {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		msgs := rec.messages()
		if len(msgs) >= min {
			return msgs
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d submissions, got %d", min, len(msgs))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitForOffset(t *testing.T, f *fakeBot, want int64) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		for _, c := range f.callsFor("getUpdates") {
			if c.param("offset") == strconv.FormatInt(want, 10) {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for getUpdates offset %d", want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitGoroutines(t *testing.T, want int, what string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if runtime.NumGoroutine() <= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for goroutines to settle to %d for %s, at %d", want, what, runtime.NumGoroutine())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type submitRecorder struct {
	mu      sync.Mutex
	msgs    []gateway.InboundMessage
	receipt gateway.Receipt
	err     error
}

func (s *submitRecorder) Submit(ctx context.Context, msg gateway.InboundMessage) (gateway.Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = append(s.msgs, msg)
	return s.receipt, s.err
}

func (s *submitRecorder) RegisterSink(transport string, sink gateway.DeliverySink) {}

func (s *submitRecorder) messages() []gateway.InboundMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]gateway.InboundMessage, len(s.msgs))
	copy(out, s.msgs)
	return out
}

type echoRunner struct{}

func (echoRunner) Run(ctx context.Context, work gateway.WorkItem) (gateway.RunResult, error) {
	return gateway.RunResult{Text: "reply:" + work.Text}, nil
}

func startTransport(t *testing.T, fake *fakeBot, mutate func(*Options)) (*Telegram, context.CancelFunc) {
	t.Helper()
	opts := Options{
		Gateway:         &submitRecorder{},
		Token:           func() (string, error) { return fake.token, nil },
		AllowedUserIDs:  []int64{42},
		APIBase:         fake.url(),
		PollTimeoutSecs: 5,
	}
	if mutate != nil {
		mutate(&opts)
	}
	tr := New(opts)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- tr.Start(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("transport did not stop after cancel")
		}
	})
	waitCalls(t, fake, "getUpdates", 1)
	return tr, cancel
}

func rawServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func closedServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	return url
}
