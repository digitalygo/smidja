package oauth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
)

func decodeJWTPayload(token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("malformed JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, err
	}
	return claims, nil
}

func codexAccountID(accessToken string) (string, error) {
	claims, err := decodeJWTPayload(accessToken)
	if err != nil {
		return "", err
	}
	auth, _ := claims[codexJWTClaimPath].(map[string]any)
	accountID, _ := auth["chatgpt_account_id"].(string)
	if accountID == "" {
		return "", errors.New("failed to extract accountId from token")
	}
	return accountID, nil
}
