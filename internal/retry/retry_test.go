package retry

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
)

func errMsg(text string) *agent.AssistantMessage {
	return &agent.AssistantMessage{Role: string(agent.RoleAssistant), StopReason: "error", ErrorMessage: text}
}

func okMsg() *agent.AssistantMessage {
	return &agent.AssistantMessage{Role: string(agent.RoleAssistant), StopReason: "stop"}
}

func abortedMsg() *agent.AssistantMessage {
	return &agent.AssistantMessage{Role: string(agent.RoleAssistant), StopReason: "aborted"}
}

type scheduledEvent struct {
	attempt      int
	maxAttempts  int
	delayMs      int64
	errorMessage string
}

type finishedEvent struct {
	success    bool
	attempt    int
	finalError string
}

type recordingCallbacks struct {
	scheduled     []scheduledEvent
	attemptStarts int
	finished      []finishedEvent
}

func (c *recordingCallbacks) OnRetryScheduled(attempt, maxAttempts int, delayMs int64, errorMessage string) {
	c.scheduled = append(c.scheduled, scheduledEvent{attempt, maxAttempts, delayMs, errorMessage})
}

func (c *recordingCallbacks) OnRetryAttemptStart() { c.attemptStarts++ }

func (c *recordingCallbacks) OnRetryFinished(success bool, attempt int, finalError string) {
	c.finished = append(c.finished, finishedEvent{success, attempt, finalError})
}

func immediateSleeper(record *[]time.Duration) SleepFunc {
	return func(_ context.Context, d time.Duration) error {
		*record = append(*record, d)
		return nil
	}
}

func cancellingSleeper(cancel context.CancelFunc) SleepFunc {
	return func(ctx context.Context, _ time.Duration) error {
		cancel()
		<-ctx.Done()
		return ctx.Err()
	}
}

func TestClassifyPatterns(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		want bool
	}{
		{"go usage limit", "GoUsageLimitError: monthly limit reached", false},
		{"free usage limit", "FreeUsageLimitError", false},
		{"monthly usage limit", "Monthly usage limit reached for your plan", false},
		{"available balance", "enable available balance usage", false},
		{"insufficient quota", "insufficient_quota: you exceeded your current quota", false},
		{"out of budget", "request out of budget", false},
		{"quota exceeded", "quota exceeded", false},
		{"billing", "billing issue on your account", false},
		{"billing beats overloaded", "billing overloaded endpoint", false},
		{"quota beats 429", "quota exceeded (429)", false},
		{"overloaded", "model is overloaded", true},
		{"rate limit spaced", "rate limit hit", true},
		{"ratelimit", "ratelimit", true},
		{"rate-limit", "rate-limit", true},
		{"too many requests", "too many requests", true},
		{"429", "429 Too Many Requests", true},
		{"500", "500 Internal Server Error", true},
		{"502", "502 Bad Gateway", true},
		{"503", "503 Service Unavailable", true},
		{"504", "504 Gateway Timeout", true},
		{"524", "524", true},
		{"service unavailable", "service unavailable", true},
		{"server error", "internal server error", true},
		{"internal error", "internal error", true},
		{"provider returned error", "Provider returned error: 500 from upstream", true},
		{"buffer limit", "exceeded request buffer limit while retrying upstream", true},
		{"network error", "network error", true},
		{"connection error", "connection error", true},
		{"connection refused", "connection refused", true},
		{"connection lost", "connection lost", true},
		{"other side closed", "other side closed", true},
		{"fetch failed", "fetch failed", true},
		{"getaddrinfo", "getaddrinfo ENOTFOUND api.openai.com", true},
		{"ENOTFOUND", "ENOTFOUND", true},
		{"EAI_AGAIN", "EAI_AGAIN", true},
		{"upstream connect", "upstream connect error", true},
		{"reset before headers", "reset before headers", true},
		{"socket hang up", "socket hang up", true},
		{"socket connection was closed", "socket connection was closed", true},
		{"timed out", "request timed out", true},
		{"time out", "time out", true},
		{"timeout", "timeout", true},
		{"terminated", "stream terminated", true},
		{"websocket closed", "websocket closed", true},
		{"websocket error", "websocket error", true},
		{"ended without", "stream ended without a message", true},
		{"stream ended before message_stop", "Anthropic stream ended before message_stop", true},
		{"terminal response", "stream ended before a terminal response event", true},
		{"http2 no response", "http2 request did not get a response", true},
		{"retry delay", "retry delay requested by provider", true},
		{"you can retry", "you can retry your request", true},
		{"try again", "try your request again", true},
		{"please retry", "please retry your request", true},
		{"ResourceExhausted", "ResourceExhausted: grpc status", true},
		{"case retryable", "RATE LIMIT", true},
		{"case retryable mixed", "Overloaded", true},
		{"case non-retryable", "BILLING", false},
		{"empty", "", false},
		{"random text", "some unrelated error", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Classify(tt.msg); got != tt.want {
				t.Errorf("Classify(%q) = %v, want %v", tt.msg, got, tt.want)
			}
		})
	}
}

