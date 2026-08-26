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
	anthropicClientID       = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	anthropicAuthorizeURL   = "https://claude.ai/oauth/authorize"
	anthropicTokenURL       = "https://platform.claude.com/v1/oauth/token"
	anthropicCallbackPort   = 53692
	anthropicCallbackPath   = "/callback"
	anthropicScopes         = "org:create_api_key user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"
	anthropicRequestTimeout = 30 * time.Second
)

func AnthropicLogin(ctx context.Context, opts Options) (authstore.Entry, error) {
	loginCtx, cancel := context.WithTimeout(ctx, loginTimeout(opts, defaultLoginTimeout))
	defer cancel()
	host := callbackHost(opts)
	port := resolveCallbackPort(opts, anthropicCallbackPort)
	verifier, challenge, err := generatePKCE()
	if err != nil {
		return authstore.Entry{}, err
	}
	server, err := startCallbackServer(loginCtx, host, port, "localhost", callbackConfig{
		providerName:       "Anthropic",
		path:               anthropicCallbackPath,
		expectedState:      verifier,
		requireState:       true,
		deniedPageTitle:    "Anthropic authentication did not complete.",
		deniedPageDetail:   "Error: %s",
		missingCodeMessage: "Missing code or state parameter.",
		successMessage:     "Anthropic authentication completed. You can close this window.",
	})
	if err != nil {
		return authstore.Entry{}, err
	}
	defer server.close()
	authorizeURL := buildAnthropicAuthorizeURL(server.url, verifier, challenge, opts)
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
	code, state := outcome.code, outcome.state
	if outcome.manualInput != "" {
		parsedCode, parsedState, ok := parseAuthorizationInput(outcome.manualInput)
		if !ok {
			return authstore.Entry{}, errors.New("missing authorization code")
		}
		if parsedState != "" && parsedState != verifier {
			return authstore.Entry{}, errors.New("OAuth state mismatch")
		}
		code, state = parsedCode, parsedState
		if state == "" {
			state = verifier
		}
	}
	if code == "" {
		return authstore.Entry{}, errors.New("missing authorization code")
	}
	if state == "" {
		return authstore.Entry{}, errors.New("missing OAuth state")
	}
	return exchangeAnthropicCode(loginCtx, code, state, verifier, server.url, opts)
}

func AnthropicRefresh(ctx context.Context, entry authstore.Entry, opts ...Options) (authstore.Entry, error) {
	options := firstOptions(opts)
	reqCtx, cancel := context.WithTimeout(ctx, anthropicRequestTimeout)
	defer cancel()
	tokenURL := firstNonEmpty(options.TokenURL, anthropicTokenURL)
	resp, err := postJSON(reqCtx, tokenURL, map[string]any{
		"grant_type":    "refresh_token",
		"client_id":     anthropicClientID,
		"refresh_token": entry.Refresh,
	})
	if err != nil {
		return authstore.Entry{}, fmt.Errorf("anthropic token refresh request failed. url=%s; details=%v", tokenURL, err)
	}
	body, readErr := readBody(resp)
	if readErr != nil {
		return authstore.Entry{}, readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return authstore.Entry{}, fmt.Errorf("HTTP request failed. status=%d; url=%s; body=%s", resp.StatusCode, tokenURL, body)
	}
	parsed := decodeMap(body)
	if parsed == nil {
		return authstore.Entry{}, fmt.Errorf("anthropic token refresh returned invalid JSON. url=%s; body=%s", tokenURL, body)
	}
	return anthropicCredentials(parsed)
}

func buildAnthropicAuthorizeURL(redirectURI, verifier, challenge string, opts Options) string {
	query := url.Values{}
	query.Set("code", "true")
	query.Set("client_id", anthropicClientID)
	query.Set("response_type", "code")
	query.Set("redirect_uri", redirectURI)
	query.Set("scope", anthropicScopes)
	query.Set("code_challenge", challenge)
	query.Set("code_challenge_method", "S256")
	query.Set("state", verifier)
	return firstNonEmpty(opts.AuthorizeURL, anthropicAuthorizeURL) + "?" + query.Encode()
}

func exchangeAnthropicCode(ctx context.Context, code, state, verifier, redirectURI string, opts Options) (authstore.Entry, error) {
	reqCtx, cancel := context.WithTimeout(ctx, anthropicRequestTimeout)
	defer cancel()
	tokenURL := firstNonEmpty(opts.TokenURL, anthropicTokenURL)
	resp, err := postJSON(reqCtx, tokenURL, map[string]any{
		"grant_type":    "authorization_code",
		"client_id":     anthropicClientID,
		"code":          code,
		"state":         state,
		"redirect_uri":  redirectURI,
		"code_verifier": verifier,
	})
	if err != nil {
		return authstore.Entry{}, fmt.Errorf("token exchange request failed. url=%s; redirect_uri=%s; response_type=authorization_code; details=%v", tokenURL, redirectURI, err)
	}
	body, readErr := readBody(resp)
	if readErr != nil {
		return authstore.Entry{}, readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return authstore.Entry{}, fmt.Errorf("HTTP request failed. status=%d; url=%s; body=%s", resp.StatusCode, tokenURL, body)
	}
	parsed := decodeMap(body)
	if parsed == nil {
		return authstore.Entry{}, fmt.Errorf("token exchange returned invalid JSON. url=%s; body=%s", tokenURL, body)
	}
	return anthropicCredentials(parsed)
}

func anthropicCredentials(body map[string]any) (authstore.Entry, error) {
	access := stringField(body, "access_token")
	refresh := stringField(body, "refresh_token")
	expiresIn := 0.0
	if value, ok := body["expires_in"].(float64); ok {
		expiresIn = value
	}
	expires := time.Now().UnixMilli() + int64(expiresIn*1000) - int64(refreshSkew/time.Millisecond)
	return newEntry(access, refresh, expires, nil)
}
