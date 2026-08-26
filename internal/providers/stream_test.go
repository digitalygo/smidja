package providers

import (
	"context"
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
		`{"id":"gen_1","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`,
		`{"id":"gen_1","choices":[{"index":0,"delta":{"content":"Hello"}}]}`,
		`{"id":"gen_1","choices":[{"index":0,"delta":{"content":" world"}}]}`,
		`{"id":"gen_1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`{"id":"gen_1","choices":[],"usage":{"prompt_tokens":12,"completion_tokens":7,"total_tokens":19}}`,
		`[DONE]`,
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
	if msg.API != "openai-completions" || msg.Provider != "test-provider" || msg.Model != "test/model" {
		t.Errorf("identity = api %q provider %q model %q", msg.API, msg.Provider, msg.Model)
	}
	if msg.ResponseID != "gen_1" {
		t.Errorf("responseId = %q, want gen_1", msg.ResponseID)
	}
	if msg.Timestamp <= 0 {
		t.Errorf("timestamp = %d, want positive", msg.Timestamp)
	}
	if msg.Usage.Input != 12 || msg.Usage.Output != 7 || msg.Usage.TotalTokens != 19 {
		t.Errorf("usage = %+v", msg.Usage)
	}
	if want := []string{"Hello", " world"}; !equalStrings(textDeltas, want) {
		t.Errorf("text deltas = %v, want %v", textDeltas, want)
	}
	if len(thinkDeltas) != 0 {
		t.Errorf("thinking deltas = %v, want none", thinkDeltas)
	}
}

// TestStreamTurnUsageOnlyFinalChunk exercises the final chunk with an
// empty choices array: it must contribute usage without touching content,
// and cost/detail fields must map into agent.Usage.
func TestStreamTurnUsageOnlyFinalChunk(t *testing.T) {
	events := []string{
		`{"id":"gen_2","choices":[{"index":0,"delta":{"content":"hi there"}}]}`,
		`{"id":"gen_2","choices":[],"usage":{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150,` +
			`"prompt_tokens_details":{"cached_tokens":30},"completion_tokens_details":{"reasoning":20},` +
			`"cost":{"input":0.01,"output":0.02,"total":0.03}}}`,
		`[DONE]`,
	}
	srv, _ := captureServer(t, events...)
	defer srv.Close()

	msg, err := testDriver(t, srv.URL).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}

	if len(msg.Content) != 1 || msg.Content[0].Text != "hi there" {
		t.Errorf("content = %+v, usage chunk must not add blocks", msg.Content)
	}
	u := msg.Usage
	if u.Input != 100 || u.Output != 50 || u.TotalTokens != 150 {
		t.Errorf("usage = %+v, want input 100 output 50 total 150", u)
	}
	if u.CacheRead != 30 {
		t.Errorf("cacheRead = %d, want 30", u.CacheRead)
	}
	if u.Reasoning != 20 {
		t.Errorf("reasoning = %d, want 20", u.Reasoning)
	}
	if u.Cost.Input != 0.01 || u.Cost.Output != 0.02 || u.Cost.Total != 0.03 {
		t.Errorf("cost = %+v, want 0.01/0.02/0.03", u.Cost)
	}
	if msg.StopReason != "stop" {
		t.Errorf("stopReason = %q, want stop default", msg.StopReason)
	}
}

// TestStreamTurnCostVariants is a regression test for the live-stream
// failure where OpenRouter reported usage.cost as a bare number and the
// wire decoder aborted the turn mid-stream. Every cost shape must decode
// without error and map to sane agent.Usage values.
func TestStreamTurnCostVariants(t *testing.T) {
	tests := []struct {
		name string
		cost string
		want agent.Cost
	}{
		{
			name: "object",
			cost: `{"input":0.01,"output":0.02,"cache_read":0.003,"cache_write":0.004,"total":0.037}`,
			want: agent.Cost{Input: 0.01, Output: 0.02, CacheRead: 0.003, CacheWrite: 0.004, Total: 0.037},
		},
		{
			name: "object camelCase cache keys",
			cost: `{"input":0.01,"output":0.02,"cacheRead":0.003,"cacheWrite":0.004,"total":0.037}`,
			want: agent.Cost{Input: 0.01, Output: 0.02, CacheRead: 0.003, CacheWrite: 0.004, Total: 0.037},
		},
		{
			name: "bare number",
			cost: `0.0042`,
			want: agent.Cost{Total: 0.0042},
		},
		{
			name: "null",
			cost: `null`,
			want: agent.Cost{},
		},
		{
			name: "missing",
			cost: "",
			want: agent.Cost{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usage := `{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150`
			if tt.cost != "" {
				usage += `,"cost":` + tt.cost
			}
			usage += `}`
			events := []string{
				`{"id":"gen_cost","choices":[{"index":0,"delta":{"content":"ok"}}]}`,
				`{"id":"gen_cost","choices":[],"usage":` + usage + `}`,
				`[DONE]`,
			}
			srv, _ := captureServer(t, events...)
			defer srv.Close()

			msg, err := testDriver(t, srv.URL).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
			if err != nil {
				t.Fatalf("StreamTurn: %v", err)
			}
			if msg.Usage.Cost != tt.want {
				t.Errorf("cost = %+v, want %+v", msg.Usage.Cost, tt.want)
			}
		})
	}
}

// TestStreamTurnUsageDetailVariants verifies that the optional usage
// breakdowns never abort the stream: absent or null details, alternate key
// spellings, and non-object shapes all decode to sane agent.Usage values.
func TestStreamTurnUsageDetailVariants(t *testing.T) {
	tests := []struct {
		name  string
		usage string
		want  agent.Usage
	}{
		{
			name:  "details absent",
			usage: `{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}`,
			want:  agent.Usage{Input: 10, Output: 5, TotalTokens: 15},
		},
		{
			name:  "details null",
			usage: `{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"prompt_tokens_details":null,"completion_tokens_details":null}`,
			want:  agent.Usage{Input: 10, Output: 5, TotalTokens: 15},
		},
		{
			name:  "reasoning_tokens spelling",
			usage: `{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"completion_tokens_details":{"reasoning_tokens":3}}`,
			want:  agent.Usage{Input: 10, Output: 5, TotalTokens: 15, Reasoning: 3},
		},
		{
			name:  "nested cache breakdown",
			usage: `{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"prompt_tokens_details":{"cache_read":{"cached_tokens":4},"cache_creation":{"cached_tokens":2}}}`,
			want:  agent.Usage{Input: 10, Output: 5, TotalTokens: 15, CacheRead: 4, CacheWrite: 2},
		},
		{
			name:  "non-object details shapes",
			usage: `{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"prompt_tokens_details":[1,2],"completion_tokens_details":"junk"}`,
			want:  agent.Usage{Input: 10, Output: 5, TotalTokens: 15},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := []string{
				`{"id":"gen_usage","choices":[{"index":0,"delta":{"content":"ok"}}]}`,
				`{"id":"gen_usage","choices":[],"usage":` + tt.usage + `}`,
				`[DONE]`,
			}
			srv, _ := captureServer(t, events...)
			defer srv.Close()

			msg, err := testDriver(t, srv.URL).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
			if err != nil {
				t.Fatalf("StreamTurn: %v", err)
			}
			if msg.Usage != tt.want {
				t.Errorf("usage = %+v, want %+v", msg.Usage, tt.want)
			}
		})
	}
}

// TestStreamTurnToolCallSplit feeds one tool call whose arguments arrive
// in fragments and verifies the accumulated raw JSON.
func TestStreamTurnToolCallSplit(t *testing.T) {
	events := []string{
		`{"id":"gen_3","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_abc","type":"function","function":{"name":"read","arguments":""}}]}}]}`,
		`{"id":"gen_3","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":"}}]}}]}`,
		`{"id":"gen_3","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"main.go\"}"}}]}}]}`,
		`{"id":"gen_3","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
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
	if block.Type != agent.BlockTypeToolCall || block.ID != "call_abc" || block.Name != "read" {
		t.Errorf("block = %+v, want toolCall call_abc/read", block)
	}
	if got := string(block.Arguments); got != `{"path":"main.go"}` {
		t.Errorf("arguments = %s, want {\"path\":\"main.go\"}", got)
	}
}

// TestStreamTurnInterleavedToolCalls feeds two tool calls whose fragments
// interleave by index and verifies first-appearance order and per-index
// accumulation.
func TestStreamTurnInterleavedToolCalls(t *testing.T) {
	events := []string{
		`{"id":"gen_4","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_0","type":"function","function":{"name":"read","arguments":"{\"path\":"}}]}}]}`,
		`{"id":"gen_4","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_1","type":"function","function":{"name":"exec","arguments":"{\"cmd\":"}}]}}]}`,
		`{"id":"gen_4","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"a.txt\"}"}}]}}]}`,
		`{"id":"gen_4","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"\"ls\"}"}}]}}]}`,
		`{"id":"gen_4","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	srv, _ := captureServer(t, events...)
	defer srv.Close()

	msg, err := testDriver(t, srv.URL).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}

	if len(msg.Content) != 2 {
		t.Fatalf("content = %+v, want two tool call blocks", msg.Content)
	}
	first, second := msg.Content[0], msg.Content[1]
	if first.Type != agent.BlockTypeToolCall || first.ID != "call_0" || first.Name != "read" ||
		string(first.Arguments) != `{"path":"a.txt"}` {
		t.Errorf("first = %+v, want call_0 read {\"path\":\"a.txt\"}", first)
	}
	if second.Type != agent.BlockTypeToolCall || second.ID != "call_1" || second.Name != "exec" ||
		string(second.Arguments) != `{"cmd":"ls"}` {
		t.Errorf("second = %+v, want call_1 exec {\"cmd\":\"ls\"}", second)
	}
}

