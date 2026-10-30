package manager

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/clientextensions"
	"github.com/digitalygo/smidja/internal/machineclient"
	"github.com/digitalygo/smidja/internal/privateconfig"
)

func extensionClient(t *testing.T, broker *machineclient.Broker, id string) *clientextensions.Runtime {
	t.Helper()
	token := strings.Repeat(id, 20)
	if err := broker.AddClient(id, id, token); err != nil {
		t.Fatal(err)
	}
	runtime, err := clientextensions.Open(clientextensions.Config{Dir: t.TempDir(), ClientID: id})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(broker)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- machineclient.Run(ctx, machineclient.Config{ManagerURL: server.URL, ClientID: id, Name: id, Token: token, InventoryProvider: runtime.Inventory, HeartbeatInterval: 10 * time.Millisecond}, runtime)
	}()
	t.Cleanup(func() { cancel(); <-done; server.Close(); runtime.Close() })
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		for _, client := range broker.List() {
			if client.ID == id && client.Online {
				return runtime
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("client did not connect")
	return nil
}
func TestClientExtensionsApprovedActivationExecutionArchiveRestoreMove(t *testing.T) {
	catalog := privateCatalog(t)
	broker := machineclient.NewBroker(machineclient.BrokerConfig{PollTimeout: 10 * time.Millisecond})
	extensionClient(t, broker, "laptop")
	extensionClient(t, broker, "desktop")
	s := &Server{cfg: Config{Private: catalog, Clients: broker}}
	definition := privateconfig.Extension{Name: "echo", Description: "echo", ClientID: "laptop", Parameters: json.RawMessage(`{"type":"object"}`), Command: []string{"/bin/sh", "-c", "printf activated"}}
	payload, _ := json.Marshal(definition)
	proposal, err := catalog.Propose(privateconfig.Change{Action: "create", Kind: "extension", ID: "private:echo", Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	s.syncClientExtensions(context.Background())
	if len(s.clientExtensionActivations()) != 0 {
		t.Fatal("unapproved proposal activated")
	}
	if _, err = catalog.Approve(proposal.ID); err != nil {
		t.Fatal(err)
	}
	if s.clientExtensionActivations()[0].Status == "active" {
		t.Fatal("premature activation")
	}
	s.syncClientExtensions(context.Background())
	if got := s.clientExtensionActivations()[0].Status; got != "active" {
		t.Fatal(got)
	}
	tool := broker.List()[1].Inventory.ToolDefinitions[0]
	if !s.approvedClientDefinition("laptop", tool) || s.approvedClientDefinition("desktop", tool) {
		t.Fatal("incorrect approval binding")
	}
	if result := s.executeClientTool(context.Background(), "laptop", "private:echo@1", json.RawMessage(`{}`)); !result.OK {
		t.Fatal(result)
	}
	s.syncClientExtensions(context.Background())
	if _, err = catalog.ApplyOwner(privateconfig.Change{Action: "archive", Kind: "extension", ID: "private:echo", BaseRevision: 1}); err != nil {
		t.Fatal(err)
	}
	if result := s.executeClientTool(context.Background(), "laptop", "private:echo@1", json.RawMessage(`{}`)); result.OK {
		t.Fatal("archive did not immediately revoke route")
	}
	s.syncClientExtensions(context.Background())
	if got := s.clientExtensionActivations()[0].Status; got != "archived" {
		t.Fatal(got)
	}
	if _, err = catalog.ApplyOwner(privateconfig.Change{Action: "restore", Kind: "extension", ID: "private:echo", BaseRevision: 2}); err != nil {
		t.Fatal(err)
	}
	s.syncClientExtensions(context.Background())
	if result := s.executeClientTool(context.Background(), "laptop", "private:echo@3", json.RawMessage(`{}`)); !result.OK {
		t.Fatal(result)
	}
	definition.ClientID = "desktop"
	savePrivate(t, catalog, "private:echo", "extension", 3, definition)
	s.syncClientExtensions(context.Background())
	if result := s.executeClientTool(context.Background(), "desktop", "private:echo@4", json.RawMessage(`{}`)); !result.OK {
		t.Fatal(result)
	}
	if result := broker.Execute(context.Background(), "laptop", "private:echo@3", json.RawMessage(`{}`)); result.OK {
		t.Fatal("old client still exposes moved extension")
	}
	for _, name := range []string{"private:missing@1", "private:echo", "private:echo@1"} {
		if result := s.executeClientTool(context.Background(), "desktop", name, json.RawMessage(`{}`)); result.OK {
			t.Fatal(name)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.syncClientExtensions(ctx)
}

func TestClientExtensionOfflineAndAbsentCatalog(t *testing.T) {
	broker := machineclient.NewBroker(machineclient.BrokerConfig{})
	s := &Server{cfg: Config{Clients: broker}}
	s.startClientExtensionSync()
	s.syncClientExtensions(context.Background())
	if len(s.clientExtensionActivations()) != 0 || s.approvedClientDefinition("laptop", machineclient.ToolDefinition{}) {
		t.Fatal("missing catalog accepted")
	}
	if s.executeClientTool(context.Background(), "laptop", "private:x@1", json.RawMessage(`{}`)).OK {
		t.Fatal("missing catalog executed")
	}
	if s.executeClientTool(context.Background(), "laptop", "read", json.RawMessage(`{}`)).OK {
		t.Fatal("offline executed")
	}
	s.cfg.Private = privateCatalog(t)
	definition := extensionDefinition()
	definition.ClientID = "missing"
	savePrivate(t, s.cfg.Private, "private:lookup", "extension", 0, definition)
	s.syncClientExtensions(context.Background())
	if !strings.Contains(s.clientExtensionActivations()[0].Status, "offline") {
		t.Fatal("offline not explicit")
	}
	definition.ClientID = "manager"
	savePrivate(t, s.cfg.Private, "private:local", "extension", 0, definition)
	if len(s.clientExtensionActivations()) != 1 {
		t.Fatal("manager extension treated as client")
	}
	if s.approvedClientDefinition("missing", machineclient.ToolDefinition{ID: "private:lookup", Route: "private:lookup@2", PrivateRevision: 2}) {
		t.Fatal("wrong revision accepted")
	}
}

func TestClientArchiveRevokesNewCallsWithoutClaimingRollback(t *testing.T) {
	catalog := privateCatalog(t)
	broker := machineclient.NewBroker(machineclient.BrokerConfig{PollTimeout: 10 * time.Millisecond})
	extensionClient(t, broker, "laptop")
	s := &Server{cfg: Config{Private: catalog, Clients: broker}}
	directory := t.TempDir()
	started := directory + "/started"
	release := directory + "/release"
	definition := privateconfig.Extension{Name: "slow", ClientID: "laptop", Parameters: json.RawMessage(`{"type":"object"}`), Command: []string{"/bin/sh", "-c", "touch '" + started + "'; while [ ! -f '" + release + "' ]; do sleep 0.01; done; printf finished"}}
	savePrivate(t, catalog, "private:slow", "extension", 0, definition)
	s.syncClientExtensions(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan bool, 1)
	go func() { done <- s.executeClientTool(ctx, "laptop", "private:slow@1", json.RawMessage(`{}`)).OK }()
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("tool did not start")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := catalog.ApplyOwner(privateconfig.Change{Action: "archive", Kind: "extension", ID: "private:slow", BaseRevision: 1}); err != nil {
		t.Fatal(err)
	}
	if s.executeClientTool(ctx, "laptop", "private:slow@1", json.RawMessage(`{}`)).OK {
		t.Fatal("new archived call accepted")
	}
	syncing := make(chan struct{})
	go func() { s.syncClientExtensions(ctx); close(syncing) }()
	if err := os.WriteFile(release, []byte("release"), 0600); err != nil {
		t.Fatal(err)
	}
	if !<-done {
		t.Fatal("existing action unexpectedly claimed cancelled")
	}
	<-syncing
	if s.clientExtensionActivations()[0].Status != "archived" {
		t.Fatal("archive not propagated after action")
	}
}