func TestIsContextOverflow(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		want bool
	}{
		{"anthropic prompt too long", "prompt is too long: 213462 tokens > 200000 maximum", true},
		{"anthropic request too large", `413 {"error":{"type":"request_too_large"}}`, true},
		{"bedrock input too long", "input is too long for requested model", true},
		{"openai context window", "Your input exceeds the context window of this model", true},
		{"openai max context length", "Requested token count exceeds the model's maximum context length of 131072 tokens", true},
		{"openai parenth context length", "Input length (265330) exceeds model's maximum context length (262144).", true},
		{"google input token count", "The input token count (1196265) exceeds the maximum number of tokens allowed", true},
		{"xai max prompt length", "This model's maximum prompt length is 131072 but the request contains 537812 tokens", true},
		{"openrouter max context", "This endpoint's maximum context length is 128000 tokens. However, you requested about 131072 tokens", true},
		{"poolside input length", "Input length 300000 exceeds the maximum allowed input length of 200000 tokens.", true},
		{"llama.cpp context size", "the request exceeds the available context size, try increasing it", true},
		{"lm studio context length", "tokens to keep from the initial prompt is greater than the context length", true},
		{"cerebras no body", "400 status code (no body)", true},
		{"generic too many tokens", "too many tokens", true},
		{"generic token limit", "token limit exceeded", true},
		{"bedrock throttling", "Throttling error: Too many tokens, please wait before trying again", false},
		{"bedrock service unavailable", "Service unavailable: too many tokens", false},
		{"rate limit", "rate limit: too many tokens", false},
		{"too many requests", "too many requests", false},
		{"empty", "", false},
		{"unrelated", "model crashed", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsContextOverflow(tt.msg); got != tt.want {
				t.Errorf("IsContextOverflow(%q) = %v, want %v", tt.msg, got, tt.want)
			}
		})
	}
}

func TestRetrySuccessNoRetry(t *testing.T) {
	calls := 0
	produce := func(ctx context.Context) (*agent.AssistantMessage, error) {
		calls++
		return okMsg(), nil
	}
	cbs := &recordingCallbacks{}
	got, err := Retry(context.Background(), produce, Default(), cbs, WithSleeper(immediateSleeper(&[]time.Duration{})))
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if got.StopReason != "stop" {
		t.Errorf("StopReason = %q, want %q", got.StopReason, "stop")
	}
	if calls != 1 {
		t.Errorf("produce calls = %d, want 1", calls)
	}
	if len(cbs.finished) != 0 || len(cbs.scheduled) != 0 || cbs.attemptStarts != 0 {
		t.Errorf("callbacks emitted without any retry: %+v", cbs)
	}
}

