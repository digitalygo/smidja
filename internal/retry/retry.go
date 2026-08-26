package retry

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
)

type Policy struct {
	Enabled bool

	MaxRetries int

	BaseDelayMs int64
}

func Default() Policy {
	return Policy{Enabled: true, MaxRetries: 10, BaseDelayMs: 2000}
}

type Produce func(ctx context.Context) (*agent.AssistantMessage, error)

var nonRetryablePatterns = []string{
	"GoUsageLimitError",
	"FreeUsageLimitError",
	"Monthly usage limit reached",
	"available balance",
	"insufficient_quota",
	"out of budget",
	"quota exceeded",
	"billing",
}

var retryablePatterns = []string{
	"overloaded",
	"rate.?limit",
	"too many requests",
	"429",
	"500",
	"502",
	"503",
	"504",
	"524",
	"service.?unavailable",
	"server.?error",
	"internal.?error",
	"provider.?returned.?error",
	"exceeded request buffer limit while retrying upstream",
	"network.?error",
	"connection.?error",
	"connection.?refused",
	"connection.?lost",
	"other side closed",
	"fetch failed",
	"getaddrinfo",
	"ENOTFOUND",
	"EAI_AGAIN",
	"upstream.?connect",
	"reset before headers",
	"socket hang up",
	"socket connection was closed",
	"timed? out",
	"timeout",
	"terminated",
	"websocket.?closed",
	"websocket.?error",
	"ended without",
	"stream ended before message_stop",
	"stream ended before a terminal response event",
	"http2 request did not get a response",
	"retry delay",
	"you can retry your request",
	"try your request again",
	"please retry your request",
	"ResourceExhausted",
}

var overflowPatterns = []string{
	"prompt is too long",
	"request_too_large",
	"input is too long for requested model",
	"exceeds the context window",
	`exceeds (?:the )?(?:model'?s )?maximum context length(?: of [\d,]+ tokens?|\s*\([\d,]+\))`,
	"input token count.*exceeds the maximum",
	"maximum prompt length is \\d+",
	"reduce the length of the messages",
	"maximum context length is \\d+ tokens",
	`exceeds (?:the )?maximum allowed input length of [\d,]+ tokens?`,
	`input \(\d+ tokens\) is longer than the model'?s context length \(\d+ tokens\)`,
	"exceeds the limit of \\d+",
	"exceeds the available context size",
	"greater than the context length",
	"context window exceeds limit",
	"exceeded model token limit",
	"too large for model with \\d+ maximum context length",
	`prompt has [\d,]+ tokens?, but the configured context size is [\d,]+ tokens?`,
	"model_context_window_exceeded",
	"prompt too long; exceeded (?:max )?context length",
	"range of input length should be",
	"context[_ ]length[_ ]exceeded",
	"too many tokens",
	"token limit exceeded",
	`^4(?:00|13)\s*(?:status code)?\s*\(no body\)`,
}

var nonOverflowPatterns = []string{
	`^(Throttling error|Service unavailable):`,
	"rate limit",
	"too many requests",
}

var (
	nonRetryableRE = regexp.MustCompile("(?i)" + strings.Join(nonRetryablePatterns, "|"))
	retryableRE    = regexp.MustCompile("(?i)" + strings.Join(retryablePatterns, "|"))
	overflowRE     = regexp.MustCompile("(?i)" + strings.Join(overflowPatterns, "|"))
	nonOverflowRE  = regexp.MustCompile("(?i)" + strings.Join(nonOverflowPatterns, "|"))
)

func Classify(errorMessage string) bool {
	if errorMessage == "" {
		return false
	}
	if nonRetryableRE.MatchString(errorMessage) {
		return false
	}
	return retryableRE.MatchString(errorMessage)
}

func IsContextOverflow(errorMessage string) bool {
	if errorMessage == "" {
		return false
	}
	if nonOverflowRE.MatchString(errorMessage) {
		return false
	}
	return overflowRE.MatchString(errorMessage)
}

type Callbacks interface {
	OnRetryScheduled(attempt, maxAttempts int, delayMs int64, errorMessage string)

	OnRetryAttemptStart()

	OnRetryFinished(success bool, attempt int, finalError string)
}

type CallbacksFunc struct {
	Scheduled func(attempt, maxAttempts int, delayMs int64, errorMessage string)

	AttemptStart func()

	Finished func(success bool, attempt int, finalError string)
}

