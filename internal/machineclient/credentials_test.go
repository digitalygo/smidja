package machineclient

import (
	"context"
	"encoding/json"
	"github.com/digitalygo/smidja/internal/machinetools"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func credentialBroker(t *testing.T, path string) *Broker {
	t.Helper()
	b := NewBroker(BrokerConfig{CredentialsPath: path, PollTimeout: time.Second})
	if b.Err() != nil {
		t.Fatal(b.Err())
	}
	t.Cleanup(func() { b.Close() })
	return b
}
func credentialConnect(t *testing.T, b *Broker, id, token string) int {
	return request(t, b, "POST", "/api/clients/connect", token, Registration{ProtocolVersion: 1, ClientID: id, Inventory: Inventory{OS: "linux", Arch: "amd64", Tools: []string{"read"}}}).Code
}
func pendingCredentialExecution(t *testing.T, b *Broker, token string) <-chan machinetools.Result {
	t.Helper()
	pending := make(chan machinetools.Result, 1)
	go func() { pending <- b.Execute(context.Background(), "laptop", "read", json.RawMessage(`{}`)) }()
	if response := request(t, b, "GET", "/api/clients/poll?client_id=laptop", token, nil); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	return pending
}
func TestCredentialPairRotateRevokeAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.jsonl")
	b := credentialBroker(t, path)
	pairing, err := b.PairClient("laptop", "Laptop", false)
	if err != nil || len(pairing.Token) != 64 {
		t.Fatal(pairing, err)
	}
	if credentialConnect(t, b, "laptop", pairing.Token) != 200 || credentialConnect(t, b, "laptop", "wrong") != 401 {
		t.Fatal("authentication mismatch")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if strings.Contains(string(raw), pairing.Token) || info.Mode().Perm() != 0600 {
		t.Fatal("secret or unsafe permission persisted")
	}
	if _, err = b.PairClient("laptop", "Laptop", false); err == nil {
		t.Fatal("lost-response duplicate regenerated secret")
	}
	pending := pendingCredentialExecution(t, b, pairing.Token)
	if err = b.Revoke("laptop"); err != nil {
		t.Fatal(err)
	}
	if result := <-pending; result.Code != "client_revoked" {
		t.Fatal(result)
	}
	if len(b.List()) != 1 || !b.List()[0].Revoked {
		t.Fatal(b.List())
	}
	if credentialConnect(t, b, "laptop", pairing.Token) != 401 {
		t.Fatal("revoked credential accepted")
	}
	b.Close()
	b = credentialBroker(t, path)
	if err = b.AddClient("laptop", "Old static", pairing.Token); err != nil {
		t.Fatal(err)
	}
	if credentialConnect(t, b, "laptop", pairing.Token) != 401 {
		t.Fatal("static configuration resurrected revoked ID")
	}
	rotated, err := b.PairClient("laptop", "Renamed", true)
	if err != nil || rotated.Token == pairing.Token {
		t.Fatal(rotated, err)
	}
	if credentialConnect(t, b, "laptop", rotated.Token) != 200 || credentialConnect(t, b, "laptop", pairing.Token) != 401 {
		t.Fatal("rotation did not invalidate old secret")
	}
	live, err := b.PairClient("laptop", "Live rotation", true)
	if err != nil {
		t.Fatal(err)
	}
	b.Close()
	b = credentialBroker(t, path)
	if credentialConnect(t, b, "laptop", live.Token) != 200 || credentialConnect(t, b, "laptop", rotated.Token) != 401 {
		t.Fatal("live rotation not durable")
	}
	if err = b.Revoke("laptop"); err != nil {
		t.Fatal(err)
	}
	if err = b.Revoke("laptop"); err != nil {
		t.Fatal("repeat revoke not idempotent", err)
	}
}
func TestStaticCredentialRevocationPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.jsonl")
	b := credentialBroker(t, path)
	if err := b.AddClient("static", "Static", testToken); err != nil {
		t.Fatal(err)
	}
	if _, err := b.PairClient("static", "Static", false); err == nil {
		t.Fatal("static ID collision accepted")
	}
	if err := b.Revoke("static"); err != nil {
		t.Fatal(err)
	}
	b.Close()
	b = credentialBroker(t, path)
	if err := b.AddClient("static", "Static", testToken); err != nil {
		t.Fatal(err)
	}
	if credentialConnect(t, b, "static", testToken) != 401 {
		t.Fatal("static revocation lost")
	}
}
func TestCredentialValidationFailuresAndFailClosedJournal(t *testing.T) {
	if _, err := NewBroker(BrokerConfig{}).PairClient("laptop", "Laptop", false); err == nil {
		t.Fatal("ephemeral pairing accepted")
	}
	path := filepath.Join(t.TempDir(), "credentials.jsonl")
	b := credentialBroker(t, path)
	for _, id := range []string{"", "manager", "../path", "white space"} {
		if _, err := b.PairClient(id, "Name", false); err == nil {
			t.Fatal(id)
		}
	}
	for _, name := range []string{"", strings.Repeat("x", 257)} {
		if _, err := b.PairClient("laptop", name, false); err == nil {
			t.Fatal("invalid name")
		}
	}
	if _, err := b.PairClient("missing", "Missing", true); err == nil {
		t.Fatal("rotated unknown client")
	}
	if err := b.Revoke("missing"); err == nil {
		t.Fatal("revoked unknown client")
	}
	locked := NewBroker(BrokerConfig{CredentialsPath: path})
	if locked.Err() == nil {
		t.Fatal("two journal writers")
	}
	paired, err := b.PairClient("laptop", "Laptop", false)
	if err != nil {
		t.Fatal(err)
	}
	b.credentials.file.Close()
	if err = b.Revoke("laptop"); err == nil {
		t.Fatal("journal failure ignored")
	}
	if credentialConnect(t, b, "laptop", paired.Token) != 401 {
		t.Fatal("failed journal did not fail closed")
	}
	if _, err = b.PairClient("other", "Other", false); err == nil {
		t.Fatal("pair after journal failure")
	}
	b.Close()
	b.Close()
	invalid := []string{"{", "{}\n", `{"version":1,"sequence":1,"id":"laptop","name":"Laptop","sha256":"bad"}` + "\n"}
	for _, raw := range invalid {
		badpath := filepath.Join(t.TempDir(), "bad.jsonl")
		os.WriteFile(badpath, []byte(raw), 0600)
		if candidate := NewBroker(BrokerConfig{CredentialsPath: badpath}); candidate.Err() == nil {
			candidate.Close()
			t.Fatal("invalid journal accepted")
		}
	}
	if candidate := NewBroker(BrokerConfig{CredentialsPath: "relative"}); candidate.Err() == nil {
		t.Fatal("relative journal")
	}
	link := filepath.Join(t.TempDir(), "link")
	os.Symlink(path, link)
	if candidate := NewBroker(BrokerConfig{CredentialsPath: link}); candidate.Err() == nil {
		t.Fatal("symlink journal")
	}
	encoded, _ := json.Marshal(b.List())
	if strings.Contains(string(encoded), paired.Token) {
		t.Fatal("token leaked in inventory")
	}
}
func TestBrokerCloseFailsClosedWithoutRevokingDurableIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.jsonl")
	b := credentialBroker(t, path)
	paired, err := b.PairClient("laptop", "Laptop", false)
	if err != nil {
		t.Fatal(err)
	}
	if credentialConnect(t, b, "laptop", paired.Token) != 200 {
		t.Fatal("connect")
	}
	pending := pendingCredentialExecution(t, b, paired.Token)
	if err = b.Close(); err != nil {
		t.Fatal(err)
	}
	if result := <-pending; result.OK || result.Code != "client_revoked" {
		t.Fatal(result)
	}
	if b.PairingReady() || credentialConnect(t, b, "laptop", paired.Token) != 401 {
		t.Fatal("closed broker still authenticated")
	}
	if result := b.Execute(context.Background(), "laptop", "read", json.RawMessage(`{}`)); result.Code != "broker_unavailable" {
		t.Fatal(result)
	}
	if err := b.AddClient("other", "Other", testToken); err == nil {
		t.Fatal("closed broker added client")
	}
	if err := b.Revoke("laptop"); err == nil {
		t.Fatal("closed broker mutated revocation")
	}
	if _, err := b.PairClient("laptop", "Laptop", true); err == nil {
		t.Fatal("closed broker rotated")
	}
	if err = b.Close(); err != nil {
		t.Fatal("close not idempotent", err)
	}
	reopened := credentialBroker(t, path)
	if credentialConnect(t, reopened, "laptop", paired.Token) != 200 {
		t.Fatal("shutdown permanently revoked persisted credential")
	}
}
