package oauth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/authstore"
)

func startTestServer(t *testing.T, cfg callbackConfig) *callbackServer {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	server, err := startCallbackServer(ctx, "127.0.0.1", 0, "localhost", cfg)
	if err != nil {
		t.Fatalf("startCallbackServer: %v", err)
	}
	t.Cleanup(server.close)
	return server
}

func TestCallbackServerNotFound(t *testing.T) {
	server := startTestServer(t, callbackConfig{
		providerName:       "Test",
		path:               "/callback",
		missingCodeMessage: "Missing code.",
		successMessage:     "ok",
	})
	resp, err := http.Get(server.url + "/wrong")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	if !strings.Contains(string(body), "callback route not found") {
		t.Errorf("body = %s", body)
	}
}

func TestCallbackServerRejectsPost(t *testing.T) {
	server := startTestServer(t, callbackConfig{
		providerName:   "Test",
		path:           "/callback",
		successMessage: "ok",
	})
	resp, err := http.Post(server.url+"?code=c", "text/plain", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestCallbackServerMissingCode(t *testing.T) {
	server := startTestServer(t, callbackConfig{
		providerName:       "Test",
		path:               "/callback",
		missingCodeMessage: "Missing code.",
		successMessage:     "ok",
	})
	resp, err := http.Get(server.url)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(string(body), "Missing code.") {
		t.Errorf("body = %s", body)
	}
}

func TestCallbackServerStateMismatchKeepsWaiting(t *testing.T) {
	server := startTestServer(t, callbackConfig{
		providerName:       "Test",
		path:               "/callback",
		expectedState:      "expected",
		requireState:       true,
		missingCodeMessage: "Missing code or state.",
		successMessage:     "ok",
	})
	resp, err := http.Get(server.url + "?code=c&state=wrong")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(string(body), "State mismatch.") {
		t.Errorf("body = %s", body)
	}
	select {
	case <-server.waitCh:
		t.Fatal("state mismatch settled the server")
	case <-time.After(100 * time.Millisecond):
	}
	resp, err = http.Get(server.url + "?code=c&state=expected")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	outcome := <-server.waitCh
	if outcome.code != "c" || outcome.state != "expected" {
		t.Errorf("outcome = %+v", outcome)
	}
}

func TestCallbackServerStateCheckBeforeCode(t *testing.T) {
	server := startTestServer(t, callbackConfig{
		providerName:         "Test",
		path:                 "/callback",
		expectedState:        "expected",
		checkStateBeforeCode: true,
		missingCodeMessage:   "Missing authorization code.",
		successMessage:       "ok",
	})
	resp, err := http.Get(server.url + "?state=wrong")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "State mismatch.") {
		t.Errorf("status = %d body = %s", resp.StatusCode, body)
	}
	resp, err = http.Get(server.url + "?state=expected")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Missing authorization code.") {
		t.Errorf("body = %s", body)
	}
}

func TestCallbackServerDeniedSettlesError(t *testing.T) {
	server := startTestServer(t, callbackConfig{
		providerName:       "Test",
		path:               "/callback",
		deniedPageTitle:    "Test authorization was denied.",
		deniedPageDetail:   "%s",
		deniedError:        "Test authorization failed: %s",
		missingCodeMessage: "Missing code.",
		successMessage:     "ok",
	})
	resp, err := http.Get(server.url + "?error=access_denied&error_description=no+thanks")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(string(body), "no thanks") {
		t.Errorf("body = %s", body)
	}
	outcome := <-server.waitCh
	if outcome.err == nil || !strings.Contains(outcome.err.Error(), "no thanks") {
		t.Errorf("outcome err = %v", outcome.err)
	}
}

