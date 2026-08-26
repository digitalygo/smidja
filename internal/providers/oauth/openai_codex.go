package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/digitalygo/smidja/internal/authstore"
)

const (
	codexClientID             = "app_EMoamEEZ73f0CkXaXp7hrann"
	codexAuthBaseURL          = "https://auth.openai.com"
	codexCallbackPort         = 1455
	codexCallbackPath         = "/auth/callback"
	codexScope                = "openid profile email offline_access"
	codexJWTClaimPath         = "https://api.openai.com/auth"
	codexDeviceTimeoutSeconds = 15 * 60
)

type codexEndpoints struct {
	authorize      string
	token          string
	deviceCode     string
	deviceToken    string
	verification   string
	deviceRedirect string
}

func resolveCodexEndpoints(opts Options) codexEndpoints {
	base := strings.TrimRight(firstNonEmpty(opts.AuthBaseURL, codexAuthBaseURL), "/")
	return codexEndpoints{
		authorize:      firstNonEmpty(opts.AuthorizeURL, base+"/oauth/authorize"),
		token:          firstNonEmpty(opts.TokenURL, base+"/oauth/token"),
		deviceCode:     firstNonEmpty(opts.DeviceCodeURL, base+"/api/accounts/deviceauth/usercode"),
		deviceToken:    firstNonEmpty(opts.DeviceTokenURL, base+"/api/accounts/deviceauth/token"),
		verification:   firstNonEmpty(opts.VerificationURI, base+"/codex/device"),
		deviceRedirect: base + "/deviceauth/callback",
	}
}

type codexDevice struct {
	deviceAuthID    string
	userCode        string
	intervalSeconds float64
}

type codexPollValue struct {
	authorizationCode string
	codeVerifier      string
}

func CodexLogin(ctx context.Context, opts Options) (authstore.Entry, error) {
	loginCtx, cancel := context.WithTimeout(ctx, loginTimeout(opts, defaultLoginTimeout))
	defer cancel()
	host := callbackHost(opts)
	port := resolveCallbackPort(opts, codexCallbackPort)
	endpoints := resolveCodexEndpoints(opts)
	verifier, challenge, err := generatePKCE()
	if err != nil {
		return authstore.Entry{}, err
	}
	state, err := randomHex(16)
	if err != nil {
		return authstore.Entry{}, err
	}
	server, err := startCallbackServer(loginCtx, host, port, "localhost", callbackConfig{
		providerName:         "OpenAI",
		path:                 codexCallbackPath,
		expectedState:        state,
		checkStateBeforeCode: true,
		missingCodeMessage:   "Missing authorization code.",
		successMessage:       "OpenAI authentication completed. You can close this window.",
	})
	if err != nil {
		server = nil
	} else {
		defer server.close()
	}
	redirectURI := codexRedirectURI(port)
	if server != nil {
		redirectURI = server.url
	}
	authorizeURL := buildCodexAuthorizeURL(endpoints.authorize, redirectURI, challenge, state)
	if opts.OpenBrowser != nil {
		_ = opts.OpenBrowser(authorizeURL)
	}
	outcome, err := awaitCode(loginCtx, opts, redirectURI, server)
	if err != nil {
		return authstore.Entry{}, err
	}
	if outcome.err != nil {
		return authstore.Entry{}, outcome.err
	}
	code := outcome.code
	if outcome.manualInput != "" {
		parsedCode, parsedState, ok := parseAuthorizationInput(outcome.manualInput)
		if !ok {
			return authstore.Entry{}, errors.New("missing authorization code")
		}
		if parsedState != "" && parsedState != state {
			return authstore.Entry{}, errors.New("State mismatch")
		}
		code = parsedCode
	}
	if code == "" {
		return authstore.Entry{}, errors.New("missing authorization code")
	}
	return exchangeCodexCode(loginCtx, code, verifier, redirectURI, endpoints.token)
}

