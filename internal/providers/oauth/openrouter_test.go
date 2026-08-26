package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/authstore"
)

func TestOpenRouterLogin(t *testing.T) {
	challengeCh := make(chan string, 1)
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode exchange body: %v", err)
		}
		verifier, _ := body["code_verifier"].(string)
		sum := sha256.Sum256([]byte(verifier))
		if base64.RawURLEncoding.EncodeToString(sum[:]) != <-challengeCh {
			t.Errorf("PKCE verifier does not match challenge")
		}
		if body["code"] != "auth-code-1" {
			t.Errorf("exchange code = %v", body["code"])
		}
		if body["code_challenge_method"] != "S256" {
			t.Errorf("code_challenge_method = %v", body["code_challenge_method"])
		}
		w.Header().Set("content-type", "application/json")
		fmt.Fprint(w, `{"key":"sk-or-v1-test"}`)
	}))
	defer tokenServer.Close()

	browserURL := make(chan string, 1)
	result := make(chan loginResult, 1)
	go func() {
		entry, err := OpenRouterLogin(context.Background(), Options{
			TokenURL:    tokenServer.URL,
			OpenBrowser: func(u string) error { browserURL <- u; return nil },
		})
		result <- loginResult{entry: entry, err: err}
	}()

	authorizeURL := waitForBrowser(t, browserURL)
	parsed, err := url.Parse(authorizeURL)
	if err != nil {
		t.Fatalf("parse authorize URL: %v", err)
	}
	challengeCh <- parsed.Query().Get("code_challenge")
	callbackURL := parsed.Query().Get("callback_url")
	if parsed.Query().Get("code_challenge_method") != "S256" {
		t.Errorf("code_challenge_method = %q", parsed.Query().Get("code_challenge_method"))
	}
	if !strings.HasPrefix(callbackURL, "http://127.0.0.1:") {
		t.Errorf("callback URL = %q", callbackURL)
	}
	if !strings.Contains(callbackURL, "/oauth/callback/") {
		t.Errorf("callback URL missing uuid path: %q", callbackURL)
	}
	resp, err := http.Get(callbackURL + "?code=auth-code-1")
	if err != nil {
		t.Fatalf("callback request: %v", err)
	}
	resp.Body.Close()

	res := awaitResult(t, result)
	if res.err != nil {
		t.Fatalf("login: %v", res.err)
	}
	if res.entry.Type != "oauth" || res.entry.Access != "sk-or-v1-test" || res.entry.Refresh != "" {
		t.Errorf("entry = %+v", res.entry)
	}
	if res.entry.Expires != expiresMaxSafeInteger {
		t.Errorf("expires = %d, want %d", res.entry.Expires, expiresMaxSafeInteger)
	}
}

func TestOpenRouterLoginManualFallback(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["code"] != "manual-code-1" {
			t.Errorf("exchange code = %v", body["code"])
		}
		w.Header().Set("content-type", "application/json")
		fmt.Fprint(w, `{"key":"sk-or-v1-manual"}`)
	}))
	defer tokenServer.Close()

	result := make(chan loginResult, 1)
	go func() {
		entry, err := OpenRouterLogin(context.Background(), Options{
			TokenURL:    tokenServer.URL,
			OpenBrowser: func(u string) error { return nil },
			ManualCode: func(ctx context.Context, prompt string) (string, error) {
				return "code=manual-code-1", nil
			},
		})
		result <- loginResult{entry: entry, err: err}
	}()
	res := awaitResult(t, result)
	if res.err != nil {
		t.Fatalf("login: %v", res.err)
	}
	if res.entry.Access != "sk-or-v1-manual" {
		t.Errorf("entry = %+v", res.entry)
	}
}

func TestOpenRouterLoginManualPasteRedirectURL(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["code"] != "pasted-code" {
			t.Errorf("exchange code = %v", body["code"])
		}
		w.Header().Set("content-type", "application/json")
		fmt.Fprint(w, `{"key":"sk-or-v1-pasted"}`)
	}))
	defer tokenServer.Close()

	browserURL := make(chan string, 1)
	result := make(chan loginResult, 1)
	go func() {
		entry, err := OpenRouterLogin(context.Background(), Options{
			TokenURL:    tokenServer.URL,
			OpenBrowser: func(u string) error { browserURL <- u; return nil },
			ManualCode: func(ctx context.Context, prompt string) (string, error) {
				return prompt + "?code=pasted-code", nil
			},
		})
		result <- loginResult{entry: entry, err: err}
	}()
	waitForBrowser(t, browserURL)
	res := awaitResult(t, result)
	if res.err != nil {
		t.Fatalf("login: %v", res.err)
	}
	if res.entry.Access != "sk-or-v1-pasted" {
		t.Errorf("entry = %+v", res.entry)
	}
}

