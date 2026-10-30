package machineclient

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/digitalygo/smidja/internal/privateconfig"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/machinetools"
)

type controlSpy struct{ calls atomic.Int32 }

func (s *controlSpy) Execute(context.Context, string, json.RawMessage) machinetools.Result {
	s.calls.Add(1)
	return machinetools.Result{OK: true}
}
func (s *controlSpy) ExecuteControl(context.Context, string, json.RawMessage) machinetools.Result {
	s.calls.Add(1)
	return machinetools.Result{OK: true}
}
func TestClientRefusesInvalidControlFrames(t *testing.T) {
	for _, test := range []struct {
		name     string
		control  bool
		executor Executor
		message  string
	}{
		{"__smidja_unknown", true, &controlSpy{}, "unsupported client control"},
		{ApplyExtensionControl, false, &controlSpy{}, "reserved control command"},
		{ApplyExtensionControl, true, executorFunc(func(context.Context, string, json.RawMessage) machinetools.Result {
			t.Error("executed control as ordinary tool")
			return machinetools.Result{OK: true}
		}), "unsupported client control"},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/clients/connect" {
				w.Write([]byte(`{}`))
				return
			}
			json.NewEncoder(w).Encode(Execution{ProtocolVersion: 1, RequestID: "control1", ClientID: "laptop", Tool: test.name, Control: test.control, Args: json.RawMessage(`{}`), Deadline: time.Now().Add(time.Second)})
		}))
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		cfg := clientConfig(server.URL)
		cfg.HeartbeatInterval = time.Hour
		err := Run(ctx, cfg, test.executor)
		cancel()
		server.Close()
		if err == nil || !strings.Contains(err.Error(), test.message) {
			t.Fatal(err)
		}
		if spy, ok := test.executor.(*controlSpy); ok && spy.calls.Load() != 0 {
			t.Fatal("invalid frame executed")
		}
	}
}
func TestPrivateInventoryProvenanceAndReservedNames(t *testing.T) {
	baseline := Inventory{OS: "linux", Arch: "amd64", Tools: []string{"private:echo@1"}, ToolDefinitions: []ToolDefinition{{ID: "private:echo", Route: "private:echo@1", PrivateRevision: 1, Parameters: json.RawMessage(`{"type":"object"}`)}}}
	if !validInventory(baseline) {
		t.Fatal("valid private metadata rejected")
	}
	for _, mutate := range []func(*Inventory){
		func(i *Inventory) { i.ToolDefinitions[0].PrivateRevision = 0 },
		func(i *Inventory) {
			i.ToolDefinitions[0].Package = PackageRevision{ID: "package", Version: "1", Digest: "digest"}
			i.Packages = []PackageRevision{i.ToolDefinitions[0].Package}
		},
		func(i *Inventory) { i.ToolDefinitions[0].ID = "package/name" },
		func(i *Inventory) {
			i.ToolDefinitions[0].Route = "private:echo@2"
			i.Tools = []string{"private:echo@2"}
		},
		func(i *Inventory) { i.ToolDefinitions[0].ID = "private:../escape" },
		func(i *Inventory) { i.Tools = append(i.Tools, ApplyExtensionControl) },
	} {
		copy := copyInventory(baseline)
		mutate(&copy)
		if validInventory(copy) {
			t.Fatal("invalid private provenance accepted", copy)
		}
	}
	b := NewBroker(BrokerConfig{})
	register(t, b, "laptop")
	b.mu.Lock()
	b.clients["laptop"].status.Inventory.Tools = append(b.clients["laptop"].status.Inventory.Tools, ApplyExtensionControl)
	b.mu.Unlock()
	if result := b.Execute(context.Background(), "laptop", ApplyExtensionControl, json.RawMessage(`{}`)); result.Code != "tool_unavailable" {
		t.Fatal("advertisement bypassed public control deny", result)
	}
}

func TestExtensionControlAdmissionAndClientRefresh(t *testing.T) {
	for _, refreshFailure := range []bool{false, true} {
		b := NewBroker(BrokerConfig{PollTimeout: 10 * time.Millisecond, ExecutionTimeout: time.Second})
		if err := b.AddClient("laptop", "Laptop", testToken); err != nil {
			t.Fatal(err)
		}
		definition := ExtensionControl{ID: "private:echo", Revision: 1, Definition: privateconfig.Extension{ClientID: "laptop", Parameters: json.RawMessage(`{"type":"object"}`)}}
		invalid := definition
		invalid.Definition.ClientID = "other"
		if result := b.ApplyExtension(context.Background(), "laptop", invalid); result.Code != "invalid_arguments" {
			t.Fatal(result)
		}
		invalid = definition
		invalid.Definition.Parameters = json.RawMessage(`{`)
		if result := b.ApplyExtension(context.Background(), "laptop", invalid); result.Code != "invalid_arguments" {
			t.Fatal(result)
		}
		server := httptest.NewServer(b)
		ctx, cancel := context.WithCancel(context.Background())
		spy := &controlSpy{}
		cfg := clientConfig(server.URL)
		cfg.HeartbeatInterval = time.Hour
		cfg.InventoryProvider = func(context.Context) (Inventory, error) {
			if refreshFailure && spy.calls.Load() > 0 {
				return Inventory{}, errors.New("inventory unavailable")
			}
			return Inventory{OS: "linux", Arch: "amd64", Tools: []string{"read"}, ExtensionControls: true}, nil
		}
		done := make(chan error, 1)
		go func() { done <- Run(ctx, cfg, spy) }()
		deadline := time.Now().Add(time.Second)
		for !b.List()[0].Online && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		result := b.ApplyExtension(ctx, "laptop", definition)
		cancel()
		<-done
		server.Close()
		if spy.calls.Load() != 1 {
			t.Fatal("control not executed exactly once")
		}
		if refreshFailure {
			if result.Code != "inventory_refresh_failed" {
				t.Fatal(result)
			}
		} else if !result.OK {
			t.Fatal(result)
		}
	}
}

