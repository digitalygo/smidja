package machineclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/machinetools"
)

func TestResultGoneRetainsCacheAcrossExpiredDuplicate(t *testing.T) {
	var polls, deliveries, calls atomic.Int32
	execution := Execution{ProtocolVersion: 1, RequestID: "once", ClientID: "laptop", Tool: "read", Args: json.RawMessage(`{}`), Deadline: time.Now().Add(time.Second)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/clients/connect":
			w.Write([]byte(`{}`))
		case "/api/clients/poll":
			switch polls.Add(1) {
			case 1:
				json.NewEncoder(w).Encode(execution)
			case 2:
				time.Sleep(time.Until(execution.Deadline) + time.Millisecond)
				json.NewEncoder(w).Encode(execution)
			default:
				http.Error(w, "stop", http.StatusUnauthorized)
			}
		case "/api/clients/result":
			deliveries.Add(1)
			http.Error(w, "expired", http.StatusGone)
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer server.Close()
	cfg := clientConfig(server.URL)
	cfg.HeartbeatInterval = time.Hour
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := Run(ctx, cfg, executorFunc(func(context.Context, string, json.RawMessage) machinetools.Result {
		calls.Add(1)
		return machinetools.Result{OK: true}
	}))
	var status *protocolStatusError
	if !errors.As(err, &status) || status.Status != 401 {
		t.Fatal("stale result/duplicate ended healthy client", err)
	}
	if calls.Load() != 1 || deliveries.Load() != 2 {
		t.Fatal("duplicate execution or missing cached delivery", calls.Load(), deliveries.Load())
	}
}
func TestResultSecurityFailuresRemainFatal(t *testing.T) {
	for _, code := range []int{401, 403, 409, 500} {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/clients/connect":
				w.Write([]byte(`{}`))
			case "/api/clients/poll":
				json.NewEncoder(w).Encode(Execution{ProtocolVersion: 1, RequestID: "once", ClientID: "laptop", Tool: "read", Args: json.RawMessage(`{}`), Deadline: time.Now().Add(time.Second)})
			case "/api/clients/result":
				http.Error(w, "failure", code)
			default:
				http.Error(w, "unexpected", 500)
			}
		}))
		cfg := clientConfig(server.URL)
		cfg.HeartbeatInterval = time.Hour
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := Run(ctx, cfg, executorFunc(func(context.Context, string, json.RawMessage) machinetools.Result {
			calls.Add(1)
			return machinetools.Result{OK: true}
		}))
		cancel()
		server.Close()
		var status *protocolStatusError
		if !errors.As(err, &status) || status.Status != code || calls.Load() != 1 || !strings.Contains(err.Error(), "must not be repeated") {
			t.Fatal(code, err, calls.Load())
		}
	}
}
func TestGoneOtherEndpointRemainsFatal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "gone", http.StatusGone) }))
	defer server.Close()
	client, err := newRemoteClient(clientConfig(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	err = client.request(context.Background(), http.MethodGet, "/api/clients/poll", nil, nil)
	var status *protocolStatusError
	if !errors.As(err, &status) || status.Status != http.StatusGone {
		t.Fatal("non-result410 hidden", err)
	}
}
