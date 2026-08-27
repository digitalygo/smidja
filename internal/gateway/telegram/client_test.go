package telegram

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestClientGetMe(t *testing.T) {
	fake := newFakeBot(t, "tok")
	c := &client{base: fake.url(), token: "tok", http: http.DefaultClient}
	user, err := c.getMe(context.Background())
	if err != nil {
		t.Fatalf("getMe: %v", err)
	}
	if user.ID != 111 || user.Username != "testbot" {
		t.Fatalf("user = %+v", user)
	}
}

func TestClientGetUpdatesParams(t *testing.T) {
	fake := newFakeBot(t, "tok")
	c := &client{base: fake.url(), token: "tok", http: http.DefaultClient}
	if _, err := c.getUpdates(context.Background(), 7, 25); err != nil {
		t.Fatalf("getUpdates: %v", err)
	}
	calls := fake.callsFor("getUpdates")
	if len(calls) != 1 {
		t.Fatalf("getUpdates calls = %d", len(calls))
	}
	p := calls[0]
	if p.param("offset") != "7" {
		t.Fatalf("offset = %s", p.param("offset"))
	}
	if p.param("limit") != "100" {
		t.Fatalf("limit = %s", p.param("limit"))
	}
	if p.param("timeout") != "25" {
		t.Fatalf("timeout = %s", p.param("timeout"))
	}
	if p.param("allowed_updates") != `["message"]` {
		t.Fatalf("allowed_updates = %s", p.param("allowed_updates"))
	}
}

func TestClientGetUpdatesDecodesBatch(t *testing.T) {
	fake := newFakeBot(t, "tok")
	fake.enqueue(
		Update{UpdateID: 1, Message: &Message{MessageID: 10, Chat: &Chat{ID: 5, Type: chatTypePrivate}, From: &User{ID: 42}, Text: "a"}},
		Update{UpdateID: 2, Message: &Message{MessageID: 11, Chat: &Chat{ID: 5, Type: chatTypePrivate}, From: &User{ID: 42}, Text: "b"}},
	)
	c := &client{base: fake.url(), token: "tok", http: http.DefaultClient}
	updates, err := c.getUpdates(context.Background(), 0, 5)
	if err != nil {
		t.Fatalf("getUpdates: %v", err)
	}
	if len(updates) != 2 || updates[0].UpdateID != 1 || updates[1].Message.Text != "b" {
		t.Fatalf("updates = %+v", updates)
	}
}

func TestClientSendRichMessageBody(t *testing.T) {
	fake := newFakeBot(t, "tok")
	c := &client{base: fake.url(), token: "tok", http: http.DefaultClient}
	_, err := c.sendRichMessage(context.Background(), 123, 7, InputRichMessage{Markdown: "# hi"}, &ReplyParameters{MessageID: 42})
	if err != nil {
		t.Fatalf("sendRichMessage: %v", err)
	}
	calls := fake.callsFor("sendRichMessage")
	if len(calls) != 1 {
		t.Fatalf("calls = %d", len(calls))
	}
	p := calls[0]
	if p.param("chat_id") != "123" || p.param("message_thread_id") != "7" {
		t.Fatalf("chat params = %v", p.params)
	}
	if p.param("rich_message") != `{"markdown":"# hi"}` {
		t.Fatalf("rich_message = %s", p.param("rich_message"))
	}
	if p.param("reply_parameters") != `{"message_id":42}` {
		t.Fatalf("reply_parameters = %s", p.param("reply_parameters"))
	}
}

func TestClientSendRichMessageOmitsThreadWhenZero(t *testing.T) {
	fake := newFakeBot(t, "tok")
	c := &client{base: fake.url(), token: "tok", http: http.DefaultClient}
	if _, err := c.sendRichMessage(context.Background(), 123, 0, InputRichMessage{Markdown: "hi"}, nil); err != nil {
		t.Fatalf("sendRichMessage: %v", err)
	}
	p := fake.callsFor("sendRichMessage")[0]
	if p.param("message_thread_id") != "" {
		t.Fatalf("message_thread_id = %s", p.param("message_thread_id"))
	}
	if p.param("reply_parameters") != "" {
		t.Fatalf("reply_parameters = %s", p.param("reply_parameters"))
	}
}

func TestClientSendRichMessageDraft(t *testing.T) {
	fake := newFakeBot(t, "tok")
	c := &client{base: fake.url(), token: "tok", http: http.DefaultClient}
	if err := c.sendRichMessageDraft(context.Background(), 123, 0, 5, InputRichMessage{Markdown: "partial"}); err != nil {
		t.Fatalf("sendRichMessageDraft: %v", err)
	}
	p := fake.callsFor("sendRichMessageDraft")[0]
	if p.param("draft_id") != "5" {
		t.Fatalf("draft_id = %s", p.param("draft_id"))
	}
	if p.param("rich_message") != `{"markdown":"partial"}` {
		t.Fatalf("rich_message = %s", p.param("rich_message"))
	}
	if p.param("chat_id") != "123" {
		t.Fatalf("chat_id = %s", p.param("chat_id"))
	}
}

