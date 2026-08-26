package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/authstore"
)

func TestCodexLogin(t *testing.T) {
	challengeCh := make(chan string, 1)
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if base64.RawURLEncoding.EncodeToString(sum[:]) != <-challengeCh {
			t.Errorf("PKCE verifier does not match challenge")
		}
		if r.PostForm.Get("code") != "cx-1" {
			t.Errorf("exchange code = %q", r.PostForm.Get("code"))
		}
		if r.PostForm.Get("grant_type") != "authorization_code" {
			t.Errorf("grant_type = %q", r.PostForm.Get("grant_type"))
		}
		w.Header().Set("content-type", "application/json")
		fmt.Fprint(w, codexTokenJSON("acc-123"))
	}))
	defer tokenServer.Close()

	browserURL := make(chan string, 1)
	result := make(chan loginResult, 1)
	go func() {
		entry, err := CodexLogin(context.Background(), Options{
			TokenURL:     tokenServer.URL,
			CallbackPort: EphemeralCallbackPort,
			OpenBrowser:  func(u string) error { browserURL <- u; return nil },
		})
		result <- loginResult{entry: entry, err: err}
	}()

	authorizeURL := waitForBrowser(t, browserURL)
	parsed, err := url.Parse(authorizeURL)
	if err != nil {
		t.Fatalf("parse authorize URL: %v", err)
	}
	query := parsed.Query()
	challengeCh <- query.Get("code_challenge")
	state := query.Get("state")
	redirectURI := query.Get("redirect_uri")
	if query.Get("client_id") != codexClientID {
		t.Errorf("client_id = %q", query.Get("client_id"))
	}
	if query.Get("response_type") != "code" {
		t.Errorf("response_type = %q", query.Get("response_type"))
	}
	if query.Get("scope") != codexScope {
		t.Errorf("scope = %q", query.Get("scope"))
	}
	if query.Get("id_token_add_organizations") != "true" {
		t.Errorf("id_token_add_organizations = %q", query.Get("id_token_add_organizations"))
	}
	if query.Get("codex_cli_simplified_flow") != "true" {
		t.Errorf("codex_cli_simplified_flow = %q", query.Get("codex_cli_simplified_flow"))
	}
	if query.Get("originator") != "pi" {
		t.Errorf("originator = %q", query.Get("originator"))
	}
	if !strings.HasPrefix(redirectURI, "http://localhost:") || !strings.HasSuffix(redirectURI, "/auth/callback") {
		t.Errorf("redirect_uri = %q", redirectURI)
	}
	ru, _ := url.Parse(redirectURI)
	callbackURL := "http://127.0.0.1:" + ru.Port() + "/auth/callback"
	resp, err := http.Get(callbackURL + "?code=cx-1&state=" + url.QueryEscape(state))
	if err != nil {
		t.Fatalf("callback request: %v", err)
	}
	resp.Body.Close()

	res := awaitResult(t, result)
	if res.err != nil {
		t.Fatalf("login: %v", res.err)
	}
	assertCodexEntry(t, res.entry, "acc-123")
}

func assertCodexEntry(t *testing.T, entry authstore.Entry, wantAccountID string) {
	t.Helper()
	if entry.Type != "oauth" || entry.Access == "" || entry.Refresh == "" {
		t.Errorf("entry = %+v", entry)
	}
	body, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal entry: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal entry: %v", err)
	}
	if parsed["accountId"] != wantAccountID {
		t.Errorf("entry missing accountId %q: %s", wantAccountID, body)
	}
}

func TestCodexLoginStateMismatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	browserURL := make(chan string, 1)
	result := make(chan error, 1)
	go func() {
		_, err := CodexLogin(ctx, Options{
			CallbackPort: EphemeralCallbackPort,
			OpenBrowser:  func(u string) error { browserURL <- u; return nil },
		})
		result <- err
	}()
	authorizeURL := waitForBrowser(t, browserURL)
	parsed, _ := url.Parse(authorizeURL)
	ru, _ := url.Parse(parsed.Query().Get("redirect_uri"))
	callbackURL := "http://127.0.0.1:" + ru.Port() + "/auth/callback"
	resp, err := http.Get(callbackURL + "?code=c&state=wrong")
	if err != nil {
		t.Fatalf("callback request: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(string(body), "State mismatch.") {
		t.Errorf("body = %s", body)
	}
	cancel()
	err = <-result
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want cancelled", err)
	}
}

