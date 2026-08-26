package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func runRetries(ctx context.Context, produce func(context.Context) (*AssistantMessage, error), policy RetryPolicy, callbacks *RetryCallbacks) (*AssistantMessage, error) {
	var lastErr error
	for attempt := 1; ; attempt++ {
		msg, err := produce(ctx)
		if err == nil {
			if callbacks != nil && callbacks.Finished != nil {
				callbacks.Finished(true, attempt-1, "")
			}
			return msg, nil
		}
		lastErr = err
		if attempt > policy.MaxRetries {
			if callbacks != nil && callbacks.Finished != nil {
				callbacks.Finished(false, attempt-1, lastErr.Error())
			}
			return nil, lastErr
		}
		if callbacks != nil && callbacks.Scheduled != nil {
			callbacks.Scheduled(attempt, policy.MaxRetries, policy.BaseDelayMs, lastErr.Error())
		}
		if callbacks != nil && callbacks.AttemptStart != nil {
			callbacks.AttemptStart()
		}
	}
}

func TestRunTurnRetryConsumedAndEventsOrdered(t *testing.T) {
	client := &fakeClient{failFirst: 2, script: []*AssistantMessage{textStop("recovered")}}
	rec := &fakeRecorder{}
	hooks := &fakeHooks{}
	var policySeen RetryPolicy
	var scheduled []string
	var finished []string
	_, err := RunTurn(context.Background(), &LoopDeps{
		Client:   client,
		Recorder: rec,
		Hooks:    hooks,
		Retry: func(ctx context.Context, produce func(context.Context) (*AssistantMessage, error), policy RetryPolicy, callbacks *RetryCallbacks) (*AssistantMessage, error) {
			policySeen = policy
			return runRetries(ctx, produce, policy, callbacks)
		},
		OnRetryScheduled: func(attempt, maxAttempts int, delayMs int64, errorMessage string) {
			scheduled = append(scheduled, fmt.Sprintf("%d/%d", attempt, maxAttempts))
		},
		OnRetryFinished: func(success bool, attempt int, finalError string) {
			finished = append(finished, fmt.Sprintf("%v/%d", success, attempt))
		},
	}, "m", "", nil, "hi")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if client.attempts != 3 {
		t.Errorf("client invoked %d times, want 3 (2 failures + 1 success)", client.attempts)
	}
	if policySeen != DefaultRetryPolicy() {
		t.Errorf("retrier saw policy %+v, want DefaultRetryPolicy %+v", policySeen, DefaultRetryPolicy())
	}
	if got := strings.Join(scheduled, ","); got != "1/10,2/10" {
		t.Errorf("OnRetryScheduled = %q, want 1/10,2/10", got)
	}
	if got := strings.Join(finished, ","); got != "true/2" {
		t.Errorf("OnRetryFinished = %q, want true/2", got)
	}
	if got := hooks.joinEvents(); got != "context,auto_retry_start,auto_retry_start,auto_retry_end,message_end" {
		t.Errorf("hook events = %q, want context,auto_retry_start,auto_retry_start,auto_retry_end,message_end", got)
	}
	if got := rec.joinEvents(); got != "user,assistant" {
		t.Errorf("recorder events = %q, want user,assistant", got)
	}
}

func TestRunTurnContextOverflowSentinelWithoutRetry(t *testing.T) {
	classify := func(s string) bool { return strings.Contains(s, "too long") }

	client := &fakeClient{err: errors.New("provider: prompt is too long for requested model")}
	history, err := RunTurn(context.Background(), &LoopDeps{
		Client:            client,
		IsContextOverflow: classify,
	}, "m", "", nil, "hi")
	if !errors.Is(err, ErrContextOverflow) {
		t.Fatalf("err = %v, want ErrContextOverflow", err)
	}
	var oe *ContextOverflowError
	if !errors.As(err, &oe) || oe.Assistant != nil {
		t.Errorf("overflow error = %+v, want typed error with nil assistant for a transport failure", err)
	}
	if client.attempts != 1 {
		t.Errorf("client invoked %d times, want 1 (no retries consumed)", client.attempts)
	}
	if len(history) != 1 || history[0].User == nil {
		t.Errorf("history = %+v, want just the recorded user message", history)
	}

	client2 := &fakeClient{script: []*AssistantMessage{{
		Role:         string(RoleAssistant),
		StopReason:   "error",
		ErrorMessage: "input is too long for requested model",
		Timestamp:    1,
	}}}
	rec := &fakeRecorder{}
	history2, err := RunTurn(context.Background(), &LoopDeps{
		Client:            client2,
		Recorder:          rec,
		IsContextOverflow: classify,
	}, "m", "", nil, "hi")
	if !errors.Is(err, ErrContextOverflow) {
		t.Fatalf("err = %v, want ErrContextOverflow", err)
	}
	if !errors.As(err, &oe) || oe.Assistant == nil || oe.Assistant.ErrorMessage != "input is too long for requested model" {
		t.Errorf("overflow error = %+v, want typed error wrapping the failed assistant message", err)
	}
	if len(history2) != 2 || history2[1].Assistant == nil || history2[1].Assistant.StopReason != "error" {
		t.Errorf("history = %+v, want the failed assistant message recorded", history2)
	}
	if got := rec.joinEvents(); got != "user,assistant" {
		t.Errorf("recorder events = %q, want user,assistant", got)
	}
}
