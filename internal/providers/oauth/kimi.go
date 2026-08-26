package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/digitalygo/smidja/internal/authstore"
)

const (
	kimiClientID                   = "17e5f671-d194-4dfb-9706-5516cb48c098"
	kimiDefaultOAuthHost           = "https://auth.kimi.com"
	kimiDeviceTimeoutSeconds       = 15 * 60
	kimiDefaultPollIntervalSeconds = 5.0
	kimiRequestTimeout             = 30 * time.Second
	kimiRefreshMaxRetries          = 3
)

type kimiDevice struct {
	deviceCode              string
	userCode                string
	verificationURI         string
	verificationURIComplete string
	intervalSeconds         float64
	expiresInSeconds        float64
}

func KimiLogin(ctx context.Context, opts Options) (authstore.Entry, error) {
	loginCtx := ctx
	cancel := func() {}
	if opts.Timeout > 0 {
		loginCtx, cancel = context.WithTimeout(ctx, opts.Timeout)
	}
	defer cancel()
	host := resolveKimiHost(opts)
	device, err := startKimiDeviceAuth(loginCtx, host)
	if err != nil {
		return authstore.Entry{}, err
	}
	notifyDeviceCode(opts, DeviceCode{
		UserCode:         device.userCode,
		VerificationURI:  device.verificationURIComplete,
		IntervalSeconds:  device.intervalSeconds,
		ExpiresInSeconds: device.expiresInSeconds,
	})
	value, err := pollDeviceCodeFlow(loginCtx, deviceFlowConfig{
		intervalSeconds:     device.intervalSeconds,
		expiresInSeconds:    device.expiresInSeconds,
		waitBeforeFirstPoll: true,
		poll: func(ctx context.Context) (devicePollOutcome, error) {
			return pollKimiToken(ctx, host, device)
		},
	})
	if err != nil {
		return authstore.Entry{}, err
	}
	return value.(authstore.Entry), nil
}

func KimiRefresh(ctx context.Context, entry authstore.Entry, opts ...Options) (authstore.Entry, error) {
	options := firstOptions(opts)
	return refreshKimiToken(ctx, resolveKimiHost(options), entry.Refresh)
}

func resolveKimiHost(opts Options) string {
	host := opts.OAuthHost
	if host == "" {
		host = os.Getenv("KIMI_CODE_OAUTH_HOST")
	}
	if host == "" {
		host = os.Getenv("KIMI_OAUTH_HOST")
	}
	return strings.TrimRight(firstNonEmpty(host, kimiDefaultOAuthHost), "/")
}

func startKimiDeviceAuth(ctx context.Context, host string) (kimiDevice, error) {
	reqCtx, cancel := context.WithTimeout(ctx, kimiRequestTimeout)
	defer cancel()
	resp, err := postForm(reqCtx, host+"/api/oauth/device_authorization", url.Values{
		"client_id": {kimiClientID},
	})
	if err != nil {
		return kimiDevice{}, err
	}
	body, readErr := readBody(resp)
	if readErr != nil {
		return kimiDevice{}, readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := fmt.Sprintf("kimi code device authorization failed with status %d", resp.StatusCode)
		if text := strings.TrimSpace(string(body)); text != "" {
			message += ": " + text
		}
		return kimiDevice{}, errors.New(message)
	}
	parsed := decodeMap(body)
	if parsed == nil {
		return kimiDevice{}, fmt.Errorf("invalid kimi code device authorization response: %s", body)
	}
	deviceCode := stringField(parsed, "device_code")
	userCode := stringField(parsed, "user_code")
	verificationURI := stringField(parsed, "verification_uri")
	verificationURIComplete := stringField(parsed, "verification_uri_complete")
	if deviceCode == "" || userCode == "" || verificationURI == "" || verificationURIComplete == "" ||
		!trustedHTTPURL(verificationURI) || !trustedHTTPURL(verificationURIComplete) {
		return kimiDevice{}, fmt.Errorf("invalid kimi code device authorization response: %s", body)
	}
	intervalSeconds := kimiDefaultPollIntervalSeconds
	if value, ok := kimiPositiveNumber(parsed, "interval"); ok {
		intervalSeconds = value
	}
	expiresInSeconds := float64(kimiDeviceTimeoutSeconds)
	if value, ok := kimiPositiveNumber(parsed, "expires_in"); ok {
		expiresInSeconds = value
	}
	return kimiDevice{
		deviceCode:              deviceCode,
		userCode:                userCode,
		verificationURI:         verificationURI,
		verificationURIComplete: verificationURIComplete,
		intervalSeconds:         intervalSeconds,
		expiresInSeconds:        expiresInSeconds,
	}, nil
}

