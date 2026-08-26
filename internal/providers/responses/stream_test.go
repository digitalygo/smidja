package responses

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
)

func TestStreamTurnText(t *testing.T) {
	events := []string{
		`{"type":"response.created","response":{"id":"resp_1"}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"","annotations":[]}],"status":"completed"}}`,
		`{"type":"response.output_text.delta","output_index":0,"delta":"Hello"}`,
		`{"type":"response.output_text.delta","output_index":0,"delta":" world"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"Hello world","annotations":[]}],"status":"completed"}}`,
		`{"type":"response.completed","response":{"id":"resp_1","status":"completed","usage":{"input_tokens":20,"output_tokens":7,"total_tokens":27,"input_tokens_details":{"cached_tokens":4,"cache_write_tokens":2},"output_tokens_details":{"reasoning_tokens":3}},"output":[]}}`,
	}
	srv, _ := captureServer(t, events...)
	defer srv.Close()

	var textDeltas []string
	var thinkDeltas []string
	msg, err := testDriver(t, srv.URL).StreamTurn(context.Background(), baseTurnReq(),
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
	if msg.API != "openai-responses" || msg.Provider != "openai" || msg.Model != "gpt-5" {
		t.Errorf("identity = api %q provider %q model %q", msg.API, msg.Provider, msg.Model)
	}
	if msg.ResponseID != "resp_1" {
		t.Errorf("responseId = %q, want resp_1", msg.ResponseID)
	}
	u := msg.Usage
	if u.Input != 14 || u.Output != 7 || u.TotalTokens != 27 {
		t.Errorf("usage = %+v, want input 14 = 20-4-2, output 7, total 27", u)
	}
	if u.CacheRead != 4 || u.CacheWrite != 2 {
		t.Errorf("usage cache = %+v, want read 4 write 2", u)
	}
	if u.Reasoning != 3 {
		t.Errorf("usage reasoning = %d, want 3", u.Reasoning)
	}
	if want := []string{"Hello", " world"}; !equalStrings(textDeltas, want) {
		t.Errorf("text deltas = %v, want %v", textDeltas, want)
	}
	if len(thinkDeltas) != 0 {
		t.Errorf("thinking deltas = %v, want none", thinkDeltas)
	}
}

func TestStreamTurnFunctionCall(t *testing.T) {
	events := []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_abc","call_id":"call_1","name":"read","arguments":""}}`,
		`{"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"path\":"}`,
		`{"type":"response.function_call_arguments.delta","output_index":0,"delta":"\"main.go\"}"}`,
		`{"type":"response.function_call_arguments.done","output_index":0,"arguments":"{\"path\":\"main.go\"}"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_abc","call_id":"call_1","name":"read","arguments":"{\"path\":\"main.go\"}"}}`,
		`{"type":"response.completed","response":{"id":"resp_2","status":"completed","usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12},"output":[]}}`,
	}
	srv, _ := captureServer(t, events...)
	defer srv.Close()

	msg, err := testDriver(t, srv.URL).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
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
	if block.Type != agent.BlockTypeToolCall || block.ID != "call_1|fc_abc" || block.Name != "read" {
		t.Errorf("block = %+v, want toolCall call_1|fc_abc/read", block)
	}
	if got := string(block.Arguments); got != `{"path":"main.go"}` {
		t.Errorf("arguments = %s, want {\"path\":\"main.go\"}", got)
	}
}

func TestStreamTurnFunctionCallDoneFirstOrder(t *testing.T) {
	events := []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_xyz","call_id":"call_2","name":"exec","arguments":""}}`,
		`{"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"cmd\":\"ls\"}"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_xyz","call_id":"call_2","name":"exec","arguments":"{\"cmd\":\"ls\"}"}}`,
		`{"type":"response.function_call_arguments.done","output_index":0,"arguments":"{\"cmd\":\"ls\"}"}`,
		`{"type":"response.completed","response":{"id":"resp_3","status":"completed","output":[]}}`,
	}
	srv, _ := captureServer(t, events...)
	defer srv.Close()

	msg, err := testDriver(t, srv.URL).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	if len(msg.Content) != 1 || string(msg.Content[0].Arguments) != `{"cmd":"ls"}` {
		t.Errorf("content = %+v, want accumulated arguments", msg.Content)
	}
}

