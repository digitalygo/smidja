package session

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/digitalygo/smidja/internal/agent"
)

// Entry type discriminators for the Pi v3 session format. The constants
// are the exact JSON "type" values Pi writes; keep them in sync with
// dist/core/session-manager.d.ts.
const (
	// EntryTypeSession is the first line of every session file.
	EntryTypeSession = "session"
	// EntryTypeMessage is a user, assistant, or toolResult message.
	EntryTypeMessage = "message"
	// EntryTypeThinkingLevelChange records a thinking-level switch.
	EntryTypeThinkingLevelChange = "thinking_level_change"
	// EntryTypeModelChange records a model switch.
	EntryTypeModelChange = "model_change"
	// EntryTypeCompaction records a compaction summary.
	EntryTypeCompaction = "compaction"
	// EntryTypeBranchSummary records a summary of an abandoned branch.
	EntryTypeBranchSummary = "branch_summary"
	// EntryTypeCustom is an extension state entry (not part of LLM context).
	EntryTypeCustom = "custom"
	// EntryTypeCustomMessage is an extension message that participates in
	// LLM context.
	EntryTypeCustomMessage = "custom_message"
	// EntryTypeLabel is a user bookmark on another entry.
	EntryTypeLabel = "label"
	// EntryTypeSessionInfo is session metadata (for example a display name).
	EntryTypeSessionInfo = "session_info"
)

// Header is the first line of every v3 session file, aligned with Pi's
// SessionHeader: type, format version, the UUIDv7 session id, the ISO
// header timestamp, the working directory, and an optional parent session
// path for forked sessions.
type Header struct {
	Type          string  `json:"type"`
	Version       int     `json:"version"`
	ID            string  `json:"id"`
	Timestamp     string  `json:"timestamp"`
	Cwd           string  `json:"cwd"`
	ParentSession *string `json:"parentSession,omitempty"`
}

// EntryBase is the envelope shared by every session entry: the type
// discriminator, the entry id, the parent id (null for the first entry of
// a branch), and the ISO timestamp. It mirrors Pi's SessionEntryBase.
type EntryBase struct {
	Type      string  `json:"type"`
	ID        string  `json:"id"`
	ParentID  *string `json:"parentId"`
	Timestamp string  `json:"timestamp"`
}

// Entry is one typed session entry: a payload wrapped in the shared
// envelope. The concrete payload types are MessageEntry,
// ThinkingLevelChangeEntry, ModelChangeEntry, CompactionEntry,
// BranchSummaryEntry, CustomEntry, CustomMessageEntry, LabelEntry,
// SessionInfoEntry, and OpaqueEntry (for types the codec does not know).
type Entry interface {
	// EntryType returns the JSON "type" discriminator of the entry.
	EntryType() string
}

// MessageEntry is a user, assistant, or toolResult message. The message
// payload is kept as raw JSON so it round-trips byte-exact; use
// DecodeMessage to project it into the agent message types.
type MessageEntry struct {
	EntryBase
	Message json.RawMessage `json:"message"`
}

// EntryType returns "message".
func (*MessageEntry) EntryType() string { return EntryTypeMessage }

// MessageRole returns the "role" of the message payload: "user",
// "assistant", "toolResult", or any other role Pi sessions carry (for
// example "custom"). It returns "" when the payload is not a JSON
// object or has no string role.
func (e *MessageEntry) MessageRole() string {
	var role struct {
		Role string `json:"role"`
	}
	if err := json.Unmarshal(e.Message, &role); err != nil {
		return ""
	}
	return role.Role
}

// DecodeMessage unmarshals the message payload into the agent.Message
// variant matching its role: "user", "assistant", or "toolResult". Pi
// sessions can also carry other roles (for example "custom" messages
// injected by extensions); those payloads stay available as raw JSON on
// the entry but cannot be decoded into the agent union and return an
// error.
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

// ThinkingLevelChangeEntry records a switch of the thinking level.
type ThinkingLevelChangeEntry struct {
	EntryBase
	ThinkingLevel string `json:"thinkingLevel"`
}

// EntryType returns "thinking_level_change".
func (*ThinkingLevelChangeEntry) EntryType() string { return EntryTypeThinkingLevelChange }

// ModelChangeEntry records a switch of the active model.
type ModelChangeEntry struct {
	EntryBase
	Provider string `json:"provider"`
	ModelID  string `json:"modelId"`
}

// EntryType returns "model_change".
func (*ModelChangeEntry) EntryType() string { return EntryTypeModelChange }

// CompactionEntry records a compaction: the summary of the summarized
// prefix, the id of the first kept entry, the token count before
// compaction, and optional extension details, usage accounting, and a
// fromHook flag. Fields match Pi's CompactionEntry.
type CompactionEntry struct {
	EntryBase
	Summary          string          `json:"summary"`
	FirstKeptEntryID string          `json:"firstKeptEntryId"`
	TokensBefore     int64           `json:"tokensBefore"`
	Details          json.RawMessage `json:"details,omitempty"`
	Usage            *agent.Usage    `json:"usage,omitempty"`
	FromHook         *bool           `json:"fromHook,omitempty"`
}

