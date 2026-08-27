package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/authstore"
	"github.com/digitalygo/smidja/internal/gateway"
)

func TestChatKeyRoundTrip(t *testing.T) {
	key := chatKey(123, 0, 42)
	if key != "telegram:123:0:42" {
		t.Fatalf("chatKey = %s", key)
	}
	chatID, threadID, userID, err := parseChatKey(key)
	if err != nil || chatID != 123 || threadID != 0 || userID != 42 {
		t.Fatalf("parse = %d %d %d %v", chatID, threadID, userID, err)
	}
}

func TestChatKeyNegativeChatID(t *testing.T) {
	key := chatKey(-100123, 7, 42)
	if key != "telegram:-100123:7:42" {
		t.Fatalf("chatKey = %s", key)
	}
	chatID, threadID, userID, err := parseChatKey(key)
	if err != nil || chatID != -100123 || threadID != 7 || userID != 42 {
		t.Fatalf("parse = %d %d %d %v", chatID, threadID, userID, err)
	}
}

func TestParseChatKeyInvalid(t *testing.T) {
	for _, key := range []string{
		"", "telegram", "telegram:1", "telegram:1:2",
		"telegram:1:2:3:4", "discord:1:2:3",
		"telegram:abc:2:3", "telegram:1:abc:3", "telegram:1:2:abc",
	} {
		if _, _, _, err := parseChatKey(key); err == nil {
			t.Fatalf("expected error for %q", key)
		}
	}
}

func TestIsAllowed(t *testing.T) {
	tr := New(Options{AllowedUserIDs: []int64{42}})
	if !tr.isAllowed(42) || tr.isAllowed(7) {
		t.Fatal("allowlist lookup failed")
	}
	empty := New(Options{})
	if empty.isAllowed(42) {
		t.Fatal("empty allowlist must reject all")
	}
}

func TestResolverDelegatesWorkspaceForChat(t *testing.T) {
	tr := New(Options{WorkspaceForChat: func(key string) (string, string) {
		return "ws-" + key, "/hint-" + key + ".jsonl"
	}})
	workspace, hint := tr.Resolver("telegram:telegram:123:0:42")
	if workspace != "ws-telegram:123:0:42" {
		t.Fatalf("workspace = %s", workspace)
	}
	if hint != "/hint-telegram:123:0:42.jsonl" {
		t.Fatalf("hint = %s", hint)
	}
}

func TestResolverNil(t *testing.T) {
	tr := New(Options{})
	workspace, hint := tr.Resolver("telegram:telegram:1:0:2")
	if workspace != "" || hint != "" {
		t.Fatalf("resolver = %q %q", workspace, hint)
	}
}