func TestStreamTurnReasoningSummary(t *testing.T) {
	reasoningItem := `{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"Thinking out loud"}],"content":[{"type":"reasoning_text","text":"raw","annotations":[]}],"encrypted_content":"enc_1"}`
	events := []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"Thinking out loud"}],"encrypted_content":"enc_1"}}`,
		`{"type":"response.reasoning_summary_text.delta","output_index":0,"delta":"Thinking"}`,
		`{"type":"response.reasoning_summary_text.delta","output_index":0,"delta":" out loud"}`,
		`{"type":"response.reasoning_summary_part.done","output_index":0}`,
		`{"type":"response.output_item.done","output_index":0,"item":` + reasoningItem + `}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"","annotations":[]}],"status":"completed"}}`,
		`{"type":"response.output_text.delta","output_index":1,"delta":"Answer"}`,
		`{"type":"response.output_item.done","output_index":1,"item":{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"Answer","annotations":[]}],"status":"completed"}}`,
		`{"type":"response.completed","response":{"id":"resp_4","status":"completed","output":[]}}`,
	}
	srv, _ := captureServer(t, events...)
	defer srv.Close()

	var thinkDeltas []string
	msg, err := testDriver(t, srv.URL).StreamTurn(context.Background(), baseTurnReq(),
		nil,
		func(d string) { thinkDeltas = append(thinkDeltas, d) })
	if err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}

	if want := []string{"Thinking", " out loud", "\n\n"}; !equalStrings(thinkDeltas, want) {
		t.Errorf("thinking deltas = %v, want %v", thinkDeltas, want)
	}
	if len(msg.Content) != 2 {
		t.Fatalf("content = %+v, want thinking then text", msg.Content)
	}
	if msg.Content[0].Type != agent.BlockTypeThinking {
		t.Fatalf("block[0] = %+v, want thinking block", msg.Content[0])
	}
	if msg.Content[0].Thinking != "Thinking out loud" {
		t.Errorf("block[0].thinking = %q, want summary text", msg.Content[0].Thinking)
	}
	var persisted map[string]any
	if err := json.Unmarshal([]byte(msg.Content[0].ThinkingSignature), &persisted); err != nil {
		t.Fatalf("signature is not the persisted item: %v", err)
	}
	if persisted["type"] != "reasoning" || persisted["id"] != "rs_1" {
		t.Errorf("persisted item = %v, want reasoning rs_1", persisted)
	}
	if msg.Content[1].Type != agent.BlockTypeText || msg.Content[1].Text != "Answer" {
		t.Errorf("block[1] = %+v, want text block", msg.Content[1])
	}
}

func TestStreamTurnReasoningContentFallback(t *testing.T) {
	events := []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_2","content":[{"type":"reasoning_text","text":"raw reasoning","annotations":[]}],"encrypted_content":"enc_2"}}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_2","content":[{"type":"reasoning_text","text":"raw reasoning","annotations":[]}],"encrypted_content":"enc_2"}}`,
		`{"type":"response.completed","response":{"id":"r","status":"completed","output":[]}}`,
	}
	srv, _ := captureServer(t, events...)
	defer srv.Close()

	msg, err := testDriver(t, srv.URL).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	if len(msg.Content) != 1 || msg.Content[0].Thinking != "raw reasoning" {
		t.Errorf("content = %+v, want content fallback", msg.Content)
	}
}

