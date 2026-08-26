package oauth

import (
	"context"
	"os"
	"time"
)

const (
	ProviderOpenRouter = "openrouter-oauth"
	ProviderAnthropic  = "anthropic-oauth"
	ProviderCodex      = "codex"
	ProviderXAI        = "xai-subscription"
	ProviderKimi       = "kimi-coding-oauth"
)

const (
	defaultCallbackHost = "127.0.0.1"
	defaultLoginTimeout = 10 * time.Minute
	refreshSkew         = 5 * time.Minute
)

const EphemeralCallbackPort = -1

type Options struct {
	OpenBrowser  func(url string) error
	CallbackHost string
	Timeout      time.Duration
	ManualCode   func(ctx context.Context, prompt string) (string, error)
	DeviceCode   func(DeviceCode)
	CallbackPort int

	AuthorizeURL    string
	TokenURL        string
	AuthBaseURL     string
	DeviceCodeURL   string
	DeviceTokenURL  string
	VerificationURI string
	OAuthHost       string
}

type DeviceCode struct {
	UserCode         string
	VerificationURI  string
	IntervalSeconds  float64
	ExpiresInSeconds float64
}

func callbackHost(opts Options) string {
	if opts.CallbackHost != "" {
		return opts.CallbackHost
	}
	if host := os.Getenv("PI_OAUTH_CALLBACK_HOST"); host != "" {
		return host
	}
	return defaultCallbackHost
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func firstOptions(opts []Options) Options {
	if len(opts) == 0 {
		return Options{}
	}
	return opts[0]
}

func loginTimeout(opts Options, fallback time.Duration) time.Duration {
	if opts.Timeout > 0 {
		return opts.Timeout
	}
	return fallback
}

func resolveCallbackPort(opts Options, providerDefault int) int {
	if opts.CallbackPort < 0 {
		return 0
	}
	if opts.CallbackPort == 0 {
		return providerDefault
	}
	return opts.CallbackPort
}
