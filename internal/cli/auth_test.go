package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/authstore"
	"github.com/digitalygo/smidja/internal/providers/manifest"
	"github.com/digitalygo/smidja/internal/providers/oauth"
)

func authHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	return home
}

func authFile(home string) string {
	return filepath.Join(home, ".smidja", "auth.json")
}

func readAuthStore(t *testing.T, home string) map[string]map[string]any {
	t.Helper()
	content, err := os.ReadFile(authFile(home))
	if err != nil {
		t.Fatalf("read auth.json: %v", err)
	}
	var entries map[string]map[string]any
	if err := json.Unmarshal(content, &entries); err != nil {
		t.Fatalf("parse auth.json: %v", err)
	}
	return entries
}

func seedStore(t *testing.T, home string, entries map[string]authstore.Entry) {
	t.Helper()
	store, err := authstore.Load(authFile(home))
	if err != nil {
		t.Fatal(err)
	}
	for provider, entry := range entries {
		if err := store.Set(provider, entry); err != nil {
			t.Fatal(err)
		}
	}
}

func manualAuthOptions(tokenURL string) func(string) oauth.Options {
	return func(string) oauth.Options {
		return oauth.Options{
			TokenURL:     tokenURL,
			CallbackPort: oauth.EphemeralCallbackPort,
			Timeout:      10 * time.Second,
			OpenBrowser:  func(u string) error { return nil },
			ManualCode:   func(ctx context.Context, prompt string) (string, error) { return "code=test-code", nil },
		}
	}
}

func authDeps(home string, env map[string]string, options func(string) oauth.Options, httpClient *http.Client, stdin string) (*Deps, *bytes.Buffer, *bytes.Buffer) {
	var stdout, stderr bytes.Buffer
	deps := &Deps{
		Env:         envFrom(env),
		Getwd:       func() (string, error) { return home, nil },
		Home:        func() string { return home },
		Stdin:       strings.NewReader(stdin),
		Stdout:      &stdout,
		Stderr:      &stderr,
		AuthOptions: options,
		HTTPClient:  httpClient,
	}
	return deps, &stdout, &stderr
}

func tokenServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func buildJWT(claims string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(claims))
	return header + "." + payload + ".sig"
}

type capturedRequest struct {
	header http.Header
	body   []byte
}

type rewriteTransport struct {
	target *url.URL
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = t.target.Scheme
	clone.URL.Host = t.target.Host
	return http.DefaultTransport.RoundTrip(clone)
}

