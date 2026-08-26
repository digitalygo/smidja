package oauth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPollDeviceFlowPendingThenComplete(t *testing.T) {
	polls := 0
	start := time.Now()
	value, err := pollDeviceCodeFlow(context.Background(), deviceFlowConfig{
		intervalSeconds:  1,
		expiresInSeconds: 10,
		poll: func(ctx context.Context) (devicePollOutcome, error) {
			polls++
			if polls < 3 {
				return outcomePending(), nil
			}
			return outcomeComplete("done"), nil
		},
	})
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if value != "done" {
		t.Errorf("value = %v", value)
	}
	if polls != 3 {
		t.Errorf("polls = %d, want 3", polls)
	}
	if elapsed := time.Since(start); elapsed < 1900*time.Millisecond {
		t.Errorf("polled too fast: %v", elapsed)
	}
}

func TestPollDeviceFlowFailed(t *testing.T) {
	_, err := pollDeviceCodeFlow(context.Background(), deviceFlowConfig{
		intervalSeconds:  1,
		expiresInSeconds: 10,
		poll: func(ctx context.Context) (devicePollOutcome, error) {
			return outcomeFailed("nope"), nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("err = %v", err)
	}
}

func TestPollDeviceFlowTimeout(t *testing.T) {
	_, err := pollDeviceCodeFlow(context.Background(), deviceFlowConfig{
		intervalSeconds:  1,
		expiresInSeconds: 0.05,
		poll: func(ctx context.Context) (devicePollOutcome, error) {
			return outcomePending(), nil
		},
	})
	if err == nil || err.Error() != deviceTimeoutMessage {
		t.Errorf("err = %v, want %q", err, deviceTimeoutMessage)
	}
}

func TestPollDeviceFlowSlowDownServerInterval(t *testing.T) {
	pollTimes := []time.Duration{}
	start := time.Now()
	_, err := pollDeviceCodeFlow(context.Background(), deviceFlowConfig{
		intervalSeconds:  1,
		expiresInSeconds: 3.5,
		poll: func(ctx context.Context) (devicePollOutcome, error) {
			pollTimes = append(pollTimes, time.Since(start))
			if len(pollTimes) == 1 {
				interval := 2.0
				return outcomeSlowDown(&interval), nil
			}
			return outcomePending(), nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "slow_down") {
		t.Fatalf("err = %v, want slow_down timeout", err)
	}
	if len(pollTimes) != 2 {
		t.Fatalf("polls = %d, want 2", len(pollTimes))
	}
	if gap := pollTimes[1] - pollTimes[0]; gap < 1800*time.Millisecond || gap > 3*time.Second {
		t.Errorf("slow_down interval gap = %v, want ~2s (server-provided)", gap)
	}
}

func TestPollDeviceFlowSlowDownIncrement(t *testing.T) {
	_, err := pollDeviceCodeFlow(context.Background(), deviceFlowConfig{
		intervalSeconds:  1,
		expiresInSeconds: 2,
		poll: func(ctx context.Context) (devicePollOutcome, error) {
			return outcomeSlowDown(nil), nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "slow_down") {
		t.Errorf("err = %v, want slow_down timeout message", err)
	}
}

func TestPollDeviceFlowCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	_, err := pollDeviceCodeFlow(ctx, deviceFlowConfig{
		intervalSeconds:  1,
		expiresInSeconds: 10,
		poll: func(ctx context.Context) (devicePollOutcome, error) {
			return outcomePending(), nil
		},
	})
	if err == nil {
		t.Fatal("want cancel error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if !strings.Contains(err.Error(), deviceCancelMessage) {
		t.Errorf("err = %v, want cancel message", err)
	}
}

func TestPollDeviceFlowWaitBeforeFirstPoll(t *testing.T) {
	start := time.Now()
	polls := 0
	value, err := pollDeviceCodeFlow(context.Background(), deviceFlowConfig{
		intervalSeconds:     1,
		expiresInSeconds:    10,
		waitBeforeFirstPoll: true,
		poll: func(ctx context.Context) (devicePollOutcome, error) {
			polls++
			return outcomeComplete("v"), nil
		},
	})
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if value != "v" || polls != 1 {
		t.Errorf("value = %v polls = %d", value, polls)
	}
	if elapsed := time.Since(start); elapsed < 900*time.Millisecond {
		t.Errorf("first poll happened too early: %v", elapsed)
	}
}

func TestIntervalFromSeconds(t *testing.T) {
	cases := []struct {
		seconds float64
		want    time.Duration
	}{
		{0, 5 * time.Second},
		{2, 2 * time.Second},
		{0.5, time.Second},
		{-1, 5 * time.Second},
	}
	for _, tc := range cases {
		if got := intervalFromSeconds(tc.seconds); got != tc.want {
			t.Errorf("intervalFromSeconds(%v) = %v, want %v", tc.seconds, got, tc.want)
		}
	}
}
