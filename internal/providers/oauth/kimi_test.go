package oauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/authstore"
)

func TestKimiLogin(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/oauth/device_authorization", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.PostForm.Get("client_id") != kimiClientID {
			t.Errorf("client_id = %q", r.PostForm.Get("client_id"))
		}
		w.Header().Set("content-type", "application/json")
		fmt.Fprint(w, `{"device_code":"kdc","user_code":"K-CODE","verification_uri":"https://auth.kimi.com/device","verification_uri_complete":"https://auth.kimi.com/device?code=K-CODE","interval":1,"expires_in":60}`)
	})
	var polls atomic.Int32
	mux.HandleFunc("/api/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		polls.Add(1)
		w.Header().Set("content-type", "application/json")
		if polls.Load() == 1 {
			fmt.Fprint(w, `{"error":"authorization_pending"}`)
			return
		}
		fmt.Fprint(w, `{"access_token":"ka-1","refresh_token":"kr-1","expires_in":3600}`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	var notified DeviceCode
	result := make(chan loginResult, 1)
	go func() {
		entry, err := KimiLogin(context.Background(), Options{
			OAuthHost:  server.URL,
			DeviceCode: func(d DeviceCode) { notified = d },
		})
		result <- loginResult{entry: entry, err: err}
	}()
	res := awaitResult(t, result)
	if res.err != nil {
		t.Fatalf("login: %v", res.err)
	}
	if notified.UserCode != "K-CODE" {
		t.Errorf("user code = %q", notified.UserCode)
	}
	if notified.VerificationURI != "https://auth.kimi.com/device?code=K-CODE" {
		t.Errorf("verification URI = %q", notified.VerificationURI)
	}
	if notified.IntervalSeconds != 1 || notified.ExpiresInSeconds != 60 {
		t.Errorf("notified = %+v", notified)
	}
	if res.entry.Access != "ka-1" || res.entry.Refresh != "kr-1" {
		t.Errorf("entry = %+v", res.entry)
	}
	expected := time.Now().UnixMilli() + 3600*1000
	if diff := res.entry.Expires - expected; diff > 5000 || diff < -5000 {
		t.Errorf("expires = %d, want ~%d", res.entry.Expires, expected)
	}
}

func TestKimiLoginEnvHost(t *testing.T) {
	t.Setenv("KIMI_CODE_OAUTH_HOST", "")
	hostCh := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hostCh <- r.Host
		w.Header().Set("content-type", "application/json")
		if r.URL.Path == "/api/oauth/device_authorization" {
			fmt.Fprint(w, `{"device_code":"kdc","user_code":"K","verification_uri":"https://auth.kimi.com/d","verification_uri_complete":"https://auth.kimi.com/d?c=K","interval":1,"expires_in":60}`)
			return
		}
		fmt.Fprint(w, `{"error":"authorization_pending"}`)
	}))
	defer server.Close()
	t.Setenv("KIMI_OAUTH_HOST", server.URL)
	_, err := KimiLogin(context.Background(), Options{
		DeviceCode: func(d DeviceCode) {},
		Timeout:    2 * time.Second,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want DeadlineExceeded via env host", err)
	}
	if host := <-hostCh; !strings.Contains(host, "127.0.0.1") {
		t.Errorf("host = %q", host)
	}
}

func TestKimiLoginInvalidDeviceResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"device_code":"kdc","user_code":"K","verification_uri":"javascript:alert(1)","verification_uri_complete":"https://auth.kimi.com/d"}`)
	}))
	defer server.Close()
	_, err := startKimiDeviceAuth(context.Background(), server.URL)
	if err == nil || !strings.Contains(err.Error(), "invalid kimi code device authorization response") {
		t.Errorf("err = %v", err)
	}
}

func TestKimiLoginDeviceAuthFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	_, err := startKimiDeviceAuth(context.Background(), server.URL)
	if err == nil || !strings.Contains(err.Error(), "device authorization failed with status 500") {
		t.Errorf("err = %v", err)
	}
}

func TestPollKimiTokenOutcomes(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		response   string
		wantStatus deviceStatus
	}{
		{"pending", 200, `{"error":"authorization_pending"}`, devicePending},
		{"slow down", 200, `{"error":"slow_down","interval":3}`, deviceSlowDown},
		{"expired", 400, `{"error":"expired_token"}`, deviceFailed},
		{"denied", 400, `{"error":"access_denied"}`, deviceFailed},
		{"server error", 500, `oops`, deviceFailed},
		{"other error", 400, `{"error":"weird","error_description":"explained"}`, deviceFailed},
		{"complete", 200, `{"access_token":"a","refresh_token":"r","expires_in":60}`, deviceComplete},
		{"missing fields", 200, `{"access_token":"a"}`, deviceFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.response)
			}))
			defer server.Close()
			outcome, err := pollKimiToken(context.Background(), server.URL, kimiDevice{deviceCode: "kdc"})
			if err != nil {
				t.Fatalf("poll: %v", err)
			}
			if outcome.status != tc.wantStatus {
				t.Errorf("status = %v, want %v (message %q)", outcome.status, tc.wantStatus, outcome.message)
			}
			if tc.name == "slow down" && (outcome.interval == nil || *outcome.interval != 3) {
				t.Errorf("interval = %v", outcome.interval)
			}
			if tc.name == "server error" && !strings.Contains(outcome.message, "status 500") {
				t.Errorf("message = %q", outcome.message)
			}
			if tc.name == "other error" && !strings.Contains(outcome.message, "weird: explained") {
				t.Errorf("message = %q", outcome.message)
			}
			if tc.wantStatus == deviceComplete {
				entry := outcome.value.(authstore.Entry)
				if entry.Access != "a" || entry.Refresh != "r" {
					t.Errorf("entry = %+v", entry)
				}
			}
		})
	}
}

func TestKimiRefresh(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.PostForm.Get("grant_type") != "refresh_token" || r.PostForm.Get("refresh_token") != "kr-1" {
			t.Errorf("refresh body = %v", r.PostForm)
		}
		if r.PostForm.Get("client_id") != kimiClientID {
			t.Errorf("client_id = %q", r.PostForm.Get("client_id"))
		}
		w.Header().Set("content-type", "application/json")
		fmt.Fprint(w, `{"access_token":"ka-2","refresh_token":"kr-2","expires_in":3600}`)
	}))
	defer server.Close()
	entry, err := KimiRefresh(context.Background(),
		authstore.Entry{Type: "oauth", Access: "ka-1", Refresh: "kr-1", Expires: 1},
		Options{OAuthHost: server.URL})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if entry.Access != "ka-2" || entry.Refresh != "kr-2" {
		t.Errorf("entry = %+v", entry)
	}
}

func TestKimiRefreshRetriesOnTransientFailure(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("content-type", "application/json")
		fmt.Fprint(w, `{"access_token":"ka-2","refresh_token":"kr-2","expires_in":3600}`)
	}))
	defer server.Close()
	start := time.Now()
	entry, err := KimiRefresh(context.Background(), authstore.Entry{Refresh: "kr-1"}, Options{OAuthHost: server.URL})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if attempts.Load() != 2 {
		t.Errorf("attempts = %d, want 2", attempts.Load())
	}
	if entry.Access != "ka-2" {
		t.Errorf("entry = %+v", entry)
	}
	if elapsed := time.Since(start); elapsed < 900*time.Millisecond {
		t.Errorf("no backoff applied: %v", elapsed)
	}
}

func TestKimiRefreshRetriesOn429(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("content-type", "application/json")
		fmt.Fprint(w, `{"access_token":"ka-2","refresh_token":"kr-2","expires_in":3600}`)
	}))
	defer server.Close()
	entry, err := KimiRefresh(context.Background(), authstore.Entry{Refresh: "kr-1"}, Options{OAuthHost: server.URL})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if attempts.Load() != 2 || entry.Access != "ka-2" {
		t.Errorf("attempts = %d entry = %+v", attempts.Load(), entry)
	}
}

func TestKimiRefreshUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"invalid_grant","error_description":"dead token"}`)
	}))
	defer server.Close()
	_, err := KimiRefresh(context.Background(), authstore.Entry{Refresh: "dead"}, Options{OAuthHost: server.URL})
	if err == nil || !strings.Contains(err.Error(), "unauthorized") || !strings.Contains(err.Error(), "dead token") {
		t.Errorf("err = %v", err)
	}
}

func TestKimiRefreshTerminalError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"unsupported_grant_type"}`)
	}))
	defer server.Close()
	_, err := KimiRefresh(context.Background(), authstore.Entry{Refresh: "kr"}, Options{OAuthHost: server.URL})
	if err == nil || !strings.Contains(err.Error(), "status 400") {
		t.Errorf("err = %v", err)
	}
}

func TestKimiRefreshGivesUp(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	_, err := KimiRefresh(context.Background(), authstore.Entry{Refresh: "kr"}, Options{OAuthHost: server.URL})
	if err == nil {
		t.Fatal("want error after retries")
	}
	if attempts.Load() != 4 {
		t.Errorf("attempts = %d, want 4 (initial + 3 retries)", attempts.Load())
	}
	if !strings.Contains(err.Error(), "status 500") {
		t.Errorf("err = %v", err)
	}
}

func TestKimiRefreshAborted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	_, err := refreshKimiToken(ctx, server.URL, "kr")
	if err == nil {
		t.Fatal("want abort error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestResolveKimiHost(t *testing.T) {
	t.Setenv("KIMI_CODE_OAUTH_HOST", "")
	t.Setenv("KIMI_OAUTH_HOST", "")
	if got := resolveKimiHost(Options{}); got != kimiDefaultOAuthHost {
		t.Errorf("default host = %q", got)
	}
	if got := resolveKimiHost(Options{OAuthHost: "https://mock.example/"}); got != "https://mock.example" {
		t.Errorf("options host = %q", got)
	}
	t.Setenv("KIMI_OAUTH_HOST", "https://env.example/")
	if got := resolveKimiHost(Options{}); got != "https://env.example" {
		t.Errorf("env host = %q", got)
	}
	if got := resolveKimiHost(Options{OAuthHost: "https://opt.example"}); got != "https://opt.example" {
		t.Errorf("options must win over env: %q", got)
	}
}

func TestKimiTokenResponseMissingFields(t *testing.T) {
	_, err := kimiTokenResponse(map[string]any{"access_token": "a"}, "poll")
	if err == nil || !strings.Contains(err.Error(), "missing fields") {
		t.Errorf("err = %v", err)
	}
}

func TestTrustedHTTPURL(t *testing.T) {
	if !trustedHTTPURL("https://auth.kimi.com/x") {
		t.Error("https URL must be trusted")
	}
	if !trustedHTTPURL("http://auth.kimi.com/x") {
		t.Error("http URL must be trusted")
	}
	if trustedHTTPURL("javascript:alert(1)") {
		t.Error("javascript URL must be rejected")
	}
	if trustedHTTPURL("::not a url") {
		t.Error("malformed URL must be rejected")
	}
}
