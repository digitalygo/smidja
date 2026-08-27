package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLoginFlowCookieAndCSRF(t *testing.T) {
	_, _, ts := startTestServer(t, nil)
	client := jarClient(t, ts)
	csrf := loginAs(t, client, ts, "secret-token")

	resp, err := client.Get(ts.URL + "/api/csrf")
	if err != nil {
		t.Fatalf("csrf: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("csrf status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		CSRF string `json:"csrf"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.CSRF != csrf {
		t.Errorf("csrf mismatch: got %q want %q", out.CSRF, csrf)
	}
}

func TestLoginSetsStrictHttpOnlyCookie(t *testing.T) {
	s, fw, ts := startTestServer(t, nil)
	_ = fw
	client := jarClient(t, ts)
	resp, err := client.Post(ts.URL+"/login", "application/json", strings.NewReader(`{"token":"secret-token"}`))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	cookies := resp.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %d, want 1", len(cookies))
	}
	c := cookies[0]
	if c.Name != cookieName {
		t.Errorf("cookie name = %q", c.Name)
	}
	if !c.HttpOnly {
		t.Error("cookie is not HttpOnly")
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Errorf("SameSite = %v, want Strict", c.SameSite)
	}
	if c.Path != "/" {
		t.Errorf("Path = %q, want /", c.Path)
	}
	if len(c.Value) != 64 {
		t.Errorf("session id length = %d, want 64", len(c.Value))
	}
	s.mu.Lock()
	stored, ok := s.cookies[c.Value]
	s.mu.Unlock()
	if !ok || stored.csrf == "" {
		t.Error("session missing csrf")
	}
}

func TestLoginWrongToken(t *testing.T) {
	_, _, ts := startTestServer(t, nil)
	client := jarClient(t, ts)
	resp := doJSON(t, client, "POST", ts.URL+"/login", `{"token":"wrong"}`, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
	if len(resp.Cookies()) != 0 {
		t.Error("login set a cookie on failure")
	}
}

func TestLoginTokenNotConfigured(t *testing.T) {
	fw := &fakeGateway{}
	s, err := New(Config{
		WebTokenFunc: func() (string, error) { return "", nil },
		Gateway:      fw,
		Workspaces:   map[string]string{"w": t.TempDir()},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	resp := doJSON(t, ts.Client(), "POST", ts.URL+"/login", `{"token":"anything"}`, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

func TestLoginTokenFuncError(t *testing.T) {
	fw := &fakeGateway{}
	s, err := New(Config{
		WebTokenFunc: func() (string, error) { return "", errors.New("store broken") },
		Gateway:      fw,
		Workspaces:   map[string]string{"w": t.TempDir()},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	resp := doJSON(t, ts.Client(), "POST", ts.URL+"/login", `{"token":"x"}`, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

func TestLoginBadBody(t *testing.T) {
	_, _, ts := startTestServer(t, nil)
	cases := []struct {
		name string
		body string
	}{
		{name: "invalid json", body: `{`},
		{name: "empty token", body: `{"token":""}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := doJSON(t, ts.Client(), "POST", ts.URL+"/login", tc.body, nil)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", resp.StatusCode)
			}
		})
	}
}

func TestLoginBodyTooLarge(t *testing.T) {
	_, _, ts := startTestServer(t, nil)
	big := strings.Repeat("x", maxLoginBytes+1)
	resp := doJSON(t, ts.Client(), "POST", ts.URL+"/login", `{"token":"`+big+`"}`, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", resp.StatusCode)
	}
}

