package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/gateway"
)

func TestDeliverNotStarted(t *testing.T) {
	tr := New(Options{})
	err := tr.Deliver(context.Background(), gateway.Delivery{ExternalChatKey: "telegram:1:0:2"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDeliverInvalidChatKey(t *testing.T) {
	fake := newFakeBot(t, "tok")
	tr, cancel := startTransport(t, fake, nil)
	defer cancel()
	_ = tr
	if err := tr.Deliver(context.Background(), gateway.Delivery{ExternalChatKey: "discord:1:0:2"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestDeliverRichMessageMarkdownPassthrough(t *testing.T) {
	fake := newFakeBot(t, "tok")
	tr, cancel := startTransport(t, fake, nil)
	defer cancel()
	_ = tr
	text := "# Title\n\nBody with *bold* and `code`"
	err := tr.Deliver(context.Background(), gateway.Delivery{
		ID:              "1:100",
		Transport:       TransportName,
		ExternalChatKey: "telegram:123:0:42",
		Result:          gateway.RunResult{Text: text},
	})
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	calls := waitCalls(t, fake, "sendRichMessage", 1)
	var rich InputRichMessage
	if err := json.Unmarshal([]byte(calls[0].param("rich_message")), &rich); err != nil {
		t.Fatalf("decode rich_message: %v", err)
	}
	if rich.Markdown != text {
		t.Fatalf("markdown = %q, want %q", rich.Markdown, text)
	}
	if calls[0].param("chat_id") != "123" {
		t.Fatalf("chat_id = %s", calls[0].param("chat_id"))
	}
	if got := calls[0].param("reply_parameters"); got != `{"message_id":100}` {
		t.Fatalf("reply_parameters = %s", got)
	}
}

func TestDeliverRichMessageThreadPropagation(t *testing.T) {
	fake := newFakeBot(t, "tok")
	tr, cancel := startTransport(t, fake, nil)
	defer cancel()
	_ = tr
	err := tr.Deliver(context.Background(), gateway.Delivery{
		ID:              "1:100",
		ExternalChatKey: "telegram:123:7:42",
		Result:          gateway.RunResult{Text: "hello"},
	})
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	calls := waitCalls(t, fake, "sendRichMessage", 1)
	if calls[0].param("message_thread_id") != "7" {
		t.Fatalf("message_thread_id = %s", calls[0].param("message_thread_id"))
	}
}

func TestDeliverEmptyResultNoSend(t *testing.T) {
	fake := newFakeBot(t, "tok")
	tr, cancel := startTransport(t, fake, nil)
	defer cancel()
	_ = tr
	err := tr.Deliver(context.Background(), gateway.Delivery{
		ID:              "1:100",
		ExternalChatKey: "telegram:123:0:42",
		Result:          gateway.RunResult{},
	})
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	assertNoCalls(t, fake, "sendRichMessage", 50*time.Millisecond)
	assertNoCalls(t, fake, "sendMessage", 0)
}

func TestDeliverErrorSendsSanitizedMessage(t *testing.T) {
	fake := newFakeBot(t, "tok")
	tr, cancel := startTransport(t, fake, nil)
	defer cancel()
	_ = tr
	err := tr.Deliver(context.Background(), gateway.Delivery{
		ID:              "1:100",
		ExternalChatKey: "telegram:123:0:42",
		Err:             errors.New("boom: tool output SECRET-TOKEN-42 leaked"),
	})
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	calls := waitCalls(t, fake, "sendMessage", 1)
	if calls[0].param("text") != errorUserMessage {
		t.Fatalf("text = %q", calls[0].param("text"))
	}
	if strings.Contains(calls[0].param("text"), "SECRET-TOKEN-42") || strings.Contains(calls[0].param("text"), "boom") {
		t.Fatalf("sanitized message leaked content: %q", calls[0].param("text"))
	}
	if calls[0].param("reply_parameters") != `{"message_id":100}` {
		t.Fatalf("reply_parameters = %s", calls[0].param("reply_parameters"))
	}
}

func TestDeliverFallbackChunksOnRich400(t *testing.T) {
	fake := newFakeBot(t, "tok")
	fake.richSupported = false
	tr, cancel := startTransport(t, fake, nil)
	defer cancel()
	_ = tr
	text := strings.Repeat("a", 4000) + " " + strings.Repeat("b", 200)
	err := tr.Deliver(context.Background(), gateway.Delivery{
		ID:              "1:100",
		ExternalChatKey: "telegram:123:0:42",
		Result:          gateway.RunResult{Text: text},
	})
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	waitCalls(t, fake, "sendRichMessage", 1)
	calls := waitCalls(t, fake, "sendMessage", 2)
	if len([]rune(calls[0].param("text"))) > 4096 || len([]rune(calls[1].param("text"))) > 4096 {
		t.Fatalf("chunk over 4096: %d %d", len([]rune(calls[0].param("text"))), len([]rune(calls[1].param("text"))))
	}
	if calls[0].param("text") != strings.Repeat("a", 4000) {
		t.Fatalf("chunk0 = %q", calls[0].param("text"))
	}
	if calls[1].param("text") != strings.Repeat("b", 200) {
		t.Fatalf("chunk1 = %q", calls[1].param("text"))
	}
	if calls[0].param("parse_mode") != "" || calls[1].param("parse_mode") != "" {
		t.Fatalf("legacy chunks must not set parse_mode")
	}
	if calls[0].param("reply_parameters") != `{"message_id":100}` {
		t.Fatalf("first chunk reply_parameters = %s", calls[0].param("reply_parameters"))
	}
	if calls[1].param("reply_parameters") != "" {
		t.Fatalf("second chunk must not carry reply_parameters: %s", calls[1].param("reply_parameters"))
	}
}

func TestDeliverFallbackOnRich404(t *testing.T) {
	fake := newFakeBot(t, "tok")
	fake.fail("sendRichMessage", 404, 0, "Not Found: method sendRichMessage is not available", 1)
	tr, cancel := startTransport(t, fake, nil)
	defer cancel()
	_ = tr
	err := tr.Deliver(context.Background(), gateway.Delivery{
		ID:              "1:100",
		ExternalChatKey: "telegram:123:0:42",
		Result:          gateway.RunResult{Text: "short text"},
	})
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	calls := waitCalls(t, fake, "sendMessage", 1)
	if calls[0].param("text") != "short text" {
		t.Fatalf("text = %q", calls[0].param("text"))
	}
}

func TestDeliverRichErrorNoFallbackOn403(t *testing.T) {
	fake := newFakeBot(t, "tok")
	fake.fail("sendRichMessage", 403, 0, "Forbidden: bot was blocked by the user", 1)
	tr, cancel := startTransport(t, fake, nil)
	defer cancel()
	_ = tr
	err := tr.Deliver(context.Background(), gateway.Delivery{
		ID:              "1:100",
		ExternalChatKey: "telegram:123:0:42",
		Result:          gateway.RunResult{Text: "hello"},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != 403 {
		t.Fatalf("error = %v", err)
	}
	assertNoCalls(t, fake, "sendMessage", 50*time.Millisecond)
}

func TestDeliverFallbackSendErrorReturned(t *testing.T) {
	fake := newFakeBot(t, "tok")
	fake.richSupported = false
	fake.fail("sendMessage", 400, 0, "Bad Request: chat not found", 1)
	tr, cancel := startTransport(t, fake, nil)
	defer cancel()
	_ = tr
	err := tr.Deliver(context.Background(), gateway.Delivery{
		ID:              "1:100",
		ExternalChatKey: "telegram:123:0:42",
		Result:          gateway.RunResult{Text: "hello"},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), fake.token) {
		t.Fatalf("token leaked: %v", err)
	}
}

func TestDeliverStreamingOffNoDrafts(t *testing.T) {
	fake := newFakeBot(t, "tok")
	tr, cancel := startTransport(t, fake, nil)
	defer cancel()
	_ = tr
	err := tr.Deliver(context.Background(), gateway.Delivery{
		ID:              "1:100",
		ExternalChatKey: "telegram:123:0:42",
		Result:          gateway.RunResult{Text: "p1\n\np2\n\np3"},
	})
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	waitCalls(t, fake, "sendRichMessage", 1)
	assertNoCalls(t, fake, "sendRichMessageDraft", 50*time.Millisecond)
}

func TestDeliverStreamingSendsProgressiveDrafts(t *testing.T) {
	fake := newFakeBot(t, "tok")
	tr, cancel := startTransport(t, fake, func(o *Options) {
		o.Streaming = true
		o.StreamingInterval = time.Nanosecond
	})
	defer cancel()
	_ = tr
	text := "p1\n\np2\n\np3\n\np4"
	err := tr.Deliver(context.Background(), gateway.Delivery{
		ID:              "1:100",
		ExternalChatKey: "telegram:123:0:42",
		Result:          gateway.RunResult{Text: text},
	})
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	drafts := waitCalls(t, fake, "sendRichMessageDraft", 3)
	want := []string{"p1", "p1\n\np2", "p1\n\np2\n\np3"}
	for i, call := range drafts {
		var rich InputRichMessage
		if err := json.Unmarshal([]byte(call.param("rich_message")), &rich); err != nil {
			t.Fatalf("decode draft %d: %v", i, err)
		}
		if rich.Markdown != want[i] {
			t.Fatalf("draft %d markdown = %q, want %q", i, rich.Markdown, want[i])
		}
		if call.param("draft_id") == "" || call.param("draft_id") == "0" {
			t.Fatalf("draft %d draft_id = %s", i, call.param("draft_id"))
		}
		if call.param("draft_id") != drafts[0].param("draft_id") {
			t.Fatalf("draft ids differ: %s vs %s", call.param("draft_id"), drafts[0].param("draft_id"))
		}
	}
	final := waitCalls(t, fake, "sendRichMessage", 1)
	var rich InputRichMessage
	if err := json.Unmarshal([]byte(final[0].param("rich_message")), &rich); err != nil {
		t.Fatalf("decode final: %v", err)
	}
	if rich.Markdown != text {
		t.Fatalf("final markdown = %q", rich.Markdown)
	}
}

func TestDeliverStreamingSingleParagraphNoDrafts(t *testing.T) {
	fake := newFakeBot(t, "tok")
	tr, cancel := startTransport(t, fake, func(o *Options) {
		o.Streaming = true
		o.StreamingInterval = time.Nanosecond
	})
	defer cancel()
	_ = tr
	err := tr.Deliver(context.Background(), gateway.Delivery{
		ID:              "1:100",
		ExternalChatKey: "telegram:123:0:42",
		Result:          gateway.RunResult{Text: "single paragraph"},
	})
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	waitCalls(t, fake, "sendRichMessage", 1)
	assertNoCalls(t, fake, "sendRichMessageDraft", 50*time.Millisecond)
}

func TestReplyParamsFor(t *testing.T) {
	if got := replyParamsFor("1:100"); got == nil || got.MessageID != 100 {
		t.Fatalf("replyParamsFor = %+v", got)
	}
	if got := replyParamsFor("42"); got != nil {
		t.Fatalf("replyParamsFor = %+v", got)
	}
	if got := replyParamsFor("x:y"); got != nil {
		t.Fatalf("replyParamsFor = %+v", got)
	}
	if got := replyParamsFor("1:2:3"); got != nil {
		t.Fatalf("replyParamsFor = %+v", got)
	}
}

func TestStreamingInterval(t *testing.T) {
	tr := New(Options{})
	if got := tr.streamingInterval(); got != 3*time.Second {
		t.Fatalf("default = %v", got)
	}
	tr2 := New(Options{StreamingInterval: time.Second})
	if got := tr2.streamingInterval(); got != time.Second {
		t.Fatalf("custom = %v", got)
	}
}

func TestDeliverStreamingDraftFailureFallsThroughToFinal(t *testing.T) {
	fake := newFakeBot(t, "tok")
	fake.fail("sendRichMessageDraft", 400, 0, "Bad Request: draft failed", 1)
	tr, cancel := startTransport(t, fake, func(o *Options) {
		o.Streaming = true
		o.StreamingInterval = time.Nanosecond
	})
	defer cancel()
	_ = tr
	text := "p1\n\np2\n\np3"
	err := tr.Deliver(context.Background(), gateway.Delivery{
		ID:              "1:100",
		ExternalChatKey: "telegram:123:0:42",
		Result:          gateway.RunResult{Text: text},
	})
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	waitCalls(t, fake, "sendRichMessageDraft", 1)
	final := waitCalls(t, fake, "sendRichMessage", 1)
	var rich InputRichMessage
	if err := json.Unmarshal([]byte(final[0].param("rich_message")), &rich); err != nil {
		t.Fatalf("decode final: %v", err)
	}
	if rich.Markdown != text {
		t.Fatalf("final markdown = %q", rich.Markdown)
	}
}

func TestIsFallbackError(t *testing.T) {
	if !isFallbackError(&APIError{Code: 400}) {
		t.Fatal("400 must fall back")
	}
	if !isFallbackError(&APIError{Code: 404}) {
		t.Fatal("404 must fall back")
	}
	if isFallbackError(&APIError{Code: 403}) {
		t.Fatal("403 must not fall back")
	}
	if isFallbackError(errors.New("network down")) {
		t.Fatal("non api error must not fall back")
	}
}

func TestAPIBase(t *testing.T) {
	if got := New(Options{}).apiBase(); got != apiBaseDefault {
		t.Fatalf("default = %s", got)
	}
	if got := New(Options{APIBase: "http://example.test/"}).apiBase(); got != "http://example.test" {
		t.Fatalf("custom = %s", got)
	}
}

func TestHTTPClient(t *testing.T) {
	if got := New(Options{}).httpClient(); got != http.DefaultClient {
		t.Fatal("default client mismatch")
	}
	custom := &http.Client{}
	if got := New(Options{HTTP: custom}).httpClient(); got != custom {
		t.Fatal("custom client mismatch")
	}
}