func CodexDeviceLogin(ctx context.Context, opts Options) (authstore.Entry, error) {
	loginCtx := ctx
	cancel := func() {}
	if opts.Timeout > 0 {
		loginCtx, cancel = context.WithTimeout(ctx, opts.Timeout)
	}
	defer cancel()
	endpoints := resolveCodexEndpoints(opts)
	device, err := startCodexDeviceAuth(loginCtx, endpoints)
	if err != nil {
		return authstore.Entry{}, err
	}
	notifyDeviceCode(opts, DeviceCode{
		UserCode:         device.userCode,
		VerificationURI:  endpoints.verification,
		IntervalSeconds:  device.intervalSeconds,
		ExpiresInSeconds: codexDeviceTimeoutSeconds,
	})
	value, err := pollDeviceCodeFlow(loginCtx, deviceFlowConfig{
		intervalSeconds:  device.intervalSeconds,
		expiresInSeconds: codexDeviceTimeoutSeconds,
		poll: func(ctx context.Context) (devicePollOutcome, error) {
			return pollCodexDeviceAuth(ctx, endpoints, device)
		},
	})
	if err != nil {
		return authstore.Entry{}, err
	}
	authorized := value.(codexPollValue)
	return exchangeCodexCode(loginCtx, authorized.authorizationCode, authorized.codeVerifier, endpoints.deviceRedirect, endpoints.token)
}

func CodexRefresh(ctx context.Context, entry authstore.Entry, opts ...Options) (authstore.Entry, error) {
	options := firstOptions(opts)
	endpoints := resolveCodexEndpoints(options)
	resp, err := postForm(ctx, endpoints.token, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {entry.Refresh},
		"client_id":     {codexClientID},
	})
	if err != nil {
		return authstore.Entry{}, fmt.Errorf("openai codex token refresh error: %v", err)
	}
	return readCodexTokenResponse(resp, "refresh")
}

func buildCodexAuthorizeURL(authorizeURL, redirectURI, challenge, state string) string {
	query := url.Values{}
	query.Set("response_type", "code")
	query.Set("client_id", codexClientID)
	query.Set("redirect_uri", redirectURI)
	query.Set("scope", codexScope)
	query.Set("code_challenge", challenge)
	query.Set("code_challenge_method", "S256")
	query.Set("state", state)
	query.Set("id_token_add_organizations", "true")
	query.Set("codex_cli_simplified_flow", "true")
	query.Set("originator", "pi")
	return authorizeURL + "?" + query.Encode()
}

func codexRedirectURI(port int) string {
	return "http://localhost:" + strconv.Itoa(port) + codexCallbackPath
}

func exchangeCodexCode(ctx context.Context, code, verifier, redirectURI, tokenURL string) (authstore.Entry, error) {
	resp, err := postForm(ctx, tokenURL, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {codexClientID},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {redirectURI},
	})
	if err != nil {
		if ctx.Err() != nil {
			return authstore.Entry{}, ctxErrMessage(ctx.Err())
		}
		return authstore.Entry{}, err
	}
	return readCodexTokenResponse(resp, "exchange")
}

