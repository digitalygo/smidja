package web

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/gateway"
)

type fakeGateway struct {
	mu         sync.Mutex
	submitted  []gateway.InboundMessage
	sink       gateway.DeliverySink
	submitErr  error
	cancelOK   bool
	cancelKeys []string
}

func (f *fakeGateway) Submit(ctx context.Context, msg gateway.InboundMessage) (gateway.Receipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.submitErr != nil {
		return gateway.Receipt{}, f.submitErr
	}
	f.submitted = append(f.submitted, msg)
	return gateway.Receipt{ID: msg.ID, QueuePosition: len(f.submitted) - 1}, nil
}

func (f *fakeGateway) RegisterSink(transport string, sink gateway.DeliverySink) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sink = sink
}

func (f *fakeGateway) Cancel(transport, externalChatKey string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelKeys = append(f.cancelKeys, transport+":"+externalChatKey)
	return f.cancelOK
}

func (f *fakeGateway) delivered() []gateway.InboundMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]gateway.InboundMessage, len(f.submitted))
	copy(out, f.submitted)
	return out
}

func newTestServer(t *testing.T, workspaces map[string]string) (*Server, *fakeGateway) {
	t.Helper()
	if workspaces == nil {
		workspaces = map[string]string{"demo": t.TempDir()}
	}
	fw := &fakeGateway{cancelOK: true}
	s, err := New(Config{
		ListenAddr:   "127.0.0.1:8179",
		WebTokenFunc: func() (string, error) { return "secret-token", nil },
		Gateway:      fw,
		Workspaces:   workspaces,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s, fw
}

func startTestServer(t *testing.T, workspaces map[string]string) (*Server, *fakeGateway, *httptest.Server) {
	t.Helper()
	s, fw := newTestServer(t, workspaces)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, fw, ts
}

func jarClient(t *testing.T, ts *httptest.Server) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	return &http.Client{Jar: jar, Transport: ts.Client().Transport}
}

func loginAs(t *testing.T, client *http.Client, ts *httptest.Server, token string) string {
	t.Helper()
	resp, err := client.Post(ts.URL+"/login", "application/json", strings.NewReader(`{"token":"`+token+`"}`))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		CSRF string `json:"csrf"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode login body: %v", err)
	}
	if out.CSRF == "" {
		t.Fatal("login did not issue a csrf token")
	}
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == cookieName {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("login did not set a session cookie")
	}
	return out.CSRF
}

func doJSON(t *testing.T, client *http.Client, method, url, body string, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do %s %s: %v", method, url, err)
	}
	return resp
}

func decodeError(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	var out struct {
		Error string `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out.Error
}
func TestNewDefaultsAndValidation(t *testing.T) {
	t.Setenv(webTokenEnv, "")
	fw := &fakeGateway{}
	s, err := New(Config{Gateway: fw, Workspaces: map[string]string{"w": t.TempDir()}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s.cfg.ListenAddr != defaultListen {
		t.Errorf("ListenAddr = %q, want %q", s.cfg.ListenAddr, defaultListen)
	}
	if s.cfg.WebTokenFunc == nil {
		t.Fatal("WebTokenFunc not defaulted")
	}
	token, err := s.cfg.WebTokenFunc()
	if err != nil {
		t.Fatalf("WebTokenFunc: %v", err)
	}
	if token != "" {
		t.Errorf("default token = %q, want empty", token)
	}
	if fw.sink == nil {
		t.Error("sink was not registered with the gateway")
	}
}

func TestNewEnvTokenResolution(t *testing.T) {
	t.Setenv(webTokenEnv, "env-token")
	fw := &fakeGateway{}
	s, err := New(Config{Gateway: fw, Workspaces: map[string]string{"w": t.TempDir()}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	token, err := s.cfg.WebTokenFunc()
	if err != nil {
		t.Fatalf("WebTokenFunc: %v", err)
	}
	if token != "env-token" {
		t.Errorf("token = %q, want env value", token)
	}
}

func TestNewRejects(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		cfg  Config
	}{
		{name: "nil gateway", cfg: Config{Workspaces: map[string]string{"w": dir}}},
		{name: "nil workspaces", cfg: Config{Gateway: &fakeGateway{}}},
		{name: "empty workspaces", cfg: Config{Gateway: &fakeGateway{}, Workspaces: map[string]string{}}},
		{name: "empty workspace name", cfg: Config{Gateway: &fakeGateway{}, Workspaces: map[string]string{"": dir}}},
		{name: "empty workspace root", cfg: Config{Gateway: &fakeGateway{}, Workspaces: map[string]string{"w": ""}}},
		{name: "non-loopback without flag", cfg: Config{Gateway: &fakeGateway{}, Workspaces: map[string]string{"w": dir}, ListenAddr: "0.0.0.0:8179"}},
		{name: "external ip without flag", cfg: Config{Gateway: &fakeGateway{}, Workspaces: map[string]string{"w": dir}, ListenAddr: "192.168.1.5:8179"}},
		{name: "malformed addr", cfg: Config{Gateway: &fakeGateway{}, Workspaces: map[string]string{"w": dir}, ListenAddr: "not-an-addr"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.cfg); err == nil {
				t.Error("New succeeded, want error")
			}
		})
	}
}

func TestNewAllowsNonLoopbackWithFlag(t *testing.T) {
	_, err := New(Config{
		Gateway:          &fakeGateway{},
		Workspaces:       map[string]string{"w": t.TempDir()},
		ListenAddr:       "0.0.0.0:8179",
		AllowNonLoopback: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
}

func TestStaticAssets(t *testing.T) {
	_, _, ts := startTestServer(t, nil)
	client := ts.Client()
	cases := []struct {
		path string
		want int
		ct   string
	}{
		{path: "/", want: http.StatusOK, ct: "text/html"},
		{path: "/app.js", want: http.StatusOK, ct: "javascript"},
		{path: "/missing", want: http.StatusNotFound, ct: ""},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			resp, err := client.Get(ts.URL + tc.path)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Errorf("status = %d, want %d", resp.StatusCode, tc.want)
			}
			if tc.ct != "" && !strings.Contains(resp.Header.Get("Content-Type"), tc.ct) {
				t.Errorf("content-type = %q", resp.Header.Get("Content-Type"))
			}
		})
	}
}

func TestUIScriptIsRenderSafe(t *testing.T) {
	data, err := staticFiles.ReadFile("app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	js := string(data)
	if strings.Contains(js, "innerHTML") {
		t.Error("app.js contains innerHTML assignment")
	}
	if !strings.Contains(js, "textContent") {
		t.Error("app.js does not render via textContent")
	}
	if strings.Contains(js, "document.write") {
		t.Error("app.js uses document.write")
	}
}

func TestSameOriginMatrix(t *testing.T) {
	cases := []struct {
		name    string
		host    string
		origin  string
		referer string
		want    bool
	}{
		{name: "no headers", host: "127.0.0.1:8179", want: true},
		{name: "matching origin", host: "127.0.0.1:8179", origin: "http://127.0.0.1:8179", want: true},
		{name: "matching origin case host", host: "LOCALHOST:8179", origin: "http://localhost:8179", want: true},
		{name: "evil origin", host: "127.0.0.1:8179", origin: "http://evil.example", want: false},
		{name: "port mismatch", host: "127.0.0.1:8179", origin: "http://127.0.0.1:9999", want: false},
		{name: "matching referer", host: "127.0.0.1:8179", referer: "http://127.0.0.1:8179/index.html", want: true},
		{name: "evil referer", host: "127.0.0.1:8179", referer: "http://evil.example/index.html", want: false},
		{name: "malformed origin", host: "127.0.0.1:8179", origin: "::not-a-url::", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "http://"+tc.host+"/x", nil)
			req.Host = tc.host
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.referer != "" {
				req.Header.Set("Referer", tc.referer)
			}
			if got := sameOrigin(req); got != tc.want {
				t.Errorf("sameOrigin = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSameOriginHttpsScheme(t *testing.T) {
	req := httptest.NewRequest("POST", "https://127.0.0.1:8179/x", nil)
	req.Host = "127.0.0.1:8179"
	req.TLS = &tls.ConnectionState{}
	req.Header.Set("Origin", "http://127.0.0.1:8179")
	if sameOrigin(req) {
		t.Error("http origin allowed over https app")
	}
}

func TestSameOriginHttpsAppAcceptsHttpsOrigin(t *testing.T) {
	req := httptest.NewRequest("POST", "https://127.0.0.1:8179/x", nil)
	req.Host = "127.0.0.1:8179"
	req.TLS = &tls.ConnectionState{}
	req.Header.Set("Origin", "https://127.0.0.1:8179")
	if !sameOrigin(req) {
		t.Error("https origin rejected over https app")
	}
}

func TestPruneLockedEviction(t *testing.T) {
	s, _ := newTestServer(t, nil)
	now := time.Now()
	for i := 0; i < maxSessions+16; i++ {
		s.cookies[strings.Repeat("a", 8)+strconv.Itoa(i)] = webSession{csrf: "c", expires: now.Add(time.Hour)}
	}
	s.mu.Lock()
	s.pruneLocked()
	count := len(s.cookies)
	s.mu.Unlock()
	if count != maxSessions {
		t.Errorf("cookies after prune = %d, want %d", count, maxSessions)
	}
}

func TestPruneLockedExpired(t *testing.T) {
	s, _ := newTestServer(t, nil)
	s.cookies["stale"] = webSession{csrf: "c", expires: time.Now().Add(-time.Hour)}
	s.cookies["fresh"] = webSession{csrf: "c", expires: time.Now().Add(time.Hour)}
	s.mu.Lock()
	s.pruneLocked()
	_, stale := s.cookies["stale"]
	_, fresh := s.cookies["fresh"]
	s.mu.Unlock()
	if stale {
		t.Error("expired session survived prune")
	}
	if !fresh {
		t.Error("fresh session was pruned")
	}
}

func cookieValue(t *testing.T, client *http.Client, ts *httptest.Server) string {
	t.Helper()
	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	for _, c := range client.Jar.Cookies(u) {
		if c.Name == cookieName {
			return c.Value
		}
	}
	return ""
}
