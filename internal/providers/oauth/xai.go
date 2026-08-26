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
	xaiClientID             = "b1a00492-073a-47ea-816f-4c329264a828"
	xaiScope                = "openid profile email offline_access grok-cli:access api:access"
	xaiDeviceCodeURL        = "https://auth.x.ai/oauth2/device/code"
	xaiTokenURL             = "https://auth.x.ai/oauth2/token"
	xaiDefaultTokenLifetime = 3600.0
)

type xaiDevice struct {
	deviceCode              string
	userCode                string
	verificationURI         string
	verificationURIComplete string
	intervalSeconds         float64
	expiresInSeconds        float64
}

func XaiLogin(ctx context.Context, opts Options) (authstore.Entry, error) {
	loginCtx := ctx
	cancel := func() {}
	if opts.Timeout > 0 {
		loginCtx, cancel = context.WithTimeout(ctx, opts.Timeout)
	}
	defer cancel()
	device, err := startXaiDeviceAuth(loginCtx, opts)
	if err != nil {
		return authstore.Entry{}, err
	}
	notifyDeviceCode(opts, DeviceCode{
		UserCode:         device.userCode,
		VerificationURI:  firstNonEmpty(device.verificationURIComplete, device.verificationURI),
		IntervalSeconds:  device.intervalSeconds,
		ExpiresInSeconds: device.expiresInSeconds,
	})
	value, err := pollDeviceCodeFlow(loginCtx, deviceFlowConfig{
		intervalSeconds:     device.intervalSeconds,
		expiresInSeconds:    device.expiresInSeconds,
		waitBeforeFirstPoll: true,
		poll: func(ctx context.Context) (devicePollOutcome, error) {
			return pollXaiTokens(ctx, opts, device)
		},
	})
	if err != nil {
		return authstore.Entry{}, err
	}
	return value.(authstore.Entry), nil
}

func XaiRefresh(ctx context.Context, entry authstore.Entry, opts ...Options) (authstore.Entry, error) {
	options := firstOptions(opts)
	status, body, err := xaiPostForm(ctx, firstNonEmpty(options.TokenURL, xaiTokenURL), url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {xaiClientID},
		"refresh_token": {entry.Refresh},
	})
	if err != nil {
		return authstore.Entry{}, err
	}
	if status < 200 || status >= 300 {
		return authstore.Entry{}, xaiFailure("token refresh", status, body)
	}
	return xaiCredentials(body, entry.Refresh)
}

func startXaiDeviceAuth(ctx context.Context, opts Options) (xaiDevice, error) {
	status, body, err := xaiPostForm(ctx, firstNonEmpty(opts.DeviceCodeURL, xaiDeviceCodeURL), url.Values{
		"client_id": {xaiClientID},
		"scope":     {xaiScope},
		"referrer":  {"pi"},
	})
	if err != nil {
		return xaiDevice{}, err
	}
	if status < 200 || status >= 300 {
		return xaiDevice{}, xaiFailure("device authorization", status, body)
	}
	deviceCode, err := requiredString(body, "device_code")
	if err != nil {
		return xaiDevice{}, err
	}
	userCode, err := requiredString(body, "user_code")
	if err != nil {
		return xaiDevice{}, err
	}
	verificationURI, err := requiredString(body, "verification_uri")
	if err != nil {
		return xaiDevice{}, err
	}
	verificationURI, err = validateHTTPS(verificationURI)
	if err != nil {
		return xaiDevice{}, err
	}
	verificationURIComplete := ""
	if raw, ok := body["verification_uri_complete"].(string); ok && raw != "" {
		verificationURIComplete, err = validateHTTPS(raw)
		if err != nil {
			return xaiDevice{}, err
		}
	}
	intervalSeconds := 0.0
	if raw, ok := body["interval"].(float64); ok && isFinite(raw) && raw > 0 {
		intervalSeconds = raw
	}
	expiresInSeconds, err := positiveNumber(body, "expires_in")
	if err != nil {
		return xaiDevice{}, err
	}
	return xaiDevice{
		deviceCode:              deviceCode,
		userCode:                userCode,
		verificationURI:         verificationURI,
		verificationURIComplete: verificationURIComplete,
		intervalSeconds:         intervalSeconds,
		expiresInSeconds:        expiresInSeconds,
	}, nil
}