func TestCodexLoginListenerFailureFallsBackToManual(t *testing.T) {
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer blocker.Close()
	port := blocker.Addr().(*net.TCPAddr).Port
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.PostForm.Get("code") != "manual-codex" {
			t.Errorf("exchange code = %q", r.PostForm.Get("code"))
		}
		w.Header().Set("content-type", "application/json")
		fmt.Fprint(w, codexTokenJSON("acc-manual"))
	}))
	defer tokenServer.Close()

	result := make(chan loginResult, 1)
	go func() {
		entry, err := CodexLogin(context.Background(), Options{
			TokenURL:     tokenServer.URL,
			CallbackPort: port,
			OpenBrowser:  func(u string) error { return nil },
			ManualCode: func(ctx context.Context, prompt string) (string, error) {
				return "code=manual-codex", nil
			},
		})
		result <- loginResult{entry: entry, err: err}
	}()
	res := awaitResult(t, result)
	if res.err != nil {
		t.Fatalf("login: %v", res.err)
	}
	assertCodexEntry(t, res.entry, "acc-manual")
}

func TestCodexLoginManualStateMismatch(t *testing.T) {
	browserURL := make(chan string, 1)
	result := make(chan error, 1)
	go func() {
		_, err := CodexLogin(context.Background(), Options{
			CallbackPort: EphemeralCallbackPort,
			OpenBrowser:  func(u string) error { browserURL <- u; return nil },
			ManualCode: func(ctx context.Context, prompt string) (string, error) {
				return "code=c&state=wrong", nil
			},
		})
		result <- err
	}()
	waitForBrowser(t, browserURL)
	err := <-result
	if err == nil || !strings.Contains(err.Error(), "State mismatch") {
		t.Errorf("err = %v", err)
	}
}

func TestCodexRefresh(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.PostForm.Get("grant_type") != "refresh_token" || r.PostForm.Get("refresh_token") != "rt-old" {
			t.Errorf("refresh body = %v", r.PostForm)
		}
		w.Header().Set("content-type", "application/json")
		fmt.Fprint(w, codexTokenJSON("acc-new"))
	}))
	defer tokenServer.Close()

	entry, err := CodexRefresh(context.Background(),
		authstore.Entry{Type: "oauth", Access: "old", Refresh: "rt-old", Expires: 1},
		Options{TokenURL: tokenServer.URL})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	assertCodexEntry(t, entry, "acc-new")
}

func TestCodexTokenResponseMissingFields(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"access_token":"a"}`)
	}))
	defer tokenServer.Close()
	_, err := exchangeCodexCode(context.Background(), "c", "v", "http://localhost/x", tokenServer.URL)
	if err == nil || !strings.Contains(err.Error(), "missing fields") {
		t.Errorf("err = %v", err)
	}
}

func TestCodexTokenResponseHTTPError(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"bad"}`)
	}))
	defer tokenServer.Close()
	_, err := exchangeCodexCode(context.Background(), "c", "v", "http://localhost/x", tokenServer.URL)
	if err == nil || !strings.Contains(err.Error(), "exchange failed (401)") {
		t.Errorf("err = %v", err)
	}
}

func TestCodexDeviceLogin(t *testing.T) {
	var polls atomic.Int32
	var base *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/api/accounts/deviceauth/usercode", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		fmt.Fprint(w, `{"device_auth_id":"dev-1","user_code":"CODE-1","interval":"1"}`)
	})
	mux.HandleFunc("/api/accounts/deviceauth/token", func(w http.ResponseWriter, r *http.Request) {
		polls.Add(1)
		w.Header().Set("content-type", "application/json")
		if polls.Load() == 1 {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"error":"deviceauth_authorization_pending"}`)
			return
		}
		fmt.Fprint(w, `{"authorization_code":"auth-1","code_verifier":"device-verifier"}`)
	})
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.PostForm.Get("code") != "auth-1" {
			t.Errorf("exchange code = %q", r.PostForm.Get("code"))
		}
		if r.PostForm.Get("code_verifier") != "device-verifier" {
			t.Errorf("exchange verifier = %q", r.PostForm.Get("code_verifier"))
		}
		if r.PostForm.Get("redirect_uri") != base.URL+"/deviceauth/callback" {
			t.Errorf("device redirect = %q", r.PostForm.Get("redirect_uri"))
		}
		w.Header().Set("content-type", "application/json")
		fmt.Fprint(w, codexTokenJSON("acc-dev"))
	})
	base = httptest.NewServer(mux)
	defer base.Close()

	var notified DeviceCode
	result := make(chan loginResult, 1)
	go func() {
		entry, err := CodexDeviceLogin(context.Background(), Options{
			AuthBaseURL: base.URL,
			DeviceCode:  func(d DeviceCode) { notified = d },
		})
		result <- loginResult{entry: entry, err: err}
	}()
	res := awaitResult(t, result)
	if res.err != nil {
		t.Fatalf("login: %v", res.err)
	}
	if polls.Load() != 2 {
		t.Errorf("polls = %d, want 2", polls.Load())
	}
	if notified.UserCode != "CODE-1" {
		t.Errorf("notified user code = %q", notified.UserCode)
	}
	if notified.VerificationURI != base.URL+"/codex/device" {
		t.Errorf("verification URI = %q", notified.VerificationURI)
	}
	if notified.IntervalSeconds != 1 || notified.ExpiresInSeconds != codexDeviceTimeoutSeconds {
		t.Errorf("notified = %+v", notified)
	}
	assertCodexEntry(t, res.entry, "acc-dev")
}

func TestCodexDeviceAuthNotEnabled(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer tokenServer.Close()
	_, err := startCodexDeviceAuth(context.Background(), codexEndpoints{deviceCode: tokenServer.URL})
	if err == nil || !strings.Contains(err.Error(), "not enabled") {
		t.Errorf("err = %v", err)
	}
}

func TestCodexDeviceAuthInvalidResponse(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"device_auth_id":"d"}`)
	}))
	defer tokenServer.Close()
	_, err := startCodexDeviceAuth(context.Background(), codexEndpoints{deviceCode: tokenServer.URL})
	if err == nil || !strings.Contains(err.Error(), "invalid openai codex device code response") {
		t.Errorf("err = %v", err)
	}
}