func TestRetryDisabledPolicy(t *testing.T) {
	calls := 0
	produce := func(ctx context.Context) (*agent.AssistantMessage, error) {
		calls++
		return errMsg("overloaded"), nil
	}
	var delays []time.Duration
	cbs := &recordingCallbacks{}
	got, err := Retry(context.Background(), produce, Policy{}, cbs, WithSleeper(immediateSleeper(&delays)))
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if got.ErrorMessage != "overloaded" {
		t.Errorf("ErrorMessage = %q, want %q", got.ErrorMessage, "overloaded")
	}
	if calls != 1 {
		t.Errorf("produce calls = %d, want 1", calls)
	}
	if len(delays) != 0 {
		t.Errorf("backoff delays = %v, want none", delays)
	}
	if len(cbs.finished) != 0 {
		t.Errorf("OnRetryFinished emitted without any retry: %+v", cbs.finished)
	}
}

func TestRetryAbortedMessageFirst(t *testing.T) {
	calls := 0
	produce := func(ctx context.Context) (*agent.AssistantMessage, error) {
		calls++
		return abortedMsg(), nil
	}
	cbs := &recordingCallbacks{}
	got, err := Retry(context.Background(), produce, Default(), cbs, WithSleeper(immediateSleeper(&[]time.Duration{})))
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if got.StopReason != "aborted" {
		t.Errorf("StopReason = %q, want %q", got.StopReason, "aborted")
	}
	if calls != 1 {
		t.Errorf("produce calls = %d, want 1", calls)
	}
	if len(cbs.finished) != 0 {
		t.Errorf("callbacks emitted for an abort before any retry: %+v", cbs.finished)
	}
}

func TestRetryAbortedContextFirst(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	produce := func(ctx context.Context) (*agent.AssistantMessage, error) {
		calls++
		return nil, ctx.Err()
	}
	got, err := Retry(ctx, produce, Default(), nil, WithSleeper(immediateSleeper(&[]time.Duration{})))
	if err == nil {
		t.Fatal("Retry returned nil error, want context cancellation")
	}
	if got != nil {
		t.Errorf("got message %+v, want nil", got)
	}
	if calls != 1 {
		t.Errorf("produce calls = %d, want 1", calls)
	}
}

func TestRetryNonRetryableFirst(t *testing.T) {
	calls := 0
	produce := func(ctx context.Context) (*agent.AssistantMessage, error) {
		calls++
		return errMsg("quota exceeded"), nil
	}
	var delays []time.Duration
	cbs := &recordingCallbacks{}
	got, err := Retry(context.Background(), produce, Default(), cbs, WithSleeper(immediateSleeper(&delays)))
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if got.ErrorMessage != "quota exceeded" {
		t.Errorf("ErrorMessage = %q, want %q", got.ErrorMessage, "quota exceeded")
	}
	if calls != 1 {
		t.Errorf("produce calls = %d, want 1", calls)
	}
	if len(delays) != 0 || len(cbs.scheduled) != 0 {
		t.Errorf("non-retryable error scheduled retries: delays=%v scheduled=%+v", delays, cbs.scheduled)
	}
}

func TestRetryContextOverflowFirst(t *testing.T) {
	calls := 0
	produce := func(ctx context.Context) (*agent.AssistantMessage, error) {
		calls++
		return errMsg("prompt is too long: 213462 tokens > 200000 maximum"), nil
	}
	var delays []time.Duration
	got, err := Retry(context.Background(), produce, Default(), nil, WithSleeper(immediateSleeper(&delays)))
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if got.ErrorMessage != "prompt is too long: 213462 tokens > 200000 maximum" {
		t.Errorf("unexpected message: %+v", got)
	}
	if calls != 1 {
		t.Errorf("produce calls = %d, want 1 (overflow is never retried)", calls)
	}
	if len(delays) != 0 {
		t.Errorf("overflow error scheduled retries: %v", delays)
	}
}