func pollXaiTokens(ctx context.Context, opts Options, device xaiDevice) (devicePollOutcome, error) {
	status, body, err := xaiPostForm(ctx, firstNonEmpty(opts.TokenURL, xaiTokenURL), url.Values{
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		"client_id":   {xaiClientID},
		"device_code": {device.deviceCode},
	})
	if err != nil {
		return devicePollOutcome{}, err
	}
	if status >= 200 && status < 300 {
		entry, err := xaiCredentials(body, "")
		if err != nil {
			return devicePollOutcome{}, err
		}
		return outcomeComplete(entry), nil
	}
	errorCode := stringField(body, "error")
	switch errorCode {
	case "authorization_pending":
		return outcomePending(), nil
	case "slow_down":
		var interval *float64
		if raw, ok := body["interval"].(float64); ok {
			interval = &raw
		}
		return outcomeSlowDown(interval), nil
	case "access_denied", "authorization_denied":
		return outcomeFailed("xai device authorization was denied"), nil
	case "expired_token":
		return outcomeFailed("xai device code expired"), nil
	}
	return outcomeFailed(xaiFailure("device token polling", status, body).Error()), nil
}

func xaiPostForm(ctx context.Context, endpoint string, form url.Values) (int, map[string]any, error) {
	resp, err := postForm(ctx, endpoint, form)
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, ctxErrMessage(ctx.Err())
		}
		return 0, nil, err
	}
	body, readErr := readBody(resp)
	if readErr != nil {
		return resp.StatusCode, nil, readErr
	}
	parsed := decodeMap(body)
	if parsed == nil {
		if ctx.Err() != nil {
			return resp.StatusCode, nil, ctxErrMessage(ctx.Err())
		}
		return resp.StatusCode, nil, fmt.Errorf("xai oauth returned invalid JSON (HTTP %d)", resp.StatusCode)
	}
	return resp.StatusCode, parsed, nil
}

func xaiFailure(action string, status int, body map[string]any) error {
	detail := stringField(body, "error")
	if description := stringField(body, "error_description"); description != "" {
		if detail != "" {
			detail += ": " + description
		} else {
			detail = description
		}
	}
	message := fmt.Sprintf("xai oauth %s failed (HTTP %d)", action, status)
	if detail != "" {
		message += ": " + detail
	}
	return errors.New(message)
}

func xaiCredentials(body map[string]any, previousRefresh string) (authstore.Entry, error) {
	access, err := requiredString(body, "access_token")
	if err != nil {
		return authstore.Entry{}, err
	}
	var refresh string
	if _, ok := body["refresh_token"]; !ok && previousRefresh != "" {
		refresh = previousRefresh
	} else {
		refresh, err = requiredString(body, "refresh_token")
		if err != nil {
			return authstore.Entry{}, err
		}
	}
	expiresInSeconds := xaiDefaultTokenLifetime
	if _, ok := body["expires_in"]; ok {
		expiresInSeconds, err = positiveNumber(body, "expires_in")
		if err != nil {
			return authstore.Entry{}, err
		}
	}
	expires := time.Now().UnixMilli() + int64(expiresInSeconds*1000) - int64(refreshSkew/time.Millisecond)
	return newEntry(access, refresh, expires, nil)
}

func requiredString(body map[string]any, field string) (string, error) {
	value, ok := body[field].(string)
	if !ok || value == "" {
		return "", fmt.Errorf("invalid xai oauth response field: %s", field)
	}
	return value, nil
}

func positiveNumber(body map[string]any, field string) (float64, error) {
	value, ok := body[field].(float64)
	if !ok || !isFinite(value) || value <= 0 {
		return 0, fmt.Errorf("invalid xai oauth response field: %s", field)
	}
	return value, nil
}

func validateHTTPS(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" {
		return "", errors.New("untrusted verification URI in xai oauth response")
	}
	return parsed.String(), nil
}
