package session

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
)

func baseCurrentProfile() CurrentProfile {
	return CurrentProfile{
		ProviderID:                     "openrouter",
		ModelID:                        "deepseek/deepseek-v4-pro-0813",
		SystemPromptSHA256:             "sp-abc",
		ToolSchemasCanonicalJSONSHA256: "ts-abc",
		OrderingVersion:                3,
		AffinityKey:                    "workspace:/tmp/proj",
	}
}

func TestRuntimeProfilePersistSingleEntry(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := st.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cur := baseCurrentProfile()
	p, err := sess.PersistRuntimeProfile(cur, func() string { return "fp-1" })
	if err != nil {
		t.Fatal(err)
	}
	if p.ContentFingerprint != "fp-1" {
		t.Errorf("ContentFingerprint = %q, want fp-1", p.ContentFingerprint)
	}
	effective := cur
	effective.ContentFingerprint = "fp-1"
	if !p.Matches(effective) {
		t.Errorf("persisted profile must match the effective current profile")
	}

	same, err := sess.PersistRuntimeProfile(cur, func() string { return "fp-1" })
	if err != nil {
		t.Fatalf("idempotent persist: %v", err)
	}
	if same.ContentFingerprint != "fp-1" {
		t.Errorf("idempotent persist returned %+v", same)
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}

	lines := readLines(t, sess.Path())
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2 (header + one profile entry)", len(lines))
	}
	e := parseObj(t, lines[1])
	assertKeys(t, e, "type", "id", "parentId", "timestamp", "customType", "data")
	if e["type"] != "custom" || e["customType"] != RuntimeProfileCustomType {
		t.Errorf("profile entry = %v, want custom type %q", e, RuntimeProfileCustomType)
	}
	if e["parentId"] != nil {
		t.Errorf("profile entry parentId = %v, want null (first entry)", e["parentId"])
	}
	data := e["data"].(map[string]any)
	assertKeys(t, data, "providerID", "modelID", "systemPromptSHA256", "toolSchemasCanonicalJSONSHA256",
		"orderingVersion", "contentFingerprint", "affinityKey", "estimatorAnchor")
	if data["providerID"] != "openrouter" || data["modelID"] != "deepseek/deepseek-v4-pro-0813" ||
		data["orderingVersion"] != float64(3) || data["contentFingerprint"] != "fp-1" ||
		data["affinityKey"] != "workspace:/tmp/proj" {
		t.Errorf("profile data = %v", data)
	}
	anchor := data["estimatorAnchor"].(map[string]any)
	assertKeys(t, anchor, "lastInputTokens", "leafID")
	if anchor["lastInputTokens"] != float64(0) || anchor["leafID"] != "" {
		t.Errorf("initial anchor = %v, want zeros", anchor)
	}
}

