package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/config"
	"github.com/digitalygo/smidja/internal/gateway/telegram"
	"github.com/digitalygo/smidja/internal/models"
	"github.com/digitalygo/smidja/internal/session"
)

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type cliFakeBot struct {
	t         *testing.T
	mu        sync.Mutex
	queue     []telegram.Update
	wake      chan struct{}
	richCalls []string
	nextMsgID int64
}

func newCLIFakeBot(t *testing.T) *cliFakeBot {
	t.Helper()
	f := &cliFakeBot{t: t, wake: make(chan struct{}, 1)}
	return f
}

func (f *cliFakeBot) enqueue(updates ...telegram.Update) {
	f.mu.Lock()
	f.queue = append(f.queue, updates...)
	f.mu.Unlock()
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

func (f *cliFakeBot) richMessageTexts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.richCalls...)
}

func (f *cliFakeBot) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/bot")
		_, method, ok := strings.Cut(rest, "/")
		if !ok {
			f.t.Errorf("unexpected request path %s", r.URL.Path)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if err := r.ParseForm(); err != nil {
			f.t.Errorf("parse form: %v", err)
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		switch method {
		case "getMe":
			f.writeResult(w, map[string]any{"id": 111, "is_bot": true, "first_name": "smidja", "username": "smidja_test_bot"})
		case "deleteWebhook":
			f.writeResult(w, true)
		case "getUpdates":
			offset, _ := strconv.ParseInt(r.PostForm.Get("offset"), 10, 64)
			f.serveUpdates(w, r, offset)
		case "sendChatAction":
			f.writeResult(w, true)
		case "sendRichMessage":
			f.mu.Lock()
			f.richCalls = append(f.richCalls, r.PostForm.Get("rich_message"))
			f.nextMsgID++
			id := f.nextMsgID
			f.mu.Unlock()
			f.writeResult(w, map[string]any{"message_id": id})
		default:
			f.mu.Unlock()
			f.t.Errorf("unexpected method %s", method)
			http.NotFound(w, r)
		}
	})
	return mux
}

func (f *cliFakeBot) serveUpdates(w http.ResponseWriter, r *http.Request, offset int64) {
	deadline := time.NewTimer(50 * time.Millisecond)
	defer deadline.Stop()
	for {
		f.mu.Lock()
		var batch []telegram.Update
		for _, u := range f.queue {
			if u.UpdateID >= offset {
				batch = append(batch, u)
			}
		}
		if len(batch) > 0 {
			f.queue = nil
			f.mu.Unlock()
			f.writeResult(w, batch)
			return
		}
		f.mu.Unlock()
		select {
		case <-f.wake:
		case <-deadline.C:
			f.writeResult(w, []telegram.Update{})
			return
		case <-r.Context().Done():
			return
		}
	}
}

func (f *cliFakeBot) writeResult(w http.ResponseWriter, result any) {
	w.Header().Set("Content-Type", "application/json")
	body, err := json.Marshal(map[string]any{"ok": true, "result": result})
	if err != nil {
		f.t.Errorf("marshal result: %v", err)
		return
	}
	_, _ = w.Write(body)
}

func waitForString(t *testing.T, buf *syncBuffer, want string, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s (%q in %q)", what, want, buf.String())
}

