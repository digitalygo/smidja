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

func TestXaiLogin(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/code", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.PostForm.Get("client_id") != xaiClientID {
			t.Errorf("client_id = %q", r.PostForm.Get("client_id"))
		}
		if r.PostForm.Get("scope") != xaiScope {
			t.Errorf("scope = %q", r.PostForm.Get("scope"))
		}
		if r.PostForm.Get("referrer") != "pi" {
			t.Errorf("referrer = %q", r.PostForm.Get("referrer"))
		}
		w.Header().Set("content-type", "application/json")
		fmt.Fprint(w, `{"device_code":"dc-1","user_code":"X-CODE","verification_uri":"https://auth.x.ai/device","verification_uri_complete":"https://auth.x.ai/device?code=X-CODE","interval":1,"expires_in":60}`)
	})
	var polls atomic.Int32
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		polls.Add(1)
		r.ParseForm()
		if r.PostForm.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" {
			t.Errorf("grant_type = %q", r.PostForm.Get("grant_type"))
		}
		if r.PostForm.Get("device_code") != "dc-1" {
			t.Errorf("device_code = %q", r.PostForm.Get("device_code"))
		}
		w.Header().Set("content-type", "application/json")
		if polls.Load() == 1 {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"authorization_pending"}`)
			return
		}
		fmt.Fprint(w, `{"access_token":"xa-1","refresh_token":"xr-1","expires_in":3600}`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	var notified DeviceCode
	result := make(chan loginResult, 1)
	go func() {
		entry, err := XaiLogin(context.Background(), Options{
			DeviceCodeURL: server.URL + "/code",
			TokenURL:      server.URL + "/token",
			DeviceCode:    func(d DeviceCode) { notified = d },
		})
		result <- loginResult{entry: entry, err: err}
	}()
	res := awaitResult(t, result)
	if res.err != nil {
		t.Fatalf("login: %v", res.err)
	}
	if notified.UserCode != "X-CODE" {
		t.Errorf("user code = %q", notified.UserCode)
	}
	if notified.VerificationURI != "https://auth.x.ai/device?code=X-CODE" {
		t.Errorf("verification URI = %q", notified.VerificationURI)
	}
	if notified.IntervalSeconds != 1 || notified.ExpiresInSeconds != 60 {
		t.Errorf("notified = %+v", notified)
	}
	if res.entry.Access != "xa-1" || res.entry.Refresh != "xr-1" {
		t.Errorf("entry = %+v", res.entry)
	}
	expected := time.Now().UnixMilli() + 3600*1000 - int64(refreshSkew/time.Millisecond)
	if diff := res.entry.Expires - expected; diff > 5000 || diff < -5000 {
		t.Errorf("expires = %d, want ~%d", res.entry.Expires, expected)
	}
}

func TestXaiLoginBrowserFallback(t *testing.T) {
	browserURL := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		if r.URL.Path == "/code" {
			fmt.Fprint(w, `{"device_code":"dc","user_code":"UC","verification_uri":"https://auth.x.ai/device","interval":1,"expires_in":60}`)
			return
		}
		fmt.Fprint(w, `{"access_token":"xa-1","refresh_token":"xr-1","expires_in":3600}`)
	}))
	defer server.Close()
	result := make(chan loginResult, 1)
	go func() {
		entry, err := XaiLogin(context.Background(), Options{
			DeviceCodeURL: server.URL + "/code",
			TokenURL:      server.URL + "/token",
			OpenBrowser:   func(u string) error { browserURL <- u; return nil },
		})
		result <- loginResult{entry: entry, err: err}
	}()
	opened := waitForBrowser(t, browserURL)
	if opened != "https://auth.x.ai/device" {
		t.Errorf("browser URL = %q", opened)
	}
	res := awaitResult(t, result)
	if res.err != nil {
		t.Fatalf("login: %v", res.err)
	}
	if res.entry.Access != "xa-1" {
		t.Errorf("entry = %+v", res.entry)
	}
}

func TestXaiLoginDenied(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		if r.URL.Path == "/code" {
			fmt.Fprint(w, `{"device_code":"dc","user_code":"UC","verification_uri":"https://auth.x.ai/device","interval":1,"expires_in":60}`)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"access_denied"}`)
	}))
	defer server.Close()
	result := make(chan error, 1)
	go func() {
		_, err := XaiLogin(context.Background(), Options{
			DeviceCodeURL: server.URL + "/code",
			TokenURL:      server.URL + "/token",
			DeviceCode:    func(d DeviceCode) {},
		})
		result <- err
	}()
	err := <-result
	if err == nil || !strings.Contains(err.Error(), "xai device authorization was denied") {
		t.Errorf("err = %v", err)
	}
}

