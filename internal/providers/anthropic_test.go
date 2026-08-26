package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/retry"
)

// anthropicSSEEvent is one SSE frame: the event name and its data payload.
type anthropicSSEEvent struct {
	name string
	data string
}

// anthropicCaptureServer serves the given SSE events with event: names,
// flushing each frame, and records the request it received.
func anthropicCaptureServer(t *testing.T, events ...anthropicSSEEvent) (*httptest.Server, *capturedRequest) {
	t.Helper()
	captured := &capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		captured.method = r.Method
		captured.path = r.URL.Path
		captured.header = r.Header.Clone()
		captured.body = body

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if fl, ok := w.(http.Flusher); ok {
			for _, e := range events {
				fmt.Fprintf(w, "event: %s\n", e.name)
				fmt.Fprintf(w, "data: %s\n\n", e.data)
				fl.Flush()
			}
		}
	}))
	return srv, captured
}

// anthropicTestDriver returns a driver pointed at the given base URL with a
// fixed credential.
func anthropicTestDriver(t *testing.T, baseURL string, oauth bool) *Anthropic {
	t.Helper()
	return NewAnthropic(AnthropicConfig{
		BaseURL:    baseURL,
		ProviderID: "anthropic",
		APIKey: func(context.Context) (string, error) {
			return "sk-ant-test", nil
		},
		OAuth: oauth,
	}, nil)
}

// messageStart returns a message_start event with the given id and usage.
func messageStart(id, usage string) anthropicSSEEvent {
	if usage == "" {
		usage = `{"input_tokens":25,"output_tokens":1}`
	}
	return anthropicSSEEvent{
		name: "message_start",
		data: fmt.Sprintf(`{"type":"message_start","message":{"id":%q,"type":"message","role":"assistant","content":[],"model":"claude-test","stop_reason":null,"usage":%s}}`, id, usage),
	}
}

// messageDelta returns a message_delta event with the given stop reason.
func messageDelta(stopReason string) anthropicSSEEvent {
	return anthropicSSEEvent{
		name: "message_delta",
		data: fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":%q,"stop_sequence":null}}`, stopReason),
	}
}

// TestNewAnthropicDefaults checks the constructor defaults: base URL,
// max_tokens budget, prefix fallback, and default http client.
func TestNewAnthropicDefaults(t *testing.T) {
	d := NewAnthropic(AnthropicConfig{ProviderID: "p"}, nil)
	if d.baseURL != DefaultAnthropicBaseURL {
		t.Errorf("baseURL = %q, want default", d.baseURL)
	}
	if d.maxTokens != defaultAnthropicMaxTokens {
		t.Errorf("maxTokens = %d, want default %d", d.maxTokens, defaultAnthropicMaxTokens)
	}
	if d.prefix != "p" {
		t.Errorf("prefix = %q, want provider id", d.prefix)
	}
	if d.http == nil {
		t.Fatal("default http client is nil")
	}
	if d.http.Timeout != 0 {
		t.Errorf("http.Timeout = %v, want 0 (cancellation via request context)", d.http.Timeout)
	}
}

// TestNewAnthropicOverrides verifies BaseURL trimming, MaxTokens override,
// and a provided http client.
func TestNewAnthropicOverrides(t *testing.T) {
	given := &http.Client{}
	d := NewAnthropic(AnthropicConfig{BaseURL: "https://gateway.example.com/v1/messages/", ProviderID: "p", MaxTokens: 8192}, given)
	if d.baseURL != "https://gateway.example.com/v1/messages" {
		t.Errorf("baseURL = %q, want trailing slash trimmed", d.baseURL)
	}
	if d.maxTokens != 8192 {
		t.Errorf("maxTokens = %d, want 8192", d.maxTokens)
	}
	if d.http != given {
		t.Error("provided http client not used as-is")
	}
	d2 := NewAnthropic(AnthropicConfig{}, nil)
	if d2.prefix != "provider" {
		t.Errorf("prefix = %q, want fallback %q", d2.prefix, "provider")
	}
}

// TestAnthropicStreamTurnText drives one full text turn and verifies the
// accumulated blocks, usage, stop reason, identity, and callbacks.
func TestAnthropicStreamTurnText(t *testing.T) {
	events := []anthropicSSEEvent{
		messageStart("msg_01", ""),
		{name: "content_block_start", data: `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
		{name: "content_block_delta", data: `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`},
		{name: "content_block_delta", data: `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}`},
		{name: "content_block_stop", data: `{"type":"content_block_stop","index":0}`},
		{name: "message_delta", data: `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":15}}`},
		{name: "message_stop", data: `{"type":"message_stop"}`},
	}
	srv, _ := anthropicCaptureServer(t, events...)
	defer srv.Close()

	var textDeltas, thinkDeltas []string
	msg, err := anthropicTestDriver(t, srv.URL, false).StreamTurn(context.Background(), baseTurnReq(),
		func(d string) { textDeltas = append(textDeltas, d) },
		func(d string) { thinkDeltas = append(thinkDeltas, d) })
	if err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}

	if len(msg.Content) != 1 || msg.Content[0].Type != agent.BlockTypeText || msg.Content[0].Text != "Hello world" {
		t.Errorf("content = %+v, want one text block %q", msg.Content, "Hello world")
	}
	if msg.StopReason != "stop" {
		t.Errorf("stopReason = %q, want stop", msg.StopReason)
	}
	if msg.API != AnthropicAPI || msg.Provider != "anthropic" || msg.Model != "test/model" {
		t.Errorf("identity = api %q provider %q model %q", msg.API, msg.Provider, msg.Model)
	}
	if msg.ResponseID != "msg_01" {
		t.Errorf("responseId = %q, want msg_01", msg.ResponseID)
	}
	if msg.Timestamp <= 0 {
		t.Errorf("timestamp = %d, want positive", msg.Timestamp)
	}
	// message_start reports input 25/output 1; message_delta overrides
	// output to 15 and total is recomputed from components.
	if msg.Usage.Input != 25 || msg.Usage.Output != 15 || msg.Usage.TotalTokens != 40 {
		t.Errorf("usage = %+v, want input 25 output 15 total 40", msg.Usage)
	}
	if want := []string{"Hello", " world"}; !equalStrings(textDeltas, want) {
		t.Errorf("text deltas = %v, want %v", textDeltas, want)
	}
	if len(thinkDeltas) != 0 {
		t.Errorf("thinking deltas = %v, want none", thinkDeltas)
	}
}

