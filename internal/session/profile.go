package session

import (
	"encoding/json"
	"errors"
	"fmt"
)

const RuntimeProfileCustomType = "smidja.runtime.profile"

type CurrentProfile struct {
	ProviderID                     string
	ModelID                        string
	SystemPromptSHA256             string
	ToolSchemasCanonicalJSONSHA256 string
	OrderingVersion                int
	ContentFingerprint             string
	AffinityKey                    string
}

type EstimatorAnchor struct {
	LastInputTokens int64  `json:"lastInputTokens"`
	LeafID          string `json:"leafID"`
}

type RuntimeProfile struct {
	ProviderID                     string          `json:"providerID"`
	ModelID                        string          `json:"modelID"`
	SystemPromptSHA256             string          `json:"systemPromptSHA256"`
	ToolSchemasCanonicalJSONSHA256 string          `json:"toolSchemasCanonicalJSONSHA256"`
	OrderingVersion                int             `json:"orderingVersion"`
	ContentFingerprint             string          `json:"contentFingerprint"`
	AffinityKey                    string          `json:"affinityKey"`
	EstimatorAnchor                EstimatorAnchor `json:"estimatorAnchor"`
}

func (p RuntimeProfile) Matches(cur CurrentProfile) bool {
	return p.ProviderID == cur.ProviderID &&
		p.ModelID == cur.ModelID &&
		p.SystemPromptSHA256 == cur.SystemPromptSHA256 &&
		p.ToolSchemasCanonicalJSONSHA256 == cur.ToolSchemasCanonicalJSONSHA256 &&
		p.OrderingVersion == cur.OrderingVersion &&
		p.ContentFingerprint == cur.ContentFingerprint &&
		p.AffinityKey == cur.AffinityKey
}

func (p RuntimeProfile) firstMismatchField(cur CurrentProfile) string {
	switch {
	case p.ProviderID != cur.ProviderID:
		return "providerID"
	case p.ModelID != cur.ModelID:
		return "modelID"
	case p.SystemPromptSHA256 != cur.SystemPromptSHA256:
		return "systemPromptSHA256"
	case p.ToolSchemasCanonicalJSONSHA256 != cur.ToolSchemasCanonicalJSONSHA256:
		return "toolSchemasCanonicalJSONSHA256"
	case p.OrderingVersion != cur.OrderingVersion:
		return "orderingVersion"
	case p.ContentFingerprint != cur.ContentFingerprint:
		return "contentFingerprint"
	case p.AffinityKey != cur.AffinityKey:
		return "affinityKey"
	}
	return ""
}

type ProfileMismatchError struct {
	Field string
}

func (e *ProfileMismatchError) Error() string {
	return "session: runtime profile mismatch on field " + e.Field
}

func (sess *Session) PersistRuntimeProfile(cur CurrentProfile, contentFingerprint func() string) (RuntimeProfile, error) {
	cur = effectiveProfile(cur, contentFingerprint)
	sess.mu.Lock()
	existing := sess.profile
	sess.mu.Unlock()
	if existing != nil {
		if existing.Matches(cur) {
			return *existing, nil
		}
		return *existing, &ProfileMismatchError{Field: existing.firstMismatchField(cur)}
	}
	p := RuntimeProfile{
		ProviderID:                     cur.ProviderID,
		ModelID:                        cur.ModelID,
		SystemPromptSHA256:             cur.SystemPromptSHA256,
		ToolSchemasCanonicalJSONSHA256: cur.ToolSchemasCanonicalJSONSHA256,
		OrderingVersion:                cur.OrderingVersion,
		ContentFingerprint:             cur.ContentFingerprint,
		AffinityKey:                    cur.AffinityKey,
	}
	if err := sess.appendProfile(p); err != nil {
		return RuntimeProfile{}, err
	}
	return p, nil
}

func (sess *Session) ResetRuntimeProfile(cur CurrentProfile, contentFingerprint func() string) (RuntimeProfile, error) {
	cur = effectiveProfile(cur, contentFingerprint)
	p := RuntimeProfile{
		ProviderID:                     cur.ProviderID,
		ModelID:                        cur.ModelID,
		SystemPromptSHA256:             cur.SystemPromptSHA256,
		ToolSchemasCanonicalJSONSHA256: cur.ToolSchemasCanonicalJSONSHA256,
		OrderingVersion:                cur.OrderingVersion,
		ContentFingerprint:             cur.ContentFingerprint,
		AffinityKey:                    cur.AffinityKey,
	}
	if err := sess.appendProfile(p); err != nil {
		return RuntimeProfile{}, err
	}
	return p, nil
}

func effectiveProfile(cur CurrentProfile, contentFingerprint func() string) CurrentProfile {
	if contentFingerprint != nil {
		cur.ContentFingerprint = contentFingerprint()
	}
	return cur
}

func (sess *Session) UpdateProfileAnchor(lastInput int64, leafID string) error {
	sess.mu.Lock()
	if sess.profile == nil {
		sess.mu.Unlock()
		return errors.New("session: update runtime profile anchor: no profile persisted")
	}
	updated := *sess.profile
	updated.EstimatorAnchor = EstimatorAnchor{LastInputTokens: lastInput, LeafID: leafID}
	same := updated.EstimatorAnchor == sess.profile.EstimatorAnchor
	sess.mu.Unlock()
	if same {
		return nil
	}
	return sess.appendProfile(updated)
}

func (sess *Session) RuntimeProfile() (*RuntimeProfile, bool) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.profile == nil {
		return nil, false
	}
	p := *sess.profile
	return &p, true
}

func (sess *Session) appendProfile(p RuntimeProfile) error {
	data, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("session: marshal runtime profile: %w", err)
	}
	if err := sess.AppendEntry(&CustomEntry{CustomType: RuntimeProfileCustomType, Data: data}); err != nil {
		return err
	}
	sess.mu.Lock()
	cp := p
	sess.profile = &cp
	sess.mu.Unlock()
	return nil
}

func findRuntimeProfile(entries []Entry) *RuntimeProfile {
	var found *RuntimeProfile
	for _, e := range entries {
		ce, ok := e.(*CustomEntry)
		if !ok || ce.CustomType != RuntimeProfileCustomType || len(ce.Data) == 0 {
			continue
		}
		var p RuntimeProfile
		if err := json.Unmarshal(ce.Data, &p); err != nil {
			continue
		}
		cp := p
		found = &cp
	}
	return found
}
