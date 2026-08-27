package session

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/digitalygo/smidja/internal/agent"
)

const (
	EntryTypeSession             = "session"
	EntryTypeMessage             = "message"
	EntryTypeThinkingLevelChange = "thinking_level_change"
	EntryTypeModelChange         = "model_change"
	EntryTypeCompaction          = "compaction"
	EntryTypeBranchSummary       = "branch_summary"
	EntryTypeCustom              = "custom"
	EntryTypeCustomMessage       = "custom_message"
	EntryTypeLabel               = "label"
	EntryTypeSessionInfo         = "session_info"
)

type Header struct {
	Type          string  `json:"type"`
	Version       int     `json:"version"`
	ID            string  `json:"id"`
	Timestamp     string  `json:"timestamp"`
	Cwd           string  `json:"cwd"`
	ParentSession *string `json:"parentSession,omitempty"`
}

func (h *Header) EntryType() string { return h.Type }

type EntryBase struct {
	Type      string  `json:"type"`
	ID        string  `json:"id"`
	ParentID  *string `json:"parentId"`
	Timestamp string  `json:"timestamp"`
}

type Entry interface {
	EntryType() string
}

type MessageEntry struct {
	EntryBase
	Message json.RawMessage `json:"message"`
}

func (*MessageEntry) EntryType() string { return EntryTypeMessage }

func (e *MessageEntry) MessageRole() string {
	var role struct {
		Role string `json:"role"`
	}
	if err := json.Unmarshal(e.Message, &role); err != nil {
		return ""
	}
	return role.Role
}

func (e *MessageEntry) DecodeMessage() (*agent.Message, error) {
	var role struct {
		Role string `json:"role"`
	}
	if err := json.Unmarshal(e.Message, &role); err != nil {
		return nil, fmt.Errorf("session: decode message role: %w", err)
	}
	switch role.Role {
	case string(agent.RoleUser):
		var m agent.UserMessage
		if err := json.Unmarshal(e.Message, &m); err != nil {
			return nil, fmt.Errorf("session: decode user message: %w", err)
		}
		return &agent.Message{User: &m}, nil
	case string(agent.RoleAssistant):
		var m agent.AssistantMessage
		if err := json.Unmarshal(e.Message, &m); err != nil {
			return nil, fmt.Errorf("session: decode assistant message: %w", err)
		}
		return &agent.Message{Assistant: &m}, nil
	case string(agent.RoleToolResult):
		var m agent.ToolResultMessage
		if err := json.Unmarshal(e.Message, &m); err != nil {
			return nil, fmt.Errorf("session: decode tool result message: %w", err)
		}
		return &agent.Message{ToolResult: &m}, nil
	default:
		return nil, fmt.Errorf("session: unsupported message role %q", role.Role)
	}
}

type ThinkingLevelChangeEntry struct {
	EntryBase
	ThinkingLevel string `json:"thinkingLevel"`
}

func (*ThinkingLevelChangeEntry) EntryType() string { return EntryTypeThinkingLevelChange }

type ModelChangeEntry struct {
	EntryBase
	Provider string `json:"provider"`
	ModelID  string `json:"modelId"`
}

func (*ModelChangeEntry) EntryType() string { return EntryTypeModelChange }

type CompactionEntry struct {
	EntryBase
	Summary          string          `json:"summary"`
	FirstKeptEntryID string          `json:"firstKeptEntryId"`
	TokensBefore     int64           `json:"tokensBefore"`
	Details          json.RawMessage `json:"details,omitempty"`
	Usage            *agent.Usage    `json:"usage,omitempty"`
	FromHook         *bool           `json:"fromHook,omitempty"`
}

func (*CompactionEntry) EntryType() string { return EntryTypeCompaction }

type BranchSummaryEntry struct {
	EntryBase
	FromID   string          `json:"fromId"`
	Summary  string          `json:"summary"`
	Details  json.RawMessage `json:"details,omitempty"`
	Usage    *agent.Usage    `json:"usage,omitempty"`
	FromHook *bool           `json:"fromHook,omitempty"`
}

func (*BranchSummaryEntry) EntryType() string { return EntryTypeBranchSummary }

type CustomEntry struct {
	EntryBase
	CustomType string          `json:"customType"`
	Data       json.RawMessage `json:"data,omitempty"`
}

func (*CustomEntry) EntryType() string { return EntryTypeCustom }

type CustomMessageEntry struct {
	EntryBase
	CustomType string          `json:"customType"`
	Content    json.RawMessage `json:"content"`
	Display    bool            `json:"display"`
	Details    json.RawMessage `json:"details,omitempty"`
}