// TestAnthropicStreamTurnThinkingSignature feeds thinking deltas and
// signature fragments and verifies both accumulate on the thinking block.
func TestAnthropicStreamTurnThinkingSignature(t *testing.T) {
	events := []anthropicSSEEvent{
		messageStart("msg_th", `{"input_tokens":10,"output_tokens":1}`),
		{name: "content_block_start", data: `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`},
		{name: "content_block_delta", data: `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Let me think"}}`},
		{name: "content_block_delta", data: `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-1"}}`},
		{name: "content_block_delta", data: `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":" more"}}`},
		{name: "content_block_delta", data: `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"-2"}}`},
		{name: "content_block_stop", data: `{"type":"content_block_stop","index":0}`},
		messageDelta("end_turn"),
		{name: "message_stop", data: `{"type":"message_stop"}`},
	}
	srv, _ := anthropicCaptureServer(t, events...)
	defer srv.Close()

	var thinkDeltas []string
	msg, err := anthropicTestDriver(t, srv.URL, false).StreamTurn(context.Background(), baseTurnReq(),
		nil, func(d string) { thinkDeltas = append(thinkDeltas, d) })
	if err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}

	if len(msg.Content) != 1 {
		t.Fatalf("content = %+v, want one thinking block", msg.Content)
	}
	b := msg.Content[0]
	if b.Type != agent.BlockTypeThinking {
		t.Errorf("block type = %q, want thinking", b.Type)
	}
	if b.Thinking != "Let me think more" {
		t.Errorf("thinking = %q, want accumulated text", b.Thinking)
	}
	if b.ThinkingSignature != "sig-1-2" {
		t.Errorf("thinkingSignature = %q, want sig-1-2", b.ThinkingSignature)
	}
	if b.Redacted {
		t.Error("block marked redacted, want false")
	}
	if want := []string{"Let me think", " more"}; !equalStrings(thinkDeltas, want) {
		t.Errorf("thinking deltas = %v, want %v", thinkDeltas, want)
	}
}

// TestAnthropicStreamTurnRedactedThinking verifies the redacted_thinking
// block shape: opaque payload as signature, redacted flag, fixed text.
func TestAnthropicStreamTurnRedactedThinking(t *testing.T) {
	events := []anthropicSSEEvent{
		messageStart("msg_red", ""),
		{name: "content_block_start", data: `{"type":"content_block_start","index":0,"content_block":{"type":"redacted_thinking","data":"opaque-payload-123"}}`},
		{name: "content_block_stop", data: `{"type":"content_block_stop","index":0}`},
		messageDelta("end_turn"),
		{name: "message_stop", data: `{"type":"message_stop"}`},
	}
	srv, _ := anthropicCaptureServer(t, events...)
	defer srv.Close()

	msg, err := anthropicTestDriver(t, srv.URL, false).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	if len(msg.Content) != 1 {
		t.Fatalf("content = %+v, want one block", msg.Content)
	}
	b := msg.Content[0]
	if b.Type != agent.BlockTypeThinking || !b.Redacted {
		t.Errorf("block = %+v, want redacted thinking", b)
	}
	if b.Thinking != "[Reasoning redacted]" {
		t.Errorf("thinking = %q, want redaction marker", b.Thinking)
	}
	if b.ThinkingSignature != "opaque-payload-123" {
		t.Errorf("thinkingSignature = %q, want opaque payload", b.ThinkingSignature)
	}
}

// TestAnthropicStreamTurnToolUseFragmented feeds one tool use whose input
// arrives as many input_json_delta fragments and verifies the accumulated
// arguments and the toolUse stop reason.
func TestAnthropicStreamTurnToolUseFragmented(t *testing.T) {
	events := []anthropicSSEEvent{
		messageStart("msg_tool", ""),
		{name: "content_block_start", data: `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_01","name":"read","input":{}}}`},
		{name: "content_block_delta", data: `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}`},
		{name: "content_block_delta", data: `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\"main"}}`},
		{name: "content_block_delta", data: `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":".go\"}"}}`},
		{name: "content_block_stop", data: `{"type":"content_block_stop","index":0}`},
		messageDelta("tool_use"),
		{name: "message_stop", data: `{"type":"message_stop"}`},
	}
	srv, _ := anthropicCaptureServer(t, events...)
	defer srv.Close()

	msg, err := anthropicTestDriver(t, srv.URL, false).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}

	if msg.StopReason != "toolUse" {
		t.Errorf("stopReason = %q, want toolUse", msg.StopReason)
	}
	if len(msg.Content) != 1 {
		t.Fatalf("content = %+v, want one tool call block", msg.Content)
	}
	block := msg.Content[0]
	if block.Type != agent.BlockTypeToolCall || block.ID != "toolu_01" || block.Name != "read" {
		t.Errorf("block = %+v, want toolCall toolu_01/read", block)
	}
	if got := string(block.Arguments); got != `{"path":"main.go"}` {
		t.Errorf("arguments = %s, want {\"path\":\"main.go\"}", got)
	}
}

