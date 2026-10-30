package clientextensions

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/extensiontools"
	"github.com/digitalygo/smidja/internal/machineclient"
	"github.com/digitalygo/smidja/internal/machinetools"
	"github.com/digitalygo/smidja/internal/privateconfig"
)

func newRuntime(t *testing.T) *Runtime {
	t.Helper()
	r, err := Open(Config{Dir: t.TempDir(), ClientID: "laptop", Environment: []string{"PATH=/usr/bin:/bin"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}
func control(revision uint64) machineclient.ExtensionControl {
	return machineclient.ExtensionControl{ID: "private:echo", Revision: revision, Definition: privateconfig.Extension{Name: "echo", Description: "Echo JSON", Parameters: json.RawMessage(`{"type":"object"}`), Command: []string{"/bin/cat"}, ClientID: "laptop"}}
}
func apply(r *Runtime, definition machineclient.ExtensionControl) machinetools.Result {
	raw, _ := json.Marshal(definition)
	return r.ExecuteControl(context.Background(), machineclient.ApplyExtensionControl, raw)
}
func TestPersistedVersionedControlsAndMetadata(t *testing.T) {
	r := newRuntime(t)
	first := control(1)
	if result := apply(r, first); !result.OK {
		t.Fatal(result)
	}
	if result := apply(r, first); !result.OK {
		t.Fatal("idempotent retry rejected", result)
	}
	changed := control(1)
	changed.Definition.Description = "changed"
	if result := apply(r, changed); result.Code != "revision_conflict" {
		t.Fatal(result)
	}
	inventory, err := r.Inventory(context.Background())
	if err != nil || len(inventory.ToolDefinitions) != 1 {
		t.Fatal(inventory, err)
	}
	definition := inventory.ToolDefinitions[0]
	if definition.PrivateRevision != 1 || definition.ID != first.ID || definition.Route != Route(first.ID, 1) || definition.Package != (machineclient.PackageRevision{}) {
		t.Fatal(definition)
	}
	encoded, _ := json.Marshal(inventory)
	if strings.Contains(string(encoded), "/bin/cat") || strings.Contains(string(encoded), `"command"`) || strings.Contains(string(encoded), `"environment"`) {
		t.Fatal("executable configuration exposed in inventory", string(encoded))
	}
	inventory.ToolDefinitions[0].Parameters[0] = 'x'
	fresh, _ := r.Inventory(context.Background())
	if !json.Valid(fresh.ToolDefinitions[0].Parameters) {
		t.Fatal("mutable inventory")
	}
	result := r.Execute(context.Background(), definition.Route, json.RawMessage(`{"exact":true}`))
	if !result.OK || result.Data.(extensiontools.ExecutionResult).Content != `{"exact":true}` {
		t.Fatal(result)
	}
	cfg := r.cfg
	r.Close()
	reopened, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	next := control(2)
	if result = apply(reopened, next); !result.OK {
		t.Fatal(result)
	}
	if result = apply(reopened, first); result.Code != "stale_revision" {
		t.Fatal(result)
	}
	if result = reopened.Execute(context.Background(), Route(first.ID, 1), json.RawMessage(`{}`)); result.Code != "tool_unavailable" {
		t.Fatal("old version executed", result)
	}
	archived := control(3)
	archived.Archived = true
	if result = apply(reopened, archived); !result.OK {
		t.Fatal(result)
	}
	inventory, err = reopened.Inventory(context.Background())
	if err != nil || len(inventory.ToolDefinitions) != 0 {
		t.Fatal("archived extension advertised", inventory, err)
	}
	if result = reopened.Execute(context.Background(), Route(first.ID, 2), json.RawMessage(`{}`)); result.Code != "tool_unavailable" {
		t.Fatal(result)
	}
	if result = apply(reopened, control(4)); !result.OK {
		t.Fatal("higher revision restore rejected", result)
	}
	info, err := os.Stat(filepath.Join(cfg.Dir, "extensions.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("unsafe state permissions", info, err)
	}
}
func TestConfigurationReferencesAndProtectedCredentials(t *testing.T) {
	r := newRuntime(t)
	r.cfg.LookupEnv = func(name string) string {
		switch name {
		case "TOKEN", "ALIAS":
			return "auth-secret"
		case "ODOO_KEY":
			return "explicit-tool-key"
		}
		return ""
	}
	r.cfg.ProtectedEnvironment = []string{"TOKEN"}
	definition := control(1)
	definition.Definition.Command = []string{"/bin/sh", "-c", "printf %s \"$TOOL_KEY\""}
	definition.Definition.Environment = map[string]string{"TOOL_KEY": "ODOO_KEY"}
	if result := apply(r, definition); !result.OK {
		t.Fatal(result)
	}
	result := r.Execute(context.Background(), Route(definition.ID, 1), json.RawMessage(`{}`))
	if !result.OK || result.Data.(extensiontools.ExecutionResult).Content != "explicit-tool-key" {
		t.Fatal(result)
	}
	saved, _ := os.ReadFile(filepath.Join(r.cfg.Dir, "extensions.json"))
	if strings.Contains(string(saved), "explicit-tool-key") || strings.Contains(string(saved), "auth-secret") {
		t.Fatal("credential value persisted")
	}
	for index, reference := range []string{"MISSING", "TOKEN", "ALIAS"} {
		definition.Revision = uint64(index + 2)
		definition.Definition.Environment = map[string]string{"TOOL_KEY": reference}
		if result = apply(r, definition); !result.OK {
			t.Fatal("missing config blocked activation", result)
		}
		result = r.Execute(context.Background(), Route(definition.ID, definition.Revision), json.RawMessage(`{}`))
		expected := "protected_configuration"
		if reference == "MISSING" {
			expected = "missing_configuration"
		}
		if result.Code != expected || strings.Contains(result.Error, "auth-secret") {
			t.Fatal(result)
		}
	}
}
func TestControlsValidateTargetContextAndPersistence(t *testing.T) {
	r := newRuntime(t)
	for _, mutation := range []func(*machineclient.ExtensionControl){func(c *machineclient.ExtensionControl) { c.ID = "package/tool" }, func(c *machineclient.ExtensionControl) { c.Revision = 0 }, func(c *machineclient.ExtensionControl) { c.Definition.ClientID = "desktop" }, func(c *machineclient.ExtensionControl) { c.Definition.Name = "" }, func(c *machineclient.ExtensionControl) { c.Definition.Command = nil }, func(c *machineclient.ExtensionControl) { c.Definition.Parameters = json.RawMessage(`null`) }, func(c *machineclient.ExtensionControl) {
		c.Definition.Environment = map[string]string{"NAME": "literal-key-with-hyphen"}
	}} {
		definition := control(1)
		mutation(&definition)
		if result := apply(r, definition); result.Code != "invalid_control" {
			t.Fatal(result)
		}
	}
	for _, raw := range []string{`{`, `{} {}`, `{"unknown":1}`, strings.Repeat(" ", 2<<20+1)} {
		if result := r.ExecuteControl(context.Background(), machineclient.ApplyExtensionControl, json.RawMessage(raw)); result.Code != "invalid_control" {
			t.Fatal(result)
		}
	}
	if result := r.ExecuteControl(context.Background(), "other", json.RawMessage(`{}`)); result.Code != "invalid_control" {
		t.Fatal(result)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result := r.ExecuteControl(ctx, machineclient.ApplyExtensionControl, json.RawMessage(`{}`)); result.Code != "cancelled" {
		t.Fatal(result)
	}
	if result := r.Execute(context.Background(), machineclient.ApplyExtensionControl, json.RawMessage(`{}`)); result.Code != "tool_unavailable" {
		t.Fatal("public control bypass", result)
	}
	if err := os.Mkdir(filepath.Join(r.cfg.Dir, "extensions.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if result := apply(r, control(1)); result.Code != "persist_failed" {
		t.Fatal(result)
	}
	if result := apply(r, control(1)); result.Code != "closed" {
		t.Fatal("uncertain persistence retried without reopen", result)
	}
	if _, err := r.Inventory(context.Background()); err == nil {
		t.Fatal("uncertain persisted configuration advertised")
	}
	r.Close()
	r.Close()
	if result := r.Execute(context.Background(), Route("private:echo", 1), json.RawMessage(`{}`)); result.Code != "closed" {
		t.Fatal(result)
	}
}
func TestStateBindingCorruptionAndBaseInventory(t *testing.T) {
	r := newRuntime(t)
	if _, err := Open(r.cfg); err == nil {
		t.Fatal("two writers")
	}
	if result := apply(r, control(1)); !result.OK {
		t.Fatal(result)
	}
	cfg := r.cfg
	r.Close()
	cfg.ClientID = "desktop"
	if _, err := Open(cfg); err == nil {
		t.Fatal("state rebound to another client")
	}
	cfg.ClientID = "laptop"
	os.WriteFile(filepath.Join(cfg.Dir, "extensions.json"), []byte(`{`), 0600)
	if _, err := Open(cfg); err == nil {
		t.Fatal("corrupt state accepted")
	}
	os.Remove(filepath.Join(cfg.Dir, "extensions.json"))
	os.Symlink("/nonexistent", filepath.Join(cfg.Dir, "extensions.json"))
	if _, err := Open(cfg); err == nil {
		t.Fatal("symlink state accepted")
	}
	for _, bad := range []Config{{Dir: "relative", ClientID: "laptop"}, {Dir: t.TempDir(), ClientID: "manager"}, {Dir: t.TempDir()}} {
		if _, err := Open(bad); err == nil {
			t.Fatal("bad config accepted")
		}
	}
	base := newRuntime(t)
	base.cfg.InventoryProvider = func(context.Context) (machineclient.Inventory, error) {
		return machineclient.Inventory{}, errors.New("inventory offline")
	}
	if _, err := base.Inventory(context.Background()); err == nil {
		t.Fatal("base inventory error hidden")
	}
	base.cfg.InventoryProvider = func(context.Context) (machineclient.Inventory, error) {
		return machineclient.Inventory{OS: "linux", Arch: "amd64", Tools: []string{"custom"}}, nil
	}
	if result := apply(base, control(1)); !result.OK {
		t.Fatal(result)
	}
	inventory, err := base.Inventory(context.Background())
	if err != nil || len(inventory.Tools) != 2 || inventory.Tools[0] != "custom" {
		t.Fatal(inventory, err)
	}
	file := filepath.Join(t.TempDir(), "file")
	os.WriteFile(file, []byte("base"), 0600)
	args, _ := json.Marshal(map[string]string{"path": file})
	if result := base.Execute(context.Background(), "read", args); !result.OK {
		t.Fatal(result)
	}
}
func TestArchiveCancelsAlreadyRunningExtension(t *testing.T) {
	r := newRuntime(t)
	definition := control(1)
	marker := filepath.Join(t.TempDir(), "started")
	definition.Definition.Command = []string{"/bin/sh", "-c", "touch \"$1\"; sleep 5", "helper", marker}
	if result := apply(r, definition); !result.OK {
		t.Fatal(result)
	}
	done := make(chan machinetools.Result, 1)
	go func() { done <- r.Execute(context.Background(), Route(definition.ID, 1), json.RawMessage(`{}`)) }()
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("helper not started")
		}
		time.Sleep(time.Millisecond)
	}
	definition.Revision = 2
	definition.Archived = true
	if result := apply(r, definition); !result.OK {
		t.Fatal(result)
	}
	select {
	case result := <-done:
		if result.Code != "cancelled" {
			t.Fatal(result)
		}
	case <-time.After(time.Second):
		t.Fatal("archive did not stop active extension")
	}
}
func TestAuthenticatedControlAndPrivateToolEndToEnd(t *testing.T) {
	r := newRuntime(t)
	broker := machineclient.NewBroker(machineclient.BrokerConfig{PollTimeout: 10 * time.Millisecond, ExecutionTimeout: time.Second})
	const token = "test-client-secret-at-least-16"
	if err := broker.AddClient("laptop", "Laptop", token); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(broker)
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- machineclient.Run(ctx, machineclient.Config{ManagerURL: server.URL, ClientID: "laptop", Name: "Laptop", Token: token, HeartbeatInterval: 5 * time.Millisecond, InventoryProvider: r.Inventory}, r)
	}()
	deadline := time.Now().Add(time.Second)
	for !broker.List()[0].Online {
		if time.Now().After(deadline) {
			t.Fatal("client not connected")
		}
		time.Sleep(time.Millisecond)
	}
	if result := broker.Execute(ctx, "laptop", machineclient.ApplyExtensionControl, json.RawMessage(`{}`)); result.Code != "tool_unavailable" {
		t.Fatal("public broker executed control", result)
	}
	definition := control(1)
	if result := broker.ApplyExtension(ctx, "laptop", definition); !result.OK {
		t.Fatal(result)
	}
	inventory := broker.List()[0].Inventory
	if len(inventory.ToolDefinitions) != 1 || inventory.ToolDefinitions[0].PrivateRevision != 1 {
		t.Fatal("receipt preceded inventory synchronization", inventory)
	}
	if result := broker.ApplyExtension(ctx, "laptop", definition); !result.OK {
		t.Fatal("same revision control replay failed", result)
	}
	if result := broker.Execute(ctx, "laptop", Route(definition.ID, 1), json.RawMessage(`{"hello":"client"}`)); !result.OK {
		t.Fatal(result)
	}
	definition.Revision = 2
	definition.Archived = true
	if result := broker.ApplyExtension(ctx, "laptop", definition); !result.OK {
		t.Fatal(result)
	}
	if result := broker.Execute(ctx, "laptop", Route(definition.ID, 1), json.RawMessage(`{}`)); result.Code != "tool_unavailable" {
		t.Fatal("archived route advertised", result)
	}
	bad := control(3)
	bad.Definition.ClientID = "desktop"
	if result := broker.ApplyExtension(ctx, "laptop", bad); result.Code != "invalid_arguments" {
		t.Fatal(result)
	}
	unauth, err := http.Get(server.URL + "/api/clients/poll?client_id=laptop")
	if err != nil {
		t.Fatal(err)
	}
	unauth.Body.Close()
	if unauth.StatusCode != 401 {
		t.Fatal("control queue exposed without auth")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("client did not stop")
	}
}