func TestRuntimeProfileReadAfterReopen(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cur := baseCurrentProfile()
	sess, err := st.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.PersistRuntimeProfile(cur, func() string { return "fp-1" }); err != nil {
		t.Fatal(err)
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}

	re, err := st.Open(sess.Path(), OpenOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := re.RuntimeProfile()
	if !ok {
		t.Fatal("RuntimeProfile() = not present after reopen")
	}
	if got.ContentFingerprint != "fp-1" || got.ProviderID != "openrouter" ||
		got.OrderingVersion != 3 {
		t.Errorf("reopened profile = %+v", got)
	}
	if err := re.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeProfileUpdateAnchor(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cur := baseCurrentProfile()
	sess, err := st.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.PersistRuntimeProfile(cur, func() string { return "fp-1" }); err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"x"`), Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	if err := sess.UpdateProfileAnchor(4321, "leaf-abc"); err != nil {
		t.Fatal(err)
	}
	if err := sess.UpdateProfileAnchor(4321, "leaf-abc"); err != nil {
		t.Fatalf("idempotent anchor update: %v", err)
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}

	re, err := st.Open(sess.Path(), OpenOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := re.RuntimeProfile()
	if !ok {
		t.Fatal("no profile after reopen")
	}
	if got.EstimatorAnchor.LastInputTokens != 4321 || got.EstimatorAnchor.LeafID != "leaf-abc" {
		t.Errorf("anchor = %+v, want {4321 leaf-abc}", got.EstimatorAnchor)
	}
	if got.ModelID != cur.ModelID {
		t.Errorf("anchor update must not change identity: %+v", got)
	}
	re.Close()
}

func TestRuntimeProfileMismatchAllFields(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*CurrentProfile)
	}{
		{"providerID", func(c *CurrentProfile) { c.ProviderID = "other" }},
		{"modelID", func(c *CurrentProfile) { c.ModelID = "other" }},
		{"systemPromptSHA256", func(c *CurrentProfile) { c.SystemPromptSHA256 = "other" }},
		{"toolSchemasCanonicalJSONSHA256", func(c *CurrentProfile) { c.ToolSchemasCanonicalJSONSHA256 = "other" }},
		{"orderingVersion", func(c *CurrentProfile) { c.OrderingVersion = 99 }},
		{"contentFingerprint", func(c *CurrentProfile) { c.ContentFingerprint = "fp-2" }},
		{"affinityKey", func(c *CurrentProfile) { c.AffinityKey = "other" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, err := NewStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			cur := baseCurrentProfile()
			sess, err := st.Create(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := sess.PersistRuntimeProfile(cur, func() string { return "fp-1" }); err != nil {
				t.Fatal(err)
			}
			if err := sess.Close(); err != nil {
				t.Fatal(err)
			}

			re, err := st.Open(sess.Path(), OpenOptions{Strict: true})
			if err != nil {
				t.Fatal(err)
			}
			changed := cur
			changed.ContentFingerprint = "fp-1"
			tc.mutate(&changed)
			var supplier func() string
			if tc.name != "contentFingerprint" {
				supplier = func() string { return "fp-1" }
			}
			_, err = re.PersistRuntimeProfile(changed, supplier)
			var mismatch *ProfileMismatchError
			if !errors.As(err, &mismatch) {
				t.Fatalf("err = %v, want ProfileMismatchError", err)
			}
			if mismatch.Field != tc.name {
				t.Errorf("mismatch field = %q, want %q", mismatch.Field, tc.name)
			}
			re.Close()
		})
	}
}

func TestRuntimeProfileReset(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cur := baseCurrentProfile()
	sess, err := st.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.PersistRuntimeProfile(cur, func() string { return "fp-1" }); err != nil {
		t.Fatal(err)
	}
	sess.Close()

	re, err := st.Open(sess.Path(), OpenOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	fresh := CurrentProfile{
		ProviderID: "anthropic", ModelID: "claude-opus-4",
		SystemPromptSHA256: "sp-new", ToolSchemasCanonicalJSONSHA256: "ts-new",
		OrderingVersion: 4, AffinityKey: "workspace:/tmp/new",
	}
	reset, err := re.ResetRuntimeProfile(fresh, func() string { return "fp-new" })
	if err != nil {
		t.Fatal(err)
	}
	if reset.ContentFingerprint != "fp-new" || reset.OrderingVersion != 4 {
		t.Errorf("reset profile = %+v", reset)
	}
	if err := re.Close(); err != nil {
		t.Fatal(err)
	}

	re2, err := st.Open(sess.Path(), OpenOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := re2.RuntimeProfile()
	if !ok {
		t.Fatal("no profile after reset")
	}
	if got.ModelID != "claude-opus-4" || got.EstimatorAnchor.LastInputTokens != 0 {
		t.Errorf("post-reset profile = %+v, want new identity with zero anchor", got)
	}
	re2.Close()
}

func TestRuntimeProfileAnchorWithoutProfile(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := st.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.UpdateProfileAnchor(1, "x"); err == nil {
		t.Fatal("UpdateProfileAnchor without profile: want error")
	}
	if _, ok := sess.RuntimeProfile(); ok {
		t.Fatal("RuntimeProfile() on session without profile: want not present")
	}
	sess.Close()
}

func TestRuntimeProfileFingerprintSupplierFallback(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := st.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cur := baseCurrentProfile()
	cur.ContentFingerprint = "fp-direct"
	p, err := sess.PersistRuntimeProfile(cur, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.ContentFingerprint != "fp-direct" {
		t.Errorf("fingerprint = %q, want fp-direct when supplier is nil", p.ContentFingerprint)
	}
	if !p.Matches(cur) {
		t.Error("nil supplier profile must match its current profile")
	}
	sess.Close()
}

func TestRuntimeProfileMismatchErrorText(t *testing.T) {
	err := &ProfileMismatchError{Field: "providerID"}
	if !strings.Contains(err.Error(), "providerID") {
		t.Errorf("error text %q must name the field", err)
	}
}