// TestAnthropicStreamTurnInterleavedBlocks feeds text, thinking, and tool
// blocks whose starts, deltas, and stops interleave by index, plus a ping
// event, and verifies the accumulated order matches first-appearance.
func TestAnthropicStreamTurnInterleavedBlocks(t *testing.T) {
	events := []anthropicSSEEvent{
		messageStart("msg_mix", ""),
		{name: "content_block_start", data: `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
		{name: "content_block_delta", data: `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"First"}}`},
		{name: "content_block_start", data: `{"type":"content_block_start","index":1,"content_block":{"type":"thinking","thinking":"","signature":""}}`},
		{name: "content_block_delta", data: `{"type":"content_block_delta","index":1,"delta":{"type":"thinking_delta","thinking":"Think"}}`},
		{name: "content_block_start", data: `{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_2","name":"exec","input":{}}}`},
		{name: "content_block_delta", data: `{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"cmd\":\"ls\"}"}}`},
		{name: "ping", data: `{"type":"ping"}`},
		{name: "content_block_start", data: `{"type":"content_block_start","index":3,"content_block":{"type":"text","text":""}}`},
		{name: "content_block_delta", data: `{"type":"content_block_delta","index":3,"delta":{"type":"text_delta","text":"Second"}}`},
		{name: "content_block_stop", data: `{"type":"content_block_stop","index":2}`},
		{name: "content_block_stop", data: `{"type":"content_block_stop","index":1}`},
		{name: "content_block_stop", data: `{"type":"content_block_stop","index":0}`},
		{name: "content_block_stop", data: `{"type":"content_block_stop","index":3}`},
		messageDelta("tool_use"),
		{name: "message_stop", data: `{"type":"message_stop"}`},
	}
	srv, _ := anthropicCaptureServer(t, events...)
	defer srv.Close()

	msg, err := anthropicTestDriver(t, srv.URL, false).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}

	if len(msg.Content) != 4 {
		t.Fatalf("content = %+v, want 4 blocks", msg.Content)
	}
	want := []struct {
		typ  string
		text string
		name string
	}{
		{typ: agent.BlockTypeText, text: "First"},
		{typ: agent.BlockTypeThinking, text: "Think"},
		{typ: agent.BlockTypeToolCall, name: "exec"},
		{typ: agent.BlockTypeText, text: "Second"},
	}
	for i, w := range want {
		b := msg.Content[i]
		if b.Type != w.typ {
			t.Errorf("block[%d].type = %q, want %q", i, b.Type, w.typ)
		}
		switch w.typ {
		case agent.BlockTypeText:
			if b.Text != w.text {
				t.Errorf("block[%d].text = %q, want %q", i, b.Text, w.text)
			}
		case agent.BlockTypeThinking:
			if b.Thinking != w.text {
				t.Errorf("block[%d].thinking = %q, want %q", i, b.Thinking, w.text)
			}
		case agent.BlockTypeToolCall:
			if b.Name != w.name || string(b.Arguments) != `{"cmd":"ls"}` {
				t.Errorf("block[%d] = %+v, want exec tool call", i, b)
			}
		}
	}
}

