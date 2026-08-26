package gemini

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
)

// TestStreamTurnText feeds a text turn and verifies blocks, deltas,
// response id, and usage mapping.
func TestStreamTurnText(t *testing.T) {
	events := []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"Hello"}]}}],"responseId":"resp_1"}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":" world"}]}}]}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":""}]},"finishReason":"STOP"}]}`,
		`{"usageMetadata":{"promptTokenCount":12,"candidatesTokenCount":7,"totalTokenCount":19}}`,
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
	if msg.API != "google-generative-ai" || msg.Provider != "gemini" || msg.Model != "gemini-2.5-pro" {
		t.Errorf("identity = api %q provider %q model %q", msg.API, msg.Provider, msg.Model)
	}
	if msg.ResponseID != "resp_1" {
		t.Errorf("responseId = %q, want resp_1", msg.ResponseID)
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

// TestStreamTurnUsageLastWins verifies that cumulative usageMetadata
// chunks overwrite, never sum: the last chunk is authoritative.
func TestStreamTurnUsageLastWins(t *testing.T) {
	events := []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"a"}]}}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15}}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"b"}]}}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":50,"totalTokenCount":150,"cachedContentTokenCount":30,"thoughtsTokenCount":20}}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":""}]},"finishReason":"STOP"}]}`,
	}
	srv, _ := captureServer(t, events...)
	defer srv.Close()

	msg, err := testDriver(t, srv.URL).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	u := msg.Usage
	if u.Input != 70 || u.Output != 70 || u.TotalTokens != 150 {
		t.Errorf("usage = %+v, want last chunk wins (input 70 = 100-30, output 70 = 50+20, total 150)", u)
	}
	if u.CacheRead != 30 {
		t.Errorf("cacheRead = %d, want 30 from cachedContentTokenCount", u.CacheRead)
	}
	if u.Reasoning != 20 {
		t.Errorf("reasoning = %d, want 20 from thoughtsTokenCount", u.Reasoning)
	}
}

// TestStreamTurnThinking feeds thinking parts with a thought signature
// and verifies the thinking block, signature retention across deltas, and
// callback forwarding.
func TestStreamTurnThinking(t *testing.T) {
	events := []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"Let me think","thought":true,"thoughtSignature":"c2lnX2ZpcnN0"}]}}]}`,
		// Second delta omits the signature; the retained one must win.
		`{"candidates":[{"content":{"role":"model","parts":[{"text":" step by step","thought":true}]}}]}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"Answer"}]}}]}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":""}]},"finishReason":"STOP"}]}`,
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
	if msg.Content[0].ThinkingSignature != "c2lnX2ZpcnN0" {
		t.Errorf("block[0] signature = %q, want retained signature", msg.Content[0].ThinkingSignature)
	}
	if msg.Content[1].Type != agent.BlockTypeText || msg.Content[1].Text != "Answer" {
		t.Errorf("block[1] = %+v, want text block", msg.Content[1])
	}
	if want := []string{"Answer"}; !equalStrings(textDeltas, want) {
		t.Errorf("text deltas = %v, want %v", textDeltas, want)
	}
}

// TestStreamTurnThinkingSignatureReplacement verifies that a later
// non-empty signature replaces an earlier one (last non-empty wins).
func TestStreamTurnThinkingSignatureReplacement(t *testing.T) {
	events := []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"a","thought":true,"thoughtSignature":"c2lnXzE"}]}}]}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"b","thought":true,"thoughtSignature":"c2lnXzI"}]}}]}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":""}]},"finishReason":"STOP"}]}`,
	}
	srv, _ := captureServer(t, events...)
	defer srv.Close()

	msg, err := testDriver(t, srv.URL).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	if msg.Content[0].ThinkingSignature != "c2lnXzI" {
		t.Errorf("signature = %q, want the later signature", msg.Content[0].ThinkingSignature)
	}
}

// synthesizedIDRE matches the pi-ai synthesized tool call id shape
// name_timestamp_counter.
var synthesizedIDRE = regexp.MustCompile(`^read_\d+_\d+$`)

// TestStreamTurnFunctionCallSynthesizedIDs verifies that function calls
// without a reliable id get stable, unique synthesized ids of the pi-ai
// shape, and that a duplicated provider id is resynthesized.
func TestStreamTurnFunctionCallSynthesizedIDs(t *testing.T) {
	events := []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"read","args":{"path":"a.go"}}}]}}]}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"read","args":{"path":"b.go"}}}]}}]}`,
		// The provider repeats the first id: it must be resynthesized.
		`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"read","id":"dup","args":{"path":"c.go"}}},{"functionCall":{"name":"read","id":"dup","args":{"path":"d.go"}}}]}}]}`,
		`{"candidates":[{"content":{"role":"model","parts":[]},"finishReason":"STOP"}]}`,
	}
	srv, _ := captureServer(t, events...)
	defer srv.Close()

	msg, err := testDriver(t, srv.URL).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	if msg.StopReason != "toolUse" {
		t.Errorf("stopReason = %q, want toolUse because tool calls were produced", msg.StopReason)
	}
	if len(msg.Content) != 4 {
		t.Fatalf("content = %+v, want four tool call blocks", msg.Content)
	}
	ids := make(map[string]bool, len(msg.Content))
	for i, b := range msg.Content {
		if b.Type != agent.BlockTypeToolCall || b.Name != "read" {
			t.Fatalf("block[%d] = %+v, want toolCall read", i, b)
		}
		if ids[b.ID] {
			t.Errorf("block[%d] id %q duplicated", i, b.ID)
		}
		ids[b.ID] = true
		if i < 2 {
			if !synthesizedIDRE.MatchString(b.ID) {
				t.Errorf("block[%d] id = %q, want synthesized shape name_ts_counter", i, b.ID)
			}
		}
	}
	if msg.Content[0].ID == msg.Content[1].ID {
		t.Error("two synthesized ids are equal")
	}
	// The first "dup" id is kept, the second is resynthesized.
	if msg.Content[2].ID != "dup" {
		t.Errorf("block[2] id = %q, want provided id kept", msg.Content[2].ID)
	}
	if msg.Content[3].ID == "dup" || !synthesizedIDRE.MatchString(msg.Content[3].ID) {
		t.Errorf("block[3] id = %q, want resynthesized id", msg.Content[3].ID)
	}
	if string(msg.Content[0].Arguments) != `{"path":"a.go"}` {
		t.Errorf("block[0] arguments = %s", msg.Content[0].Arguments)
	}
}

// TestStreamTurnFunctionCallProvidedID verifies that a provided id is
// preserved verbatim.
func TestStreamTurnFunctionCallProvidedID(t *testing.T) {
	events := []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"read","id":"provided_1","args":{"path":"a.go"}}}]}}]}`,
		`{"candidates":[{"content":{"role":"model","parts":[]},"finishReason":"STOP"}]}`,
	}
	srv, _ := captureServer(t, events...)
	defer srv.Close()

	msg, err := testDriver(t, srv.URL).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	if len(msg.Content) != 1 || msg.Content[0].ID != "provided_1" {
		t.Errorf("content = %+v, want provided id kept", msg.Content)
	}
}

