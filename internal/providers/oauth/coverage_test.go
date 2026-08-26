package oauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestErrorDetail(t *testing.T) {
	cases := []struct {
		name string
		body map[string]any
		want string
	}{
		{"error description", map[string]any{"error_description": "desc"}, "desc"},
		{"message", map[string]any{"message": "msg"}, "msg"},
		{"error string", map[string]any{"error": "bad"}, "bad"},
		{"error object", map[string]any{"error": map[string]any{"message": "inner"}}, "inner"},
		{"empty", map[string]any{}, ""},
		{"empty description falls through", map[string]any{"error_description": "", "message": "msg"}, "msg"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := errorDetail(tc.body); got != tc.want {
				t.Errorf("errorDetail(%v) = %q, want %q", tc.body, got, tc.want)
			}
		})
	}
}

func TestExchangeCodexCodeCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	_, err := exchangeCodexCode(ctx, "c", "v", "http://localhost/x", server.URL)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestStartCodexDeviceAuthInvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("nope"))
	}))
	defer server.Close()
	_, err := startCodexDeviceAuth(context.Background(), codexEndpoints{deviceCode: server.URL})
	if err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Errorf("err = %v", err)
	}
}

func TestStartCodexDeviceAuthNetworkError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	brokenURL := server.URL
	server.Close()
	_, err := startCodexDeviceAuth(context.Background(), codexEndpoints{deviceCode: brokenURL})
	if err == nil {
		t.Error("want network error")
	}
}

func TestCallbackHost(t *testing.T) {
	t.Setenv("PI_OAUTH_CALLBACK_HOST", "")
	if got := callbackHost(Options{}); got != defaultCallbackHost {
		t.Errorf("default host = %q", got)
	}
	if got := callbackHost(Options{CallbackHost: "0.0.0.0"}); got != "0.0.0.0" {
		t.Errorf("options host = %q", got)
	}
	t.Setenv("PI_OAUTH_CALLBACK_HOST", "192.168.1.5")
	if got := callbackHost(Options{}); got != "192.168.1.5" {
		t.Errorf("env host = %q", got)
	}
	if got := callbackHost(Options{CallbackHost: "127.0.0.2"}); got != "127.0.0.2" {
		t.Errorf("options must win over env: %q", got)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "a", "b"); got != "a" {
		t.Errorf("got %q", got)
	}
	if got := firstNonEmpty("", "", ""); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestFirstOptions(t *testing.T) {
	if got := firstOptions(nil); got.Timeout != 0 {
		t.Errorf("got %+v", got)
	}
	if got := firstOptions([]Options{{Timeout: 1}, {Timeout: 2}}); got.Timeout != 1 {
		t.Errorf("got %+v", got)
	}
}

func TestResolveCallbackPort(t *testing.T) {
	if got := resolveCallbackPort(Options{}, 53692); got != 53692 {
		t.Errorf("default = %d", got)
	}
	if got := resolveCallbackPort(Options{CallbackPort: EphemeralCallbackPort}, 53692); got != 0 {
		t.Errorf("ephemeral = %d", got)
	}
	if got := resolveCallbackPort(Options{CallbackPort: 9999}, 53692); got != 9999 {
		t.Errorf("explicit = %d", got)
	}
}