// EntryType returns "compaction".
func (*CompactionEntry) EntryType() string { return EntryTypeCompaction }

// BranchSummaryEntry records a summary of an abandoned conversation path
// when branching with a summary.
type BranchSummaryEntry struct {
	EntryBase
	FromID   string          `json:"fromId"`
	Summary  string          `json:"summary"`
	Details  json.RawMessage `json:"details,omitempty"`
	Usage    *agent.Usage    `json:"usage,omitempty"`
	FromHook *bool           `json:"fromHook,omitempty"`
}

// EntryType returns "branch_summary".
func (*BranchSummaryEntry) EntryType() string { return EntryTypeBranchSummary }

// CustomEntry is extension state: customType identifies the extension and
// data is extension-specific. Custom entries do not participate in LLM
// context; use CustomMessageEntry for content that should.
type CustomEntry struct {
	EntryBase
	CustomType string          `json:"customType"`
	Data       json.RawMessage `json:"data,omitempty"`
}

// EntryType returns "custom".
func (*CustomEntry) EntryType() string { return EntryTypeCustom }

// CustomMessageEntry is an extension message that participates in LLM
// context: content is a string or a content-block array, display controls
// TUI rendering, and details is extension-specific metadata.
type CustomMessageEntry struct {
	EntryBase
	CustomType string          `json:"customType"`
	Content    json.RawMessage `json:"content"`
	Display    bool            `json:"display"`
	Details    json.RawMessage `json:"details,omitempty"`
}

// EntryType returns "custom_message".
func (*CustomMessageEntry) EntryType() string { return EntryTypeCustomMessage }

// LabelEntry bookmarks another entry: targetId names the bookmarked
// entry and Label is the marker text, or nil to clear it.
type LabelEntry struct {
	EntryBase
	TargetID string  `json:"targetId"`
	Label    *string `json:"label,omitempty"`
}

// EntryType returns "label".
func (*LabelEntry) EntryType() string { return EntryTypeLabel }

// SessionInfoEntry is session metadata, for example a user-defined
// display name.
type SessionInfoEntry struct {
	EntryBase
	Name *string `json:"name,omitempty"`
}

// EntryType returns "session_info".
func (*SessionInfoEntry) EntryType() string { return EntryTypeSessionInfo }

// OpaqueEntry preserves an entry whose type the codec does not
// understand, byte-exact: TypeName carries the unknown "type" value and
// Raw the exact line bytes. Re-serializing an OpaqueEntry reproduces the
// original bytes unchanged, so future Pi entry types survive a
// decode/encode cycle. The envelope accessors parse the id, parentId, and
// timestamp from the raw payload so opaque entries still participate in
// tree and branch reconstruction.
type OpaqueEntry struct {
	TypeName string
	Raw      json.RawMessage

	id        string
	parentID  *string
	timestamp string
}

// EntryType returns the unknown type name.
func (o *OpaqueEntry) EntryType() string { return o.TypeName }

// MarshalJSON emits the original raw bytes verbatim.
func (o *OpaqueEntry) MarshalJSON() ([]byte, error) { return o.Raw, nil }

// EnvelopeID returns the entry id parsed from the raw payload.
func (o *OpaqueEntry) EnvelopeID() string { return o.id }

// EnvelopeParentID returns the parentId parsed from the raw payload.
func (o *OpaqueEntry) EnvelopeParentID() *string { return o.parentID }

// EnvelopeTimestamp returns the timestamp parsed from the raw payload.
func (o *OpaqueEntry) EnvelopeTimestamp() string { return o.timestamp }

// newOpaqueEntry builds an OpaqueEntry from one raw line, extracting the
// envelope fields best-effort (a line that is valid JSON always has them
// parseable when present; missing fields decode as zero values).
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

// DecodeEntry parses one raw JSON line into the typed entry matching its
// "type" discriminator. Known types decode into their typed payload;
// unknown types (including future Pi entry types) decode as OpaqueEntry
// carrying the exact raw bytes. Lines that are not valid JSON objects
// return an error; callers skip them like Pi's parseSessionEntries.
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

// decodeTyped decodes one line into the typed payload T. When the payload
// does not match T's shape (for example a hand-edited entry with a field
// of the wrong JSON type), the line falls back to OpaqueEntry so the
// bytes are never lost.
func decodeTyped[T Entry](line []byte) (Entry, error) {
	var e T
	if err := json.Unmarshal(line, &e); err != nil {
		var zero T
		return newOpaqueEntry(zero.EntryType(), line), nil
	}
	return e, nil
}

// MarshalEntry serializes one entry to its JSON line (without a trailing
// newline). Opaque entries re-emit their original bytes verbatim.
func MarshalEntry(e Entry) ([]byte, error) {
	b, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("session: marshal entry: %w", err)
	}
	return b, nil
}

// entryBaseOf returns the embedded envelope of one of the nine known
// entry types, so writers can assign id, parentId, and timestamp. Opaque
// entries carry no envelope and are rejected here.
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

// envelopeOf returns the id, parent id, and timestamp of any entry,
// including opaque ones, for tree and branch reconstruction.
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
