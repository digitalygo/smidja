package cli

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/session"
)

var supportedForkEntryTypes = map[string]bool{
	session.EntryTypeMessage:             true,
	session.EntryTypeThinkingLevelChange: true,
	session.EntryTypeModelChange:         true,
	session.EntryTypeCompaction:          true,
	session.EntryTypeBranchSummary:       true,
	session.EntryTypeCustom:              true,
	session.EntryTypeCustomMessage:       true,
	session.EntryTypeLabel:               true,
	session.EntryTypeSessionInfo:         true,
}

func forkPrefixEntries(loader *session.Loader, targetID string) ([]session.Entry, error) {
	if loader == nil {
		return nil, errors.New("fork: no active session")
	}
	if targetID == "" {
		if leaf := loader.Leaf(); leaf != nil {
			targetID = session.EntryID(leaf)
		}
	}
	if targetID == "" {
		return nil, errors.New("fork: the active session has no entries")
	}
	branch, err := forkAncestry(loader, targetID)
	if err != nil {
		return nil, err
	}
	if err := validateForkEntries(branch); err != nil {
		return nil, err
	}
	return branch, nil
}

func forkAncestry(loader *session.Loader, targetID string) ([]session.Entry, error) {
	current, ok := loader.Get(targetID)
	if !ok {
		return nil, fmt.Errorf("fork: entry %q not found", targetID)
	}
	seen := make(map[string]bool)
	var reversed []session.Entry
	for current != nil {
		id, parentID, _ := sessionEntryEnvelope(current)
		if id == "" {
			return nil, errors.New("fork: an ancestor entry has no id")
		}
		if seen[id] {
			return nil, fmt.Errorf("fork: parent cycle at entry %q", id)
		}
		seen[id] = true
		reversed = append(reversed, current)
		if parentID == nil {
			break
		}
		if *parentID == id {
			return nil, fmt.Errorf("fork: entry %q is its own parent", id)
		}
		parent, ok := loader.Get(*parentID)
		if !ok {
			return nil, fmt.Errorf("fork: entry %q is missing its parent %q", id, *parentID)
		}
		current = parent
	}
	branch := make([]session.Entry, 0, len(reversed))
	for i := len(reversed) - 1; i >= 0; i-- {
		branch = append(branch, reversed[i])
	}
	return branch, nil
}

func validateForkEntries(branch []session.Entry) error {
	for _, entry := range branch {
		if _, ok := entry.(*session.OpaqueEntry); ok {
			return fmt.Errorf("fork: entry %s has unsupported type %s", session.EntryID(entry), entry.EntryType())
		}
		if !supportedForkEntryTypes[entry.EntryType()] {
			return fmt.Errorf("fork: entry %s has unsupported type %s", session.EntryID(entry), entry.EntryType())
		}
	}
	if err := validateForkReferences(branch); err != nil {
		return err
	}
	return validateForkToolSequence(branch)
}

