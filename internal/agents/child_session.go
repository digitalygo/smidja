package agents

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/session"
)

const (
	childDefinitionCustomType = "smidja.subagent"
	subagentSessionsDir       = "subagent-sessions"
)

type childSession struct {
	sess *session.Session
	path string
	id   string
}

func (s *childSession) AppendUser(message *agent.UserMessage) error {
	return s.sess.AppendUser(message)
}

func (s *childSession) AppendAssistant(message *agent.AssistantMessage) error {
	return s.sess.AppendAssistant(message)
}

func (s *childSession) AppendToolResult(message *agent.ToolResultMessage) error {
	return s.sess.AppendToolResult(message)
}

func (s *childSession) appendCompaction(entry *agent.CompactionEntry) error {
	if entry == nil {
		return nil
	}
	return s.sess.AppendEntry(&session.CompactionEntry{
		Summary:          string(entry.Summary),
		FirstKeptEntryID: entry.FirstKeptEntryID,
		TokensBefore:     entry.TokensBefore,
	})
}

func (s *childSession) refreshEntryIDs(history []*agent.Message) ([]string, error) {
	_, ids, err := projectChild(s.path)
	if err != nil {
		return nil, err
	}
	if len(ids) != len(history) {
		return nil, fmt.Errorf("agents: %d child entry ids do not align with %d context messages", len(ids), len(history))
	}
	return ids, nil
}

func (s *childSession) close() {
	if s == nil || s.sess == nil {
		return
	}
	s.sess.Close()
}

func (e *Executor) newChildSession(definition Definition, parent Parent) (*childSession, error) {
	dir, err := childSessionDir(e.deps.SessionsRoot, parent.SessionID)
	if err != nil {
		return nil, err
	}
	store, err := session.NewStore(dir)
	if err != nil {
		return nil, err
	}
	candidate, err := store.Create(e.deps.Cwd)
	if err != nil {
		return nil, err
	}
	path := candidate.Path()
	marker, err := json.Marshal(map[string]any{
		"agent":         definition.Name,
		"tier":          string(definition.Tier),
		"depth":         parent.Depth + 1,
		"parentSession": parent.SessionID,
	})
	if err != nil {
		return nil, errors.Join(err, discardChildCandidate(candidate, path, nil))
	}
	if err := candidate.AppendEntry(&session.CustomEntry{CustomType: childDefinitionCustomType, Data: marker}); err != nil {
		return nil, errors.Join(err, discardChildCandidate(candidate, path, nil))
	}
	identity, err := os.Lstat(path)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("agents: inspect the child session %q: %w", path, err), discardChildCandidate(candidate, path, nil))
	}
	if identity.Mode()&os.ModeSymlink != 0 || !identity.Mode().IsRegular() {
		return nil, errors.Join(fmt.Errorf("agents: the child session %q is not a plain file", path), discardChildCandidate(candidate, path, identity))
	}
	if err := candidate.Close(); err != nil {
		return nil, errors.Join(err, discardChildCandidate(nil, path, identity))
	}
	opened, err := store.Open(path, session.OpenOptions{Strict: true})
	if err != nil {
		return nil, errors.Join(err, discardChildCandidate(nil, path, identity))
	}
	return &childSession{sess: opened, path: opened.Path(), id: opened.ID()}, nil
}

func discardChildCandidate(candidate *session.Session, path string, identity os.FileInfo) error {
	var errs []error
	if candidate != nil {
		if err := candidate.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if path == "" {
		return errors.Join(errs...)
	}
	current, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return errors.Join(errs...)
	}
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	if current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() {
		return errors.Join(append(errs, fmt.Errorf("agents: refusing to remove a replaced child session at %q", path))...)
	}
	if identity != nil && !os.SameFile(identity, current) {
		return errors.Join(append(errs, fmt.Errorf("agents: refusing to remove a replaced child session at %q", path))...)
	}
	if err := os.Remove(path); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func childSessionDir(root, parentID string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", errors.New("agents: empty session root")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	base, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	rootInfo, err := os.Stat(base)
	if err != nil {
		return "", err
	}
	if !rootInfo.IsDir() {
		return "", errors.New("agents: the session root is not a directory")
	}
	dir := base
	for _, component := range []string{subagentSessionsDir, safeSessionDir(parentID)} {
		dir = filepath.Join(dir, component)
		info, statErr := os.Lstat(dir)
		if errors.Is(statErr, os.ErrNotExist) {
			if mkdirErr := os.Mkdir(dir, 0o700); mkdirErr != nil && !errors.Is(mkdirErr, os.ErrExist) {
				return "", mkdirErr
			}
			info, statErr = os.Lstat(dir)
		}
		if statErr != nil {
			return "", statErr
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("agents: the subagent session directory escapes the session root: %q is not a plain directory", dir)
		}
		resolved, err := filepath.EvalSymlinks(dir)
		if err != nil {
			return "", err
		}
		prefix := base + string(os.PathSeparator)
		if resolved != base && !strings.HasPrefix(resolved, prefix) {
			return "", errors.New("agents: the subagent session directory escapes the session root")
		}
	}
	return dir, nil
}

func safeSessionDir(id string) string {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return "unknown"
	}
	var out strings.Builder
	for _, r := range trimmed {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			out.WriteRune(r)
		default:
			out.WriteByte('_')
		}
	}
	safe := out.String()
	if safe == "" || safe == "." || safe == ".." {
		return "unknown"
	}
	return safe
}

func projectChild(path string) ([]*agent.Message, []string, error) {
	loader, err := session.LoadWithOptions(path, session.LoadOptions{Strict: true})
	if err != nil {
		return nil, nil, err
	}
	entries, err := loader.BuildContextEntries()
	if err != nil {
		return nil, nil, err
	}
	var history []*agent.Message
	var ids []string
	for _, entry := range entries {
		switch typed := entry.(type) {
		case *session.MessageEntry:
			message, decodeErr := typed.DecodeMessage()
			if decodeErr != nil {
				return nil, nil, decodeErr
			}
			if isProviderErrorAssistant(message) {
				continue
			}
			history = append(history, message)
			ids = append(ids, session.EntryID(entry))
		case *session.CompactionEntry:
			history = append(history, compactionContextMessage(typed))
			ids = append(ids, session.EntryID(entry))
		}
	}
	return history, ids, nil
}

func compactionContextMessage(entry *session.CompactionEntry) *agent.Message {
	text := fmt.Sprintf("[compaction %s] %s (tokensBefore=%d firstKept=%s)", session.EntryID(entry), entry.Summary, entry.TokensBefore, entry.FirstKeptEntryID)
	content, _ := json.Marshal(text)
	return &agent.Message{User: &agent.UserMessage{Role: string(agent.RoleUser), Content: content, Timestamp: agent.NowMillis()}}
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