func TestExtensionDispatchUsesMetadataTimeoutAndParentLimit(t *testing.T) {
	for _, seconds := range []int{0, 450} {
		for _, parentLimit := range []bool{false, true} {
			b := NewBroker(BrokerConfig{PollTimeout: time.Second, ExecutionTimeout: 10 * time.Millisecond})
			register(t, b, "laptop")
			b.mu.Lock()
			b.clients["laptop"].status.Inventory.Tools = append(b.clients["laptop"].status.Inventory.Tools, "private:echo@1")
			b.clients["laptop"].status.Inventory.ToolDefinitions = []ToolDefinition{{ID: "private:echo", Route: "private:echo@1", PrivateRevision: 1, TimeoutSeconds: seconds, Parameters: json.RawMessage(`{}`)}}
			b.mu.Unlock()
			ctx, cancel := context.WithCancel(context.Background())
			if parentLimit {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), time.Second)
			}
			done := make(chan machinetools.Result, 1)
			before := time.Now()
			go func() { done <- b.Execute(ctx, "laptop", "private:echo@1", json.RawMessage(`{"timeout_seconds":600}`)) }()
			response := request(t, b, "GET", "/api/clients/poll?client_id=laptop", testToken, nil)
			var execution Execution
			if json.Unmarshal(response.Body.Bytes(), &execution) != nil {
				t.Fatal(response.Body.String())
			}
			expected := time.Duration(seconds) * time.Second
			if seconds == 0 {
				expected = 30 * time.Second
			}
			if parentLimit {
				expected = time.Second
			}
			actual := execution.Deadline.Sub(before)
			if actual < expected-100*time.Millisecond || actual > expected+100*time.Millisecond {
				t.Fatal("incorrect deadline", actual, expected)
			}
			cancel()
			if result := <-done; result.Code != "outcome_unknown" {
				t.Fatal("deadline/cancel did not preserve uncertainty", result)
			}
		}
	}
	for _, seconds := range []int{-1, 601} {
		inventory := Inventory{OS: "linux", Arch: "amd64", Tools: []string{"private:echo@1"}, ToolDefinitions: []ToolDefinition{{ID: "private:echo", Route: "private:echo@1", PrivateRevision: 1, TimeoutSeconds: seconds, Parameters: json.RawMessage(`{}`)}}}
		if validInventory(inventory) {
			t.Fatal("invalid metadata accepted")
		}
	}
}

func TestRetainedPackageTimeoutDispatch(t *testing.T) {
	b := NewBroker(BrokerConfig{PollTimeout: time.Second})
	register(t, b, "laptop")
	route := "package:example@1+digest:example/echo"
	b.mu.Lock()
	b.clients["laptop"].status.Inventory.Tools = append(b.clients["laptop"].status.Inventory.Tools, route)
	b.clients["laptop"].status.Inventory.PackageTimeouts = map[string]int{route: 450}
	b.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan machinetools.Result, 1)
	before := time.Now()
	go func() { done <- b.Execute(ctx, "laptop", route, json.RawMessage(`{}`)) }()
	response := request(t, b, "GET", "/api/clients/poll?client_id=laptop", testToken, nil)
	var execution Execution
	if json.Unmarshal(response.Body.Bytes(), &execution) != nil {
		t.Fatal(response.Body.String())
	}
	if elapsed := execution.Deadline.Sub(before); elapsed < 449*time.Second || elapsed > 451*time.Second {
		t.Fatal("retained timeout clipped", elapsed)
	}
	cancel()
	<-done
	baseline := Inventory{OS: "linux", Arch: "amd64", Tools: []string{route}, PackageTimeouts: map[string]int{route: 450}}
	if !validInventory(baseline) {
		t.Fatal("retained-only metadata rejected")
	}
	for _, values := range []map[string]int{{route: -1}, {route: 601}, {"read": 30}, {"package:missing": 30}} {
		baseline.PackageTimeouts = values
		if validInventory(baseline) {
			t.Fatal("invalid retained metadata accepted", values)
		}
	}
}
