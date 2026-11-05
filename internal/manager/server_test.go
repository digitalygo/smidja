package manager

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/managerstate"
)

const testToken = "test-manager-token-with-32-characters"

func testServer(t *testing.T, configure func(*Config)) *Server {
	t.Helper()
	store, err := managerstate.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{Token: testToken, Store: store}
	if configure != nil {
		configure(&cfg)
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(); store.Close() })
	return s
}
func request(s *Server, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}
func ownerHeaders() map[string]string {
	return map[string]string{"Authorization": "Bearer " + testToken}
}
func TestAuthentication(t *testing.T) {
	s := testServer(t, nil)
	for _, tc := range []struct {
		method, path, body string
		headers            map[string]string
		status             int
	}{
		{"GET", "/", "", nil, 200}, {"GET", "/missing", "", nil, 404}, {"GET", "/api/status", "", nil, 401},
		{"POST", "/api/login", `{"token":"wrong"}`, nil, 401}, {"POST", "/api/login", `{}`, nil, 401},
		{"POST", "/api/login", `{"token":"` + testToken + `","extra":1}`, nil, 400},
		{"POST", "/api/login", `{} {}`, nil, 400},
		{"POST", "/api/login", `{"token":"` + testToken + `"}`, map[string]string{"Origin": "https://evil.test"}, 403},
		{"GET", "/api/status", "", ownerHeaders(), 200}, {"GET", "/api/status", "", map[string]string{"Authorization": "Bearer " + testToken, "Sec-Fetch-Site": "cross-site"}, 403},
	} {
		w := request(s, tc.method, tc.path, tc.body, tc.headers)
		if w.Code != tc.status {
			t.Errorf("%s %s => %d: %s", tc.method, tc.path, w.Code, w.Body)
		}
	}
	login := request(s, "POST", "/api/login", `{"token":"`+testToken+`"}`, nil)
	if login.Code != 200 {
		t.Fatal(login.Body)
	}
	var data map[string]string
	json.Unmarshal(login.Body.Bytes(), &data)
	cookies := login.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal(cookies)
	}
	headers := map[string]string{"Cookie": cookies[0].String()}
	if w := request(s, "POST", "/api/sessions", `{"title":"hello"}`, headers); w.Code != 403 {
		t.Fatal(w.Code)
	}
	headers["X-CSRF"] = data["csrf"]
	if w := request(s, "POST", "/api/sessions", `{"title":"hello"}`, headers); w.Code != 201 {
		t.Fatal(w.Body)
	}
	if w := request(s, "POST", "/api/logout", `{}`, headers); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if w := request(s, "GET", "/api/status", "", headers); w.Code != 401 {
		t.Fatal(w.Body)
	}
}
func TestLocalRoutes(t *testing.T) {
	s := testServer(t, nil)
	for _, path := range []string{"/api/sessions", "/api/tasks", "/api/devices", "/api/tools"} {
		if w := request(s, "GET", path, "", ownerHeaders()); w.Code != 200 {
			t.Fatal(path, w.Body)
		}
	}
	for _, path := range []string{"/api/teams", "/api/capabilities"} {
		if w := request(s, "GET", path, "", ownerHeaders()); w.Code != 503 {
			t.Fatal(path, w.Body)
		}
	}
	if w := request(s, "POST", "/api/tasks", `{}`, ownerHeaders()); w.Code != 503 {
		t.Fatal(w.Body)
	}
	if w := request(s, "POST", "/api/tools/execute", `{"name":"nope","args":{}}`, ownerHeaders()); w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"ok":false`)) {
		t.Fatal(w.Body)
	}
	for _, path := range []string{"/api/missing", "/api/sessions/nope", "/api/tasks/nope"} {
		if w := request(s, "GET", path, "", ownerHeaders()); w.Code != 404 {
			t.Fatal(path, w.Body)
		}
	}
	if w := request(s, "POST", "/api/sessions", `{"title":"`+strings.Repeat("x", 9000)+`"}`, ownerHeaders()); w.Code != 400 {
		t.Fatal(w.Body)
	}
}