// TestStreamTurnReasoning feeds reasoning deltas and verifies they are
// forwarded and accumulated as thinking blocks ahead of the text.
func TestStreamTurnReasoning(t *testing.T) {
	events := []string{
		`{"id":"gen_5","choices":[{"index":0,"delta":{"reasoning":"Let me think"}}]}`,
		`{"id":"gen_5","choices":[{"index":0,"delta":{"reasoning":" step by step"}}]}`,
		`{"id":"gen_5","choices":[{"index":0,"delta":{"content":"Answer"}}]}`,
		`{"id":"gen_5","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}
	srv, _ := captureServer(t, events...)
	defer srv.Close()

	var thinkDeltas []string
	var textDeltas []string
	msg, err := testDriver(t, srv.URL).StreamTurn(context.Background(), baseTurnReq(),
		func(d string) { textDeltas = append(textDeltas, d) },
		func(d string) { thinkDeltas = append(thinkDeltas, d) })
	if err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}

	if want := []string{"Let me think", " step by step"}; !equalStrings(thinkDeltas, want) {
		t.Errorf("thinking deltas = %v, want %v", thinkDeltas, want)
	}
	if len(msg.Content) != 2 {
		t.Fatalf("content = %+v, want thinking then text", msg.Content)
	}
	if msg.Content[0].Type != agent.BlockTypeThinking || msg.Content[0].Thinking != "Let me think step by step" {
		t.Errorf("block[0] = %+v, want thinking block", msg.Content[0])
	}
	if msg.Content[1].Type != agent.BlockTypeText || msg.Content[1].Text != "Answer" {
		t.Errorf("block[1] = %+v, want text block", msg.Content[1])
	}
}

// TestStreamTurnDoneSentinel verifies clean termination on [DONE] alone,
// without any finish_reason chunk.
func TestStreamTurnDoneSentinel(t *testing.T) {
	events := []string{
		`{"id":"gen_6","choices":[{"index":0,"delta":{"content":"done!"}}]}`,
		`[DONE]`,
	}
	srv, _ := captureServer(t, events...)
	defer srv.Close()

	msg, err := testDriver(t, srv.URL).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	if len(msg.Content) != 1 || msg.Content[0].Text != "done!" {
		t.Errorf("content = %+v, want done!", msg.Content)
	}
	if msg.StopReason != "stop" {
		t.Errorf("stopReason = %q, want stop default", msg.StopReason)
	}
}

// TestStreamTurnMissingDoneWithFinishReason verifies that a stream ending
// without [DONE] is accepted when a finish_reason was observed.
func TestStreamTurnMissingDoneWithFinishReason(t *testing.T) {
	events := []string{
		`{"id":"gen_7","choices":[{"index":0,"delta":{"content":"ok"}}]}`,
		`{"id":"gen_7","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	}
	srv, _ := captureServer(t, events...)
	defer srv.Close()

	msg, err := testDriver(t, srv.URL).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	if msg.StopReason != "stop" || len(msg.Content) != 1 || msg.Content[0].Text != "ok" {
		t.Errorf("msg = %+v, want accepted stream", msg)
	}
}

// TestStreamTurnPrematureEOF verifies the error when the stream ends
// without [DONE] and without a finish_reason.
func TestStreamTurnPrematureEOF(t *testing.T) {
	events := []string{
		`{"id":"gen_8","choices":[{"index":0,"delta":{"content":"half"}}]}`,
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
	if !strings.Contains(err.Error(), "premature") {
		t.Errorf("error = %q, want premature EOF", err)
	}
}

// TestStreamTurnEmptyStream treats an immediately closed stream as
// premature EOF.
func TestStreamTurnEmptyStream(t *testing.T) {
	srv, _ := captureServer(t)
	defer srv.Close()

	_, err := testDriver(t, srv.URL).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "premature") {
		t.Errorf("error = %v, want premature EOF", err)
	}
}

// TestStreamTurnSSEErrorEvent verifies that an error envelope arriving as
// an SSE data event after a 200 status aborts the stream.
func TestStreamTurnSSEErrorEvent(t *testing.T) {
	events := []string{
		`{"id":"gen_9","choices":[{"index":0,"delta":{"content":"partial"}}]}`,
		`{"error":{"code":"rate_limited","message":"Rate limit exceeded"}}`,
	}
	srv, _ := captureServer(t, events...)
	defer srv.Close()

	msg, err := testDriver(t, srv.URL).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if msg != nil {
		t.Errorf("msg = %+v, want nil on SSE error", msg)
	}
	if err == nil {
		t.Fatal("want error from SSE event")
	}
	if !strings.Contains(err.Error(), "Rate limit exceeded") {
		t.Errorf("error = %q, want message", err)
	}
	if !strings.Contains(err.Error(), "rate_limited") {
		t.Errorf("error = %q, want code", err)
	}
}

// TestStreamTurnDecodeError verifies a malformed SSE payload aborts the
// stream with a decode error.
func TestStreamTurnDecodeError(t *testing.T) {
	events := []string{
		`not json at all`,
	}
	srv, _ := captureServer(t, events...)
	defer srv.Close()

	_, err := testDriver(t, srv.URL).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if err == nil {
		t.Fatal("want decode error")
	}
	if !strings.Contains(err.Error(), "decode stream chunk") {
		t.Errorf("error = %q, want decode error", err)
	}
}

// TestStreamTurnCancellation verifies that cancelling the context aborts a
// stalled stream and returns context.Canceled.
func TestStreamTurnCancellation(t *testing.T) {
	firstChunk := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"id":"gen_10","choices":[{"index":0,"delta":{"content":"part"}}]}`+"\n\n")
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

// TestStreamTurnNoCapsLargeInterleaved is a regression test for the
// removed per-turn caps: a stream with many interleaved text and thinking
// blocks plus several tool calls whose arguments exceed the old per-call
// cap must complete fully. The old defaults capped a turn at 4 MiB of
// text, 4 MiB of thinking, 1 MiB of arguments per tool call, and 100000
// data events; this stream exceeds all four and every block must survive.
func TestStreamTurnNoCapsLargeInterleaved(t *testing.T) {
	const cycles = 50001 // 100002 data events: over the old 100000-event cap
	textDelta := strings.Repeat("x", 100)
	thinkDelta := strings.Repeat("y", 100)
	bigArgs := strings.Repeat("z", 1200*1024) // 1.2 MiB: over the old 1 MiB per-call cap

	events := make([]string, 0, 2*cycles+4)
	for i := 0; i < cycles; i++ {
		events = append(events, fmt.Sprintf(
			`{"id":"gen_nc","choices":[{"index":0,"delta":{"content":%q}}]}`, textDelta))
		events = append(events, fmt.Sprintf(
			`{"id":"gen_nc","choices":[{"index":0,"delta":{"reasoning":%q}}]}`, thinkDelta))
		if i == 1000 || i == 25000 || i == 40000 {
			idx := map[int]int{1000: 0, 25000: 1, 40000: 2}[i]
			events = append(events, fmt.Sprintf(
				`{"id":"gen_nc","choices":[{"index":0,"delta":{"tool_calls":[{"index":%d,"id":"c%d","type":"function","function":{"name":"read","arguments":%q}}]}}]}`, idx, i, bigArgs))
		}
	}
	events = append(events, `[DONE]`)
	srv, _ := captureServer(t, events...)
	defer srv.Close()

	textDeltas := 0
	thinkDeltas := 0
	msg, err := testDriver(t, srv.URL).StreamTurn(context.Background(), baseTurnReq(),
		func(string) { textDeltas++ },
		func(string) { thinkDeltas++ })
	if err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	if textDeltas != cycles {
		t.Errorf("text deltas = %d, want %d", textDeltas, cycles)
	}
	if thinkDeltas != cycles {
		t.Errorf("thinking deltas = %d, want %d", thinkDeltas, cycles)
	}
	if len(msg.Content) != 2*cycles+3 {
		t.Fatalf("content blocks = %d, want %d", len(msg.Content), 2*cycles+3)
	}
	textBytes, thinkBytes, toolCalls := 0, 0, 0
	for _, b := range msg.Content {
		switch b.Type {
		case agent.BlockTypeText:
			textBytes += len(b.Text)
		case agent.BlockTypeThinking:
			thinkBytes += len(b.Thinking)
		case agent.BlockTypeToolCall:
			toolCalls++
			if len(b.Arguments) != len(bigArgs) {
				t.Errorf("tool call arguments = %d bytes, want %d", len(b.Arguments), len(bigArgs))
			}
		}
	}
	if textBytes != cycles*len(textDelta) {
		t.Errorf("accumulated text = %d bytes, want %d", textBytes, cycles*len(textDelta))
	}
	if thinkBytes != cycles*len(thinkDelta) {
		t.Errorf("accumulated thinking = %d bytes, want %d", thinkBytes, cycles*len(thinkDelta))
	}
	if toolCalls != 3 {
		t.Errorf("tool calls = %d, want 3", toolCalls)
	}
}