func (c *CallbacksFunc) OnRetryScheduled(attempt, maxAttempts int, delayMs int64, errorMessage string) {
	if c != nil && c.Scheduled != nil {
		c.Scheduled(attempt, maxAttempts, delayMs, errorMessage)
	}
}

func (c *CallbacksFunc) OnRetryAttemptStart() {
	if c != nil && c.AttemptStart != nil {
		c.AttemptStart()
	}
}

func (c *CallbacksFunc) OnRetryFinished(success bool, attempt int, finalError string) {
	if c != nil && c.Finished != nil {
		c.Finished(success, attempt, finalError)
	}
}

type SleepFunc func(ctx context.Context, d time.Duration) error

type retryOptions struct {
	sleep SleepFunc
}

type Option func(*retryOptions)

func WithSleeper(s SleepFunc) Option {
	return func(o *retryOptions) { o.sleep = s }
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type retryRecord struct {
	attempt      int
	errorMessage string
}

func retryableFailure(attempt, maxAttempts int, errorMessage string) bool {
	if attempt >= maxAttempts {
		return false
	}
	if IsContextOverflow(errorMessage) {
		return false
	}
	return Classify(errorMessage)
}

func Retry(ctx context.Context, produce Produce, policy Policy, callbacks Callbacks, opts ...Option) (*agent.AssistantMessage, error) {
	if produce == nil {
		return nil, errors.New("retry: nil produce")
	}
	o := retryOptions{sleep: sleepCtx}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}

	maxAttempts := 0
	if policy.Enabled {
		maxAttempts = policy.MaxRetries
	}

	attempt := 0
	var lastRetry *retryRecord

	for {
		resp, err := produce(ctx)

		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				if lastRetry != nil {
					emitFinished(callbacks, false, lastRetry.attempt, "")
				}
				return nil, err
			}
		} else if resp == nil {
			return nil, errors.New("retry: produce returned nil message and nil error")
		} else if resp.StopReason == "aborted" {
			if lastRetry != nil {
				emitFinished(callbacks, false, lastRetry.attempt, "")
			}
			return resp, nil
		} else if resp.StopReason != "error" {
			if lastRetry != nil {
				emitFinished(callbacks, true, lastRetry.attempt, "")
			}
			return resp, nil
		}

		var failed *agent.AssistantMessage
		errorMessage := ""
		if err != nil {
			errorMessage = err.Error()
		} else {
			failed = resp
			errorMessage = resp.ErrorMessage
		}

		if !retryableFailure(attempt, maxAttempts, errorMessage) {
			if lastRetry != nil {
				emitFinished(callbacks, false, lastRetry.attempt, errorMessage)
			}
			if failed != nil {
				return failed, nil
			}
			return nil, err
		}

		attempt++
		lastRetry = &retryRecord{attempt: attempt, errorMessage: orUnknown(errorMessage)}
		if aborted, serr := backoff(ctx, failed, lastRetry, maxAttempts, policy, callbacks, o.sleep); serr != nil || aborted != nil {
			return aborted, serr
		}
	}
}

func backoff(ctx context.Context, failed *agent.AssistantMessage, rec *retryRecord, maxAttempts int, policy Policy, callbacks Callbacks, sleep SleepFunc) (*agent.AssistantMessage, error) {
	delayMs := policy.BaseDelayMs * int64(1<<(rec.attempt-1))
	if callbacks != nil {
		callbacks.OnRetryScheduled(rec.attempt, maxAttempts, delayMs, rec.errorMessage)
	}
	if err := sleep(ctx, time.Duration(delayMs)*time.Millisecond); err != nil {
		if callbacks != nil {
			callbacks.OnRetryFinished(false, rec.attempt, rec.errorMessage)
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			if failed != nil {
				aborted := *failed
				aborted.StopReason = "aborted"
				aborted.ErrorMessage = ""
				return &aborted, nil
			}
			return nil, ctxErr
		}
		return nil, err
	}
	if callbacks != nil {
		callbacks.OnRetryAttemptStart()
	}
	return nil, nil
}

func emitFinished(callbacks Callbacks, success bool, attempt int, finalError string) {
	if callbacks != nil {
		callbacks.OnRetryFinished(success, attempt, finalError)
	}
}

func orUnknown(errorMessage string) string {
	if errorMessage == "" {
		return "Unknown error"
	}
	return errorMessage
}