// TestAnthropicStreamTurnMaxTokensStop maps max_tokens to the "length"
// stop reason.
func TestAnthropicStreamTurnMaxTokensStop(t *testing.T) {
	events := []anthropicSSEEvent{
		messageStart("msg_len", ""),
		{name: "content_block_start", data: `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
		{name: "content_block_delta", data: `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`},
		{name: "content_block_stop", data: `{"type":"content_block_stop","index":0}`},
		messageDelta("max_tokens"),
		{name: "message_stop", data: `{"type":"message_stop"}`},
	}
	srv, _ := anthropicCaptureServer(t, events...)
	defer srv.Close()

	msg, err := anthropicTestDriver(t, srv.URL, false).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	if msg.StopReason != "length" {
		t.Errorf("stopReason = %q, want length", msg.StopReason)
	}
}

// TestAnthropicStreamTurnRefusal maps a refusal stop to an error carrying
// the stop_details explanation.
func TestAnthropicStreamTurnRefusal(t *testing.T) {
	events := []anthropicSSEEvent{
		messageStart("msg_ref", ""),
		{name: "content_block_start", data: `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
		{name: "content_block_stop", data: `{"type":"content_block_stop","index":0}`},
		{name: "message_delta", data: `{"type":"message_delta","delta":{"stop_reason":"refusal","stop_details":{"type":"refusal","explanation":"policy blocks this"}}}`},
		{name: "message_stop", data: `{"type":"message_stop"}`},
	}
	srv, _ := anthropicCaptureServer(t, events...)
	defer srv.Close()

	msg, err := anthropicTestDriver(t, srv.URL, false).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if msg != nil {
		t.Errorf("msg = %+v, want nil on refusal", msg)
	}
	if err == nil || !strings.Contains(err.Error(), "policy blocks this") {
		t.Errorf("error = %v, want refusal explanation", err)
	}
}

// TestAnthropicStreamTurnUsageMapping verifies the cache and reasoning
// token mapping across message_start and message_delta, including the
// recomputed total.
func TestAnthropicStreamTurnUsageMapping(t *testing.T) {
	events := []anthropicSSEEvent{
		messageStart("msg_u", `{"input_tokens":100,"output_tokens":5,"cache_read_input_tokens":30,"cache_creation_input_tokens":20}`),
		{name: "content_block_start", data: `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
		{name: "content_block_delta", data: `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`},
		{name: "content_block_stop", data: `{"type":"content_block_stop","index":0}`},
		{name: "message_delta", data: `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":40,"output_tokens_details":{"thinking_tokens":25}}}`},
		{name: "message_stop", data: `{"type":"message_stop"}`},
	}
	srv, _ := anthropicCaptureServer(t, events...)
	defer srv.Close()

	msg, err := anthropicTestDriver(t, srv.URL, false).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	u := msg.Usage
	if u.Input != 100 || u.Output != 40 {
		t.Errorf("usage input/output = %d/%d, want 100/40", u.Input, u.Output)
	}
	if u.CacheRead != 30 || u.CacheWrite != 20 {
		t.Errorf("cacheRead/cacheWrite = %d/%d, want 30/20", u.CacheRead, u.CacheWrite)
	}
	if u.Reasoning != 25 {
		t.Errorf("reasoning = %d, want 25", u.Reasoning)
	}
	if u.TotalTokens != 190 {
		t.Errorf("totalTokens = %d, want 190", u.TotalTokens)
	}
	// Cost is zero unless the provider reports it.
	if u.Cost != (agent.Cost{}) {
		t.Errorf("cost = %+v, want zero", u.Cost)
	}
}

// TestAnthropicStreamTurnUsageNullDeltas verifies message_delta usage with
// null fields does not wipe the counts captured at message_start.
func TestAnthropicStreamTurnUsageNullDeltas(t *testing.T) {
	events := []anthropicSSEEvent{
		messageStart("msg_n", `{"input_tokens":7,"output_tokens":3}`),
		{name: "message_delta", data: `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":null,"output_tokens":null,"cache_read_input_tokens":null,"cache_creation_input_tokens":null,"output_tokens_details":null}}`},
		{name: "message_stop", data: `{"type":"message_stop"}`},
	}
	srv, _ := anthropicCaptureServer(t, events...)
	defer srv.Close()

	msg, err := anthropicTestDriver(t, srv.URL, false).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	if msg.Usage.Input != 7 || msg.Usage.Output != 3 || msg.Usage.TotalTokens != 10 {
		t.Errorf("usage = %+v, want input 7 output 3 total 10 preserved", msg.Usage)
	}
}

// TestAnthropicStreamTurnRequestShape drives a full turn and verifies the
// exact request the driver sends: headers, method, path, and body shape
// including system blocks, assistant content replay with signatures and
// redacted thinking, grouped tool results, tools, and tool_choice.
func TestAnthropicStreamTurnRequestShape(t *testing.T) {
	events := []anthropicSSEEvent{
		messageStart("msg_rq", ""),
		{name: "content_block_start", data: `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
		{name: "content_block_delta", data: `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`},
		{name: "content_block_stop", data: `{"type":"content_block_stop","index":0}`},
		messageDelta("end_turn"),
		{name: "message_stop", data: `{"type":"message_stop"}`},
	}
	srv, captured := anthropicCaptureServer(t, events...)
	defer srv.Close()

	req := &agent.TurnRequest{
		Model:  "claude-sonnet-4.5",
		System: "You are a helpful assistant",
		Messages: []*agent.Message{
			{User: &agent.UserMessage{Role: string(agent.RoleUser), Content: json.RawMessage(`"hello"`)}},
			{Assistant: &agent.AssistantMessage{Role: string(agent.RoleAssistant), Content: []agent.ContentBlock{
				{Type: agent.BlockTypeText, Text: "Sure"},
				{Type: agent.BlockTypeThinking, Thinking: "let me check", ThinkingSignature: "sig-1"},
				{Type: agent.BlockTypeThinking, Thinking: "[Reasoning redacted]", ThinkingSignature: "opaque-9", Redacted: true},
				{Type: agent.BlockTypeToolCall, ID: "toolu_01", Name: "read", Arguments: json.RawMessage(`{"path":"a.go"}`)},
			}}},
			{ToolResult: &agent.ToolResultMessage{
				Role:       string(agent.RoleToolResult),
				ToolCallID: "toolu_01",
				ToolName:   "read",
				Content:    []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "file body"}},
			}},
			{ToolResult: &agent.ToolResultMessage{
				Role:       string(agent.RoleToolResult),
				ToolCallID: "toolu_02",
				ToolName:   "exec",
				Content:    []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "done"}},
			}},
		},
		Tools: []agent.Tool{
			stubTool{name: "read", desc: "Reads a file", schema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)},
		},
	}

	msg, err := anthropicTestDriver(t, srv.URL, false).StreamTurn(context.Background(), req, nil, nil)
	if err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	if len(msg.Content) != 1 || msg.Content[0].Text != "hi" {
		t.Fatalf("msg = %+v, want single text block %q", msg, "hi")
	}

	if captured.method != http.MethodPost {
		t.Errorf("method = %q, want POST", captured.method)
	}
	if captured.path != "/" {
		t.Errorf("path = %q, want %q", captured.path, "/")
	}
	if got := captured.header.Get("x-api-key"); got != "sk-ant-test" {
		t.Errorf("x-api-key = %q", got)
	}
	if got := captured.header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, want unset for api key auth", got)
	}
	if got := captured.header.Get("anthropic-version"); got != "2023-06-01" {
		t.Errorf("anthropic-version = %q", got)
	}

	var got struct {
		Model     string `json:"model"`
		MaxTokens int64  `json:"max_tokens"`
		Stream    bool   `json:"stream"`
		System    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"system"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
		Tools []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"input_schema"`
		} `json:"tools"`
		ToolChoice *struct {
			Type string `json:"type"`
		} `json:"tool_choice"`
	}
	if err := json.Unmarshal(captured.body, &got); err != nil {
		t.Fatalf("unmarshal captured body: %v", err)
	}

	if got.Model != req.Model {
		t.Errorf("model = %q, want %q", got.Model, req.Model)
	}
	if got.MaxTokens != defaultAnthropicMaxTokens {
		t.Errorf("max_tokens = %d, want %d", got.MaxTokens, defaultAnthropicMaxTokens)
	}
	if !got.Stream {
		t.Error("stream = false, want true")
	}
	if len(got.System) != 1 || got.System[0].Type != "text" || got.System[0].Text != "You are a helpful assistant" {
		t.Errorf("system = %+v, want one text block", got.System)
	}
	if got.ToolChoice == nil || got.ToolChoice.Type != "auto" {
		t.Errorf("tool_choice = %+v, want auto", got.ToolChoice)
	}
	if len(got.Tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(got.Tools))
	}
	if got.Tools[0].Name != "read" || got.Tools[0].Description != "Reads a file" {
		t.Errorf("tool = %+v, want read/Reads a file", got.Tools[0])
	}
	if string(got.Tools[0].InputSchema) != `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}` {
		t.Errorf("input_schema = %s", got.Tools[0].InputSchema)
	}
	// user, assistant, and one user message grouping the two consecutive
	// tool results: consecutive tool results collapse into a single user
	// message, exactly as Pi's convertMessages does.
	if len(got.Messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(got.Messages))
	}

	// message[0]: user string content.
	if got.Messages[0].Role != "user" || string(got.Messages[0].Content) != `"hello"` {
		t.Errorf("messages[0] = role %q content %s, want user string", got.Messages[0].Role, got.Messages[0].Content)
	}

	// message[1]: assistant content blocks in order, signatures preserved.
	if got.Messages[1].Role != "assistant" {
		t.Errorf("messages[1].role = %q, want assistant", got.Messages[1].Role)
	}
	var asstBlocks []struct {
		Type      string          `json:"type"`
		Text      string          `json:"text"`
		Thinking  string          `json:"thinking"`
		Signature string          `json:"signature"`
		Data      string          `json:"data"`
		ID        string          `json:"id"`
		Name      string          `json:"name"`
		Input     json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(got.Messages[1].Content, &asstBlocks); err != nil {
		t.Fatalf("unmarshal assistant content: %v", err)
	}
	if len(asstBlocks) != 4 {
		t.Fatalf("assistant blocks = %d, want 4", len(asstBlocks))
	}
	if asstBlocks[0].Type != "text" || asstBlocks[0].Text != "Sure" {
		t.Errorf("asst[0] = %+v, want text Sure", asstBlocks[0])
	}
	if asstBlocks[1].Type != "thinking" || asstBlocks[1].Thinking != "let me check" || asstBlocks[1].Signature != "sig-1" {
		t.Errorf("asst[1] = %+v, want thinking with signature", asstBlocks[1])
	}
	if asstBlocks[2].Type != "redacted_thinking" || asstBlocks[2].Data != "opaque-9" {
		t.Errorf("asst[2] = %+v, want redacted_thinking payload", asstBlocks[2])
	}
	if asstBlocks[3].Type != "tool_use" || asstBlocks[3].ID != "toolu_01" || asstBlocks[3].Name != "read" ||
		string(asstBlocks[3].Input) != `{"path":"a.go"}` {
		t.Errorf("asst[3] = %+v, want tool_use read", asstBlocks[3])
	}

	// messages[2]: the two consecutive tool results grouped into one user
	// message with one tool_result block each.
	if got.Messages[2].Role != "user" {
		t.Errorf("messages[2].role = %q, want user", got.Messages[2].Role)
	}
	var toolResults []struct {
		Type      string `json:"type"`
		ToolUseID string `json:"tool_use_id"`
		Content   string `json:"content"`
		IsError   bool   `json:"is_error"`
	}
	if err := json.Unmarshal(got.Messages[2].Content, &toolResults); err != nil {
		t.Fatalf("unmarshal tool results: %v", err)
	}
	if len(toolResults) != 2 {
		t.Fatalf("tool result blocks = %d, want 2 grouped", len(toolResults))
	}
	if toolResults[0].Type != "tool_result" || toolResults[0].ToolUseID != "toolu_01" || toolResults[0].Content != "file body" || toolResults[0].IsError {
		t.Errorf("tool result[0] = %+v", toolResults[0])
	}
	if toolResults[1].ToolUseID != "toolu_02" || toolResults[1].Content != "done" {
		t.Errorf("tool result[1] = %+v", toolResults[1])
	}
}

// TestAnthropicOAuthHeaders verifies the two auth variants: API key sends
// x-api-key, OAuth sends the Bearer token plus the Claude Code identity
// markers and the system prefix.
func TestAnthropicOAuthHeaders(t *testing.T) {
	events := []anthropicSSEEvent{
		messageStart("msg_au", ""),
		messageDelta("end_turn"),
		{name: "message_stop", data: `{"type":"message_stop"}`},
	}

	t.Run("api key", func(t *testing.T) {
		srv, captured := anthropicCaptureServer(t, events...)
		defer srv.Close()
		if _, err := anthropicTestDriver(t, srv.URL, false).StreamTurn(context.Background(), baseTurnReq(), nil, nil); err != nil {
			t.Fatalf("StreamTurn: %v", err)
		}
		if got := captured.header.Get("x-api-key"); got != "sk-ant-test" {
			t.Errorf("x-api-key = %q, want credential", got)
		}
		if got := captured.header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q, want unset", got)
		}
		if got := captured.header.Get("anthropic-beta"); got != "interleaved-thinking-2025-05-14" {
			t.Errorf("anthropic-beta = %q, want interleaved-thinking beta", got)
		}
		if got := captured.header.Get("x-app"); got != "" {
			t.Errorf("x-app = %q, want unset for api key auth", got)
		}
	})

	t.Run("oauth", func(t *testing.T) {
		srv, captured := anthropicCaptureServer(t, events...)
		defer srv.Close()

		req := baseTurnReq()
		req.System = "Be helpful"
		if _, err := anthropicTestDriver(t, srv.URL, true).StreamTurn(context.Background(), req, nil, nil); err != nil {
			t.Fatalf("StreamTurn: %v", err)
		}
		if got := captured.header.Get("Authorization"); got != "Bearer sk-ant-test" {
			t.Errorf("Authorization = %q, want Bearer token", got)
		}
		if got := captured.header.Get("x-api-key"); got != "" {
			t.Errorf("x-api-key = %q, want unset for oauth", got)
		}
		beta := captured.header.Get("anthropic-beta")
		for _, want := range []string{"claude-code-20250219", "oauth-2025-04-20", "interleaved-thinking-2025-05-14"} {
			if !strings.Contains(beta, want) {
				t.Errorf("anthropic-beta = %q, missing %q", beta, want)
			}
		}
		if got := captured.header.Get("user-agent"); got != "claude-cli/2.1.75" {
			t.Errorf("user-agent = %q, want claude-cli/2.1.75", got)
		}
		if got := captured.header.Get("x-app"); got != "cli" {
			t.Errorf("x-app = %q, want cli", got)
		}

		var got struct {
			System []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"system"`
		}
		if err := json.Unmarshal(captured.body, &got); err != nil {
			t.Fatalf("unmarshal body: %v", err)
		}
		if len(got.System) != 2 || got.System[0].Text != claudeCodeSystemPrefix || got.System[1].Text != "Be helpful" {
			t.Errorf("system = %+v, want Claude Code prefix then prompt", got.System)
		}
	})
}

// TestAnthropicOAuthToolNameCasing verifies the Claude Code tool name
// casing round-trip under subscription auth: read -> Read on the wire and
// back to the registered name on receive.
func TestAnthropicOAuthToolNameCasing(t *testing.T) {
	events := []anthropicSSEEvent{
		messageStart("msg_cc", ""),
		{name: "content_block_start", data: `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"Read","input":{"path":"a.go"}}}`},
		{name: "content_block_stop", data: `{"type":"content_block_stop","index":0}`},
		messageDelta("tool_use"),
		{name: "message_stop", data: `{"type":"message_stop"}`},
	}
	srv, captured := anthropicCaptureServer(t, events...)
	defer srv.Close()

	req := baseTurnReq()
	req.Tools = []agent.Tool{
		stubTool{name: "read", desc: "Reads a file", schema: json.RawMessage(`{"type":"object"}`)},
	}
	msg, err := anthropicTestDriver(t, srv.URL, true).StreamTurn(context.Background(), req, nil, nil)
	if err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	if len(msg.Content) != 1 || msg.Content[0].Type != agent.BlockTypeToolCall || msg.Content[0].Name != "read" {
		t.Errorf("block = %+v, want toolCall name mapped back to read", msg.Content)
	}
	// The start event carried the complete input and no input_json_delta
	// fragments followed: the input must be preserved (eager streaming).
	if got := string(msg.Content[0].Arguments); got != `{"path":"a.go"}` {
		t.Errorf("arguments = %s, want start input preserved", got)
	}
	// The wire tool list must carry the Claude Code casing.
	if !strings.Contains(string(captured.body), `"name":"Read"`) {
		t.Errorf("request body missing Claude Code-cased tool name: %s", captured.body)
	}
}

// TestAnthropicStreamTurnNon200 verifies the Anthropic error envelope is
// parsed with the error type surfaced, so the retry classifier can match
// overloaded_error.
func TestAnthropicStreamTurnNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(529)
		fmt.Fprint(w, `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)
	}))
	defer srv.Close()

	msg, err := anthropicTestDriver(t, srv.URL, false).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if msg != nil {
		t.Errorf("msg = %+v, want nil on error", msg)
	}
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "529") {
		t.Errorf("error %q missing status", err)
	}
	if !strings.Contains(err.Error(), "Overloaded") {
		t.Errorf("error %q missing message", err)
	}
	if !strings.Contains(err.Error(), "overloaded_error") {
		t.Errorf("error %q missing error type", err)
	}
	// The surfaced text must classify as retryable: classification lives
	// in internal/retry, which matches on the provider text alone.
	if !retry.Classify(err.Error()) {
		t.Errorf("error %q not classified as retryable", err)
	}
}

