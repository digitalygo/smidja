package oauth

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"
)

const (
	minimumInterval       = time.Second
	defaultPollInterval   = 5 * time.Second
	slowDownIncrement     = 5 * time.Second
	deviceCancelMessage   = "login cancelled"
	deviceTimeoutMessage  = "device flow timed out"
	deviceSlowDownMessage = "device flow timed out after one or more slow_down responses. this is often caused by clock drift in WSL or VM environments. please sync or restart the VM clock and try again."
)

type deviceStatus int

const (
	devicePending deviceStatus = iota
	deviceSlowDown
	deviceComplete
	deviceFailed
)

type devicePollOutcome struct {
	status   deviceStatus
	value    any
	message  string
	interval *float64
}

func outcomePending() devicePollOutcome {
	return devicePollOutcome{status: devicePending}
}

func outcomeSlowDown(intervalSeconds *float64) devicePollOutcome {
	return devicePollOutcome{status: deviceSlowDown, interval: intervalSeconds}
}

func outcomeComplete(value any) devicePollOutcome {
	return devicePollOutcome{status: deviceComplete, value: value}
}

func outcomeFailed(message string) devicePollOutcome {
	return devicePollOutcome{status: deviceFailed, message: message}
}

type deviceFlowConfig struct {
	intervalSeconds     float64
	expiresInSeconds    float64
	waitBeforeFirstPoll bool
	poll                func(context.Context) (devicePollOutcome, error)
}

func pollDeviceCodeFlow(ctx context.Context, cfg deviceFlowConfig) (any, error) {
	interval := intervalFromSeconds(cfg.intervalSeconds)
	var deadline time.Time
	if cfg.expiresInSeconds > 0 {
		deadline = time.Now().Add(time.Duration(cfg.expiresInSeconds * float64(time.Second)))
	}
	slowDownCount := 0
	if cfg.waitBeforeFirstPoll {
		remaining := time.Until(deadline)
		if remaining > 0 {
			if err := sleepContext(ctx, min(interval, remaining)); err != nil {
				return nil, cancelError(err)
			}
		}
	}
	for {
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			break
		}
		if err := ctx.Err(); err != nil {
			return nil, cancelError(err)
		}
		outcome, err := cfg.poll(ctx)
		if err != nil {
			return nil, err
		}
		switch outcome.status {
		case deviceComplete:
			return outcome.value, nil
		case deviceFailed:
			return nil, errors.New(outcome.message)
		case deviceSlowDown:
			slowDownCount++
			if outcome.interval != nil && *outcome.interval > 0 {
				interval = intervalFromSeconds(*outcome.interval)
			} else {
				interval += slowDownIncrement
			}
		}
		if deadline.IsZero() {
			if err := sleepContext(ctx, interval); err != nil {
				return nil, cancelError(err)
			}
			continue
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		if err := sleepContext(ctx, min(interval, remaining)); err != nil {
			return nil, cancelError(err)
		}
	}
	if slowDownCount > 0 {
		return nil, errors.New(deviceSlowDownMessage)
	}
	return nil, errors.New(deviceTimeoutMessage)
}

func intervalFromSeconds(seconds float64) time.Duration {
	if seconds <= 0 {
		return defaultPollInterval
	}
	interval := time.Duration(math.Floor(seconds*1000)) * time.Millisecond
	if interval < minimumInterval {
		return minimumInterval
	}
	return interval
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func cancelError(err error) error {
	return fmt.Errorf("%s: %w", deviceCancelMessage, err)
}
