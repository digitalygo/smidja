package oauth

import (
	"encoding/json"
	"testing"
)

func TestNewEntryPreservesExtras(t *testing.T) {
	entry, err := newEntry("access-1", "refresh-1", 12345, map[string]string{"accountId": "acc-77"})
	if err != nil {
		t.Fatalf("newEntry: %v", err)
	}
	if entry.Type != "oauth" || entry.Access != "access-1" || entry.Refresh != "refresh-1" || entry.Expires != 12345 {
		t.Errorf("entry = %+v", entry)
	}
	body, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed["accountId"] != "acc-77" {
		t.Errorf("unknown field lost: %s", body)
	}
	if parsed["type"] != "oauth" {
		t.Errorf("type = %v", parsed["type"])
	}
}

func TestNewEntryOpenRouterShape(t *testing.T) {
	entry, err := newEntry("sk-or-v1-key", "", expiresMaxSafeInteger, nil)
	if err != nil {
		t.Fatalf("newEntry: %v", err)
	}
	if entry.Refresh != "" {
		t.Errorf("refresh = %q, want empty", entry.Refresh)
	}
	if entry.Expires != expiresMaxSafeInteger {
		t.Errorf("expires = %d", entry.Expires)
	}
}

func TestParseAuthorizationInput(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantCode  string
		wantState string
		wantOK    bool
	}{
		{"redirect url", "http://localhost:53692/callback?code=c1&state=s1", "c1", "s1", true},
		{"url without state", "http://localhost:53692/callback?code=c1", "c1", "", true},
		{"hash split", "c1#s1", "c1", "s1", true},
		{"query fragment", "code=c2&state=s2", "c2", "s2", true},
		{"bare code", "raw-code-1", "raw-code-1", "", true},
		{"empty", "", "", "", false},
		{"whitespace", "   ", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, state, ok := parseAuthorizationInput(tc.input)
			if ok != tc.wantOK || code != tc.wantCode || state != tc.wantState {
				t.Errorf("parse(%q) = (%q, %q, %v), want (%q, %q, %v)", tc.input, code, state, ok, tc.wantCode, tc.wantState, tc.wantOK)
			}
		})
	}
}
