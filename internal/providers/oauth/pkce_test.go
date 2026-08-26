package oauth

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
)

func TestGeneratePKCE(t *testing.T) {
	verifier1, challenge1, err := generatePKCE()
	if err != nil {
		t.Fatalf("generatePKCE: %v", err)
	}
	verifier2, challenge2, err := generatePKCE()
	if err != nil {
		t.Fatalf("generatePKCE: %v", err)
	}
	if verifier1 == verifier2 {
		t.Error("verifiers must be random")
	}
	if len(verifier1) != 43 {
		t.Errorf("verifier length = %d, want 43", len(verifier1))
	}
	for _, r := range verifier1 {
		if !strings.ContainsRune("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_", r) {
			t.Errorf("verifier contains invalid character %q", r)
		}
	}
	raw, err := base64.RawURLEncoding.DecodeString(verifier1)
	if err != nil {
		t.Fatalf("verifier is not base64url: %v", err)
	}
	if len(raw) != 32 {
		t.Errorf("verifier entropy = %d bytes, want 32", len(raw))
	}
	sum := sha256.Sum256([]byte(verifier1))
	if challenge1 != base64.RawURLEncoding.EncodeToString(sum[:]) {
		t.Error("challenge is not base64url(sha256(verifier))")
	}
	if challenge1 == challenge2 {
		t.Error("challenges must be random")
	}
}

func TestRandomHex(t *testing.T) {
	value1, err := randomHex(16)
	if err != nil {
		t.Fatalf("randomHex: %v", err)
	}
	value2, _ := randomHex(16)
	if len(value1) != 32 {
		t.Errorf("length = %d, want 32", len(value1))
	}
	if value1 == value2 {
		t.Error("hex values must be random")
	}
}

func TestRandomUUID(t *testing.T) {
	value, err := randomUUID()
	if err != nil {
		t.Fatalf("randomUUID: %v", err)
	}
	if len(value) != 36 {
		t.Errorf("uuid length = %d, want 36", len(value))
	}
	if value[14] != '4' {
		t.Errorf("uuid version = %q, want 4", value[14])
	}
	if value[19] != '8' && value[19] != '9' && value[19] != 'a' && value[19] != 'b' {
		t.Errorf("uuid variant = %q, want 8/9/a/b", value[19])
	}
}