func TestStartMissingGateway(t *testing.T) {
	tr := New(Options{Token: func() (string, error) { return "tok", nil }})
	if err := tr.Start(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestStartMissingTokenResolver(t *testing.T) {
	tr := New(Options{Gateway: &submitRecorder{}})
	if err := tr.Start(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestStartTokenError(t *testing.T) {
	tr := New(Options{Gateway: &submitRecorder{}, Token: func() (string, error) { return "", errors.New("no token") }})
	if err := tr.Start(context.Background()); err == nil || !strings.Contains(err.Error(), "no token") {
		t.Fatalf("err = %v", err)
	}
}

func TestStartEmptyToken(t *testing.T) {
	fake := newFakeBot(t, "tok")
	tr := New(Options{Gateway: &submitRecorder{}, Token: func() (string, error) { return "   ", nil }, APIBase: fake.url()})
	if err := tr.Start(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestStartGetMeFailureRedactsToken(t *testing.T) {
	fake := newFakeBot(t, "SECRET-TOKEN-42")
	fake.fail("getMe", 401, 0, "Unauthorized for bot SECRET-TOKEN-42", 1)
	tr := New(Options{
		Gateway: &submitRecorder{},
		Token:   func() (string, error) { return "SECRET-TOKEN-42", nil },
		APIBase: fake.url(),
	})
	err := tr.Start(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "SECRET-TOKEN-42") {
		t.Fatalf("token leaked in error: %v", err)
	}
	if !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("expected redaction marker, got %v", err)
	}
}

func TestStartDeleteWebhookFailure(t *testing.T) {
	fake := newFakeBot(t, "tok")
	fake.fail("deleteWebhook", 400, 0, "Bad Request: webhook is not active", 1)
	tr := New(Options{
		Gateway: &submitRecorder{},
		Token:   func() (string, error) { return "tok", nil },
		APIBase: fake.url(),
	})
	err := tr.Start(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != 400 {
		t.Fatalf("err = %v", err)
	}
}

func TestStartAlreadyStarted(t *testing.T) {
	fake := newFakeBot(t, "tok")
	tr, cancel := startTransport(t, fake, nil)
	defer cancel()
	if err := tr.Start(context.Background()); err == nil {
		t.Fatal("expected already started error")
	}
}

func TestStartCancelStopsCleanly(t *testing.T) {
	fake := newFakeBot(t, "tok")
	before := runtime.NumGoroutine()
	tr, cancel := startTransport(t, fake, nil)
	cancel()
	_ = tr
	waitGoroutines(t, before+2, "transport stop")
}

func TestPollParams(t *testing.T) {
	fake := newFakeBot(t, "tok")
	tr, cancel := startTransport(t, fake, func(o *Options) { o.PollTimeoutSecs = 5 })
	defer cancel()
	_ = tr
	calls := waitCalls(t, fake, "getUpdates", 1)
	c := calls[0]
	if c.param("limit") != "100" {
		t.Fatalf("limit = %s", c.param("limit"))
	}
	if c.param("timeout") != "5" {
		t.Fatalf("timeout = %s", c.param("timeout"))
	}
	if c.param("allowed_updates") != `["message"]` {
		t.Fatalf("allowed_updates = %s", c.param("allowed_updates"))
	}
	if c.param("offset") != "0" {
		t.Fatalf("offset = %s", c.param("offset"))
	}
}

func TestDeleteWebhookWithoutDroppingPending(t *testing.T) {
	fake := newFakeBot(t, "tok")
	tr, cancel := startTransport(t, fake, nil)
	defer cancel()
	_ = tr
	waitCalls(t, fake, "deleteWebhook", 1)
	calls := fake.callsFor("deleteWebhook")
	if len(calls[0].params) != 0 {
		t.Fatalf("deleteWebhook params = %v", calls[0].params)
	}
	if calls[0].param("drop_pending_updates") != "" {
		t.Fatalf("drop_pending_updates must be absent")
	}
}

func TestGetMeCalledOnStart(t *testing.T) {
	fake := newFakeBot(t, "tok")
	tr, cancel := startTransport(t, fake, nil)
	defer cancel()
	_ = tr
	waitCalls(t, fake, "getMe", 1)
}

func TestUpdateFlowSubmitsAllowedPrivateMessage(t *testing.T) {
	fake := newFakeBot(t, "tok")
	rec := &submitRecorder{}
	tr, cancel := startTransport(t, fake, func(o *Options) { o.Gateway = rec })
	defer cancel()
	_ = tr
	fake.enqueue(Update{UpdateID: 1, Message: &Message{
		MessageID: 100,
		Chat:      &Chat{ID: 123, Type: chatTypePrivate},
		From:      &User{ID: 42},
		Text:      "hello",
	}})
	msgs := waitForMessages(t, rec, 1)
	m := msgs[0]
	if m.Transport != TransportName || m.ExternalChatKey != "telegram:123:0:42" || m.Text != "hello" {
		t.Fatalf("inbound = %+v", m)
	}
	if m.ID != "1:100" {
		t.Fatalf("id = %s", m.ID)
	}
	if m.UserIDHash != gateway.HashUserIdentity("42") {
		t.Fatalf("user hash = %s", m.UserIDHash)
	}
	waitCalls(t, fake, "sendChatAction", 1)
	act := fake.callsFor("sendChatAction")[0]
	if act.param("action") != actionTyping || act.param("chat_id") != "123" {
		t.Fatalf("chat action = %+v", act)
	}
}

func TestUpdateFlowRejectsDisallowedUser(t *testing.T) {
	fake := newFakeBot(t, "tok")
	rec := &submitRecorder{}
	tr, cancel := startTransport(t, fake, func(o *Options) { o.Gateway = rec })
	defer cancel()
	_ = tr
	fake.enqueue(Update{UpdateID: 1, Message: &Message{
		MessageID: 100,
		Chat:      &Chat{ID: 123, Type: chatTypePrivate},
		From:      &User{ID: 999},
		Text:      "hi",
	}})
	time.Sleep(150 * time.Millisecond)
	if n := len(rec.messages()); n != 0 {
		t.Fatalf("disallowed user submitted %d messages", n)
	}
	assertNoCalls(t, fake, "sendChatAction", 0)
}

func TestUpdateFlowRejectsGroupChat(t *testing.T) {
	fake := newFakeBot(t, "tok")
	rec := &submitRecorder{}
	tr, cancel := startTransport(t, fake, func(o *Options) { o.Gateway = rec })
	defer cancel()
	_ = tr
	fake.enqueue(Update{UpdateID: 1, Message: &Message{
		MessageID: 100,
		Chat:      &Chat{ID: -100, Type: "group"},
		From:      &User{ID: 42},
		Text:      "hi",
	}})
	time.Sleep(150 * time.Millisecond)
	if n := len(rec.messages()); n != 0 {
		t.Fatalf("group chat submitted %d messages", n)
	}
}

func TestUpdateFlowIgnoresEmptyUpdates(t *testing.T) {
	fake := newFakeBot(t, "tok")
	rec := &submitRecorder{}
	tr, cancel := startTransport(t, fake, func(o *Options) { o.Gateway = rec })
	defer cancel()
	_ = tr
	fake.enqueue(Update{UpdateID: 1})
	time.Sleep(150 * time.Millisecond)
	if n := len(rec.messages()); n != 0 {
		t.Fatalf("empty update submitted %d messages", n)
	}
}

func TestUpdateFlowIgnoresEmptyText(t *testing.T) {
	fake := newFakeBot(t, "tok")
	rec := &submitRecorder{}
	tr, cancel := startTransport(t, fake, func(o *Options) { o.Gateway = rec })
	defer cancel()
	_ = tr
	fake.enqueue(Update{UpdateID: 1, Message: &Message{
		MessageID: 100,
		Chat:      &Chat{ID: 123, Type: chatTypePrivate},
		From:      &User{ID: 42},
		Text:      "",
	}})
	time.Sleep(150 * time.Millisecond)
	if n := len(rec.messages()); n != 0 {
		t.Fatalf("empty text submitted %d messages", n)
	}
}

func TestUpdateFlowThreadPropagationInChatKey(t *testing.T) {
	fake := newFakeBot(t, "tok")
	rec := &submitRecorder{}
	tr, cancel := startTransport(t, fake, func(o *Options) { o.Gateway = rec })
	defer cancel()
	_ = tr
	fake.enqueue(Update{UpdateID: 1, Message: &Message{
		MessageID:       100,
		MessageThreadID: 7,
		Chat:            &Chat{ID: 123, Type: chatTypePrivate},
		From:            &User{ID: 42},
		Text:            "hi",
	}})
	msgs := waitForMessages(t, rec, 1)
	if msgs[0].ExternalChatKey != "telegram:123:7:42" {
		t.Fatalf("chat key = %s", msgs[0].ExternalChatKey)
	}
}

func TestUpdateFlowAdvancesOffset(t *testing.T) {
	fake := newFakeBot(t, "tok")
	rec := &submitRecorder{}
	tr, cancel := startTransport(t, fake, func(o *Options) { o.Gateway = rec })
	defer cancel()
	_ = tr
	fake.enqueue(
		Update{UpdateID: 1, Message: &Message{MessageID: 100, Chat: &Chat{ID: 123, Type: chatTypePrivate}, From: &User{ID: 42}, Text: "one"}},
		Update{UpdateID: 2, Message: &Message{MessageID: 101, Chat: &Chat{ID: 123, Type: chatTypePrivate}, From: &User{ID: 42}, Text: "two"}},
	)
	msgs := waitForMessages(t, rec, 2)
	if msgs[0].Text != "one" || msgs[1].Text != "two" {
		t.Fatalf("messages = %+v", msgs)
	}
	waitForOffset(t, fake, 3)
}

func TestPollLoopRetries429RespectingRetryAfter(t *testing.T) {
	fake := newFakeBot(t, "tok")
	rec := &submitRecorder{}
	var sleeps []time.Duration
	var sleepMu sync.Mutex
	tr, cancel := startTransport(t, fake, func(o *Options) {
		o.Gateway = rec
		o.BackoffBase = time.Millisecond
		o.Sleep = func(ctx context.Context, d time.Duration) error {
			sleepMu.Lock()
			sleeps = append(sleeps, d)
			sleepMu.Unlock()
			return nil
		}
	})
	defer cancel()
	_ = tr
	fake.fail("getUpdates", 429, 2, "Too Many Requests", 1)
	fake.enqueue(Update{UpdateID: 1, Message: &Message{
		MessageID: 100, Chat: &Chat{ID: 123, Type: chatTypePrivate}, From: &User{ID: 42}, Text: "hi",
	}})
	waitForMessages(t, rec, 1)
	if n := len(fake.callsFor("getUpdates")); n < 2 {
		t.Fatalf("getUpdates calls = %d", n)
	}
	sleepMu.Lock()
	defer sleepMu.Unlock()
	found := false
	for _, d := range sleeps {
		if d == 2*time.Second {
			found = true
		}
	}
	if !found {
		t.Fatalf("no 2s retry_after sleep recorded, sleeps = %v", sleeps)
	}
}

func TestPollLoopRetries5xx(t *testing.T) {
	fake := newFakeBot(t, "tok")
	rec := &submitRecorder{}
	tr, cancel := startTransport(t, fake, func(o *Options) {
		o.Gateway = rec
		o.BackoffBase = time.Millisecond
	})
	defer cancel()
	_ = tr
	fake.fail("getUpdates", 500, 0, "Internal Server Error", 2)
	fake.enqueue(Update{UpdateID: 1, Message: &Message{
		MessageID: 100, Chat: &Chat{ID: 123, Type: chatTypePrivate}, From: &User{ID: 42}, Text: "hi",
	}})
	waitForMessages(t, rec, 1)
	if n := len(fake.callsFor("getUpdates")); n < 3 {
		t.Fatalf("getUpdates calls = %d", n)
	}
}

func TestPollLoopRetriesNetworkErrors(t *testing.T) {
	fake := newFakeBot(t, "tok")
	rec := &submitRecorder{}
	tr, cancel := startTransport(t, fake, func(o *Options) {
		o.Gateway = rec
		o.BackoffBase = time.Millisecond
	})
	defer cancel()
	_ = tr
	fake.mu.Lock()
	fake.networkErrors = 2
	fake.mu.Unlock()
	fake.enqueue(Update{UpdateID: 1, Message: &Message{
		MessageID: 100, Chat: &Chat{ID: 123, Type: chatTypePrivate}, From: &User{ID: 42}, Text: "hi",
	}})
	waitForMessages(t, rec, 1)
	if n := len(fake.callsFor("getUpdates")); n < 3 {
		t.Fatalf("getUpdates calls = %d", n)
	}
}

func TestRetryDelay(t *testing.T) {
	tr := New(Options{})
	if d := tr.retryDelay(&APIError{RetryAfter: 3}, time.Second); d != 3*time.Second {
		t.Fatalf("retry_after delay = %v", d)
	}
	if d := tr.retryDelay(&APIError{RetryAfter: 0}, 5*time.Second); d != 5*time.Second {
		t.Fatalf("backoff delay = %v", d)
	}
	if d := tr.retryDelay(errors.New("network down"), 4*time.Second); d != 4*time.Second {
		t.Fatalf("network delay = %v", d)
	}
}

func TestPollTimeoutSecsDefaultsAndClamps(t *testing.T) {
	if got := New(Options{}).pollTimeoutSecs(); got != 50 {
		t.Fatalf("default = %d", got)
	}
	if got := New(Options{PollTimeoutSecs: 10}).pollTimeoutSecs(); got != 10 {
		t.Fatalf("custom = %d", got)
	}
	if got := New(Options{PollTimeoutSecs: 200}).pollTimeoutSecs(); got != 50 {
		t.Fatalf("clamp = %d", got)
	}
}

func TestBackoffCap(t *testing.T) {
	tr := New(Options{PollTimeoutSecs: 10})
	if got := tr.backoffCap(); got != 40*time.Second {
		t.Fatalf("cap = %v", got)
	}
}

func TestSleepDefault(t *testing.T) {
	tr := New(Options{})
	start := time.Now()
	if err := tr.sleep(context.Background(), time.Millisecond); err != nil {
		t.Fatalf("sleep: %v", err)
	}
	if time.Since(start) < time.Millisecond {
		t.Fatal("sleep returned too early")
	}
}

func TestSleepCanceledContext(t *testing.T) {
	tr := New(Options{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := tr.sleep(ctx, time.Hour); err == nil {
		t.Fatal("expected context error")
	}
}

func TestTokenFromAuthEnvFirst(t *testing.T) {
	store, err := authstore.Load(filepath.Join(t.TempDir(), "auth.json"))
	if err != nil {
		t.Fatalf("load store: %v", err)
	}
	resolve := TokenFromAuth(store, func(key string) string {
		if key == "TELEGRAM_BOT_TOKEN" {
			return "env-token"
		}
		return ""
	})
	token, err := resolve()
	if err != nil || token != "env-token" {
		t.Fatalf("token = %q err = %v", token, err)
	}
}

func TestTokenFromAuthStoreFallback(t *testing.T) {
	store, err := authstore.Load(filepath.Join(t.TempDir(), "auth.json"))
	if err != nil {
		t.Fatalf("load store: %v", err)
	}
	if err := store.Set("telegram", authstore.Entry{Type: "api_key", Key: "store-token"}); err != nil {
		t.Fatalf("set store: %v", err)
	}
	resolve := TokenFromAuth(store, func(string) string { return "" })
	token, err := resolve()
	if err != nil || token != "store-token" {
		t.Fatalf("token = %q err = %v", token, err)
	}
}

func TestTokenFromAuthMissing(t *testing.T) {
	store, err := authstore.Load(filepath.Join(t.TempDir(), "auth.json"))
	if err != nil {
		t.Fatalf("load store: %v", err)
	}
	resolve := TokenFromAuth(store, func(string) string { return "" })
	if _, err := resolve(); err == nil {
		t.Fatal("expected error")
	}
}

func TestEndToEndMessageFlow(t *testing.T) {
	fake := newFakeBot(t, "tok")
	gw, err := gateway.New(gateway.Options{
		Dir:      t.TempDir(),
		Runner:   echoRunner{},
		Resolver: func(key string) (string, string) { return "ws", "/hint.jsonl" },
	})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}
	tr := New(Options{
		Gateway:         gw,
		Token:           func() (string, error) { return "tok", nil },
		AllowedUserIDs:  []int64{42},
		APIBase:         fake.url(),
		PollTimeoutSecs: 5,
	})
	gctx, gcancel := context.WithCancel(context.Background())
	if err := gw.Start(gctx); err != nil {
		t.Fatalf("start gateway: %v", err)
	}
	defer gcancel()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- tr.Start(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("transport did not stop")
		}
	}()
	waitCalls(t, fake, "getUpdates", 1)
	fake.enqueue(Update{UpdateID: 1, Message: &Message{
		MessageID: 100,
		Chat:      &Chat{ID: 123, Type: chatTypePrivate},
		From:      &User{ID: 42},
		Text:      "hello",
	}})
	calls := waitCalls(t, fake, "sendRichMessage", 1)
	var rich InputRichMessage
	if err := json.Unmarshal([]byte(calls[0].param("rich_message")), &rich); err != nil {
		t.Fatalf("decode rich_message: %v", err)
	}
	if rich.Markdown != "reply:hello" {
		t.Fatalf("markdown = %q, want reply:hello", rich.Markdown)
	}
	if calls[0].param("chat_id") != "123" {
		t.Fatalf("chat_id = %s", calls[0].param("chat_id"))
	}
	if calls[0].param("reply_parameters") != `{"message_id":100}` {
		t.Fatalf("reply_parameters = %s", calls[0].param("reply_parameters"))
	}
	waitCalls(t, fake, "sendChatAction", 1)
	act := fake.callsFor("sendChatAction")[0]
	if act.param("action") != actionTyping {
		t.Fatalf("action = %s", act.param("action"))
	}
}

func TestBackoffGrowthCapped(t *testing.T) {
	fake := newFakeBot(t, "tok")
	rec := &submitRecorder{}
	var sleeps []time.Duration
	var sleepMu sync.Mutex
	tr, cancel := startTransport(t, fake, func(o *Options) {
		o.Gateway = rec
		o.BackoffBase = 10 * time.Millisecond
		o.Sleep = func(ctx context.Context, d time.Duration) error {
			sleepMu.Lock()
			sleeps = append(sleeps, d)
			sleepMu.Unlock()
			return nil
		}
	})
	defer cancel()
	_ = tr
	fake.fail("getUpdates", 502, 0, "Bad Gateway", 5)
	fake.enqueue(Update{UpdateID: 1, Message: &Message{
		MessageID: 100, Chat: &Chat{ID: 123, Type: chatTypePrivate}, From: &User{ID: 42}, Text: "hi",
	}})
	waitForMessages(t, rec, 1)
	sleepMu.Lock()
	defer sleepMu.Unlock()
	if len(sleeps) != 5 {
		t.Fatalf("sleeps = %v", sleeps)
	}
	want := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond, 80 * time.Millisecond, 160 * time.Millisecond}
	for i, w := range want {
		if sleeps[i] != w {
			t.Fatalf("sleep[%d] = %v, want %v", i, sleeps[i], w)
		}
	}
}