// TestAnthropicStreamTurnNon200NonJSON verifies the fallback for error
// bodies that are not the provider envelope.
func TestAnthropicStreamTurnNon200NonJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, "upstream exploded")
	}))
	defer srv.Close()

	_, err := anthropicTestDriver(t, srv.URL, false).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "502") || !strings.Contains(err.Error(), "upstream exploded") {
		t.Errorf("error = %q, want status and body", err)
	}
}

// TestAnthropicStreamTurnSSEError verifies an error SSE event mid-stream
// aborts the turn with the raw provider payload.
func TestAnthropicStreamTurnSSEError(t *testing.T) {
	events := []anthropicSSEEvent{
		messageStart("msg_err", ""),
		{name: "content_block_start", data: `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
		{name: "content_block_delta", data: `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`},
		{name: "error", data: `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`},
	}
	srv, _ := anthropicCaptureServer(t, events...)
	defer srv.Close()

	msg, err := anthropicTestDriver(t, srv.URL, false).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if msg != nil {
		t.Errorf("msg = %+v, want nil on SSE error", msg)
	}
	if err == nil {
		t.Fatal("want error from SSE event")
	}
	if !strings.Contains(err.Error(), "overloaded_error") || !strings.Contains(err.Error(), "Overloaded") {
		t.Errorf("error = %q, want provider payload", err)
	}
}

// TestAnthropicStreamTurnPrematureEOF verifies the error when the stream
// ends before message_stop.
func TestAnthropicStreamTurnPrematureEOF(t *testing.T) {
	events := []anthropicSSEEvent{
		messageStart("msg_eof", ""),
		{name: "content_block_start", data: `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
		{name: "content_block_delta", data: `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"half"}}`},
	}
	srv, _ := anthropicCaptureServer(t, events...)
	defer srv.Close()

	msg, err := anthropicTestDriver(t, srv.URL, false).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if msg != nil {
		t.Errorf("msg = %+v, want nil on premature EOF", msg)
	}
	if err == nil {
		t.Fatal("want error on premature EOF")
	}
	if !strings.Contains(err.Error(), "premature") || !strings.Contains(err.Error(), "message_stop") {
		t.Errorf("error = %q, want premature message_stop", err)
	}
}

// TestAnthropicStreamTurnEmptyStream treats an immediately closed stream
// as premature EOF.
func TestAnthropicStreamTurnEmptyStream(t *testing.T) {
	srv, _ := anthropicCaptureServer(t)
	defer srv.Close()

	_, err := anthropicTestDriver(t, srv.URL, false).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "premature") {
		t.Errorf("error = %v, want premature EOF", err)
	}
}

