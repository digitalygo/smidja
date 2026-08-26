package oauth

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/digitalygo/smidja/internal/authstore"
)

const (
	openRouterAuthorizeURL    = "https://openrouter.ai/auth"
	openRouterTokenURL        = "https://openrouter.ai/api/v1/auth/keys"
	openRouterLoginTimeout    = 5 * time.Minute
	openRouterExchangeTimeout = 30 * time.Second
	expiresMaxSafeInteger     = int64(1<<53 - 1)
)

func OpenRouterLogin(ctx context.Context, opts Options) (authstore.Entry, error) {
	loginCtx, cancel := context.WithTimeout(ctx, loginTimeout(opts, openRouterLoginTimeout))
	defer cancel()
	verifier, challenge, err := generatePKCE()
	if err != nil {
		return authstore.Entry{}, err
	}
	path, err := openRouterCallbackPath()
	if err != nil {
		return authstore.Entry{}, err
	}
	host := callbackHost(opts)
	server, err := startCallbackServer(loginCtx, host, resolveCallbackPort(opts, 0), host, callbackConfig{
		providerName:       "OpenRouter",
		path:               path,
		deniedPageTitle:    "OpenRouter authorization was denied.",
		deniedPageDetail:   "%s",
		deniedError:        "OpenRouter authorization failed: %s",
		missingCodeMessage: "OpenRouter returned no authorization code.",
		successMessage:     "Signed in to OpenRouter. You may now close this page.",
		exchange: func(ctx context.Context, code string) (authstore.Entry, error) {
			return exchangeOpenRouterCode(ctx, code, verifier, opts)
		},
	})
	if err != nil {
		return authstore.Entry{}, err
	}
	defer server.close()
	authorizeURL := buildOpenRouterAuthorizeURL(server.url, challenge, opts)
	if opts.OpenBrowser != nil {
		_ = opts.OpenBrowser(authorizeURL)
	}
	outcome, err := awaitCode(loginCtx, opts, server.url, server)
	if err != nil {
		return authstore.Entry{}, err
	}
	if outcome.err != nil {
		return authstore.Entry{}, outcome.err
	}
	if outcome.hasEntry {
		return outcome.entry, nil
	}
	code := outcome.code
	if outcome.manualInput != "" {
		parsedCode, _, ok := parseAuthorizationInput(outcome.manualInput)
		if !ok {
			return authstore.Entry{}, errors.New("missing authorization code")
		}
		code = parsedCode
	}
	if code == "" {
		return authstore.Entry{}, errors.New("missing authorization code")
	}
	return exchangeOpenRouterCode(loginCtx, code, verifier, opts)
}

func OpenRouterRefresh(ctx context.Context, entry authstore.Entry, opts ...Options) (authstore.Entry, error) {
	return entry, nil
}

func buildOpenRouterAuthorizeURL(callbackURL, challenge string, opts Options) string {
	parsed, err := url.Parse(firstNonEmpty(opts.AuthorizeURL, openRouterAuthorizeURL))
	if err != nil {
		parsed = &url.URL{}
	}
	query := parsed.Query()
	query.Set("callback_url", callbackURL)
	query.Set("code_challenge", challenge)
	query.Set("code_challenge_method", "S256")
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func openRouterCallbackPath() (string, error) {
	id, err := randomUUID()
	if err != nil {
		return "", err
	}
	return "/oauth/callback/" + id, nil
}

func exchangeOpenRouterCode(ctx context.Context, code, verifier string, opts Options) (authstore.Entry, error) {
	exchangeCtx, cancel := context.WithTimeout(ctx, openRouterExchangeTimeout)
	defer cancel()
	resp, err := postJSON(exchangeCtx, firstNonEmpty(opts.TokenURL, openRouterTokenURL), map[string]any{
		"code":                  code,
		"code_verifier":         verifier,
		"code_challenge_method": "S256",
	})
	if err != nil {
		if ctx.Err() != nil {
			return authstore.Entry{}, ctxErrMessage(ctx.Err())
		}
		if exchangeCtx.Err() != nil {
			return authstore.Entry{}, errors.New("openrouter oauth token exchange timed out")
		}
		return authstore.Entry{}, err
	}
	body, readErr := readBody(resp)
	if readErr != nil {
		return authstore.Entry{}, readErr
	}
	parsed := decodeMap(body)
	if parsed == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return authstore.Entry{}, errors.New("openrouter oauth returned invalid JSON")
	}
	if parsed == nil {
		parsed = map[string]any{}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail := errorDetail(parsed)
		message := fmt.Sprintf("openrouter oauth key exchange failed (HTTP %d)", resp.StatusCode)
		if detail != "" {
			message += ": " + detail
		}
		return authstore.Entry{}, errors.New(message)
	}
	key, _ := parsed["key"].(string)
	if key == "" {
		return authstore.Entry{}, errors.New(`openrouter oauth response carries no "key"`)
	}
	return newEntry(key, "", expiresMaxSafeInteger, nil)
}
