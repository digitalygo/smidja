package manager

import (
	"encoding/json"
	"github.com/digitalygo/smidja/internal/machineclient"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestOwnerPairingAuthorizationLifecycleAndSecretRedaction(t *testing.T) {
	broker := machineclient.NewBroker(machineclient.BrokerConfig{CredentialsPath: filepath.Join(t.TempDir(), "credentials.jsonl")})
	t.Cleanup(func() { broker.Close() })
	s := testServer(t, func(c *Config) { c.Clients = broker })
	body := `{"id":"laptop","name":"Laptop"}`
	for _, path := range []string{"/api/devices", "/api/devices/laptop/rotate", "/api/devices/laptop/revoke"} {
		if response := request(s, "POST", path, body, nil); response.Code != 401 {
			t.Fatal(response.Code, response.Body.String())
		}
	}
	login := request(s, "POST", "/api/login", `{"token":"`+testToken+`"}`, nil)
	cookie := login.Result().Cookies()[0]
	if response := request(s, "POST", "/api/devices", body, map[string]string{"Cookie": cookie.Name + "=" + cookie.Value}); response.Code != 403 {
		t.Fatal("missing CSRF accepted", response.Code)
	}
	response := request(s, "POST", "/api/devices", body, ownerHeaders())
	if response.Code != 201 {
		t.Fatal(response.Code, response.Body.String())
	}
	var first machineclient.Pairing
	if json.Unmarshal(response.Body.Bytes(), &first) != nil || first.Token == "" {
		t.Fatal(response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("credential response cacheable")
	}
	if response = request(s, "POST", "/api/devices", body, ownerHeaders()); response.Code != 409 || strings.Contains(response.Body.String(), first.Token) {
		t.Fatal("duplicate creation exposed or replaced secret", response.Code, response.Body.String())
	}
	listed := request(s, "GET", "/api/devices", "", ownerHeaders())
	if strings.Contains(listed.Body.String(), first.Token) || strings.Contains(listed.Body.String(), "sha256") {
		t.Fatal("credential leaked in list")
	}
	if response = request(s, "POST", "/api/devices/laptop/revoke", `{}`, ownerHeaders()); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	listed = request(s, "GET", "/api/devices", "", ownerHeaders())
	if !strings.Contains(listed.Body.String(), `"revoked":true`) {
		t.Fatal("revocation invisible", listed.Body.String())
	}
	if response = request(s, "POST", "/api/devices/laptop/rotate", `{"name":"New laptop"}`, ownerHeaders()); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	var next machineclient.Pairing
	json.Unmarshal(response.Body.Bytes(), &next)
	if next.Token == first.Token || next.Token == "" {
		t.Fatal("rotation did not produce new secret")
	}
	for _, test := range []struct {
		method, path, body string
		status             int
	}{
		{"POST", "/api/devices", `{`, 400}, {"POST", "/api/devices/laptop/rotate", `{`, 400},
		{"POST", "/api/devices/missing/rotate", `{"name":"Missing"}`, 409},
		{"POST", "/api/devices/missing/revoke", `{}`, 409}, {"POST", "/api/devices/laptop/unknown", `{}`, 404},
		{"POST", "/api/devices/laptop", `{}`, 404}, {http.MethodGet, "/api/devices/laptop/rotate", "", 404},
	} {
		if response := request(s, test.method, test.path, test.body, ownerHeaders()); response.Code != test.status {
			t.Fatal(test, response.Code, response.Body.String())
		}
	}
}
func TestOwnerPairingRequiresDurableStore(t *testing.T) {
	s := testServer(t, nil)
	if response := request(s, "POST", "/api/devices", `{"id":"laptop","name":"Laptop"}`, ownerHeaders()); response.Code != 503 {
		t.Fatal(response.Code, response.Body.String())
	}
}