func TestRetryBackoffSequence(t *testing.T) {
	calls := 0
	produce := func(ctx context.Context) (*agent.AssistantMessage, error) {
		calls++
		if calls <= 3 {
			return errMsg("overloaded"), nil
		}
		return okMsg(), nil
	}
	policy := Default()
	policy.MaxRetries = 3
	var delays []time.Duration
	cbs := &recordingCallbacks{}
	got, err := Retry(context.Background(), produce, policy, cbs, WithSleeper(immediateSleeper(&delays)))
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if got.StopReason != "stop" {
		t.Errorf("StopReason = %q, want %q", got.StopReason, "stop")
	}
	if calls != 4 {
		t.Errorf("produce calls = %d, want 4 (initial + 3 retries)", calls)
	}
	wantDelays := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second}
	if !reflect.DeepEqual(delays, wantDelays) {
		t.Errorf("delays = %v, want %v", delays, wantDelays)
	}
	wantScheduled := []scheduledEvent{
		{attempt: 1, maxAttempts: 3, delayMs: 2000, errorMessage: "overloaded"},
		{attempt: 2, maxAttempts: 3, delayMs: 4000, errorMessage: "overloaded"},
		{attempt: 3, maxAttempts: 3, delayMs: 8000, errorMessage: "overloaded"},
	}
	if !reflect.DeepEqual(cbs.scheduled, wantScheduled) {
		t.Errorf("scheduled = %+v, want %+v", cbs.scheduled, wantScheduled)
	}
	if cbs.attemptStarts != 3 {
		t.Errorf("attempt starts = %d, want 3", cbs.attemptStarts)
	}
	wantFinished := []finishedEvent{{success: true, attempt: 3, finalError: ""}}
	if !reflect.DeepEqual(cbs.finished, wantFinished) {
		t.Errorf("finished = %+v, want %+v", cbs.finished, wantFinished)
	}
}

func TestRetryBudgetExhaustionAtMaxRetries(t *testing.T) {
	calls := 0
	produce := func(ctx context.Context) (*agent.AssistantMessage, error) {
		calls++
		return errMsg("overloaded"), nil
	}
	policy := Default()
	var delays []time.Duration
	cbs := &recordingCallbacks{}
	got, err := Retry(context.Background(), produce, policy, cbs, WithSleeper(immediateSleeper(&delays)))
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if got.StopReason != "error" || got.ErrorMessage != "overloaded" {
		t.Errorf("got %+v, want the final error message", got)
	}
	if calls != 11 {
		t.Errorf("produce calls = %d, want 11 (initial + 10 retries)", calls)
	}
	if len(delays) != 10 {
		t.Fatalf("backoff delays = %d, want 10", len(delays))
	}
	if delays[0] != 2*time.Second {
		t.Errorf("first delay = %v, want 2s", delays[0])
	}
	if delays[9] != 1024*time.Second {
		t.Errorf("last delay = %v, want 1024s (2000ms * 2^9)", delays[9])
	}
	wantFinished := []finishedEvent{{success: false, attempt: 10, finalError: "overloaded"}}
	if !reflect.DeepEqual(cbs.finished, wantFinished) {
		t.Errorf("finished = %+v, want %+v", cbs.finished, wantFinished)
	}
}

func TestRetryTransportErrorExhaustion(t *testing.T) {
	produce := func(ctx context.Context) (*agent.AssistantMessage, error) {
		return nil, errors.New("connection refused")
	}
	policy := Default()
	policy.MaxRetries = 2
	var delays []time.Duration
	cbs := &recordingCallbacks{}
	got, err := Retry(context.Background(), produce, policy, cbs, WithSleeper(immediateSleeper(&delays)))
	if err == nil {
		t.Fatal("Retry returned nil error, want the transport error")
	}
	if got != nil {
		t.Errorf("got message %+v, want nil", got)
	}
	if len(delays) != 2 {
		t.Errorf("backoff delays = %d, want 2", len(delays))
	}
	wantFinished := []finishedEvent{{success: false, attempt: 2, finalError: "connection refused"}}
	if !reflect.DeepEqual(cbs.finished, wantFinished) {
		t.Errorf("finished = %+v, want %+v", cbs.finished, wantFinished)
	}
}