func TestGatewaySubcommandSmoke(t *testing.T) {
	fakeBot := newCLIFakeBot(t)
	botSrv := httptest.NewServer(fakeBot.handler())
	t.Cleanup(botSrv.Close)

	cwd := t.TempDir()
	sessDir := t.TempDir()
	gatewayDir := t.TempDir()
	pkgDir := t.TempDir()
	home := t.TempDir()
	t.Setenv("SMIDJA_GATEWAY_DIR", gatewayDir)
	t.Setenv("SMIDJA_SESSION_DIR", sessDir)
	t.Setenv("SMIDJA_PACKAGES_DIR", pkgDir)
	t.Setenv("TELEGRAM_BOT_TOKEN", "tok")
	t.Setenv("SMIDJA_TELEGRAM_ALLOWED_IDS", "42")
	t.Setenv("SMIDJA_TELEGRAM_API_BASE", botSrv.URL)
	t.Setenv("SMIDJA_WEB_TOKEN", "web-secret")
	t.Setenv("SMIDJA_OFFLINE", "1")

	cfg, err := config.Load(
		envFrom(map[string]string{
			"OPENROUTER_API_KEY":  "sk-test",
			"SMIDJA_SESSION_DIR":  sessDir,
			"SMIDJA_PACKAGES_DIR": pkgDir,
		}),
		func() (string, error) { return cwd, nil },
		func() string { return home },
	)
	if err != nil {
		t.Fatal(err)
	}
	store, err := session.NewStore(sessDir)
	if err != nil {
		t.Fatal(err)
	}

	var stderr syncBuffer
	client := &gatewayFakeClient{script: []*agent.AssistantMessage{
		textStop("hello from gateway"),
		textStop("reply from telegram"),
	}}
	ctx, cancel := context.WithCancel(context.Background())
	deps := &Deps{
		Context: ctx,
		Env: envFrom(map[string]string{
			"TELEGRAM_BOT_TOKEN":          "tok",
			"SMIDJA_TELEGRAM_ALLOWED_IDS": "42",
			"SMIDJA_TELEGRAM_API_BASE":    botSrv.URL,
			"SMIDJA_WEB_TOKEN":            "web-secret",
			"SMIDJA_GATEWAY_DIR":          gatewayDir,
		}),
		Getwd:  func() (string, error) { return cwd, nil },
		Home:   func() string { return home },
		Stdin:  strings.NewReader(""),
		Stdout: io.Discard,
		Stderr: &stderr,
		Config: cfg,
		Store:  store,
		Client: client,
		Tools:  []agent.Tool{&probeTool{calls: new(int)}},
		FetchModels: func(context.Context) ([]models.ModelInfo, error) {
			return nil, errors.New("catalog fetch unavailable")
		},
		ModelsCatalog: &models.CatalogSource{},
	}
	done := make(chan error, 1)
	go func() {
		done <- runGatewayServer(deps, gatewayServerOptions{webAddr: "127.0.0.1:0", allowWorkspaceMCP: false})
	}()

	waitForString(t, &stderr, "gateway listening telegram=on web=http://127.0.0.1:", "startup line")
	startup := stderr.String()
	webURL := ""
	for _, field := range strings.Fields(startup) {
		if strings.HasPrefix(field, "web=http://") {
			webURL = strings.TrimPrefix(field, "web=")
		}
	}
	if webURL == "" {
		t.Fatalf("no web url in startup line %q", startup)
	}
	if !strings.Contains(startup, "telegram=on") {
		t.Fatalf("startup line must report telegram=on: %q", startup)
	}

	resp, err := http.Get(webURL + "/")
	if err != nil {
		t.Fatalf("get web root: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("web root status = %d, want 200", resp.StatusCode)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	webClient := &http.Client{Jar: jar}
	csrf := loginWeb(t, webURL, "web-secret", webClient)
	sendWeb(t, webURL, csrf, "default", "hello web", webClient)
	waitForSessionTurn(t, sessDir, "hello web", "hello from gateway")

	enqueueTelegramUpdate(t, fakeBot)
	waitForTelegramReply(t, fakeBot, "reply from telegram")

	bindingsPath := filepath.Join(gatewayDir, "bindings.json")
	data, err := os.ReadFile(bindingsPath)
	if err != nil {
		t.Fatalf("read bindings: %v", err)
	}
	var bindings map[string]string
	if err := json.Unmarshal(data, &bindings); err != nil {
		t.Fatalf("decode bindings: %v", err)
	}
	if len(bindings) != 2 {
		t.Fatalf("bindings = %v, want 2 chat keys (web + telegram)", bindings)
	}
	for key, path := range bindings {
		if !strings.HasPrefix(key, "web:") && !strings.HasPrefix(key, "telegram:") {
			t.Errorf("binding key %q has an unexpected transport", key)
		}
		if _, err := os.Stat(path); err != nil {
			t.Errorf("bound session %q for %q does not exist", path, key)
		}
	}
	if _, err := os.Stat(filepath.Join(gatewayDir, "journal.jsonl")); err != nil {
		t.Fatalf("journal file missing: %v", err)
	}

	cancel()
	startShutdown := time.Now()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("gateway shutdown error: %v", err)
		}
		t.Logf("shutdown took %s", time.Since(startShutdown))
	case <-time.After(12 * time.Second):
		var sb strings.Builder
		pprof.Lookup("goroutine").WriteTo(&sb, 1)
		t.Fatalf("gateway did not shut down after context cancel (stderr:\n%s\nGOROUTINES:\n%s)", stderr.String(), sb.String())
	}
	if errStr := stderr.String(); strings.Contains(errStr, "smidja: telegram:") {
		t.Errorf("telegram transport reported an error:\n%s", errStr)
	}
}

