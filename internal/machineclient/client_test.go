package machineclient

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/digitalygo/smidja/internal/machinetools"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

type executorFunc func(context.Context, string, json.RawMessage) machinetools.Result

func (f executorFunc) Execute(ctx context.Context, name string, args json.RawMessage) machinetools.Result {
	return f(ctx, name, args)
}
func clientConfig(url string) Config {
	return Config{ManagerURL: url, ClientID: "laptop", Name: "Laptop", Token: testToken, HeartbeatInterval: 5 * time.Millisecond, Inventory: Inventory{Tools: []string{"read"}}}
}

func TestClientEndToEndAndCancellation(t *testing.T) {
	b := NewBroker(BrokerConfig{PollTimeout: 20 * time.Millisecond, ExecutionTimeout: time.Second})
	if err := b.AddClient("laptop", "Laptop", testToken); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(b.Handler())
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := make(chan struct{}, 1)
	executor := executorFunc(func(ctx context.Context, tool string, args json.RawMessage) machinetools.Result {
		if tool != "read" {
			t.Errorf("tool %s", tool)
		}
		if string(args) == `{"block":true}` {
			calls <- struct{}{}
			<-ctx.Done()
			return failure("cancelled", ctx.Err().Error())
		}
		return machinetools.Result{OK: true, Data: "read result"}
	})
	done := make(chan error, 1)
	go func() { done <- Run(ctx, clientConfig(server.URL), executor) }()
	until := time.Now().Add(time.Second)
	for !b.List()[0].Online && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	result := b.Execute(ctx, "laptop", "read", json.RawMessage(`{}`))
	if !result.OK || result.Data != "read result" {
		t.Fatal(result)
	}
	taskCtx, taskCancel := context.WithCancel(ctx)
	taskDone := make(chan machinetools.Result, 1)
	go func() { taskDone <- b.Execute(taskCtx, "laptop", "read", json.RawMessage(`{"block":true}`)) }()
	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("tool not dispatched")
	}
	taskCancel()
	if result := <-taskDone; result.Code != "outcome_unknown" {
		t.Fatal(result)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("delivery after cancelled task unexpectedly succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("heartbeat did not cancel tool")
	}
}

func TestClientValidation(t *testing.T) {
	for _, url := range []string{"invalid", "http://example.org", "https://user:pass@example.org", "https://example.org/path", "https://example.org?q=x", "https://example.org#x", "ftp://127.0.0.1", "http://[::1"} {
		if _, err := newRemoteClient(clientConfig(url)); err == nil {
			t.Errorf("accepted %s", url)
		}
	}
	for _, url := range []string{"https://example.org", "http://localhost", "http://127.0.0.1", "http://[::1]"} {
		cfg := clientConfig(url)
		cfg.HeartbeatInterval = 0
		c, err := newRemoteClient(cfg)
		if err != nil {
			t.Fatal(url, err)
		}
		if c.cfg.HeartbeatInterval != 10*time.Second || c.http.Timeout != 45*time.Second {
			t.Fatal("defaults")
		}
	}
	cfg := clientConfig("http://127.0.0.1")
	cfg.Token = "short"
	if _, err := newRemoteClient(cfg); err == nil {
		t.Fatal("short token")
	}
	if err := Run(context.Background(), clientConfig("http://127.0.0.1"), nil); err == nil {
		t.Fatal("nil executor")
	}
	if err := Run(context.Background(), cfg, nil); err == nil {
		t.Fatal("invalid config")
	}
}

func TestClientRejectsRedirectAndInvalidReplies(t *testing.T) {
	var destinationHits atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { destinationHits.Add(1) }))
	defer destination.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 307) }))
	defer redirect.Close()
	cfg := clientConfig(redirect.URL)
	cfg.HTTPClient = &http.Client{}
	if err := Run(context.Background(), cfg, executorFunc(func(context.Context, string, json.RawMessage) machinetools.Result { return machinetools.Result{} })); err == nil {
		t.Fatal("redirect accepted")
	}
	if destinationHits.Load() != 0 {
		t.Fatal("credentials followed redirect")
	}
	for _, status := range []int{401, 500} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
		c, _ := newRemoteClient(clientConfig(server.URL))
		if err := c.request(context.Background(), "GET", "/x", nil, nil); err == nil {
			t.Fatal(status)
		}
		server.Close()
	}
	for _, body := range []string{"bad", string(make([]byte, (2<<20)+1))} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
		c, _ := newRemoteClient(clientConfig(server.URL))
		var result Execution
		if err := c.request(context.Background(), "GET", "/x", nil, &result); err == nil {
			t.Fatal("invalid response")
		}
		server.Close()
	}
	c, _ := newRemoteClient(clientConfig("http://127.0.0.1"))
	if err := c.request(context.Background(), "POST", "/x", make(chan int), nil); err == nil {
		t.Fatal("invalid payload")
	}
}

func TestClientExecutionValidationAndReceiptDedup(t *testing.T) {
	valid := Execution{ProtocolVersion: 1, RequestID: "operation", ClientID: "laptop", Tool: "read", Args: json.RawMessage(`{}`), Deadline: time.Now().Add(time.Minute)}
	for _, change := range []func(*Execution){func(e *Execution) { e.ClientID = "other" }, func(e *Execution) { e.ProtocolVersion = 2 }, func(e *Execution) { e.Tool = "bash" }, func(e *Execution) { e.Deadline = time.Now().Add(-time.Second) }} {
		execution := valid
		change(&execution)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/clients/connect" {
				sendJSON(w, 200, map[string]int{"protocol_version": 1})
				return
			}
			sendJSON(w, 200, execution)
		}))
		calls := 0
		err := Run(context.Background(), clientConfig(server.URL), executorFunc(func(context.Context, string, json.RawMessage) machinetools.Result {
			calls++
			return machinetools.Result{OK: true}
		}))
		server.Close()
		if err == nil || calls != 0 {
			t.Fatal("invalid binding executed", err, calls)
		}
	}
	var polls, results, calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/clients/connect":
			sendJSON(w, 200, map[string]int{"protocol_version": 1})
		case "/api/clients/poll":
			if polls.Add(1) > 2 {
				http.Error(w, "stop", 503)
				return
			}
			sendJSON(w, 200, valid)
		case "/api/clients/result":
			results.Add(1)
			sendJSON(w, 200, map[string]bool{"accepted": true})
		case "/api/clients/heartbeat":
			sendJSON(w, 200, Heartbeat{ClientID: "laptop"})
		}
	}))
	defer server.Close()
	err := Run(context.Background(), clientConfig(server.URL), executorFunc(func(context.Context, string, json.RawMessage) machinetools.Result {
		calls.Add(1)
		return machinetools.Result{OK: true}
	}))
	if err == nil || calls.Load() != 1 || results.Load() != 2 {
		t.Fatal(err, calls.Load(), results.Load())
	}
}

func TestHeartbeatMismatchAndCancelledContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { sendJSON(w, 200, Heartbeat{ClientID: "other"}) }))
	defer server.Close()
	c, _ := newRemoteClient(clientConfig(server.URL))
	if err := c.beat(context.Background()); err == nil {
		t.Fatal("heartbeat binding")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.beat(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