func TestRetryAbortDuringBackoffMessage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	produce := func(ctx context.Context) (*agent.AssistantMessage, error) {
		return errMsg("503 service unavailable"), nil
	}
	cbs := &recordingCallbacks{}
	got, err := Retry(ctx, produce, Default(), cbs, WithSleeper(cancellingSleeper(cancel)))
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if got == nil || got.StopReason != "aborted" {
		t.Fatalf("got %+v, want an aborted message", got)
	}
	if got.ErrorMessage != "" {
		t.Errorf("ErrorMessage = %q, want cleared on abort normalization", got.ErrorMessage)
	}
	wantFinished := []finishedEvent{{success: false, attempt: 1, finalError: "503 service unavailable"}}
	if !reflect.DeepEqual(cbs.finished, wantFinished) {
		t.Errorf("finished = %+v, want %+v", cbs.finished, wantFinished)
	}
}

func TestRetryAbortDuringBackoffTransport(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	produce := func(ctx context.Context) (*agent.AssistantMessage, error) {
		return nil, errors.New("connection refused")
	}
	cbs := &recordingCallbacks{}
	got, err := Retry(ctx, produce, Default(), cbs, WithSleeper(cancellingSleeper(cancel)))
	if err == nil {
		t.Fatal("Retry returned nil error, want the context error")
	}
	if got != nil {
		t.Errorf("got message %+v, want nil", got)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	wantFinished := []finishedEvent{{success: false, attempt: 1, finalError: "connection refused"}}
	if !reflect.DeepEqual(cbs.finished, wantFinished) {
		t.Errorf("finished = %+v, want %+v", cbs.finished, wantFinished)
	}
}

func TestRetryAbortedMessageAfterRetry(t *testing.T) {
	calls := 0
	produce := func(ctx context.Context) (*agent.AssistantMessage, error) {
		calls++
		if calls == 1 {
			return errMsg("overloaded"), nil
		}
		return abortedMsg(), nil
	}
	var delays []time.Duration
	cbs := &recordingCallbacks{}
	got, err := Retry(context.Background(), produce, Default(), cbs, WithSleeper(immediateSleeper(&delays)))
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if got.StopReason != "aborted" {
		t.Errorf("StopReason = %q, want %q", got.StopReason, "aborted")
	}
	if calls != 2 {
		t.Errorf("produce calls = %d, want 2", calls)
	}
	wantFinished := []finishedEvent{{success: false, attempt: 1, finalError: ""}}
	if !reflect.DeepEqual(cbs.finished, wantFinished) {
		t.Errorf("finished = %+v, want %+v", cbs.finished, wantFinished)
	}
}

func TestRetryOverflowAfterRetry(t *testing.T) {
	calls := 0
	produce := func(ctx context.Context) (*agent.AssistantMessage, error) {
		calls++
		if calls == 1 {
			return errMsg("overloaded"), nil
		}
		return errMsg("prompt is too long: 213462 tokens > 200000 maximum"), nil
	}
	var delays []time.Duration
	cbs := &recordingCallbacks{}
	got, err := Retry(context.Background(), produce, Default(), cbs, WithSleeper(immediateSleeper(&delays)))
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if got.ErrorMessage != "prompt is too long: 213462 tokens > 200000 maximum" {
		t.Errorf("got %+v, want the overflow message returned as terminal", got)
	}
	if len(delays) != 1 {
		t.Errorf("backoff delays = %v, want 1 (overflow stops the loop)", delays)
	}
	wantFinished := []finishedEvent{{success: false, attempt: 1, finalError: "prompt is too long: 213462 tokens > 200000 maximum"}}
	if !reflect.DeepEqual(cbs.finished, wantFinished) {
		t.Errorf("finished = %+v, want %+v", cbs.finished, wantFinished)
	}
}

func TestRetryNonRetryableAfterRetry(t *testing.T) {
	calls := 0
	produce := func(ctx context.Context) (*agent.AssistantMessage, error) {
		calls++
		if calls == 1 {
			return errMsg("overloaded"), nil
		}
		return errMsg("billing issue on your account"), nil
	}
	var delays []time.Duration
	cbs := &recordingCallbacks{}
	got, err := Retry(context.Background(), produce, Default(), cbs, WithSleeper(immediateSleeper(&delays)))
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if got.ErrorMessage != "billing issue on your account" {
		t.Errorf("got %+v, want the billing message returned as terminal", got)
	}
	wantFinished := []finishedEvent{{success: false, attempt: 1, finalError: "billing issue on your account"}}
	if !reflect.DeepEqual(cbs.finished, wantFinished) {
		t.Errorf("finished = %+v, want %+v", cbs.finished, wantFinished)
	}
}

func TestRetryContextCancelAtProduce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	produce := func(ctx context.Context) (*agent.AssistantMessage, error) {
		calls++
		if calls <= 2 {
			return nil, errors.New("connection refused")
		}
		cancel()
		return nil, context.Canceled
	}
	var delays []time.Duration
	cbs := &recordingCallbacks{}
	got, err := Retry(ctx, produce, Default(), cbs, WithSleeper(immediateSleeper(&delays)))
	if err == nil {
		t.Fatal("Retry returned nil error, want context cancellation")
	}
	if got != nil {
		t.Errorf("got message %+v, want nil", got)
	}
	if len(delays) != 2 {
		t.Errorf("backoff delays = %v, want 2 before the abort", delays)
	}
	wantFinished := []finishedEvent{{success: false, attempt: 2, finalError: ""}}
	if !reflect.DeepEqual(cbs.finished, wantFinished) {
		t.Errorf("finished = %+v, want %+v", cbs.finished, wantFinished)
	}
}

