package envai

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
)

type signingKey struct {
	KeyID     string `json:"kid"`
	Type      string `json:"kty"`
	Curve     string `json:"crv"`
	Algorithm string `json:"alg"`
	Use       string `json:"use"`
	X         string `json:"x"`
}
type signingKeys struct {
	sync.Mutex
	keys    map[string]ed25519.PublicKey
	expires time.Time
}

func (c *Client) VerifyToolRequest(ctx context.Context, event Event) (ToolRequest, error) {
	request, err := event.ToolRequest()
	if err != nil {
		return request, err
	}
	if request.Kind != "client" || strings.ContainsAny(request.CallID+request.ToolName, "\r\n") {
		return request, errors.New("invalid client tool signature input")
	}
	signature, err := base64.RawURLEncoding.DecodeString(request.Signature.Value)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return request, errors.New("invalid Ed25519 signature encoding")
	}
	canonical, err := jsoncanonicalizer.Transform(request.Args)
	if err != nil {
		return request, err
	}
	key, err := c.signingKey(ctx, request.Signature.KeyID)
	if err != nil {
		return request, err
	}
	payload := []byte(request.CallID + "\n" + request.ToolName + "\n" + string(canonical))
	if !ed25519.Verify(key, payload, signature) {
		return request, errors.New("invalid client tool signature")
	}
	return request, nil
}

func (c *Client) signingKey(ctx context.Context, kid string) (ed25519.PublicKey, error) {
	c.signing.Lock()
	defer c.signing.Unlock()
	if key, exists := c.signing.keys[kid]; exists && time.Now().Before(c.signing.expires) {
		return key, nil
	}
	var document struct {
		Keys []signingKey `json:"keys"`
	}
	if err := c.json(ctx, http.MethodGet, "/.well-known/jwks.json", nil, &document, false); err != nil {
		return nil, err
	}
	keys := make(map[string]ed25519.PublicKey)
	for _, entry := range document.Keys {
		if entry.KeyID == "" || entry.Type != "OKP" || entry.Curve != "Ed25519" || entry.Algorithm != "EdDSA" || (entry.Use != "" && entry.Use != "sig") {
			continue
		}
		raw, err := base64.RawURLEncoding.DecodeString(entry.X)
		if err != nil || len(raw) != ed25519.PublicKeySize {
			continue
		}
		if _, exists := keys[entry.KeyID]; exists {
			return nil, errors.New("ambiguous signing key ID")
		}
		keys[entry.KeyID] = ed25519.PublicKey(raw)
	}
	c.signing.keys = keys
	c.signing.expires = time.Now().Add(5 * time.Minute)
	key, exists := keys[kid]
	if !exists {
		return nil, errors.New("client tool signing key unavailable")
	}
	return key, nil
}

type DetachedEvent struct {
	TaskID       string          `json:"task_id"`
	OriginTurnID string          `json:"origin_turn_id"`
	EventType    string          `json:"event_type"`
	EventPayload json.RawMessage `json:"event_payload"`
}

func (c *Client) VerifyDetachedToolRequest(ctx context.Context, event Event, expectedTaskID, expectedOriginTurnID string) (ToolRequest, error) {
	var request ToolRequest
	if event.Type != "task.event" || event.TurnID != "" || expectedTaskID == "" || expectedOriginTurnID == "" {
		return request, errors.New("invalid detached event binding")
	}
	var payload DetachedEvent
	if err := json.Unmarshal(event.Data, &payload); err != nil {
		return request, err
	}
	if payload.TaskID != expectedTaskID || payload.OriginTurnID != expectedOriginTurnID || payload.EventType != "agent.tool.request" {
		return request, errors.New("detached tool event does not match tracked task")
	}
	normalized := event
	normalized.Type = payload.EventType
	normalized.Data = payload.EventPayload
	normalized.TurnID = payload.OriginTurnID
	return c.VerifyToolRequest(ctx, normalized)
}