func TestCallbackServerDeniedDoesNotSettle(t *testing.T) {
	server := startTestServer(t, callbackConfig{
		providerName:       "Test",
		path:               "/callback",
		deniedPageTitle:    "Test authentication did not complete.",
		deniedPageDetail:   "Error: %s",
		missingCodeMessage: "Missing code or state.",
		successMessage:     "ok",
	})
	resp, err := http.Get(server.url + "?error=access_denied")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(string(body), "Test authentication did not complete.") {
		t.Errorf("body = %s", body)
	}
	select {
	case <-server.waitCh:
		t.Fatal("denied response settled the server")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestCallbackServerAlreadyUsed(t *testing.T) {
	server := startTestServer(t, callbackConfig{
		providerName:       "Test",
		path:               "/callback",
		missingCodeMessage: "Missing code.",
		successMessage:     "ok",
	})
	resp, err := http.Get(server.url + "?code=first")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("first status = %d, want 200", resp.StatusCode)
	}
	resp, err = http.Get(server.url + "?code=second")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("second status = %d, want 409", resp.StatusCode)
	}
	if !strings.Contains(string(body), "already been used") {
		t.Errorf("body = %s", body)
	}
}

func TestCallbackServerExchange(t *testing.T) {
	server := startTestServer(t, callbackConfig{
		providerName:       "Test",
		path:               "/callback",
		missingCodeMessage: "Missing code.",
		successMessage:     "ok",
		exchange: func(ctx context.Context, code string) (authstore.Entry, error) {
			return newEntry("access-"+code, "", 1, nil)
		},
	})
	resp, err := http.Get(server.url + "?code=abc")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	outcome := <-server.waitCh
	if !outcome.hasEntry || outcome.entry.Access != "access-abc" {
		t.Errorf("outcome = %+v", outcome)
	}
}

func TestCallbackServerExchangeFailure(t *testing.T) {
	server := startTestServer(t, callbackConfig{
		providerName:       "Test",
		path:               "/callback",
		missingCodeMessage: "Missing code.",
		successMessage:     "ok",
		exchange: func(ctx context.Context, code string) (authstore.Entry, error) {
			return authstore.Entry{}, errors.New("boom")
		},
	})
	resp, err := http.Get(server.url + "?code=abc")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", resp.StatusCode)
	}
	if !strings.Contains(string(body), "boom") {
		t.Errorf("body = %s", body)
	}
	outcome := <-server.waitCh
	if outcome.err == nil || !strings.Contains(outcome.err.Error(), "boom") {
		t.Errorf("outcome err = %v", outcome.err)
	}
}

func TestCallbackServerCancelBeforeClaim(t *testing.T) {
	server := startTestServer(t, callbackConfig{
		providerName:   "Test",
		path:           "/callback",
		successMessage: "ok",
	})
	server.cancel()
	outcome := <-server.waitCh
	if outcome.code != "" || outcome.err != nil || outcome.hasEntry {
		t.Errorf("cancel outcome = %+v", outcome)
	}
}

func TestCallbackServerCancelAfterClaimIgnored(t *testing.T) {
	server := startTestServer(t, callbackConfig{
		providerName:       "Test",
		path:               "/callback",
		missingCodeMessage: "Missing code.",
		successMessage:     "ok",
	})
	resp, err := http.Get(server.url + "?code=c")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	outcome := <-server.waitCh
	if outcome.code != "c" {
		t.Errorf("outcome = %+v", outcome)
	}
	server.cancel()
	select {
	case extra := <-server.waitCh:
		t.Errorf("unexpected extra outcome: %+v", extra)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestCallbackServerWaitCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	server := startTestServer(t, callbackConfig{path: "/callback", successMessage: "ok"})
	go cancel()
	_, err := server.wait(ctx)
	if err == nil {
		t.Fatal("want error on cancelled wait")
	}
}

func TestCallbackServerWaitTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	server := startTestServer(t, callbackConfig{path: "/callback", successMessage: "ok"})
	_, err := server.wait(ctx)
	if err == nil {
		t.Fatal("want error on wait timeout")
	}
}

func TestCallbackServerListenFailure(t *testing.T) {
	ctx := context.Background()
	cfg := callbackConfig{path: "/callback", successMessage: "ok"}
	first, err := startCallbackServer(ctx, "127.0.0.1", 0, "localhost", cfg)
	if err != nil {
		t.Fatalf("startCallbackServer: %v", err)
	}
	defer first.close()
	firstURL, _ := url.Parse(first.url)
	port, _ := strconv.Atoi(firstURL.Port())
	if _, err := startCallbackServer(ctx, "127.0.0.1", port, "localhost", cfg); err == nil {
		t.Fatal("want listen failure on occupied port")
	}
}
