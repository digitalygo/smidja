package machineclient

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInventorySurvivesRestartWithoutCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "clients.json")
	b := NewBroker(BrokerConfig{InventoryPath: path})
	if b.Err() != nil {
		t.Fatal(b.Err())
	}
	register(t, b, "laptop")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), testToken) || strings.Contains(string(raw), "token") {
		t.Fatal("credentials leaked")
	}
	stat, _ := os.Stat(path)
	if stat.Mode().Perm() != 0600 {
		t.Fatal(stat.Mode())
	}
	restored := NewBroker(BrokerConfig{InventoryPath: path})
	if restored.Err() != nil {
		t.Fatal(restored.Err())
	}
	statuses := restored.List()
	if len(statuses) != 1 || statuses[0].Online || len(statuses[0].Inventory.Tools) != 2 {
		t.Fatal(statuses)
	}
	if r := request(t, restored, "GET", "/api/clients/poll?client_id=laptop", testToken, nil); r.Code != 401 {
		t.Fatal(r.Code)
	}
	if err := restored.AddClient("laptop", "Laptop", testToken); err != nil {
		t.Fatal(err)
	}
	if restored.List()[0].Online {
		t.Fatal("restored as online")
	}
	if r := restored.Execute(context.Background(), "laptop", "read", json.RawMessage(`{}`)); r.Code != "client_offline" {
		t.Fatal(r)
	}
	if r := request(t, restored, "GET", "/api/clients/poll?client_id=laptop", testToken, nil); r.Code != 409 {
		t.Fatal(r.Code)
	}
	if err := restored.Revoke("laptop"); err != nil {
		t.Fatal(err)
	}
	if len(NewBroker(BrokerConfig{InventoryPath: path}).List()) != 0 {
		t.Fatal("revocation not persisted")
	}
	if err := b.Revoke("missing"); err != nil {
		t.Fatal(err)
	}
}

func TestInventoryErrorsFailClosed(t *testing.T) {
	for _, value := range []string{`{`, `[{"id":""}]`, `[{"id":"x"},{"id":"x"}]`, strings.Repeat("x", 16<<20+1)} {
		path := filepath.Join(t.TempDir(), "inventory.json")
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		b := NewBroker(BrokerConfig{InventoryPath: path})
		if b.Err() == nil {
			t.Fatal("accepted invalid snapshot")
		}
		if err := b.AddClient("laptop", "Laptop", testToken); err == nil {
			t.Fatal("registered after failed load")
		}
	}
	dir := t.TempDir()
	b := NewBroker(BrokerConfig{InventoryPath: dir})
	if b.Err() == nil {
		t.Fatal("read directory")
	}
	path := filepath.Join(t.TempDir(), "snapshot")
	b = NewBroker(BrokerConfig{InventoryPath: path})
	if err := b.AddClient("laptop", "Laptop", testToken); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	r := request(t, b, http.MethodPost, "/api/clients/connect", testToken, Registration{ProtocolVersion: 1, ClientID: "laptop", Inventory: Inventory{OS: "linux", Arch: "amd64"}})
	if r.Code != 500 || b.List()[0].Online {
		t.Fatal(r.Code, b.List())
	}
}
