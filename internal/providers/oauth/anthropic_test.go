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
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/authstore"
)

func TestAnthropicLogin(t *testing.T) {
	challengeCh := make(chan string, 1)
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		verifier, _ := body["code_verifier"].(string)
		sum := sha256.Sum256([]byte(verifier))
		if base64.RawURLEncoding.EncodeToString(sum[:]) != <-challengeCh {
			t.Errorf("PKCE verifier does not match challenge")
		}
		if body["state"] != verifier {
			t.Errorf("state = %v, want verifier", body["state"])
		}
		if body["grant_type"] != "authorization_code" {
			t.Errorf("grant_type = %v", body["grant_type"])
		}
		if body["client_id"] != anthropicClientID {
			t.Errorf("client_id = %v", body["client_id"])
		}
		if body["code"] != "ac-1" {
			t.Errorf("exchange code = %v", body["code"])
		}
		w.Header().Set("content-type", "application/json")
		fmt.Fprint(w, `{"access_token":"at-1","refresh_token":"rt-1","expires_in":3600}`)
	}))
	defer tokenServer.Close()

	browserURL := make(chan string, 1)
	result := make(chan loginResult, 1)
	go func() {
		entry, err := AnthropicLogin(context.Background(), Options{
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
	challenge := parsed.Query().Get("code_challenge")
	state := parsed.Query().Get("state")
	redirectURI := parsed.Query().Get("redirect_uri")
	challengeCh <- challenge
	if parsed.Query().Get("client_id") != anthropicClientID {
		t.Errorf("authorize client_id = %q", parsed.Query().Get("client_id"))
	}
	if parsed.Query().Get("response_type") != "code" {
		t.Errorf("response_type = %q", parsed.Query().Get("response_type"))
	}
	if parsed.Query().Get("code") != "true" {
		t.Errorf("authorize code param = %q", parsed.Query().Get("code"))
	}
	if parsed.Query().Get("scope") != anthropicScopes {
		t.Errorf("scope = %q", parsed.Query().Get("scope"))
	}
	if parsed.Query().Get("code_challenge_method") != "S256" {
		t.Errorf("code_challenge_method = %q", parsed.Query().Get("code_challenge_method"))
	}
	if state == "" || challenge == "" {
		t.Fatalf("missing state/challenge: %q %q", state, challenge)
	}
	ru, err := url.Parse(redirectURI)
	if err != nil {
		t.Fatalf("parse redirect URI: %v", err)
	}
	callbackURL := "http://127.0.0.1:" + ru.Port() + "/callback"
	resp, err := http.Get(callbackURL + "?code=ac-1&state=" + url.QueryEscape(state))
	if err != nil {
		t.Fatalf("callback request: %v", err)
	}
	resp.Body.Close()

	res := awaitResult(t, result)
	if res.err != nil {
		t.Fatalf("login: %v", res.err)
	}
	if res.entry.Access != "at-1" || res.entry.Refresh != "rt-1" {
		t.Errorf("entry = %+v", res.entry)
	}
	expected := time.Now().UnixMilli() + 3600*1000 - int64(refreshSkew/time.Millisecond)
	if diff := res.entry.Expires - expected; diff > 5000 || diff < -5000 {
		t.Errorf("expires = %d, want ~%d", res.entry.Expires, expected)
	}
}

func TestAnthropicLoginStateMismatchRejected(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	browserURL := make(chan string, 1)
	result := make(chan error, 1)
	go func() {
		_, err := AnthropicLogin(ctx, Options{
			CallbackPort: EphemeralCallbackPort,
			OpenBrowser:  func(u string) error { browserURL <- u; return nil },
		})
		result <- err
	}()
	authorizeURL := waitForBrowser(t, browserURL)
	parsed, _ := url.Parse(authorizeURL)
	ru, _ := url.Parse(parsed.Query().Get("redirect_uri"))
	callbackURL := "http://127.0.0.1:" + ru.Port() + "/callback"
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
		t.Errorf("err = %v, want cancelled (login must keep waiting)", err)
	}
}

func TestAnthropicLoginMissingState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	browserURL := make(chan string, 1)
	result := make(chan error, 1)
	go func() {
		_, err := AnthropicLogin(ctx, Options{
			CallbackPort: EphemeralCallbackPort,
			OpenBrowser:  func(u string) error { browserURL <- u; return nil },
		})
		result <- err
	}()
	authorizeURL := waitForBrowser(t, browserURL)
	parsed, _ := url.Parse(authorizeURL)
	ru, _ := url.Parse(parsed.Query().Get("redirect_uri"))
	callbackURL := "http://127.0.0.1:" + ru.Port() + "/callback"
	resp, err := http.Get(callbackURL + "?code=c")
	if err != nil {
		t.Fatalf("callback request: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Missing code or state parameter.") {
		t.Errorf("body = %s", body)
	}
	cancel()
	err = <-result
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want cancelled", err)
	}
}