func TestOpenRouterLoginCallbackWinsOverManual(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["code"] != "cb-code" {
			t.Errorf("exchange code = %v", body["code"])
		}
		w.Header().Set("content-type", "application/json")
		fmt.Fprint(w, `{"key":"sk-or-v1-callback"}`)
	}))
	defer tokenServer.Close()

	browserURL := make(chan string, 1)
	result := make(chan loginResult, 1)
	manualCalled := make(chan struct{})
	go func() {
		entry, err := OpenRouterLogin(context.Background(), Options{
			TokenURL:    tokenServer.URL,
			OpenBrowser: func(u string) error { browserURL <- u; return nil },
			ManualCode: func(ctx context.Context, prompt string) (string, error) {
				close(manualCalled)
				<-ctx.Done()
				return "", ctx.Err()
			},
		})
		result <- loginResult{entry: entry, err: err}
	}()
	authorizeURL := waitForBrowser(t, browserURL)
	parsed, _ := url.Parse(authorizeURL)
	callbackURL := parsed.Query().Get("callback_url")
	<-manualCalled
	resp, err := http.Get(callbackURL + "?code=cb-code")
	if err != nil {
		t.Fatalf("callback request: %v", err)
	}
	resp.Body.Close()
	res := awaitResult(t, result)
	if res.err != nil {
		t.Fatalf("login: %v", res.err)
	}
	if res.entry.Access != "sk-or-v1-callback" {
		t.Errorf("entry = %+v", res.entry)
	}
}

func TestOpenRouterLoginDenied(t *testing.T) {
	browserURL := make(chan string, 1)
	result := make(chan error, 1)
	go func() {
		_, err := OpenRouterLogin(context.Background(), Options{
			OpenBrowser: func(u string) error { browserURL <- u; return nil },
		})
		result <- err
	}()
	authorizeURL := waitForBrowser(t, browserURL)
	parsed, _ := url.Parse(authorizeURL)
	callbackURL := parsed.Query().Get("callback_url")
	resp, err := http.Get(callbackURL + "?error=access_denied&error_description=user+said+no")
	if err != nil {
		t.Fatalf("callback request: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(string(body), "OpenRouter authorization was denied.") {
		t.Errorf("body = %s", body)
	}
	err = <-result
	if err == nil || !strings.Contains(err.Error(), "user said no") {
		t.Errorf("err = %v", err)
	}
}

func TestOpenRouterLoginTimeout(t *testing.T) {
	_, err := OpenRouterLogin(context.Background(), Options{
		Timeout:     150 * time.Millisecond,
		OpenBrowser: func(u string) error { return nil },
	})
	if err == nil {
		t.Fatal("want timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want DeadlineExceeded", err)
	}
	if !strings.Contains(err.Error(), "login timed out") {
		t.Errorf("err = %v, want timeout message", err)
	}
}

func TestOpenRouterLoginCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	browserURL := make(chan string, 1)
	result := make(chan error, 1)
	go func() {
		_, err := OpenRouterLogin(ctx, Options{
			OpenBrowser: func(u string) error { browserURL <- u; return nil },
		})
		result <- err
	}()
	waitForBrowser(t, browserURL)
	cancel()
	err := <-result
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestOpenRouterLoginExchangeError(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"invalid_request","error_description":"bad code"}`)
	}))
	defer tokenServer.Close()

	browserURL := make(chan string, 1)
	result := make(chan error, 1)
	go func() {
		_, err := OpenRouterLogin(context.Background(), Options{
			TokenURL:    tokenServer.URL,
			OpenBrowser: func(u string) error { browserURL <- u; return nil },
		})
		result <- err
	}()
	authorizeURL := waitForBrowser(t, browserURL)
	parsed, _ := url.Parse(authorizeURL)
	callbackURL := parsed.Query().Get("callback_url")
	resp, err := http.Get(callbackURL + "?code=bad")
	if err != nil {
		t.Fatalf("callback request: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", resp.StatusCode)
	}
	if !strings.Contains(string(body), "bad code") {
		t.Errorf("body = %s", body)
	}
	err = <-result
	if err == nil || !strings.Contains(err.Error(), "key exchange failed") {
		t.Errorf("err = %v", err)
	}
}

func TestOpenRouterExchangeInvalidJSON(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer tokenServer.Close()
	_, err := exchangeOpenRouterCode(context.Background(), "c", "v", Options{TokenURL: tokenServer.URL})
	if err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Errorf("err = %v", err)
	}
}

func TestOpenRouterExchangeMissingKey(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"type":"oauth"}`)
	}))
	defer tokenServer.Close()
	_, err := exchangeOpenRouterCode(context.Background(), "c", "v", Options{TokenURL: tokenServer.URL})
	if err == nil || !strings.Contains(err.Error(), "no \"key\"") {
		t.Errorf("err = %v", err)
	}
}

func TestOpenRouterExchangeRequestFailure(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	brokenURL := tokenServer.URL
	tokenServer.Close()
	_, err := exchangeOpenRouterCode(context.Background(), "c", "v", Options{TokenURL: brokenURL})
	if err == nil {
		t.Error("want exchange error for closed server")
	}
}

func TestOpenRouterRefresh(t *testing.T) {
	entry := authstore.Entry{Type: "oauth", Access: "sk-a", Refresh: "", Expires: 1}
	got, err := OpenRouterRefresh(context.Background(), entry)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got.Access != entry.Access || got.Refresh != entry.Refresh || got.Expires != entry.Expires {
		t.Errorf("refresh changed entry: %+v", got)
	}
}