func loginWeb(t *testing.T, baseURL, token string, client *http.Client) string {
	t.Helper()
	if client == nil {
		client = &http.Client{}
	}
	body := bytes.NewReader([]byte(`{"token":"` + token + `"}`))
	req, err := http.NewRequest("POST", baseURL+"/login", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", resp.StatusCode)
	}
	var out struct {
		CSRF string `json:"csrf"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.CSRF == "" {
		t.Fatal("login returned no csrf")
	}
	return out.CSRF
}

func sendWeb(t *testing.T, baseURL, csrf, workspace, text string, client *http.Client) {
	t.Helper()
	if client == nil {
		client = &http.Client{}
	}
	payload, _ := json.Marshal(map[string]string{"workspace": workspace, "text": text})
	req, err := http.NewRequest("POST", baseURL+"/api/send", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF", csrf)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("send status = %d: %s", resp.StatusCode, b)
	}
}

func waitForSessionTurn(t *testing.T, sessDir, userText, responseText string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var files []string
		filepath.WalkDir(sessDir, func(p string, de os.DirEntry, err error) error {
			if err == nil && !de.IsDir() && strings.HasSuffix(de.Name(), ".jsonl") {
				files = append(files, p)
			}
			return nil
		})
		for _, p := range files {
			data, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			if strings.Contains(string(data), userText) && strings.Contains(string(data), responseText) {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("session file never recorded the web turn")
}

func enqueueTelegramUpdate(t *testing.T, f *cliFakeBot) {
	t.Helper()
	f.enqueue(telegram.Update{UpdateID: 1, Message: &telegram.Message{
		MessageID: 100,
		Chat:      &telegram.Chat{ID: 123, Type: "private"},
		From:      &telegram.User{ID: 42},
		Text:      "hello tg",
	}})
}

func waitForTelegramReply(t *testing.T, f *cliFakeBot, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, raw := range f.richMessageTexts() {
			var rich struct {
				Markdown string `json:"markdown"`
			}
			if json.Unmarshal([]byte(raw), &rich) == nil && strings.Contains(rich.Markdown, want) {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("telegram never delivered the expected reply")
}

func TestGatewaySubcommandNoTelegramNoWebFlags(t *testing.T) {
	cwd := t.TempDir()
	sessDir := t.TempDir()
	pkgDir := t.TempDir()
	home := t.TempDir()
	t.Setenv("SMIDJA_GATEWAY_DIR", t.TempDir())
	t.Setenv("SMIDJA_SESSION_DIR", sessDir)
	t.Setenv("SMIDJA_PACKAGES_DIR", pkgDir)
	t.Setenv("SMIDJA_WEB_TOKEN", "web-secret")

	cfg, err := config.Load(
		envFrom(map[string]string{"OPENROUTER_API_KEY": "sk-test"}),
		func() (string, error) { return cwd, nil },
		func() string { return home },
	)
	if err != nil {
		t.Fatal(err)
	}
	store, err := session.NewStore(sessDir)
	if err != nil {
		t.Fatal(err)
	}
	var stderr syncBuffer
	ctx, cancel := context.WithCancel(context.Background())
	deps := &Deps{
		Context: ctx,
		Env:     envFrom(nil),
		Getwd:   func() (string, error) { return cwd, nil },
		Home:    func() string { return home },
		Stdin:   strings.NewReader(""),
		Stdout:  io.Discard,
		Stderr:  &stderr,
		Config:  cfg,
		Store:   store,
		Client:  &gatewayFakeClient{script: []*agent.AssistantMessage{textStop("ok")}},
		Tools:   []agent.Tool{&probeTool{calls: new(int)}},
	}
	done := make(chan error, 1)
	go func() { done <- runGatewayServer(deps, gatewayServerOptions{noWeb: true}) }()

	waitForString(t, &stderr, "gateway listening telegram=off web=off", "startup line")
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("gateway shutdown error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("gateway did not shut down")
	}
}

func TestGatewayFlagParsing(t *testing.T) {
	var stdout, stderr bytes.Buffer
	d := testDeps("", &stdout, &stderr)
	if err := runGateway([]string{"-bogus"}, d); err == nil {
		t.Fatal("unknown flag: want error")
	}
	err := runGateway([]string{"extra"}, d)
	if err == nil || !strings.Contains(err.Error(), "unexpected argument") {
		t.Fatalf("positional arg err = %v", err)
	}
	var usageOut bytes.Buffer
	printGatewayUsage(&usageOut)
	if !strings.Contains(usageOut.String(), "usage: smidja gateway") {
		t.Errorf("gateway usage missing header:\n%s", usageOut.String())
	}
	if !strings.Contains(usageOut.String(), "SMIDJA_TELEGRAM_ALLOWED_IDS") {
		t.Errorf("gateway usage missing the allowlist env:\n%s", usageOut.String())
	}
}

func TestParseAllowedUserIDs(t *testing.T) {
	cases := map[string][]int64{
		"":        nil,
		"42":      {42},
		"42, 7,9": {42, 7, 9},
		"abc, 5,": {5},
	}
	for raw, want := range cases {
		got := parseAllowedUserIDs(raw)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("parseAllowedUserIDs(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestWorkspaceRootForChat(t *testing.T) {
	roots := map[string]string{"default": "/ws"}
	if got := workspaceRootForChat("telegram:1:0:2", "/fallback", roots); got != "/fallback" {
		t.Errorf("telegram key root = %q, want the default root", got)
	}
	if got := workspaceRootForChat("web:abc", "/fallback", roots); got != "/ws" {
		t.Errorf("web key root = %q, want the web workspace root", got)
	}
	if got := workspaceRootForChat("web:abc", "/fallback", map[string]string{"a": "/x", "b": "/y"}); got != "/fallback" {
		t.Errorf("multi-workspace web key root = %q, want the default root", got)
	}
}

func gatewayErrorDeps(t *testing.T, cfg *config.Config, env map[string]string) *Deps {
	t.Helper()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &Deps{
		Env:    envFrom(env),
		Getwd:  func() (string, error) { return t.TempDir(), nil },
		Home:   func() string { return t.TempDir() },
		Stdin:  strings.NewReader(""),
		Stdout: io.Discard,
		Stderr: io.Discard,
		Config: cfg,
		Store:  store,
		Client: &fakeClient{script: []*agent.AssistantMessage{textStop("ok")}},
		Tools:  []agent.Tool{&probeTool{calls: new(int)}},
	}
}

func TestGatewayServerHelpFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := runGateway([]string{"-h"}, testDeps("", &stdout, &stderr)); err != nil {
		t.Fatalf("runGateway -h: %v", err)
	}
	if !strings.Contains(stderr.String(), "usage: smidja gateway") {
		t.Errorf("stderr = %q, want the gateway usage", stderr.String())
	}
}

func TestGatewayServerConfigLoadFailure(t *testing.T) {
	pkgRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(pkgRoot, "index.json"), []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := &Deps{
		Env:    envFrom(map[string]string{"SMIDJA_PACKAGES_DIR": pkgRoot}),
		Getwd:  func() (string, error) { return t.TempDir(), nil },
		Home:   func() string { return t.TempDir() },
		Stdin:  strings.NewReader(""),
		Stdout: io.Discard,
		Stderr: io.Discard,
	}
	if err := runGatewayServer(d, gatewayServerOptions{}); err == nil {
		t.Fatal("corrupt packages index: want config load error")
	}
}

func TestGatewayServerProviderBuildFailure(t *testing.T) {
	d := gatewayErrorDeps(t, testConfig(t, t.TempDir()), nil)
	if err := runGatewayServer(d, gatewayServerOptions{provider: "no-such-provider"}); err == nil {
		t.Fatal("unknown provider: want build error")
	}
}

func TestGatewayServerEmptyWorkspaceRootFails(t *testing.T) {
	cfg := &config.Config{Model: "test/model", WorkspaceRoot: "", SessionDir: t.TempDir()}
	d := gatewayErrorDeps(t, cfg, nil)
	d.Tools = nil
	d.Store = nil
	if err := runGatewayServer(d, gatewayServerOptions{}); err == nil {
		t.Fatal("empty workspace root: want error")
	}
}

func TestGatewayServerSessionDirUnwritable(t *testing.T) {
	block := filepath.Join(t.TempDir(), "block")
	if err := os.WriteFile(block, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Model: "test/model", WorkspaceRoot: t.TempDir(), SessionDir: filepath.Join(block, "sessions")}
	d := gatewayErrorDeps(t, cfg, nil)
	d.Store = nil
	if err := runGatewayServer(d, gatewayServerOptions{}); err == nil {
		t.Fatal("unwritable session dir: want error")
	}
}

func TestGatewayServerCorruptBindings(t *testing.T) {
	gatewayDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(gatewayDir, "bindings.json"), []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := gatewayErrorDeps(t, testConfig(t, t.TempDir()), map[string]string{"SMIDJA_GATEWAY_DIR": gatewayDir})
	if err := runGatewayServer(d, gatewayServerOptions{}); err == nil {
		t.Fatal("corrupt bindings: want error")
	}
}
