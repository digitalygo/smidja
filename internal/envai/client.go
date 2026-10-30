package envai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const ContractVersion = "3"
const maximumResponseBytes = 8 << 20

var ErrUnsupportedFeature = errors.New("envai feature unavailable")
var ErrContractMismatch = errors.New("envai contract version mismatch")

type Config struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
	signing signingKeys
}

type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("envai HTTP %d (%s): %s", e.Status, e.Code, e.Message)
}

type Capabilities struct {
	ProtocolVersion int      `json:"protocol_version"`
	ContractVersion int      `json:"contract_version"`
	Features        []string `json:"features"`
	EventTypes      []string `json:"event_types"`
}

func (c Capabilities) Has(feature string) bool {
	for _, candidate := range c.Features {
		if candidate == feature {
			return true
		}
	}
	return false
}

func New(config Config) (*Client, error) {
	parsed, err := url.Parse(config.BaseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("envai base URL must be an absolute HTTP(S) URL without path, credentials, query, or fragment")
	}
	if parsed.Scheme == "http" {
		ip := net.ParseIP(parsed.Hostname())
		if !strings.EqualFold(parsed.Hostname(), "localhost") && (ip == nil || !ip.IsLoopback()) {
			return nil, errors.New("Envai API credentials require HTTPS except on loopback")
		}
	}
	if strings.TrimSpace(config.APIKey) == "" || strings.ContainsAny(config.APIKey, "\r\n") {
		return nil, errors.New("envai API key is required")
	}
	transport := config.HTTPClient
	if transport == nil {
		transport = &http.Client{Timeout: 0}
	}
	copyClient := *transport
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{baseURL: strings.TrimRight(config.BaseURL, "/"), apiKey: config.APIKey, http: &copyClient}, nil
}

func (c *Client) request(ctx context.Context, method, path string, input any, atp bool) (*http.Response, error) {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	if path != "/.well-known/jwks.json" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	req.Header.Set("Accept", "application/json")
	if strings.Contains(path, "/events?") {
		req.Header.Set("Accept", "text/event-stream")
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if atp {
		req.Header.Set("X-Envai-Contract-Version", ContractVersion)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("envai request outcome may be unknown: %w", err)
	}
	if atp && (len(resp.Header.Values("X-Envai-Contract-Version")) != 1 || resp.Header.Get("X-Envai-Contract-Version") != ContractVersion) {
		resp.Body.Close()
		return nil, ErrContractMismatch
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		var payload struct {
			Error   json.RawMessage `json:"error"`
			Code    string          `json:"code"`
			Message string          `json:"message"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&payload)
		var nested struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if json.Unmarshal(payload.Error, &nested) == nil {
			if nested.Code != "" {
				payload.Code = nested.Code
			}
			if nested.Message != "" {
				payload.Message = nested.Message
			}
		}
		if payload.Message == "" {
			payload.Message = http.StatusText(resp.StatusCode)
		}
		return nil, &APIError{Status: resp.StatusCode, Code: payload.Code, Message: payload.Message}
	}
	return resp, nil
}

func (c *Client) json(ctx context.Context, method, path string, input, output any, atp bool) error {
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	resp, err := c.request(bounded, method, path, input, atp)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if output == nil {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maximumResponseBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maximumResponseBytes {
		return errors.New("envai response exceeds supported size")
	}
	if err = json.Unmarshal(data, output); err != nil {
		return fmt.Errorf("invalid envai JSON: %w", err)
	}
	return nil
}

func (c *Client) Capabilities(ctx context.Context) (Capabilities, error) {
	var result Capabilities
	err := c.json(ctx, http.MethodGet, "/api/v1/atp/v3/capabilities", nil, &result, true)
	if err == nil && (result.ProtocolVersion != 3 || result.ContractVersion != 3) {
		err = ErrContractMismatch
	}
	return result, err
}

func (c *Client) RequireFeatures(ctx context.Context, features ...string) error {
	capabilities, err := c.Capabilities(ctx)
	if err != nil {
		return err
	}
	for _, feature := range features {
		if !capabilities.Has(feature) {
			return fmt.Errorf("%w: %s", ErrUnsupportedFeature, feature)
		}
	}
	return nil
}

func identifier(value string) (string, error) {
	if value == "" || strings.ContainsAny(value, "/\\?#\r\n") || value == "." || value == ".." {
		return "", errors.New("invalid envai identifier")
	}
	return url.PathEscape(value), nil
}
