// Package retry implements smidja's bounded retry policy for assistant
// turns, a faithful Go port of Pi's retry semantics
// (utils/retry.js): the error classifier (isRetryableAssistantError) and
// the exponential-backoff wrapper (retryAssistantCall), with smidja's
// default budget.
//
// The classifier and the backoff loop are the two halves of Pi's retry
// semantics: isRetryableAssistantError decides which failed messages are
// worth retrying, and retryAssistantCall runs the bounded exponential
// backoff around a single assistant-producing call. Context-overflow
// errors are never retried here: compaction handles them later, so Retry
// returns them as terminal failures for the loop to compact.
//
// The one structural difference from Pi is the transport seam. Pi encodes
// every failure as an AssistantMessage with stopReason "error", while the
// smidja provider client returns Go errors for transport failures. Retry
// accepts a produce closure that returns either shape and treats a
// non-nil error like a failed message whose error message is the error
// text; context cancellation maps to Pi's "aborted" stop reason.
package retry

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
)

// Policy is the retry budget of one assistant-producing call, mirroring
// Pi's RetryPolicy: bounded attempts with exponential backoff
// (baseDelayMs * 2^(attempt-1)) before jitter.
type Policy struct {
	// Enabled turns retry on. When false, Retry returns the first
	// response unchanged, equivalent to calling produce directly.
	Enabled bool

	// MaxRetries is the maximum number of retry attempts after the
	// initial call. 0 disables retries. Default 10.
	MaxRetries int

	// BaseDelayMs is the first backoff delay in milliseconds; attempt n
	// waits baseDelayMs * 2^(n-1). Default 2000.
	BaseDelayMs int64
}

// Default returns the smidja default policy: enabled, 10 retries, 2000ms
// base delay. MaxRetries deliberately overrides Pi's default of 3: smidja
// chooses a larger transient-failure budget and caps the total backoff at
// roughly 34 minutes (2s + 4s + ... + 1024s).
func Default() Policy {
	return Policy{Enabled: true, MaxRetries: 10, BaseDelayMs: 2000}
}

// Produce performs one assistant turn. It returns the completed message
// on success, or a non-nil error when the call failed before producing a
// message (transport failures, context cancellation). A provider-reported
// failure is returned as a message with StopReason "error" and
// ErrorMessage set, mirroring Pi's AssistantMessage shape.
type Produce func(ctx context.Context) (*agent.AssistantMessage, error)

// Non-retryable provider error patterns: subscription/account limits and
// billing exhaustion. Ported verbatim from Pi's
// NON_RETRYABLE_PROVIDER_LIMIT_ERROR_PATTERN. These are deterministic
// failures, never transient, so they win over any retryable pattern.
var nonRetryablePatterns = []string{
	// OpenCode Go/free-tier limits returned as 429 JSON error types:
	// subscription/account limits, not transient throttles.
	"GoUsageLimitError",
	"FreeUsageLimitError",
	// OpenCode Go subscription-limit text asks users to enable
	// available-balance usage after rolling/weekly/monthly limits.
	"Monthly usage limit reached",
	"available balance",
	// Generic quota/budget/billing exhaustion.
	"insufficient_quota",
	"out of budget",
	"quota exceeded",
	"billing",
}

// Retryable provider error patterns: provider load, HTTP status codes,
// server-side transients, transport failures, and explicit retry
// guidance. Ported verbatim from Pi's RETRYABLE_PROVIDER_ERROR_PATTERN.
var retryablePatterns = []string{
	// Generic provider load, HTTP status, and server-side transient
	// failures.
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
	// Wrapper/provider text for transient upstream failures, including
	// OpenRouter "Provider returned error" responses.
	"provider.?returned.?error",
	"exceeded request buffer limit while retrying upstream",
	// Network, proxy, and fetch transport failures.
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
	// WebSocket transports can report close/error text instead of
	// HTTP/fetch text.
	"websocket.?closed",
	"websocket.?error",
	// Premature stream endings from SDKs and transports.
	"ended without",
	"stream ended before message_stop",
	"stream ended before a terminal response event",
	"http2 request did not get a response",
	// Provider-requested retry delay cap failures flow through the
	// outer retry policy so callers can surface/abort the backoff.
	"retry delay",
	// Explicit retry guidance emitted mid-stream by OpenAI Responses
	// and Bedrock stream exceptions.
	"you can retry your request",
	"try your request again",
	"please retry your request",
	// gRPC based providers (e.g. NVIDIA NIM).
	"ResourceExhausted",
}