func TestStreamTurnFailed(t *testing.T) {
	events := []string{
		`{"type":"response.failed","response":{"id":"resp_9","status":"failed","error":{"code":"server_error","message":"The server had an error"}}}`,
	}
	srv, _ := captureServer(t, events...)
	defer srv.Close()

	msg, err := testDriver(t, srv.URL).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if msg != nil {
		t.Errorf("msg = %+v, want nil on failed", msg)
	}
	if err == nil {
		t.Fatal("want error from response.failed")
	}
	if !strings.Contains(err.Error(), "server_error") || !strings.Contains(err.Error(), "The server had an error") {
		t.Errorf("error = %q, want code and message", err)
	}
}

func TestStreamTurnErrorEvent(t *testing.T) {
	events := []string{
		`{"type":"response.output_text.delta","output_index":0,"delta":"partial"}`,
		`{"type":"error","code":"rate_limit_exceeded","message":"Rate limit reached"}`,
	}
	srv, _ := captureServer(t, events...)
	defer srv.Close()

	msg, err := testDriver(t, srv.URL).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if msg != nil {
		t.Errorf("msg = %+v, want nil on error event", msg)
	}
	if err == nil {
		t.Fatal("want error from error event")
	}
	if !strings.Contains(err.Error(), "rate_limit_exceeded") || !strings.Contains(err.Error(), "Rate limit reached") {
		t.Errorf("error = %q, want code and message", err)
	}
}

func TestStreamTurnIncompleteLength(t *testing.T) {
	events := []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"m","role":"assistant","content":[{"type":"output_text","text":"","annotations":[]}],"status":"completed"}}`,
		`{"type":"response.output_text.delta","output_index":0,"delta":"trunc"}`,
		`{"type":"response.incomplete","response":{"id":"r","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":5,"output_tokens":4,"total_tokens":9},"output":[]}}`,
	}
	srv, _ := captureServer(t, events...)
	defer srv.Close()

	msg, err := testDriver(t, srv.URL).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	if msg.StopReason != "length" {
		t.Errorf("stopReason = %q, want length", msg.StopReason)
	}
}

func TestStreamTurnIncompleteError(t *testing.T) {
	events := []string{
		`{"type":"response.incomplete","response":{"id":"r","status":"incomplete","incomplete_details":{"reason":"content_filter"},"output":[]}}`,
	}
	srv, _ := captureServer(t, events...)
	defer srv.Close()

	msg, err := testDriver(t, srv.URL).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if msg != nil {
		t.Errorf("msg = %+v, want nil on incomplete error", msg)
	}
	if err == nil || !strings.Contains(err.Error(), "content_filter") {
		t.Errorf("error = %v, want incomplete reason", err)
	}
}

func TestStreamTurnUsageLastWins(t *testing.T) {
	events := []string{
		`{"type":"response.completed","response":{"id":"r","status":"completed","usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12},"output":[]}}`,
		`{"type":"response.completed","response":{"id":"r","status":"completed","usage":{"input_tokens":30,"output_tokens":6,"total_tokens":36,"input_tokens_details":{"cached_tokens":5}},"output":[]}}`,
	}
	srv, _ := captureServer(t, events...)
	defer srv.Close()

	msg, err := testDriver(t, srv.URL).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	if msg.Usage.Input != 25 || msg.Usage.Output != 6 || msg.Usage.CacheRead != 5 {
		t.Errorf("usage = %+v, want last terminal wins", msg.Usage)
	}
}

