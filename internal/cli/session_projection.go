package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/internal/tui/interactive"
)

type sessionProjection struct {
	loader     *session.Loader
	tree       []interactive.TreeBrowserNode
	transcript []interactive.TranscriptEntry
	history    []*agent.Message
	entryIDs   []string
	warnings   []string
	name       string
	leafID     string
}

func projectSession(loader *session.Loader) (*sessionProjection, error) {
	if loader == nil {
		return &sessionProjection{}, nil
	}
	history, entryIDs, warnings, err := projectModelHistoryWithIDs(loader)
	if err != nil {
		return nil, err
	}
	transcript, transcriptWarnings := projectTranscript(loader)
	tree, treeWarnings := buildTreeNodes(loader, false, false)
	projection := &sessionProjection{
		loader:     loader,
		tree:       tree,
		transcript: transcript,
		history:    history,
		entryIDs:   entryIDs,
		name:       sessionDisplayName(loader),
	}
	projection.warnings = append(projection.warnings, warnings...)
	projection.warnings = append(projection.warnings, transcriptWarnings...)
	projection.warnings = append(projection.warnings, treeWarnings...)
	if leaf := loader.Leaf(); leaf != nil {
		projection.leafID = session.EntryID(leaf)
	}
	return projection, nil
}

type entryLabel struct {
	text      string
	timestamp string
}

func buildTreeNodes(loader *session.Loader, deletable, renamable bool) ([]interactive.TreeBrowserNode, []string) {
	entries := loader.Entries()
	labels := map[string]entryLabel{}
	for _, entry := range entries {
		label, ok := entry.(*session.LabelEntry)
		if !ok {
			continue
		}
		if label.TargetID == "" {
			continue
		}
		if label.Label == nil || strings.TrimSpace(*label.Label) == "" {
			delete(labels, label.TargetID)
			continue
		}
		_, _, timestamp := sessionEntryEnvelope(entry)
		labels[label.TargetID] = entryLabel{text: *label.Label, timestamp: timestamp}
	}
	active := map[string]bool{}
	if branch, err := loader.ActiveBranch(); err == nil {
		for _, entry := range branch {
			active[session.EntryID(entry)] = true
		}
	}
	leafID := ""
	if leaf := loader.Leaf(); leaf != nil {
		leafID = session.EntryID(leaf)
	}

	var nodes []interactive.TreeBrowserNode
	var warnings []string
	visited := map[string]bool{}
	onPath := map[string]bool{}

	var walk func(entry session.Entry, depth int)
	walk = func(entry session.Entry, depth int) {
		id := session.EntryID(entry)
		if id == "" {
			warnings = append(warnings, "entry without an id omitted from the tree")
			return
		}
		if onPath[id] {
			warnings = append(warnings, "cycle detected at entry "+id)
			return
		}
		if visited[id] {
			return
		}
		visited[id] = true
		onPath[id] = true
		node := treeNodeFor(entry, depth, labels, active, leafID, deletable, renamable)
		if _, parentID, _ := sessionEntryEnvelope(entry); parentID != nil {
			if _, ok := loader.Get(*parentID); !ok {
				node.Corrupt = true
			}
		}
		nodes = append(nodes, node)
		for _, child := range loader.Children(id) {
			walk(child, depth+1)
		}
		delete(onPath, id)
	}

	for _, root := range loader.Roots() {
		walk(root, 0)
	}
	for _, entry := range entries {
		id := session.EntryID(entry)
		if id == "" || visited[id] {
			continue
		}
		visited[id] = true
		warnings = append(warnings, "unreachable entry "+id+" (cycle or missing parent)")
		node := treeNodeFor(entry, 0, labels, active, leafID, deletable, renamable)
		node.Corrupt = true
		nodes = append(nodes, node)
	}
	return nodes, warnings
}

func treeNodeFor(entry session.Entry, depth int, labels map[string]entryLabel, active map[string]bool, leafID string, deletable, renamable bool) interactive.TreeBrowserNode {
	id := session.EntryID(entry)
	_, parentID, timestamp := sessionEntryEnvelope(entry)
	node := interactive.TreeBrowserNode{
		ID:        id,
		Depth:     depth,
		Kind:      treeEntryKind(entry),
		Timestamp: timestamp,
		Preview:   entryPreviewText(entry),
		Active:    active[id],
		Leaf:      leafID != "" && id == leafID,
		Deletable: deletable,
		Renamable: renamable,
	}
	if custom, ok := entry.(*session.CustomEntry); ok {
		node.CustomType = custom.CustomType
	}
	if customMessage, ok := entry.(*session.CustomMessageEntry); ok {
		node.CustomType = customMessage.CustomType
		node.Display = customMessage.Display
	}
	if parentID != nil {
		node.ParentID = *parentID
	}
	if label, ok := labels[id]; ok {
		node.Label = label.text
		node.LabelTimestamp = label.timestamp
	}
	return node
}