func (*CustomMessageEntry) EntryType() string { return EntryTypeCustomMessage }

type LabelEntry struct {
	EntryBase
	TargetID string  `json:"targetId"`
	Label    *string `json:"label,omitempty"`
}

func (*LabelEntry) EntryType() string { return EntryTypeLabel }

type SessionInfoEntry struct {
	EntryBase
	Name *string `json:"name,omitempty"`
}

func (*SessionInfoEntry) EntryType() string { return EntryTypeSessionInfo }

type OpaqueEntry struct {
	TypeName string
	Raw      json.RawMessage

	id        string
	parentID  *string
	timestamp string
}

func (o *OpaqueEntry) EntryType() string { return o.TypeName }

func (o *OpaqueEntry) MarshalJSON() ([]byte, error) { return o.Raw, nil }

func (o *OpaqueEntry) EnvelopeID() string { return o.id }

func (o *OpaqueEntry) EnvelopeParentID() *string { return o.parentID }

func (o *OpaqueEntry) EnvelopeTimestamp() string { return o.timestamp }

func newOpaqueEntry(typeName string, line []byte) *OpaqueEntry {
	o := &OpaqueEntry{
		TypeName: typeName,
		Raw:      append([]byte(nil), line...),
	}
	var env struct {
		ID        string  `json:"id"`
		ParentID  *string `json:"parentId"`
		Timestamp string  `json:"timestamp"`
	}
	_ = json.Unmarshal(line, &env)
	o.id, o.parentID, o.timestamp = env.ID, env.ParentID, env.Timestamp
	return o
}

func DecodeEntry(line []byte) (Entry, error) {
	var discriminator struct {
		Type json.RawMessage `json:"type"`
	}
	if err := json.Unmarshal(line, &discriminator); err != nil {
		return nil, fmt.Errorf("session: decode entry: %w", err)
	}
	typeName := ""
	if len(discriminator.Type) >= 2 && discriminator.Type[0] == '"' {
		_ = json.Unmarshal(discriminator.Type, &typeName)
	}
	switch typeName {
	case EntryTypeMessage:
		return decodeTyped[*MessageEntry](line)
	case EntryTypeThinkingLevelChange:
		return decodeTyped[*ThinkingLevelChangeEntry](line)
	case EntryTypeModelChange:
		return decodeTyped[*ModelChangeEntry](line)
	case EntryTypeCompaction:
		return decodeTyped[*CompactionEntry](line)
	case EntryTypeBranchSummary:
		return decodeTyped[*BranchSummaryEntry](line)
	case EntryTypeCustom:
		return decodeTyped[*CustomEntry](line)
	case EntryTypeCustomMessage:
		return decodeTyped[*CustomMessageEntry](line)
	case EntryTypeLabel:
		return decodeTyped[*LabelEntry](line)
	case EntryTypeSessionInfo:
		return decodeTyped[*SessionInfoEntry](line)
	default:
		return newOpaqueEntry(typeName, line), nil
	}
}

func decodeTyped[T Entry](line []byte) (Entry, error) {
	var e T
	if err := json.Unmarshal(line, &e); err != nil {
		var zero T
		return newOpaqueEntry(zero.EntryType(), line), nil
	}
	return e, nil
}

func MarshalEntry(e Entry) ([]byte, error) {
	b, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("session: marshal entry: %w", err)
	}
	return b, nil
}

func entryBaseOf(e Entry) (*EntryBase, error) {
	switch v := e.(type) {
	case *MessageEntry:
		return &v.EntryBase, nil
	case *ThinkingLevelChangeEntry:
		return &v.EntryBase, nil
	case *ModelChangeEntry:
		return &v.EntryBase, nil
	case *CompactionEntry:
		return &v.EntryBase, nil
	case *BranchSummaryEntry:
		return &v.EntryBase, nil
	case *CustomEntry:
		return &v.EntryBase, nil
	case *CustomMessageEntry:
		return &v.EntryBase, nil
	case *LabelEntry:
		return &v.EntryBase, nil
	case *SessionInfoEntry:
		return &v.EntryBase, nil
	case *OpaqueEntry:
		return nil, errors.New("session: opaque entries carry no envelope and cannot be appended")
	default:
		return nil, fmt.Errorf("session: unsupported entry type %T", e)
	}
}

func envelopeOf(e Entry) (id string, parentID *string, timestamp string) {
	if o, ok := e.(*OpaqueEntry); ok {
		return o.EnvelopeID(), o.EnvelopeParentID(), o.EnvelopeTimestamp()
	}
	base, err := entryBaseOf(e)
	if err != nil {
		return "", nil, ""
	}
	return base.ID, base.ParentID, base.Timestamp
}