func TestXaiLoginExpired(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		if r.URL.Path == "/code" {
			fmt.Fprint(w, `{"device_code":"dc","user_code":"UC","verification_uri":"https://auth.x.ai/device","interval":1,"expires_in":60}`)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"expired_token"}`)
	}))
	defer server.Close()
	result := make(chan error, 1)
	go func() {
		_, err := XaiLogin(context.Background(), Options{
			DeviceCodeURL: server.URL + "/code",
			TokenURL:      server.URL + "/token",
			DeviceCode:    func(d DeviceCode) {},
		})
		result <- err
	}()
	err := <-result
	if err == nil || !strings.Contains(err.Error(), "xai device code expired") {
		t.Errorf("err = %v", err)
	}
}

func TestXaiLoginTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		if r.URL.Path == "/code" {
			fmt.Fprint(w, `{"device_code":"dc","user_code":"UC","verification_uri":"https://auth.x.ai/device","interval":1,"expires_in":1}`)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"authorization_pending"}`)
	}))
	defer server.Close()
	result := make(chan error, 1)
	go func() {
		_, err := XaiLogin(context.Background(), Options{
			DeviceCodeURL: server.URL + "/code",
			TokenURL:      server.URL + "/token",
			DeviceCode:    func(d DeviceCode) {},
		})
		result <- err
	}()
	err := <-result
	if err == nil || err.Error() != deviceTimeoutMessage {
		t.Errorf("err = %v, want %q", err, deviceTimeoutMessage)
	}
}

func TestXaiDeviceUntrustedURI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"device_code":"dc","user_code":"uc","verification_uri":"http://evil.example/","expires_in":60}`)
	}))
	defer server.Close()
	_, err := startXaiDeviceAuth(context.Background(), Options{DeviceCodeURL: server.URL})
	if err == nil || !strings.Contains(err.Error(), "untrusted verification URI") {
		t.Errorf("err = %v", err)
	}
}

func TestXaiDeviceInvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("nope"))
	}))
	defer server.Close()
	_, err := startXaiDeviceAuth(context.Background(), Options{DeviceCodeURL: server.URL})
	if err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Errorf("err = %v", err)
	}
}

func TestXaiDeviceHTTPFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"invalid_client","error_description":"bad client"}`)
	}))
	defer server.Close()
	_, err := startXaiDeviceAuth(context.Background(), Options{DeviceCodeURL: server.URL})
	if err == nil || !strings.Contains(err.Error(), "invalid_client: bad client") {
		t.Errorf("err = %v", err)
	}
}