func sessionEntryEnvelope(entry session.Entry) (id string, parentID *string, timestamp string) {
	switch typed := entry.(type) {
	case *session.MessageEntry:
		return typed.ID, typed.ParentID, typed.Timestamp
	case *session.ThinkingLevelChangeEntry:
		return typed.ID, typed.ParentID, typed.Timestamp
	case *session.ModelChangeEntry:
		return typed.ID, typed.ParentID, typed.Timestamp
	case *session.CompactionEntry:
		return typed.ID, typed.ParentID, typed.Timestamp
	case *session.BranchSummaryEntry:
		return typed.ID, typed.ParentID, typed.Timestamp
	case *session.CustomEntry:
		return typed.ID, typed.ParentID, typed.Timestamp
	case *session.CustomMessageEntry:
		return typed.ID, typed.ParentID, typed.Timestamp
	case *session.LabelEntry:
		return typed.ID, typed.ParentID, typed.Timestamp
	case *session.SessionInfoEntry:
		return typed.ID, typed.ParentID, typed.Timestamp
	case *session.OpaqueEntry:
		return typed.EnvelopeID(), typed.EnvelopeParentID(), typed.EnvelopeTimestamp()
	default:
		return session.EntryID(entry), nil, ""
	}
}

func treeEntryKind(entry session.Entry) string {
	switch typed := entry.(type) {
	case *session.MessageEntry:
		switch typed.MessageRole() {
		case string(agent.RoleUser):
			return "user"
		case string(agent.RoleAssistant):
			return "assistant"
		case string(agent.RoleToolResult):
			return "tool"
		default:
			return "message"
		}
	case *session.CompactionEntry:
		return "compaction"
	case *session.BranchSummaryEntry:
		return "summary"
	case *session.CustomMessageEntry:
		if typed.Display {
			return "custom-message"
		}
		return "custom-hidden"
	case *session.CustomEntry:
		switch typed.CustomType {
		case sessionProvenanceCustomType:
			return "provenance"
		case session.RuntimeProfileCustomType:
			return "profile"
		default:
			return "custom"
		}
	case *session.LabelEntry:
		return "label"
	case *session.SessionInfoEntry:
		return "info"
	case *session.ModelChangeEntry:
		return "model"
	case *session.ThinkingLevelChangeEntry:
		return "thinking"
	default:
		return entry.EntryType()
	}
}

func sessionDisplayName(loader *session.Loader) string {
	name := ""
	for _, entry := range loader.Entries() {
		info, ok := entry.(*session.SessionInfoEntry)
		if !ok || info.Name == nil {
			continue
		}
		name = strings.TrimSpace(*info.Name)
	}
	return name
}

func entryPreviewText(entry session.Entry) string {
	switch typed := entry.(type) {
	case *session.MessageEntry:
		message, err := typed.DecodeMessage()
		if err != nil {
			return ""
		}
		switch {
		case message.User != nil:
			return userContentText(message.User.Content)
		case message.Assistant != nil:
			return assistantContentText(message.Assistant.Content)
		case message.ToolResult != nil:
			return blocksText(message.ToolResult.Content)
		}
		return ""
	case *session.CompactionEntry:
		return typed.Summary
	case *session.BranchSummaryEntry:
		return typed.Summary
	case *session.CustomMessageEntry:
		return rawContentText(typed.Content)
	case *session.CustomEntry:
		return string(typed.Data)
	case *session.SessionInfoEntry:
		if typed.Name != nil {
			return *typed.Name
		}
		return ""
	case *session.ModelChangeEntry:
		return typed.Provider + "/" + typed.ModelID
	case *session.ThinkingLevelChangeEntry:
		return typed.ThinkingLevel
	default:
		return ""
	}
}

func userContentText(raw json.RawMessage) string {
	var plain string
	if err := json.Unmarshal(raw, &plain); err == nil {
		return plain
	}
	var blocks []agent.ContentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		return blocksText(blocks)
	}
	return ""
}

func rawContentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var plain string
	if err := json.Unmarshal(raw, &plain); err == nil {
		return plain
	}
	var blocks []agent.ContentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		return blocksText(blocks)
	}
	return string(raw)
}

func blocksText(blocks []agent.ContentBlock) string {
	var builder strings.Builder
	for _, block := range blocks {
		switch block.Type {
		case agent.BlockTypeText:
			builder.WriteString(block.Text)
		case agent.BlockTypeThinking:
			builder.WriteString(block.Thinking)
		}
	}
	return builder.String()
}

func assistantContentText(blocks []agent.ContentBlock) string {
	return blocksText(blocks)
}

func latestBranchCompaction(branch []session.Entry) (*session.CompactionEntry, int) {
	var latest *session.CompactionEntry
	latestIndex := -1
	for i, entry := range branch {
		if compaction, ok := entry.(*session.CompactionEntry); ok {
			latest = compaction
			latestIndex = i
		}
	}
	return latest, latestIndex
}

func unresolvedLatestCompactionAnchor(branch []session.Entry) string {
	latest, latestIndex := latestBranchCompaction(branch)
	if latest == nil {
		return ""
	}
	anchor := latest.FirstKeptEntryID
	if anchor == "" {
		return ""
	}
	for i := 0; i < latestIndex; i++ {
		if session.EntryID(branch[i]) == anchor {
			return ""
		}
	}
	return anchor
}

func projectModelHistory(loader *session.Loader) ([]*agent.Message, []string, error) {
	history, _, warnings, err := projectModelHistoryWithIDs(loader)
	if err != nil {
		return nil, nil, err
	}
	return history, warnings, nil
}

func contextUserMessage(text string) *agent.Message {
	raw, _ := json.Marshal(text)
	return &agent.Message{User: &agent.UserMessage{Role: string(agent.RoleUser), Content: raw}}
}

func compactionHistoryMessage(entry *session.CompactionEntry) *agent.Message {
	id := session.EntryID(entry)
	text := "[compaction " + id + "] " + entry.Summary + " (tokensBefore=" + itoa(entry.TokensBefore) + " firstKept=" + entry.FirstKeptEntryID + ")"
	return contextUserMessage(text)
}

func branchSummaryHistoryMessage(entry *session.BranchSummaryEntry) *agent.Message {
	id := session.EntryID(entry)
	text := "[branch summary " + id + " from " + entry.FromID + "] " + entry.Summary
	return contextUserMessage(text)
}

func customHistoryMessage(entry *session.CustomMessageEntry) (*agent.Message, error) {
	id := session.EntryID(entry)
	if len(entry.Content) == 0 {
		return nil, fmt.Errorf("session: entry %s custom message has empty content", id)
	}
	if !json.Valid(entry.Content) {
		return nil, fmt.Errorf("session: entry %s custom message has invalid content", id)
	}
	text := rawContentText(entry.Content)
	if strings.TrimSpace(text) == "" {
		text = strings.TrimSpace(string(entry.Content))
	}
	full := "[custom " + entry.CustomType + " " + id + "] " + text
	return contextUserMessage(full), nil
}

func itoa(n int64) string {
	return fmt.Sprintf("%d", n)
}

func validateModelHistoryTools(history []*agent.Message) error {
	calls := map[string]string{}
	results := map[string]struct{}{}
	for _, msg := range history {
		if msg == nil {
			continue
		}
		if msg.Assistant != nil {
			for _, block := range msg.Assistant.Content {
				if block.Type != agent.BlockTypeToolCall {
					continue
				}
				if strings.TrimSpace(block.ID) == "" {
					return fmt.Errorf("session: tool call without id")
				}
				if _, dup := calls[block.ID]; dup {
					return fmt.Errorf("session: duplicate tool call %q", block.ID)
				}
				calls[block.ID] = block.Name
			}
		}
		if msg.ToolResult != nil {
			callID := msg.ToolResult.ToolCallID
			if strings.TrimSpace(callID) == "" {
				return fmt.Errorf("session: tool result without tool call id")
			}
			callName, ok := calls[callID]
			if !ok {
				return fmt.Errorf("session: orphan tool result %q has no matching tool call", callID)
			}
			if _, dup := results[callID]; dup {
				return fmt.Errorf("session: duplicate tool result %q", callID)
			}
			if msg.ToolResult.ToolName != "" && callName != "" && msg.ToolResult.ToolName != callName {
				return fmt.Errorf("session: unmatched tool result %q for tool call %q", msg.ToolResult.ToolName, callID)
			}
			results[callID] = struct{}{}
		}
	}
	for callID := range calls {
		if _, ok := results[callID]; !ok {
			return fmt.Errorf("session: pending tool call %q has no recorded result", callID)
		}
	}
	return nil
}