// Context-overflow error patterns, ported from Pi's isContextOverflow
// error-message path (utils/overflow.js). Overflow is handled by
// compaction, never by retry.
var overflowPatterns = []string{
	"prompt is too long",                    // Anthropic
	"request_too_large",                     // Anthropic HTTP 413
	"input is too long for requested model", // Amazon Bedrock
	"exceeds the context window",            // OpenAI
	`exceeds (?:the )?(?:model'?s )?maximum context length(?: of [\d,]+ tokens?|\s*\([\d,]+\))`, // OpenAI-compatible proxies
	"input token count.*exceeds the maximum",                                                    // Google
	"maximum prompt length is \\d+",                                                             // xAI
	"reduce the length of the messages",                                                         // Groq
	"maximum context length is \\d+ tokens",                                                     // OpenRouter
	`exceeds (?:the )?maximum allowed input length of [\d,]+ tokens?`,                           // OpenRouter/Poolside
	`input \(\d+ tokens\) is longer than the model'?s context length \(\d+ tokens\)`,            // Together AI
	"exceeds the limit of \\d+",                                                                 // GitHub Copilot
	"exceeds the available context size",                                                        // llama.cpp
	"greater than the context length",                                                           // LM Studio
	"context window exceeds limit",                                                              // MiniMax
	"exceeded model token limit",                                                                // Kimi
	"too large for model with \\d+ maximum context length",                                      // Mistral
	`prompt has [\d,]+ tokens?, but the configured context size is [\d,]+ tokens?`,              // DS4
	"model_context_window_exceeded",                                                             // z.ai
	"prompt too long; exceeded (?:max )?context length",                                         // Ollama
	"range of input length should be",                                                           // DashScope/Qwen
	"context[_ ]length[_ ]exceeded",                                                             // Generic fallback
	"too many tokens",                                                                           // Generic fallback
	"token limit exceeded",                                                                      // Generic fallback
	`^4(?:00|13)\s*(?:status code)?\s*\(no body\)`,                                              // Cerebras
}

// Non-overflow patterns that exclude overflow detection even when an
// overflow pattern also matches, ported from Pi's NON_OVERFLOW_PATTERNS.
// For example Bedrock formats throttling as "Throttling error: Too many
// tokens", which must stay retryable.
var nonOverflowPatterns = []string{
	`^(Throttling error|Service unavailable):`, // AWS Bedrock
	"rate limit",        // Generic rate limiting
	"too many requests", // Generic HTTP 429 style
}

var (
	nonRetryableRE = regexp.MustCompile("(?i)" + strings.Join(nonRetryablePatterns, "|"))
	retryableRE    = regexp.MustCompile("(?i)" + strings.Join(retryablePatterns, "|"))
	overflowRE     = regexp.MustCompile("(?i)" + strings.Join(overflowPatterns, "|"))
	nonOverflowRE  = regexp.MustCompile("(?i)" + strings.Join(nonOverflowPatterns, "|"))
)

// Classify reports whether an error message looks like a transient
// provider or transport error, mirroring Pi's isRetryableAssistantError
// classification on the message text alone. Non-retryable patterns win
// over retryable ones, exactly as in Pi: a message that mentions billing
// is never retried even when it also matches a retryable pattern.
// Matching is case-insensitive.
//
// Classify does not implement retry policy, and it does not cover context
// overflow: check IsContextOverflow first (as Retry does) and handle
// overflow separately, exactly like Pi keeps overflow.js apart from
// retry.js.
func Classify(errorMessage string) bool {
	if errorMessage == "" {
		return false
	}
	if nonRetryableRE.MatchString(errorMessage) {
		return false
	}
	return retryableRE.MatchString(errorMessage)
}