func validateForkReferences(branch []session.Entry) error {
	index := make(map[string]int, len(branch))
	for i, entry := range branch {
		id := session.EntryID(entry)
		if id == "" {
			return errors.New("fork: an entry without an id cannot be retained")
		}
		if _, exists := index[id]; exists {
			return fmt.Errorf("fork: duplicate entry id %q", id)
		}
		index[id] = i
	}
	for i, entry := range branch {
		entryID := session.EntryID(entry)
		switch typed := entry.(type) {
		case *session.CompactionEntry:
			if err := validateBackwardReference(index, i, entryID, "compaction anchor", typed.FirstKeptEntryID); err != nil {
				return err
			}
		case *session.BranchSummaryEntry:
			if err := validateBackwardReference(index, i, entryID, "branch summary source", typed.FromID); err != nil {
				return err
			}
		case *session.LabelEntry:
			if err := validateBackwardReference(index, i, entryID, "label target", typed.TargetID); err != nil {
				return err
			}
		case *session.CustomEntry:
			if typed.CustomType != session.RuntimeProfileCustomType || len(typed.Data) == 0 {
				continue
			}
			anchor, err := forkRuntimeProfileAnchor(typed.Data)
			if err != nil {
				return fmt.Errorf("fork: entry %s: %w", entryID, err)
			}
			if err := validateBackwardReference(index, i, entryID, "runtime profile anchor", anchor); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateBackwardReference(index map[string]int, position int, entryID, kind, reference string) error {
	if reference == "" {
		return nil
	}
	target, ok := index[reference]
	if !ok {
		return fmt.Errorf("fork: entry %s: %s %s is outside the fork prefix", entryID, kind, reference)
	}
	if target >= position {
		return fmt.Errorf("fork: entry %s: %s %s is a forward reference", entryID, kind, reference)
	}
	return nil
}

func forkRuntimeProfileAnchor(data json.RawMessage) (string, error) {
	var profile session.RuntimeProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		return "", fmt.Errorf("runtime profile is malformed: %w", err)
	}
	return profile.EstimatorAnchor.LeafID, nil
}

func validateForkToolSequence(branch []session.Entry) error {
	var history []*agent.Message
	for _, entry := range branch {
		messageEntry, ok := entry.(*session.MessageEntry)
		if !ok {
			continue
		}
		message, err := messageEntry.DecodeMessage()
		if err != nil {
			return fmt.Errorf("fork: entry %s: %w", session.EntryID(entry), err)
		}
		history = append(history, message)
	}
	if err := validateModelHistoryTools(history); err != nil {
		return fmt.Errorf("fork: %w", err)
	}
	return nil
}

func cloneSessionPrefix(target *session.Session, prefix []session.Entry) error {
	if target == nil {
		return errors.New("fork: no target session to clone into")
	}
	mapping := make(map[string]string, len(prefix))
	for _, entry := range prefix {
		oldID := session.EntryID(entry)
		if oldID == "" {
			return errors.New("fork: an entry without an id cannot be cloned")
		}
		cloned, err := cloneEntryForAppend(entry, mapping)
		if err != nil {
			return err
		}
		if err := target.AppendEntry(cloned); err != nil {
			return err
		}
		assigned, err := lastAppendedEntryID(target.Path())
		if err != nil {
			return err
		}
		mapping[oldID] = assigned
	}
	return nil
}

func cloneEntryForAppend(entry session.Entry, mapping map[string]string) (session.Entry, error) {
	switch typed := entry.(type) {
	case *session.MessageEntry:
		return &session.MessageEntry{Message: cloneRaw(typed.Message)}, nil
	case *session.ThinkingLevelChangeEntry:
		return &session.ThinkingLevelChangeEntry{ThinkingLevel: typed.ThinkingLevel}, nil
	case *session.ModelChangeEntry:
		return &session.ModelChangeEntry{Provider: typed.Provider, ModelID: typed.ModelID}, nil
	case *session.CompactionEntry:
		firstKept, err := remapForkReference(mapping, typed.FirstKeptEntryID, "compaction anchor")
		if err != nil {
			return nil, err
		}
		return &session.CompactionEntry{
			Summary:          typed.Summary,
			FirstKeptEntryID: firstKept,
			TokensBefore:     typed.TokensBefore,
			Details:          cloneRaw(typed.Details),
			Usage:            cloneUsage(typed.Usage),
			FromHook:         typed.FromHook,
		}, nil
	case *session.BranchSummaryEntry:
		from, err := remapForkReference(mapping, typed.FromID, "branch summary source")
		if err != nil {
			return nil, err
		}
		return &session.BranchSummaryEntry{
			FromID:   from,
			Summary:  typed.Summary,
			Details:  cloneRaw(typed.Details),
			Usage:    cloneUsage(typed.Usage),
			FromHook: typed.FromHook,
		}, nil
	case *session.LabelEntry:
		targetID, err := remapForkReference(mapping, typed.TargetID, "label target")
		if err != nil {
			return nil, err
		}
		var label *string
		if typed.Label != nil {
			value := *typed.Label
			label = &value
		}
		return &session.LabelEntry{TargetID: targetID, Label: label}, nil
	case *session.CustomEntry:
		data, err := cloneForkCustomData(typed.CustomType, typed.Data, mapping)
		if err != nil {
			return nil, err
		}
		return &session.CustomEntry{CustomType: typed.CustomType, Data: data}, nil
	case *session.CustomMessageEntry:
		return &session.CustomMessageEntry{
			CustomType: typed.CustomType,
			Content:    cloneRaw(typed.Content),
			Display:    typed.Display,
			Details:    cloneRaw(typed.Details),
		}, nil
	case *session.SessionInfoEntry:
		var name *string
		if typed.Name != nil {
			value := *typed.Name
			name = &value
		}
		return &session.SessionInfoEntry{Name: name}, nil
	default:
		return nil, fmt.Errorf("fork: entry type %s cannot be cloned", entry.EntryType())
	}
}

func remapForkReference(mapping map[string]string, reference, kind string) (string, error) {
	if reference == "" {
		return "", nil
	}
	remapped, ok := mapping[reference]
	if !ok || remapped == "" {
		return "", fmt.Errorf("fork: %s %s is outside the fork prefix", kind, reference)
	}
	return remapped, nil
}

func cloneForkCustomData(customType string, data json.RawMessage, mapping map[string]string) (json.RawMessage, error) {
	if customType != session.RuntimeProfileCustomType || len(data) == 0 {
		return cloneRaw(data), nil
	}
	anchor, err := forkRuntimeProfileAnchor(data)
	if err != nil {
		return nil, err
	}
	if anchor == "" {
		return cloneRaw(data), nil
	}
	var profile session.RuntimeProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		return nil, fmt.Errorf("fork: runtime profile is malformed: %w", err)
	}
	remapped, err := remapForkReference(mapping, anchor, "runtime profile anchor")
	if err != nil {
		return nil, err
	}
	profile.EstimatorAnchor.LeafID = remapped
	encoded, err := json.Marshal(profile)
	if err != nil {
		return nil, fmt.Errorf("fork: marshal runtime profile: %w", err)
	}
	return encoded, nil
}

func lastAppendedEntryID(path string) (string, error) {
	loader, err := session.LoadWithOptions(path, session.LoadOptions{Strict: true})
	if err != nil {
		return "", err
	}
	leaf := loader.Leaf()
	if leaf == nil {
		return "", errors.New("fork: cloned session lost its entries")
	}
	return session.EntryID(leaf), nil
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

func cloneUsage(usage *agent.Usage) *agent.Usage {
	if usage == nil {
		return nil
	}
	cloned := *usage
	return &cloned
}