// TestAnthropicStreamTurnMissingStopReason verifies an error when the
// stream completes without a message_delta stop reason.
func TestAnthropicStreamTurnMissingStopReason(t *testing.T) {
	events := []anthropicSSEEvent{
		messageStart("msg_ns", ""),
		{name: "message_stop", data: `{"type":"message_stop"}`},
	}
	srv, _ := anthropicCaptureServer(t, events...)
	defer srv.Close()

	_, err := anthropicTestDriver(t, srv.URL, false).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "stop reason") {
		t.Errorf("error = %v, want missing stop reason", err)
	}
}

// TestAnthropicStreamTurnCancellation verifies that cancelling the context
// aborts a stalled stream and returns context.Canceled.
func TestAnthropicStreamTurnCancellation(t *testing.T) {
	firstChunk := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\n")
		fmt.Fprint(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_cx\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n")
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
		close(firstChunk)
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type result struct {
		msg *agent.AssistantMessage
		err error
	}
	ch := make(chan result, 1)
	go func() {
		m, err := anthropicTestDriver(t, srv.URL, false).StreamTurn(ctx, baseTurnReq(), nil, nil)
		ch <- result{m, err}
	}()

	<-firstChunk
	cancel()
	res := <-ch
	if res.err == nil {
		t.Fatal("want error after cancel")
	}
	if !errors.Is(res.err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", res.err)
	}
	if res.msg != nil {
		t.Errorf("msg = %+v, want nil on cancellation", res.msg)
	}
}

// TestAnthropicStreamTurnNilArguments guards the nil contract of
// StreamTurn.
func TestAnthropicStreamTurnNilArguments(t *testing.T) {
	d := anthropicTestDriver(t, "https://example.com", false)
	if _, err := d.StreamTurn(context.Background(), nil, nil, nil); err == nil {
		t.Error("nil request: want error")
	}
	req := baseTurnReq()
	req.Messages = nil
	if _, err := d.StreamTurn(context.Background(), req, nil, nil); err == nil {
		t.Error("nil messages: want error")
	}
}

// TestAnthropicStreamTurnMissingAPIKey verifies the driver fails clearly
// without a credential resolver.
func TestAnthropicStreamTurnMissingAPIKey(t *testing.T) {
	d := NewAnthropic(AnthropicConfig{BaseURL: "https://example.com", ProviderID: "anthropic"}, nil)
	_, err := d.StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "no API key") {
		t.Errorf("error = %v, want missing API key", err)
	}
}