func TestStreamTurnPrematureEOF(t *testing.T) {
	events := []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"m","role":"assistant","content":[{"type":"output_text","text":"half","annotations":[]}],"status":"completed"}}`,
	}
	srv, _ := captureServer(t, events...)
	defer srv.Close()

	msg, err := testDriver(t, srv.URL).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if msg != nil {
		t.Errorf("msg = %+v, want nil on premature EOF", msg)
	}
	if err == nil {
		t.Fatal("want error on premature EOF")
	}
	if !strings.Contains(err.Error(), "terminal response event") {
		t.Errorf("error = %q, want terminal event error", err)
	}
}

func TestStreamTurnMalformedSSE(t *testing.T) {
	events := []string{
		`not json at all`,
	}
	srv, _ := captureServer(t, events...)
	defer srv.Close()

	_, err := testDriver(t, srv.URL).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if err == nil {
		t.Fatal("want decode error")
	}
	if !strings.Contains(err.Error(), "decode stream event") {
		t.Errorf("error = %q, want decode error", err)
	}
}

func TestStreamTurnDoneSentinel(t *testing.T) {
	events := []string{
		`[DONE]`,
		`{"type":"response.completed","response":{"id":"r","status":"completed","output":[]}}`,
	}
	srv, _ := captureServer(t, events...)
	defer srv.Close()

	msg, err := testDriver(t, srv.URL).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	if msg == nil || msg.StopReason != "stop" {
		t.Errorf("msg = %+v, want completed turn", msg)
	}
}

func TestStreamTurnCancellation(t *testing.T) {
	firstChunk := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"type":"response.output_text.delta","output_index":0,"delta":"part"}`+"\n\n")
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
		m, err := testDriver(t, srv.URL).StreamTurn(ctx, baseTurnReq(), nil, nil)
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

func TestStreamTurnBackfillReasoningSignatures(t *testing.T) {
	events := []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_9","summary":[{"type":"summary_text","text":"t"}]}}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_9","summary":[{"type":"summary_text","text":"t"}]}}`,
		`{"type":"response.completed","response":{"id":"r","status":"completed","output":[{"type":"reasoning","id":"rs_9","encrypted_content":"enc_backfilled"}]}}`,
	}
	srv, _ := captureServer(t, events...)
	defer srv.Close()

	msg, err := testDriver(t, srv.URL).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	var persisted map[string]any
	if err := json.Unmarshal([]byte(msg.Content[0].ThinkingSignature), &persisted); err != nil {
		t.Fatalf("signature: %v", err)
	}
	if persisted["encrypted_content"] != "enc_backfilled" {
		t.Errorf("persisted = %v, want backfilled encrypted_content", persisted)
	}
}

func TestStreamTurnRefusalDelta(t *testing.T) {
	events := []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"m","role":"assistant","content":[{"type":"refusal","refusal":""}],"status":"completed"}}`,
		`{"type":"response.refusal.delta","output_index":0,"delta":"I cannot do that"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"m","role":"assistant","content":[{"type":"refusal","refusal":"I cannot do that"}],"status":"completed"}}`,
		`{"type":"response.completed","response":{"id":"r","status":"completed","output":[]}}`,
	}
	srv, _ := captureServer(t, events...)
	defer srv.Close()

	var textDeltas []string
	msg, err := testDriver(t, srv.URL).StreamTurn(context.Background(), baseTurnReq(),
		func(d string) { textDeltas = append(textDeltas, d) }, nil)
	if err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	if len(msg.Content) != 1 || msg.Content[0].Text != "I cannot do that" {
		t.Errorf("content = %+v, want refusal text", msg.Content)
	}
	if want := []string{"I cannot do that"}; !equalStrings(textDeltas, want) {
		t.Errorf("text deltas = %v, want %v", textDeltas, want)
	}
}

func TestStreamTurnUnknownEventsIgnored(t *testing.T) {
	events := []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"m","role":"assistant","content":[{"type":"output_text","text":"","annotations":[]}],"status":"completed"}}`,
		`{"type":"response.output_text.delta","output_index":0,"delta":"ok"}`,
		`{"type":"response.some_future_event","output_index":0,"delta":"future"}`,
		`{"type":"response.completed","response":{"id":"r","status":"completed","output":[]}}`,
	}
	srv, _ := captureServer(t, events...)
	defer srv.Close()

	msg, err := testDriver(t, srv.URL).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	if len(msg.Content) != 1 || msg.Content[0].Text != "ok" {
		t.Errorf("content = %+v, want unaffected text", msg.Content)
	}
}