func projectModelHistoryWithIDs(loader *session.Loader) ([]*agent.Message, []string, []string, error) {
	if loader == nil {
		return nil, nil, nil, nil
	}
	contextEntries, err := loader.BuildContextEntries()
	if err != nil {
		return nil, nil, nil, err
	}
	branch, err := loader.ActiveBranch()
	if err != nil {
		return nil, nil, nil, err
	}
	var warnings []string
	if anchor := unresolvedLatestCompactionAnchor(branch); anchor != "" {
		warnings = append(warnings, "compaction anchor "+anchor+" is unresolved; using uncompacted active-branch recovery with fresh context")
		contextEntries = branch
	}
	var history []*agent.Message
	var entryIDs []string
	for _, entry := range contextEntries {
		id := session.EntryID(entry)
		switch typed := entry.(type) {
		case *session.MessageEntry:
			message, err := typed.DecodeMessage()
			if err != nil {
				return nil, nil, nil, fmt.Errorf("session: entry %s: %w", id, err)
			}
			if isProviderErrorAssistant(message) {
				continue
			}
			history = append(history, message)
			entryIDs = append(entryIDs, id)
		case *session.CompactionEntry:
			history = append(history, compactionHistoryMessage(typed))
			entryIDs = append(entryIDs, id)
		case *session.BranchSummaryEntry:
			history = append(history, branchSummaryHistoryMessage(typed))
			entryIDs = append(entryIDs, id)
		case *session.CustomMessageEntry:
			message, err := customHistoryMessage(typed)
			if err != nil {
				return nil, nil, nil, err
			}
			history = append(history, message)
			entryIDs = append(entryIDs, id)
		case *session.CustomEntry:
			continue
		case *session.LabelEntry:
			continue
		case *session.SessionInfoEntry:
			continue
		case *session.ThinkingLevelChangeEntry:
			continue
		case *session.ModelChangeEntry:
			continue
		case *session.OpaqueEntry:
			return nil, nil, nil, fmt.Errorf("session: entry %s has unsupported type %s", id, typed.TypeName)
		default:
			return nil, nil, nil, fmt.Errorf("session: entry %s has unsupported type %s", id, entry.EntryType())
		}
	}
	if err := validateModelHistoryTools(history); err != nil {
		return nil, nil, nil, err
	}
	return history, entryIDs, warnings, nil
}

func isProviderErrorAssistant(message *agent.Message) bool {
	if message == nil || message.Assistant == nil {
		return false
	}
	if message.Assistant.StopReason != "error" {
		return false
	}
	for _, block := range message.Assistant.Content {
		if block.Type == agent.BlockTypeToolCall {
			return false
		}
	}
	return true
}

