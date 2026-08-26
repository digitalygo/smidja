package authstore

import (
	"path/filepath"
	"testing"
)

func TestResolveCredentialEnvPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := s.Set("openrouter", Entry{Type: "api_key", Key: "sk-store"}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	key, ok := ResolveCredential("openrouter", "OPENROUTER_API_KEY", s, func(string) string {
		return "sk-env"
	})
	if !ok || key != "sk-env" {
		t.Errorf("ResolveCredential = %q, %v; want env key", key, ok)
	}

	key, ok = ResolveCredential("openrouter", "OPENROUTER_API_KEY", s, func(string) string {
		return ""
	})
	if !ok || key != "sk-store" {
		t.Errorf("ResolveCredential = %q, %v; want store key", key, ok)
	}

	key, ok = ResolveCredential("openrouter", "OPENROUTER_API_KEY", s, nil)
	if !ok || key != "sk-store" {
		t.Errorf("ResolveCredential(nil env) = %q, %v; want store key", key, ok)
	}
}

func TestResolveCredentialMissingSources(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if key, ok := ResolveCredential("openrouter", "OPENROUTER_API_KEY", s, func(string) string {
		return ""
	}); ok || key != "" {
		t.Errorf("empty store and env: got %q, %v; want no credential", key, ok)
	}

	if err := s.Set("oauth-provider", Entry{Type: "oauth", Access: "a", Refresh: "r", Expires: 1}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if key, ok := ResolveCredential("oauth-provider", "SOME_ENV", s, func(string) string {
		return ""
	}); ok || key != "" {
		t.Errorf("entry without key: got %q, %v; want no credential", key, ok)
	}

	if key, ok := ResolveCredential("openrouter", "OPENROUTER_API_KEY", nil, func(string) string {
		return ""
	}); ok || key != "" {
		t.Errorf("nil store: got %q, %v; want no credential", key, ok)
	}
}
