package oauth

import (
	"strings"
	"testing"
)

func TestDecodeJWTPayload(t *testing.T) {
	token := buildJWT(`{"sub":"s1","https://api.openai.com/auth":{"chatgpt_account_id":"acc-9"}}`)
	claims, err := decodeJWTPayload(token)
	if err != nil {
		t.Fatalf("decodeJWTPayload: %v", err)
	}
	if claims["sub"] != "s1" {
		t.Errorf("sub = %v", claims["sub"])
	}
}

func TestDecodeJWTPayloadMalformed(t *testing.T) {
	cases := []string{"", "one", "a.b", "a.b.c.d", "a.%%%.c"}
	for _, token := range cases {
		if _, err := decodeJWTPayload(token); err == nil {
			t.Errorf("decodeJWTPayload(%q): want error", token)
		}
	}
}

func TestCodexAccountID(t *testing.T) {
	token := buildJWT(`{"https://api.openai.com/auth":{"chatgpt_account_id":"acc-42"}}`)
	accountID, err := codexAccountID(token)
	if err != nil {
		t.Fatalf("codexAccountID: %v", err)
	}
	if accountID != "acc-42" {
		t.Errorf("accountID = %q", accountID)
	}
}

func TestCodexAccountIDMissing(t *testing.T) {
	token := buildJWT(`{"sub":"s1"}`)
	_, err := codexAccountID(token)
	if err == nil || !strings.Contains(err.Error(), "accountId") {
		t.Errorf("err = %v, want accountId error", err)
	}
}

func TestCodexAccountIDInvalidToken(t *testing.T) {
	if _, err := codexAccountID("not-a-jwt"); err == nil {
		t.Error("want error for non-JWT token")
	}
}