// TestAnthropicStreamTurnErrorPrefix verifies every error is prefixed with
// the provider id.
func TestAnthropicStreamTurnErrorPrefix(t *testing.T) {
	d := NewAnthropic(AnthropicConfig{BaseURL: "https://example.com", ProviderID: "anthropic"}, nil)
	_, err := d.StreamTurn(context.Background(), nil, nil, nil)
	if err == nil || !strings.HasPrefix(err.Error(), "anthropic: ") {
		t.Errorf("error = %q, want anthropic: prefix", err)
	}
}

// TestAnthropicBuildMessages verifies the wire conversion of every message
// variant: string and block user content, empty-content dropping, assistant
// replay with signature rules, grouped tool results, and tool input
// defaults.
func TestAnthropicBuildMessages(t *testing.T) {
	cases := []struct {
		name     string
		system   string
		oauth    bool
		messages []*agent.Message
		want     string
	}{
		{
			name:     "user string content",
			messages: []*agent.Message{{User: &agent.UserMessage{Content: json.RawMessage(`"hi"`)}}},
			want:     `[{"role":"user","content":"hi"}]`,
		},
		{
			name: "user block array to text blocks",
			messages: []*agent.Message{{User: &agent.UserMessage{Content: json.RawMessage(
				`[{"type":"text","text":"part one"},{"type":"text","text":"part two"}]`)}}},
			want: `[{"role":"user","content":[{"type":"text","text":"part one"},{"type":"text","text":"part two"}]}]`,
		},
		{
			name:     "empty user string dropped",
			messages: []*agent.Message{{User: &agent.UserMessage{Content: json.RawMessage(`""`)}}},
			want:     `[]`,
		},
		{
			name: "empty user blocks dropped",
			messages: []*agent.Message{{User: &agent.UserMessage{Content: json.RawMessage(
				`[{"type":"text","text":"  "}]`)}}},
			want: `[]`,
		},
		{
			name: "assistant replay with signature rules",
			messages: []*agent.Message{{Assistant: &agent.AssistantMessage{Content: []agent.ContentBlock{
				{Type: agent.BlockTypeText, Text: "Sure"},
				{Type: agent.BlockTypeThinking, Thinking: "think", ThinkingSignature: "sig-1"},
				{Type: agent.BlockTypeThinking, Thinking: "no signature here"},
				{Type: agent.BlockTypeThinking, Thinking: " ", ThinkingSignature: ""},
				{Type: agent.BlockTypeThinking, Thinking: "x", ThinkingSignature: "op", Redacted: true},
				{Type: agent.BlockTypeToolCall, ID: "c1", Name: "read", Arguments: json.RawMessage(`{"path":"a"}`)},
				{Type: agent.BlockTypeToolCall, ID: "c2", Name: "exec", Arguments: nil},
			}}}},
			want: `[{"role":"assistant","content":[` +
				`{"type":"text","text":"Sure"},` +
				`{"type":"thinking","thinking":"think","signature":"sig-1"},` +
				`{"type":"text","text":"no signature here"},` +
				`{"type":"redacted_thinking","data":"op"},` +
				`{"type":"tool_use","id":"c1","name":"read","input":{"path":"a"}},` +
				`{"type":"tool_use","id":"c2","name":"exec","input":{}}]}]`,
		},
		{
			name:     "assistant without content dropped",
			messages: []*agent.Message{{Assistant: &agent.AssistantMessage{Content: nil}}},
			want:     `[]`,
		},
		{
			name: "consecutive tool results grouped",
			messages: []*agent.Message{
				{ToolResult: &agent.ToolResultMessage{ToolCallID: "c1", Content: []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "a"}}}},
				{ToolResult: &agent.ToolResultMessage{ToolCallID: "c2", IsError: true, Content: []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "boom"}}}},
			},
			want: `[{"role":"user","content":[` +
				`{"type":"tool_result","tool_use_id":"c1","content":"a","is_error":false},` +
				`{"type":"tool_result","tool_use_id":"c2","content":"boom","is_error":true}]}]`,
		},
		{
			name:     "empty conversation is empty array",
			messages: []*agent.Message{},
			want:     `[]`,
		},
		{
			name:  "oauth tool name casing",
			oauth: true,
			messages: []*agent.Message{
				{Assistant: &agent.AssistantMessage{Content: []agent.ContentBlock{
					{Type: agent.BlockTypeToolCall, ID: "c1", Name: "read", Arguments: json.RawMessage(`{}`)},
				}}},
			},
			want: `[{"role":"assistant","content":[{"type":"tool_use","id":"c1","name":"Read","input":{}}]}]`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.Marshal(buildAnthropicMessages(tc.messages, tc.oauth))
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("messages = %s\nwant         %s", got, tc.want)
			}
		})
	}
}