func projectTranscript(loader *session.Loader) ([]interactive.TranscriptEntry, []string) {
	branch, err := loader.ActiveBranch()
	if err != nil {
		return nil, []string{"active branch could not be read: " + err.Error()}
	}
	resolvable := map[string]bool{}
	for _, entry := range branch {
		resolvable[session.EntryID(entry)] = true
	}
	var warnings []string
	var items []interactive.TranscriptEntry
	toolItems := map[string]int{}
	for _, entry := range branch {
		switch typed := entry.(type) {
		case *session.MessageEntry:
			message, err := typed.DecodeMessage()
			if err != nil {
				warnings = append(warnings, "message entry "+session.EntryID(entry)+" could not be decoded")
				continue
			}
			switch {
			case message.User != nil:
				items = append(items, interactive.TranscriptEntry{
					Kind:      interactive.ReplayUser,
					Text:      userContentText(message.User.Content),
					Timestamp: typed.Timestamp,
				})
			case message.Assistant != nil:
				parts := assistantParts(message.Assistant.Content)
				items = append(items, interactive.TranscriptEntry{
					Kind:         interactive.ReplayAssistant,
					Parts:        parts,
					StopReason:   message.Assistant.StopReason,
					ErrorMessage: message.Assistant.ErrorMessage,
					Timestamp:    typed.Timestamp,
				})
				for _, block := range message.Assistant.Content {
					if block.Type != agent.BlockTypeToolCall {
						continue
					}
					toolItems[block.ID] = len(items)
					items = append(items, interactive.TranscriptEntry{
						Kind:      interactive.ReplayTool,
						ToolName:  block.Name,
						ToolArgs:  block.Arguments,
						Pending:   true,
						Timestamp: typed.Timestamp,
					})
				}
			case message.ToolResult != nil:
				index, ok := toolItems[message.ToolResult.ToolCallID]
				if !ok {
					items = append(items, interactive.TranscriptEntry{
						Kind:       interactive.ReplayTool,
						ToolName:   message.ToolResult.ToolName,
						ToolOutput: blocksText(message.ToolResult.Content),
						ToolError:  message.ToolResult.IsError,
						Pending:    true,
						Timestamp:  typed.Timestamp,
					})
					warnings = append(warnings, "orphan tool result "+message.ToolResult.ToolCallID+" has no matching tool call")
					continue
				}
				items[index].ToolOutput = blocksText(message.ToolResult.Content)
				items[index].ToolError = message.ToolResult.IsError
				items[index].Pending = false
			}
		case *session.CompactionEntry:
			if typed.FirstKeptEntryID != "" && !resolvable[typed.FirstKeptEntryID] {
				warnings = append(warnings, "compaction anchor "+typed.FirstKeptEntryID+" is unresolved; the summary is replayed without its kept prefix")
			}
			items = append(items, interactive.TranscriptEntry{
				Kind:         interactive.ReplayCompaction,
				Summary:      typed.Summary,
				TokensBefore: typed.TokensBefore,
				Timestamp:    typed.Timestamp,
			})
		case *session.BranchSummaryEntry:
			items = append(items, interactive.TranscriptEntry{
				Kind:      interactive.ReplayCompaction,
				Summary:   typed.Summary,
				Timestamp: typed.Timestamp,
			})
		case *session.CustomEntry:
			if typed.CustomType == session.RuntimeProfileCustomType || typed.CustomType == sessionProvenanceCustomType {
				continue
			}
			items = append(items, interactive.TranscriptEntry{
				Kind:        interactive.ReplayCustom,
				CustomType:  typed.CustomType,
				CustomLabel: typed.CustomType,
				Text:        string(typed.Data),
				Timestamp:   typed.Timestamp,
			})
		case *session.CustomMessageEntry:
			if !typed.Display {
				continue
			}
			items = append(items, interactive.TranscriptEntry{
				Kind:        interactive.ReplayCustom,
				CustomType:  typed.CustomType,
				CustomLabel: "custom message",
				Text:        rawContentText(typed.Content),
				Timestamp:   typed.Timestamp,
			})
		case *session.ThinkingLevelChangeEntry:
			items = append(items, interactive.TranscriptEntry{
				Kind:      interactive.ReplayNotice,
				Text:      "thinking level: " + typed.ThinkingLevel,
				Timestamp: typed.Timestamp,
			})
		case *session.ModelChangeEntry:
			items = append(items, interactive.TranscriptEntry{
				Kind:      interactive.ReplayNotice,
				Text:      "historical model change: " + typed.Provider + "/" + typed.ModelID,
				Timestamp: typed.Timestamp,
			})
		case *session.SessionInfoEntry:
			if typed.Name != nil {
				items = append(items, interactive.TranscriptEntry{
					Kind:      interactive.ReplayNotice,
					Text:      "session renamed to " + *typed.Name,
					Timestamp: typed.Timestamp,
				})
			}
		case *session.OpaqueEntry:
			warnings = append(warnings, "unsupported entry type "+typed.TypeName+" is not shown")
		}
	}
	for index := range items {
		if items[index].Kind == interactive.ReplayTool && items[index].Pending {
			warnings = append(warnings, "tool call "+items[index].ToolName+" has no recorded result")
		}
	}
	return items, warnings
}

func assistantParts(blocks []agent.ContentBlock) []interactive.AssistantMessagePart {
	var parts []interactive.AssistantMessagePart
	for _, block := range blocks {
		switch block.Type {
		case agent.BlockTypeThinking:
			parts = append(parts, interactive.AssistantMessagePart{Thinking: true, Text: block.Thinking})
		case agent.BlockTypeText:
			parts = append(parts, interactive.AssistantMessagePart{Text: block.Text})
		}
	}
	return parts
}