func completionsStream(t *testing.T, text string) (*httptest.Server, *capturedRequest) {
	t.Helper()
	captured := &capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		captured.header = r.Header.Clone()
		captured.body = body
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\n", fmt.Sprintf(`{"id":"gen_1","choices":[{"index":0,"delta":{"content":%q}}]}`, text))
		fmt.Fprint(w, `data: {"id":"gen_1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
	}))
	t.Cleanup(srv.Close)
	return srv, captured
}

func anthropicStream(t *testing.T, text string) (*httptest.Server, *capturedRequest) {
	t.Helper()
	captured := &capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		captured.header = r.Header.Clone()
		captured.body = body
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\n")
		fmt.Fprint(w, `data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":1,"output_tokens":0}}}`+"\n\n")
		fmt.Fprint(w, "event: content_block_start\n")
		fmt.Fprint(w, `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`+"\n\n")
		fmt.Fprint(w, "event: content_block_delta\n")
		fmt.Fprintf(w, "data: %s\n\n", fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%q}}`, text))
		fmt.Fprint(w, "event: content_block_stop\n")
		fmt.Fprint(w, `data: {"type":"content_block_stop","index":0}`+"\n\n")
		fmt.Fprint(w, "event: message_delta\n")
		fmt.Fprint(w, `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}`+"\n\n")
		fmt.Fprint(w, "event: message_stop\n")
		fmt.Fprint(w, `data: {"type":"message_stop"}`+"\n\n")
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
	}))
	t.Cleanup(srv.Close)
	return srv, captured
}

func readAuthStoreIfExists(t *testing.T, home string) map[string]map[string]any {
	t.Helper()
	content, err := os.ReadFile(authFile(home))
	if err != nil {
		return nil
	}
	var entries map[string]map[string]any
	if err := json.Unmarshal(content, &entries); err != nil {
		t.Fatalf("parse auth.json: %v", err)
	}
	return entries
}

func TestAuthLoginOpenRouterEndToEnd(t *testing.T) {
	tokenSrv := tokenServer(t, `{"key":"sk-or-v1-test"}`)
	home := authHome(t)
	deps, stdout, _ := authDeps(home, nil, manualAuthOptions(tokenSrv.URL), nil, "")
	if err := run([]string{"auth", "login", "openrouter"}, deps); err != nil {
		t.Fatalf("auth login openrouter: %v", err)
	}
	if !strings.Contains(stdout.String(), "signed in to openrouter") {
		t.Errorf("stdout = %q, want the success line", stdout.String())
	}
	entries := readAuthStore(t, home)
	entry, ok := entries["openrouter-oauth"]
	if !ok {
		t.Fatalf("auth.json entries = %v, want openrouter-oauth", entries)
	}
	if entry["type"] != "oauth" || entry["access"] != "sk-or-v1-test" {
		t.Errorf("openrouter-oauth entry = %v", entry)
	}
	if _, has := entry["refresh"]; has {
		t.Errorf("openrouter-oauth entry carries a refresh token: %v", entry)
	}
}

func TestAuthLoginOpenRouterStoreKeyEndToEnd(t *testing.T) {
	tokenSrv := tokenServer(t, `{"key":"sk-or-v1-key"}`)
	home := authHome(t)
	deps, stdout, _ := authDeps(home, nil, manualAuthOptions(tokenSrv.URL), nil, "")
	if err := run([]string{"auth", "login", "openrouter-oauth"}, deps); err != nil {
		t.Fatalf("auth login openrouter-oauth: %v", err)
	}
	if !strings.Contains(stdout.String(), "signed in to openrouter") {
		t.Errorf("stdout = %q", stdout.String())
	}
	entries := readAuthStore(t, home)
	if entries["openrouter-oauth"]["access"] != "sk-or-v1-key" {
		t.Errorf("openrouter-oauth entry = %v", entries["openrouter-oauth"])
	}
}

func TestAuthLoginAnthropicEndToEnd(t *testing.T) {
	tokenSrv := tokenServer(t, `{"access_token":"sk-ant-oat-test","refresh_token":"rt-1","expires_in":3600}`)
	home := authHome(t)
	deps, stdout, _ := authDeps(home, nil, manualAuthOptions(tokenSrv.URL), nil, "")
	if err := run([]string{"auth", "login", "anthropic"}, deps); err != nil {
		t.Fatalf("auth login anthropic: %v", err)
	}
	if !strings.Contains(stdout.String(), "signed in to anthropic") {
		t.Errorf("stdout = %q", stdout.String())
	}
	entry := readAuthStore(t, home)["anthropic-oauth"]
	if entry["type"] != "oauth" || entry["access"] != "sk-ant-oat-test" || entry["refresh"] != "rt-1" {
		t.Errorf("anthropic-oauth entry = %v", entry)
	}
}

func TestAuthLoginCodexEndToEnd(t *testing.T) {
	claims := `{"sub":"u1","https://api.openai.com/auth":{"chatgpt_account_id":"acct_1"}}`
	tokenSrv := tokenServer(t, `{"access_token":"`+buildJWT(claims)+`","refresh_token":"rt-1","expires_in":1800}`)
	home := authHome(t)
	deps, stdout, _ := authDeps(home, nil, manualAuthOptions(tokenSrv.URL), nil, "")
	if err := run([]string{"auth", "login", "codex"}, deps); err != nil {
		t.Fatalf("auth login codex: %v", err)
	}
	if !strings.Contains(stdout.String(), "signed in to codex") {
		t.Errorf("stdout = %q", stdout.String())
	}
	entry := readAuthStore(t, home)["codex"]
	if entry["type"] != "oauth" || entry["access"] == "" || entry["refresh"] != "rt-1" {
		t.Errorf("codex entry = %v", entry)
	}
	if entry["accountId"] != "acct_1" {
		t.Errorf("codex entry accountId = %v", entry)
	}
}

func TestAuthLoginAPIKeyFromEnv(t *testing.T) {
	home := authHome(t)
	deps, stdout, _ := authDeps(home, map[string]string{"DEEPSEEK_API_KEY": "sk-ds-env"}, nil, nil, "")
	if err := run([]string{"auth", "login", "deepseek", "--api-key"}, deps); err != nil {
		t.Fatalf("auth login deepseek --api-key: %v", err)
	}
	if !strings.Contains(stdout.String(), "stored API key for deepseek") {
		t.Errorf("stdout = %q", stdout.String())
	}
	entry := readAuthStore(t, home)["deepseek"]
	if entry["type"] != "api_key" || entry["key"] != "sk-ds-env" {
		t.Errorf("deepseek entry = %v", entry)
	}
}

func TestAuthLoginAPIKeyFromStdin(t *testing.T) {
	home := authHome(t)
	deps, stdout, _ := authDeps(home, nil, nil, nil, "sk-ds-stdin\n")
	if err := run([]string{"auth", "login", "deepseek", "--api-key"}, deps); err != nil {
		t.Fatalf("auth login deepseek --api-key: %v", err)
	}
	if !strings.Contains(stdout.String(), "stored API key for deepseek") {
		t.Errorf("stdout = %q", stdout.String())
	}
	entry := readAuthStore(t, home)["deepseek"]
	if entry["type"] != "api_key" || entry["key"] != "sk-ds-stdin" {
		t.Errorf("deepseek entry = %v", entry)
	}
}

func TestAuthLoginAPIKeyEmptyStdin(t *testing.T) {
	home := authHome(t)
	deps, _, stderr := authDeps(home, nil, nil, nil, "")
	if err := run([]string{"auth", "login", "deepseek", "--api-key"}, deps); err == nil {
		t.Fatal("want error for empty API key")
	} else if !strings.Contains(err.Error(), "empty API key") {
		t.Errorf("err = %v", err)
	}
	if _, ok := readAuthStoreIfExists(t, home)["deepseek"]; ok {
		t.Error("empty login stored an entry")
	}
	if !strings.Contains(stderr.String(), "smidja:") {
		t.Errorf("stderr = %q, want the smidja error line", stderr.String())
	}
}

func TestAuthLoginUnknownProvider(t *testing.T) {
	home := authHome(t)
	deps, _, _ := authDeps(home, nil, nil, nil, "")
	err := run([]string{"auth", "login", "bogus"}, deps)
	if err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Errorf("err = %v, want unknown provider", err)
	}
	err = run([]string{"auth", "login", "bogus", "--api-key"}, deps)
	if err == nil || !strings.Contains(err.Error(), "not an API-key provider") {
		t.Errorf("err = %v, want not an API-key provider", err)
	}
	err = run([]string{"auth", "login", "codex", "--api-key"}, deps)
	if err == nil || !strings.Contains(err.Error(), "not an API-key provider") {
		t.Errorf("err = %v, want codex rejected in api-key mode", err)
	}
}

func TestAuthLoginTelegramFromStdin(t *testing.T) {
	home := authHome(t)
	deps, stdout, stderr := authDeps(home, nil, nil, nil, "123456:ABC-secret\n")
	if err := run([]string{"auth", "login", "telegram"}, deps); err != nil {
		t.Fatalf("auth login telegram: %v", err)
	}
	if !strings.Contains(stdout.String(), "stored token for telegram") {
		t.Errorf("stdout = %q, want the success line", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Telegram bot token:") {
		t.Errorf("stderr = %q, want the interactive prompt", stderr.String())
	}
	entry := readAuthStore(t, home)["telegram"]
	if entry["type"] != "api_key" || entry["key"] != "123456:ABC-secret" {
		t.Errorf("telegram entry = %v", entry)
	}
	assertRedacted(t, "123456:ABC-secret", stdout.String(), stderr.String())
}

func TestAuthLoginTelegramAPIKeyFlagFromEnv(t *testing.T) {
	home := authHome(t)
	deps, stdout, stderr := authDeps(home, map[string]string{"TELEGRAM_BOT_TOKEN": "789:env-secret"}, nil, nil, "")
	if err := run([]string{"auth", "login", "telegram", "--api-key"}, deps); err != nil {
		t.Fatalf("auth login telegram --api-key: %v", err)
	}
	if !strings.Contains(stdout.String(), "stored token for telegram") {
		t.Errorf("stdout = %q, want the success line", stdout.String())
	}
	if strings.Contains(stderr.String(), "Telegram bot token:") {
		t.Errorf("stderr = %q, the env flow must not prompt", stderr.String())
	}
	entry := readAuthStore(t, home)["telegram"]
	if entry["type"] != "api_key" || entry["key"] != "789:env-secret" {
		t.Errorf("telegram entry = %v", entry)
	}
	assertRedacted(t, "789:env-secret", stdout.String(), stderr.String())
}

func TestAuthLoginTelegramAPIKeyFlagFromStdin(t *testing.T) {
	home := authHome(t)
	deps, stdout, stderr := authDeps(home, nil, nil, nil, "1011:stdin-secret\n")
	if err := run([]string{"auth", "login", "telegram", "--api-key"}, deps); err != nil {
		t.Fatalf("auth login telegram --api-key: %v", err)
	}
	if !strings.Contains(stdout.String(), "stored token for telegram") {
		t.Errorf("stdout = %q, want the success line", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Telegram bot token:") {
		t.Errorf("stderr = %q, want the interactive prompt", stderr.String())
	}
	entry := readAuthStore(t, home)["telegram"]
	if entry["type"] != "api_key" || entry["key"] != "1011:stdin-secret" {
		t.Errorf("telegram entry = %v", entry)
	}
	assertRedacted(t, "1011:stdin-secret", stdout.String(), stderr.String())
}

func TestAuthLoginTelegramEmptyToken(t *testing.T) {
	home := authHome(t)
	deps, _, stderr := authDeps(home, nil, nil, nil, "")
	err := run([]string{"auth", "login", "telegram"}, deps)
	if err == nil || !strings.Contains(err.Error(), "empty token") {
		t.Errorf("err = %v, want empty token", err)
	}
	if _, ok := readAuthStoreIfExists(t, home)["telegram"]; ok {
		t.Error("empty login stored an entry")
	}
	if !strings.Contains(stderr.String(), "smidja:") {
		t.Errorf("stderr = %q, want the smidja error line", stderr.String())
	}
}

func TestAuthLoginWebFromEnv(t *testing.T) {
	home := authHome(t)
	deps, stdout, _ := authDeps(home, map[string]string{"SMIDJA_WEB_TOKEN": "web-secret"}, nil, nil, "")
	if err := run([]string{"auth", "login", "web", "--api-key"}, deps); err != nil {
		t.Fatalf("auth login web --api-key: %v", err)
	}
	if !strings.Contains(stdout.String(), "stored token for web") {
		t.Errorf("stdout = %q, want the success line", stdout.String())
	}
	entry := readAuthStore(t, home)["web"]
	if entry["type"] != "api_key" || entry["key"] != "web-secret" {
		t.Errorf("web entry = %v", entry)
	}
	assertRedacted(t, "web-secret", stdout.String())
}

func TestAuthLoginWebFromStdin(t *testing.T) {
	home := authHome(t)
	deps, stdout, stderr := authDeps(home, nil, nil, nil, "web-stdin-secret\n")
	if err := run([]string{"auth", "login", "web"}, deps); err != nil {
		t.Fatalf("auth login web: %v", err)
	}
	if !strings.Contains(stdout.String(), "stored token for web") {
		t.Errorf("stdout = %q, want the success line", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Smidja web token:") {
		t.Errorf("stderr = %q, want the interactive prompt", stderr.String())
	}
	entry := readAuthStore(t, home)["web"]
	if entry["type"] != "api_key" || entry["key"] != "web-stdin-secret" {
		t.Errorf("web entry = %v", entry)
	}
	assertRedacted(t, "web-stdin-secret", stdout.String(), stderr.String())
}

func TestInfraTokenTitleFallback(t *testing.T) {
	if got := infraTokenTitle("future"); got != "future token" {
		t.Errorf("infraTokenTitle(future) = %q, want the generic fallback", got)
	}
}

func TestAuthStatusShowsInfraCredentials(t *testing.T) {
	home := authHome(t)
	seedStore(t, home, map[string]authstore.Entry{
		"telegram": {Type: "api_key", Key: "tg-status-secret"},
	})
	deps, stdout, _ := authDeps(home, map[string]string{"SMIDJA_WEB_TOKEN": "web-status-secret"}, nil, nil, "")
	if err := run([]string{"auth", "status"}, deps); err != nil {
		t.Fatalf("auth status: %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"telegram", "web", "configured (store)", "configured (env)"} {
		if !strings.Contains(out, want) {
			t.Errorf("status output missing %q:\n%s", want, out)
		}
	}
	for _, secret := range []string{"tg-status-secret", "web-status-secret"} {
		if strings.Contains(out, secret) {
			t.Errorf("status output leaked %q:\n%s", secret, out)
		}
	}
}

func TestAuthLoginTelegramCorruptStore(t *testing.T) {
	home := authHome(t)
	if err := os.MkdirAll(filepath.Dir(authFile(home)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(authFile(home), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	deps, _, _ := authDeps(home, nil, nil, nil, "123:abc\n")
	err := run([]string{"auth", "login", "telegram"}, deps)
	if err == nil || !strings.Contains(err.Error(), "auth.json") {
		t.Errorf("err = %v, want the corrupt store error", err)
	}
}

func TestAuthLoginTelegramStoreWriteError(t *testing.T) {
	home := authHome(t)
	dir := filepath.Dir(authFile(home))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	deps, _, _ := authDeps(home, nil, nil, nil, "123:abc\n")
	err := run([]string{"auth", "login", "telegram"}, deps)
	if err == nil || !strings.Contains(err.Error(), "auth login telegram") {
		t.Errorf("err = %v, want the store write failure wrapped", err)
	}
}

func assertRedacted(t *testing.T, token string, outputs ...string) {
	t.Helper()
	for i, out := range outputs {
		if strings.Contains(out, token) {
			t.Errorf("output %d leaked the token %q", i, token)
		}
	}
}

func TestAuthDispatchErrors(t *testing.T) {
	home := authHome(t)
	deps, _, stderr := authDeps(home, nil, nil, nil, "")
	err := run([]string{"auth"}, deps)
	if err == nil || !strings.Contains(err.Error(), "auth: a subcommand is required") {
		t.Errorf("err = %v, want subcommand required", err)
	}
	if !strings.Contains(stderr.String(), "usage: smidja auth") {
		t.Errorf("stderr = %q, want auth usage", stderr.String())
	}
	err = run([]string{"auth", "bogus"}, deps)
	if err == nil || !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("err = %v, want unknown subcommand", err)
	}
	var stdout, helpErr bytes.Buffer
	helpDeps := &Deps{Env: envFrom(nil), Getwd: func() (string, error) { return home, nil }, Home: func() string { return home },
		Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &helpErr}
	if err := run([]string{"auth", "help"}, helpDeps); err != nil {
		t.Fatalf("auth help: %v", err)
	}
	if !strings.Contains(stdout.String(), "usage: smidja auth") {
		t.Errorf("stdout = %q, want usage", stdout.String())
	}
}

func TestAuthLoginMissingArg(t *testing.T) {
	home := authHome(t)
	deps, _, _ := authDeps(home, nil, nil, nil, "")
	err := run([]string{"auth", "login"}, deps)
	if err == nil || !strings.Contains(err.Error(), "exactly one provider argument") {
		t.Errorf("err = %v, want missing provider", err)
	}
	err = run([]string{"auth", "logout"}, deps)
	if err == nil || !strings.Contains(err.Error(), "exactly one provider argument") {
		t.Errorf("err = %v, want missing provider", err)
	}
}

func TestAuthLogout(t *testing.T) {
	home := authHome(t)
	seedStore(t, home, map[string]authstore.Entry{
		"openrouter-oauth": {Type: "oauth", Access: "sk-or-v1-a", Expires: 9007199254740991},
	})
	deps, stdout, _ := authDeps(home, nil, nil, nil, "")
	if err := run([]string{"auth", "logout", "openrouter"}, deps); err != nil {
		t.Fatalf("auth logout openrouter: %v", err)
	}
	if !strings.Contains(stdout.String(), "signed out of openrouter") {
		t.Errorf("stdout = %q", stdout.String())
	}
	if _, ok := readAuthStore(t, home)["openrouter-oauth"]; ok {
		t.Error("openrouter-oauth entry still present after logout")
	}
}

func TestAuthLogoutStoreKey(t *testing.T) {
	home := authHome(t)
	seedStore(t, home, map[string]authstore.Entry{
		"deepseek": {Type: "api_key", Key: "sk-ds-1"},
	})
	deps, _, _ := authDeps(home, nil, nil, nil, "")
	if err := run([]string{"auth", "logout", "deepseek"}, deps); err != nil {
		t.Fatalf("auth logout deepseek: %v", err)
	}
	if _, ok := readAuthStore(t, home)["deepseek"]; ok {
		t.Error("deepseek entry still present after logout")
	}
}

func TestAuthLogoutNoEntry(t *testing.T) {
	home := authHome(t)
	deps, stdout, _ := authDeps(home, nil, nil, nil, "")
	if err := run([]string{"auth", "logout", "never"}, deps); err != nil {
		t.Fatalf("auth logout never: %v", err)
	}
	if !strings.Contains(stdout.String(), "no stored credential for never") {
		t.Errorf("stdout = %q", stdout.String())
	}
}

func TestAuthStatusMasking(t *testing.T) {
	home := authHome(t)
	seedStore(t, home, map[string]authstore.Entry{
		"deepseek":        {Type: "api_key", Key: "sk-ds-secret"},
		"anthropic-oauth": {Type: "oauth", Access: "sk-ant-oat-secret", Refresh: "rt-secret", Expires: time.Now().Add(time.Hour).UnixMilli()},
	})
	deps, stdout, _ := authDeps(home, map[string]string{"OPENROUTER_API_KEY": "sk-or-env-secret"}, nil, nil, "")
	if err := run([]string{"auth", "status"}, deps); err != nil {
		t.Fatalf("auth status: %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"provider", "openrouter", "deepseek", "anthropic-oauth", "codex", "configured (env)", "configured (store)", "not configured"} {
		if !strings.Contains(out, want) {
			t.Errorf("status output missing %q:\n%s", want, out)
		}
	}
	for _, secret := range []string{"sk-or-env-secret", "sk-ds-secret", "sk-ant-oat-secret", "rt-secret"} {
		if strings.Contains(out, secret) {
			t.Errorf("status output leaked %q:\n%s", secret, out)
		}
	}
}

func TestAuthStatusUnexpectedArg(t *testing.T) {
	home := authHome(t)
	deps, _, _ := authDeps(home, nil, nil, nil, "")
	err := run([]string{"auth", "status", "extra"}, deps)
	if err == nil || !strings.Contains(err.Error(), "unexpected argument") {
		t.Errorf("err = %v, want unexpected argument", err)
	}
}

func TestOAuthCredentialResolve(t *testing.T) {
	home := authHome(t)
	refreshCalled := false
	cred := &oauthCredential{
		store: mustStore(t, home),
		id:    "anthropic-oauth",
		name:  "anthropic",
		refresh: func(ctx context.Context, entry authstore.Entry, opts ...oauth.Options) (authstore.Entry, error) {
			refreshCalled = true
			return authstore.Entry{Type: "oauth", Access: "sk-ant-oat-new", Refresh: "rt-new", Expires: time.Now().Add(time.Hour).UnixMilli()}, nil
		},
	}
	cred.store.Set("anthropic-oauth", authstore.Entry{Type: "oauth", Access: "sk-ant-oat-old", Refresh: "rt-old", Expires: time.Now().Add(-time.Minute).UnixMilli()})
	token, err := cred.resolve(context.Background())
	if err != nil {
		t.Fatalf("resolve expired: %v", err)
	}
	if token != "sk-ant-oat-new" {
		t.Errorf("token = %q, want the refreshed access", token)
	}
	if !refreshCalled {
		t.Error("expired entry did not refresh")
	}
	stored, _ := cred.store.Get("anthropic-oauth")
	if stored.Access != "sk-ant-oat-new" {
		t.Errorf("stored access = %q, want the refreshed entry persisted", stored.Access)
	}
}

func TestOAuthCredentialResolveFresh(t *testing.T) {
	home := authHome(t)
	cred := &oauthCredential{
		store: mustStore(t, home),
		id:    "xai-subscription",
		name:  "xai",
		refresh: func(ctx context.Context, entry authstore.Entry, opts ...oauth.Options) (authstore.Entry, error) {
			t.Error("fresh entry must not refresh")
			return entry, nil
		},
	}
	cred.store.Set("xai-subscription", authstore.Entry{Type: "oauth", Access: "xai-tok", Expires: time.Now().Add(time.Hour).UnixMilli()})
	token, err := cred.resolve(context.Background())
	if err != nil {
		t.Fatalf("resolve fresh: %v", err)
	}
	if token != "xai-tok" {
		t.Errorf("token = %q, want the stored access", token)
	}
}

func TestOAuthCredentialResolveMissing(t *testing.T) {
	home := authHome(t)
	cred := &oauthCredential{store: mustStore(t, home), id: "codex", name: "codex"}
	_, err := cred.resolve(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no oauth credential for codex") {
		t.Errorf("err = %v, want missing credential", err)
	}
}

func mustStore(t *testing.T, home string) *authstore.Store {
	t.Helper()
	store, err := authstore.Load(authFile(home))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestProviderDefaultModel(t *testing.T) {
	cases := map[string]string{
		"deepseek":          "deepseek-v4-pro",
		"openrouter":        "anthropic/claude-sonnet-4.5",
		"openrouter-oauth":  "anthropic/claude-sonnet-4.5",
		"anthropic-oauth":   "claude-sonnet-4-6",
		"codex":             "gpt-5.4",
		"xai-subscription":  "grok-4.6",
		"kimi-coding-oauth": "kimi-for-coding",
		"kimi-coding":       "kimi-for-coding",
	}
	for provider, want := range cases {
		got, ok := providerDefaultModel(provider)
		if !ok || got != want {
			t.Errorf("providerDefaultModel(%q) = %q, %v; want %q", provider, got, ok, want)
		}
	}
	if _, ok := providerDefaultModel("bogus"); ok {
		t.Error("providerDefaultModel(bogus) reported a model")
	}
}

func TestBuildProviderClientManifest(t *testing.T) {
	home := authHome(t)
	deps, _, _ := authDeps(home, map[string]string{"DEEPSEEK_API_KEY": "sk-ds-1"}, nil, nil, "")
	client, err := buildProviderClient(deps, "deepseek")
	if err != nil {
		t.Fatalf("buildProviderClient(deepseek): %v", err)
	}
	if client == nil {
		t.Fatal("nil client")
	}
}

func TestBuildProviderClientOAuthFriendly(t *testing.T) {
	home := authHome(t)
	seedStore(t, home, map[string]authstore.Entry{
		"anthropic-oauth": {Type: "oauth", Access: "sk-ant-oat-1", Refresh: "rt-1", Expires: time.Now().Add(time.Hour).UnixMilli()},
	})
	deps, _, _ := authDeps(home, nil, nil, nil, "")
	client, err := buildProviderClient(deps, "anthropic")
	if err != nil {
		t.Fatalf("buildProviderClient(anthropic): %v", err)
	}
	if client == nil {
		t.Fatal("nil client")
	}
}

func TestBuildProviderClientOAuthErrors(t *testing.T) {
	home := authHome(t)
	deps, _, _ := authDeps(home, nil, nil, nil, "")
	_, err := buildProviderClient(deps, "codex")
	if err == nil || !strings.Contains(err.Error(), "no oauth credential for codex") {
		t.Errorf("err = %v, want the oauth guidance error", err)
	}
	_, err = buildProviderClient(deps, "bogus")
	if err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Errorf("err = %v, want unknown provider", err)
	}
}

func TestRunProviderFlagDeepseek(t *testing.T) {
	srv, captured := completionsStream(t, "hello from deepseek")
	target, _ := url.Parse(srv.URL)
	home := authHome(t)
	deps, stdout, _ := authDeps(home, map[string]string{"DEEPSEEK_API_KEY": "sk-ds-1"}, nil, &http.Client{Transport: &rewriteTransport{target: target}}, "")
	if err := run([]string{"-p", "hi", "-provider", "deepseek"}, deps); err != nil {
		t.Fatalf("run -provider deepseek: %v", err)
	}
	if !strings.Contains(stdout.String(), "hello from deepseek") {
		t.Errorf("stdout = %q, want the streamed text", stdout.String())
	}
	var req map[string]any
	if err := json.Unmarshal(captured.body, &req); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if req["model"] != "deepseek-v4-pro" {
		t.Errorf("model = %v, want the provider default", req["model"])
	}
	if got := captured.header.Get("Authorization"); got != "Bearer sk-ds-1" {
		t.Errorf("Authorization = %q, want the env credential", got)
	}
}

func TestRunProviderFlagOpenRouterOAuth(t *testing.T) {
	srv, captured := completionsStream(t, "hello from openrouter oauth")
	target, _ := url.Parse(srv.URL)
	home := authHome(t)
	seedStore(t, home, map[string]authstore.Entry{
		"openrouter-oauth": {Type: "oauth", Access: "sk-or-v1-oauth", Expires: 9007199254740991},
	})
	deps, stdout, _ := authDeps(home, nil, nil, &http.Client{Transport: &rewriteTransport{target: target}}, "")
	if err := run([]string{"-p", "hi", "-provider", "openrouter-oauth"}, deps); err != nil {
		t.Fatalf("run -provider openrouter-oauth: %v", err)
	}
	if !strings.Contains(stdout.String(), "hello from openrouter oauth") {
		t.Errorf("stdout = %q", stdout.String())
	}
	var req map[string]any
	if err := json.Unmarshal(captured.body, &req); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if req["model"] != "anthropic/claude-sonnet-4.5" {
		t.Errorf("model = %v, want the oauth default", req["model"])
	}
	if got := captured.header.Get("Authorization"); got != "Bearer sk-or-v1-oauth" {
		t.Errorf("Authorization = %q, want the stored oauth access", got)
	}
}

func TestRunProviderFlagAnthropicOAuth(t *testing.T) {
	srv, captured := anthropicStream(t, "hello from anthropic oauth")
	target, _ := url.Parse(srv.URL)
	home := authHome(t)
	seedStore(t, home, map[string]authstore.Entry{
		"anthropic-oauth": {Type: "oauth", Access: "sk-ant-oat-1", Refresh: "rt-1", Expires: time.Now().Add(time.Hour).UnixMilli()},
	})
	deps, stdout, _ := authDeps(home, nil, nil, &http.Client{Transport: &rewriteTransport{target: target}}, "")
	if err := run([]string{"-p", "hi", "-provider", "anthropic-oauth"}, deps); err != nil {
		t.Fatalf("run -provider anthropic-oauth: %v", err)
	}
	if !strings.Contains(stdout.String(), "hello from anthropic oauth") {
		t.Errorf("stdout = %q", stdout.String())
	}
	if got := captured.header.Get("Authorization"); got != "Bearer sk-ant-oat-1" {
		t.Errorf("Authorization = %q, want the oauth bearer", got)
	}
	if got := captured.header.Get("anthropic-beta"); !strings.Contains(got, "claude-code-20250219") {
		t.Errorf("anthropic-beta = %q, want the Claude Code identity marker", got)
	}
	var req map[string]any
	if err := json.Unmarshal(captured.body, &req); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if req["model"] != "claude-sonnet-4-6" {
		t.Errorf("model = %v, want the anthropic oauth default", req["model"])
	}
}

func TestRunProviderFlagKimiOAuth(t *testing.T) {
	srv, captured := anthropicStream(t, "hello from kimi oauth")
	target, _ := url.Parse(srv.URL)
	home := authHome(t)
	seedStore(t, home, map[string]authstore.Entry{
		"kimi-coding-oauth": {Type: "oauth", Access: "kimi-tok", Refresh: "rt-1", Expires: time.Now().Add(time.Hour).UnixMilli()},
	})
	deps, stdout, _ := authDeps(home, nil, nil, &http.Client{Transport: &rewriteTransport{target: target}}, "")
	if err := run([]string{"-p", "hi", "-provider", "kimi-coding-oauth"}, deps); err != nil {
		t.Fatalf("run -provider kimi-coding-oauth: %v", err)
	}
	if !strings.Contains(stdout.String(), "hello from kimi oauth") {
		t.Errorf("stdout = %q", stdout.String())
	}
	if got := captured.header.Get("x-api-key"); got != "kimi-tok" {
		t.Errorf("x-api-key = %q, want the kimi access token", got)
	}
	if got := captured.header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, kimi must not send a bearer", got)
	}
}

func TestRunProviderFlagXaiSubscription(t *testing.T) {
	srv, captured := completionsStream(t, "hello from xai")
	target, _ := url.Parse(srv.URL)
	home := authHome(t)
	seedStore(t, home, map[string]authstore.Entry{
		"xai-subscription": {Type: "oauth", Access: "xai-tok", Refresh: "rt-1", Expires: time.Now().Add(time.Hour).UnixMilli()},
	})
	deps, stdout, _ := authDeps(home, nil, nil, &http.Client{Transport: &rewriteTransport{target: target}}, "")
	if err := run([]string{"-p", "hi", "-provider", "xai-subscription"}, deps); err != nil {
		t.Fatalf("run -provider xai-subscription: %v", err)
	}
	if !strings.Contains(stdout.String(), "hello from xai") {
		t.Errorf("stdout = %q", stdout.String())
	}
	if got := captured.header.Get("Authorization"); got != "Bearer xai-tok" {
		t.Errorf("Authorization = %q, want the xai access", got)
	}
	var req map[string]any
	if err := json.Unmarshal(captured.body, &req); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if req["model"] != "grok-4.6" {
		t.Errorf("model = %v, want the xai default", req["model"])
	}
}

func TestRunProviderFlagUnknown(t *testing.T) {
	home := authHome(t)
	deps, _, stderr := authDeps(home, nil, nil, nil, "")
	err := run([]string{"-p", "hi", "-provider", "bogus"}, deps)
	if err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Errorf("err = %v, want unknown provider", err)
	}
	if !strings.Contains(stderr.String(), "smidja:") {
		t.Errorf("stderr = %q, want the smidja error line", stderr.String())
	}
}

func TestRunProviderFlagModelFlagWins(t *testing.T) {
	srv, captured := completionsStream(t, "hello")
	target, _ := url.Parse(srv.URL)
	home := authHome(t)
	deps, _, _ := authDeps(home, map[string]string{"DEEPSEEK_API_KEY": "sk-ds-1"}, nil, &http.Client{Transport: &rewriteTransport{target: target}}, "")
	if err := run([]string{"-p", "hi", "-provider", "deepseek", "-model", "custom/model"}, deps); err != nil {
		t.Fatalf("run: %v", err)
	}
	var req map[string]any
	if err := json.Unmarshal(captured.body, &req); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if req["model"] != "custom/model" {
		t.Errorf("model = %v, want the explicit -model", req["model"])
	}
}

func TestRunProviderFlagDefaultKeptWithoutProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\n", `{"id":"gen_1","choices":[{"index":0,"delta":{"content":"default client"}}]}`)
		fmt.Fprint(w, "data: [DONE]\n\n")
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
	}))
	defer srv.Close()
	home := authHome(t)
	var stdout, stderr bytes.Buffer
	err := run([]string{"-p", "hi"}, &Deps{
		Env: envFrom(map[string]string{
			"SMIDJA_OPENROUTER_URL": srv.URL,
			"OPENROUTER_API_KEY":    "sk-default",
		}),
		Getwd:  func() (string, error) { return home, nil },
		Home:   func() string { return home },
		Stdin:  strings.NewReader(""),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("run without -provider: %v", err)
	}
	if !strings.Contains(stdout.String(), "default client") {
		t.Errorf("stdout = %q, want the default client response", stdout.String())
	}
}

func TestAuthLoginOAuthFlowError(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"invalid_request","error_description":"bad code"}`)
	}))
	t.Cleanup(tokenSrv.Close)
	home := authHome(t)
	deps, _, _ := authDeps(home, nil, manualAuthOptions(tokenSrv.URL), nil, "")
	err := run([]string{"auth", "login", "openrouter"}, deps)
	if err == nil || !strings.Contains(err.Error(), "auth login openrouter") {
		t.Errorf("err = %v, want the login failure wrapped", err)
	}
	if _, ok := readAuthStoreIfExists(t, home)["openrouter-oauth"]; ok {
		t.Error("failed login stored an entry")
	}
}

