package machineclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/machinetools"
)

func packageInventory(version string) Inventory {
	revision := PackageRevision{ID: "blender", Version: version, Digest: strings.Repeat("a", 64)}
	return Inventory{OS: "linux", Arch: "amd64", Tools: []string{"read", "route-" + version}, Packages: []PackageRevision{revision}, ToolDefinitions: []ToolDefinition{{ID: "blender/tool", Route: "route-" + version, Description: "Blender tool", Parameters: json.RawMessage(`{"type":"object"}`), Package: revision}}, TeamDefinitions: []CatalogDefinition{{ID: "blender/team", Package: revision, Definition: json.RawMessage(`{"id":"blender/team","name":"Blender"}`)}}, WorkflowDefinitions: []CatalogDefinition{{ID: "blender/work", Package: revision, Definition: json.RawMessage(`{"id":"blender/work"}`)}}}
}
func TestInventoryRefreshRetainsBindingsAndOfflineDefinitions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clients.json")
	b := NewBroker(BrokerConfig{InventoryPath: path, PollTimeout: time.Second, HeartbeatTTL: 20 * time.Millisecond})
	if err := b.AddClient("laptop", "Laptop", testToken); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(b)
	defer server.Close()
	inventory := packageInventory("1.0.0")
	c, err := newRemoteClient(Config{ManagerURL: server.URL, ClientID: "laptop", Name: "Laptop", Token: testToken, InventoryProvider: func(context.Context) (Inventory, error) { return inventory, nil }})
	if err != nil {
		t.Fatal(err)
	}
	if err = c.refreshInventory(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	execution, done := dispatched(t, b, "laptop", context.Background())
	inventory = packageInventory("1.1.0")
	inventory.Tools = append(inventory.Tools, "route-1.0.0")
	if err = c.refreshInventory(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if got := b.List()[0]; got.Inventory.ToolDefinitions[0].Package.Version != "1.1.0" {
		t.Fatal(got)
	}
	w := request(t, b, "POST", "/api/clients/result", testToken, ResultEnvelope{ClientID: "laptop", RequestID: execution.RequestID, Result: machinetools.Result{OK: true}})
	if w.Code != 200 || !(<-done).OK {
		t.Fatal("refresh invalidated pending execution", w.Code)
	}
	snapshot := b.List()
	snapshot[0].Inventory.ToolDefinitions[0].Parameters[0] = 'x'
	if !json.Valid(b.List()[0].Inventory.ToolDefinitions[0].Parameters) {
		t.Fatal("metadata alias")
	}
	time.Sleep(30 * time.Millisecond)
	if b.List()[0].Online {
		t.Fatal("client never became offline")
	}
	restored := NewBroker(BrokerConfig{InventoryPath: path})
	if restored.Err() != nil {
		t.Fatal(restored.Err())
	}
	got := restored.List()[0]
	if got.Online || len(got.Inventory.TeamDefinitions) != 1 || len(got.Inventory.WorkflowDefinitions) != 1 || got.Inventory.Packages[0].Version != "1.1.0" {
		t.Fatal(got)
	}
}

func TestHeartbeatPublishesChangedProviderInventory(t *testing.T) {
	b := NewBroker(BrokerConfig{})
	if err := b.AddClient("laptop", "Laptop", testToken); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(b)
	defer server.Close()
	var mu sync.Mutex
	inventory := packageInventory("1.0.0")
	cfg := Config{ManagerURL: server.URL, ClientID: "laptop", Name: "Laptop", Token: testToken, HeartbeatInterval: 5 * time.Millisecond, InventoryProvider: func(context.Context) (Inventory, error) {
		mu.Lock()
		defer mu.Unlock()
		return copyInventory(inventory), nil
	}}
	c, err := newRemoteClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.refreshInventory(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.beat(ctx) }()
	mu.Lock()
	inventory = packageInventory("1.1.0")
	mu.Unlock()
	deadline := time.Now().Add(time.Second)
	for b.List()[0].Inventory.Packages[0].Version != "1.1.0" {
		if time.Now().After(deadline) {
			t.Fatal("heartbeat did not announce change")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err = <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestInventoryValidationAndProviderFailure(t *testing.T) {
	for _, modify := range []func(*Inventory){
		func(in *Inventory) { in.Tools = append(in.Tools, in.Tools[0]) },
		func(in *Inventory) { in.Packages = append(in.Packages, in.Packages[0]) },
		func(in *Inventory) { in.ToolDefinitions[0].Route = "unadvertised" },
		func(in *Inventory) { in.ToolDefinitions[0].Package.Version = "unknown" },
		func(in *Inventory) { in.TeamDefinitions[0].ID = "blender/tool" },
		func(in *Inventory) { in.WorkflowDefinitions[0].Definition = json.RawMessage(`{`) },
	} {
		inventory := packageInventory("1.0.0")
		modify(&inventory)
		if validInventory(inventory) {
			t.Fatal("invalid metadata accepted")
		}
	}
	c, err := newRemoteClient(Config{ManagerURL: "http://127.0.0.1", ClientID: "laptop", Name: "Laptop", Token: testToken, InventoryProvider: func(context.Context) (Inventory, error) { return Inventory{}, errors.New("invalid installed package") }})
	if err != nil {
		t.Fatal(err)
	}
	if err = c.refreshInventory(context.Background(), true); err == nil {
		t.Fatal("provider error ignored")
	}
	c.cfg.InventoryProvider = func(context.Context) (Inventory, error) { return Inventory{}, nil }
	if err = c.refreshInventory(context.Background(), true); err == nil {
		t.Fatal("empty inventory accepted")
	}
	c.cfg.InventoryProvider = func(context.Context) (Inventory, error) {
		in := packageInventory("1.0.0")
		in.ToolDefinitions[0].Description = strings.Repeat("x", 1<<20)
		return in, nil
	}
	if err = c.refreshInventory(context.Background(), true); err == nil || !strings.Contains(err.Error(), "1 MiB") {
		t.Fatal(err)
	}
}