func readCodexTokenResponse(resp *http.Response, operation string) (authstore.Entry, error) {
	body, readErr := readBody(resp)
	if readErr != nil {
		return authstore.Entry{}, readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := fmt.Sprintf("openai codex token %s failed (%d)", operation, resp.StatusCode)
		if text := strings.TrimSpace(string(body)); text != "" {
			message += ": " + text
		}
		return authstore.Entry{}, errors.New(message)
	}
	var parsed struct {
		AccessToken  string   `json:"access_token"`
		RefreshToken string   `json:"refresh_token"`
		ExpiresIn    *float64 `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return authstore.Entry{}, fmt.Errorf("openai codex token %s response invalid JSON: %s", operation, body)
	}
	if parsed.AccessToken == "" || parsed.RefreshToken == "" || parsed.ExpiresIn == nil {
		return authstore.Entry{}, fmt.Errorf("openai codex token %s response missing fields: %s", operation, body)
	}
	accountID, err := codexAccountID(parsed.AccessToken)
	if err != nil {
		return authstore.Entry{}, err
	}
	expires := time.Now().UnixMilli() + int64(*parsed.ExpiresIn*1000)
	return newEntry(parsed.AccessToken, parsed.RefreshToken, expires, map[string]string{"accountId": accountID})
}

func startCodexDeviceAuth(ctx context.Context, endpoints codexEndpoints) (codexDevice, error) {
	resp, err := postJSON(ctx, endpoints.deviceCode, map[string]any{"client_id": codexClientID})
	if err != nil {
		if ctx.Err() != nil {
			return codexDevice{}, ctxErrMessage(ctx.Err())
		}
		return codexDevice{}, err
	}
	body, readErr := readBody(resp)
	if readErr != nil {
		return codexDevice{}, readErr
	}
	if resp.StatusCode == http.StatusNotFound {
		return codexDevice{}, errors.New("openai codex device code login is not enabled for this server. use browser login or verify the server URL.")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := fmt.Sprintf("openai codex device code request failed with status %d", resp.StatusCode)
		if text := strings.TrimSpace(string(body)); text != "" {
			message += ": " + text
		}
		return codexDevice{}, errors.New(message)
	}
	parsed := decodeMap(body)
	if parsed == nil {
		return codexDevice{}, fmt.Errorf("openai codex device code response invalid JSON: %s", body)
	}
	deviceAuthID := stringField(parsed, "device_auth_id")
	userCode := stringField(parsed, "user_code")
	intervalSeconds, intervalErr := codexIntervalSeconds(parsed)
	if deviceAuthID == "" || userCode == "" || intervalErr != nil {
		return codexDevice{}, fmt.Errorf("invalid openai codex device code response: %s", body)
	}
	return codexDevice{deviceAuthID: deviceAuthID, userCode: userCode, intervalSeconds: intervalSeconds}, nil
}

func codexIntervalSeconds(body map[string]any) (float64, error) {
	raw, ok := body["interval"]
	if !ok {
		return 0, errors.New("missing interval")
	}
	var value float64
	switch typed := raw.(type) {
	case float64:
		value = typed
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return 0, err
		}
		value = parsed
	default:
		return 0, fmt.Errorf("invalid interval type %T", raw)
	}
	if !isFinite(value) || value < 0 {
		return 0, fmt.Errorf("invalid interval %v", value)
	}
	return value, nil
}

func pollCodexDeviceAuth(ctx context.Context, endpoints codexEndpoints, device codexDevice) (devicePollOutcome, error) {
	resp, err := postJSON(ctx, endpoints.deviceToken, map[string]any{
		"device_auth_id": device.deviceAuthID,
		"user_code":      device.userCode,
	})
	if err != nil {
		if ctx.Err() != nil {
			return devicePollOutcome{}, ctxErrMessage(ctx.Err())
		}
		return devicePollOutcome{}, err
	}
	body, readErr := readBody(resp)
	if readErr != nil {
		return devicePollOutcome{}, readErr
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		parsed := decodeMap(body)
		if parsed == nil {
			return devicePollOutcome{}, fmt.Errorf("openai codex device auth token response invalid JSON: %s", body)
		}
		authorizationCode := stringField(parsed, "authorization_code")
		codeVerifier := stringField(parsed, "code_verifier")
		if authorizationCode == "" || codeVerifier == "" {
			return outcomeFailed(fmt.Sprintf("invalid openai codex device auth token response: %s", body)), nil
		}
		return outcomeComplete(codexPollValue{authorizationCode: authorizationCode, codeVerifier: codeVerifier}), nil
	}
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
		return outcomePending(), nil
	}
	errorCode := codexErrorCode(body)
	if errorCode == "deviceauth_authorization_pending" {
		return outcomePending(), nil
	}
	if errorCode == "slow_down" {
		return outcomeSlowDown(nil), nil
	}
	message := fmt.Sprintf("openai codex device auth failed with status %d", resp.StatusCode)
	if text := strings.TrimSpace(string(body)); text != "" {
		message += ": " + text
	}
	return outcomeFailed(message), nil
}

func codexErrorCode(body []byte) string {
	var parsed struct {
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ""
	}
	var code string
	if err := json.Unmarshal(parsed.Error, &code); err == nil {
		return code
	}
	var object struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(parsed.Error, &object); err == nil {
		return object.Code
	}
	return ""
}