func TestAuthLoginCorruptStore(t *testing.T) {
	home := authHome(t)
	if err := os.MkdirAll(filepath.Dir(authFile(home)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(authFile(home), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	deps, _, _ := authDeps(home, nil, manualAuthOptions("http://127.0.0.1:1"), nil, "")
	err := run([]string{"auth", "login", "openrouter"}, deps)
	if err == nil || !strings.Contains(err.Error(), "auth.json") {
		t.Errorf("err = %v, want the corrupt store error", err)
	}
	err = run([]string{"auth", "logout", "openrouter"}, deps)
	if err == nil || !strings.Contains(err.Error(), "auth.json") {
		t.Errorf("logout err = %v, want the corrupt store error", err)
	}
	err = run([]string{"auth", "status"}, deps)
	if err == nil || !strings.Contains(err.Error(), "auth.json") {
		t.Errorf("status err = %v, want the corrupt store error", err)
	}
}

func TestAuthSubcommandFlagErrors(t *testing.T) {
	home := authHome(t)
	deps, _, _ := authDeps(home, nil, nil, nil, "")
	err := run([]string{"auth", "login", "--bogus", "openrouter"}, deps)
	if err == nil || !strings.Contains(err.Error(), "flag provided but not defined: -bogus") {
		t.Errorf("login err = %v, want the flag error", err)
	}
	err = run([]string{"auth", "logout", "--bogus", "openrouter"}, deps)
	if err == nil || !strings.Contains(err.Error(), "flag provided but not defined: -bogus") {
		t.Errorf("logout err = %v, want the flag error", err)
	}
	err = run([]string{"auth", "status", "--bogus"}, deps)
	if err == nil || !strings.Contains(err.Error(), "flag provided but not defined: -bogus") {
		t.Errorf("status err = %v, want the flag error", err)
	}
}

func TestAuthOptionsForDefault(t *testing.T) {
	home := authHome(t)
	deps, _, _ := authDeps(home, nil, nil, nil, "")
	p, _ := oauthProviderByID("openrouter")
	opts := authOptionsFor(deps, p)
	if opts.OpenBrowser == nil || opts.ManualCode == nil || opts.DeviceCode == nil {
		t.Errorf("default options missing callbacks: %+v", opts)
	}
	if opts.CallbackPort != 0 || opts.TokenURL != "" {
		t.Errorf("default options carry test seams: %+v", opts)
	}
	_, err := opts.ManualCode(context.Background(), "prompt")
	if err != nil {
		t.Fatalf("manual code on empty stdin: %v", err)
	}
	if opts.DeviceCode != nil {
		opts.DeviceCode(oauth.DeviceCode{UserCode: "ABCD-EFGH", VerificationURI: "https://example.test/device"})
	}
}

func TestAuthOptionsForSeam(t *testing.T) {
	home := authHome(t)
	var received string
	deps, _, _ := authDeps(home, nil, func(provider string) oauth.Options {
		received = provider
		return oauth.Options{TokenURL: "https://seam.test"}
	}, nil, "")
	p, _ := oauthProviderByID("openrouter-oauth")
	opts := authOptionsFor(deps, p)
	if received != "openrouter-oauth" {
		t.Errorf("seam received provider %q", received)
	}
	if opts.TokenURL != "https://seam.test" {
		t.Errorf("opts = %+v, want the seam options", opts)
	}
}

func TestBrowserCommand(t *testing.T) {
	tests := []struct {
		name       string
		goos       string
		wantBinary string
		wantArgs   []string
	}{
		{name: "linux", goos: "linux", wantBinary: "xdg-open", wantArgs: nil},
		{name: "darwin", goos: "darwin", wantBinary: "open", wantArgs: nil},
		{name: "windows", goos: "windows", wantBinary: "rundll32", wantArgs: []string{"url.dll,FileProtocolHandler"}},
		{name: "unknown defaults to xdg-open", goos: "plan9", wantBinary: "xdg-open", wantArgs: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binary, args := browserCommand(tt.goos)
			if binary != tt.wantBinary {
				t.Errorf("browserCommand(%q) binary = %q, want %q", tt.goos, binary, tt.wantBinary)
			}
			if !slices.Equal(args, tt.wantArgs) {
				t.Errorf("browserCommand(%q) args = %v, want %v", tt.goos, args, tt.wantArgs)
			}
		})
	}
}

func TestOpenBrowserURLMissingOpener(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	currentOS = "windows"
	t.Cleanup(func() { currentOS = runtime.GOOS })
	err := openBrowserURL("https://127.0.0.1:1/")
	if err == nil {
		t.Fatal("openBrowserURL succeeded with an empty PATH")
	}
	if !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("err = %v, want a PATH lookup error", err)
	}
	if !strings.Contains(err.Error(), "rundll32") {
		t.Errorf("err = %v, want the windows opener binary in the error", err)
	}
}

func TestStatusHelpers(t *testing.T) {
	spec := manifest.Spec{ID: "deepseek", EnvVar: "DEEPSEEK_API_KEY"}
	if got := manifestStatus(spec, nil, nil); got != "not configured" {
		t.Errorf("manifestStatus with nil store/env = %q", got)
	}
	if got := oauthStatus(oauthProvider{id: "codex", name: "codex"}, nil); got != "not configured" {
		t.Errorf("oauthStatus with nil store = %q", got)
	}
	if got := combinedStatus(true, true); got != "configured (env + store)" {
		t.Errorf("combinedStatus(true, true) = %q", got)
	}
}

func TestOAuthCredentialResolveRefreshError(t *testing.T) {
	home := authHome(t)
	cred := &oauthCredential{
		store: mustStore(t, home),
		id:    "anthropic-oauth",
		name:  "anthropic",
		refresh: func(ctx context.Context, entry authstore.Entry, opts ...oauth.Options) (authstore.Entry, error) {
			return authstore.Entry{}, fmt.Errorf("refresh failed")
		},
	}
	cred.store.Set("anthropic-oauth", authstore.Entry{Type: "oauth", Access: "old", Expires: time.Now().Add(-time.Minute).UnixMilli()})
	_, err := cred.resolve(context.Background())
	if err == nil || !strings.Contains(err.Error(), "refresh token: refresh failed") {
		t.Errorf("err = %v, want the refresh failure", err)
	}
}

func TestOAuthCredentialResolveEmptyRefreshResult(t *testing.T) {
	home := authHome(t)
	cred := &oauthCredential{
		store: mustStore(t, home),
		id:    "codex",
		name:  "codex",
		refresh: func(ctx context.Context, entry authstore.Entry, opts ...oauth.Options) (authstore.Entry, error) {
			return authstore.Entry{}, nil
		},
	}
	cred.store.Set("codex", authstore.Entry{Type: "oauth", Access: "old", Expires: time.Now().Add(-time.Minute).UnixMilli()})
	_, err := cred.resolve(context.Background())
	if err == nil || !strings.Contains(err.Error(), "empty token") {
		t.Errorf("err = %v, want the empty token error", err)
	}
}

func TestAuthSubcommandHelp(t *testing.T) {
	home := authHome(t)
	for _, sub := range []string{"login", "logout", "status"} {
		var stdout, stderr bytes.Buffer
		deps := &Deps{Env: envFrom(nil), Getwd: func() (string, error) { return home, nil }, Home: func() string { return home },
			Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr}
		if err := run([]string{"auth", sub, "-h"}, deps); err != nil {
			t.Fatalf("auth %s -h: %v", sub, err)
		}
		if !strings.Contains(stderr.String(), "usage: smidja auth") {
			t.Errorf("auth %s -h stderr = %q, want usage", sub, stderr.String())
		}
	}
}

func TestAuthStorePathUnderHome(t *testing.T) {
	home := authHome(t)
	if got := authStorePath(&Deps{Home: func() string { return home }}); got != filepath.Join(home, ".smidja", "auth.json") {
		t.Errorf("authStorePath = %q", got)
	}
}