func TestClientSendMessagePlainText(t *testing.T) {
	fake := newFakeBot(t, "tok")
	c := &client{base: fake.url(), token: "tok", http: http.DefaultClient}
	if _, err := c.sendMessage(context.Background(), 123, 0, "plain *text* with _marks_", &ReplyParameters{MessageID: 9}); err != nil {
		t.Fatalf("sendMessage: %v", err)
	}
	p := fake.callsFor("sendMessage")[0]
	if p.param("text") != "plain *text* with _marks_" {
		t.Fatalf("text = %s", p.param("text"))
	}
	if p.param("parse_mode") != "" {
		t.Fatalf("parse_mode = %s", p.param("parse_mode"))
	}
	if p.param("reply_parameters") != `{"message_id":9}` {
		t.Fatalf("reply_parameters = %s", p.param("reply_parameters"))
	}
}

func TestClientSendChatAction(t *testing.T) {
	fake := newFakeBot(t, "tok")
	c := &client{base: fake.url(), token: "tok", http: http.DefaultClient}
	if err := c.sendChatAction(context.Background(), 123, 0, actionTyping); err != nil {
		t.Fatalf("sendChatAction: %v", err)
	}
	p := fake.callsFor("sendChatAction")[0]
	if p.param("action") != actionTyping || p.param("chat_id") != "123" {
		t.Fatalf("params = %v", p.params)
	}
}

func TestClientDeleteWebhook(t *testing.T) {
	fake := newFakeBot(t, "tok")
	c := &client{base: fake.url(), token: "tok", http: http.DefaultClient}
	if err := c.deleteWebhook(context.Background()); err != nil {
		t.Fatalf("deleteWebhook: %v", err)
	}
	calls := fake.callsFor("deleteWebhook")
	if len(calls) != 1 {
		t.Fatalf("deleteWebhook calls = %d", len(calls))
	}
	if len(calls[0].params) != 0 {
		t.Fatalf("deleteWebhook params = %v", calls[0].params)
	}
}

func TestClientAPIErrorParsed(t *testing.T) {
	fake := newFakeBot(t, "tok")
	fake.fail("getMe", 429, 3, "Too Many Requests", 1)
	c := &client{base: fake.url(), token: "tok", http: http.DefaultClient}
	_, err := c.getMe(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v", err)
	}
	if apiErr.Code != 429 || apiErr.RetryAfter != 3 {
		t.Fatalf("apiErr = %+v", apiErr)
	}
}

func TestClientAPIErrorRedactsToken(t *testing.T) {
	fake := newFakeBot(t, "SECRET-TOKEN")
	fake.fail("getMe", 400, 0, "Bad Request for bot SECRET-TOKEN", 1)
	c := &client{base: fake.url(), token: "SECRET-TOKEN", http: http.DefaultClient}
	_, err := c.getMe(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "SECRET-TOKEN") {
		t.Fatalf("token leaked: %v", err)
	}
	if !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("no redaction marker: %v", err)
	}
}

func TestClientNetworkErrorRedactsToken(t *testing.T) {
	c := &client{base: closedServer(t), token: "NET-TOKEN", http: http.DefaultClient}
	_, err := c.getMe(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "NET-TOKEN") {
		t.Fatalf("token leaked: %v", err)
	}
	if !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("no redaction marker: %v", err)
	}
}

func TestClientMalformedResponse(t *testing.T) {
	srv := rawServer(t, 200, `not json at all`)
	c := &client{base: srv.URL, token: "tok", http: http.DefaultClient}
	if _, err := c.getMe(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestClientResultDecodeError(t *testing.T) {
	srv := rawServer(t, 200, `{"ok":true,"result":"oops"}`)
	c := &client{base: srv.URL, token: "tok", http: http.DefaultClient}
	if _, err := c.getMe(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestClientRedact(t *testing.T) {
	c := &client{token: ""}
	if got := c.redact("hello"); got != "hello" {
		t.Fatalf("redact = %q", got)
	}
	c2 := &client{token: "tok"}
	if got := c2.redact(""); got != "" {
		t.Fatalf("redact = %q", got)
	}
	if got := c2.redact("a tok b tok"); got != "a [redacted] b [redacted]" {
		t.Fatalf("redact = %q", got)
	}
}