func TestLoginCrossOriginRejected(t *testing.T) {
	_, _, ts := startTestServer(t, nil)
	req, err := http.NewRequest("POST", ts.URL+"/login", strings.NewReader(`{"token":"secret-token"}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Origin", "http://evil.example")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestLoginGetMethodNotAllowed(t *testing.T) {
	_, _, ts := startTestServer(t, nil)
	resp, err := ts.Client().Get(ts.URL + "/login")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestBearerPath(t *testing.T) {
	_, fw, ts := startTestServer(t, nil)
	client := ts.Client()
	headers := map[string]string{"Authorization": "Bearer secret-token"}

	resp := doJSON(t, client, "GET", ts.URL+"/api/sessions", "", headers)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("sessions status = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()

	resp = doJSON(t, client, "POST", ts.URL+"/api/send", `{"workspace":"demo","text":"hi"}`, headers)
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("send status = %d, want 202", resp.StatusCode)
	}
	resp.Body.Close()

	resp = doJSON(t, client, "GET", ts.URL+"/api/csrf", "", headers)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("csrf status = %d, want 200", resp.StatusCode)
	}
	var csrfOut struct {
		CSRF string `json:"csrf"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&csrfOut); err != nil {
		t.Fatalf("decode: %v", err)
	}
	resp.Body.Close()
	if csrfOut.CSRF != "" {
		t.Errorf("bearer csrf = %q, want empty", csrfOut.CSRF)
	}

	submitted := fw.delivered()
	if len(submitted) != 1 {
		t.Fatalf("submitted = %d, want 1", len(submitted))
	}
	m := submitted[0]
	if m.Transport != transportWeb {
		t.Errorf("transport = %q", m.Transport)
	}
	if m.ExternalChatKey == "" || m.UserIDHash == "" {
		t.Error("missing key or hash on inbound message")
	}
}

func TestBearerWrongToken(t *testing.T) {
	_, _, ts := startTestServer(t, nil)
	resp := doJSON(t, ts.Client(), "GET", ts.URL+"/api/sessions", "", map[string]string{"Authorization": "Bearer nope"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestBearerCaseInsensitive(t *testing.T) {
	_, _, ts := startTestServer(t, nil)
	resp := doJSON(t, ts.Client(), "GET", ts.URL+"/api/sessions", "", map[string]string{"Authorization": "bearer secret-token"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestAuthNegativeMatrix(t *testing.T) {
	_, _, ts := startTestServer(t, nil)
	endpoints := []struct {
		method string
		path   string
	}{
		{method: "GET", path: "/api/csrf"},
		{method: "GET", path: "/api/sessions"},
		{method: "POST", path: "/api/send"},
		{method: "GET", path: "/api/events"},
		{method: "GET", path: "/api/transcript?id=x"},
		{method: "POST", path: "/api/cancel"},
	}
	for _, ep := range endpoints {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			resp := doJSON(t, ts.Client(), ep.method, ts.URL+ep.path, "{}", nil)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", resp.StatusCode)
			}
		})
	}
	t.Run("expired session", func(t *testing.T) {
		s, _, ts := startTestServer(t, nil)
		client := jarClient(t, ts)
		loginAs(t, client, ts, "secret-token")
		for id := range s.cookies {
			s.mu.Lock()
			s.cookies[id] = webSession{csrf: s.cookies[id].csrf, expires: time.Now().Add(-time.Hour)}
			s.mu.Unlock()
		}
		resp := doJSON(t, client, "GET", ts.URL+"/api/sessions", "", nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", resp.StatusCode)
		}
	})
}

func TestExpiredSessionRejected(t *testing.T) {
	s, _, ts := startTestServer(t, nil)
	client := jarClient(t, ts)
	loginAs(t, client, ts, "secret-token")
	for id := range s.cookies {
		s.mu.Lock()
		s.cookies[id] = webSession{csrf: s.cookies[id].csrf, expires: time.Now().Add(-time.Hour)}
		s.mu.Unlock()
	}
	resp := doJSON(t, client, "GET", ts.URL+"/api/sessions", "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestHandleCSRFGhostSession(t *testing.T) {
	s, _ := newTestServer(t, nil)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/csrf", nil)
	s.handleCSRF(rr, req, authCtx{userID: "ghost"})
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

func TestAuthenticateTokenFuncError(t *testing.T) {
	fw := &fakeGateway{}
	s, err := New(Config{
		WebTokenFunc: func() (string, error) { return "", errors.New("store broken") },
		Gateway:      fw,
		Workspaces:   map[string]string{"w": t.TempDir()},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/sessions", nil)
	req.Header.Set("Authorization", "Bearer anything")
	s.authenticate(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rr.Code)
	}
}