func TestCodexIntervalSeconds(t *testing.T) {
	cases := []struct {
		name string
		body map[string]any
		want float64
	}{
		{"number", map[string]any{"interval": 2.0}, 2},
		{"string", map[string]any{"interval": "3"}, 3},
		{"zero", map[string]any{"interval": 0.0}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := codexIntervalSeconds(tc.body)
			if err != nil {
				t.Fatalf("codexIntervalSeconds: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
	bad := []map[string]any{{}, {"interval": "abc"}, {"interval": -1.0}}
	for _, body := range bad {
		if _, err := codexIntervalSeconds(body); err == nil {
			t.Errorf("codexIntervalSeconds(%v): want error", body)
		}
	}
}

func TestPollCodexDeviceAuth(t *testing.T) {
	cases := []struct {
		name       string
		response   string
		status     int
		wantStatus deviceStatus
	}{
		{"pending string", `{"error":"deviceauth_authorization_pending"}`, http.StatusBadRequest, devicePending},
		{"pending object", `{"error":{"code":"deviceauth_authorization_pending"}}`, http.StatusBadRequest, devicePending},
		{"forbidden", `{}`, http.StatusForbidden, devicePending},
		{"not found", `{}`, http.StatusNotFound, devicePending},
		{"slow down string", `{"error":"slow_down"}`, http.StatusBadRequest, deviceSlowDown},
		{"slow down object", `{"error":{"code":"slow_down"}}`, http.StatusBadRequest, deviceSlowDown},
		{"failed", `{"error":"other"}`, http.StatusBadRequest, deviceFailed},
		{"complete", `{"authorization_code":"a","code_verifier":"v"}`, 200, deviceComplete},
		{"incomplete", `{"authorization_code":"a"}`, 200, deviceFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.response)
			}))
			defer tokenServer.Close()
			outcome, err := pollCodexDeviceAuth(context.Background(), codexEndpoints{deviceToken: tokenServer.URL}, codexDevice{deviceAuthID: "d", userCode: "u"})
			if err != nil {
				t.Fatalf("poll: %v", err)
			}
			if outcome.status != tc.wantStatus {
				t.Errorf("status = %v, want %v (message %q)", outcome.status, tc.wantStatus, outcome.message)
			}
			if tc.wantStatus == deviceComplete {
				value := outcome.value.(codexPollValue)
				if value.authorizationCode != "a" || value.codeVerifier != "v" {
					t.Errorf("value = %+v", value)
				}
			}
		})
	}
}

func TestCodexErrorCode(t *testing.T) {
	if got := codexErrorCode([]byte(`{"error":"abc"}`)); got != "abc" {
		t.Errorf("string error = %q", got)
	}
	if got := codexErrorCode([]byte(`{"error":{"code":"xyz"}}`)); got != "xyz" {
		t.Errorf("object error = %q", got)
	}
	if got := codexErrorCode([]byte(`nope`)); got != "" {
		t.Errorf("invalid body error = %q", got)
	}
}

func TestCodexDeviceLoginExpires(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		fmt.Fprint(w, `{"device_auth_id":"d","user_code":"u","interval":1}`)
	}))
	defer server.Close()
	_, err := CodexDeviceLogin(ctx, Options{DeviceCodeURL: server.URL})
	if err == nil {
		t.Fatal("want error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want DeadlineExceeded", err)
	}
}