// IsContextOverflow reports whether an error message is a context-overflow
// marker, ported from Pi's isContextOverflow error-pattern path. Overflow
// errors are never retried by Retry: compaction handles them later. The
// silent-overflow (usage-based) and length-stop cases from Pi need the
// host's context-window knowledge and are out of scope here.
func IsContextOverflow(errorMessage string) bool {
	if errorMessage == "" {
		return false
	}
	if nonOverflowRE.MatchString(errorMessage) {
		return false
	}
	return overflowRE.MatchString(errorMessage)
}

// Callbacks reports the retry lifecycle events, mirroring Pi's
// RetryCallbacks. Methods are invoked synchronously from Retry in call
// order. Implement only the events you need; CallbacksFunc provides a
// partial functional implementation. Pass nil to Retry to disable all
// callbacks.
type Callbacks interface {
	// OnRetryScheduled is called before the backoff sleep of each retry
	// attempt (attempt is 1-indexed).
	OnRetryScheduled(attempt, maxAttempts int, delayMs int64, errorMessage string)

	// OnRetryAttemptStart is called after the backoff sleep, immediately
	// before the retried call starts.
	OnRetryAttemptStart()

	// OnRetryFinished is called once when the loop ends after at least
	// one retry: success if the last call completed normally, otherwise
	// the final error message (empty when success is true).
	OnRetryFinished(success bool, attempt int, finalError string)
}

// CallbacksFunc is a functional Callbacks implementation: leave any field
// nil to skip that event.
type CallbacksFunc struct {
	// Scheduled maps to OnRetryScheduled.
	Scheduled func(attempt, maxAttempts int, delayMs int64, errorMessage string)

	// AttemptStart maps to OnRetryAttemptStart.
	AttemptStart func()

	// Finished maps to OnRetryFinished.
	Finished func(success bool, attempt int, finalError string)
}

// OnRetryScheduled implements Callbacks.
func (c *CallbacksFunc) OnRetryScheduled(attempt, maxAttempts int, delayMs int64, errorMessage string) {
	if c != nil && c.Scheduled != nil {
		c.Scheduled(attempt, maxAttempts, delayMs, errorMessage)
	}
}

// OnRetryAttemptStart implements Callbacks.
func (c *CallbacksFunc) OnRetryAttemptStart() {
	if c != nil && c.AttemptStart != nil {
		c.AttemptStart()
	}
}

// OnRetryFinished implements Callbacks.
func (c *CallbacksFunc) OnRetryFinished(success bool, attempt int, finalError string) {
	if c != nil && c.Finished != nil {
		c.Finished(success, attempt, finalError)
	}
}

// SleepFunc sleeps for the given duration, returning nil when the full
// delay elapsed and ctx.Err() when the context is cancelled first.
type SleepFunc func(ctx context.Context, d time.Duration) error

// retryOptions carries the per-call Retry options.
type retryOptions struct {
	sleep SleepFunc
}

// Option customizes one Retry call.
type Option func(*retryOptions)

// WithSleeper replaces the default context-aware timer sleep. Tests use
// it to observe the backoff sequence without real timers.
func WithSleeper(s SleepFunc) Option {
	return func(o *retryOptions) { o.sleep = s }
}

// sleepCtx is the default backoff sleep: a cancellable timer.
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

// retryRecord tracks one scheduled retry, mirroring Pi's lastRetry.
type retryRecord struct {
	attempt      int
	errorMessage string
}

