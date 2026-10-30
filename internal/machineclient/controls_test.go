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