// TestStreamTurnNoCandidatesChunk verifies that a chunk with no
// candidates (or candidates without parts) is skipped without aborting
// the stream.
func TestStreamTurnNoCandidatesChunk(t *testing.T) {
	events := []string{
		`{"candidates":[]}`,
		`{"candidates":[{"content":null}]}`,
		`{"candidates":[{"content":{"role":"model","parts":[]}}]}`,
		`{"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":3,"totalTokenCount":8}}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`,
	}
	srv, _ := captureServer(t, events...)
	defer srv.Close()

	msg, err := testDriver(t, srv.URL).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	if len(msg.Content) != 1 || msg.Content[0].Text != "ok" {
		t.Errorf("content = %+v, want one text block", msg.Content)
	}
	if msg.Usage.Input != 5 || msg.Usage.Output != 3 {
		t.Errorf("usage = %+v, want usage from skipped-candidate chunk applied", msg.Usage)
	}
}

// TestStreamTurnBlockReason verifies that promptFeedback.blockReason
// surfaces as an error result.
func TestStreamTurnBlockReason(t *testing.T) {
	events := []string{
		`{"promptFeedback":{"blockReason":"SAFETY","blockReasonMessage":"The prompt was blocked for safety reasons."}}`,
	}
	srv, _ := captureServer(t, events...)
	defer srv.Close()

	msg, err := testDriver(t, srv.URL).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if msg != nil {
		t.Errorf("msg = %+v, want nil on prompt block", msg)
	}
	if err == nil {
		t.Fatal("want error on prompt block")
	}
	if !strings.Contains(err.Error(), "prompt blocked") || !strings.Contains(err.Error(), "SAFETY") {
		t.Errorf("error = %q, want block reason surfaced", err)
	}
	if !strings.Contains(err.Error(), "blocked for safety reasons") {
		t.Errorf("error = %q, want block message surfaced", err)
	}
}

// TestStreamTurnFinishReasonError verifies that a safety or other
// non-STOP finish reason aborts the turn like pi-ai does.
func TestStreamTurnFinishReasonError(t *testing.T) {
	events := []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"partial"}]},"finishReason":"SAFETY"}]}`,
	}
	srv, _ := captureServer(t, events...)
	defer srv.Close()

	msg, err := testDriver(t, srv.URL).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if msg != nil {
		t.Errorf("msg = %+v, want nil on error finish reason", msg)
	}
	if err == nil {
		t.Fatal("want error from SAFETY finish reason")
	}
	if !strings.Contains(err.Error(), "SAFETY") {
		t.Errorf("error = %q, want finish reason surfaced", err)
	}
}

// TestStreamTurnMaxTokensLength verifies MAX_TOKENS maps to the length
// stop reason, not an error.
func TestStreamTurnMaxTokensLength(t *testing.T) {
	events := []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"truncated"}]},"finishReason":"MAX_TOKENS"}]}`,
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

// TestStreamTurnPrematureEOF verifies the error when the stream ends
// without a finish reason.
func TestStreamTurnPrematureEOF(t *testing.T) {
	events := []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"half"}]}}]}`,
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
	if !strings.Contains(err.Error(), "without a finish reason") {
		t.Errorf("error = %q, want missing finish reason", err)
	}
}

// TestStreamTurnDoneSentinel verifies the [DONE] sentinel ends the stream
// and the finish checks still apply.
func TestStreamTurnDoneSentinel(t *testing.T) {
	events := []string{
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"done!"}]},"finishReason":"STOP"}]}`,
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
}

// TestStreamTurnEmptyStream treats an immediately closed stream as
// premature EOF.
func TestStreamTurnEmptyStream(t *testing.T) {
	srv, _ := captureServer(t)
	defer srv.Close()

	_, err := testDriver(t, srv.URL).StreamTurn(context.Background(), baseTurnReq(), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "without a finish reason") {
		t.Errorf("error = %v, want missing finish reason", err)
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

// TestStreamTurnCancellation verifies that cancelling the context aborts
// a stalled stream and returns context.Canceled.
func TestStreamTurnCancellation(t *testing.T) {
	firstChunk := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"part"}]}}]}`+"\n\n")
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