// retryableFailure reports whether a failed turn with the given error
// message is worth retrying given the attempts used so far. Context
// overflow is never retryable: compaction handles it later.
func retryableFailure(attempt, maxAttempts int, errorMessage string) bool {
	if attempt >= maxAttempts {
		return false
	}
	if IsContextOverflow(errorMessage) {
		return false
	}
	return Classify(errorMessage)
}

// Retry runs a single assistant-producing call with bounded retry on
// transient errors, mirroring Pi's retryAssistantCall.
//
// Behavior:
//   - A successful response (any StopReason other than "error" or
//     "aborted") is returned immediately; if a retry had been scheduled,
//     OnRetryFinished(true, attempt) is emitted first.
//   - Aborts are terminal and never retried, but reported as unsuccessful
//     if they happen after a retry was scheduled. A produce error while
//     ctx is cancelled is an abort, and so is a response with StopReason
//     "aborted". Aborts during the backoff sleep are normalized to an
//     aborted result: the failed message with StopReason "aborted" and a
//     cleared error message when the last failure was a provider message,
//     or the context error for transport failures.
//   - A non-retryable error (per Classify, including quota/billing
//     exhaustion) or a context-overflow error (per IsContextOverflow) is
//     returned immediately so deterministic failures fail fast.
//   - Otherwise the call is retried up to Policy.MaxRetries times with
//     exponential backoff baseDelayMs*2^(attempt-1), emitting
//     OnRetryScheduled before each sleep, OnRetryAttemptStart after each
//     sleep before the retried call starts, and OnRetryFinished once at
//     the end (success, exhausted retries, or aborted backoff).
//
// When policy.Enabled is false the first response is returned unchanged,
// equivalent to calling produce directly.
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

		// Cancellation is terminal and never retried: Pi's "aborted"
		// stop reason.
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
			// Aborted responses are terminal but not successful.
			if lastRetry != nil {
				emitFinished(callbacks, false, lastRetry.attempt, "")
			}
			return resp, nil
		} else if resp.StopReason != "error" {
			// Success: any non-error, non-abort response returns as-is.
			if lastRetry != nil {
				emitFinished(callbacks, true, lastRetry.attempt, "")
			}
			return resp, nil
		}

		// Failed turn: a provider error message or a transport error.
		var failed *agent.AssistantMessage
		errorMessage := ""
		if err != nil {
			errorMessage = err.Error()
		} else {
			failed = resp
			errorMessage = resp.ErrorMessage
		}

		// Non-retryable, context overflow, or budget exhausted: return
		// the final failure unchanged.
		if !retryableFailure(attempt, maxAttempts, errorMessage) {
			if lastRetry != nil {
				emitFinished(callbacks, false, lastRetry.attempt, errorMessage)
			}
			if failed != nil {
				return failed, nil
			}
			return nil, err
		}

		// Schedule the retry, sleep the backoff, then loop back into
		// the next produce call.
		attempt++
		lastRetry = &retryRecord{attempt: attempt, errorMessage: orUnknown(errorMessage)}
		if aborted, serr := backoff(ctx, failed, lastRetry, maxAttempts, policy, callbacks, o.sleep); serr != nil || aborted != nil {
			return aborted, serr
		}
	}
}

// backoff runs the sleep for one scheduled retry, mirroring Pi's
// onRetryScheduled -> sleep -> onRetryAttemptStart sequence. On success it
// returns (nil, nil) and the retried call starts. When the sleep is
// interrupted it emits OnRetryFinished and returns the normalized aborted
// result: the failed message with StopReason "aborted" when the last
// failure was a provider message (failed != nil), otherwise the context
// error.
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

// emitFinished emits OnRetryFinished when callbacks are present.
func emitFinished(callbacks Callbacks, success bool, attempt int, finalError string) {
	if callbacks != nil {
		callbacks.OnRetryFinished(success, attempt, finalError)
	}
}

// orUnknown mirrors Pi's `errorMessage || "Unknown error"` fallback used
// when a retry is scheduled.
func orUnknown(errorMessage string) string {
	if errorMessage == "" {
		return "Unknown error"
	}
	return errorMessage
}