func TestRetryDefaultSleepTimer(t *testing.T) {
	calls := 0
	produce := func(ctx context.Context) (*agent.AssistantMessage, error) {
		calls++
		if calls == 1 {
			return errMsg("overloaded"), nil
		}
		return okMsg(), nil
	}
	policy := Default()
	policy.MaxRetries = 1
	policy.BaseDelayMs = 1
	got, err := Retry(context.Background(), produce, policy, nil)
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if got.StopReason != "stop" {
		t.Errorf("StopReason = %q, want %q", got.StopReason, "stop")
	}
	if calls != 2 {
		t.Errorf("produce calls = %d, want 2", calls)
	}
}

func TestRetryDefaultSleepCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	produce := func(ctx context.Context) (*agent.AssistantMessage, error) {
		return errMsg("overloaded"), nil
	}
	policy := Default()
	policy.MaxRetries = 3
	got, err := Retry(ctx, produce, policy, nil)
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if got == nil || got.StopReason != "aborted" {
		t.Fatalf("got %+v, want an aborted message", got)
	}
	if got.ErrorMessage != "" {
		t.Errorf("ErrorMessage = %q, want cleared on abort normalization", got.ErrorMessage)
	}
}

func TestRetryNilProduce(t *testing.T) {
	if _, err := Retry(context.Background(), nil, Default(), nil); err == nil {
		t.Fatal("Retry with nil produce returned nil error")
	}
}

func TestRetryUnknownErrorMessageFallback(t *testing.T) {
	produce := func(ctx context.Context) (*agent.AssistantMessage, error) {
		return nil, errors.New("overloaded")
	}
	policy := Default()
	policy.MaxRetries = 1
	cbs := &recordingCallbacks{}
	_, _ = Retry(context.Background(), produce, policy, cbs, WithSleeper(immediateSleeper(&[]time.Duration{})))
	if len(cbs.scheduled) != 1 || cbs.scheduled[0].errorMessage != "overloaded" {
		t.Errorf("scheduled = %+v, want errorMessage %q", cbs.scheduled, "overloaded")
	}
}