// TestAnthropicBuildSystem verifies the system block construction in both
// auth modes.
func TestAnthropicBuildSystem(t *testing.T) {
	if got := buildAnthropicSystem("", false); got != nil {
		t.Errorf("empty system = %+v, want nil", got)
	}
	got := buildAnthropicSystem("Be helpful", false)
	if len(got) != 1 || got[0].Text != "Be helpful" {
		t.Errorf("system = %+v, want one block", got)
	}
	oauth := buildAnthropicSystem("Be helpful", true)
	if len(oauth) != 2 || oauth[0].Text != claudeCodeSystemPrefix || oauth[1].Text != "Be helpful" {
		t.Errorf("oauth system = %+v, want identity prefix then prompt", oauth)
	}
	if got := buildAnthropicSystem("", true); len(got) != 1 || got[0].Text != claudeCodeSystemPrefix {
		t.Errorf("oauth empty system = %+v, want identity block only", got)
	}
}

// TestAnthropicBuildTools verifies the tool wire conversion, including the
// input_schema shape and the empty case.
func TestAnthropicBuildTools(t *testing.T) {
	if empty := buildAnthropicTools(nil, false); empty != nil {
		t.Errorf("buildAnthropicTools(nil) = %v, want nil", empty)
	}
	tools := buildAnthropicTools([]agent.Tool{
		stubTool{name: "read", desc: "Reads a file", schema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)},
	}, false)
	got, err := json.Marshal(tools)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `[{"name":"read","description":"Reads a file","input_schema":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}}]`
	if string(got) != want {
		t.Errorf("tools = %s\nwant    %s", got, want)
	}

	// A schema without properties/required still yields a valid object
	// schema, and an unparseable schema degrades the same way.
	degraded := buildAnthropicTools([]agent.Tool{
		stubTool{name: "bare", desc: "d", schema: json.RawMessage(`"not an object"`)},
	}, false)
	b, err := json.Marshal(degraded)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"input_schema":{"type":"object","properties":{},"required":[]}`) {
		t.Errorf("degraded schema = %s", b)
	}
}