func pollKimiToken(ctx context.Context, host string, device kimiDevice) (devicePollOutcome, error) {
	reqCtx, cancel := context.WithTimeout(ctx, kimiRequestTimeout)
	defer cancel()
	resp, err := postForm(reqCtx, host+"/api/oauth/token", url.Values{
		"client_id":   {kimiClientID},
		"device_code": {device.deviceCode},
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
	})
	if err != nil {
		return devicePollOutcome{}, err
	}
	body, readErr := readBody(resp)
	if readErr != nil {
		return devicePollOutcome{}, readErr
	}
	if resp.StatusCode >= 500 {
		message := fmt.Sprintf("kimi code device token request failed with status %d", resp.StatusCode)
		if text := strings.TrimSpace(string(body)); text != "" {
			message += ": " + text
		}
		return outcomeFailed(message), nil
	}
	parsed := decodeMap(body)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && parsed != nil && stringField(parsed, "access_token") != "" {
		entry, err := kimiTokenResponse(parsed, "poll")
		if err != nil {
			return outcomeFailed(err.Error()), nil
		}
		return outcomeComplete(entry), nil
	}
	errorCode := ""
	if parsed != nil {
		errorCode = stringField(parsed, "error")
	}
	switch errorCode {
	case "authorization_pending":
		return outcomePending(), nil
	case "slow_down":
		var interval *float64
		if parsed != nil {
			if value, ok := kimiPositiveNumber(parsed, "interval"); ok {
				interval = &value
			}
		}
		return outcomeSlowDown(interval), nil
	case "expired_token":
		return outcomeFailed("kimi code device authorization expired. please restart login."), nil
	case "access_denied":
		return outcomeFailed("kimi code login was denied."), nil
	}
	message := fmt.Sprintf("kimi code device token request failed (status %d)", resp.StatusCode)
	if errorCode != "" {
		message += ": " + errorCode
		if parsed != nil {
			if description := stringField(parsed, "error_description"); description != "" {
				message += ": " + description
			}
		}
	}
	return outcomeFailed(message), nil
}

func kimiTokenResponse(body map[string]any, operation string) (authstore.Entry, error) {
	access := stringField(body, "access_token")
	refresh := stringField(body, "refresh_token")
	expiresIn, ok := kimiPositiveNumber(body, "expires_in")
	if access == "" || refresh == "" || !ok {
		raw, _ := json.Marshal(body)
		return authstore.Entry{}, fmt.Errorf("kimi code token %s response missing fields: %s", operation, raw)
	}
	expires := time.Now().UnixMilli() + int64(expiresIn*1000)
	return newEntry(access, refresh, expires, nil)
}

func refreshKimiToken(ctx context.Context, host, refreshToken string) (authstore.Entry, error) {
	var lastErr error
	for attempt := 0; attempt <= kimiRefreshMaxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1000*(1<<(attempt-1))) * time.Millisecond
			if err := sleepContext(ctx, backoff); err != nil {
				return authstore.Entry{}, err
			}
		}
		if ctx.Err() != nil {
			return authstore.Entry{}, errors.New("kimi code token refresh aborted")
		}
		reqCtx, cancel := context.WithTimeout(ctx, kimiRequestTimeout)
		resp, err := postForm(reqCtx, host+"/api/oauth/token", url.Values{
			"client_id":     {kimiClientID},
			"grant_type":    {"refresh_token"},
			"refresh_token": {refreshToken},
		})
		cancel()
		if err != nil {
			lastErr = err
			continue
		}
		body, readErr := readBody(resp)
		if readErr != nil {
			lastErr = readErr
			continue
		}
		parsed := decodeMap(body)
		if resp.StatusCode >= 200 && resp.StatusCode < 300 && parsed != nil {
			return kimiTokenResponse(parsed, "refresh")
		}
		errorCode := ""
		if parsed != nil {
			errorCode = stringField(parsed, "error")
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || errorCode == "invalid_grant" {
			message := fmt.Sprintf("kimi code token refresh unauthorized (status %d)", resp.StatusCode)
			if parsed != nil {
				if description := stringField(parsed, "error_description"); description != "" {
					message += ": " + description
				}
			}
			return authstore.Entry{}, errors.New(message)
		}
		if isRetryableKimiRefresh(resp.StatusCode) && attempt < kimiRefreshMaxRetries {
			lastErr = fmt.Errorf("kimi code token refresh failed with status %d", resp.StatusCode)
			continue
		}
		message := fmt.Sprintf("kimi code token refresh failed with status %d", resp.StatusCode)
		if text := strings.TrimSpace(string(body)); text != "" {
			message += ": " + text
		}
		return authstore.Entry{}, errors.New(message)
	}
	if lastErr == nil {
		lastErr = errors.New("kimi code token refresh failed")
	}
	return authstore.Entry{}, lastErr
}

func isRetryableKimiRefresh(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

func trustedHTTPURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return parsed.Scheme == "https" || parsed.Scheme == "http"
}

func kimiPositiveNumber(body map[string]any, field string) (float64, bool) {
	value, ok := body[field].(float64)
	if !ok || !isFinite(value) || value <= 0 {
		return 0, false
	}
	return value, true
}