func TestPollXaiTokensSlowDown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"slow_down","interval":7}`)
	}))
	defer server.Close()
	outcome, err := pollXaiTokens(context.Background(), Options{TokenURL: server.URL}, xaiDevice{deviceCode: "dc"})
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if outcome.status != deviceSlowDown {
		t.Errorf("status = %v", outcome.status)
	}
	if outcome.interval == nil || *outcome.interval != 7 {
		t.Errorf("interval = %v", outcome.interval)
	}
}

func TestPollXaiTokensDeniedAndExpired(t *testing.T) {
	cases := []struct {
		name      string
		response  string
		wantError string
	}{
		{"denied", `{"error":"access_denied"}`, "xai device authorization was denied"},
		{"denied alt", `{"error":"authorization_denied"}`, "xai device authorization was denied"},
		{"expired", `{"error":"expired_token"}`, "xai device code expired"},
		{"unknown", `{"error":"weird"}`, "xai oauth device token polling failed (HTTP 400): weird"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, tc.response)
			}))
			defer server.Close()
			outcome, err := pollXaiTokens(context.Background(), Options{TokenURL: server.URL}, xaiDevice{deviceCode: "dc"})
			if err != nil {
				t.Fatalf("poll: %v", err)
			}
			if outcome.status != deviceFailed {
				t.Errorf("status = %v, want failed", outcome.status)
			}
			if !strings.Contains(outcome.message, tc.wantError) {
				t.Errorf("message = %q, want %q", outcome.message, tc.wantError)
			}
		})
	}
}

func TestXaiRefresh(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.PostForm.Get("grant_type") != "refresh_token" || r.PostForm.Get("refresh_token") != "xr-1" {
			t.Errorf("refresh body = %v", r.PostForm)
		}
		w.Header().Set("content-type", "application/json")
		fmt.Fprint(w, `{"access_token":"xa-2","expires_in":1800}`)
	}))
	defer server.Close()
	entry, err := XaiRefresh(context.Background(),
		authstore.Entry{Type: "oauth", Access: "xa-1", Refresh: "xr-1", Expires: 1},
		Options{TokenURL: server.URL})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if entry.Access != "xa-2" {
		t.Errorf("access = %q", entry.Access)
	}
	if entry.Refresh != "xr-1" {
		t.Errorf("refresh = %q, want previous when omitted", entry.Refresh)
	}
	expected := time.Now().UnixMilli() + 1800*1000 - int64(refreshSkew/time.Millisecond)
	if diff := entry.Expires - expected; diff > 5000 || diff < -5000 {
		t.Errorf("expires = %d, want ~%d", entry.Expires, expected)
	}
}

func TestXaiRefreshRotatesToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"access_token":"xa-3","refresh_token":"xr-3","expires_in":1800}`)
	}))
	defer server.Close()
	entry, err := XaiRefresh(context.Background(), authstore.Entry{Refresh: "xr-1"}, Options{TokenURL: server.URL})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if entry.Refresh != "xr-3" {
		t.Errorf("refresh = %q, want rotated", entry.Refresh)
	}
}

func TestXaiRefreshUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"invalid_grant"}`)
	}))
	defer server.Close()
	_, err := XaiRefresh(context.Background(), authstore.Entry{Refresh: "dead"}, Options{TokenURL: server.URL})
	if err == nil || !strings.Contains(err.Error(), "invalid_grant") {
		t.Errorf("err = %v", err)
	}
}

func TestXaiCredentialsInvalid(t *testing.T) {
	if _, err := xaiCredentials(map[string]any{}, ""); err == nil {
		t.Error("empty body should fail")
	}
	if _, err := xaiCredentials(map[string]any{"access_token": "a", "refresh_token": "r", "expires_in": -1.0}, ""); err == nil {
		t.Error("negative expires_in should fail")
	}
	if _, err := xaiCredentials(map[string]any{"access_token": "a"}, "prev"); err != nil {
		t.Errorf("omitted refresh_token with previous should pass: %v", err)
	}
}

func TestXaiLoginCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		if r.URL.Path == "/code" {
			fmt.Fprint(w, `{"device_code":"dc","user_code":"UC","verification_uri":"https://auth.x.ai/device","interval":1,"expires_in":60}`)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"authorization_pending"}`)
	}))
	defer server.Close()
	result := make(chan error, 1)
	go func() {
		_, err := XaiLogin(ctx, Options{
			DeviceCodeURL: server.URL + "/code",
			TokenURL:      server.URL + "/token",
			DeviceCode:    func(d DeviceCode) {},
		})
		result <- err
	}()
	time.Sleep(1500 * time.Millisecond)
	cancel()
	err := <-result
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
