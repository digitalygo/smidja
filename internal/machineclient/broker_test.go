package machineclient

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/digitalygo/smidja/internal/machinetools"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testToken = "test-client-token-0123456789"

func request(t *testing.T, h http.Handler, method, path, token string, value any) *httptest.ResponseRecorder {
	t.Helper()
	var body []byte
	if value != nil {
		var err error
		body, err = json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func register(t *testing.T, b *Broker, id string) {
	t.Helper()
	token := testToken
	if id == "desktop" {
		token += "desktop"
	}
	if err := b.AddClient(id, id, token); err != nil {
		t.Fatal(err)
	}
	w := request(t, b, "POST", "/api/clients/connect", token, Registration{ProtocolVersion: 1, ClientID: id, Inventory: Inventory{OS: "linux", Arch: "arm64", Tools: []string{"read", "bash"}, Teams: []string{"Blender"}, Packages: []PackageRevision{{ID: "blender", Version: "1.0.0", Digest: "digest"}}}})
	if w.Code != 200 {
		t.Fatalf("register %d %s", w.Code, w.Body.String())
	}
}
func dispatched(t *testing.T, b *Broker, id string, ctx context.Context) (Execution, <-chan machinetools.Result) {
	t.Helper()
	done := make(chan machinetools.Result, 1)
	go func() { done <- b.Execute(ctx, id, "read", json.RawMessage(`{"path":"/tmp/file"}`)) }()
	w := request(t, b, "GET", "/api/clients/poll?client_id="+id, testToken, nil)
	if w.Code != 200 {
		t.Fatalf("poll %d %s", w.Code, w.Body.String())
	}
	var execution Execution
	if err := json.Unmarshal(w.Body.Bytes(), &execution); err != nil {
		t.Fatal(err)
	}
	return execution, done
}
func TestBrokerBindingReceiptAndInventory(t *testing.T) {
	b := NewBroker(BrokerConfig{PollTimeout: time.Second, CompletedLimit: 1})
	register(t, b, "laptop")
	register(t, b, "desktop")
	execution, done := dispatched(t, b, "laptop", context.Background())
	if execution.ClientID != "laptop" || execution.Tool != "read" || execution.ProtocolVersion != 1 {
		t.Fatal(execution)
	}
	envelope := ResultEnvelope{ClientID: "desktop", RequestID: execution.RequestID, Result: machinetools.Result{OK: true, Data: "original"}}
	if w := request(t, b, "POST", "/api/clients/result", testToken+"desktop", envelope); w.Code != 409 {
		t.Fatal(w.Code)
	}
	envelope.ClientID = "laptop"
	if w := request(t, b, "POST", "/api/clients/result", testToken, envelope); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if result := <-done; !result.OK || result.Data != "original" {
		t.Fatal(result)
	}
	if w := request(t, b, "POST", "/api/clients/result", testToken, envelope); w.Code != 200 {
		t.Fatal(w.Code)
	}
	envelope.Result.Data = "different"
	if w := request(t, b, "POST", "/api/clients/result", testToken, envelope); w.Code != 409 {
		t.Fatal(w.Code)
	}
	statuses := b.List()
	if len(statuses) != 2 || statuses[0].ID != "desktop" || !statuses[1].Online {
		t.Fatal(statuses)
	}
	statuses[1].Inventory.Tools[0] = "changed"
	if b.List()[1].Inventory.Tools[0] != "read" {
		t.Fatal("inventory alias")
	}
	second, done2 := dispatched(t, b, "laptop", context.Background())
	envelope.RequestID = second.RequestID
	if w := request(t, b, "POST", "/api/clients/result", testToken, envelope); w.Code != 200 {
		t.Fatal(w.Code)
	}
	<-done2
	envelope.RequestID = execution.RequestID
	if w := request(t, b, "POST", "/api/clients/result", testToken, envelope); w.Code != 410 {
		t.Fatal(w.Code)
	}
}
func TestBrokerCancellationRevocationAndTimeout(t *testing.T) {
	b := NewBroker(BrokerConfig{PollTimeout: 20 * time.Millisecond, ExecutionTimeout: 80 * time.Millisecond})
	register(t, b, "laptop")
	ctx, cancel := context.WithCancel(context.Background())
	execution, done := dispatched(t, b, "laptop", ctx)
	cancel()
	if result := <-done; result.Code != "outcome_unknown" {
		t.Fatal(result)
	}
	w := request(t, b, "POST", "/api/clients/heartbeat", testToken, Heartbeat{ClientID: "laptop"})
	var reply Heartbeat
	json.Unmarshal(w.Body.Bytes(), &reply)
	if len(reply.Cancelled) != 1 || reply.Cancelled[0] != execution.RequestID {
		t.Fatal(reply)
	}
	request(t, b, "POST", "/api/clients/heartbeat", testToken, Heartbeat{ClientID: "laptop", Cancelled: reply.Cancelled})
	if len(b.clients["laptop"].cancelled) != 0 {
		t.Fatal("cancel not acknowledged")
	}
	if w = request(t, b, "POST", "/api/clients/result", testToken, ResultEnvelope{ClientID: "laptop", RequestID: execution.RequestID}); w.Code != 410 {
		t.Fatal(w.Code)
	}
	_, revoked := dispatched(t, b, "laptop", context.Background())
	b.Revoke("laptop")
	if result := <-revoked; result.Code != "client_revoked" {
		t.Fatal(result)
	}
	b.Revoke("laptop")
	if w = request(t, b, "GET", "/api/clients/poll?client_id=laptop", testToken, nil); w.Code != 401 {
		t.Fatal(w.Code)
	}
	register(t, b, "laptop")
	if result := b.Execute(context.Background(), "laptop", "read", json.RawMessage(`{}`)); result.Code != "cancelled" {
		t.Fatal(result)
	}
	if w = request(t, b, "GET", "/api/clients/poll?client_id=laptop", testToken, nil); w.Code != 204 {
		t.Fatal(w.Code)
	}
	_, expired := dispatched(t, b, "laptop", context.Background())
	if result := <-expired; result.Code != "outcome_unknown" {
		t.Fatal(result)
	}
}
func TestBrokerValidationAndOffline(t *testing.T) {
	b := NewBroker(BrokerConfig{HeartbeatTTL: time.Millisecond, PollTimeout: time.Millisecond})
	if err := b.AddClient("", "name", testToken); err == nil {
		t.Fatal("invalid id accepted")
	}
	if err := b.AddClient("id", "name", "short"); err == nil {
		t.Fatal("short token")
	}
	if err := b.AddClient("id", "name", testToken); err != nil {
		t.Fatal(err)
	}
	if err := b.AddClient("id", "name", testToken); err == nil {
		t.Fatal("replacement without revoke")
	}
	if result := b.Execute(context.Background(), "id", "read", json.RawMessage(`{}`)); result.Code != "client_offline" {
		t.Fatal(result)
	}
	if result := b.Execute(context.Background(), "id", "read", json.RawMessage(`broken`)); result.Code != "invalid_arguments" {
		t.Fatal(result)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result := b.Execute(ctx, "id", "read", json.RawMessage(`{}`)); result.Code != "cancelled" {
		t.Fatal(result)
	}
	if w := request(t, b, "POST", "/api/clients/heartbeat", testToken, Heartbeat{ClientID: "id"}); w.Code != 409 {
		t.Fatal(w.Code)
	}
	if w := request(t, b, "GET", "/api/clients/poll?client_id=id", testToken, nil); w.Code != 409 {
		t.Fatal(w.Code)
	}
	registration := Registration{ProtocolVersion: 1, ClientID: "id", Inventory: Inventory{OS: "linux", Arch: "amd64", Tools: []string{"read"}}}
	if w := request(t, b, "POST", "/api/clients/connect", testToken, registration); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if result := b.Execute(context.Background(), "id", "missing", json.RawMessage(`{}`)); result.Code != "tool_unavailable" {
		t.Fatal(result)
	}
	time.Sleep(3 * time.Millisecond)
	if b.List()[0].Online {
		t.Fatal("online TTL")
	}
	if result := b.Execute(context.Background(), "id", "read", json.RawMessage(`{}`)); result.Code != "client_offline" {
		t.Fatal(result)
	}
	if b.List()[0].Inventory.Tools[0] != "read" {
		t.Fatal("offline inventory removed")
	}
}
func TestHTTPGuards(t *testing.T) {
	b := NewBroker(BrokerConfig{})
	register(t, b, "laptop")
	for _, path := range []string{"connect", "heartbeat", "result"} {
		if w := request(t, b, "GET", "/api/clients/"+path, testToken, nil); w.Code != 405 {
			t.Fatal(path, w.Code)
		}
		if w := request(t, b, "POST", "/api/clients/"+path, "bad", Heartbeat{ClientID: "laptop"}); w.Code == 200 {
			t.Fatal(path, w.Code)
		}
	}
	if w := request(t, b, "POST", "/api/clients/poll", testToken, nil); w.Code != 405 {
		t.Fatal(w.Code)
	}
	if w := request(t, b, "GET", "/api/clients/missing", testToken, nil); w.Code != 404 {
		t.Fatal(w.Code)
	}
	for _, body := range []string{"{", `{} {}`, strings.Repeat(" ", 2<<20) + `{}`} {
		r := httptest.NewRequest("POST", "/api/clients/connect", strings.NewReader(body))
		w := httptest.NewRecorder()
		b.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
	r := httptest.NewRequest("POST", "/api/clients/connect", nil)
	r.Header.Set("Origin", "https://example.org")
	w := httptest.NewRecorder()
	b.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
	registration := Registration{ProtocolVersion: 2, ClientID: "laptop"}
	if w := request(t, b, "POST", "/api/clients/connect", testToken, registration); w.Code != 409 {
		t.Fatal(w.Code)
	}
	registration.ProtocolVersion = 1
	if w := request(t, b, "POST", "/api/clients/connect", testToken, registration); w.Code != 400 {
		t.Fatal(w.Code)
	}
	registration.Inventory = Inventory{OS: "linux", Arch: "arm64"}
	if w := request(t, b, "POST", "/api/clients/connect", "wrong", registration); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := request(t, b, "GET", "/api/clients/poll?client_id=unknown", testToken, nil); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := request(t, b, "POST", "/api/clients/result", testToken, ResultEnvelope{ClientID: "unknown"}); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := request(t, b, "POST", "/api/clients/heartbeat", testToken, Heartbeat{ClientID: "unknown"}); w.Code != 401 {
		t.Fatal(w.Code)
	}
}
func TestQueueLimitAndCancelledPoll(t *testing.T) {
	b := NewBroker(BrokerConfig{MaxPending: 1, PollTimeout: time.Second})
	register(t, b, "laptop")
	_, done := dispatched(t, b, "laptop", context.Background())
	if result := b.Execute(context.Background(), "laptop", "read", json.RawMessage(`{}`)); result.Code != "busy" {
		t.Fatal(result)
	}
	b.Revoke("laptop")
	<-done
	register(t, b, "laptop")
	r := httptest.NewRequest("GET", "/api/clients/poll?client_id=laptop", nil)
	r.Header.Set("Authorization", "Bearer "+testToken)
	ctx, cancel := context.WithCancel(r.Context())
	cancel()
	r = r.WithContext(ctx)
	w := httptest.NewRecorder()
	b.ServeHTTP(w, r)
	if w.Body.Len() != 0 {
		t.Fatal(w.Body.String())
	}
}

func TestReconnectDoesNotReplayDispatchedOperation(t *testing.T) {
	b := NewBroker(BrokerConfig{PollTimeout: 10 * time.Millisecond, ExecutionTimeout: 60 * time.Millisecond})
	register(t, b, "laptop")
	_, result := dispatched(t, b, "laptop", context.Background())
	r := request(t, b, "POST", "/api/clients/connect", testToken, Registration{ProtocolVersion: 1, ClientID: "laptop", Inventory: Inventory{OS: "linux", Arch: "amd64", Tools: []string{"read"}}})
	if r.Code != 200 {
		t.Fatal(r.Code)
	}
	r = request(t, b, "GET", "/api/clients/poll?client_id=laptop", testToken, nil)
	if r.Code != 204 {
		t.Fatalf("dispatched operation replayed: %d", r.Code)
	}
	if outcome := <-result; outcome.Code != "outcome_unknown" {
		t.Fatal(outcome)
	}
}

func TestBrokerRejectsSharedClientCredentials(t *testing.T) {
	b := NewBroker(BrokerConfig{})
	if err := b.AddClient("laptop", "Laptop", testToken); err != nil {
		t.Fatal(err)
	}
	if err := b.AddClient("desktop", "Desktop", testToken); err == nil {
		t.Fatal("shared token accepted")
	}
	if len(b.List()) != 1 {
		t.Fatal("failed registration mutated inventory")
	}
	if err := b.AddClient("desktop", "Desktop", testToken+"unique"); err != nil {
		t.Fatal(err)
	}
	if w := request(t, b, "POST", "/api/clients/connect", testToken, Registration{ProtocolVersion: 1, ClientID: "desktop", Inventory: Inventory{OS: "linux", Arch: "amd64"}}); w.Code != 401 {
		t.Fatal("laptop credential authenticated desktop", w.Code)
	}
}

func TestManagerClientIDIsReserved(t *testing.T) {
	b := NewBroker(BrokerConfig{})
	if err := b.AddClient("manager", "Remote manager", testToken); err == nil {
		t.Fatal("remote client reused local manager sentinel")
	}
	if len(b.List()) != 0 {
		t.Fatal("reserved registration changed inventory")
	}
	if _, err := newRemoteClient(Config{ManagerURL: "http://127.0.0.1", ClientID: "manager", Name: "Remote manager", Token: testToken}); err == nil {
		t.Fatal("client accepted local manager sentinel")
	}
}