func TestAnthropicLoginDeniedKeepsWaiting(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	browserURL := make(chan string, 1)
	result := make(chan error, 1)
	go func() {
		_, err := AnthropicLogin(ctx, Options{
			CallbackPort: EphemeralCallbackPort,
			OpenBrowser:  func(u string) error { browserURL <- u; return nil },
		})
		result <- err
	}()
	authorizeURL := waitForBrowser(t, browserURL)
	parsed, _ := url.Parse(authorizeURL)
	ru, _ := url.Parse(parsed.Query().Get("redirect_uri"))
	callbackURL := "http://127.0.0.1:" + ru.Port() + "/callback"
	resp, err := http.Get(callbackURL + "?error=access_denied")
	if err != nil {
		t.Fatalf("callback request: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(string(body), "Anthropic authentication did not complete.") {
		t.Errorf("body = %s", body)
	}
	cancel()
	err = <-result
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want cancelled (denied must not settle)", err)
	}
}

func TestAnthropicLoginManualFallback(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["code"] != "manual-code" {
			t.Errorf("exchange code = %v", body["code"])
		}
		if body["state"] != body["code_verifier"] {
			t.Errorf("state = %v, want verifier default", body["state"])
		}
		w.Header().Set("content-type", "application/json")
		fmt.Fprint(w, `{"access_token":"at-m","refresh_token":"rt-m","expires_in":60}`)
	}))
	defer tokenServer.Close()

	result := make(chan loginResult, 1)
	go func() {
		entry, err := AnthropicLogin(context.Background(), Options{
			TokenURL:     tokenServer.URL,
			CallbackPort: EphemeralCallbackPort,
			OpenBrowser:  func(u string) error { return nil },
			ManualCode: func(ctx context.Context, prompt string) (string, error) {
				return "code=manual-code", nil
			},
		})
		result <- loginResult{entry: entry, err: err}
	}()
	res := awaitResult(t, result)
	if res.err != nil {
		t.Fatalf("login: %v", res.err)
	}
	if res.entry.Access != "at-m" {
		t.Errorf("entry = %+v", res.entry)
	}
}

func TestAnthropicLoginManualStateMismatch(t *testing.T) {
	result := make(chan error, 1)
	go func() {
		_, err := AnthropicLogin(context.Background(), Options{
			CallbackPort: EphemeralCallbackPort,
			OpenBrowser:  func(u string) error { return nil },
			ManualCode: func(ctx context.Context, prompt string) (string, error) {
				return "code=c&state=wrong", nil
			},
		})
		result <- err
	}()
	err := <-result
	if err == nil || !strings.Contains(err.Error(), "state mismatch") {
		t.Errorf("err = %v", err)
	}
}

func TestAnthropicLoginMissingAuthorizationCode(t *testing.T) {
	result := make(chan error, 1)
	go func() {
		_, err := AnthropicLogin(context.Background(), Options{
			CallbackPort: EphemeralCallbackPort,
			OpenBrowser:  func(u string) error { return nil },
			ManualCode: func(ctx context.Context, prompt string) (string, error) {
				return "", nil
			},
		})
		result <- err
	}()
	err := <-result
	if err == nil || !strings.Contains(err.Error(), "missing authorization code") {
		t.Errorf("err = %v", err)
	}
}

func TestAnthropicRefresh(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["grant_type"] != "refresh_token" || body["refresh_token"] != "rt-1" {
			t.Errorf("refresh body = %v", body)
		}
		if body["client_id"] != anthropicClientID {
			t.Errorf("client_id = %v", body["client_id"])
		}
		w.Header().Set("content-type", "application/json")
		fmt.Fprint(w, `{"access_token":"at-2","refresh_token":"rt-2","expires_in":7200}`)
	}))
	defer tokenServer.Close()

	entry, err := AnthropicRefresh(context.Background(),
		authstore.Entry{Type: "oauth", Access: "at-1", Refresh: "rt-1", Expires: 1},
		Options{TokenURL: tokenServer.URL})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if entry.Access != "at-2" || entry.Refresh != "rt-2" {
		t.Errorf("entry = %+v", entry)
	}
}

func TestAnthropicRefreshHTTPError(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"invalid_grant"}`)
	}))
	defer tokenServer.Close()
	_, err := AnthropicRefresh(context.Background(), authstore.Entry{Refresh: "rt"}, Options{TokenURL: tokenServer.URL})
	if err == nil || !strings.Contains(err.Error(), "HTTP request failed. status=400") {
		t.Errorf("err = %v", err)
	}
}

func TestAnthropicExchangeInvalidJSON(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer tokenServer.Close()
	_, err := exchangeAnthropicCode(context.Background(), "c", "s", "v", "http://localhost/x", Options{TokenURL: tokenServer.URL})
	if err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Errorf("err = %v", err)
	}
}

func TestAnthropicExchangeRequestFailure(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	brokenURL := tokenServer.URL
	tokenServer.Close()
	_, err := exchangeAnthropicCode(context.Background(), "c", "s", "v", "http://localhost/x", Options{TokenURL: brokenURL})
	if err == nil || !strings.Contains(err.Error(), "request failed") {
		t.Errorf("err = %v", err)
	}
}

func TestAnthropicLoginListenFailure(t *testing.T) {
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer blocker.Close()
	port := blocker.Addr().(*net.TCPAddr).Port
	_, err = AnthropicLogin(context.Background(), Options{
		CallbackPort: port,
		OpenBrowser:  func(u string) error { return nil },
	})
	if err == nil {
		t.Error("want listen failure error")
	}
}
